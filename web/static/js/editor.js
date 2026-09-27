// The card editor: a side drawer with a friendly form and a "Caddyfile text"
// tab. Works on a deep copy of the card; nothing is saved until "Save to
// draft", and nothing goes live until "Apply".

import { h, icon, api, toast, modal, drawer, field, toggle, select, confirmDialog, codeBlock, fill } from './lib.js';
import {
  quote, unquote, dir, blockDir, name, args, directives, find, findAll, clone, toPayload, classify, TYPES, segTitle,
  DNS_PROVIDERS, providerById, readProvider, buildProvider, ACME_CAS, addresses,
} from './caddy.js';
import { DIRECTIVES, RECIPES, describe, rawEditor, listEditor } from './directives.js';

// ---- block editor (list of behaviors) ----

function blockEditor(nodes, ctx) {
  const wrap = h('div', { class: 'block-editor' });
  const render = () => {
    const visible = nodes.filter((n) => !ctx.hideNode || !ctx.hideNode(n, nodes));
    fill(wrap, 
      visible.length ? null : h('div', { class: 'empty-block' }, 'Nothing here yet — add a behavior below.'),
      ...visible.map((n) => itemCard(n, nodes, ctx, render)),
      addButton(nodes, ctx, render));
  };
  render();
  return wrap;
}

function itemCard(node, nodes, ctx, rerender) {
  const d = describe(node);
  let rawMode = node.__raw !== undefined || (node.type === 'directive' && !d.editor);
  const body = h('div', { class: 'item-body' });
  const renderBody = () => {
    fill(body, );
    if (node.type === 'comment') {
      body.append(h('input', { type: 'text', class: 'mono comment-input', value: node.text, oninput: (e) => { node.text = e.target.value; } }));
    } else if (rawMode) {
      body.append(rawEditor(node, ctx));
    } else {
      const siblings = { ...ctx, hasRoot: ctx.hasRoot || !!find(nodes, 'root'), blockEditor };
      body.append(d.editor(node, siblings));
    }
  };
  renderBody();
  const idx = () => nodes.indexOf(node);
  const move = (delta) => {
    // swap with the next *visible* item (hidden ones, like tls, stay put)
    const i = idx();
    let j = i + delta;
    while (j >= 0 && j < nodes.length && ctx.hideNode && ctx.hideNode(nodes[j], nodes)) j += delta;
    if (j < 0 || j >= nodes.length) return;
    [nodes[i], nodes[j]] = [nodes[j], nodes[i]];
    rerender();
  };
  const canForm = node.type === 'directive' && d.editor;
  return h('div', { class: 'item' + (node.type === 'comment' ? ' item-comment' : '') },
    h('div', { class: 'item-head' },
      h('span', { class: 'item-icon' }, icon(d.icon || 'code')),
      h('div', { class: 'item-title' }, h('strong', null, d.label), d.desc && node.type !== 'comment' ? h('small', null, d.desc) : null),
      h('div', { class: 'item-actions' },
        canForm ? h('button', { class: 'btn btn-ghost btn-xs', type: 'button', title: rawMode ? 'Back to the form' : 'Edit this as Caddyfile text', onclick: () => {
          if (rawMode && node.__raw !== undefined) {
            toast('Text edits are kept as text. Save the card to turn them back into a form.', 'info');
            return;
          }
          rawMode = !rawMode;
          renderBody();
        } }, icon('code'), rawMode ? 'Form' : 'Text') : null,
        h('button', { class: 'icon-btn', type: 'button', title: 'Move up', 'aria-label': 'Move up', onclick: () => move(-1) }, icon('chevronUp')),
        h('button', { class: 'icon-btn', type: 'button', title: 'Move down', 'aria-label': 'Move down', onclick: () => move(1) }, icon('chevronDown')),
        h('button', { class: 'icon-btn danger', type: 'button', title: 'Remove', 'aria-label': 'Remove', onclick: () => { nodes.splice(idx(), 1); rerender(); } }, icon('trash')))),
    body);
}

