// A server's dashboard: every block of its Caddyfile as a card, the "New"
// button, and the pending-changes bar with Review / Apply.

import { h, icon, api, toast, modal, confirmDialog, timeAgo, fill } from '../lib.js';
import { app, navigate } from '../app.js';
import { TYPES, summarize, segTitle, addresses, templates, find, args, readProvider, providerById, unquote } from '../caddy.js';
import { openEditor } from '../editor.js';
import { base, loadServer, serverHeader, connectionBanner, showCaddyError, diffView } from './server-common.js';

const FILTERS = [
  ['all', 'All'],
  ['proxy', 'Proxies', ['proxy', 'loadbalancer', 'routes']],
  ['files', 'Files & apps', ['files', 'spa', 'php']],
  ['redirect', 'Redirects & responses', ['redirect', 'respond']],
  ['other', 'Other', ['custom', 'snippet', 'namedroute', 'directive']],
];

let filterState = { q: '', type: 'all' };

export async function renderDashboard(view, id, stillCurrent) {
  view.append(h('div', { class: 'loading' }, h('span', { class: 'spinner' }), 'Reading the Caddyfile…'));
  const [summary, first] = await Promise.all([loadServer(id), api('GET', base(id) + '/state')]);
  if (!stillCurrent()) return;
  let state = first;
  let dns = { installed: [], checked: false };
  let header = serverHeader(summary, 'sites');
  const banners = h('div', { class: 'banners' });
  const toolbar = h('div', { class: 'toolbar' });
  const content = h('div', { class: 'dash' });
  const bar = h('div', { class: 'pending-bar', role: 'region', 'aria-label': 'Unapplied changes' });
  fill(view, header, banners, toolbar, content, bar);

  const loadDNS = async () => {
    if (dns.checked || !summary.ok) return;
    try { const r = await api('GET', base(id) + '/dns-providers'); dns = { installed: r.installed || [], checked: !r.error }; } catch { /* ignore */ }
  };
  loadDNS();

  const refresh = async (force = false) => {
    try {
      state = await api('GET', base(id) + '/state' + (force ? '?refresh=1' : ''));
      Object.assign(summary, { ok: state.connection.ok, error: state.connection.error, info: state.connection.info || summary.info });
      renderAll();
    } catch (e) { toast(e.message, 'error'); }
  };

  const setState = (s) => { state = s; renderAll(); };

  const editorCtx = () => {
    const global = state.segments.find((s) => s.kind === 'global');
    const acme = global ? find(global.nodes, 'acme_dns') : null;
    return {
      base: base(id), serverId: id, servers: app.servers, indent: state.indent || '\t',
      domains: state.domains, snippets: state.segments.filter((s) => s.kind === 'snippet').map((s) => s.header[0].replace(/^\(|\)$/g, '')),
      dnsInstalled: dns.installed, dnsChecked: dns.checked, globalProvider: acme ? args(acme)[0] : '',
      rev: () => state.rev, onState: setState, isAdmin: app.isAdmin, role: app.user.role,
    };
  };

  const open = (seg) => openEditor({ seg, readOnly: !seg.canEdit, ctx: editorCtx() });
  const openByKey = (key) => { const seg = state.segments.find((s) => s.key === key); if (seg) open(seg); };

  // ---- rendering ----
  function renderAll() {
    // keep the status pill in the header in step with the draft
    summary.pending = changes().length;
    summary.conflict = state.conflict;
    const fresh = serverHeader(summary, 'sites');
    header.replaceWith(fresh);
    header = fresh;
    renderBanners();
    renderToolbar();
    renderCards();
    renderBar();
  }

  function renderBanners() {
    const out = [connectionBanner(summary, () => refresh(true))];
    if (state.conflict) {
      out.push(h('div', { class: 'banner banner-warn' }, icon('alert'), h('div', null,
        h('strong', null, 'The Caddyfile was changed on the server while you had unapplied changes.'),
        h('p', null, 'Probably an edit from the command line. Decide what to do with your draft before applying.')),
      h('div', { class: 'row-wrap' },
        h('button', { class: 'btn', type: 'button', onclick: review }, icon('eye'), 'Compare'),
        app.isAdmin ? h('button', { class: 'btn', type: 'button', onclick: discardAll }, icon('undo'), 'Use the server’s version') : null,
        app.isAdmin ? h('button', { class: 'btn btn-ghost', type: 'button', onclick: async () => {
          try { setState(await api('POST', `${base(id)}/draft/rebase?rev=${state.rev}`)); toast('Keeping your draft. Applying will replace the server’s version.', 'info'); } catch (e) { toast(e.message, 'error'); }
        } }, 'Keep mine') : null)));
    }
    if (state.parseError) {
      out.push(h('div', { class: 'banner banner-danger' }, icon('alert'), h('div', null,
        h('strong', null, 'CaddyWeb couldn’t read part of this Caddyfile.'),
        h('p', { class: 'mono small' }, state.parseError),
        h('p', null, app.isAdmin ? 'Fix it on the Caddyfile page, then come back.' : 'Ask an admin to fix it on the Caddyfile page.')),
      h('a', { class: 'btn', href: '#/s/' + encodeURIComponent(id) + '/caddyfile' }, icon('file'), 'Open Caddyfile')));
    }
    fill(banners, ...out.filter(Boolean));
  }

  function renderToolbar() {
    const search = h('input', { type: 'search', class: 'search-input', placeholder: 'Search domains, IPs, names…', value: filterState.q, 'aria-label': 'Search cards',
      oninput: (e) => { filterState.q = e.target.value; renderCards(); } });
    const counts = {};
    for (const s of state.segments) { if (s.kind === 'global') continue; const t = summarize(s).type; counts[t] = (counts[t] || 0) + 1; }
    const chips = FILTERS.map(([key, label, types]) => {
      const n = key === 'all' ? state.segments.filter((s) => s.kind !== 'global').length : (types || []).reduce((a, t) => a + (counts[t] || 0), 0);
      if (key !== 'all' && !n) return null;
      return h('button', { type: 'button', class: 'chip-filter' + (filterState.type === key ? ' active' : ''), 'aria-pressed': String(filterState.type === key),
        onclick: () => { filterState.type = key; renderToolbar(); renderCards(); } }, label, h('span', { class: 'count' }, n));
    });
    fill(toolbar, 
      h('div', { class: 'search' }, icon('search'), search),
      h('div', { class: 'chips' }, chips),
      h('span', { class: 'spacer' }),
      h('button', { class: 'icon-btn', type: 'button', title: 'Reload from server', 'aria-label': 'Reload from server', onclick: () => refresh(true) }, icon('refresh')),
      app.canAdd && !state.parseError && (summary.ok || state.segments.length) ? h('button', { class: 'btn btn-primary', type: 'button', onclick: newCard }, icon('plus'), 'New') : null);
  }

  function matches(seg) {
    const f = FILTERS.find(([k]) => k === filterState.type);
    const sum = summarize(seg);
    if (f && f[2] && !f[2].includes(sum.type)) return false;
    const q = filterState.q.trim().toLowerCase();
    if (!q) return true;
    const hay = [segTitle(seg), ...addresses(seg), ...sum.targets.map((t) => t.text), ...sum.chips, TYPES[sum.type]?.label || ''].join(' ').toLowerCase();
    return q.split(/\s+/).every((w) => hay.includes(w));
  }

  function renderCards() {
    const global = state.segments.find((s) => s.kind === 'global');
    const sites = state.segments.filter((s) => s.kind === 'site' && matches(s));
    const others = state.segments.filter((s) => s.kind !== 'site' && s.kind !== 'global' && matches(s));
    const deleted = state.deleted.filter(matches);
    const parts = [];
    if (filterState.type === 'all' && !filterState.q) parts.push(globalCard(global));
    if (!state.segments.length && !state.parseError) {
      parts.push(h('div', { class: 'empty-state' }, icon('layers', 'xl'), h('h2', null, summary.ok ? 'This Caddyfile has no sites yet' : 'Nothing to show yet'),
        app.canAdd && summary.ok ? h('button', { class: 'btn btn-primary', type: 'button', onclick: newCard }, icon('plus'), 'Add your first site') : null));
    } else if (!sites.length && !others.length && !deleted.length) {
      parts.push(h('div', { class: 'empty-state small' }, icon('search', 'xl'), h('p', null, 'No cards match your search.')));
    }
    if (sites.length) parts.push(h('div', { class: 'card-grid' }, sites.map(siteCard)));
    if (others.length) parts.push(h('h2', { class: 'section-title' }, 'Snippets & other blocks'), h('div', { class: 'card-grid' }, others.map(siteCard)));
    if (deleted.length) parts.push(h('h2', { class: 'section-title' }, 'Removed — goes away when you apply'), h('div', { class: 'card-grid' }, deleted.map(deletedCard)));
    fill(content, ...parts);
  }

  function globalCard(g) {
    if (!g) {
      return app.isAdmin && summary.ok ? h('button', { class: 'global-card global-empty', type: 'button', onclick: () => openEditor({ seg: { kind: 'global', comments: ['# Global options'], header: [], nodes: [] }, isNew: true, ctx: editorCtx() }) },
        icon('settings'), h('span', null, h('strong', null, 'Global settings'), h('small', null, 'Set up certificates (DNS provider, email) for all sites on this server'))) : null;
    }
    const acme = find(g.nodes, 'acme_dns');
    const prov = readProvider(acme);
    const email = find(g.nodes, 'email');
    const adminN = find(g.nodes, 'admin');
    const adminVal = (adminN && args(adminN)[0]) || 'localhost:2019';
    const exposed = adminVal.startsWith('0.0.0.0') || adminVal.startsWith(':');
    const card = h('article', { class: 'global-card status-' + g.status, tabindex: '0', role: 'button', 'aria-label': 'Global settings',
      onclick: () => open(g), onkeydown: (e) => { if (e.key === 'Enter') open(g); } },
    h('div', { class: 'global-title' }, h('span', { class: 'type-tile type-global' }, icon('settings')),
      h('div', null, h('span', { class: 'eyebrow' }, 'Global settings'), h('h2', null, segTitle(g))), statusBadge(g)),
    h('div', { class: 'global-facts' },
      fact('lock', 'Certificates', prov ? (providerById(prov.id)?.name || prov.id) + ' DNS challenge' : 'Automatic (HTTP challenge)'),
      fact('message', 'Notices to', email ? args(email)[0] : 'not set'),
      fact(exposed ? 'alert' : 'shield', 'Admin API', adminVal === 'off' ? 'off' : adminVal, exposed ? 'warn' : '')),
    exposed ? h('div', { class: 'global-warning' }, icon('alert'), h('span', null, 'The Caddy admin API is open to your whole network without a password. ', app.isAdmin ? h('button', { class: 'link-btn', type: 'button', onclick: (e) => { e.stopPropagation(); lockAdmin(g); } }, 'Fix it now') : 'Ask an admin to restrict it.')) : null);
    return card;
  }

  async function lockAdmin(g) {
    const ok = await confirmDialog('Restrict the admin API?', h('div', null,
      h('p', null, 'This changes “admin ', h('code', null, 'localhost:2019'), '” in the global settings so only programs on the Caddy server itself can use it. CaddyWeb keeps working — it talks to Caddy through its agent on that machine.'),
      h('p', null, 'The change goes into your draft; press Apply to make it live.')), { confirm: 'Restrict it' });
    if (!ok) return;
    const nodes = g.nodes.map((n) => (n.type === 'directive' && n.tokens[0] === 'admin' ? { ...n, tokens: ['admin', 'localhost:2019'] } : n));
    try {
      setState(await api('PUT', `${base(id)}/draft/segments/${g.id}?rev=${state.rev}&key=${encodeURIComponent(g.key)}`,
        { segment: { kind: 'global', comments: g.comments, header: [], headerComment: g.headerComment, nodes } }));
      toast('Added to your draft. Press Apply to make it live.', 'ok');
    } catch (e) { toast(e.message, 'error'); }
  }

  function fact(ic, label, value, cls = '') {
    return h('div', { class: 'fact ' + cls }, icon(ic), h('div', null, h('small', null, label), h('strong', { class: 'mono' }, value)));
  }

  function statusBadge(seg) {
    const labels = { new: 'New', modified: 'Changed', deleted: 'Removed' };
    return labels[seg.status] ? h('span', { class: 'badge badge-' + seg.status, title: 'Not live yet — press Apply' }, labels[seg.status]) : null;
  }

  function siteCard(seg) {
    const sum = summarize(seg);
    const t = TYPES[sum.type] || TYPES.custom;
    const addrs = seg.kind === 'site' ? addresses(seg) : [];
    const card = h('article', { class: `card type-${t.color} status-${seg.status}`, tabindex: '0', role: 'button', 'aria-label': `${t.label}: ${segTitle(seg)}`,
      onclick: (e) => { if (!e.target.closest('a')) open(seg); }, onkeydown: (e) => { if (e.key === 'Enter' && e.target === card) open(seg); } },
    h('div', { class: 'card-head' },
      h('span', { class: 'type-tile type-' + t.color }, icon(t.icon)),
      h('div', { class: 'card-titles' }, h('span', { class: 'eyebrow' }, t.label), h('h3', null, segTitle(seg))),
      statusBadge(seg)),
    addrs.length ? h('div', { class: 'card-addrs' }, addrs.slice(0, 3).map((a) => {
      const url = linkFor(a);
      return h('div', { class: 'addr' }, icon('globe'), url
        ? h('a', { href: url, target: '_blank', rel: 'noopener noreferrer', class: 'mono', title: 'Open ' + url }, a.replace(/^https?:\/\//, ''), icon('external', 'tiny'))
        : h('span', { class: 'mono' }, a));
    }), addrs.length > 3 ? h('div', { class: 'addr muted' }, `+${addrs.length - 3} more`) : null) : null,
    sum.targets.length ? h('div', { class: 'flow' }, sum.targets.slice(0, 4).map((tg) => h('div', { class: 'flow-row' },
      h('span', { class: 'flow-arrow', 'aria-hidden': 'true' }), icon(tg.icon), h('span', { class: tg.mono === false ? '' : 'mono' }, tg.text))),
    sum.targets.length > 4 ? h('div', { class: 'flow-row muted' }, `+${sum.targets.length - 4} more`) : null) : null,
    sum.chips.length ? h('div', { class: 'card-chips' }, sum.chips.map((c) => h('span', { class: 'chip' }, c))) : null,
    h('div', { class: 'card-foot' },
      h('span', { class: 'muted small' }, `Lines ${seg.startLine}–${seg.endLine}`),
      seg.createdBy && seg.status === 'new' ? h('span', { class: 'muted small' }, 'added by ' + seg.createdBy) : null,
      h('span', { class: 'card-open' }, seg.canEdit ? 'Edit' : 'View', icon('arrowRight'))));
    return card;
  }

  function deletedCard(seg) {
    const sum = summarize(seg);
    const t = TYPES[sum.type] || TYPES.custom;
    return h('article', { class: `card type-${t.color} status-deleted` },
      h('div', { class: 'card-head' }, h('span', { class: 'type-tile type-' + t.color }, icon(t.icon)),
        h('div', { class: 'card-titles' }, h('span', { class: 'eyebrow' }, t.label), h('h3', null, segTitle(seg))), statusBadge(seg)),
      h('div', { class: 'card-addrs' }, addresses(seg).map((a) => h('div', { class: 'addr' }, icon('globe'), h('span', { class: 'mono' }, a)))),
      app.isAdmin ? h('div', { class: 'card-foot' }, h('span', { class: 'spacer' }), h('button', { class: 'btn btn-sm', type: 'button', onclick: async () => {
        try { setState(await api('POST', `${base(id)}/draft/restore-deleted?rev=${state.rev}`, { key: seg.key })); toast('Restored', 'ok'); } catch (e) { toast(e.message, 'error'); }
      } }, icon('undo'), 'Keep it')) : null);
  }

  function linkFor(a) {
    if (a.includes('*') || a.includes('{')) return null;
    if (a.startsWith('http://') || a.startsWith('https://')) return a;
    if (a.startsWith(':')) return null;
    // follow a custom https_port from the global options
    const g = state.segments.find((s) => s.kind === 'global');
    const hp = g && find(g.nodes, 'https_port');
    const port = hp ? args(hp)[0] : '';
    return 'https://' + a + (port && port !== '443' && !/:\d+$/.test(a) ? ':' + port : '');
  }

  // ---- pending changes ----
  function changes() {
    return [...state.segments.filter((s) => s.status === 'new' || s.status === 'modified'), ...state.deleted];
  }

  function renderBar() {
    const list = changes();
    bar.classList.toggle('show', state.hasChanges);
    if (!state.hasChanges) { fill(bar, ); return; }
    fill(bar, 
      h('span', { class: 'pending-dot', 'aria-hidden': 'true' }),
      h('div', { class: 'pending-text' }, h('strong', null, list.length ? `${list.length} change${list.length === 1 ? '' : 's'} not live yet` : 'Unapplied changes'),
        h('small', null, app.isAdmin ? 'Saved in your draft. Apply to update Caddy (a backup is made first).' : 'Waiting for an admin to apply.')),
      h('button', { class: 'btn btn-ghost-inv', type: 'button', onclick: review }, icon('eye'), 'Review'),
      app.isAdmin ? h('button', { class: 'btn btn-apply', type: 'button', onclick: (e) => apply(e.currentTarget) }, icon('rocket'), 'Apply') : null);
  }

  async function review() {
    let diff;
    try { diff = await api('GET', base(id) + '/draft/diff'); } catch (e) { toast(e.message, 'error'); return; }
    const list = changes();
    const who = {};
    for (const c of state.log) who[c.target] = c;
    const m = modal({
      title: 'Review changes', wide: true,
      body: h('div', { class: 'stack' },
        list.length ? h('ul', { class: 'change-list' }, list.map((s) => h('li', { class: 'change-' + s.status },
          h('span', { class: 'badge badge-' + s.status }, { new: 'New', modified: 'Changed', deleted: 'Removed' }[s.status]),
          h('span', null, segTitle(s)), h('span', { class: 'muted mono small' }, addresses(s).join(', '))))) : null,
        state.log.length ? h('details', { class: 'section-details' }, h('summary', null, 'Who did what'),
          h('ul', { class: 'log-list' }, state.log.map((c) => h('li', null, h('strong', null, c.user), ` ${c.action} `, h('span', { class: 'mono' }, c.target), h('span', { class: 'muted' }, ' · ' + timeAgo(c.time)))))) : null,
        h('div', { class: 'diff-head' }, h('strong', null, 'Caddyfile differences'), h('span', { class: 'muted small' }, `+${diff.added} −${diff.removed} lines`)),
        diffView(diff.lines)),
      actions: [
        app.isAdmin ? { label: 'Discard all', kind: 'danger-ghost', icon: 'undo', onClick: async () => { await discardAll(); } } : null,
        app.canAdd ? { label: 'Check with Caddy', icon: 'check', keepOpen: true, onClick: validate } : null,
        app.isAdmin ? { label: 'Apply now', kind: 'primary', icon: 'rocket', onClick: async (btn) => { const ok = await apply(btn); return ok; } } : { label: 'Close' },
      ].filter(Boolean),
    });
    return m;
  }

  async function validate() {
    try {
      const r = await api('POST', base(id) + '/validate');
      toast(r.message, 'ok');
    } catch (e) { showCaddyError(e, { onOpenCard: openByKey }); }
  }

  async function apply(btn) {
    if (btn) { btn.disabled = true; btn.classList.add('busy'); }
    try {
      const r = await api('POST', base(id) + '/apply', { rev: state.rev });
      setState(r.state);
      toast(`Applied! Caddy is running the new configuration. Previous version saved as ${r.backup}.`, 'ok', 7000);
      return true;
    } catch (e) {
      if (e.data && e.data.state) setState(e.data.state);
      showCaddyError(e, { onOpenCard: openByKey });
      return false;
    } finally {
      if (btn) { btn.disabled = false; btn.classList.remove('busy'); }
    }
  }

  async function discardAll() {
    const ok = await confirmDialog('Discard all unapplied changes?', 'Your draft goes back to exactly what is on the server now. This can’t be undone.', { confirm: 'Discard', danger: true });
    if (!ok) return false;
    try { setState(await api('POST', `${base(id)}/draft/discard?rev=${state.rev}`)); toast('Draft discarded', 'ok'); } catch (e) { toast(e.message, 'error'); return false; }
  }

  function newCard() {
    const list = templates({ domains: state.domains }).filter((t) => !t.admin || app.isAdmin);
    const m = modal({
      title: 'What would you like to add?', wide: true,
      body: h('div', { class: 'template-grid' }, list.map((t) => h('button', { class: 'template type-' + t.color, type: 'button', onclick: () => {
        m.close();
        openEditor({ seg: t.make(), isNew: true, ctx: editorCtx() });
      } }, h('span', { class: 'type-tile type-' + t.color }, icon(t.icon)), h('strong', null, t.label), h('small', null, t.desc)))),
    });
  }

  renderAll();
  if (location.hash.includes('?new')) newCard();
}
