// Home page: one card per Caddy server, plus the "connect a server" dialog.

import { h, icon, api, toast, modal, field, codeBlock, timeAgo, confirmDialog, select, fill } from '../lib.js';
import { app, navigate } from '../app.js';

export async function renderOverview(view, stillCurrent) {
  view.append(h('div', { class: 'loading' }, h('span', { class: 'spinner' }), 'Checking your servers…'));
  const { servers } = await api('GET', '/api/servers');
  if (!stillCurrent()) return;
  app.servers = servers;

  const head = h('div', { class: 'page-head' },
    h('div', null,
      h('h1', null, 'Your Caddy servers'),
      h('p', { class: 'lede' }, servers.length ? 'Pick a server to see and edit its sites.' : 'Connect CaddyWeb to the machine(s) running Caddy to get started.')),
    app.isAdmin && servers.length ? h('button', { class: 'btn btn-primary', type: 'button', onclick: () => serverDialog(null, () => navigate('#/')) }, icon('plus'), 'Add server') : null);

  if (!servers.length) {
    fill(view, head, h('section', { class: 'welcome' },
      h('div', { class: 'welcome-art', 'aria-hidden': 'true' }, icon('server', 'xl')),
      h('h2', null, app.isAdmin ? 'Connect your first Caddy server' : 'No servers yet'),
      app.isAdmin
        ? h('ol', { class: 'steps' },
          h('li', null, h('strong', null, 'Add the server'), ' — give it a name and its IP address.'),
          h('li', null, h('strong', null, 'Run one command on it'), ' — this installs a tiny, locked-down helper so CaddyWeb can edit the Caddyfile safely.'),
          h('li', null, h('strong', null, 'Test & trust'), ' — CaddyWeb connects and shows your sites as cards.'))
        : h('p', null, 'Ask an admin to connect a Caddy server.'),
      app.isAdmin ? h('button', { class: 'btn btn-primary btn-lg', type: 'button', onclick: () => serverDialog(null, () => navigate('#/')) }, icon('plus'), 'Add a server') : null,
      h('a', { class: 'link', href: '#/help/install' }, 'Read the step-by-step setup guide')));
    return;
  }

  const grid = h('div', { class: 'server-grid' }, servers.map(serverCard));
  // some servers are slow to answer: look again shortly
  if (servers.some((s) => s.checking)) {
    setTimeout(async () => {
      if (!stillCurrent() || document.querySelector('.backdrop')) return;
      try {
        const fresh = (await api('GET', '/api/servers')).servers;
        if (!stillCurrent()) return;
        app.servers = fresh;
        fill(grid, fresh.map(serverCard), app.isAdmin ? grid.lastElementChild : null);
      } catch { /* keep what we have */ }
    }, 4000);
  }
  if (app.isAdmin) grid.append(h('button', { class: 'server-card server-add', type: 'button', onclick: () => serverDialog(null, () => navigate('#/')) },
    h('span', { class: 'add-circle' }, icon('plus')), h('strong', null, 'Add another server'), h('small', null, 'Manage several Caddy machines from one place')));
  fill(view, head, grid);
}

export function statusOf(s) {
  if (!s.configured) return { cls: 'idle', text: 'Not set up' };
  if (s.error && s.error.kind === 'hostkey') return { cls: 'warn', text: 'Needs trust' };
  if (s.checking || (!s.ok && !s.error)) return { cls: 'idle', text: 'Checking…' };
  if (!s.ok) return { cls: 'down', text: 'Unreachable' };
  if (s.conflict) return { cls: 'warn', text: 'Changed on server' };
  if (s.pending) return { cls: 'pending', text: s.pending + ' unapplied' };
  return { cls: 'up', text: 'Online' };
}