function addButton(nodes, ctx, rerender) {
  const add = (list) => { nodes.push(...list); rerender(); };
  return h('button', { class: 'btn btn-dashed', type: 'button', onclick: () => {
    const groups = {};
    for (const [key, d] of Object.entries(DIRECTIVES)) {
      if (d.hidden || !d.make) continue;
      (groups[d.group] ||= []).push({ label: d.label, desc: d.desc, icon: d.icon, make: () => [d.make(ctx)] });
    }
    for (const r of RECIPES) (groups[r.group] ||= []).unshift({ label: r.label, desc: r.desc, icon: r.icon, make: r.make });
    groups.Other.push({ label: 'Caddyfile text', desc: 'Type any directive by hand.', icon: 'code', make: () => [{ type: 'directive', tokens: [], block: null, __raw: '' }] });
    groups.Other.push({ label: 'Note', desc: 'A comment to remind yourself of something.', icon: 'info', make: () => [{ type: 'comment', text: '# ' }] });
    const m = modal({
      title: 'Add a behavior', wide: true,
      body: h('div', { class: 'add-menu' }, ['Serve', 'Routing', 'Security', 'Performance', 'Other'].filter((g) => groups[g]).map((g) => h('section', null,
        h('h3', null, g),
        h('div', { class: 'add-grid' }, groups[g].map((it) => h('button', { class: 'add-option', type: 'button', onclick: () => { add(it.make()); m.close(); } },
          h('span', { class: 'item-icon' }, icon(it.icon)), h('span', null, h('strong', null, it.label), h('small', null, it.desc)))))))),
    });
  } }, icon('plus'), 'Add a behavior');
}

// ---- certificate section for a site ----

function tlsMode(work) {
  const t = find(work.nodes, 'tls');
  const addrs = addresses(work);
  if (work.__http === true) return 'http';
  if (work.__http === undefined && addrs.length && addrs.every((a) => a.startsWith('http://'))) return 'http';
  if (!t) return 'auto';
  const a = args(t);
  if (a[0] === 'internal' && !t.block) return 'internal';
  if (a.length === 2 && !t.block) return 'custom';
  if (!a.length && t.block && directives(t.block).length === 1 && find(t.block, 'dns')) return 'dns';
  return 'advanced';
}

