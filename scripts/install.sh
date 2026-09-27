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
# Upgrade: run the same command again (your data and port are kept; put other
#          customisations in `sudo systemctl edit caddyweb`, which survives upgrades).
# Remove:  curl -fsSL …/install.sh | sudo bash -s -- --uninstall          (keeps /var/lib/caddyweb)
#          curl -fsSL …/install.sh | sudo bash -s -- --uninstall --purge  (also deletes all CaddyWeb data)
set -euo pipefail

REPO="mf-ky/caddy-web-interface"
BIN=/usr/local/bin/caddyweb
DATA=/var/lib/caddyweb
UNIT=/etc/systemd/system/caddyweb.service
# keep the port of an existing installation unless a new one is given
OLD_PORT="$(sed -n 's/^Environment=CADDYWEB_LISTEN=.*:\([0-9]*\)$/\1/p' /etc/systemd/system/caddyweb.service 2>/dev/null | head -n1)"
PORT="${CADDYWEB_PORT:-${OLD_PORT:-8090}}"
USER_NAME=caddyweb

say() { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "Please run with sudo."
command -v systemctl >/dev/null 2>&1 || fail "This installer needs systemd. Use Docker instead (see the README)."

if [ "${1:-}" = "--uninstall" ]; then
	HOME_DIR="$(getent passwd "$USER_NAME" | cut -d: -f6 || true)"
	if [ "${2:-}" = "--purge" ] && [ -n "$HOME_DIR" ] && [ -f "$HOME_DIR/.ssh/authorized_keys" ]; then
		fail "The CaddyWeb agent is also installed on this machine (it uses $HOME_DIR). Remove the agent first — curl -fsSL http://localhost:$PORT/agent/install.sh | sudo bash -s -- --uninstall — then run this again."
	fi
	say "Stopping and removing CaddyWeb"
	systemctl disable --now caddyweb 2>/dev/null || true
	rm -f "$UNIT" "$BIN"
	rm -rf /etc/systemd/system/caddyweb.service.d
	systemctl daemon-reload
	if [ "${2:-}" = "--purge" ]; then
		rm -rf "$DATA"
		userdel "$USER_NAME" 2>/dev/null || true
		say "Deleted $DATA and the $USER_NAME user"
	else
		say "Kept your data in $DATA (delete it with --uninstall --purge)"
	fi
	say "Done. Caddy itself was not touched."
	exit 0
fi

case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
armv8l | armv7l | armv7*) ARCH=armv7 ;;
armv6l | armv6*) ARCH=armv6 ;;
*) fail "Unsupported CPU: $(uname -m)" ;;
esac
# A 64-bit kernel with a 32-bit system (common on Raspberry Pi OS) needs the 32-bit build.
if [ "$ARCH" = arm64 ] && [ "$(getconf LONG_BIT 2>/dev/null || echo 64)" = 32 ]; then ARCH=armv7; fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
SRC="${CADDYWEB_BINARY:-}"
if [ -z "$SRC" ]; then
	if [ -n "${CADDYWEB_VERSION:-}" ]; then
		BASE="https://github.com/$REPO/releases/download/$CADDYWEB_VERSION"
	else
		BASE="https://github.com/$REPO/releases/latest/download"
	fi
	say "Downloading $BASE/caddyweb-linux-$ARCH"
	if ! curl -fL --progress-bar -o "$TMP/caddyweb-linux-$ARCH" "$BASE/caddyweb-linux-$ARCH"; then
		fail "Download failed. Either there is no published release for $ARCH yet (see https://github.com/$REPO/releases), or this machine can't reach GitHub. Alternatives: Docker, or build from source (docs/INSTALL.md)."
	fi
	if curl -fsSL -o "$TMP/SHA256SUMS" "$BASE/SHA256SUMS"; then
		(cd "$TMP" && grep " caddyweb-linux-$ARCH\$" SHA256SUMS | sha256sum -c --quiet -) || fail "The download is corrupted (checksum mismatch). Please try again."
	fi
	SRC="$TMP/caddyweb-linux-$ARCH"
fi
chmod +x "$SRC"
"$SRC" version >/dev/null || fail "$SRC does not run on this machine (wrong CPU type?)"

say "Installing $BIN"
systemctl stop caddyweb 2>/dev/null || true
install -m 0755 "$SRC" "$BIN"

say "Creating system user '$USER_NAME' and data folder $DATA"
if ! id "$USER_NAME" >/dev/null 2>&1; then
	useradd --system --home-dir "$DATA" --create-home --shell /usr/sbin/nologin --comment "CaddyWeb" "$USER_NAME"
else
	# The agent installer may have created this user first; it now belongs to
	# CaddyWeb too, so the agent's uninstaller must not delete it.
	rm -f "$(getent passwd "$USER_NAME" | cut -d: -f6)/.created-by-caddyweb-agent"
fi
mkdir -p "$DATA"
chown -R "$USER_NAME:$USER_NAME" "$DATA"
chmod 750 "$DATA"

command -v rsync >/dev/null 2>&1 && command -v ssh >/dev/null 2>&1 || say "Tip: to let CaddyWeb copy backups to another machine, install rsync and an SSH client (sudo apt install rsync openssh-client)."

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
echo "    Customise (port, HTTPS…) without losing it on upgrades:  sudo systemctl edit caddyweb"
echo "      e.g.  [Service]"
echo "            Environment=CADDYWEB_TLS_CERT=/path/cert.pem CADDYWEB_TLS_KEY=/path/key.pem"
echo
echo "    Useful commands:"
echo "      sudo systemctl status caddyweb        # is it running?"
echo "      sudo journalctl -u caddyweb -f        # logs"
echo "      sudo caddyweb user passwd <name>      # reset a forgotten password"
echo "      sudo caddyweb user list               # list accounts"
