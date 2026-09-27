// Per-server pages besides the dashboard: Caddyfile, Backups, History.

import { h, icon, api, toast, modal, confirmDialog, fmtDate, timeAgo, fmtBytes, copyText, fill } from '../lib.js';
import { app, navigate } from '../app.js';
import { base, loadServer, serverHeader, connectionBanner, showCaddyError, diffView } from './server-common.js';

// ---- Caddyfile ----

function highlightLine(line) {
  const frag = [];
  const push = (cls, text) => frag.push(cls ? h('span', { class: cls }, text) : document.createTextNode(text));
  let i = 0;
  let first = true;
  const n = line.length;
  while (i < n) {
    const c = line[i];
    if (c === ' ' || c === '\t') { let j = i; while (j < n && (line[j] === ' ' || line[j] === '\t')) j++; push(null, line.slice(i, j)); i = j; continue; }
    if (c === '#') { push('tok-c', line.slice(i)); break; }
    if (c === '"' || c === '`') {
      let j = i + 1;
      while (j < n && line[j] !== c) { if (line[j] === '\\') j++; j++; }
      push('tok-s', line.slice(i, j + 1)); i = j + 1; first = false; continue;
    }
    let j = i;
    while (j < n && line[j] !== ' ' && line[j] !== '\t') j++;
    const word = line.slice(i, j);
    let cls = null;
    if (word === '{' || word === '}') cls = 'tok-b';
    else if (first && word.startsWith('@')) cls = 'tok-m';
    else if (first) cls = 'tok-d';
    else if (/^\{[^}]*\}$/.test(word)) cls = 'tok-p';
    else if (word.startsWith('@') || word.startsWith('/')) cls = 'tok-m';
    push(cls, word);
    if (word !== '}') first = false;
    i = j;
  }
  return frag;
}

export function codeView(text, { highlight = 0 } = {}) {
  const lines = text.replace(/\r\n/g, '\n').replace(/\n$/, '').split('\n');
  return h('div', { class: 'code-view' }, lines.map((l, i) => h('div', { class: 'code-line' + (highlight === i + 1 ? ' bad' : ''), id: 'L' + (i + 1) },
    h('span', { class: 'ln' }, i + 1), h('code', null, highlightLine(l)))));
}

export async function renderCaddyfile(view, id, stillCurrent) {
  view.append(h('div', { class: 'loading' }, h('span', { class: 'spinner' }), 'Loading…'));
  const summary = await loadServer(id);
  let which = 'draft';
  let data = await api('GET', base(id) + '/caddyfile');
  if (!stillCurrent()) return;
  const body = h('div', { class: 'panel' });
  const render = () => {
    const edit = () => {
      const ta = h('textarea', { class: 'mono raw code-editor full', spellcheck: 'false', rows: Math.max(20, data.text.split('\n').length + 2) }, data.text);
      const err = h('div');
      fill(body, 
        h('div', { class: 'panel-head' }, h('h2', null, 'Edit the whole Caddyfile'), h('span', { class: 'spacer' }),
          h('button', { class: 'btn', type: 'button', onclick: render }, 'Cancel'),
          h('button', { class: 'btn btn-primary', type: 'button', onclick: async () => {
            try {
              await api('PUT', `${base(id)}/draft/raw?rev=${data.rev}`, { text: ta.value });
              toast('Saved to your draft. Review and apply it from the Sites page.', 'ok', 6000);
              data = await api('GET', base(id) + '/caddyfile');
              render();
            } catch (e) {
              fill(err, h('div', { class: 'callout callout-danger' }, icon('alert'), h('span', null, e.message)));
              if (e.data && e.data.line) {
                const lines = ta.value.split('\n');
                const pos = lines.slice(0, e.data.line - 1).join('\n').length + 1;
                ta.focus(); ta.setSelectionRange(pos, pos + (lines[e.data.line - 1] || '').length);
              }
            }
          } }, icon('check'), 'Save to draft')),
        h('p', { class: 'muted' }, 'Changes go into the draft like any card edit; nothing reaches Caddy until you Apply. Comments and formatting are kept exactly as you type them.'),
        err, ta);
      ta.focus();
    };
    fill(body, 
      h('div', { class: 'panel-head' },
        h('div', { class: 'segmented' },
          h('button', { type: 'button', class: which === 'draft' ? 'active' : '', onclick: async () => { which = 'draft'; data = await api('GET', base(id) + '/caddyfile'); render(); } }, 'With my changes'),
          h('button', { type: 'button', class: which === 'live' ? 'active' : '', onclick: async () => { which = 'live'; data = await api('GET', base(id) + '/caddyfile?which=live'); render(); } }, 'Live on server')),
        h('span', { class: 'muted mono small' }, data.path || ''),
        h('span', { class: 'spacer' }),
        h('button', { class: 'btn btn-ghost', type: 'button', onclick: () => copyText(data.text) }, icon('copy'), 'Copy'),
        app.isAdmin && which === 'draft' ? h('button', { class: 'btn', type: 'button', onclick: edit }, icon('edit'), 'Edit as text') : null),
      !app.isAdmin ? h('p', { class: 'muted small' }, 'Passwords and API keys are hidden (••••) for your role.') : null,
      data.text ? codeView(data.text) : h('p', { class: 'muted' }, 'Nothing loaded yet.'));
  };
  render();
  fill(view, serverHeader(summary, 'caddyfile'), connectionBanner(summary, () => navigate(location.hash)) || '', body);
}

