#!/bin/bash
# CaddyWeb installer for Linux (systemd): Debian, Ubuntu, DietPi, Raspberry Pi OS, …
#
#   curl -fsSL https://raw.githubusercontent.com/mf-ky/caddy-web-interface/main/scripts/install.sh | sudo bash
#
# Options (environment variables):
#   CADDYWEB_PORT=8090        port for the web UI
#   CADDYWEB_VERSION=v1.0.0   install a specific release instead of the latest
#   CADDYWEB_BINARY=/path     install this binary instead of downloading one
#
# Upgrade: run the same command again (your data is kept).
# Remove:  sudo bash install.sh --uninstall          (keeps /var/lib/caddyweb)
#          sudo bash install.sh --uninstall --purge  (also deletes all CaddyWeb data)
set -euo pipefail

REPO="mf-ky/caddy-web-interface"
BIN=/usr/local/bin/caddyweb
DATA=/var/lib/caddyweb
UNIT=/etc/systemd/system/caddyweb.service
PORT="${CADDYWEB_PORT:-8090}"
USER_NAME=caddyweb

say() { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "Please run with sudo."
command -v systemctl >/dev/null 2>&1 || fail "This installer needs systemd. Use Docker instead (see the README)."

if [ "${1:-}" = "--uninstall" ]; then
	say "Stopping and removing CaddyWeb"
	systemctl disable --now caddyweb 2>/dev/null || true
	rm -f "$UNIT" "$BIN"
	systemctl daemon-reload
	if [ "${2:-}" = "--purge" ]; then
		rm -rf "$DATA"
		say "Deleted $DATA"
	else
		say "Kept your data in $DATA (delete it with --uninstall --purge)"
	fi
	say "Done. Caddy itself was not touched."
	exit 0
fi

case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
armv7l | armv7*) ARCH=armv7 ;;
armv6l | armv6*) ARCH=armv6 ;;
*) fail "Unsupported CPU: $(uname -m)" ;;
esac

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
SRC="${CADDYWEB_BINARY:-}"
if [ -z "$SRC" ] && [ -x "./caddyweb" ]; then SRC="./caddyweb"; fi
if [ -z "$SRC" ]; then
	if [ -n "${CADDYWEB_VERSION:-}" ]; then
		URL="https://github.com/$REPO/releases/download/$CADDYWEB_VERSION/caddyweb-linux-$ARCH"
	else
		URL="https://github.com/$REPO/releases/latest/download/caddyweb-linux-$ARCH"
	fi
	say "Downloading $URL"
	curl -fL --progress-bar -o "$TMP/caddyweb" "$URL" || fail "Download failed. Check your internet connection, or build from source (see README)."
	SRC="$TMP/caddyweb"
fi
chmod +x "$SRC"
"$SRC" version >/dev/null || fail "$SRC does not run on this machine (wrong CPU type?)"

say "Installing $BIN"
systemctl stop caddyweb 2>/dev/null || true
install -m 0755 "$SRC" "$BIN"

say "Creating system user '$USER_NAME' and data folder $DATA"
if ! id "$USER_NAME" >/dev/null 2>&1; then
	useradd --system --home-dir "$DATA" --create-home --shell /usr/sbin/nologin --comment "CaddyWeb" "$USER_NAME"
fi
mkdir -p "$DATA"
chown -R "$USER_NAME:$USER_NAME" "$DATA"
chmod 750 "$DATA"

command -v rsync >/dev/null 2>&1 || say "Tip: install rsync if you want CaddyWeb to copy backups to another machine (sudo apt install rsync)."

say "Installing the systemd service"
cat > "$UNIT" <<EOF
[Unit]
Description=CaddyWeb - front end manager for Caddy Server
Documentation=https://github.com/$REPO
After=network-online.target
Wants=network-online.target

[Service]
User=$USER_NAME
Group=$USER_NAME
Environment=CADDYWEB_DATA=$DATA
Environment=CADDYWEB_LISTEN=:$PORT
ExecStart=$BIN serve
Restart=on-failure
RestartSec=3
NoNewPrivileges=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now caddyweb

IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
echo
say "CaddyWeb is running!"
echo "    Open  http://${IP:-this-machine}:$PORT  in your browser and create your admin account."
echo
echo "    Useful commands:"
echo "      sudo systemctl status caddyweb        # is it running?"
echo "      sudo journalctl -u caddyweb -f        # logs"
echo "      sudo caddyweb user passwd <name>      # reset a forgotten password"
echo "      sudo caddyweb user list               # list accounts"
