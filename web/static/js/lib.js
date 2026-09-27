// Small DOM, API and UI helpers shared by every view. No framework: the UI is
// plain ES modules so it works offline on a LAN with nothing to build.

const SVG_NS = 'http://www.w3.org/2000/svg';

/** h('div', {class: 'x', onclick: fn}, child, 'text', [more]) */
export function h(tag, props, ...children) {
  const el = document.createElement(tag);
  if (props) {
    for (const [k, v] of Object.entries(props)) {
      if (v === undefined || v === null || v === false) continue;
      if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2).toLowerCase(), v);
      else if (k === 'class') el.className = v;
      else if (k === 'dataset') Object.assign(el.dataset, v);
      else if (k === 'value' || k === 'checked' || k === 'disabled' || k === 'selected' || k === 'indeterminate') el[k] = v;
      else if (k === 'html') el.innerHTML = v;
      else el.setAttribute(k, v === true ? '' : v);
    }
  }
  append(el, children);
  return el;
}

function append(el, children) {
  for (const c of children) {
    if (c === null || c === undefined || c === false) continue;
    if (Array.isArray(c)) append(el, c);
    else if (c instanceof Node) el.appendChild(c);
    else el.appendChild(document.createTextNode(String(c)));
  }
}

export function clear(el) { while (el.firstChild) el.removeChild(el.firstChild); return el; }

/** Replace an element's children, skipping null/false and flattening arrays. */
export function fill(el, ...children) { clear(el); append(el, children); return el; }

