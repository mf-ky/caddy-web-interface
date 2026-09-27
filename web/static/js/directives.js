// Editors for individual Caddyfile directives. Each editor edits a node IN
// PLACE (tokens / block), so anything it doesn't understand — comments,
// unusual options — is left exactly as it was.

import { h, icon, field, toggle, select, api, toast, modal } from './lib.js';
import {
  quote, unquote, dir, blockDir, name, args, directives, find, findAll, splitMatcher, isMatcher,
  serializeNode, PROXY_HEADER_PRESETS, RESPONSE_HEADER_PRESETS, LB_POLICIES,
} from './caddy.js';

// ---- small building blocks ----
function input(value, onInput, attrs = {}) {
  return h('input', { type: 'text', value: value ?? '', spellcheck: 'false', autocomplete: 'off', ...attrs, oninput: (e) => onInput(e.target.value) });
}

/** Editable list of strings with add/remove buttons. */
export function listEditor(items, onChange, { placeholder = '', addLabel = 'Add', min = 0, datalist } = {}) {
  const wrap = h('div', { class: 'list-editor' });
  const render = () => {
    wrap.replaceChildren(
      ...items.map((v, i) => h('div', { class: 'list-row' },
        input(v, (nv) => { items[i] = nv; onChange(items, false); }, { placeholder, class: 'mono', list: datalist }),
        items.length > min ? h('button', { class: 'icon-btn', type: 'button', title: 'Remove', 'aria-label': 'Remove', onclick: () => { items.splice(i, 1); onChange(items, true); render(); } }, icon('x')) : null)),
      h('button', { class: 'btn btn-ghost btn-sm', type: 'button', onclick: () => { items.push(''); onChange(items, true); render(); wrap.querySelectorAll('input')[items.length - 1]?.focus(); } }, icon('plus'), addLabel));
  };
  render();
  return wrap;
}

function setMatcherAndArgs(node, matcher, rest) {
  node.tokens = [node.tokens[0], ...(matcher ? [matcher.trim().includes(' ') ? quote(matcher.trim()) : matcher.trim()] : []), ...rest.filter((x) => x !== '' && x != null).map(quote)];
}

function matcherField(node, rest, label = 'Only for these paths', hint = 'Leave empty to apply to every request. Examples: /api/*, /admin*, @name') {
  const { matcher } = splitMatcher(node);
  return field(label, input(matcher, (v) => setMatcherAndArgs(node, v.trim(), rest()), { placeholder: 'all requests', class: 'mono' }), hint, { optional: true });
}

function kids(node) { if (!node.block) node.block = []; return node.block; }
function removeWhere(nodes, pred) { for (let i = nodes.length - 1; i >= 0; i--) if (pred(nodes[i])) nodes.splice(i, 1); }

/** Generic raw editor: shows the node as Caddyfile text. */
export function rawEditor(node, ctx, { rows } = {}) {
  const text = node.__raw !== undefined ? node.__raw : serializeNode(node, ctx.indent || '\t');
  const lines = text.split('\n').length;
  return h('textarea', {
    class: 'mono raw', spellcheck: 'false', rows: rows || Math.min(Math.max(lines, 1), 18),
    oninput: (e) => { node.__raw = e.target.value; },
  }, text);
}

/** "Other options" list for children an editor doesn't handle itself. */
function otherOptions(node, known, ctx, title = 'Other options') {
  const box = h('div', { class: 'other-options' });
  const render = () => {
    const others = (node.block || []).filter((c) => c.__raw !== undefined || c.type === 'comment' || !known(c));
    box.replaceChildren(
      others.length ? h('div', { class: 'sub-label' }, title) : null,
      ...others.map((c) => h('div', { class: 'list-row' },
        c.type === 'comment'
          ? input(c.text, (v) => { c.text = v; }, { class: 'mono comment-input' })
          : rawEditor(c, ctx, { rows: 1 }),
        h('button', { class: 'icon-btn', type: 'button', title: 'Remove', 'aria-label': 'Remove', onclick: () => { node.block.splice(node.block.indexOf(c), 1); render(); } }, icon('x')))),
      h('button', { class: 'btn btn-ghost btn-sm', type: 'button', onclick: () => { kids(node).push({ type: 'directive', tokens: [], block: null, __raw: '' }); render(); } }, icon('plus'), 'Add option (Caddyfile text)'));
  };
  render();
  return box;
}