function serverCard(s) {
  const st = statusOf(s);
  const where = s.mode === 'local' ? 'This machine' : `${s.user || 'caddyweb'}@${s.host}${s.port && s.port !== 22 ? ':' + s.port : ''}`;
  const open = () => navigate('#/s/' + encodeURIComponent(s.id));
  const card = h('article', { class: 'server-card status-' + st.cls, tabindex: '0', role: 'link', 'aria-label': 'Open ' + s.name,
    onclick: (e) => { if (!e.target.closest('.card-menu')) open(); },
    onkeydown: (e) => { if (e.key === 'Enter' && e.target === card) open(); } },
  h('div', { class: 'server-card-top' },
    h('span', { class: 'server-icon' }, icon('server')),
    h('div', { class: 'server-id' }, h('h2', null, s.name), h('span', { class: 'mono muted' }, where)),
    h('span', { class: 'status-pill status-' + st.cls }, h('span', { class: 'dot' }), st.text)),
  h('div', { class: 'server-stats' },
    stat(s.sites, s.sites === 1 ? 'site' : 'sites'),
    stat(s.info ? s.info.caddyVersion || '—' : '—', 'Caddy'),
    stat(s.info ? s.info.hostname || '—' : '—', 'host')),
  !s.ok && s.configured && s.error ? h('p', { class: 'server-error' }, icon('alert'), s.error.message) : null,
  !s.configured ? h('p', { class: 'server-error muted' }, icon('info'), 'Finish connecting this server in its settings.') : null,
  s.checking ? h('p', { class: 'server-error muted' }, h('span', { class: 'spinner' }), 'Waiting for the server to answer…') : null,
  h('div', { class: 'server-card-foot' },
    h('span', { class: 'muted' }, s.lastApply ? `Last change ${timeAgo(s.lastApply.time)} by ${s.lastApply.user}` : 'No changes made from CaddyWeb yet'),
    app.isAdmin ? h('div', { class: 'card-menu' },
      h('button', { class: 'icon-btn', type: 'button', title: 'Connection settings', 'aria-label': 'Connection settings for ' + s.name, onclick: () => serverDialog(s, () => navigate('#/')) }, icon('settings'))) : null,
    h('span', { class: 'go' }, icon('arrowRight'))));
  return card;
}

function stat(value, label) {
  return h('div', { class: 'stat' }, h('strong', null, String(value)), h('span', null, label));
}

/**
 * The add/edit server dialog: connection details → installer command →
 * test & trust. Also used from a server's dashboard.
 */