// ---- icons (paths adapted from the ISC-licensed Lucide set) ----
const ICONS = {
  proxy: '<path d="M8 3 4 7l4 4"/><path d="M4 7h16"/><path d="m16 21 4-4-4-4"/><path d="M20 17H4"/>',
  folder: '<path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>',
  redirect: '<polyline points="15 10 20 15 15 20"/><path d="M4 4v7a4 4 0 0 0 4 4h12"/>',
  message: '<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>',
  code: '<polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/>',
  branch: '<line x1="6" x2="6" y1="3" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>',
  file: '<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="m10 13-2 2 2 2"/><path d="m14 17 2-2-2-2"/>',
  globe: '<circle cx="12" cy="12" r="10"/><path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20"/><path d="M2 12h20"/>',
  server: '<rect width="20" height="8" x="2" y="2" rx="2" ry="2"/><rect width="20" height="8" x="2" y="14" rx="2" ry="2"/><line x1="6" x2="6.01" y1="6" y2="6"/><line x1="6" x2="6.01" y1="18" y2="18"/>',
  lock: '<rect width="18" height="11" x="3" y="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>',
  shield: '<path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/>',
  settings: '<path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/>',
  users: '<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/>',
  archive: '<rect width="20" height="5" x="2" y="3" rx="1"/><path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8"/><path d="M10 12h4"/>',
  history: '<path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/><path d="M12 7v5l4 2"/>',
  plus: '<path d="M5 12h14"/><path d="M12 5v14"/>',
  sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2"/><path d="M12 20v2"/><path d="m4.93 4.93 1.41 1.41"/><path d="m17.66 17.66 1.41 1.41"/><path d="M2 12h2"/><path d="M20 12h2"/><path d="m6.34 17.66-1.41 1.41"/><path d="m19.07 4.93-1.41 1.41"/>',
  moon: '<path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"/>',
  monitor: '<rect width="20" height="14" x="2" y="3" rx="2"/><line x1="8" x2="16" y1="21" y2="21"/><line x1="12" x2="12" y1="17" y2="21"/>',
  logout: '<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><polyline points="16 17 21 12 16 7"/><line x1="21" x2="9" y1="12" y2="12"/>',
  check: '<path d="M20 6 9 17l-5-5"/>',
  x: '<path d="M18 6 6 18"/><path d="m6 6 12 12"/>',
  alert: '<path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3"/><path d="M12 9v4"/><path d="M12 17h.01"/>',
  info: '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>',
  search: '<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>',
  eye: '<path d="M2.06 12.35a1 1 0 0 1 0-.7 10.75 10.75 0 0 1 19.88 0 1 1 0 0 1 0 .7 10.75 10.75 0 0 1-19.88 0"/><circle cx="12" cy="12" r="3"/>',
  download: '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" x2="12" y1="15" y2="3"/>',
  undo: '<path d="M3 7v6h6"/><path d="M21 17a9 9 0 0 0-9-9 9 9 0 0 0-6 2.3L3 13"/>',
  edit: '<path d="M12 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.375 2.625a1 1 0 0 1 3 3l-9.013 9.014a2 2 0 0 1-.853.505l-2.873.84a.5.5 0 0 1-.62-.62l.84-2.873a2 2 0 0 1 .506-.852z"/>',
  trash: '<path d="M3 6h18"/><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"/><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/>',
  copy: '<rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/>',
  key: '<path d="m15.5 7.5 2.3 2.3a1 1 0 0 0 1.4 0l2.1-2.1a1 1 0 0 0 0-1.4L19 4"/><path d="m21 2-9.6 9.6"/><circle cx="7.5" cy="15.5" r="5.5"/>',
  refresh: '<path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M8 16H3v5"/>',
  rocket: '<path d="M4.5 16.5c-1.5 1.26-2 5-2 5s3.74-.5 5-2c.71-.84.7-2.13-.09-2.91a2.18 2.18 0 0 0-2.91-.09z"/><path d="m12 15-3-3a22 22 0 0 1 2-3.95A12.88 12.88 0 0 1 22 2c0 2.72-.78 7.5-6 11a22.35 22.35 0 0 1-4 2z"/><path d="M9 12H4s.55-3.03 2-4c1.62-1.08 5 0 5 0"/><path d="M12 15v5s3.03-.55 4-2c1.08-1.62 0-5 0-5"/>',
  help: '<circle cx="12" cy="12" r="10"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/><path d="M12 17h.01"/>',
  arrowRight: '<path d="M5 12h14"/><path d="m12 5 7 7-7 7"/>',
  chevronDown: '<path d="m6 9 6 6 6-6"/>',
  chevronUp: '<path d="m18 15-6-6-6 6"/>',
  grip: '<circle cx="9" cy="12" r="1"/><circle cx="9" cy="5" r="1"/><circle cx="9" cy="19" r="1"/><circle cx="15" cy="12" r="1"/><circle cx="15" cy="5" r="1"/><circle cx="15" cy="19" r="1"/>',
  layers: '<path d="m12.83 2.18a2 2 0 0 0-1.66 0L2.6 6.08a1 1 0 0 0 0 1.83l8.58 3.91a2 2 0 0 0 1.66 0l8.58-3.9a1 1 0 0 0 0-1.83Z"/><path d="m22 17.65-9.17 4.16a2 2 0 0 1-1.66 0L2 17.65"/><path d="m22 12.65-9.17 4.16a2 2 0 0 1-1.66 0L2 12.65"/>',
  scale: '<path d="m16 16 3-8 3 8c-.87.65-1.92 1-3 1s-2.13-.35-3-1Z"/><path d="m2 16 3-8 3 8c-.87.65-1.92 1-3 1s-2.13-.35-3-1Z"/><path d="M7 21h10"/><path d="M12 3v18"/><path d="M3 7h2c2 0 5-1 7-2 2 1 5 2 7 2h2"/>',
  sparkles: '<path d="M9.94 14.06 4 20"/><path d="m12 2 1.5 4.5L18 8l-4.5 1.5L12 14l-1.5-4.5L6 8l4.5-1.5z"/><path d="M19 13v4"/><path d="M17 15h4"/>',
  link: '<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>',
  external: '<path d="M15 3h6v6"/><path d="M10 14 21 3"/><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>',
  terminal: '<polyline points="4 17 10 11 4 5"/><line x1="12" x2="20" y1="19" y2="19"/>',
  more: '<circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/><circle cx="5" cy="12" r="1"/>',
  php: '<ellipse cx="12" cy="12" rx="10" ry="6"/><path d="M8 14l1-4h2a1 1 0 0 1 0 2H9"/><path d="M15 14l1-4h2a1 1 0 0 1 0 2h-2"/><path d="M12 9v5"/>',
  zap: '<path d="M4 14a1 1 0 0 1-.78-1.63l9.9-10.2a.5.5 0 0 1 .86.46l-1.92 6.02A1 1 0 0 0 13 10h7a1 1 0 0 1 .78 1.63l-9.9 10.2a.5.5 0 0 1-.86-.46l1.92-6.02A1 1 0 0 0 11 14z"/>',
  filter: '<polygon points="22 3 2 3 10 12.46 10 19 14 21 14 12.46 22 3"/>',
  snippet: '<path d="M16 3h5v5"/><path d="M8 3H3v5"/><path d="M12 22v-8.3a4 4 0 0 0-1.172-2.872L3 3"/><path d="m15 9 6-6"/>',
};