// ---- editors ----

function reverseProxyEditor(node, ctx) {
  const wrap = h('div', { class: 'stack' });
  const render = () => {
    const { matcher, rest } = splitMatcher(node);
    const ups = rest.length ? rest : [''];
    const setUps = (list) => setMatcherAndArgs(node, splitMatcher(node).matcher, list);
    const transport = findAll(node.block, 'transport').find((t) => args(t)[0] === 'http');
    const skip = !!(transport && find(transport.block, 'tls_insecure_skip_verify'));
    const pctx = { firstUpstream: ups[0] ? (ups[0].includes('://') ? ups[0] : 'http://' + ups[0]) : '', firstAddress: ctx.firstAddress };

    const known = (c) => ['header_up', 'header_down', 'lb_policy', 'health_uri', 'flush_interval'].includes(name(c)) ||
      (name(c) === 'transport' && args(c)[0] === 'http' && (c.block || []).every((x) => ['tls_insecure_skip_verify', 'tls'].includes(name(x))));

    const headerRows = () => (node.block || []).filter((c) => ['header_up', 'header_down'].includes(name(c)) && !PROXY_HEADER_PRESETS.some((p) => p.match(c)));
    const customHeaders = h('div', { class: 'stack-sm' });
    const renderHeaders = () => {
      customHeaders.replaceChildren(
        ...headerRows().map((c) => {
          const a = args(c);
          const set = (dirName, field, ...vals) => { c.tokens = [dirName, quote(field), ...vals.filter((v) => v !== '').map(quote)]; };
          return h('div', { class: 'header-row' },
            select([['header_up', 'To backend'], ['header_down', 'To visitor']], c.tokens[0], (v) => set(v, a[0] || '', ...a.slice(1))),
            input(a[0] || '', (v) => { a[0] = v; set(c.tokens[0], v, ...a.slice(1)); }, { placeholder: 'Header-Name (prefix - to remove)', class: 'mono' }),
            input(a.slice(1).join(' '), (v) => { set(c.tokens[0], a[0] || '', ...(v.trim() ? [v] : [])); }, { placeholder: 'value', class: 'mono' }),
            h('button', { class: 'icon-btn', type: 'button', 'aria-label': 'Remove', onclick: () => { node.block.splice(node.block.indexOf(c), 1); renderHeaders(); } }, icon('x')));
        }),
        h('button', { class: 'btn btn-ghost btn-sm', type: 'button', onclick: () => { kids(node).push(dir('header_up', 'X-Custom-Header', 'value')); renderHeaders(); } }, icon('plus'), 'Add header rule'));
    };
    renderHeaders();

    wrap.replaceChildren(
      field(ups.length > 1 ? 'Backends (load balanced)' : 'Send traffic to',
        listEditor(ups, (list, structural) => { setUps(list); if (structural) render(); }, { placeholder: '192.168.0.10:8080  or  https://192.168.0.10:8443', addLabel: 'Add another backend (load balancing)', min: 1 }),
        'IP or host name with port. Start with https:// if the app itself uses HTTPS.'),
      toggle('Backend uses a self-signed certificate', skip, (on) => {
        let t = findAll(node.block, 'transport').find((x) => args(x)[0] === 'http');
        if (on) {
          if (!t) { t = blockDir('transport', ['http'], []); kids(node).push(t); }
          if (!find(t.block, 'tls_insecure_skip_verify')) kids(t).push(dir('tls_insecure_skip_verify'));
        } else if (t) {
          removeWhere(t.block || [], (x) => name(x) === 'tls_insecure_skip_verify');
          if (!(t.block || []).length) node.block.splice(node.block.indexOf(t), 1);
        }
      }, 'Typical for Proxmox, NAS panels, routers, UniFi… Caddy will still encrypt the connection but won’t check the backend’s certificate.'),
      ups.length > 1 ? h('div', { class: 'grid-2' },
        field('How to share the load', select(LB_POLICIES, args(find(node.block, 'lb_policy') || dir('x', 'random'))[0], (v) => {
          removeWhere(kids(node), (c) => name(c) === 'lb_policy');
          if (v !== 'random') node.block.unshift(dir('lb_policy', v));
        })),
        field('Health check path', input(args(find(node.block, 'health_uri') || dir('x'))[0] || '', (v) => {
          removeWhere(kids(node), (c) => name(c) === 'health_uri');
          if (v.trim()) node.block.push(dir('health_uri', v.trim()));
        }, { placeholder: '/health', class: 'mono' }), 'Caddy stops sending traffic to backends that fail this check.', { optional: true })) : null,
      h('details', { class: 'section-details', open: headerRows().length > 0 || PROXY_HEADER_PRESETS.some((p) => (node.block || []).some(p.match)) },
        h('summary', null, 'Header rules'),
        h('div', { class: 'presets' }, PROXY_HEADER_PRESETS.map((p) => {
          const on = (node.block || []).some(p.match);
          return h('label', { class: 'preset' },
            h('input', { type: 'checkbox', checked: on, onchange: (e) => {
              if (e.target.checked) kids(node).push(p.make(pctx));
              else removeWhere(kids(node), p.match);
              render();
            } }),
            h('span', null, h('strong', null, p.label), h('small', null, p.desc)));
        })),
        (node.block || []).filter((c) => PROXY_HEADER_PRESETS[3].match(c)).map((c) => h('div', { class: 'grid-2 indent' },
          field('Backend address in redirects', input(args(c)[1] || '', (v) => { const a = args(c); c.tokens = ['header_down', 'Location', quote(v), quote(a[2] || '')]; }, { class: 'mono' })),
          field('Replace with', input(args(c)[2] || '', (v) => { const a = args(c); c.tokens = ['header_down', 'Location', quote(a[1] || ''), quote(v)]; }, { class: 'mono' })))),
        h('div', { class: 'sub-label' }, 'Custom header rules'),
        customHeaders),
      h('details', { class: 'section-details' },
        h('summary', null, 'Advanced'),
        toggle('Stream responses immediately', !!find(node.block, 'flush_interval'), (on) => {
          removeWhere(kids(node), (c) => name(c) === 'flush_interval');
          if (on) node.block.push(dir('flush_interval', '-1'));
        }, 'For live logs, chat, server-sent events or media servers. (WebSockets already work without this.)'),
        matcherField(node, () => splitMatcher(node).rest),
        otherOptions(node, known, ctx)),
    );
  };
  render();
  return wrap;
}

