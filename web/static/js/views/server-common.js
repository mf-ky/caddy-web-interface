// Header and helpers shared by the per-server pages (Sites, Caddyfile,
// Backups, History).

import { h, icon, api, modal, toast, confirmDialog } from '../lib.js';
import { app, navigate } from '../app.js';
import { serverDialog, statusOf } from './overview.js';

export const base = (id) => '/api/servers/' + encodeURIComponent(id);

export async function loadServer(id) {
  const s = await api('GET', base(id));
  if (!app.servers.length) {
    try { app.servers = (await api('GET', '/api/servers')).servers; } catch { /* ignore */ }
  }
  return s;
}

export function serverHeader(s, active) {
  const st = statusOf(s);
  const tabs = [['sites', 'Sites', 'layers'], ['caddyfile', 'Caddyfile', 'file'], ['backups', 'Backups', 'archive'], ['history', 'History', 'history']];
  const others = app.servers.filter((x) => x.id !== s.id);
  return h('div', { class: 'server-head' },
    h('div', { class: 'crumbs' },
      h('a', { href: '#/' }, 'Servers'), h('span', { 'aria-hidden': 'true' }, '/'),
      others.length ? h('select', { class: 'server-switch', 'aria-label': 'Switch server', onchange: (e) => navigate('#/s/' + encodeURIComponent(e.target.value) + (active === 'sites' ? '' : '/' + active)) },
        [s, ...others].map((x) => h('option', { value: x.id, selected: x.id === s.id }, x.name))) : h('span', null, s.name)),
    h('div', { class: 'server-title' },
      h('span', { class: 'server-icon' }, icon('server')),
      h('div', null,
        h('h1', null, s.name),
        h('div', { class: 'server-sub' },
          h('span', { class: 'status-pill status-' + st.cls }, h('span', { class: 'dot' }), st.text),
          s.info ? h('span', { class: 'muted' }, `${s.info.hostname} · Caddy ${s.info.caddyVersion}`) : null,
          s.info ? h('span', { class: 'muted mono small' }, s.info.caddyfile) : null)),
      h('span', { class: 'spacer' }),
      app.isAdmin ? h('button', { class: 'btn btn-ghost', type: 'button', onclick: () => serverDialog(s, () => navigate(location.hash)) }, icon('settings'), 'Connection') : null),
    h('nav', { class: 'subtabs', 'aria-label': 'Server sections' }, tabs.map(([key, label, ic]) =>
      h('a', { href: '#/s/' + encodeURIComponent(s.id) + (key === 'sites' ? '' : '/' + key), class: 'subtab' + (active === key ? ' active' : ''), 'aria-current': active === key ? 'page' : null }, icon(ic), label))));
}

/** Banner when the server can't be reached or isn't set up. */
export function connectionBanner(s, onRetry) {
  if (s.ok) return null;
  if (!s.configured) {
    return h('div', { class: 'banner banner-info' }, icon('info'), h('div', null, h('strong', null, 'This server isn’t connected yet.'),
      h('p', null, app.isAdmin ? 'Open its connection settings to finish setting it up.' : 'Ask an admin to finish setting it up.')),
    app.isAdmin ? h('button', { class: 'btn', type: 'button', onclick: () => serverDialog(s, onRetry) }, 'Connection settings') : null);
  }
  return h('div', { class: 'banner banner-danger' }, icon('alert'), h('div', null,
    h('strong', null, s.error && s.error.kind === 'hostkey' ? 'CaddyWeb doesn’t trust this server’s identity yet.' : 'Can’t reach the Caddy server.'),
    h('p', null, s.error ? s.error.message : ''),
    h('p', { class: 'muted small' }, 'You are seeing the last copy CaddyWeb saw. Changes can’t be applied until the connection works.')),
  h('div', { class: 'row-wrap' },
    h('button', { class: 'btn', type: 'button', onclick: onRetry }, icon('refresh'), 'Retry'),
    app.isAdmin ? h('button', { class: 'btn btn-ghost', type: 'button', onclick: () => serverDialog(s, onRetry) }, 'Connection settings') : null));
}

/** Show a Caddy / agent error returned by apply, validate or restore. */
export function showCaddyError(e, { onOpenCard } = {}) {
  const d = e.data || {};
  const ce = d.caddy || {};
  const titles = {
    invalid: 'Caddy found a problem in the configuration',
    reload: 'Caddy couldn’t start the new configuration',
    conflict: 'The Caddyfile changed on the server',
    connection: 'Can’t reach the Caddy server',
    hostkey: 'Server identity not trusted',
  };
  const hints = {
    invalid: 'Nothing was changed on the server — your sites keep running as before. Fix the highlighted line and try again.',
    reload: 'Caddy refused to switch, so it is still running the previous configuration, and the Caddyfile on disk was put back. Common causes: a port already in use by another program, or a certificate/DNS provider problem.',
    conflict: 'Someone (or something) edited the Caddyfile directly after you started your changes. Review the differences, then keep or discard your draft.',
    connection: 'Check that the Caddy server is on and reachable.',
  };
  const body = h('div', { class: 'stack' },
    h('p', { class: 'error-message' }, ce.message || e.message),
    hints[ce.kind] ? h('div', { class: 'callout callout-info' }, icon('info'), h('span', null, hints[ce.kind])) : null,
    d.context ? h('div', { class: 'code-context' }, d.context.map((l) => h('div', { class: 'ctx-line' + (l.n === ce.line ? ' ctx-bad' : '') },
      h('span', { class: 'ln' }, l.n), h('code', null, l.text || ' ')))) : null,
    d.segmentLabel ? h('p', null, 'The problem is in the card ', h('strong', null, d.segmentLabel), '.') : null,
    ce.detail && ce.detail !== ce.message ? h('details', { class: 'section-details' }, h('summary', null, 'Full message from Caddy'), h('pre', { class: 'detail-pre' }, ce.detail)) : null);
  modal({
    title: titles[ce.kind] || 'Something went wrong', body, wide: true,
    actions: [
      d.segmentKey && onOpenCard ? { label: 'Open that card', kind: 'primary', icon: 'edit', onClick: () => onOpenCard(d.segmentKey) } : null,
      { label: 'Close' },
    ].filter(Boolean),
  });
}

/** Unified diff viewer. */
export function diffView(lines) {
  if (!lines || !lines.length) return h('p', { class: 'muted' }, 'No differences.');
  return h('div', { class: 'diff' }, lines.map((l) => l.op === '~'
    ? h('div', { class: 'diff-skip' }, '⋯')
    : h('div', { class: 'diff-line diff-' + (l.op === '+' ? 'add' : l.op === '-' ? 'del' : 'ctx') },
      h('span', { class: 'ln' }, l.old || ''), h('span', { class: 'ln' }, l.new || ''), h('span', { class: 'op' }, l.op === ' ' ? '' : l.op), h('code', null, l.text || ' '))));
}

export { confirmDialog, toast };
