// The in-app Help page. Plain content, rendered with a table of contents.

import { h, icon, codeBlock, fill } from '../lib.js';

const P = (...c) => h('p', null, ...c);
const C = (t) => h('code', null, t);
const B = (t) => h('strong', null, t);
const UL = (...items) => h('ul', null, items.map((i) => h('li', null, i)));
const OL = (...items) => h('ol', { class: 'steps' }, items.map((i) => h('li', null, i)));
const TIP = (...c) => h('div', { class: 'callout callout-info' }, icon('info'), h('div', null, ...c));
const WARN = (...c) => h('div', { class: 'callout callout-warn' }, icon('alert'), h('div', null, ...c));

function sections() {
  const origin = location.origin;
  return [
    { id: 'start', title: 'How CaddyWeb works', icon: 'sparkles', body: [
      P('CaddyWeb shows every block of your Caddyfile as a card. You edit cards with simple forms; CaddyWeb writes ', B('ordinary Caddyfile text'), ' back to the real file — keeping your comments and layout — so you can still open it with nano any time and see exactly what changed.'),
      OL(
        [B('Edit'), ' — click a card (or ', B('New'), '). Saving puts the change in your ', B('draft'), '. Nothing on the server changes yet.'],
        [B('Review'), ' — the bar at the bottom shows how many changes are waiting. “Review” shows the exact lines that will change.'],
        [B('Apply'), ' — Caddy checks the new file. If it is valid, the old Caddyfile is backed up, the new one is written and Caddy reloads. If not, you get the error and ', B('nothing changes'), '.']),
      TIP('Several people can edit the draft; it is shared per server. If the file is edited on the server itself in the meantime, CaddyWeb notices and asks what to do before applying.'),
    ] },
    { id: 'install', title: 'Installation & setup', icon: 'rocket', body: [
      P('There are two pieces: ', B('CaddyWeb'), ' (this web app — run it on any machine on your LAN, or on the Caddy server itself) and a tiny ', B('agent'), ' on each Caddy server.'),
      h('h3', null, '1. Install CaddyWeb'),
      P(B('Option A — Linux service'), ' (Debian, Ubuntu, DietPi, Raspberry Pi OS… on x86-64 or ARM):'),
      codeBlock('curl -fsSL https://raw.githubusercontent.com/mf-ky/caddy-web-interface/main/scripts/install.sh | sudo bash'),
      P('This downloads the latest release, installs ', C('/usr/local/bin/caddyweb'), ', creates a ', C('caddyweb'), ' system user and data folder ', C('/var/lib/caddyweb'), ', and starts the ', C('caddyweb'), ' service on port 8090.'),
      P(B('Option B — Docker:')),
      codeBlock('git clone https://github.com/mf-ky/caddy-web-interface.git\ncd caddy-web-interface\ndocker compose up -d --build'),
      P('The README and INSTALL guide in the repository have every step in detail.'),
      h('h3', null, '2. Create your admin account'),
      P('Open ', C('http://<machine-ip>:8090'), '. The first visit asks you to create the admin account.'),
      h('h3', null, '3. Add a server'),
      P('On the ', B('Servers'), ' page press ', B('Add a server'), ', give it a name and its IP address, and save.'),
      h('h3', null, '4. Run the agent installer on the Caddy server'),
      P('SSH into the Caddy server and run the command shown in the dialog:'),
      codeBlock(`curl -fsSL ${origin}/agent/install.sh | sudo bash`),
      P('It creates a locked-down ', C('caddyweb'), ' user, installs ', C('/usr/local/bin/caddyweb-agent'), ', lets that user edit ', C('/etc/caddy/Caddyfile'), ', creates ', C('/etc/caddy/backups'), ', and authorizes CaddyWeb’s key. At the end it prints the server’s ', B('fingerprint'), '.'),
      h('h3', null, '5. Test & trust'),
      P('Back in CaddyWeb press ', B('Test connection'), '. The first time, it shows the server’s fingerprint — check it matches one the installer printed, then press ', B('It matches — trust this server'), '. Your sites appear as cards.'),
      TIP(B('Running CaddyWeb on the Caddy server itself?'), ' Install CaddyWeb first, then add the server with IP ', C('127.0.0.1'), ' and run the agent installer as usual — it reuses the ', C('caddyweb'), ' user. With Docker, use the machine’s LAN IP instead of 127.0.0.1.'),
    ] },
    { id: 'cards', title: 'Cards & what they mean', icon: 'layers', body: [
      P('Each site block becomes a card. The colour and icon show what it does:'),
      UL([B('Reverse Proxy'), ' — sends visitors to an app on another machine/port (the → line shows where).'],
        [B('Load Balancer'), ' — a proxy with several backends.'],
        [B('File Server / Web App / PHP'), ' — serves files from a folder on the Caddy server.'],
        [B('Redirect'), ' — sends visitors to another address.'],
        [B('Static Response'), ' — answers with fixed text.'],
        [B('Path Routes'), ' — different paths go to different places.'],
        [B('Custom'), ' — anything else; still fully editable.']),
      P('Badges: ', h('span', { class: 'badge badge-new' }, 'New'), ' ', h('span', { class: 'badge badge-modified' }, 'Changed'), ' ', h('span', { class: 'badge badge-deleted' }, 'Removed'), ' mean the card differs from what is live — press Apply to make it so.'),
      P('The small chips list extras such as header rules, compression, password protection or a self-signed backend. The comment line above a block in the Caddyfile (e.g. ', C('# Music'), ') is used as the card’s name.'),
    ] },
    { id: 'editing', title: 'Adding & editing', icon: 'edit', body: [
      P('Press ', B('New'), ' and pick what you want to create. The form asks only for what matters; everything else Caddy supports can be added with ', B('Add a behavior'), ' — redirects, headers, password protection, compression, path routes, IP restrictions, logs and more.'),
      P('Every behavior can be switched to ', B('Text'), ' to edit it as Caddyfile text, and the ', B('Caddyfile text'), ' tab edits the whole card as text. Anything the forms don’t understand is kept exactly as written.'),
      P(B('Header rules'), ' on a reverse proxy come with ready-made options (with explanations), e.g. “Send the backend its own address as Host” for NAS panels, or “Fix redirects that point to the backend’s internal address”.'),
      TIP(B('Copy to…'), ' (in the card editor) copies a card into another server’s draft — handy when two servers should serve the same thing.'),
    ] },
    { id: 'apply', title: 'Applying & errors', icon: 'rocket', body: [
      P('Apply always runs ', C('caddy validate'), ' on the server first. If Caddy rejects the file you see its message, the line it points to and which card it is in — and the server is untouched.'),
      P('If the file is valid but Caddy can’t start it (for example, another program is already using a port), Caddy keeps running the old configuration and CaddyWeb puts the old file back, so disk and running config always agree.'),
      P('Use ', B('Check with Caddy'), ' in the Review window to validate without applying.'),
    ] },
    { id: 'backups', title: 'Backups & restoring', icon: 'archive', body: [
      P('Before every Apply or Restore, the Caddyfile being replaced is copied to ', C('/etc/caddy/backups/Caddyfile.YYYY-MM-DD_HH-MM-SS'), ' on the Caddy server, and a copy is kept in CaddyWeb’s data folder as well.'),
      P('On a server’s ', B('Backups'), ' tab you can view each backup, compare it with the current file, download it, or ', B('Restore'), ' it (the current file is backed up first, so a restore can be undone too).'),
      P('Choose how many backups to keep in ', B('Settings'), '. You can also have CaddyWeb copy all backups to another machine with rsync after every change.'),
      P('Restoring by hand from the command line:'),
      codeBlock('sudo cp /etc/caddy/backups/Caddyfile.2026-01-31_20-15-00 /etc/caddy/Caddyfile\nsudo systemctl reload caddy'),
    ] },
    { id: 'certs', title: 'Certificates & DNS providers', icon: 'lock', body: [
      P('Caddy gets free HTTPS certificates automatically. Normally it proves domain ownership over ports 80/443. With a ', B('DNS provider'), ' it proves it by creating a DNS record instead — needed for wildcard certificates and for sites not reachable from the internet.'),
      P('Set the provider for all sites in ', B('Global settings'), ' (the wide card at the top of a server), or for one site in its card under ', B('HTTPS certificate'), '.'),
      P('Each provider needs its Caddy plugin. CaddyWeb shows ✓ for the ones installed on that server. To add one, on the Caddy server:'),
      codeBlock('sudo caddy add-package github.com/caddy-dns/cloudflare\nsudo systemctl restart caddy'),
      WARN('If Caddy was installed with apt, an apt upgrade replaces the binary without your plugins. Run add-package again after upgrading, or build a custom binary with xcaddy.'),
      P('Instead of pasting API keys into the Caddyfile you can write ', C('{env.PORKBUN_API_KEY}'), ' and set the variable for the Caddy service (', C('sudo systemctl edit caddy'), ').'),
    ] },
    { id: 'servers', title: 'Multiple servers', icon: 'server', body: [
      P('The ', B('Servers'), ' page shows one card per Caddy server with its status and number of unapplied changes. Click one to manage its sites. Each server has its own draft, backups and history.'),
      P('Install the agent on each server with the same command — CaddyWeb uses one key for all of them.'),
    ] },
    { id: 'users', title: 'Accounts & passwords', icon: 'users', body: [
      UL([B('Admin'), ' — everything: edit, delete, apply, restore, servers, users, settings.'],
        [B('Power User'), ' — sees all cards, can add new site cards and edit their own new cards until they are applied. Can’t change or delete live cards, apply, restore or download backups. Passwords and API keys are hidden.'],
        [B('User'), ' — read only. Passwords and API keys are hidden.']),
      P('On the command line the roles are called ', C('admin'), ', ', C('power'), ' and ', C('viewer'), '.'),
      h('h3', null, 'Forgot a password?'),
      P('There is no “forgot password” link on purpose. On the machine running CaddyWeb:'),
      codeBlock('sudo caddyweb user passwd alex        # set a new password (asks for it)\nsudo caddyweb user list\nsudo caddyweb user add alice --role power\nsudo caddyweb user role alice admin\nsudo caddyweb user delete alice'),
      P('With Docker: ', C('docker exec -it caddyweb caddyweb user passwd alex'), '. Changing a password signs that user out everywhere.'),
    ] },
    { id: 'security', title: 'Security', icon: 'shield', body: [
      P(B('The agent key can’t open a shell.'), ' The installer locks CaddyWeb’s key to ', C('/usr/local/bin/caddyweb-agent'), ' (forced command, no port forwarding, no terminal). The agent only reads/validates/writes the Caddyfile, lists/reads/restores backups and reloads Caddy. You can read the whole script — it is short.'),
      P(B('Close port 2019.'), ' Caddy’s admin API has no password. If your global options say ', C('admin 0.0.0.0:2019'), ', anyone on your network can reconfigure Caddy and read your DNS API keys. CaddyWeb doesn’t need it; the Global settings card offers a one-click fix that changes it to ', C('localhost:2019'), '. Don’t use ', C('admin off'), ' — that stops ', C('systemctl reload caddy'), ' (and CaddyWeb) from working.'),
      P(B('Server identity.'), ' CaddyWeb remembers each server’s SSH fingerprint and refuses to connect if it changes.'),
      P(B('Serve CaddyWeb over HTTPS'), ' if you use it from untrusted networks: ', C('caddyweb serve --tls-cert … --tls-key …'), ' or put it behind Caddy (on its own port, so a broken Caddyfile can’t lock you out of the tool that fixes it).'),
    ] },
    { id: 'trouble', title: 'Troubleshooting', icon: 'help', body: [
      UL([B('“refused CaddyWeb’s key”'), ' — the agent installer wasn’t run on that server, or was run with a different CaddyWeb. Run it again. On OpenSSH servers with ', C('AllowUsers'), ', add ', C('caddyweb'), '.'],
        [B('“cannot reach … connection refused”'), ' — wrong IP/port, SSH not running, or a firewall.'],
        [B('“server’s SSH key changed”'), ' — the server was reinstalled, or something is impersonating it. If you reinstalled it, remove and re-add the server.'],
        [B('“agent can’t write the Caddyfile”'), ' — re-run the installer; it fixes permissions.'],
        [B('Apply fails with “admin endpoint”'), ' — the Caddyfile has ', C('admin off'), ' or Caddy isn’t running (', C('systemctl status caddy'), ').'],
        [B('DNS provider not installed'), ' — see Certificates & DNS providers.'],
        [B('Logs'), ' — CaddyWeb: ', C('journalctl -u caddyweb'), ' (or ', C('docker logs caddyweb'), '). Caddy: ', C('journalctl -u caddy'), '.']),
    ] },
    { id: 'uninstall', title: 'Uninstalling', icon: 'trash', body: [
      P('Caddy never depends on CaddyWeb. Your Caddyfile is a normal Caddyfile, so you can stop using CaddyWeb at any time.'),
      P('First remove the agent from each Caddy server (keeps the Caddyfile and backups):'),
      codeBlock(`curl -fsSL ${origin}/agent/install.sh | sudo bash -s -- --uninstall`),
      P('Then remove CaddyWeb itself (add ', C('--purge'), ' to also delete its data):'),
      codeBlock('curl -fsSL https://raw.githubusercontent.com/mf-ky/caddy-web-interface/main/scripts/install.sh | sudo bash -s -- --uninstall'),
      P('With Docker: ', C('docker compose down'), ' (add ', C('-v'), ' to delete the data volume).'),
    ] },
  ];
}

export async function renderHelp(view, anchor) {
  const list = sections();
  fill(view, 
    h('div', { class: 'page-head' }, h('div', null, h('h1', null, 'Help'), h('p', { class: 'lede' }, 'Everything you need to set up and use CaddyWeb.'))),
    h('div', { class: 'help' },
      h('nav', { class: 'help-toc', 'aria-label': 'Help topics' }, list.map((s) => h('a', { href: '#/help/' + s.id, class: anchor === s.id ? 'active' : '' }, icon(s.icon), s.title))),
      h('div', { class: 'help-body' }, list.map((s) => h('section', { class: 'help-section', id: 'help-' + s.id },
        h('h2', null, icon(s.icon), s.title), ...s.body)))));
  if (anchor) setTimeout(() => document.getElementById('help-' + anchor)?.scrollIntoView({ block: 'start' }), 30);
}