function rootEditor(node) {
  const { rest } = splitMatcher(node);
  return h('div', { class: 'stack' },
    field('Folder on the Caddy server', input(rest[0] || '', (v) => { node.tokens = ['root', '*', quote(v)]; }, { placeholder: '/srv/www', class: 'mono' }),
      'The files must be readable by the “caddy” user on the Caddy server.'));
}

function fileServerEditor(node, ctx) {
  const has = (w) => node.tokens.slice(1).includes(w) || !!find(node.block, w);
  const setFlag = (w, on) => {
    node.tokens = node.tokens.filter((t, i) => i === 0 || t !== w);
    if (node.block) removeWhere(node.block, (c) => name(c) === w);
    if (on) node.tokens.push(w);
  };
  const hide = find(node.block, 'hide');
  return h('div', { class: 'stack' },
    toggle('Show folder listings', has('browse'), (on) => setFlag('browse', on), 'Visitors see a list of files when a folder has no index.html.'),
    field('Hide these files', input(hide ? args(hide).join(' ') : '', (v) => {
      removeWhere(kids(node), (c) => name(c) === 'hide');
      const list = v.split(/[\s,]+/).filter(Boolean);
      if (list.length) node.block.push(dir('hide', ...list));
      if (!node.block.length) node.block = null;
    }, { placeholder: '.git .env *.bak', class: 'mono' }), 'Space-separated names or patterns.', { optional: true }),
    ctx.hasRoot ? null : h('div', { class: 'callout callout-warn' }, icon('info'), h('span', null, 'Add a “Site folder” behavior to choose which folder is served.')));
}

function redirEditor(node) {
  const { matcher, rest } = splitMatcher(node);
  let to = rest[0] || '';
  let code = rest[1] || '';
  const setT = () => setMatcherAndArgs(node, splitMatcher(node).matcher, [to, code]);
  return h('div', { class: 'stack' },
    field('Send visitors to', input(to, (v) => { to = v; setT(); }, { placeholder: 'https://new.example.com{uri}', class: 'mono' }),
      'Add {uri} at the end to keep the rest of the address (path and query).'),
    h('div', { class: 'grid-2' },
      field('Only for this path', input(matcher, (v) => setMatcherAndArgs(node, v.trim(), [to, code]), { placeholder: 'all requests', class: 'mono' }), 'e.g. / to redirect only the front page', { optional: true }),
      field('Kind of redirect', select([['', 'Temporary (302)'], ['permanent', 'Permanent (301)'], ['temporary', 'Temporary (302) — explicit'], ['307', 'Temporary, keep method (307)'], ['308', 'Permanent, keep method (308)'], ['301', '301'], ['302', '302'], ['html', 'HTML page redirect']], code, (v) => { code = v; setT(); }))));
}

