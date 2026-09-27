// Users, global Settings and My account.

import { h, icon, api, toast, modal, field, select, toggle, confirmDialog, fmtDate, timeAgo, codeBlock, copyText, fill } from '../lib.js';
import { app, navigate, setTheme } from '../app.js';

const ROLES = [
  ['admin', 'Admin', 'Full access: edit, delete, apply, restore backups, manage servers and users.'],
  ['power', 'Power User', 'Sees all cards and can add new site cards. Can’t change or delete live cards, apply, or restore. Passwords and API keys are hidden.'],
  ['viewer', 'User', 'Read only. Passwords and API keys are hidden.'],
];

// ---- Users ----

export async function renderUsers(view) {
  const data = await api('GET', '/api/users');
  const render = (users) => {
    fill(view, 
      h('div', { class: 'page-head' },
        h('div', null, h('h1', null, 'Users'), h('p', { class: 'lede' }, 'Who can sign in to CaddyWeb, and what they may do.')),
        h('button', { class: 'btn btn-primary', type: 'button', onclick: addUser }, icon('plus'), 'Add user')),
      h('div', { class: 'role-legend' }, ROLES.map(([k, l, d]) => h('div', { class: 'role-card role-' + k }, h('strong', null, l), h('p', null, d)))),
      h('div', { class: 'table-wrap' }, h('table', { class: 'table' },
        h('thead', null, h('tr', null, h('th', null, 'User'), h('th', null, 'Role'), h('th', null, 'Last sign-in'), h('th', null, 'Password changed'), h('th', { class: 'right' }, ''))),
        h('tbody', null, users.map((u) => h('tr', null,
          h('td', null, h('span', { class: 'avatar sm' }, u.username[0].toUpperCase()), h('strong', null, u.username), u.username === app.user.username ? h('span', { class: 'muted small' }, ' (you)') : null),
          h('td', null, select(ROLES.map(([k, l]) => [k, l]), u.role, async (v) => {
            try { render((await api('PUT', '/api/users/' + encodeURIComponent(u.username), { role: v })).users); toast(`${u.username} is now ${ROLES.find((r) => r[0] === v)[1]}`, 'ok'); } catch (e) { toast(e.message, 'error'); render(users); }
          })),
          h('td', { class: 'muted' }, u.lastLogin && !u.lastLogin.startsWith('0001') ? timeAgo(u.lastLogin) : 'never'),
          h('td', { class: 'muted' }, fmtDate(u.passwordSet)),
          h('td', { class: 'right nowrap' },
            h('button', { class: 'btn btn-sm btn-ghost', type: 'button', onclick: () => resetPw(u) }, icon('key'), 'Reset password'),
            u.username !== app.user.username ? h('button', { class: 'icon-btn danger', type: 'button', title: 'Delete', 'aria-label': 'Delete ' + u.username, onclick: () => del(u) }, icon('trash')) : null)))))),
      h('div', { class: 'callout callout-info' }, icon('terminal'), h('div', null,
        h('strong', null, 'Locked out?'), h('p', null, 'On the machine running CaddyWeb you can always manage accounts from the command line:'),
        codeBlock('sudo caddyweb user list\nsudo caddyweb user passwd <name>\nsudo caddyweb user add <name> --role admin'),
        h('p', { class: 'muted small' }, 'Docker: prefix with “docker exec -it caddyweb”. See Help → Accounts & passwords.'))));
  };
  const addUser = () => {
    let role = 'viewer';
    const name = h('input', { type: 'text', autocomplete: 'off', autocapitalize: 'off' });
    const pw = h('input', { type: 'password', autocomplete: 'new-password' });
    modal({ title: 'Add user', body: h('div', { class: 'stack' }, field('Username', name), field('Password', pw, 'At least 8 characters. They can change it later under My account.'),
      field('Role', select(ROLES.map(([k, l]) => [k, l]), role, (v) => { role = v; }), 'See the role descriptions above.')),
    actions: [{ label: 'Cancel' }, { label: 'Add user', kind: 'primary', onClick: async () => {
      try { render((await api('POST', '/api/users', { username: name.value, password: pw.value, role })).users); toast('User added', 'ok'); } catch (e) { toast(e.message, 'error'); return false; }
    } }] });
  };
  const resetPw = (u) => {
    const pw = h('input', { type: 'password', autocomplete: 'new-password' });
    modal({ title: 'New password for ' + u.username, body: field('New password', pw, 'They will be signed out everywhere.'),
      actions: [{ label: 'Cancel' }, { label: 'Set password', kind: 'primary', onClick: async () => {
        try { render((await api('PUT', '/api/users/' + encodeURIComponent(u.username), { password: pw.value })).users); toast('Password changed', 'ok'); } catch (e) { toast(e.message, 'error'); return false; }
      } }] });
  };
  const del = async (u) => {
    if (!await confirmDialog('Delete ' + u.username + '?', 'They won’t be able to sign in any more.', { confirm: 'Delete', danger: true })) return;
    try { render((await api('DELETE', '/api/users/' + encodeURIComponent(u.username))).users); toast('User deleted', 'ok'); } catch (e) { toast(e.message, 'error'); }
  };
  render(data.users);
}

// ---- Settings ----

