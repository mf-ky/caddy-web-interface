// CaddyWeb entry point: session handling, the page shell, theme and routing.

import { h, icon, api, clear, setUnauthorizedHandler, toast, field, fill } from './lib.js';
import { renderOverview } from './views/overview.js';
import { renderDashboard } from './views/dashboard.js';
import { renderCaddyfile, renderBackups, renderHistory } from './views/server-pages.js';
import { renderUsers, renderSettings, renderAccount } from './views/admin.js';
import { renderHelp } from './views/help.js';

export const app = {
  session: null, // {user:{username, role, roleLabel}, needsSetup, version}
  servers: [], // summaries from /api/servers
  get user() { return this.session && this.session.user; },
  get isAdmin() { return this.user && this.user.role === 'admin'; },
  get canAdd() { return this.user && (this.user.role === 'admin' || this.user.role === 'power'); },
};

// ---- theme ----
const THEME_KEY = 'caddyweb-theme';
function getTheme() { try { return localStorage.getItem(THEME_KEY) || 'system'; } catch { return 'system'; } }
export function setTheme(t) {
  try { localStorage.setItem(THEME_KEY, t); } catch { /* private mode */ }
  applyTheme();
}
function applyTheme() {
  const t = getTheme();
  if (t === 'system') document.documentElement.removeAttribute('data-theme');
  else document.documentElement.setAttribute('data-theme', t);
  const btn = document.getElementById('theme-btn');
  if (btn) fill(btn, icon(t === 'dark' ? 'moon' : t === 'light' ? 'sun' : 'monitor'));
}
applyTheme();
function cycleTheme() {
  const order = ['system', 'light', 'dark'];
  const next = order[(order.indexOf(getTheme()) + 1) % order.length];
  setTheme(next);
  toast({ system: 'Theme follows your device', light: 'Light theme', dark: 'Dark theme' }[next], 'info', 1800);
}

// ---- brand ----
export function brand(large = false) {
  return h('div', { class: 'brand' + (large ? ' brand-lg' : '') },
    h('span', { class: 'brand-mark', 'aria-hidden': 'true' }, logoSVG()),
    h('span', { class: 'brand-text' }, h('span', { class: 'brand-name' }, 'CaddyWeb'), h('span', { class: 'brand-sub' }, 'Front end manager for Caddy Server')));
}

let logoCount = 0;
function logoSVG() {
  const id = 'cw-logo-' + (++logoCount);
  const s = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  s.setAttribute('viewBox', '0 0 32 32');
  s.innerHTML = `<defs><linearGradient id="${id}" x1="0" y1="0" x2="1" y2="1"><stop offset="0" style="stop-color:var(--brand-a)"/><stop offset="1" style="stop-color:var(--brand-b)"/></linearGradient></defs>` +
    `<rect x="1" y="1" width="30" height="30" rx="9" fill="url(#${id})"/>` +
    '<path d="M10 13.5v-2a6 6 0 0 1 12 0v2" fill="none" stroke="#fff" stroke-width="2.4" stroke-linecap="round"/>' +
    '<rect x="7.5" y="13.5" width="17" height="11" rx="3" fill="#fff"/>' +
    '<path d="M12.5 19h7m-2.5-2.5L19.5 19 17 21.5" fill="none" style="stroke:var(--brand-a)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>';
  return s;
}

// ---- login / first-run setup ----
function renderLogin(root) {
  const setup = app.session && app.session.needsSetup;
  const user = h('input', { type: 'text', autocomplete: 'username', autocapitalize: 'off', spellcheck: 'false', required: true });
  const pass = h('input', { type: 'password', autocomplete: setup ? 'new-password' : 'current-password', required: true });
  const pass2 = setup ? h('input', { type: 'password', autocomplete: 'new-password', required: true }) : null;
  const err = h('div', { class: 'form-error', role: 'alert' });
  const btn = h('button', { class: 'btn btn-primary btn-block', type: 'submit' }, setup ? 'Create admin account' : 'Sign in');
  const form = h('form', { class: 'login-form', onsubmit: async (e) => {
    e.preventDefault();
    err.textContent = '';
    if (setup && pass.value !== pass2.value) { err.textContent = 'The passwords don’t match.'; return; }
    btn.disabled = true;
    try {
      app.session = await api('POST', setup ? '/api/setup' : '/api/login', { username: user.value, password: pass.value });
      start();
    } catch (x) { err.textContent = x.message; pass.select(); } finally { btn.disabled = false; }
  } },
  setup ? h('div', { class: 'callout callout-info' }, icon('sparkles'), h('span', null, 'Welcome! Create the first admin account. You can add more people later under Users.')) : null,
  field('Username', user),
  field('Password', pass, setup ? 'At least 8 characters.' : null),
  setup ? field('Repeat password', pass2) : null,
  err, btn,
  setup ? null : h('p', { class: 'login-hint' }, 'Forgot your password? On the CaddyWeb machine run ', h('code', null, 'caddyweb user passwd <name>'), '.'));
  clear(root).append(h('div', { class: 'login-page' },
    h('div', { class: 'login-glow', 'aria-hidden': 'true' }),
    h('main', { class: 'login-card' }, brand(true), h('h1', { class: 'sr-only' }, setup ? 'Set up CaddyWeb' : 'Sign in to CaddyWeb'), form),
    h('button', { class: 'icon-btn login-theme', id: 'theme-btn', type: 'button', title: 'Theme', 'aria-label': 'Change theme', onclick: cycleTheme })));
  applyTheme();
  setTimeout(() => user.focus(), 50);
}