export function icon(name, cls = '') {
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('fill', 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '2');
  svg.setAttribute('stroke-linecap', 'round');
  svg.setAttribute('stroke-linejoin', 'round');
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('class', 'icon ' + cls);
  svg.innerHTML = ICONS[name] || ICONS.file;
  return svg;
}

// ---- API ----
export class ApiError extends Error {
  constructor(message, status, data) { super(message); this.status = status; this.data = data || {}; }
}

let onUnauthorized = () => {};
export function setUnauthorizedHandler(fn) { onUnauthorized = fn; }

export async function api(method, path, body) {
  const opts = { method, headers: { 'X-CaddyWeb': '1' }, credentials: 'same-origin' };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  let res;
  try {
    res = await fetch(path, opts);
  } catch (e) {
    throw new ApiError('Cannot reach CaddyWeb. Is it still running?', 0);
  }
  let data = null;
  const ct = res.headers.get('Content-Type') || '';
  if (ct.includes('application/json')) data = await res.json().catch(() => null);
  if (!res.ok) {
    if (res.status === 401 && !path.startsWith('/api/login') && !path.startsWith('/api/me/')) onUnauthorized();
    throw new ApiError((data && data.error) || res.statusText || 'Request failed', res.status, data);
  }
  return data;
}

// ---- toasts ----
export function toast(message, kind = 'ok', timeout = 4500) {
  let host = document.getElementById('toasts');
  if (!host) { host = h('div', { id: 'toasts', 'aria-live': 'polite' }); document.body.appendChild(host); }
  const t = h('div', { class: 'toast toast-' + kind },
    icon(kind === 'error' ? 'alert' : kind === 'info' ? 'info' : 'check'),
    h('span', null, message));
  host.appendChild(t);
  requestAnimationFrame(() => t.classList.add('show'));
  setTimeout(() => { t.classList.remove('show'); setTimeout(() => t.remove(), 300); }, timeout);
}

// ---- modal & drawer ----
let openLayers = [];

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), summary, [tabindex]:not([tabindex="-1"])';

function trapKeys(e) {
  const top = openLayers[openLayers.length - 1];
  if (!top) return;
  if (e.key === 'Escape' && top.dismissible) { e.preventDefault(); top.close(); return; }
  if (e.key === 'Tab' && top.el) {
    // keep keyboard focus inside the open dialog
    const items = [...top.el.querySelectorAll(FOCUSABLE)].filter((x) => x.offsetParent !== null);
    if (!items.length) return;
    const first = items[0];
    const last = items[items.length - 1];
    if (!top.el.contains(document.activeElement)) { e.preventDefault(); first.focus(); }
    else if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
  }
}
document.addEventListener('keydown', trapKeys);

/**
 * modal({title, body, actions:[{label, kind, onClick, keepOpen}], wide, dismissible})
 * returns {close, el}
 */