export async function renderSettings(view) {
  const data = await api('GET', '/api/settings');
  const set = data.settings;
  const retention = h('input', { type: 'number', min: 1, max: 1000, value: set.backupRetention });
  const hours = h('input', { type: 'number', min: 1, value: set.sessionHours });
  const target = h('input', { type: 'text', class: 'mono', value: set.offsite.target || '', placeholder: 'backup@nas.lan:/volume1/backups/caddy/' });
  const port = h('input', { type: 'number', min: 1, max: 65535, value: set.offsite.port || 22 });
  let offEnabled = set.offsite.enabled;
  const status = h('div');
  const renderStatus = (o) => fill(status, o.lastRun ? h('div', { class: 'callout ' + (o.lastError ? 'callout-danger' : 'callout-ok') }, icon(o.lastError ? 'alert' : 'check'),
    h('span', null, o.lastError ? `Last copy failed ${timeAgo(o.lastRun)}: ${o.lastError}` : `Last copy succeeded ${timeAgo(o.lastRun)}.`)) : null);
  renderStatus(set.offsite);

  const save = async () => {
    try {
      const r = await api('PUT', '/api/settings', {
        backupRetention: parseInt(retention.value, 10), sessionHours: parseInt(hours.value, 10),
        offsite: { enabled: offEnabled, target: target.value, port: parseInt(port.value, 10) || 22 },
      });
      renderStatus(r.settings.offsite);
      toast('Settings saved', 'ok');
    } catch (e) { toast(e.message, 'error', 7000); }
  };

  fill(view, 
    h('div', { class: 'page-head' }, h('div', null, h('h1', null, 'Settings'), h('p', { class: 'lede' }, 'Options that apply to CaddyWeb and all your servers.')),
      h('button', { class: 'btn btn-primary', type: 'button', onclick: save }, icon('check'), 'Save settings')),
    h('div', { class: 'settings-grid' },
      h('section', { class: 'panel' },
        h('h2', null, icon('server'), 'Servers'),
        h('p', null, 'Add, test or remove Caddy servers from the ', h('a', { href: '#/' }, 'Servers'), ' page (the gear on each card).'),
        h('h3', null, 'CaddyWeb’s SSH key'),
        h('p', { class: 'muted small' }, 'The agent installer authorizes this key on each Caddy server, locked so it can only run the agent. Keep CaddyWeb’s data folder private.'),
        codeBlock(data.publicKey),
        h('h3', null, 'Agent installer'),
        codeBlock(`curl -fsSL ${location.origin}/agent/install.sh | sudo bash`),
        h('p', { class: 'muted small' }, 'Run on each Caddy server. Undo with: ', h('code', null, 'curl -fsSL ' + location.origin + '/agent/install.sh | sudo bash -s -- --uninstall'))),
      h('section', { class: 'panel' },
        h('h2', null, icon('archive'), 'Backups'),
        field('Backups to keep per server', retention, 'Older ones are deleted automatically after each apply. Caddyfiles are tiny, so a generous number is fine.'),
        h('h3', null, 'Extra copy with rsync'),
        h('p', { class: 'muted small' }, 'After every apply, CaddyWeb can copy all backups to another machine (a NAS, for example) — one folder per server.'),
        toggle('Copy backups with rsync', offEnabled, (on) => { offEnabled = on; }),
        field('Destination', target, 'user@host:/path/ for another machine (authorize CaddyWeb’s SSH key there), or a local folder like /mnt/backup/caddy/.'),
        field('SSH port', port),
        h('div', { class: 'row-wrap' },
          h('button', { class: 'btn', type: 'button', onclick: async () => {
            await save();
            try { const r = await api('POST', '/api/offsite/run'); renderStatus(r.offsite); toast('Backups copied', 'ok'); } catch (e) { toast(e.message, 'error', 8000); navigate('#/settings'); }
          } }, icon('refresh'), 'Save and copy now')),
        status),
      h('section', { class: 'panel' },
        h('h2', null, icon('lock'), 'Sign-in'),
        field('Stay signed in for (hours)', hours, '168 hours = one week. Changing a password signs that user out everywhere.'),
        h('h3', null, 'About'),
        h('dl', { class: 'facts' }, h('dt', null, 'Version'), h('dd', null, app.session.version), h('dt', null, 'Data folder'), h('dd', { class: 'mono' }, data.dataDir)))));
}

// ---- My account ----

export async function renderAccount(view) {
  const cur = h('input', { type: 'password', autocomplete: 'current-password' });
  const nw = h('input', { type: 'password', autocomplete: 'new-password' });
  const nw2 = h('input', { type: 'password', autocomplete: 'new-password' });
  let theme = (() => { try { return localStorage.getItem('caddyweb-theme') || 'system'; } catch { return 'system'; } })();
  fill(view, 
    h('div', { class: 'page-head' }, h('div', null, h('h1', null, 'My account'), h('p', { class: 'lede' }, `Signed in as ${app.user.username} · ${app.user.roleLabel}`))),
    h('div', { class: 'settings-grid' },
      h('section', { class: 'panel' },
        h('h2', null, icon('key'), 'Change password'),
        field('Current password', cur), field('New password', nw, 'At least 8 characters.'), field('Repeat new password', nw2),
        h('button', { class: 'btn btn-primary', type: 'button', onclick: async () => {
          if (nw.value !== nw2.value) { toast('The new passwords don’t match', 'error'); return; }
          try { await api('POST', '/api/me/password', { current: cur.value, new: nw.value }); toast('Password changed', 'ok'); cur.value = nw.value = nw2.value = ''; } catch (e) { toast(e.message, 'error'); }
        } }, 'Change password')),
      h('section', { class: 'panel' },
        h('h2', null, icon('sun'), 'Appearance'),
        h('div', { class: 'segmented' }, [['system', 'Match my device', 'monitor'], ['light', 'Light', 'sun'], ['dark', 'Dark', 'moon']].map(([k, l, ic]) =>
          h('button', { type: 'button', class: theme === k ? 'active' : '', onclick: (e) => { theme = k; setTheme(k); e.target.closest('.segmented').querySelectorAll('button').forEach((b) => b.classList.remove('active')); e.target.closest('button').classList.add('active'); } }, icon(ic), l))))));
}

export { copyText };