function certSection(work, ctx, onAddrChange) {
  const wrap = h('div', { class: 'stack' });
  const render = () => {
    const mode = tlsMode(work);
    const t = find(work.nodes, 'tls');
    const setMode = (m) => {
      // remove current tls directive and http:// prefixes, then apply mode
      for (let i = work.nodes.length - 1; i >= 0; i--) if (name(work.nodes[i]) === 'tls') work.nodes.splice(i, 1);
      work.header = work.header.map((h0) => h0.replace(/^http:\/\//, ''));
      if (m === 'internal') work.nodes.unshift(dir('tls', 'internal'));
      if (m === 'custom') work.nodes.unshift(dir('tls', '/etc/caddy/certs/site.crt', '/etc/caddy/certs/site.key'));
      if (m === 'dns') work.nodes.unshift(blockDir('tls', [], [buildProvider('dns', ctx.globalProvider || 'cloudflare', {})]));
      work.__http = m === 'http';
      if (m === 'http') work.header = work.header.map((h0) => (h0.includes('://') ? h0 : 'http://' + h0));
      if (m === 'advanced') work.nodes.unshift(blockDir('tls', [], []));
      onAddrChange();
      render();
    };
    const modes = [
      ['auto', ctx.globalProvider ? `Automatic — using ${providerById(ctx.globalProvider)?.name || ctx.globalProvider} from Global settings` : 'Automatic (recommended)'],
      ['dns', 'Automatic, with a DNS provider just for this site'],
      ['internal', 'Caddy’s own certificate (LAN-only sites)'],
      ['custom', 'My own certificate files'],
      ['http', 'No HTTPS (plain HTTP only)'],
    ];
    if (mode === 'advanced') modes.push(['advanced', 'Custom TLS settings (edit below)']);
    const parts = [field('Certificate', select(modes, mode, setMode), {
      auto: 'Caddy gets and renews a free certificate for you.',
      dns: 'Proves you own the domain through your DNS provider — works even if the site isn’t reachable from the internet.',
      internal: 'Browsers will warn unless they trust Caddy’s local CA. Fine for internal tools.',
      custom: 'Point to certificate and key files on the Caddy server.',
      http: 'Traffic is not encrypted. Only for testing or when something else handles HTTPS.',
      advanced: 'This site has TLS settings the form doesn’t cover; they are kept as they are.',
    }[mode])];
    if (mode === 'custom') {
      const a = args(t);
      parts.push(h('div', { class: 'grid-2' },
        field('Certificate file', h('input', { type: 'text', class: 'mono', value: a[0], oninput: (e) => { t.tokens[1] = quote(e.target.value); } })),
        field('Key file', h('input', { type: 'text', class: 'mono', value: a[1], oninput: (e) => { t.tokens[2] = quote(e.target.value); } }))));
    }
    if (mode === 'dns') {
      const dnsNode = find(t.block, 'dns');
      parts.push(providerForm(dnsNode, 'dns', ctx, (n) => { t.block[t.block.indexOf(find(t.block, 'dns'))] = n; }));
    }
    if (mode === 'advanced') parts.push(rawEditor(t, ctx));
    fill(wrap, ...parts);
  };
  render();
  return wrap;
}

/** Provider picker + credential fields. onReplace(newNode) swaps the node. */
function providerForm(node, keyword, ctx, onReplace) {
  const wrap = h('div', { class: 'provider-form' });
  let cur = readProvider(node) || { id: '', values: {}, extraArgs: [] };
  const render = () => {
    const p = providerById(cur.id);
    const installed = ctx.dnsInstalled || [];
    const known = DNS_PROVIDERS.map((x) => [x.id, x.name + (installed.includes(x.id) ? '  ✓ installed' : '')]);
    if (cur.id && !p) known.unshift([cur.id, cur.id + ' (other)']);
    const update = () => onReplace(node = buildProvider(keyword, cur.id, cur.values, cur.extraArgs, cur.extra || [], { comment: cur.comment, blank: cur.blank }));
    const missing = cur.id && ctx.dnsChecked && !installed.includes(cur.id);
    fill(wrap, 
      field('DNS provider', select([['', 'Choose your DNS provider…'], ...known], cur.id, (v) => { cur = { id: v, values: {}, extraArgs: [], extra: [], comment: cur.comment, blank: cur.blank }; update(); render(); })),
      missing ? h('div', { class: 'callout callout-warn' }, icon('alert'), h('div', null,
        h('strong', null, `The ${p ? p.name : cur.id} module isn’t installed in your Caddy.`),
        h('p', null, 'Caddy needs a plugin for each DNS provider. On the Caddy server run:'),
        codeBlock(`sudo caddy add-package github.com/caddy-dns/${cur.id}\nsudo systemctl restart caddy`),
        h('p', { class: 'muted' }, 'If you installed Caddy with apt, the next Caddy update replaces the binary; run the command again after upgrading (or build with xcaddy).'))) : null,
      ...(p ? p.fields : Object.keys(cur.values).map((k) => [k, k])).map(([k, label, secret]) => {
        const inp = h('input', { type: secret ? 'password' : 'text', class: 'mono', value: cur.values[k] || '', autocomplete: 'off', spellcheck: 'false',
          oninput: (e) => { cur.values[k] = e.target.value; update(); } });
        return field(label, secret ? h('div', { class: 'secret-input' }, inp,
          h('button', { class: 'icon-btn', type: 'button', title: 'Show', 'aria-label': 'Show or hide', onclick: () => { inp.type = inp.type === 'password' ? 'text' : 'password'; } }, icon('eye'))) : inp,
        secret ? 'Tip: you can write {env.MY_VARIABLE} to read the value from an environment variable instead.' : null);
      }),
      (cur.extra || []).length ? h('p', { class: 'muted small' }, `${cur.extra.length} more option(s) for this provider are kept as they are (see the “Caddyfile text” tab).`) : null,
      cur.id && !p ? h('p', { class: 'muted' }, 'Unknown provider — edit its settings as Caddyfile text on the “Caddyfile text” tab.') : null);
  };
  render();
  return wrap;
}

// ---- site / snippet editor ----

function siteForm(work, ctx) {
  const titleInput = h('input', { type: 'text', value: (work.comments[0] || '').replace(/^#\s?/, ''), placeholder: 'e.g. Home Assistant',
    oninput: (e) => { const rest = work.comments.slice(1); work.comments = e.target.value.trim() ? ['# ' + e.target.value.trim(), ...rest] : rest; } });
  const sections = [field('Name', titleInput, 'Shown on the card. Saved as a # comment above the block, just like you would write it by hand.', { optional: true })];

  if (work.kind === 'site') {
    // http:// prefixes are controlled by the certificate setting below
    const addrs = addresses(work).map((a) => a.replace(/^http:\/\//, ''));
    const list = addrs.length ? addrs : [''];
    const dl = 'dl-domains-' + Math.random().toString(36).slice(2, 7);
    const certHost = h('div');
    const setHeader = (items) => {
      const http = tlsMode(work) === 'http';
      const clean = items.map((s) => s.trim().replace(/^http:\/\//, '')).filter(Boolean).map((a) => (http && !a.includes('://') ? 'http://' + a : a));
      work.header = clean.map((a, i) => quote(a) + (i < clean.length - 1 ? ',' : ''));
    };
    const suggestions = [];
    for (const d of ctx.domains || []) suggestions.push(h('option', { value: 'app.' + d }), h('option', { value: '*.' + d }));
    sections.push(
      h('datalist', { id: dl }, suggestions),
      field('Web address', listEditor(list, (items) => setHeader(items), { placeholder: 'app.' + ((ctx.domains || [])[0] || 'example.com'), addLabel: 'Add another address', min: 1, datalist: dl }),
        'The domain people type in the browser. It must point (DNS) to your Caddy server. You can also use :8080 for a port or localhost.'),
      h('h3', { class: 'form-section' }, icon('lock'), 'HTTPS certificate'),
      certHost);
    certHost.append(certSection(work, ctx, () => {}));
  } else if (work.kind === 'snippet') {
    sections.push(field('Snippet name', h('input', { type: 'text', class: 'mono', value: (work.header[0] || '').replace(/^\(|\)$/g, ''),
      oninput: (e) => { work.header = ['(' + e.target.value.trim().replace(/[()\s]/g, '') + ')']; } }), 'Sites use it with “import name”.'));
  } else if (work.kind === 'namedroute') {
    sections.push(field('Route name', h('input', { type: 'text', class: 'mono', value: work.header.join(' '), oninput: (e) => { work.header = [e.target.value.trim()]; } })));
  }

  if (work.kind === 'directive') {
    sections.push(field('Line', h('input', { type: 'text', class: 'mono', value: work.header.join(' '),
      oninput: (e) => { work.header = e.target.value.trim().split(/\s+/); } }), 'A top-level line such as “import sites/*.caddy”.'));
    return h('div', { class: 'form' }, sections);
  }

  sections.push(
    h('h3', { class: 'form-section' }, icon('sparkles'), work.kind === 'site' ? 'What this site does' : 'Contents'),
    // the certificate section above manages the tls directive
    blockEditor(work.nodes, { ...ctx, hideNode: (n) => work.kind === 'site' && name(n) === 'tls' }));
  return h('div', { class: 'form' }, sections);
}

// ---- global options editor ----

function globalForm(work, ctx) {
  const nodes = work.nodes;
  const setOpt = (nm, value, argsList) => {
    const i = nodes.findIndex((n) => name(n) === nm);
    if (value === null || value === '') { if (i >= 0) nodes.splice(i, 1); return; }
    const n = dir(nm, ...(argsList || [value]));
    if (i >= 0) { n.comment = nodes[i].comment; n.blank = nodes[i].blank; n.block = nodes[i].block; nodes[i] = n; } else nodes.unshift(n);
  };
  const email = find(nodes, 'email');
  const ca = find(nodes, 'acme_ca');
  const admin = find(nodes, 'admin');
  const caVal = ca ? args(ca)[0] : '';
  const adminVal = (admin && args(admin)[0]) || 'localhost:2019';

  const providerHost = h('div');
  const renderProvider = () => {
    const node = find(nodes, 'acme_dns');
    fill(providerHost, 
      toggle('Use a DNS provider to get certificates', !!node, (on) => {
        if (on) nodes.push(buildProvider('acme_dns', 'cloudflare', {}));
        else { const i = nodes.findIndex((n) => name(n) === 'acme_dns'); if (i >= 0) nodes.splice(i, 1); }
        renderProvider();
      }, 'Needed for wildcard certificates, or when your sites aren’t reachable from the internet on ports 80/443. Without it, Caddy uses the normal HTTP check.'),
      node ? providerForm(node, 'acme_dns', ctx, (n) => { const i = nodes.findIndex((x) => name(x) === 'acme_dns'); nodes[i] = n; }) : null);
  };
  renderProvider();

  const caSelect = select([...ACME_CAS, ...(caVal && !ACME_CAS.some(([v]) => v === caVal) ? [[caVal, caVal]] : [])], caVal, (v) => setOpt('acme_ca', v));
  const adminOpts = [['localhost:2019', 'This machine only — localhost:2019 (recommended)'], ['0.0.0.0:2019', 'Whole network — 0.0.0.0:2019 (not recommended)']];
  if (!adminOpts.some(([v]) => v === adminVal)) adminOpts.push([adminVal, adminVal]);
  const knownOpts = new Set(['email', 'acme_ca', 'admin', 'acme_dns']);
  const otherNodes = h('div');
  const renderOthers = () => fill(otherNodes, blockEditor(nodes, { ...ctx, hideNode: (n) => knownOpts.has(name(n)) }));
  renderOthers();

  return h('div', { class: 'form' },
    h('h3', { class: 'form-section' }, icon('lock'), 'Certificates'),
    field('Email for certificate notices', h('input', { type: 'email', value: email ? args(email)[0] : '', placeholder: 'you@example.com', oninput: (e) => setOpt('email', e.target.value.trim()) }),
      'Let’s Encrypt writes here if a certificate is about to expire.', { optional: true }),
    field('Certificate authority', caSelect),
    providerHost,
    h('h3', { class: 'form-section' }, icon('shield'), 'Caddy admin API'),
    field('Who may reconfigure Caddy through its API', select(adminOpts, adminVal, (v) => setOpt('admin', v === 'localhost:2019' && !admin ? null : v))),
    adminVal.startsWith('0.0.0.0') || adminVal.startsWith(':') ? h('div', { class: 'callout callout-danger' }, icon('alert'), h('div', null,
      h('strong', null, 'Anyone on your network can currently reconfigure Caddy'), ' and read its settings (including DNS API keys) through port 2019 — it has no password. CaddyWeb doesn’t need this: it talks to Caddy through its agent. Choose “This machine only”, save, and Apply.')) : null,
    adminVal === 'off' ? h('div', { class: 'callout callout-warn' }, icon('alert'), h('span', null, 'With the admin API off, “caddy reload” — and therefore CaddyWeb’s Apply — cannot work.')) : null,
    h('h3', { class: 'form-section' }, icon('settings'), 'Other global options'),
    otherNodes);
}

// ---- the drawer ----

/**
 * openEditor({seg, isNew, readOnly, ctx})
 * ctx: {base ('/api/servers/<id>'), indent, domains, snippets, dnsInstalled, dnsChecked, globalProvider,
 *       rev(), onState(state), isAdmin, role, servers:[{id,name}], serverId}
 */
export function openEditor({ seg, isNew = false, readOnly = false, ctx }) {
  const work = clone(seg);
  work.comments ||= [];
  work.header ||= [];
  work.nodes ||= [];
  let mode = 'form';
  let rawText = null;
  let rawAtSwitch = null;
  const type = work.kind === 'site' ? classify(work) : work.kind;
  const t = TYPES[type] || TYPES.custom;
  const ectx = { ...ctx, firstAddress: addresses(work)[0], getAddress: () => addresses(work)[0] };

  const body = h('div', { class: 'editor' });
  const tabs = h('div', { class: 'tabs', role: 'tablist' });
  const status = seg.status && seg.status !== 'unchanged' ? h('span', { class: 'badge badge-' + seg.status }, { new: 'New — not applied', modified: 'Changed — not applied', deleted: 'Will be removed' }[seg.status]) : null;

  const renderBody = async () => {
    fill(tabs, 
      h('button', { class: 'tab' + (mode === 'form' ? ' active' : ''), type: 'button', role: 'tab', onclick: () => switchTo('form') }, icon('edit'), readOnly ? 'Overview' : 'Settings'),
      h('button', { class: 'tab' + (mode === 'text' ? ' active' : ''), type: 'button', role: 'tab', onclick: () => switchTo('text') }, icon('code'), 'Caddyfile text'));
    fill(body, );
    if (mode === 'text') {
      if (readOnly) { body.append(codeBlock(seg.text || '', { copy: true, cls: 'code-lg' })); return; }
      const ta = h('textarea', { class: 'mono raw code-editor', spellcheck: 'false', rows: 22, oninput: (e) => { rawText = e.target.value; } }, rawText ?? '');
      body.append(h('p', { class: 'muted' }, 'This is exactly what will be written to the Caddyfile for this card. Edit freely — switching back to Settings reads your text again.'), ta);
      setTimeout(() => ta.focus(), 50);
      return;
    }
    if (readOnly) {
      body.append(readOnlyView(seg));
      return;
    }
    body.append(work.kind === 'global' ? globalForm(work, ectx) : siteForm(work, ectx));
  };

  const switchTo = async (m) => {
    if (m === mode) return;
    if (readOnly) { mode = m; renderBody(); return; }
    try {
      if (m === 'text') {
        const r = await api('POST', ctx.base + '/draft/preview', { segment: toPayload(work) });
        rawText = r.text;
        rawAtSwitch = r.text;
      } else {
        const r = await api('POST', ctx.base + '/draft/preview', { text: rawText });
        const s = r.segment;
        Object.assign(work, { kind: s.kind, comments: s.comments || [], header: s.header || [], headerComment: s.headerComment || '', nodes: s.nodes || [] });
        rawText = null;
      }
      mode = m;
      renderBody();
    } catch (e) {
      toast(e.message, 'error', 7000);
    }
  };

  const save = async (btn) => {
    btn.disabled = true;
    try {
      const rev = ctx.rev();
      let res;
      const payload = mode === 'text' ? { text: rawText } : { segment: toPayload(work) };
      if (isNew) {
        res = await api('POST', `${ctx.base}/draft/segments?rev=${rev}`, payload);
      } else if (mode === 'text') {
        res = await api('PUT', `${ctx.base}/draft/segments/${seg.id}/raw?rev=${rev}&key=${encodeURIComponent(seg.key)}`, payload);
      } else {
        res = await api('PUT', `${ctx.base}/draft/segments/${seg.id}?rev=${rev}&key=${encodeURIComponent(seg.key)}`, payload);
      }
      ctx.onState(res);
      toast(isNew ? 'Card added to your draft. Press Apply to make it live.' : 'Saved to your draft. Press Apply to make it live.', 'ok');
      d.close();
    } catch (e) {
      if (e.data && e.data.state) ctx.onState(e.data.state);
      toast(e.message, 'error', 8000);
    } finally { btn.disabled = false; }
  };

  const del = async () => {
    const ok = await confirmDialog('Remove this card?', `“${segTitle(seg)}” will be removed from the draft. It stays live until you press Apply, and you can restore it before then.`, { confirm: 'Remove', danger: true });
    if (!ok) return;
    try {
      const res = await api('DELETE', `${ctx.base}/draft/segments/${seg.id}?rev=${ctx.rev()}&key=${encodeURIComponent(seg.key)}`);
      ctx.onState(res);
      toast('Removed from the draft. Press Apply to make it live.', 'ok');
      d.close();
    } catch (e) { toast(e.message, 'error'); }
  };

  const revert = async () => {
    try {
      const res = await api('POST', `${ctx.base}/draft/segments/${seg.id}/revert?rev=${ctx.rev()}&key=${encodeURIComponent(seg.key)}`);
      ctx.onState(res);
      toast(seg.status === 'new' ? 'Discarded the new card.' : 'Card is back to what is live on the server.', 'ok');
      d.close();
    } catch (e) { toast(e.message, 'error'); }
  };

  const preview = async () => {
    try {
      const r = mode === 'text' ? await api('POST', ctx.base + '/draft/preview', { text: rawText }) : await api('POST', ctx.base + '/draft/preview', { segment: toPayload(work) });
      modal({ title: 'Caddyfile preview', wide: true, body: h('div', null, h('p', { class: 'muted' }, 'This block will be written to the Caddyfile like this:'), codeBlock(r.text, { cls: 'code-lg' })) });
    } catch (e) { toast(e.message, 'error', 8000); }
  };

  const header = h('div', { class: 'drawer-head drawer-head-' + t.color },
    h('div', { class: 'type-tile type-' + t.color }, icon(t.icon)),
    h('div', { class: 'drawer-title' },
      h('div', { class: 'eyebrow' }, isNew ? 'New ' + t.label : t.label, status),
      h('h2', null, isNew ? 'Set up your ' + t.label.toLowerCase() : segTitle(seg))),
    h('button', { class: 'icon-btn', type: 'button', 'aria-label': 'Close', onclick: () => d.requestClose() }, icon('x')));

  const saveBtn = h('button', { class: 'btn btn-primary', type: 'button' }, icon('check'), isNew ? 'Add to draft' : 'Save to draft');
  saveBtn.addEventListener('click', () => save(saveBtn));
  const copyTo = async () => {
    const others = (ctx.servers || []).filter((x) => x.id !== ctx.serverId);
    let target = others[0] ? others[0].id : '';
    modal({
      title: 'Copy to another server',
      body: h('div', { class: 'stack' },
        h('p', null, `A copy of “${segTitle(seg)}” is added to the other server’s draft. Nothing goes live there until someone applies it.`),
        field('Server', select(others.map((x) => [x.id, x.name]), target, (v) => { target = v; }))),
      actions: [{ label: 'Cancel' }, { label: 'Copy', kind: 'primary', icon: 'copy', onClick: async () => {
        try {
          const r = await api('POST', `${ctx.base}/draft/segments/${seg.id}/copy?key=${encodeURIComponent(seg.key)}`, { target });
          toast(`Copied to ${r.target}. Open that server to review and apply it.`, 'ok', 6000);
        } catch (e) { toast(e.message, 'error', 8000); return false; }
      } }],
    });
  };
  const canCopy = !isNew && seg.kind !== 'global' && ctx.role !== 'viewer' && (ctx.servers || []).length > 1 && seg.status !== 'deleted' &&
    (ctx.isAdmin || seg.kind === 'site');
  const footer = h('div', { class: 'drawer-foot' },
    !readOnly && !isNew && seg.canDelete && seg.status !== 'deleted' ? h('button', { class: 'btn btn-sm btn-danger-ghost', type: 'button', onclick: del }, icon('trash'), 'Remove') : null,
    !readOnly && !isNew && ctx.isAdmin && (seg.status === 'modified' || seg.status === 'new') ? h('button', { class: 'btn btn-sm btn-ghost', type: 'button', onclick: revert }, icon('undo'), seg.status === 'new' ? 'Discard' : 'Undo') : null,
    h('span', { class: 'spacer' }),
    canCopy ? h('button', { class: 'btn btn-sm btn-ghost', type: 'button', title: 'Copy this card to another server', onclick: copyTo }, icon('copy'), 'Copy to…') : null,
    !readOnly ? h('button', { class: 'btn btn-sm btn-ghost', type: 'button', onclick: preview }, icon('eye'), 'Preview') : null,
    h('button', { class: 'btn', type: 'button', onclick: () => d.requestClose() }, readOnly ? 'Close' : 'Cancel'),
    !readOnly ? saveBtn : null);

  const snapshot = JSON.stringify(toPayload(work));
  const dirty = () => !readOnly && (JSON.stringify(toPayload(work)) !== snapshot || (mode === 'text' && rawText !== rawAtSwitch));
  const d = drawer({ title: segTitle(seg), header: h('div', null, header, tabs), body, footer,
    beforeClose: async () => !dirty() || confirmDialog('Discard your changes?', 'You have changes in this card that aren’t saved to the draft yet.', { confirm: 'Discard changes', danger: true }) });
  if (readOnly && !seg.canEdit && ctx.role === 'power') {
    body.before(h('div', { class: 'callout callout-info drawer-note' }, icon('info'), h('span', null, 'Power users can view every card and add new ones. Only an admin can change cards that are already live.')));
  }
  renderBody();
  return d;
}

function readOnlyView(seg) {
  const s = segSummaryBlock(seg);
  return h('div', { class: 'stack' }, s, h('div', { class: 'sub-label' }, 'Caddyfile'), codeBlock(seg.text || '', { copy: true }));
}

function segSummaryBlock(seg) {
  const addrs = addresses(seg);
  return h('dl', { class: 'facts' },
    addrs.length ? [h('dt', null, 'Addresses'), h('dd', { class: 'mono' }, addrs.join(', '))] : null,
    h('dt', null, 'Lines in Caddyfile'), h('dd', null, `${seg.startLine}–${seg.endLine}`),
    seg.createdBy ? [h('dt', null, 'Added by'), h('dd', null, seg.createdBy)] : null);
}
