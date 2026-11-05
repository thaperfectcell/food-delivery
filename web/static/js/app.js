// Точка входа веб-клиента FoodFlow: маршрутизация по хешу, шапка с
// уведомлениями, сессия, обновление экранов в реальном времени.

import { api, setUnauthorizedHandler } from './api.js';
import { session, cart } from './state.js';
import * as realtime from './realtime.js';
import { html, mount, icon, brandMark, toast, toastError, $ } from './ui.js';
import { ROLE, ago } from './format.js';

import loginView from './views/login.js';
import catalogView from './views/catalog.js';
import restaurantView from './views/restaurant.js';
import ordersView from './views/orders.js';
import orderView from './views/order.js';
import kitchenView from './views/kitchen.js';
import menuEditorView from './views/menu-editor.js';
import courierView from './views/courier.js';
import { adminView, adminUsersView } from './views/admin.js';

const HOME = { customer: '#/restaurants', restaurant: '#/kitchen', courier: '#/courier', admin: '#/admin' };

const NAV = {
  customer: [['#/restaurants', 'Рестораны'], ['#/orders', 'Мои заказы']],
  restaurant: [['#/kitchen', 'Заказы'], ['#/menu', 'Меню и ресторан']],
  courier: [['#/courier', 'Доставки']],
  admin: [['#/admin', 'Обзор'], ['#/admin/users', 'Пользователи']],
};

const ROUTES = [
  { re: /^\/login$/, view: loginView, public: true },
  { re: /^\/restaurants$/, view: catalogView, roles: ['customer', 'admin'] },
  { re: /^\/restaurants\/([\w-]+)$/, view: restaurantView, roles: ['customer', 'admin'] },
  { re: /^\/orders$/, view: ordersView, roles: ['customer'] },
  { re: /^\/orders\/([\w-]+)$/, view: orderView, roles: ['customer', 'restaurant', 'courier', 'admin'] },
  { re: /^\/kitchen$/, view: kitchenView, roles: ['restaurant'] },
  { re: /^\/menu$/, view: menuEditorView, roles: ['restaurant'] },
  { re: /^\/courier$/, view: courierView, roles: ['courier'] },
  { re: /^\/admin$/, view: adminView, roles: ['admin'] },
  { re: /^\/admin\/users$/, view: adminUsersView, roles: ['admin'] },
];

const app = document.getElementById('app');
const topbar = document.getElementById('topbar');
let current = null; // активный экран
let unread = 0;
let renderSeq = 0;

export function navigate(hash) {
  if (location.hash === hash) route();
  else location.hash = hash;
}

export function home() {
  return HOME[session.role] || '#/login';
}

// ------------------------------------------------------------------ сессия
export async function signIn(resp) {
  session.set(resp.token, resp.user);
  realtime.connect(resp.token);
  await loadNotifications();
  renderTopbar();
  navigate(home());
}

export function signOut(message) {
  session.clear();
  realtime.disconnect();
  unread = 0;
  topbar.hidden = true;
  if (message) toast(message, { kind: 'error' });
  navigate('#/login');
}

setUnauthorizedHandler(() => signOut('Сессия истекла — войдите снова'));