export function serverDialog(existing, onDone) {
  let s = existing ? { ...existing } : { name: '', mode: 'ssh', host: '', port: 22, user: 'caddyweb', agentPath: '' };
  const body = h('div', { class: 'stack' });
  let saved = !!existing;
  let result = null;

  const render = () => {
    const name = h('input', { type: 'text', value: s.name, placeholder: 'e.g. DietPi', oninput: (e) => { s.name = e.target.value; } });
    const host = h('input', { type: 'text', class: 'mono', value: s.host || '', placeholder: '192.168.0.5', oninput: (e) => { s.host = e.target.value.trim(); } });
    const port = h('input', { type: 'number', value: s.port || 22, min: 1, max: 65535, oninput: (e) => { s.port = parseInt(e.target.value, 10) || 22; } });
    const user = h('input', { type: 'text', class: 'mono', value: s.user || 'caddyweb', oninput: (e) => { s.user = e.target.value.trim(); } });
    const agentPath = h('input', { type: 'text', class: 'mono', value: s.agentPath || '', placeholder: '/usr/local/bin/caddyweb-agent', oninput: (e) => { s.agentPath = e.target.value.trim(); } });
    const installCmd = `curl -fsSL ${location.origin}/agent/install.sh | sudo bash`;

    fill(body, 
      h('div', { class: 'wizard-step' + (saved ? ' done' : '') },
        h('h3', null, h('span', { class: 'step-no' }, '1'), 'Where is Caddy running?'),
        field('Name', name, 'Anything that helps you recognise it.'),
        h('div', { class: 'segmented', role: 'radiogroup' },
          ['ssh', 'local'].map((m) => h('button', { type: 'button', role: 'radio', 'aria-checked': String(s.mode === m), class: s.mode === m ? 'active' : '', onclick: () => { s.mode = m; render(); } },
            m === 'ssh' ? 'Another machine (SSH)' : 'This same machine'))),
        s.mode === 'ssh'
          ? h('div', { class: 'grid-3' }, field('IP address or host name', host), field('SSH port', port), field('Agent user', user, 'Created by the installer.'))
          : field('Agent path', agentPath, 'CaddyWeb must run as a user allowed to execute it (see Help → Installation).', { optional: true }),
        h('div', { class: 'row-end' }, h('button', { class: 'btn btn-primary', type: 'button', onclick: saveConn }, icon('check'), saved ? 'Save changes' : 'Save and continue'))),
      saved ? h('div', { class: 'wizard-step' },
        h('h3', null, h('span', { class: 'step-no' }, '2'), 'Install the agent on ', s.mode === 'ssh' ? (s.host || 'the server') : 'this machine'),
        h('p', null, 'Log in to the Caddy server (for example ', h('code', null, `ssh you@${s.host || 'server'}`), ') and run:'),
        codeBlock(installCmd),
        h('p', { class: 'muted small' }, 'Prefer to read it first? ', h('a', { href: '/agent/install.sh', target: '_blank', rel: 'noopener' }, 'Open the installer'), '. It only creates a locked-down “caddyweb” user that can do nothing except manage the Caddyfile. You only need to do this once per server; the same command works for all your servers.')) : null,
      saved ? h('div', { class: 'wizard-step' },
        h('h3', null, h('span', { class: 'step-no' }, '3'), 'Test the connection'),
        h('div', { class: 'row-wrap' }, h('button', { class: 'btn', type: 'button', onclick: test }, icon('zap'), 'Test connection')),
        result) : null,
      existing ? h('div', { class: 'danger-zone' },
        h('div', null, h('strong', null, 'Remove this server from CaddyWeb'), h('p', { class: 'muted small' }, 'Caddy and its Caddyfile are not touched. CaddyWeb just forgets it (its local history is kept in the data folder).')),
        h('button', { class: 'btn btn-danger-ghost', type: 'button', onclick: remove }, icon('trash'), 'Remove')) : null);
  };

  const payload = () => ({ name: s.name, connection: { mode: s.mode, host: s.host, port: s.port, user: s.user, agentPath: s.agentPath } });
  const saveConn = async () => {
    try {
      const r = existing || s.id ? await api('PUT', `/api/servers/${encodeURIComponent(s.id)}`, payload()) : await api('POST', '/api/servers', payload());
      s = { ...s, ...r };
      saved = true;
      toast('Server saved', 'ok');
      render();
    } catch (e) { toast(e.message, 'error', 7000); }
  };
  const test = async () => {
    result = h('div', { class: 'loading' }, h('span', { class: 'spinner' }), 'Connecting…');
    render();
    try {
      const r = await api('POST', `/api/servers/${encodeURIComponent(s.id)}/test`);
      showResult(r);
    } catch (e) { result = h('div', { class: 'callout callout-danger' }, icon('alert'), h('span', null, e.message)); render(); }
  };
  const showResult = (r) => {
    if (r.ok) {
      result = h('div', { class: 'callout callout-ok' }, icon('check'), h('div', null, h('strong', null, r.message), h('p', null, 'All set! You can close this window.')));
    } else if (r.needsTrust) {
      result = h('div', { class: r.changed ? 'callout callout-danger' : 'callout callout-info' }, icon(r.changed ? 'alert' : 'key'), h('div', { class: 'stack-sm' },
        h('strong', null, r.changed ? 'The server’s identity changed!' : 'First connection: confirm this is your server'),
        h('p', null, r.changed ? 'If you didn’t reinstall that machine, don’t trust this — someone could be pretending to be it.' : 'The installer printed the server’s fingerprint at the end. It should match:'),
        h('code', { class: 'fingerprint' }, r.fingerprint),
        h('div', null, h('button', { class: 'btn btn-primary', type: 'button', onclick: async () => {
          try { showResult(await api('POST', `/api/servers/${encodeURIComponent(s.id)}/trust`, { fingerprint: r.fingerprint })); } catch (e) { toast(e.message, 'error'); }
        } }, icon('check'), 'It matches — trust this server'))));
    } else {
      result = h('div', { class: 'callout callout-danger' }, icon('alert'), h('div', null, h('strong', null, 'Could not connect'), h('p', null, r.message),
        h('p', { class: 'muted small' }, 'Did the installer finish without errors? Is the IP/port right? See Help → Troubleshooting.')));
    }
    render();
  };
  const remove = async () => {
    if (!await confirmDialog('Remove ' + s.name + '?', 'CaddyWeb will stop managing this server. Caddy keeps running with its current Caddyfile. To also remove the agent, run the installer with --uninstall on that server.', { confirm: 'Remove', danger: true })) return;
    try {
      await api('DELETE', `/api/servers/${encodeURIComponent(s.id)}`);
      toast('Server removed', 'ok');
      m.close();
      onDone && onDone();
    } catch (e) { toast(e.message, 'error'); }
  };

  render();
  const m = modal({ title: existing ? 'Server connection — ' + existing.name : 'Add a Caddy server', body, wide: true,
    actions: [{ label: 'Done', kind: 'primary', onClick: () => { onDone && onDone(); } }] });
}
