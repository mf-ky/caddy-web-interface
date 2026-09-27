// Caddyfile knowledge for the UI: quoting, node helpers, card classification
// and the catalogs (templates, header presets, DNS providers).
//
// A "node" mirrors internal/caddyfile.Node: {type:'directive', tokens:[...raw],
// block:[...]|null, comment, blank} or {type:'comment', text}. Tokens are RAW
// Caddyfile text (quotes included), so always go through quote()/unquote().

export function quote(v) {
  v = String(v ?? '');
  if (v === '') return '""';
  if (/^[^\s"'`#{}]+$/.test(v) || /^\{[^\s{}]+\}$/.test(v)) return v;
  if (/^[^\s"`#]+$/.test(v) && !/^[{}]$/.test(v) && !v.startsWith('#')) return v; // e.g. {host}:443 or /path/{x}
  // Inside quotes Caddy only treats \" as an escape; backslashes stay as typed.
  return '"' + v.replace(/"/g, '\\"') + '"';
}

export function unquote(t) {
  if (t == null) return '';
  if (t.length >= 2 && t[0] === '"' && t[t.length - 1] === '"') return t.slice(1, -1).replace(/\\"/g, '"');
  if (t.length >= 2 && t[0] === '`' && t[t.length - 1] === '`') return t.slice(1, -1);
  return t;
}

export const isMatcher = (t) => t === '*' || (t && (t.startsWith('/') || t.startsWith('@')));

export function dir(name, ...args) {
  return { type: 'directive', tokens: [name, ...args.filter((a) => a !== undefined && a !== null && a !== '').map(quote)], block: null };
}

export function blockDir(name, args, children) {
  const n = dir(name, ...(args || []));
  n.block = children || [];
  return n;
}

export const name = (n) => (n && n.type === 'directive' ? n.tokens[0] : '');
export const args = (n) => n.tokens.slice(1).map(unquote);
export const directives = (nodes) => (nodes || []).filter((n) => n.type === 'directive' && n.__raw === undefined);
export const find = (nodes, nm) => directives(nodes).find((n) => n.tokens[0] === nm);
export const findAll = (nodes, nm) => directives(nodes).filter((n) => n.tokens[0] === nm);

/** splitMatcher(node) → {matcher, rest[]} for directives whose first arg may be a matcher */
export function splitMatcher(node) {
  if (!node) return { matcher: '', rest: [] };
  const t = node.tokens.slice(1);
  let matcher = '';
  // "root /srv" has one argument, which is the path — not a matcher
  const single = ['root', 'redir', 'rewrite', 'try_files'].includes(node.tokens[0]) && t.length === 1;
  if (t.length && isMatcher(t[0]) && !single) matcher = t.shift();
  return { matcher, rest: t.map(unquote) };
}

/** Find a directive here or inside handle/handle_path/route blocks. */
export function findDeep(nodes, nm) {
  const direct = find(nodes, nm);
  if (direct) return direct;
  for (const n of directives(nodes)) {
    if (['handle', 'handle_path', 'route'].includes(n.tokens[0]) && n.block) {
      const inner = findDeep(n.block, nm);
      if (inner) return inner;
    }
  }
  return undefined;
}

// ---- serialization (mirrors the Go formatter) ----
export function serializeNodes(nodes, indent = '\t', depth = 0) {
  let out = '';
  const pad = indent.repeat(depth);
  (nodes || []).forEach((n, i) => {
    if (n.blank && i > 0) out += '\n';
    if (n.__raw !== undefined) {
      out += n.__raw.split('\n').map((l) => (l.trim() ? pad + l.replace(/^\s+/, '') : l)).join('\n').replace(/\s*$/, '') + '\n';
      return;
    }
    if (n.type === 'comment') { out += pad + n.text + '\n'; return; }
    out += pad + n.tokens.join(' ');
    if (n.block) out += ' {';
    if (n.comment) out += ' ' + n.comment;
    out += '\n';
    if (n.block) out += serializeNodes(n.block, indent, depth + 1) + pad + '}\n';
  });
  return out;
}

export function serializeNode(n, indent = '\t') { return serializeNodes([{ ...n, blank: false }], indent, 0).replace(/\n$/, ''); }

// Directives whose block is meaningful even when empty.
const KEEP_EMPTY_BLOCK = new Set(['handle', 'handle_path', 'route']);

/** Convert the UI's working copy into what the API expects. */
export function toPayload(seg) {
  const conv = (nodes) => (nodes || []).map((n) => {
    if (n.__raw !== undefined) return { raw: n.__raw, blank: !!n.blank };
    if (n.type === 'comment') return { type: 'comment', text: n.text, blank: !!n.blank };
    const keep = n.block && (n.block.length || KEEP_EMPTY_BLOCK.has(n.tokens[0]));
    return { type: 'directive', tokens: n.tokens.filter((t) => t !== ''), block: keep ? conv(n.block) : null, comment: n.comment || '', blank: !!n.blank };
  });
  return {
    kind: seg.kind,
    comments: (seg.comments || []).filter((c) => c.trim()),
    header: (seg.header || []).filter((h) => h.trim()),
    headerComment: seg.headerComment || '',
    nodes: conv(seg.nodes),
  };
}

export function clone(o) { return JSON.parse(JSON.stringify(o)); }

// ---- card classification ----
export const TYPES = {
  proxy: { label: 'Reverse Proxy', icon: 'proxy', color: 'proxy' },
  loadbalancer: { label: 'Load Balancer', icon: 'scale', color: 'proxy' },
  files: { label: 'File Server', icon: 'folder', color: 'files' },
  spa: { label: 'Web App (SPA)', icon: 'layers', color: 'files' },
  redirect: { label: 'Redirect', icon: 'redirect', color: 'redirect' },
  respond: { label: 'Static Response', icon: 'message', color: 'respond' },
  php: { label: 'PHP Site', icon: 'php', color: 'php' },
  routes: { label: 'Path Routes', icon: 'branch', color: 'routes' },
  custom: { label: 'Custom Site', icon: 'code', color: 'raw' },
  snippet: { label: 'Snippet', icon: 'snippet', color: 'raw' },
  namedroute: { label: 'Named Route', icon: 'branch', color: 'raw' },
  directive: { label: 'Import', icon: 'file', color: 'raw' },
  global: { label: 'Global Settings', icon: 'settings', color: 'global' },
};

export function upstreamsOf(rp) {
  if (!rp) return [];
  const { rest } = splitMatcher(rp);
  const ups = [...rest];
  for (const to of findAll(rp.block, 'to')) ups.push(...args(to));
  return ups;
}

export function classify(seg) {
  if (seg.kind !== 'site') return seg.kind;
  const ds = directives(seg.nodes);
  const names = ds.map((d) => d.tokens[0]);
  const handles = ds.filter((d) => ['handle', 'handle_path', 'route'].includes(d.tokens[0]));
  if (handles.length >= 2 || (handles.length === 1 && !names.includes('reverse_proxy') && !names.includes('file_server') && handles[0].tokens.length > 1)) return 'routes';
  const rp = find(seg.nodes, 'reverse_proxy');
  if (rp) return upstreamsOf(rp).length > 1 ? 'loadbalancer' : 'proxy';
  if (names.includes('php_fastcgi')) return 'php';
  if (names.includes('file_server')) return names.includes('try_files') ? 'spa' : 'files';
  if (names.includes('redir')) return 'redirect';
  if (names.includes('respond')) return 'respond';
  if (handles.length === 1) {
    const inner = classify({ kind: 'site', nodes: handles[0].block || [] });
    if (inner !== 'custom') return inner;
  }
  return 'custom';
}

export function segTitle(seg) {
  const c = (seg.comments || []).map((x) => x.replace(/^#\s?/, '').trim()).filter(Boolean);
  if (c.length) return c[0];
  if (seg.kind === 'global') return 'Global settings';
  if (seg.kind === 'snippet') return 'Snippet ' + seg.header.join(' ');
  if (seg.kind === 'directive') return seg.header.join(' ');
  return (seg.header || []).map(unquote).join(', ').replace(/,+/g, ',');
}

export function addresses(seg) {
  return (seg.header || []).flatMap((h) => h.split(',')).map((s) => unquote(s.trim())).filter(Boolean);
}

/** A human summary for the card face: {targets:[{icon,text,mono}], chips:[]} */
export function summarize(seg) {
  const type = seg.kind === 'site' ? classify(seg) : seg.kind;
  const targets = [];
  const chips = [];
  const ds = directives(seg.nodes);
  const walk = (nodes, fn, parents = []) => (nodes || []).forEach((n) => { fn(n, parents); if (n.block) walk(n.block, fn, [...parents, n]); });

  const deep = (nm) => (type === 'routes' ? find(seg.nodes, nm) : findDeep(seg.nodes, nm));
  const rp = deep('reverse_proxy');
  if (type === 'proxy' || type === 'loadbalancer') {
    for (const u of upstreamsOf(rp)) targets.push({ icon: 'server', text: u });
  } else if (type === 'files' || type === 'spa') {
    const root = deep('root');
    targets.push({ icon: 'folder', text: root ? splitMatcher(root).rest[0] || '(current folder)' : '(current folder)' });
  } else if (type === 'php') {
    const root = deep('root');
    const php = deep('php_fastcgi');
    if (root) targets.push({ icon: 'folder', text: splitMatcher(root).rest[0] || '(current folder)' });
    if (php) targets.push({ icon: 'server', text: splitMatcher(php).rest.join(' ') });
  } else if (type === 'redirect') {
    const r = deep('redir');
    const { matcher, rest } = splitMatcher(r);
    targets.push({ icon: 'redirect', text: (matcher ? matcher + ' → ' : '') + (rest[0] || '') });
    if (rest[1]) chips.push(codeLabel(rest[1]));
  } else if (type === 'respond') {
    const r = deep('respond');
    const { rest } = splitMatcher(r);
    const body = rest.find((x) => !/^\d{3}$/.test(x));
    const code = rest.find((x) => /^\d{3}$/.test(x));
    const shown = body && body.startsWith('<<') ? 'multi-line text' : body;
    targets.push({ icon: 'message', text: shown ? '“' + (shown.length > 40 ? shown.slice(0, 40) + '…' : shown) + '”' : 'Empty response', mono: false });
    if (code) chips.push('Status ' + code);
  } else if (type === 'routes') {
    for (const hd of ds.filter((d) => ['handle', 'handle_path', 'route'].includes(d.tokens[0]))) {
      const path = hd.tokens.slice(1).map(unquote).join(' ') || 'everything else';
      const inner = find(hd.block, 'reverse_proxy');
      const fsrv = find(hd.block, 'file_server');
      const rd = find(hd.block, 'redir');
      const rs = find(hd.block, 'respond');
      let what = '…';
      if (inner) what = upstreamsOf(inner).join(', ');
      else if (fsrv) { const r = find(hd.block, 'root'); what = 'files ' + (r ? splitMatcher(r).rest[0] : ''); }
      else if (rd) what = '↪ ' + (splitMatcher(rd).rest[0] || '');
      else if (rs) what = 'responds “' + (splitMatcher(rs).rest[0] || '') + '”';
      targets.push({ icon: 'branch', text: path + '  →  ' + what });
    }
    if (rp) for (const u of upstreamsOf(rp)) targets.push({ icon: 'server', text: 'everything else → ' + u });
  } else if (type === 'directive') {
    targets.push({ icon: 'file', text: seg.header.map(unquote).join(' ') });
  } else if (type === 'snippet' || type === 'namedroute') {
    targets.push({ icon: 'snippet', text: ds.length + ' directive' + (ds.length === 1 ? '' : 's'), mono: false });
  } else if (type === 'custom') {
    const list = [...new Set(ds.map((d) => d.tokens[0]))].slice(0, 4).join(', ');
    targets.push({ icon: 'code', text: list || 'empty', mono: true });
  }

  // extra features become chips
  if (rp && type !== 'routes') {
    if (rp.block) {
      const hu = findAll(rp.block, 'header_up').length + findAll(rp.block, 'header_down').length;
      if (hu) chips.push(hu + ' header rule' + (hu === 1 ? '' : 's'));
      if (upstreamsOf(rp).length > 1) {
        const lb = find(rp.block, 'lb_policy');
        chips.push(lb ? 'Balancing: ' + args(lb)[0] : 'Balancing: random');
      }
    }
  }
  let skip = false;
  walk(seg.nodes, (n) => { if (name(n) === 'tls_insecure_skip_verify') skip = true; });
  if (skip) chips.push('Self-signed backend');
  if (type !== 'redirect') {
    for (const r of findAll(seg.nodes, 'redir')) {
      const { matcher, rest } = splitMatcher(r);
      chips.push('↪ ' + (matcher || '') + ' → ' + (rest[0] || ''));
    }
  }
  if (find(seg.nodes, 'encode')) chips.push('Compression');
  if (find(seg.nodes, 'basic_auth') || find(seg.nodes, 'basicauth')) chips.push('Password protected');
  if (find(seg.nodes, 'forward_auth')) chips.push('Forward auth');
  if (find(seg.nodes, 'header')) chips.push('Response headers');
  if (find(seg.nodes, 'log')) chips.push('Access log');
  let ipr = false;
  walk(seg.nodes, (n) => { if (name(n).startsWith('@') && (n.tokens.includes('remote_ip') || n.tokens.includes('client_ip') || (n.block || []).some((c) => ['remote_ip', 'client_ip'].includes(name(c))))) ipr = true; });
  if (ipr) chips.push('IP restricted');
  for (const imp of findAll(seg.nodes, 'import')) chips.push('Snippet ' + args(imp)[0]);
  const tls = find(seg.nodes, 'tls');
  if (tls) {
    const a = args(tls);
    if (a[0] === 'internal') chips.push('Internal certificate');
    else if (tls.block && find(tls.block, 'dns')) chips.push('DNS cert: ' + args(find(tls.block, 'dns'))[0]);
    else if (a.length === 2) chips.push('Own certificate');
    else chips.push('Custom TLS');
  }
  if (seg.kind === 'site' && addresses(seg).length && addresses(seg).every((a) => a.startsWith('http://'))) chips.push('HTTP only');
  return { type, targets, chips };
}

function codeLabel(c) {
  return { permanent: '301 permanent', temporary: '302 temporary', html: 'HTML redirect' }[c] || 'Status ' + c;
}

// ---- catalog: what "New" can create ----
export function templates(ctx) {
  const d = ctx.domains && ctx.domains[0] ? ctx.domains[0] : 'example.com';
  const site = (sub, nodes, title) => ({ kind: 'site', comments: ['# ' + title], header: [sub + '.' + d], headerComment: '', nodes });
  return [
    { id: 'proxy', ...TYPES.proxy, desc: 'Send visitors of a domain to an app running on another machine or port. The most common choice.',
      make: () => site('app', [dir('reverse_proxy', '192.168.0.10:8080')], 'New app') },
    { id: 'loadbalancer', ...TYPES.loadbalancer, desc: 'Spread visitors across several copies of the same app, with health checks.',
      make: () => { const n = dir('reverse_proxy', '192.168.0.10:8080', '192.168.0.11:8080'); n.block = [dir('lb_policy', 'round_robin'), dir('health_uri', '/')]; return site('app', [n], 'Load-balanced app'); } },
    { id: 'files', ...TYPES.files, desc: 'Serve a folder of files (a static website, downloads, media) straight from the Caddy server.',
      make: () => site('files', [dir('root', '*', '/srv/www'), dir('file_server')], 'Static files') },
    { id: 'spa', ...TYPES.spa, desc: 'Serve a single-page app (React, Vue, …): unknown paths fall back to index.html.',
      make: () => site('web', [dir('root', '*', '/srv/app'), dir('encode', 'zstd', 'gzip'), dir('try_files', '{path}', '/index.html'), dir('file_server')], 'Web app') },
    { id: 'redirect', ...TYPES.redirect, desc: 'Send everyone who visits this domain to another address.',
      make: () => site('old', [dir('redir', 'https://www.' + d + '{uri}', 'permanent')], 'Redirect') },
    { id: 'respond', ...TYPES.respond, desc: 'Answer with a fixed text and status code. Handy for maintenance pages or health checks.',
      make: () => site('status', [dir('respond', 'OK', '200')], 'Status page') },
    { id: 'php', ...TYPES.php, desc: 'Run a PHP application through PHP-FPM (WordPress, Nextcloud, …).',
      make: () => site('php', [dir('root', '*', '/var/www/html'), dir('php_fastcgi', 'unix//run/php/php-fpm.sock'), dir('file_server')], 'PHP site') },
    { id: 'routes', ...TYPES.routes, desc: 'Send different paths of one domain to different places (e.g. /api to one app, the rest to another).',
      make: () => site('app', [blockDir('handle_path', ['/api/*'], [dir('reverse_proxy', '192.168.0.10:3000')]), blockDir('handle', [], [dir('reverse_proxy', '192.168.0.10:8080')])], 'Routed app') },
    { id: 'custom', ...TYPES.custom, desc: 'Start from an empty site and add behaviors yourself, or write Caddyfile text directly.',
      make: () => site('new', [], 'Custom site') },
    { id: 'snippet', ...TYPES.snippet, admin: true, desc: 'A reusable piece of configuration that sites can pull in with “import”.',
      make: () => ({ kind: 'snippet', comments: ['# Reusable settings'], header: ['(common)'], headerComment: '', nodes: [dir('encode', 'zstd', 'gzip')] }) },
    { id: 'directive', ...TYPES.directive, admin: true, desc: 'Include another Caddyfile or folder of files (top-level import).',
      make: () => ({ kind: 'directive', comments: [], header: ['import', 'sites/*.caddy'], headerComment: '', nodes: [] }) },
  ];
}

// ---- header presets ----
const hv = (n) => args(n);
export const PROXY_HEADER_PRESETS = [
  { id: 'host_upstream', dir: 'header_up', label: 'Send the backend its own address as Host',
    desc: 'Some HTTPS backends and NAS/Apache panels reject requests for a host name they don’t know. This makes Caddy use the backend’s own address instead.',
    match: (n) => name(n) === 'header_up' && /^host$/i.test(hv(n)[0]) && hv(n)[1] === '{upstream_hostport}',
    make: () => dir('header_up', 'Host', '{upstream_hostport}') },
  { id: 'real_ip', dir: 'header_up', label: 'Pass the visitor’s real IP (X-Real-IP)',
    desc: 'Caddy already sends X-Forwarded-For. Some apps (Nginx-style) only read X-Real-IP.',
    match: (n) => name(n) === 'header_up' && /^x-real-ip$/i.test(hv(n)[0]),
    make: () => dir('header_up', 'X-Real-IP', '{remote_host}') },
  { id: 'fwd_host', dir: 'header_up', label: 'Tell the backend which domain was used (X-Forwarded-Host)',
    desc: 'Caddy sends this by default. Turn on only if your app insists on seeing it explicitly.',
    match: (n) => name(n) === 'header_up' && /^x-forwarded-host$/i.test(hv(n)[0]),
    make: () => dir('header_up', 'X-Forwarded-Host', '{host}') },
  { id: 'location', dir: 'header_down', label: 'Fix redirects that point to the backend’s internal address',
    desc: 'If logging in sends you to https://192.168.x.x:port, this rewrites those redirects back to your domain.',
    match: (n) => name(n) === 'header_down' && /^location$/i.test(hv(n)[0]),
    make: (ctx) => dir('header_down', 'Location', ctx.firstUpstream || 'http://backend', 'https://' + (ctx.firstAddress || 'example.com')) },
  { id: 'strip_server', dir: 'header_down', label: 'Hide the backend’s Server header',
    desc: 'Stops the site from advertising which software and version it runs.',
    match: (n) => name(n) === 'header_down' && /^-server$/i.test(hv(n)[0]),
    make: () => dir('header_down', '-Server') },
  { id: 'strip_powered', dir: 'header_down', label: 'Hide X-Powered-By',
    desc: 'Removes headers like “X-Powered-By: PHP/8.2” or “Express”.',
    match: (n) => name(n) === 'header_down' && /^-x-powered-by$/i.test(hv(n)[0]),
    make: () => dir('header_down', '-X-Powered-By') },
];

export const RESPONSE_HEADER_PRESETS = [
  { id: 'hsts', field: 'Strict-Transport-Security', value: 'max-age=31536000; includeSubDomains', label: 'Always use HTTPS (HSTS)',
    desc: 'Browsers will refuse plain HTTP for this site for a year. Only enable once HTTPS works.' },
  { id: 'nosniff', field: 'X-Content-Type-Options', value: 'nosniff', label: 'Block content-type guessing',
    desc: 'Stops browsers from treating files as a different type than declared. Safe for almost every site.' },
  { id: 'frame', field: 'X-Frame-Options', value: 'SAMEORIGIN', label: 'Block embedding by other sites',
    desc: 'Protects against clickjacking. Turn off if another site of yours needs to show this one in an iframe.' },
  { id: 'referrer', field: 'Referrer-Policy', value: 'strict-origin-when-cross-origin', label: 'Limit referrer information',
    desc: 'Other sites only see your domain, not the full address, when someone clicks a link.' },
  { id: 'permissions', field: 'Permissions-Policy', value: 'camera=(), microphone=(), geolocation=()', label: 'Disable camera, mic and location',
    desc: 'The page can never ask for these. Don’t use for video-call or map apps.' },
  { id: 'noindex', field: 'X-Robots-Tag', value: 'noindex, nofollow', label: 'Hide from search engines',
    desc: 'Asks Google & co. not to list this site. Great for private dashboards.' },
  { id: 'hide_server', field: '-Server', value: '', label: 'Remove the Server header',
    desc: 'Don’t reveal that the site is served by Caddy.' },
  { id: 'cors', field: 'Access-Control-Allow-Origin', value: '*', label: 'Allow any website to call this (CORS)',
    desc: 'Needed for public APIs used from other domains. Leave off unless you know you need it.' },
  { id: 'cache', field: 'Cache-Control', value: 'public, max-age=3600', label: 'Let browsers cache for an hour',
    desc: 'Faster repeat visits for static content.' },
  { id: 'nocache', field: 'Cache-Control', value: 'no-store', label: 'Never cache',
    desc: 'Always fetch fresh content. Useful for dashboards showing live data.' },
];

// ---- DNS providers (for certificates via the DNS challenge) ----
// single: provider takes one argument (acme_dns name TOKEN); otherwise a block.
export const DNS_PROVIDERS = [
  { id: 'porkbun', name: 'Porkbun', fields: [['api_key', 'API key', true], ['api_secret_key', 'Secret API key', true]] },
  { id: 'cloudflare', name: 'Cloudflare', single: true, fields: [['api_token', 'API token (Zone:Read + DNS:Edit)', true]] },
  { id: 'duckdns', name: 'Duck DNS', single: true, fields: [['api_token', 'Duck DNS token', true]] },
  { id: 'digitalocean', name: 'DigitalOcean', single: true, fields: [['auth_token', 'API token', true]] },
  { id: 'godaddy', name: 'GoDaddy', single: true, fields: [['api_token', 'API key:secret', true]] },
  { id: 'namecheap', name: 'Namecheap', fields: [['api_key', 'API key', true], ['user', 'API username'], ['api_endpoint', 'API endpoint (https://api.namecheap.com/xml.response)'], ['client_ip', 'Your public IP']] },
  { id: 'route53', name: 'AWS Route 53', fields: [['access_key_id', 'Access key ID'], ['secret_access_key', 'Secret access key', true], ['region', 'Region (e.g. us-east-1)'], ['hosted_zone_id', 'Hosted zone ID (optional)']] },
  { id: 'hetzner', name: 'Hetzner', single: true, fields: [['api_token', 'API token', true]] },
  { id: 'gandi', name: 'Gandi', single: true, fields: [['bearer_token', 'Personal access token', true]] },
  { id: 'ovh', name: 'OVH', fields: [['endpoint', 'Endpoint (e.g. ovh-eu)'], ['application_key', 'Application key'], ['application_secret', 'Application secret', true], ['consumer_key', 'Consumer key', true]] },
  { id: 'desec', name: 'deSEC', fields: [['token', 'Token', true]] },
  { id: 'linode', name: 'Linode (Akamai)', single: true, fields: [['api_token', 'Personal access token', true]] },
  { id: 'vultr', name: 'Vultr', single: true, fields: [['api_token', 'API token', true]] },
  { id: 'netcup', name: 'netcup', fields: [['customer_number', 'Customer number'], ['api_key', 'API key', true], ['api_password', 'API password', true]] },
  { id: 'ionos', name: 'IONOS', single: true, fields: [['auth_api_token', 'API token (prefix.secret)', true]] },
  { id: 'dnsimple', name: 'DNSimple', single: true, fields: [['api_access_token', 'API access token', true]] },
  { id: 'azure', name: 'Azure DNS', fields: [['subscription_id', 'Subscription ID'], ['resource_group_name', 'Resource group'], ['tenant_id', 'Tenant ID'], ['client_id', 'Client ID'], ['client_secret', 'Client secret', true]] },
  { id: 'googleclouddns', name: 'Google Cloud DNS', fields: [['gcp_project', 'GCP project ID']] },
  { id: 'rfc2136', name: 'RFC 2136 (BIND, Knot, …)', fields: [['key_name', 'Key name'], ['key_alg', 'Key algorithm (e.g. hmac-sha256)'], ['key', 'Key', true], ['server', 'Server (host:53)']] },
  { id: 'acmedns', name: 'ACME-DNS', fields: [['username', 'Username'], ['password', 'Password', true], ['subdomain', 'Subdomain'], ['server_url', 'Server URL']] },
  { id: 'powerdns', name: 'PowerDNS', positional: true, fields: [['server_url', 'Server URL'], ['api_token', 'API token', true]] },
];

export function providerById(id) { return DNS_PROVIDERS.find((p) => p.id === id); }

/** Read `acme_dns NAME ...` or `dns NAME ...` into {id, values:{}} */
export function readProvider(node) {
  if (!node) return null;
  const a = args(node);
  const id = a[0] || '';
  const p = providerById(id);
  const known = new Set(p ? p.fields.map(([k]) => k) : []);
  const values = {};
  const extra = []; // sub-options the form doesn't show, kept verbatim
  if (p && (p.single || p.positional)) {
    p.fields.forEach(([k], i) => { if (a[i + 1] !== undefined) values[k] = a[i + 1]; });
  }
  for (const c of node.block || []) {
    if (c.type === 'directive' && c.__raw === undefined && known.has(c.tokens[0]) && c.tokens.length === 2 && !c.block) values[c.tokens[0]] = args(c)[0];
    else extra.push(c);
  }
  const extraArgs = p ? (p.single && a.length > 2 ? a.slice(2) : []) : a.slice(1);
  return { id, values, extra, extraArgs, comment: node.comment, blank: node.blank };
}

/** Build the provider directive (keyword is 'acme_dns' globally or 'dns' inside tls). */
export function buildProvider(keyword, id, values, extraArgs = [], extra = [], meta = {}) {
  const p = providerById(id);
  let n;
  if (!p) {
    n = { type: 'directive', tokens: [keyword, quote(id), ...extraArgs.map(quote)], block: null };
  } else if (p.positional) {
    n = dir(keyword, id, ...p.fields.map(([k]) => values[k] || ''));
  } else if (p.single && !extra.length) {
    n = dir(keyword, id, values[p.fields[0][0]] || '', ...extraArgs);
  } else {
    n = dir(keyword, id);
    n.block = p.fields.filter(([k]) => (values[k] || '') !== '').map(([k]) => dir(k, values[k]));
  }
  if (extra.length) n.block = [...(n.block || []), ...extra];
  if (meta.comment) n.comment = meta.comment;
  if (meta.blank) n.blank = meta.blank;
  return n;
}

export const ACME_CAS = [
  ['', 'Let’s Encrypt (default)'],
  ['https://acme-staging-v02.api.letsencrypt.org/directory', 'Let’s Encrypt — staging (for testing, untrusted certificates)'],
  ['https://acme.zerossl.com/v2/DV90', 'ZeroSSL'],
];

export const LB_POLICIES = [
  ['random', 'Random (default)'], ['round_robin', 'Round robin — take turns'], ['least_conn', 'Least busy'],
  ['first', 'First available (fail-over)'], ['ip_hash', 'Same visitor → same server (by IP)'],
  ['client_ip_hash', 'Same client → same server (client IP)'], ['uri_hash', 'Same path → same server'], ['cookie', 'Sticky via cookie'],
];