// ---- Backups ----

export async function renderBackups(view, id, stillCurrent) {
  view.append(h('div', { class: 'loading' }, h('span', { class: 'spinner' }), 'Loading backups…'));
  const summary = await loadServer(id);
  let data;
  try { data = await api('GET', base(id) + '/backups'); } catch (e) { data = { error: e.message, backups: [] }; }
  if (!stillCurrent()) return;
  const list = data.backups || [];

  const viewBackup = async (b) => {
    let r;
    try { r = await api('GET', `${base(id)}/backups/${encodeURIComponent(b.name)}`); } catch (e) { toast(e.message, 'error'); return; }
    let tab = 'diff';
    const host = h('div');
    const renderTab = () => fill(host, tab === 'diff'
      ? h('div', { class: 'stack' }, h('p', { class: 'muted small' }, 'Red lines are in the current Caddyfile but not in this backup; green lines would come back if you restore it.'), diffView(r.diff))
      : codeView(r.text));
    const tabs = h('div', { class: 'segmented' },
      h('button', { type: 'button', class: 'active', onclick: (e) => { tab = 'diff'; tabs.querySelectorAll('button').forEach((x) => x.classList.remove('active')); e.target.classList.add('active'); renderTab(); } }, `Compared with now (+${r.added} −${r.removed})`),
      h('button', { type: 'button', onclick: (e) => { tab = 'full'; tabs.querySelectorAll('button').forEach((x) => x.classList.remove('active')); e.target.classList.add('active'); renderTab(); } }, 'Full file'));
    renderTab();
    modal({ title: 'Backup from ' + fmtDate(b.time), wide: true, body: h('div', { class: 'stack' }, tabs, host),
      actions: [app.isAdmin ? { label: 'Restore this version', kind: 'primary', icon: 'undo', onClick: () => restore(b) } : null, { label: 'Close' }].filter(Boolean) });
  };

  const restore = async (b, discard = false) => {
    if (!discard) {
      const ok = await confirmDialog('Restore this backup?', h('div', null,
        h('p', null, 'Caddy will switch to the configuration from ', h('strong', null, fmtDate(b.time)), '.'),
        h('p', null, 'The current Caddyfile is backed up first, so you can undo this.')), { confirm: 'Restore' });
      if (!ok) return;
    }
    try {
      const r = await api('POST', `${base(id)}/backups/${encodeURIComponent(b.name)}/restore`, { discardDraft: discard });
      toast('Restored. Caddy is running that version now; the one it replaced was saved as ' + r.backup + '.', 'ok', 7000);
      navigate(location.hash);
    } catch (e) {
      if (e.data && e.data.needsDiscard) {
        const ok = await confirmDialog('You have unapplied changes', 'Restoring a backup discards the changes in your draft. Continue?', { confirm: 'Discard and restore', danger: true });
        if (ok) return restore(b, true);
        return;
      }
      showCaddyError(e);
    }
  };

  const items = list.map((b, i) => h('li', { class: 'backup' },
    h('div', { class: 'backup-when' }, h('strong', null, fmtDate(b.time)), h('span', { class: 'muted small' }, timeAgo(b.time))),
    h('div', { class: 'backup-what' },
      h('span', { class: 'mono small' }, b.name),
      h('span', { class: 'muted small' }, [fmtBytes(b.size), b.replacedBy ? `replaced by ${b.action === 'restore' ? 'a restore' : 'changes'} from ${b.replacedBy}` : 'replaced by a change', b.mirrored ? 'copy kept in CaddyWeb' : null].filter(Boolean).join(' · '))),
    h('div', { class: 'backup-actions' },
      h('button', { class: 'btn btn-sm', type: 'button', onclick: () => viewBackup(b) }, icon('eye'), 'View'),
      app.isAdmin ? h('a', { class: 'btn btn-sm btn-ghost', href: `${base(id)}/backups/${encodeURIComponent(b.name)}/download`, download: b.name }, icon('download'), 'Download') : null,
      app.isAdmin ? h('button', { class: 'btn btn-sm', type: 'button', onclick: () => restore(b) }, icon('undo'), 'Restore') : null),
    i === 0 ? h('span', { class: 'badge badge-new latest' }, 'Latest') : null));

  const off = data.offsite || {};
  fill(view, 
    serverHeader(summary, 'backups'),
    connectionBanner(summary, () => navigate(location.hash)) || '',
    h('section', { class: 'info-strip' },
      h('div', { class: 'info-item' }, icon('server'), h('div', null, h('small', null, 'Stored on the Caddy server'), h('strong', { class: 'mono' }, data.dir || '/etc/caddy/backups'))),
      h('div', { class: 'info-item' }, icon('archive'), h('div', null, h('small', null, 'Keeping'), h('strong', null, `newest ${data.retention || 20} backups`))),
      h('div', { class: 'info-item' }, icon('refresh'), h('div', null, h('small', null, 'Extra copy (rsync)'), h('strong', null, off.enabled ? (off.lastError ? 'failing' : off.target) : 'off'))),
      app.isAdmin ? h('a', { class: 'btn btn-ghost btn-sm', href: '#/settings' }, icon('settings'), 'Change') : null),
    h('p', { class: 'muted' }, 'Every time a change is applied or a backup is restored, the Caddyfile it replaced is saved here first. From the command line: ', h('code', null, `ls ${data.dir || '/etc/caddy/backups'}`), '.'),
    data.error ? h('div', { class: 'callout callout-danger' }, icon('alert'), h('span', null, data.error)) : null,
    list.length ? h('ol', { class: 'backup-list' }, items) : h('div', { class: 'empty-state small' }, icon('archive', 'xl'), h('p', null, 'No backups yet. One is created automatically the first time you apply a change.')));
}

