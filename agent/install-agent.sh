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
# Undo everything with:   curl -fsSL <caddyweb>/agent/install.sh | sudo bash -s -- --uninstall
# Caddy keeps working either way; your Caddyfile and backups are never removed.
set -euo pipefail

PUBKEY='__PUBLIC_KEY__'
AGENT_USER=caddyweb
AGENT_HOME=/var/lib/caddyweb-agent
AGENT_BIN=/usr/local/bin/caddyweb-agent
CONF=/etc/caddyweb-agent.conf
# root-only record of what we changed, so --uninstall can undo it
STATE=/var/lib/caddyweb-agent-install

say() { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mWARNING:\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "Please run it with sudo (… | sudo bash)."

# Paths from an earlier install win, then the environment, then defaults.
if [ -r "$CONF" ]; then
	# shellcheck disable=SC1090
	. "$CONF"
fi
CADDYFILE="${CADDYFILE:-/etc/caddy/Caddyfile}"
BACKUP_DIR="${BACKUP_DIR:-/etc/caddy/backups}"
STAGED="$CADDYFILE.caddyweb-staged"

# If the user already exists (e.g. CaddyWeb itself runs on this machine as
# "caddyweb"), reuse it and its home directory.
if id "$AGENT_USER" >/dev/null 2>&1; then
	AGENT_HOME="$(getent passwd "$AGENT_USER" | cut -d: -f6)"
fi
mkdir -p "$STATE"
chmod 700 "$STATE"
# migrate records from older versions of this installer
for f in original-perms .created-by-caddyweb-agent; do
	if [ -f "$AGENT_HOME/$f" ] && [ ! -f "$STATE/${f#.}" ]; then mv "$AGENT_HOME/$f" "$STATE/${f#.}"; fi
	rm -f "$AGENT_HOME/$f"
done

if [ "${1:-}" = "--uninstall" ]; then
	say "Removing the CaddyWeb agent"
	# 1. revoke the key first, so nothing can log in any more
	rm -f "$AGENT_HOME/.ssh/authorized_keys"
	# 2. undo the permission changes on the Caddyfile
	if command -v setfacl >/dev/null 2>&1 && [ -f "$STATE/acl" ]; then
		setfacl -x "u:$AGENT_USER" "$CADDYFILE" 2>/dev/null && say "Removed $AGENT_USER's access to $CADDYFILE"
	fi
	if [ -f "$STATE/original-perms" ]; then
		read -r owner mode < "$STATE/original-perms" || true
		if [ -n "${owner:-}" ] && chown "$owner" "$CADDYFILE" && chmod "$mode" "$CADDYFILE"; then
			say "Restored $CADDYFILE ownership to $owner ($mode)"
		fi
	fi
	rm -f "$STAGED"
	# 3. remove the user only if this installer created it
	if [ -f "$STATE/created-by-caddyweb-agent" ]; then
		pkill -u "$AGENT_USER" 2>/dev/null || true
		sleep 1
		if userdel -r "$AGENT_USER" 2>/dev/null; then
			say "Removed the $AGENT_USER user"
		else
			warn "Could not remove the $AGENT_USER user (still logged in?). Its key is revoked; remove it later with: sudo userdel -r $AGENT_USER"
		fi
	else
		rm -rf "$AGENT_HOME/.ssh"
		say "Revoked CaddyWeb's key (the $AGENT_USER user belongs to a CaddyWeb installation on this machine and was kept)"
	fi
	rm -f "$AGENT_BIN" "$CONF"
	rm -rf "$STATE"
	say "Done. Caddy, your Caddyfile and the backups in $BACKUP_DIR were left untouched."
	exit 0
fi

case "$PUBKEY" in
ssh-*) ;;
*) fail "This installer has no public key in it. Download it from CaddyWeb: Servers → Add server (or the gear on a server card) shows the right command." ;;
esac

CADDY_BIN="$(command -v caddy || true)"
[ -n "$CADDY_BIN" ] || fail "caddy was not found on this machine. Install the agent on the server that runs Caddy."
[ -f "$CADDYFILE" ] || fail "No Caddyfile at $CADDYFILE. Re-run with:  curl -fsSL <caddyweb>/agent/install.sh | sudo CADDYFILE=/path/to/Caddyfile bash"
CADDYFILE="$(readlink -f "$CADDYFILE")"
STAGED="$CADDYFILE.caddyweb-staged"

say "Setting up system user '$AGENT_USER'"
if ! id "$AGENT_USER" >/dev/null 2>&1; then
	useradd --system --home-dir "$AGENT_HOME" --create-home --shell /bin/sh --comment "CaddyWeb agent" "$AGENT_USER"
	touch "$STATE/created-by-caddyweb-agent"
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

# Absolute paths: some SSH servers (Dropbear) give a minimal PATH.
printf 'CADDYFILE=%q\nBACKUP_DIR=%q\nCADDY_BIN=%q\n' "$CADDYFILE" "$BACKUP_DIR" "$CADDY_BIN" > "$CONF"
chown root:root "$CONF"
chmod 644 "$CONF"