function respondEditor(node) {
  const { matcher, rest } = splitMatcher(node);
  let body = rest.find((x) => !/^\d{3}$/.test(x)) ?? '';
  let code = rest.find((x) => /^\d{3}$/.test(x)) ?? '';
  const setT = () => setMatcherAndArgs(node, splitMatcher(node).matcher, body === '' && code ? [code] : [body, code]);
  return h('div', { class: 'stack' },
    field('Response text', h('textarea', { rows: 3, oninput: (e) => { body = e.target.value; setT(); } }, body)),
    h('div', { class: 'grid-2' },
      field('Status code', input(code, (v) => { code = v.trim(); setT(); }, { placeholder: '200', inputmode: 'numeric' }), '200 OK, 403 Forbidden, 404 Not Found, 503 Unavailable…', { optional: true }),
      field('Only for these paths', input(matcher, (v) => setMatcherAndArgs(node, v.trim(), body === '' && code ? [code] : [body, code]), { placeholder: 'all requests', class: 'mono' }), null, { optional: true })));
}

function encodeEditor(node) {
  const a = args(node);
  const set = (fmt, on) => {
    const cur = new Set(node.tokens.slice(1));
    if (on) cur.add(fmt); else cur.delete(fmt);
    node.tokens = ['encode', ...['zstd', 'gzip'].filter((f) => cur.has(f)), ...[...cur].filter((f) => !['zstd', 'gzip'].includes(f))];
  };
  return h('div', { class: 'stack' },
    h('p', { class: 'muted' }, 'Compress responses so pages load faster. Safe to leave on.'),
    h('div', { class: 'row-wrap' },
      toggle('Zstandard (modern, fastest)', a.includes('zstd') || a.length === 0, (on) => set('zstd', on)),
      toggle('Gzip (works everywhere)', a.includes('gzip') || a.length === 0, (on) => set('gzip', on))));
}

function headerEditor(node, ctx) {
  const wrap = h('div', { class: 'stack' });
  const render = () => {
    // normalise to block form so rows are easy to edit
    const { matcher, rest } = splitMatcher(node);
    if (!node.block && rest.length) {
      node.block = [{ type: 'directive', tokens: rest.map(quote), block: null }];
      node.tokens = ['header', ...(matcher ? [matcher] : [])];
    }
    if (!node.block) node.block = [];
    const rows = node.block.filter((c) => c.type === 'directive' && c.__raw === undefined);
    const presetOn = (p) => rows.some((c) => unquote(c.tokens[0]).toLowerCase() === p.field.toLowerCase() && (p.value === '' || args({ tokens: ['x', ...c.tokens.slice(1)] }).join(' ') === p.value));
    wrap.replaceChildren(
      h('p', { class: 'muted' }, 'Headers added to every response sent to visitors.'),
      h('div', { class: 'presets' }, RESPONSE_HEADER_PRESETS.map((p) => h('label', { class: 'preset' },
        h('input', { type: 'checkbox', checked: presetOn(p), onchange: (e) => {
          removeWhere(node.block, (c) => c.type === 'directive' && unquote(c.tokens[0] || '').toLowerCase() === p.field.toLowerCase());
          if (e.target.checked) node.block.push({ type: 'directive', tokens: [quote(p.field), ...(p.value ? [quote(p.value)] : [])], block: null });
          render();
        } }),
        h('span', null, h('strong', null, p.label), h('small', null, p.desc))))),
      h('div', { class: 'sub-label' }, 'All header rules'),
      ...rows.map((c) => {
        const f = unquote(c.tokens[0] || '');
        const v = c.tokens.slice(1).map(unquote).join(' ');
        return h('div', { class: 'header-row header-row-2' },
          input(f, (nv) => { c.tokens = [quote(nv), ...c.tokens.slice(1)]; }, { placeholder: 'Header-Name (-Name removes it)', class: 'mono' }),
          input(v, (nv) => { c.tokens = [c.tokens[0], ...(nv ? [quote(nv)] : [])]; }, { placeholder: 'value', class: 'mono' }),
          h('button', { class: 'icon-btn', type: 'button', 'aria-label': 'Remove', onclick: () => { node.block.splice(node.block.indexOf(c), 1); render(); } }, icon('x')));
      }),
      h('button', { class: 'btn btn-ghost btn-sm', type: 'button', onclick: () => { node.block.push({ type: 'directive', tokens: ['X-Custom', 'value'], block: null }); render(); } }, icon('plus'), 'Add header'),
      matcherField(node, () => []));
  };
  render();
  return wrap;
}