export function modal({ title, body, actions = [], wide = false, dismissible = true, className = '', onClose }) {
  const backdrop = h('div', { class: 'backdrop' });
  const box = h('div', { class: 'modal ' + (wide ? 'modal-wide ' : '') + className, role: 'dialog', 'aria-modal': 'true', 'aria-label': title });
  const layer = { dismissible, close: () => {}, el: box };
  const opener = document.activeElement;
  let closed = false;
  const close = () => {
    if (closed) return;
    closed = true;
    backdrop.classList.remove('show');
    openLayers = openLayers.filter((l) => l !== layer);
    setTimeout(() => backdrop.remove(), 200);
    if (!openLayers.length) document.body.classList.remove('noscroll');
    if (opener && opener.focus && document.body.contains(opener)) opener.focus();
    if (onClose) onClose();
  };
  layer.close = close;
  const footer = h('div', { class: 'modal-actions' });
  for (const a of actions) {
    const btn = h('button', { class: 'btn ' + (a.kind ? 'btn-' + a.kind : ''), type: 'button' }, a.icon ? icon(a.icon) : null, a.label);
    btn.addEventListener('click', async () => {
      if (!a.onClick) return close();
      btn.disabled = true;
      try {
        const r = await a.onClick(btn);
        if (r !== false && !a.keepOpen) close();
      } finally { btn.disabled = false; }
    });
    footer.appendChild(btn);
  }
  append(box, [
    h('div', { class: 'modal-head' }, h('h2', null, title),
      dismissible ? h('button', { class: 'icon-btn', type: 'button', 'aria-label': 'Close', onclick: close }, icon('x')) : null),
    h('div', { class: 'modal-body' }, body),
    actions.length ? footer : null,
  ]);
  backdrop.appendChild(box);
  if (dismissible) backdrop.addEventListener('mousedown', (e) => { if (e.target === backdrop) close(); });
  document.body.appendChild(backdrop);
  document.body.classList.add('noscroll');
  openLayers.push(layer);
  requestAnimationFrame(() => backdrop.classList.add('show'));
  setTimeout(() => { const f = box.querySelector('input, textarea, select'); if (f) f.focus(); }, 60);
  return { close, el: box };
}

/** confirmDialog('Delete?', 'text', {confirm:'Delete', danger:true}) → Promise<boolean> */
export function confirmDialog(title, text, { confirm = 'Confirm', danger = false } = {}) {
  return new Promise((resolve) => {
    let done = false;
    const m = modal({
      title,
      body: typeof text === 'string' ? h('p', null, text) : text,
      actions: [
        { label: 'Cancel', onClick: () => { done = true; resolve(false); } },
        { label: confirm, kind: danger ? 'danger' : 'primary', onClick: () => { done = true; resolve(true); } },
      ],
    });
    const obs = new MutationObserver(() => { if (!document.body.contains(m.el)) { obs.disconnect(); if (!done) resolve(false); } });
    obs.observe(document.body, { childList: true });
  });
}

/**
 * drawer({title, header, body, footer, beforeClose}) — a side sheet used by the
 * card editor. beforeClose() may return false (or a Promise of false) to keep
 * it open, e.g. to ask about unsaved changes. Returns {close (forced),
 * requestClose (asks beforeClose first)}.
 */