say "Giving '$AGENT_USER' write access to $CADDYFILE"
[ -f "$STATE/original-perms" ] || stat -L -c '%U:%G %a' "$CADDYFILE" > "$STATE/original-perms"
if command -v setfacl >/dev/null 2>&1 && setfacl -m "u:$AGENT_USER:rw" "$CADDYFILE" 2>/dev/null; then
	touch "$STATE/acl"
else
	chgrp "$AGENT_USER" "$CADDYFILE"
	chmod g+rw "$CADDYFILE"
fi
# Caddy itself must still be able to read its config.
if id caddy >/dev/null 2>&1 && ! runuser -u caddy -- test -r "$CADDYFILE"; then
	read -r owner mode < "$STATE/original-perms"
	chown "$owner" "$CADDYFILE"; chmod "$mode" "$CADDYFILE"
	fail "Giving access would stop the 'caddy' user from reading $CADDYFILE (it is $owner, mode $mode). Install the 'acl' package (sudo apt install acl) and run this again."
fi
# The file where CaddyWeb stages a new Caddyfile before checking it. It lives
# next to the real one so relative imports resolve the same way.
touch "$STAGED"
chown "$AGENT_USER:$AGENT_USER" "$STAGED"
chmod 640 "$STAGED"

say "Creating backup folder $BACKUP_DIR"
mkdir -p "$BACKUP_DIR"
chown "$AGENT_USER:$AGENT_USER" "$BACKUP_DIR"
chmod 750 "$BACKUP_DIR"

say "Authorizing CaddyWeb's SSH key (locked to the agent)"
OPTS="command=\"$AGENT_BIN\",no-port-forwarding,no-agent-forwarding,no-X11-forwarding,no-pty"
if ! pgrep -x dropbear >/dev/null 2>&1; then
	# OpenSSH: also never run ~/.ssh/rc (Dropbear has no such feature and
	# would reject the unknown option).
	OPTS="$OPTS,no-user-rc"
fi
# The .ssh folder belongs to root, so the agent user can't change what its
# own key may do.
rm -rf "$AGENT_HOME/.ssh"
mkdir -p "$AGENT_HOME/.ssh"
echo "$OPTS $PUBKEY" > "$AGENT_HOME/.ssh/authorized_keys"
chown root:"$AGENT_USER" "$AGENT_HOME/.ssh" "$AGENT_HOME/.ssh/authorized_keys"
chmod 750 "$AGENT_HOME/.ssh"
chmod 640 "$AGENT_HOME/.ssh/authorized_keys"

if command -v sshd >/dev/null 2>&1; then
	allow="$(sshd -T 2>/dev/null | awk 'tolower($1)=="allowusers"{ $1=""; print }' || true)"
	if [ -n "$allow" ] && ! echo " $allow " | grep -q " $AGENT_USER "; then
		warn "Your SSH server only allows these users:$allow"
		warn "Add '$AGENT_USER' to AllowUsers in /etc/ssh/sshd_config and restart ssh."
	fi
fi

say "Testing the agent"
if ! runuser -u "$AGENT_USER" -- env -i HOME="$AGENT_HOME" PATH=/usr/bin:/bin "$AGENT_BIN" info | sed 's/^/    /'; then
	warn "The agent test failed; see the output above."
fi

echo
say "All done! Now go back to CaddyWeb and press 'Test connection'."
echo "    CaddyWeb will show this server's SSH fingerprint. It should match one of these:"

# SHA256 fingerprint of an OpenSSH-format public key line ("type base64 …"),
# computed the same way CaddyWeb shows it.
fp_of() {
	local b64
	b64="$(echo "$1" | awk '{print $2}')"
	[ -n "$b64" ] || return 0
	if command -v openssl >/dev/null 2>&1; then
		echo "SHA256:$(echo "$b64" | base64 -d 2>/dev/null | openssl dgst -sha256 -binary | base64 | tr -d '=')  ($(echo "$1" | awk '{print $1}'))"
	elif command -v ssh-keygen >/dev/null 2>&1; then
		echo "$1" | ssh-keygen -lf - 2>/dev/null
	fi
}
shown=0
for k in /etc/ssh/ssh_host_ed25519_key.pub /etc/ssh/ssh_host_ecdsa_key.pub /etc/ssh/ssh_host_rsa_key.pub; do
	[ -f "$k" ] || continue
	line="$(fp_of "$(cat "$k")" || true)"
	[ -n "$line" ] && echo "    $line" && shown=1
done
if command -v dropbearkey >/dev/null 2>&1; then
	for k in /etc/dropbear/dropbear_ed25519_host_key /etc/dropbear/dropbear_ecdsa_host_key /etc/dropbear/dropbear_rsa_host_key; do
		[ -f "$k" ] || continue
		pub="$(dropbearkey -y -f "$k" 2>/dev/null | grep -E '^(ssh-|ecdsa-)' || true)"
		[ -n "$pub" ] || continue
		line="$(fp_of "$pub" || true)"
		[ -n "$line" ] && echo "    $line" && shown=1
	done
fi
[ "$shown" = 1 ] || echo "    (could not work out the fingerprints here — compare with 'ssh-keygen -lf' on the server's host keys)"
exit 0