function basicAuthEditor(node, ctx) {
  const wrap = h('div', { class: 'stack' });
  if (!node.block) node.block = [];
  const render = () => {
    const users = node.block.filter((c) => c.type === 'directive' && c.__raw === undefined);
    wrap.replaceChildren(
      h('p', { class: 'muted' }, 'Visitors must enter a username and password before they see the site. Passwords are stored as secure hashes, never in plain text.'),
      ...users.map((c) => h('div', { class: 'header-row header-row-2' },
        input(unquote(c.tokens[0]), (v) => { c.tokens[0] = quote(v); }, { placeholder: 'username' }),
        h('button', { class: 'btn btn-sm', type: 'button', onclick: () => setPassword(c) }, icon('key'), c.tokens[1] ? 'Change password' : 'Set password'),
        h('button', { class: 'icon-btn', type: 'button', 'aria-label': 'Remove', onclick: () => { node.block.splice(node.block.indexOf(c), 1); render(); } }, icon('x')))),
      h('button', { class: 'btn btn-ghost btn-sm', type: 'button', onclick: () => { const c = { type: 'directive', tokens: ['user'], block: null }; node.block.push(c); render(); setPassword(c); } }, icon('plus'), 'Add login'),
      matcherField(node, () => []));
  };
  const setPassword = (c) => {
    const pw = h('input', { type: 'password', autocomplete: 'new-password' });
    modal({
      title: 'Password for ' + unquote(c.tokens[0]),
      body: field('Password', pw, 'It is hashed with bcrypt before being written to the Caddyfile.'),
      actions: [{ label: 'Cancel' }, { label: 'Set password', kind: 'primary', onClick: async () => {
        if (pw.value.length < 1) return false;
        try {
          const r = await api('POST', '/api/tools/hash-password', { password: pw.value });
          c.tokens = [c.tokens[0], r.hash];
          toast('Password set — remember to Save the card', 'info');
          render();
        } catch (e) { toast(e.message, 'error'); return false; }
      } }],
    });
  };
  render();
  return wrap;
}

function blockDirectiveEditor(label, hint) {
  return (node, ctx) => {
    const { matcher } = splitMatcher(node);
    if (!node.block) node.block = [];
    return h('div', { class: 'stack' },
      field(label, input(node.tokens.slice(1).map(unquote).join(' '), (v) => {
        const parts = v.trim().split(/\s+/).filter(Boolean);
        node.tokens = [node.tokens[0], ...parts.map((p) => (isMatcher(p) || p.startsWith('@') ? p : quote(p)))];
      }, { placeholder: node.tokens[0] === 'handle_path' ? '/api/*' : 'all other requests', class: 'mono' }), hint, { optional: node.tokens[0] !== 'handle_path' }),
      h('div', { class: 'nested' }, ctx.blockEditor(node.block, { ...ctx, depth: (ctx.depth || 0) + 1 })));
  };
}

function simpleArgsEditor(label, placeholder, hint) {
  return (node) => h('div', { class: 'stack' },
    field(label, input(node.tokens.slice(1).map(unquote).join(' '), (v) => {
      const parts = v.match(/"[^"]*"|\S+/g) || [];
      node.tokens = [node.tokens[0], ...parts.map((p) => (p.startsWith('"') ? p : quote(p)))];
    }, { placeholder, class: 'mono' }), hint),
    node.block && node.block.length ? h('div', { class: 'callout' }, icon('info'), h('span', null, 'This directive has extra options — use “Edit as text” to change them.')) : null);
}

