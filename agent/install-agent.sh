#!/bin/bash
# CaddyWeb agent installer — run this ON YOUR CADDY SERVER with sudo.
#
# What it does (and nothing more):
#   1. creates a locked-down system user "caddyweb" (no password, cannot log in
#      with a shell)
#   2. installs /usr/local/bin/caddyweb-agent, the only command that user's SSH
#      key is allowed to run
#   3. lets that user edit the Caddyfile and store backups in the backup folder
#   4. authorizes CaddyWeb's public SSH key, locked to the agent
#
# Undo everything with:   sudo bash install-agent.sh --uninstall
# Caddy keeps working either way; your Caddyfile and backups are never removed.
set -euo pipefail

PUBKEY='__PUBLIC_KEY__'
AGENT_USER=caddyweb
AGENT_HOME=/var/lib/caddyweb-agent
AGENT_BIN=/usr/local/bin/caddyweb-agent
CADDYFILE="${CADDYFILE:-/etc/caddy/Caddyfile}"
BACKUP_DIR="${BACKUP_DIR:-/etc/caddy/backups}"

say() { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mWARNING:\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "Please run with sudo:  sudo bash $0"

# If the user already exists (e.g. CaddyWeb itself runs on this machine as
# "caddyweb"), reuse it and its home directory.
if id "$AGENT_USER" >/dev/null 2>&1; then
	AGENT_HOME="$(getent passwd "$AGENT_USER" | cut -d: -f6)"
fi

if [ "${1:-}" = "--uninstall" ]; then
	say "Removing the CaddyWeb agent"
	if [ -f "$AGENT_HOME/original-perms" ]; then
		read -r owner mode < "$AGENT_HOME/original-perms" || true
		[ -n "${owner:-}" ] && chown "$owner" "$CADDYFILE" && chmod "$mode" "$CADDYFILE" && say "Restored $CADDYFILE ownership to $owner ($mode)"
	fi
	if [ -f "$AGENT_HOME/.created-by-caddyweb-agent" ]; then
		userdel -r "$AGENT_USER" 2>/dev/null || true
	else
		# the user belongs to a CaddyWeb installation on this machine: only revoke the key
		rm -f "$AGENT_HOME/.ssh/authorized_keys" "$AGENT_HOME/original-perms"
	fi
	rm -f "$AGENT_BIN" /etc/caddyweb-agent.conf
	say "Done. Caddy, your Caddyfile and the backups in $BACKUP_DIR were left untouched."
	exit 0
fi

case "$PUBKEY" in
ssh-*) ;;
*) fail "This installer has no public key in it. Download it from CaddyWeb (Settings → Connection)." ;;
esac

command -v caddy >/dev/null 2>&1 || fail "caddy was not found on this machine. Install the agent on the server that runs Caddy."
[ -f "$CADDYFILE" ] || fail "No Caddyfile at $CADDYFILE. Re-run with CADDYFILE=/path/to/Caddyfile sudo -E bash $0"

say "Setting up system user '$AGENT_USER'"
if ! id "$AGENT_USER" >/dev/null 2>&1; then
	useradd --system --home-dir "$AGENT_HOME" --create-home --shell /bin/sh --comment "CaddyWeb agent" "$AGENT_USER"
	touch "$AGENT_HOME/.created-by-caddyweb-agent"
else
	# SSH runs the forced command through the user's shell, so it can't be nologin.
	case "$(getent passwd "$AGENT_USER" | cut -d: -f7)" in
	*nologin | */false) usermod -s /bin/sh "$AGENT_USER" ;;
	esac
fi
# '*' = no password at all (key login only). A '!' lock would make some SSH
# servers refuse the key as well.
usermod -p '*' "$AGENT_USER"
mkdir -p "$AGENT_HOME"
chown "$AGENT_USER:$AGENT_USER" "$AGENT_HOME"
chmod 750 "$AGENT_HOME"

say "Installing $AGENT_BIN"
cat > "$AGENT_BIN" <<'CADDYWEB_AGENT_EOF'
__AGENT_SCRIPT__
CADDYWEB_AGENT_EOF
chown root:root "$AGENT_BIN"
chmod 755 "$AGENT_BIN"

if [ "$CADDYFILE" != /etc/caddy/Caddyfile ] || [ "$BACKUP_DIR" != /etc/caddy/backups ]; then
	printf 'CADDYFILE=%q\nBACKUP_DIR=%q\n' "$CADDYFILE" "$BACKUP_DIR" > /etc/caddyweb-agent.conf
	chmod 644 /etc/caddyweb-agent.conf
fi

say "Giving '$AGENT_USER' write access to $CADDYFILE"
[ -f "$AGENT_HOME/original-perms" ] || stat -c '%U:%G %a' "$CADDYFILE" > "$AGENT_HOME/original-perms"
chgrp "$AGENT_USER" "$CADDYFILE"
chmod g+rw "$CADDYFILE"

say "Creating backup folder $BACKUP_DIR"
mkdir -p "$BACKUP_DIR"
chown "$AGENT_USER:$AGENT_USER" "$BACKUP_DIR"
chmod 750 "$BACKUP_DIR"

say "Authorizing CaddyWeb's SSH key (locked to the agent)"
mkdir -p "$AGENT_HOME/.ssh"
# These options work with both OpenSSH and Dropbear (DietPi's default).
echo "command=\"$AGENT_BIN\",no-port-forwarding,no-agent-forwarding,no-X11-forwarding,no-pty $PUBKEY" > "$AGENT_HOME/.ssh/authorized_keys"
chown -R "$AGENT_USER:$AGENT_USER" "$AGENT_HOME/.ssh"
chmod 700 "$AGENT_HOME/.ssh"
chmod 600 "$AGENT_HOME/.ssh/authorized_keys"

if command -v sshd >/dev/null 2>&1; then
	allow="$(sshd -T 2>/dev/null | awk 'tolower($1)=="allowusers"{ $1=""; print }' || true)"
	if [ -n "$allow" ] && ! echo " $allow " | grep -q " $AGENT_USER "; then
		warn "Your SSH server only allows these users:$allow"
		warn "Add '$AGENT_USER' to AllowUsers in /etc/ssh/sshd_config and restart ssh."
	fi
fi

say "Testing the agent"
if ! runuser -u "$AGENT_USER" -- env HOME="$AGENT_HOME" "$AGENT_BIN" info | sed 's/^/    /'; then
	warn "The agent test failed; see the output above."
fi

echo
say "All done! Now go back to CaddyWeb and press 'Test connection'."
echo "    CaddyWeb will show this server's SSH fingerprint. It should match one of these:"
for k in /etc/ssh/ssh_host_ed25519_key.pub /etc/ssh/ssh_host_ecdsa_key.pub /etc/ssh/ssh_host_rsa_key.pub; do
	[ -f "$k" ] && ssh-keygen -lf "$k" 2>/dev/null | sed 's/^/    /'
done
if command -v dropbearkey >/dev/null 2>&1; then
	for k in /etc/dropbear/dropbear_ed25519_host_key /etc/dropbear/dropbear_ecdsa_host_key /etc/dropbear/dropbear_rsa_host_key; do
		[ -f "$k" ] && dropbearkey -y -f "$k" 2>/dev/null | grep -i fingerprint | sed 's/^/    /'
	done
fi
exit 0