// ---- shell ----
let main;
let openMenu = null;
document.addEventListener('click', (e) => { if (openMenu && !openMenu.contains(e.target)) openMenu.open = false; });
function closeMenusOnOutsideClick(menu) { openMenu = menu; }
function renderShell(root) {
  const u = app.user;
  const nav = [
    ['#/', 'Servers', 'server'],
    app.isAdmin ? ['#/users', 'Users', 'users'] : null,
    app.isAdmin ? ['#/settings', 'Settings', 'settings'] : null,
    ['#/help', 'Help', 'help'],
  ].filter(Boolean);
  const userMenu = h('details', { class: 'menu' },
    h('summary', { class: 'user-chip', 'aria-label': 'Account menu' },
      h('span', { class: 'avatar' }, u.username.slice(0, 1).toUpperCase()),
      h('span', { class: 'user-meta' }, h('strong', null, u.username), h('small', null, u.roleLabel))),
    h('div', { class: 'menu-pop' },
      h('a', { href: '#/account', class: 'menu-item' }, icon('key'), 'My account'),
      h('button', { class: 'menu-item', type: 'button', onclick: async () => { await api('POST', '/api/logout').catch(() => {}); app.session = await api('GET', '/api/session'); renderLogin(document.getElementById('app')); } }, icon('logout'), 'Sign out')));
  closeMenusOnOutsideClick(userMenu);
  main = h('main', { id: 'main', class: 'main', tabindex: '-1' });
  clear(root).append(
    h('a', { class: 'skip', href: '#main', onclick: (e) => { e.preventDefault(); main.focus(); } }, 'Skip to content'),
    h('header', { class: 'topbar' },
      h('div', { class: 'topbar-inner' },
        h('a', { href: '#/', class: 'brand-link', 'aria-label': 'CaddyWeb home' }, brand()),
        h('nav', { class: 'nav', 'aria-label': 'Main' }, nav.map(([href, label, ic]) => h('a', { href, class: 'nav-link', 'data-nav': href, title: label, 'aria-label': label }, icon(ic), h('span', null, label)))),
        h('div', { class: 'topbar-actions' },
          h('button', { class: 'icon-btn', id: 'theme-btn', type: 'button', title: 'Theme: system / light / dark', 'aria-label': 'Change theme', onclick: cycleTheme }),
          userMenu))),
    main,
    h('footer', { class: 'footer' }, 'CaddyWeb ', app.session.version, ' · Your Caddyfile stays yours: every change is plain text you can read and edit by hand.'));
  applyTheme();
}

function setActiveNav(path) {
  document.querySelectorAll('[data-nav]').forEach((a) => {
    const href = a.getAttribute('data-nav');
    const active = href === '#/' ? (path === '/' || path.startsWith('/s/')) : ('#' + path).startsWith(href);
    a.classList.toggle('active', active);
    if (active) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
  });
}

// ---- router ----
let routeToken = 0;
export function navigate(hash) { if (location.hash === hash) route(); else location.hash = hash; }

async function route() {
  if (!app.user) return;
  const token = ++routeToken;
  const path = (location.hash || '#/').slice(1) || '/';
  setActiveNav(path);
  const parts = path.split('/').filter(Boolean);
  clear(main);
  main.classList.remove('wide');
  const view = h('div', { class: 'view' });
  main.append(view);
  const stillCurrent = () => token === routeToken;
  try {
    if (parts[0] === 's' && parts[1]) {
      const id = decodeURIComponent(parts[1]);
      const page = parts[2] || 'sites';
      if (page === 'sites') await renderDashboard(view, id, stillCurrent);
      else if (page === 'caddyfile') await renderCaddyfile(view, id, stillCurrent);
      else if (page === 'backups') await renderBackups(view, id, stillCurrent);
      else if (page === 'history') await renderHistory(view, id, stillCurrent);
      else navigate('#/s/' + id);
    } else if (parts[0] === 'users' && app.isAdmin) await renderUsers(view);
    else if (parts[0] === 'settings' && app.isAdmin) await renderSettings(view);
    else if (parts[0] === 'account') await renderAccount(view);
    else if (parts[0] === 'help') await renderHelp(view, parts[1]);
    else await renderOverview(view, stillCurrent);
  } catch (e) {
    if (!stillCurrent()) return;
    fill(view, h('div', { class: 'empty-state' }, icon('alert', 'xl'), h('h2', null, 'Something went wrong'), h('p', null, e.message),
      h('div', { class: 'row-wrap' },
        h('button', { class: 'btn', type: 'button', onclick: route }, icon('refresh'), 'Try again'),
        h('a', { class: 'btn btn-ghost', href: '#/' }, icon('server'), 'Back to servers'))));
  }
  if (stillCurrent()) window.scrollTo({ top: 0 });
}

window.addEventListener('hashchange', route);

async function start() {
  const root = document.getElementById('app');
  if (!app.user) { renderLogin(root); return; }
  renderShell(root);
  route();
}

setUnauthorizedHandler(async () => {
  app.session = await api('GET', '/api/session').catch(() => ({}));
  if (!app.user) {
    toast('Your session ended. Please sign in again.', 'info');
    renderLogin(document.getElementById('app'));
  }
});

(async () => {
  try {
    app.session = await api('GET', '/api/session');
  } catch (e) {
    document.getElementById('app').replaceChildren(h('div', { class: 'empty-state' }, h('h2', null, 'Cannot reach CaddyWeb'), h('p', null, e.message)));
    return;
  }
  start();
})();
