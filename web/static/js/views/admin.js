// Экраны администратора: сводная панель (композиция статистики нескольких
// сервисов в API Gateway) и список пользователей.

import { api } from '../api.js';
import { html, mount } from '../ui.js';
import { money, dateTime, orderNo, ROLE, ORDER_STATUS, plural } from '../format.js';
import { pill, on, cap } from './common.js';

const STATUS_COLORS = {
  created: '#C5CEC7', paid: '#F0B323', accepted: '#1B6B45', ready: '#3D7EA6',
  in_delivery: '#7B5EA7', delivered: '#5F8F6E', cancelled: '#D8432E',
};

export function adminView() {
  let root;
  let snapshot = '';

  function kpi(value, label, extra = '') {
    return html`<div class="kpi"><div class="kpi__value">${value}</div><div class="kpi__label">${label}</div>${extra ? html`<div class="kpi__extra">${extra}</div>` : ''}</div>`;
  }

  function draw(stats, orders) {
    const o = stats.orders || {};
    const d = stats.delivery || {};
    const by = o.by_status || {};
    const total = Object.values(by).reduce((s, n) => s + n, 0) || 1;
    mount(root, html`
      <div class="container">
        <div class="page-head"><div>
          <h1 class="page-title">Обзор платформы</h1>
          <p class="page-sub">Данные собираются шлюзом из Order, Delivery, Restaurant и Auth сервисов</p>
        </div></div>
        <div class="kpis">
          ${kpi(o.total ?? 0, 'заказов всего', `${o.today ?? 0} сегодня`)}
          ${kpi(money(o.revenue), 'выручка (доставленные)')}
          ${kpi(money(o.average_check), 'средний чек')}
          ${kpi(by.delivered ?? 0, 'доставлено', `${by.cancelled ?? 0} отменено`)}
          ${kpi(d.couriers_online ?? 0, 'курьеров на смене', `${d.couriers_busy ?? 0} на доставке`)}
          ${kpi(d.deliveries_pending ?? 0, 'ждут курьера', `${d.deliveries_active ?? 0} в работе`)}
          ${kpi(stats.restaurants ?? 0, 'ресторанов')}
          ${kpi(stats.users ?? 0, 'пользователей')}
        </div>
        <div class="admin-grid">
          <section class="panel">
            <div class="panel__head"><h2 class="panel__title">Заказы по статусам</h2></div>
            <div class="panel__body">
              <div class="statusbar" role="img" aria-label="Распределение заказов по статусам">
                ${Object.keys(STATUS_COLORS).map((s) => (by[s] ? html`<span style="width:${(by[s] / total) * 100}%;background:${STATUS_COLORS[s]}" title="${ORDER_STATUS[s]}: ${by[s]}"></span>` : ''))}
              </div>
              <ul class="legend">
                ${Object.keys(STATUS_COLORS).map((s) => html`<li><i style="background:${STATUS_COLORS[s]}"></i>${cap(ORDER_STATUS[s])}<b>${by[s] ?? 0}</b></li>`)}
              </ul>
            </div>
          </section>
          <section class="panel">
            <div class="panel__head"><h2 class="panel__title">Последние заказы</h2></div>
            <div class="table-wrap">
              <table class="table">
                <thead><tr><th>Номер</th><th>Создан</th><th>Ресторан</th><th>Адрес</th><th>Статус</th><th class="right">Сумма</th></tr></thead>
                <tbody>
                  ${orders.map((x) => html`
                    <tr class="is-link" data-order="${x.id}">
                      <td class="num"><b>№${orderNo(x)}</b></td>
                      <td>${dateTime(x.created_at)}</td>
                      <td>${x.restaurant_name}</td>
                      <td class="muted">${x.delivery_address}</td>
                      <td>${pill(x.status)}</td>
                      <td class="right num">${money(x.total)}</td>
                    </tr>`)}
                </tbody>
              </table>
            </div>
          </section>
        </div>
      </div>`);
  }

  async function load(force) {
    const [stats, orders] = await Promise.all([api.stats(), api.orders({ limit: 15 })]);
    const list = orders?.items || [];
    const snap = JSON.stringify([stats, list.map((x) => [x.id, x.status])]);
    if (force || snap !== snapshot) { snapshot = snap; draw(stats, list); }
  }

  return {
    title: 'Обзор',
    async render(app) {
      root = document.createElement('div');
      app.replaceChildren(root);
      on(root, 'click', 'tr[data-order]', (e, el) => (location.hash = `#/orders/${el.dataset.order}`));
      await load(true);
    },
    async refresh() { await load(false); },
  };
}

export function adminUsersView() {
  let root;
  let role = '';

  async function load() {
    const data = await api.users({ role, limit: 100 });
    const users = data?.items || [];
    mount(root, html`
      <div class="container">
        <div class="page-head">
          <div>
            <h1 class="page-title">Пользователи</h1>
            <p class="page-sub">${data?.total ?? users.length} ${plural(data?.total ?? users.length, 'учётная запись', 'учётные записи', 'учётных записей')}</p>
          </div>
          <div class="chips" role="group" aria-label="Фильтр по роли">
            ${[['', 'Все'], ...Object.entries(ROLE)].map(([k, v]) => html`<button type="button" class="chip" data-role="${k}" aria-pressed="${role === k}">${cap(v)}</button>`)}
          </div>
        </div>
        <section class="panel table-wrap">
          <table class="table">
            <thead><tr><th>Имя</th><th>Email</th><th>Телефон</th><th>Роль</th><th>Зарегистрирован</th></tr></thead>
            <tbody>
              ${users.map((u) => html`<tr>
                <td><b>${u.name}</b></td><td>${u.email}</td><td class="num">${u.phone || '—'}</td>
                <td>${cap(ROLE[u.role] || u.role)}</td><td>${dateTime(u.created_at)}</td></tr>`)}
            </tbody>
          </table>
        </section>
      </div>`);
  }

  return {
    title: 'Пользователи',
    async render(app) {
      root = document.createElement('div');
      app.replaceChildren(root);
      on(root, 'click', '[data-role]', (e, el) => { role = el.dataset.role; load(); });
      await load();
    },
    onNotification: () => false,
  };
}
