// Экран курьера: выход на смену, текущая доставка (маршрут «ресторан → клиент»,
// готовность заказа) и история выполненных доставок.

import { api } from '../api.js';
import { html, mount, icon, toast, toastError, busy } from '../ui.js';
import { money, dateTime, time, ago } from '../format.js';
import { on, pill, emptyState, cap } from './common.js';
import { COURIER_STATUS } from '../format.js';

export default function courierView() {
  let root;
  let courier = null;
  let deliveries = [];
  let snapshot = '';

  const active = () => deliveries.find((d) => d.status === 'assigned' || d.status === 'picked_up');

  function shiftCard() {
    const online = courier.status !== 'offline';
    const today = deliveries.filter((d) => d.status === 'delivered' && new Date(d.delivered_at).toDateString() === new Date().toDateString());
    return html`
      <section class="panel shift">
        <div class="shift__state">
          <div>
            <div class="shift__label">${online ? 'Вы на смене' : 'Смена закрыта'}</div>
            <div class="small muted">${cap(COURIER_STATUS[courier.status] || courier.status)}${courier.status_changed_at ? ` · ${ago(courier.status_changed_at)}` : ''}</div>
          </div>
          <label class="switch switch--lg" title="${online ? 'Уйти со смены' : 'Выйти на смену'}">
            <input type="checkbox" id="shift" ${online ? html`checked` : ''} ${courier.status === 'busy' ? html`disabled` : ''}>
            <span class="switch__track"></span>
          </label>
        </div>
        <div class="shift__stats">
          <div class="mini"><b>${today.length}</b><span>доставок сегодня</span></div>
          <div class="mini"><b>${courier.completed_deliveries}</b><span>всего доставок</span></div>
        </div>
        ${courier.status === 'busy' ? html`<p class="note">Уйти со смены можно после завершения текущей доставки.</p>` : ''}
        ${courier.status === 'available' ? html`<p class="note">Ждём заказ: система назначит доставку автоматически, как только ресторан примет заказ.</p>` : ''}
      </section>`;
  }

  function jobCard(d) {
    if (!d) {
      const online = courier.status !== 'offline';
      return html`<section class="panel">${emptyState({
        art: online ? '🛵' : '😴',
        title: online ? 'Активных доставок нет' : 'Вы не на смене',
        text: online ? 'Новая доставка появится здесь — мы пришлём уведомление.' : 'Включите смену, чтобы получать заказы.',
      })}</section>`;
    }
    const picked = d.status === 'picked_up';
    return html`
      <section class="panel job" data-id="${d.id}">
        <div class="job__head">
          <div>
            <div class="small muted">Заказ №${d.order_number} · ${money(d.order_total)}</div>
            <h2 class="job__title">${picked ? 'Везёте заказ клиенту' : 'Заберите заказ в ресторане'}</h2>
          </div>
          ${picked ? pill('picked_up', 'delivery')
            : html`<span class="readiness ${d.order_ready ? 'readiness--ok' : 'readiness--wait'}">${d.order_ready ? html`${icon('check')} Заказ готов` : html`${icon('clock')} Готовится`}</span>`}
        </div>
        <div class="legs">
          <div class="leg">
            <div class="leg__kind">Откуда${d.picked_up_at ? ` · забрали в ${time(d.picked_up_at)}` : ''}</div>
            <div class="leg__name">${d.restaurant_name}</div>
            <div class="leg__addr">${d.restaurant_address}</div>
          </div>
          <div class="leg leg--to">
            <div class="leg__kind">Куда</div>
            <div class="leg__name">Клиент</div>
            <div class="leg__addr">${d.delivery_address}</div>
          </div>
        </div>
        <div class="job__actions">
          <button class="btn" type="button" data-act="pickup" ${picked || !d.order_ready ? html`disabled` : ''}>${icon('bag')} Забрал заказ</button>
          <button class="btn btn--saffron" type="button" data-act="deliver" ${picked ? '' : html`disabled`}>${icon('check')} Доставил</button>
        </div>
        ${!picked && !d.order_ready ? html`<p class="small muted">Кнопка «Забрал заказ» станет активной, когда ресторан отметит заказ готовым.</p>` : ''}
      </section>`;
  }

  function historyCard() {
    const done = deliveries.filter((d) => d.status === 'delivered' || d.status === 'cancelled');
    if (!done.length) return '';
    return html`
      <section class="panel" style="margin-top:24px">
        <div class="panel__head"><h2 class="panel__title">История</h2></div>
        <div class="history">
          ${done.map((d) => html`
            <div class="history__row">
              <div>
                <div class="history__title">${d.restaurant_name} → ${d.delivery_address}</div>
                <div class="history__sub">№${d.order_number} · ${dateTime(d.delivered_at || d.created_at)}</div>
              </div>
              <span class="num">${money(d.order_total)}</span>
              ${pill(d.status, 'delivery')}
            </div>`)}
        </div>
      </section>`;
  }

  function draw() {
    mount(root, html`
      <div class="container">
        <div class="page-head"><div>
          <h1 class="page-title">Доставки</h1>
          <p class="page-sub">${courier.name}</p>
        </div></div>
        <div class="courier-grid">
          ${shiftCard()}
          <div>${jobCard(active())}${historyCard()}</div>
        </div>
      </div>`);
  }

  async function load(force) {
    const [c, list] = await Promise.all([api.courier(), api.deliveries({})]);
    courier = c;
    deliveries = list?.items || [];
    const snap = JSON.stringify([courier.status, deliveries.map((d) => [d.id, d.status, d.order_ready])]);
    if (force || snap !== snapshot) { snapshot = snap; draw(); }
  }

  function bind() {
    on(root, 'change', '#shift', async (e, el) => {
      try {
        courier = await api.setShift(el.checked);
        toast(el.checked ? 'Вы на смене' : 'Смена завершена', { kind: 'success' });
        await load(true);
      } catch (err) { toastError(err); el.checked = !el.checked; }
    });
    on(root, 'click', '[data-act]', async (e, el) => {
      const id = el.closest('[data-id]').dataset.id;
      try {
        await busy(el, async () => {
          if (el.dataset.act === 'pickup') {
            await api.pickup(id);
            toast('Заказ у вас', { body: 'Клиент получил уведомление, что вы в пути', kind: 'success' });
          } else {
            await api.deliver(id);
            toast('Доставка завершена', { body: 'Спасибо! Ждите следующий заказ', kind: 'success' });
          }
        });
        await load(true);
      } catch (err) { toastError(err); }
    });
  }

  return {
    title: 'Доставки',
    async render(app) {
      root = document.createElement('div');
      app.replaceChildren(root);
      bind();
      await load(true);
    },
    async refresh() { await load(false); },
  };
}