// ------------------------------------------------------------------ шапка
function renderTopbar() {
  if (!session.user) { topbar.hidden = true; return; }
  const path = location.hash.replace(/^#/, '') || '/';
  const links = NAV[session.role] || [];
  topbar.hidden = false;
  mount(topbar, html`
    <div class="container topbar__inner">
      <a class="brand" href="${home()}" aria-label="FoodFlow — на главную">${brandMark()}<span class="brand__name">FoodFlow</span></a>
      <nav class="nav" aria-label="Разделы">
        ${links.map(([href, label]) => {
          const active = path === href.slice(1) || (href !== '#/admin' && path.startsWith(href.slice(1) + '/'));
          return html`<a href="${href}" ${active ? html`aria-current="page"` : ''}>${label}</a>`;
        })}
      </nav>
      <div class="topbar__spacer"></div>
      <div class="bell">
        <button class="bell__btn" type="button" id="bell-btn" aria-haspopup="true" aria-expanded="false" aria-label="Уведомления${unread ? `: ${unread} новых` : ''}">
          ${icon('bell')}
        </button>
        ${unread ? html`<span class="bell__count" aria-hidden="true">${unread > 99 ? '99+' : unread}</span>` : ''}
        <div class="notif-panel" id="notif-panel" hidden></div>
      </div>
      <div class="userchip">
        <span class="userchip__name">${session.user.name}</span>
        <span class="userchip__role">${capitalize(ROLE[session.role] || session.role)}</span>
      </div>
      <button class="icon-btn" type="button" id="logout-btn" title="Выйти" aria-label="Выйти">${icon('logout')}</button>
    </div>`);
  $('#logout-btn').addEventListener('click', () => signOut());
  $('#bell-btn').addEventListener('click', toggleNotifications);
}

const capitalize = (s) => (s ? s[0].toUpperCase() + s.slice(1) : s);

async function loadNotifications() {
  try {
    const data = await api.notifications({ limit: 20 });
    unread = data?.unread ?? (data?.items || []).filter((n) => !n.read).length;
    return data?.items || [];
  } catch {
    return [];
  }
}

async function toggleNotifications(e) {
  e.stopPropagation();
  const panel = $('#notif-panel');
  const btn = $('#bell-btn');
  if (!panel.hidden) { panel.hidden = true; btn.setAttribute('aria-expanded', 'false'); return; }
  panel.hidden = false;
  btn.setAttribute('aria-expanded', 'true');
  mount(panel, html`<div class="notif-empty">Загружаем…</div>`);
  const items = await loadNotifications();
  mount(panel, html`
    <div class="notif-panel__head">
      <h2>Уведомления</h2>
      ${items.some((n) => !n.read) ? html`<button class="btn btn--link small" type="button" id="read-all">Прочитать все</button>` : ''}
    </div>
    ${items.length ? items.map((n) => html`
      <button type="button" class="notif ${n.read ? '' : 'is-unread'}" data-id="${n.id}" data-order="${n.order_id || ''}">
        <span class="notif__dot"></span>
        <span>
          <span class="notif__title">${n.title}</span>
          <span class="notif__body" style="display:block">${n.body}</span>
          <span class="notif__time" style="display:block">${ago(n.created_at)}</span>
        </span>
      </button>`) : html`<div class="notif-empty">Пока тихо. Здесь появятся новости о заказах.</div>`}`);
  panel.querySelector('#read-all')?.addEventListener('click', async (ev) => {
    ev.stopPropagation();
    try { await api.markRead({ all: true }); unread = 0; renderTopbar(); } catch (err) { toastError(err); }
  });
  panel.querySelectorAll('.notif').forEach((el) => el.addEventListener('click', async () => {
    panel.hidden = true;
    if (el.classList.contains('is-unread')) {
      api.markRead({ ids: [el.dataset.id] }).then(() => { unread = Math.max(0, unread - 1); renderTopbar(); }).catch(() => {});
    }
    openOrder(el.dataset.order);
  }));
}

function openOrder(orderId) {
  if (!orderId) return;
  const role = session.role;
  if (role === 'restaurant') navigate('#/kitchen');
  else if (role === 'courier') navigate('#/courier');
  else navigate(`#/orders/${orderId}`);
}

document.addEventListener('click', (e) => {
  const panel = document.getElementById('notif-panel');
  if (panel && !panel.hidden && !panel.contains(e.target)) {
    panel.hidden = true;
    document.getElementById('bell-btn')?.setAttribute('aria-expanded', 'false');
  }
});
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') {
    const panel = document.getElementById('notif-panel');
    if (panel && !panel.hidden) { panel.hidden = true; $('#bell-btn')?.focus(); }
  }
});

// ------------------------------------------------------------------ реальное время
realtime.onNotification((n) => {
  unread += 1;
  renderTopbar();
  const btn = $('#bell-btn');
  if (btn) btn.classList.add('is-ringing');
  toast(n.title, { body: n.body, kind: 'live' });
  if (current?.onNotification?.(n) !== false) current?.refresh?.();
});

setInterval(() => {
  if (document.visibilityState === 'visible' && current?.refresh) current.refresh().catch?.(() => {});
}, 5000);

// ------------------------------------------------------------------ маршрутизация
async function route() {
  const path = location.hash.replace(/^#/, '') || '/';
  const seq = ++renderSeq;
  if (!session.token) {
    if (path !== '/login') { location.hash = '#/login'; return; }
  } else if (path === '/' || path === '/login') {
    location.hash = home();
    return;
  }

  let match = null;
  let params = [];
  for (const r of ROUTES) {
    const m = path.match(r.re);
    if (m) { match = r; params = m.slice(1); break; }
  }
  if (!match) { location.hash = session.token ? home() : '#/login'; return; }
  if (!match.public && match.roles && !match.roles.includes(session.role)) { location.hash = home(); return; }

  current?.destroy?.();
  current = match.view(...params);
  renderTopbar();
  document.title = current.title ? `${current.title} — FoodFlow` : 'FoodFlow';
  mount(app, html`<div class="loading">Загружаем…</div>`);
  try {
    await current.render(app);
  } catch (err) {
    if (seq !== renderSeq) return;
    mount(app, html`
      <div class="container"><div class="empty">
        <div class="empty__art">🥡</div>
        <h1 class="empty__title">Не удалось открыть страницу</h1>
        <p>${err.message || 'Неизвестная ошибка'}</p>
        <a class="btn" href="${home()}">На главную</a>
      </div></div>`);
  }
  if (seq === renderSeq) window.scrollTo({ top: 0 });
}

window.addEventListener('hashchange', route);

async function boot() {
  if (session.token) {
    try {
      const me = await api.me();
      session.setUser(me);
      realtime.connect(session.token);
      await loadNotifications();
    } catch (err) {
      if (err.status === 401) { session.clear(); }
    }
  }
  cart.onChange(() => {});
  route();
}

boot();