// ---- History ----

export async function renderHistory(view, id, stillCurrent) {
  view.append(h('div', { class: 'loading' }, h('span', { class: 'spinner' }), 'Loading…'));
  const [summary, data] = await Promise.all([loadServer(id), api('GET', base(id) + '/history')]);
  if (!stillCurrent()) return;
  const list = data.history || [];
  fill(view, 
    serverHeader(summary, 'history'),
    list.length ? h('ol', { class: 'timeline' }, list.map((r) => h('li', { class: 'tl-item ' + (r.ok ? 'ok' : 'fail') },
      h('span', { class: 'tl-dot' }, icon(r.ok ? (r.action === 'restore' ? 'undo' : 'rocket') : 'alert')),
      h('div', { class: 'tl-body' },
        h('div', { class: 'tl-title' },
          h('strong', null, r.action === 'restore' ? (r.ok ? 'Restored a backup' : 'Restore failed') : (r.ok ? 'Applied changes' : 'Apply failed')),
          h('span', { class: 'muted' }, ` by ${r.user} · ${fmtDate(r.time)}`)),
        r.note ? h('div', { class: 'muted small mono' }, r.note) : null,
        r.error ? h('div', { class: 'tl-error' }, r.error) : null,
        r.changes && r.changes.length ? h('ul', { class: 'tl-changes' }, r.changes.map((c) => h('li', null, `${c.action} `, h('span', { class: 'mono' }, c.target), h('span', { class: 'muted' }, ` (${c.user})`)))) : null,
        r.backup ? h('div', { class: 'muted small' }, 'Previous version saved as ', h('a', { href: '#/s/' + encodeURIComponent(id) + '/backups', class: 'mono' }, r.backup)) : null))))
      : h('div', { class: 'empty-state small' }, icon('history', 'xl'), h('p', null, 'Nothing applied from CaddyWeb yet.')));
}
