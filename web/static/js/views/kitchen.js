// Экран ресторана «Заказы»: канбан-доска по статусам с действиями
// «Принять», «Отклонить», «Готов». Обновляется по push-уведомлениям и опросом.

import { api } from '../api.js';
import { html, mount, icon, toast, toastError, confirmDialog, busy } from '../ui.js';
import { money, minutesSince, orderNo, plural } from '../format.js';
import { on, emptyState, statusTime, countItems } from './common.js';

const LANES = [
  { status: 'paid', title: 'Новые', color: 'var(--saffron)', empty: 'Новых заказов нет' },
  { status: 'accepted', title: 'Готовятся', color: 'var(--basil)', empty: 'Ничего не готовится' },
  { status: 'ready', title: 'Готовы', color: '#3D7EA6', empty: 'Нет готовых заказов' },
  { status: 'in_delivery', title: 'В доставке', color: 'var(--mist)', empty: 'Курьеры свободны' },
];

const REJECT_REASONS = ['Закончились ингредиенты', 'Ресторан перегружен', 'Скоро закрываемся'];

export default function kitchenView() {
  let root;
  let restaurants = [];
  let selected = null;
  let snapshot = '';

  function ticket(o) {
    const since = statusTime(o, o.status) || o.updated_at;
    const age = minutesSince(since);
    const late = (o.status === 'paid' && age >= 5) || (o.status === 'accepted' && age >= 30);
    return html`
      <article class="ticket ${o.status === 'paid' ? 'ticket--new' : ''}" data-id="${o.id}">
        <div class="ticket__top">
          <span class="ticket__num">№${orderNo(o)}</span>
          <span class="ticket__age ${late ? 'is-late' : ''}">${age < 1 ? 'только что' : `${age} мин`}</span>
        </div>
        <ul class="ticket__items">
          ${o.items.map((i) => html`<li><b>${i.quantity}×</b><span>${i.name}</span></li>`)}
        </ul>
        ${o.comment ? html`<div class="ticket__note">💬 ${o.comment}</div>` : ''}
        <div class="ticket__meta">
          <span>${countItems(o.items)} ${plural(countItems(o.items), 'позиция', 'позиции', 'позиций')}</span>
          <b>${money(o.total)}</b>
        </div>
        ${o.courier_name ? html`<div class="ticket__courier">${icon('truck')} Курьер: ${o.courier_name}</div>` : ''}
        ${o.status === 'paid' ? html`
          <div class="ticket__actions">
            <button class="btn btn--sm" type="button" data-act="accept">Принять</button>
            <button class="btn btn--sm btn--danger" type="button" data-act="reject" title="Отклонить">${icon('x')}</button>
          </div>` : ''}
        ${o.status === 'accepted' ? html`
          <div class="ticket__actions">
            <button class="btn btn--sm btn--saffron" type="button" data-act="ready">Готов к выдаче</button>
            <button class="btn btn--sm btn--danger" type="button" data-act="reject" title="Отменить">${icon('x')}</button>
          </div>` : ''}
      </article>`;
  }

  function draw(orders) {
    const by = Object.fromEntries(LANES.map((l) => [l.status, []]));
    orders.forEach((o) => by[o.status]?.push(o));
    // Старые заказы — сверху: их нужно обработать первыми.
    Object.values(by).forEach((list) => list.sort((a, b) => new Date(a.created_at) - new Date(b.created_at)));
    const r = restaurants.find((x) => x.id === selected);
    mount(root, html`
      <div class="container">
        <div class="page-head">
          <div>
            <h1 class="page-title">Заказы</h1>
            <p class="page-sub">${r ? r.name : ''} · новые заказы появляются автоматически после оплаты</p>
          </div>
          <div class="board-tools">
            ${restaurants.length > 1 ? html`
              <select class="input" id="rest-select" aria-label="Ресторан">
                ${restaurants.map((x) => html`<option value="${x.id}" ${x.id === selected ? html`selected` : ''}>${x.name}</option>`)}
              </select>` : ''}
            ${r ? html`<span class="pill ${r.is_active ? 'pill--delivered' : 'pill--cancelled'}">${r.is_active ? 'Принимает заказы' : 'Приём закрыт'}</span>` : ''}
          </div>
        </div>
        <div class="board">
          ${LANES.map((l) => html`
            <section class="lane" style="--lane:${l.color}" aria-label="${l.title}">
              <div class="lane__head">
                <h2 class="lane__title">${l.title}</h2>
                <span class="lane__count">${by[l.status].length}</span>
              </div>
              <div class="lane__list">
                ${by[l.status].length ? by[l.status].map(ticket) : html`<div class="lane__empty">${l.empty}</div>`}
              </div>
            </section>`)}
        </div>
      </div>`);
    root.querySelector('#rest-select')?.addEventListener('change', (e) => {
      selected = e.target.value;
      snapshot = '';
      load(true).catch(toastError);
    });
  }

  async function load(force) {
    if (!selected) return;
    const data = await api.orders({ restaurant_id: selected, status: LANES.map((l) => l.status).join(','), limit: 100 });
    const list = data?.items || [];
    const snap = JSON.stringify(list.map((o) => [o.id, o.status, o.courier_name]));
    if (force || snap !== snapshot) {
      snapshot = snap;
      draw(list);
    }
  }

  async function act(id, action, btn) {
    try {
      await busy(btn, async () => {
        if (action === 'accept') {
          await api.acceptOrder(id);
          toast('Заказ принят', { body: 'Курьер будет назначен автоматически', kind: 'success' });
        } else if (action === 'ready') {
          await api.readyOrder(id);
          toast('Заказ готов', { body: 'Курьер получил уведомление', kind: 'success' });
        } else if (action === 'reject') {
          const reason = await confirmDialog({
            title: 'Отклонить заказ?',
            text: 'Клиент получит уведомление, а деньги вернутся на его карту автоматически.',
            confirm: 'Отклонить', danger: true,
            input: { label: 'Причина', placeholder: 'Например, закончились ингредиенты', suggestions: REJECT_REASONS },
          });
          if (reason === null) return;
          await api.rejectOrder(id, reason);
          toast('Заказ отклонён', { body: 'Инициирован возврат средств', kind: 'info' });
        }
      });
      await load(true);
    } catch (e) {
      toastError(e);
    }
  }

  return {
    title: 'Заказы ресторана',
    async render(app) {
      // Собственный контейнер: обработчики событий живут ровно столько, сколько экран.
      root = document.createElement('div');
      app.replaceChildren(root);
      const data = await api.myRestaurants();
      restaurants = data?.items || [];
      if (!restaurants.length) {
        mount(root, html`<div class="container"><div class="panel">${emptyState({
          art: '👩‍🍳', title: 'У вас пока нет ресторана',
          text: 'Создайте ресторан и добавьте блюда — после этого клиенты смогут делать заказы.',
          action: html`<a class="btn" href="#/menu">Создать ресторан</a>`,
        })}</div></div>`);
        return;
      }
      selected = restaurants[0].id;
      on(root, 'click', '[data-act]', (e, el) => act(el.closest('[data-id]').dataset.id, el.dataset.act, el));
      await load(true);
    },
    async refresh() { await load(false); },
  };
}