export function drawer({ title, header, body, footer, onClose, beforeClose }) {
  const backdrop = h('div', { class: 'backdrop drawer-backdrop' });
  const panel = h('aside', { class: 'drawer', role: 'dialog', 'aria-modal': 'true', 'aria-label': title || 'Editor' });
  const layer = { dismissible: true, close: () => {}, el: panel };
  const opener = document.activeElement;
  let closed = false;
  const close = () => {
    if (closed) return;
    closed = true;
    backdrop.classList.remove('show');
    openLayers = openLayers.filter((l) => l !== layer);
    setTimeout(() => backdrop.remove(), 250);
    if (!openLayers.length) document.body.classList.remove('noscroll');
    if (opener && opener.focus && document.body.contains(opener)) opener.focus();
    if (onClose) onClose();
  };
  const requestClose = async () => {
    if (beforeClose && (await beforeClose()) === false) return;
    close();
  };
  layer.close = requestClose;
  append(panel, [header, h('div', { class: 'drawer-body' }, body), footer]);
  backdrop.appendChild(panel);
  backdrop.addEventListener('mousedown', (e) => { if (e.target === backdrop) layer.close(); });
  document.body.appendChild(backdrop);
  document.body.classList.add('noscroll');
  openLayers.push(layer);
  requestAnimationFrame(() => backdrop.classList.add('show'));
  setTimeout(() => { const f = panel.querySelector('.drawer-body ' + FOCUSABLE) || panel.querySelector(FOCUSABLE); if (f) f.focus({ preventScroll: true }); }, 80);
  return { close, requestClose, layer, el: panel };
}

// ---- formatting ----
export function timeAgo(date) {
  const d = typeof date === 'string' ? new Date(date) : date;
  if (!d || isNaN(d)) return '';
  const s = Math.round((Date.now() - d.getTime()) / 1000);
  if (s < 45) return 'just now';
  const units = [[60, 'minute'], [3600, 'hour'], [86400, 'day'], [604800, 'week'], [2629800, 'month'], [31557600, 'year']];
  let unit = ['second', 1];
  for (const [secs, name] of units) if (s >= secs) unit = [name, secs];
  const n = Math.round(s / unit[1]);
  return `${n} ${unit[0]}${n === 1 ? '' : 's'} ago`;
}

export function fmtDate(date) {
  const d = typeof date === 'string' ? new Date(date) : date;
  if (!d || isNaN(d) || d.getFullYear() < 2000) return '—';
  return d.toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

export function fmtBytes(n) {
  if (n < 1024) return n + ' B';
  return (n / 1024).toFixed(1) + ' KB';
}

export async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    toast('Copied to clipboard', 'ok', 2000);
  } catch {
    // clipboard API needs HTTPS; fall back to a selection
    const ta = h('textarea', { class: 'offscreen' });
    ta.value = text;
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand('copy'); toast('Copied to clipboard', 'ok', 2000); } catch { toast('Copy failed — select the text manually', 'error'); }
    ta.remove();
  }
}

/** A code block with a copy button. */
export function codeBlock(text, { copy = true, cls = '' } = {}) {
  return h('div', { class: 'codeblock ' + cls },
    h('pre', null, h('code', null, text)),
    copy ? h('button', { class: 'icon-btn copy-btn', type: 'button', title: 'Copy', 'aria-label': 'Copy', onclick: () => copyText(text) }, icon('copy')) : null);
}

/** Form field with label and optional hint. */
export function field(label, control, hint, opts = {}) {
  const id = control.id || ('f' + Math.random().toString(36).slice(2, 9));
  if (control.tagName && !control.id) control.id = id;
  return h('div', { class: 'field ' + (opts.class || '') },
    label ? h('label', { for: id }, label, opts.optional ? h('span', { class: 'optional' }, ' optional') : null) : null,
    control,
    hint ? h('div', { class: 'hint' }, hint) : null);
}

export function toggle(label, checked, onChange, hint) {
  const input = h('input', { type: 'checkbox', checked, onchange: (e) => onChange(e.target.checked) });
  return h('label', { class: 'toggle' }, input, h('span', { class: 'switch', 'aria-hidden': 'true' }),
    h('span', { class: 'toggle-text' }, h('span', { class: 'toggle-label' }, label), hint ? h('span', { class: 'hint' }, hint) : null));
}

export function select(options, value, onChange) {
  const el = h('select', { onchange: (e) => onChange(e.target.value) },
    options.map((o) => {
      const [v, l] = Array.isArray(o) ? o : [o.value, o.label];
      return h('option', { value: v, selected: v === value }, l);
    }));
  return el;
}

export function debounce(fn, ms) {
  let t;
  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}

export function el(id) { return document.getElementById(id); }