function importEditor(node, ctx) {
  const cur = args(node)[0] || '';
  const opts = [...ctx.snippets.map((s) => [s, 'Snippet: ' + s])];
  if (cur && !ctx.snippets.includes(cur)) opts.unshift([cur, cur]);
  return h('div', { class: 'stack' },
    opts.length
      ? field('Snippet to include', select([['', 'Choose…'], ...opts], cur, (v) => { node.tokens = ['import', v, ...node.tokens.slice(2)]; }))
      : h('p', { class: 'muted' }, 'No snippets defined yet. Create one with New → Snippet.'),
    field('Arguments', input(node.tokens.slice(2).map(unquote).join(' '), (v) => { node.tokens = ['import', node.tokens[1] || '', ...(v.match(/"[^"]*"|\S+/g) || []).map((p) => (p.startsWith('"') ? p : quote(p)))]; }, { class: 'mono' }), 'Values passed to the snippet as {args[0]}, {args[1]}…', { optional: true }));
}

function matcherDefEditor(node, ctx) {
  const nm = node.tokens[0];
  const body = node.block ? node.block : null;
  const text = body ? body.map((c) => serializeNode(c)).join('\n') : node.tokens.slice(1).join(' ');
  return h('div', { class: 'stack' },
    field('Name', input(nm, (v) => { node.tokens[0] = v.startsWith('@') ? v : '@' + v; }, { class: 'mono' }), 'Use it in other directives by writing ' + nm + '.'),
    field('Conditions', h('textarea', { class: 'mono raw', rows: 3, spellcheck: 'false', oninput: (e) => {
      const v = e.target.value.trim();
      if (v.includes('\n')) { node.block = [{ type: 'directive', tokens: [], block: null, __raw: v }]; node.tokens = [node.tokens[0]]; }
      else { node.block = null; node.tokens = [node.tokens[0], ...(v.match(/"[^"]*"|`[^`]*`|\S+/g) || [])]; }
    } }, text), 'One condition per line, e.g. “path /admin/*”, “remote_ip 192.168.0.0/16”, “not remote_ip 10.0.0.0/8”, “header X-Api-Key secret”.'));
}

function logEditor(node) {
  const out = find(node.block, 'output');
  const file = out && args(out)[0] === 'file' ? args(out)[1] : '';
  return h('div', { class: 'stack' },
    h('p', { class: 'muted' }, 'Record every visit (time, address, path, status). By default logs go to Caddy’s journal: journalctl -u caddy.'),
    field('Write to a file instead', input(file, (v) => {
      if (!node.block) node.block = [];
      removeWhere(node.block, (c) => name(c) === 'output');
      if (v.trim()) node.block.push(dir('output', 'file', v.trim()));
      if (!node.block.length) node.block = null;
    }, { placeholder: '/var/log/caddy/access.log', class: 'mono' }), 'Folder must be writable by the caddy user.', { optional: true }));
}

function forwardAuthEditor(node, ctx) {
  const { rest } = splitMatcher(node);
  const uri = find(node.block, 'uri');
  const copy = find(node.block, 'copy_headers');
  return h('div', { class: 'stack' },
    h('p', { class: 'muted' }, 'Ask a login service (Authelia, Authentik, …) whether the visitor may enter before showing the site.'),
    field('Auth service', input(rest[0] || '', (v) => setMatcherAndArgs(node, splitMatcher(node).matcher, [v]), { placeholder: 'authelia:9091', class: 'mono' })),
    field('Verify path', input(uri ? args(uri)[0] : '', (v) => { if (!node.block) node.block = []; removeWhere(node.block, (c) => name(c) === 'uri'); if (v) node.block.unshift(dir('uri', v)); }, { placeholder: '/api/authz/forward-auth', class: 'mono' })),
    field('Copy these headers to the app', input(copy ? args(copy).join(' ') : '', (v) => { if (!node.block) node.block = []; removeWhere(node.block, (c) => name(c) === 'copy_headers'); const l = v.split(/\s+/).filter(Boolean); if (l.length) node.block.push(dir('copy_headers', ...l)); }, { placeholder: 'Remote-User Remote-Groups Remote-Email', class: 'mono' }), null, { optional: true }));
}

function uriEditor(node) {
  const a = args(node);
  const ops = [['strip_prefix', 'Remove a prefix from the path'], ['strip_suffix', 'Remove a suffix'], ['replace', 'Replace text'], ['path_regexp', 'Replace with a regular expression']];
  let op = a[0] && !isMatcher(node.tokens[1]) ? a[0] : (a[1] || 'strip_prefix');
  const matcher = isMatcher(node.tokens[1]) ? node.tokens[1] : '';
  let rest = a.slice(matcher ? 2 : 1);
  const setT = () => { node.tokens = ['uri', ...(matcher ? [matcher] : []), op, ...rest.filter(Boolean).map(quote)]; };
  return h('div', { class: 'stack' },
    field('Change the path by', select(ops, op, (v) => { op = v; setT(); })),
    field('Values', input(rest.join(' '), (v) => { rest = v.split(/\s+/); setT(); }, { placeholder: '/api', class: 'mono' }), 'For replace: the text to find, then its replacement.'));
}

// Registry. `desc` shows in the "Add behavior" menu and on the item header.
export const DIRECTIVES = {
  reverse_proxy: { label: 'Reverse proxy', icon: 'proxy', group: 'Serve', desc: 'Forward requests to another app or server.', make: () => dir('reverse_proxy', '192.168.0.10:8080'), editor: reverseProxyEditor },
  root: { label: 'Site folder', icon: 'folder', group: 'Serve', desc: 'Which folder files are served from.', make: () => dir('root', '*', '/srv/www'), editor: rootEditor },
  file_server: { label: 'Serve files', icon: 'folder', group: 'Serve', desc: 'Serve static files from the site folder.', make: () => dir('file_server'), editor: fileServerEditor },
  php_fastcgi: { label: 'PHP', icon: 'php', group: 'Serve', desc: 'Run .php files through PHP-FPM.', make: () => dir('php_fastcgi', 'unix//run/php/php-fpm.sock'), editor: simpleArgsEditor('PHP-FPM address', 'unix//run/php/php-fpm.sock  or  127.0.0.1:9000', 'Where PHP-FPM listens.') },
  respond: { label: 'Fixed response', icon: 'message', group: 'Serve', desc: 'Reply with fixed text and a status code.', make: () => dir('respond', 'OK', '200'), editor: respondEditor },
  redir: { label: 'Redirect', icon: 'redirect', group: 'Routing', desc: 'Send visitors to another address.', make: () => dir('redir', '/', 'permanent'), editor: redirEditor },
  handle_path: { label: 'Path route (strip prefix)', icon: 'branch', group: 'Routing', desc: 'Handle one path separately, removing the prefix before passing it on.', make: () => blockDir('handle_path', ['/api/*'], [dir('reverse_proxy', '192.168.0.10:3000')]), editor: blockDirectiveEditor('Path', 'The prefix (e.g. /api) is removed before the request is passed on.') },
  handle: { label: 'Path route', icon: 'branch', group: 'Routing', desc: 'Handle matching requests separately (only the first matching route runs).', make: () => blockDir('handle', ['/app/*'], [dir('reverse_proxy', '192.168.0.10:8080')]), editor: blockDirectiveEditor('Path or matcher', 'Leave empty for “everything else”.') },
  route: { label: 'Ordered group', icon: 'layers', group: 'Routing', desc: 'Run the directives inside exactly in the order written.', make: () => blockDir('route', [], []), editor: blockDirectiveEditor('Path or matcher', null) },
  rewrite: { label: 'Rewrite', icon: 'redirect', group: 'Routing', desc: 'Change the request path internally (the visitor doesn’t see it).', make: () => dir('rewrite', '/old', '/new'), editor: simpleArgsEditor('Matcher and new path', '/old  /new', 'e.g. “/ /index.html” or “* /app{uri}”.') },
  uri: { label: 'Change path', icon: 'edit', group: 'Routing', desc: 'Strip a prefix or replace part of the path.', make: () => dir('uri', 'strip_prefix', '/api'), editor: uriEditor },
  try_files: { label: 'Try files', icon: 'file', group: 'Routing', desc: 'Look for files in order; used for single-page apps.', make: () => dir('try_files', '{path}', '/index.html'), editor: simpleArgsEditor('Files to try, in order', '{path} /index.html', '{path} is the requested file.') },
  basic_auth: { label: 'Password protection', icon: 'lock', group: 'Security', desc: 'Ask for a username and password.', make: () => blockDir('basic_auth', [], []), editor: basicAuthEditor },
  basicauth: { label: 'Password protection', icon: 'lock', group: 'Security', hidden: true, editor: basicAuthEditor },
  forward_auth: { label: 'Single sign-on (forward auth)', icon: 'shield', group: 'Security', desc: 'Let Authelia/Authentik decide who gets in.', make: () => { const n = dir('forward_auth', 'authelia:9091'); n.block = [dir('uri', '/api/authz/forward-auth'), dir('copy_headers', 'Remote-User', 'Remote-Groups', 'Remote-Email', 'Remote-Name')]; return n; }, editor: forwardAuthEditor },
  header: { label: 'Response headers', icon: 'shield', group: 'Security', desc: 'Security headers, caching, CORS…', make: () => { const n = dir('header'); n.block = [{ type: 'directive', tokens: ['X-Content-Type-Options', 'nosniff'], block: null }, { type: 'directive', tokens: ['Referrer-Policy', 'strict-origin-when-cross-origin'], block: null }, { type: 'directive', tokens: ['-Server'], block: null }]; return n; }, editor: headerEditor },
  request_body: { label: 'Upload size limit', icon: 'archive', group: 'Security', desc: 'Refuse uploads larger than a limit.', make: () => { const n = dir('request_body'); n.block = [dir('max_size', '100MB')]; return n; }, editor: (node) => h('div', { class: 'stack' }, field('Maximum size', input(args(find(node.block, 'max_size') || dir('x'))[0] || '', (v) => { node.block = v ? [dir('max_size', v)] : []; }, { placeholder: '100MB', class: 'mono' }), 'e.g. 10MB, 1GB')) },
  encode: { label: 'Compression', icon: 'zap', group: 'Performance', desc: 'Make pages smaller and faster to load.', make: () => dir('encode', 'zstd', 'gzip'), editor: encodeEditor },
  log: { label: 'Access log', icon: 'history', group: 'Other', desc: 'Record every request.', make: () => dir('log'), editor: logEditor },
  import: { label: 'Use a snippet', icon: 'snippet', group: 'Other', desc: 'Include a reusable snippet.', make: () => dir('import', 'common'), editor: importEditor },
  abort: { label: 'Drop connection', icon: 'x', group: 'Other', desc: 'Close the connection without answering.', make: () => dir('abort', '/wp-admin*'), editor: simpleArgsEditor('For these paths', '/wp-admin*', 'Leave empty to drop every request.') },
  templates: { label: 'Templates', icon: 'code', group: 'Other', desc: 'Process files as Go templates.', make: () => dir('templates'), editor: () => h('p', { class: 'muted' }, 'Files are rendered as templates before being sent. No settings needed.') },
  metrics: { label: 'Metrics endpoint', icon: 'zap', group: 'Other', desc: 'Expose Prometheus metrics at this address.', make: () => dir('metrics', '/metrics'), editor: simpleArgsEditor('Path', '/metrics', null) },
};

// Behaviors that insert several nodes at once.
export const RECIPES = [
  { id: 'lan_only', label: 'Allow only my local network', icon: 'shield', group: 'Security', desc: 'Everyone outside your LAN gets “403 Forbidden”.',
    make: () => [dir('@outside', 'not', 'remote_ip', 'private_ranges'), dir('respond', '@outside', 'Forbidden', '403')] },
  { id: 'redirect_root', label: 'Send the front page to a path', icon: 'redirect', group: 'Routing', desc: 'e.g. visiting / goes to /admin/ (like Pi-hole).',
    make: () => [dir('redir', '/', '/admin/', '301')] },
  { id: 'security_headers', label: 'Recommended security headers', icon: 'shield', group: 'Security', desc: 'HSTS, no-sniff, frame and referrer protection.',
    make: () => { const n = dir('header'); n.block = ['Strict-Transport-Security|max-age=31536000; includeSubDomains', 'X-Content-Type-Options|nosniff', 'X-Frame-Options|SAMEORIGIN', 'Referrer-Policy|strict-origin-when-cross-origin', '-Server|'].map((s) => { const [f, v] = s.split('|'); return { type: 'directive', tokens: [quote(f), ...(v ? [quote(v)] : [])], block: null }; }); return [n]; } },
];

export function describe(node) {
  if (node.type === 'comment') return { label: 'Note', icon: 'info', desc: '' };
  if (node.__raw !== undefined) return { label: 'Caddyfile text', icon: 'code', desc: '' };
  const nm = name(node);
  if (nm.startsWith('@')) return { label: 'Condition ' + nm, icon: 'filter', desc: 'A named matcher other directives can refer to.', editor: matcherDefEditor };
  const d = DIRECTIVES[nm];
  if (d) return d;
  return { label: nm, icon: 'code', desc: 'Edited as Caddyfile text.' };
}
