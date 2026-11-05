// «Мои заказы»: сначала активные, затем история.

import { api } from '../api.js';
import { html, mount } from '../ui.js';
import { money, dateTime, orderNo } from '../format.js';
import { pill, itemsSummary, isActive, emptyState } from './common.js';

export default function ordersView() {
  let root;
  let snapshot = '';

  function row(o) {
    return html`
      <a class="orow" href="#/orders/${o.id}">
        <div><div class="orow__num">№${orderNo(o)}</div><div class="orow__date">${dateTime(o.created_at)}</div></div>
        <div style="min-width:0"><div class="orow__rest">${o.restaurant_name}</div><div class="orow__items">${itemsSummary(o.items)}</div></div>
        ${pill(o.status)}
        <div class="orow__sum">${money(o.total)}</div>
      </a>`;
  }

  function draw(list) {
    const active = list.filter(isActive);
    const done = list.filter((o) => !isActive(o));
    mount(root, html`
      <div class="container">
        <div class="page-head">
          <div>
            <h1 class="page-title">Мои заказы</h1>
            <p class="page-sub">Статусы обновляются сами, держать страницу открытой не обязательно.</p>
          </div>
          <a class="btn btn--ghost" href="#/restaurants">Выбрать ресторан</a>
        </div>
        ${!list.length ? html`<div class="panel">${emptyState({
          art: '🥡', title: 'Заказов пока нет',
          text: 'Выберите ресторан и соберите корзину: оформление займёт меньше минуты.',
          action: html`<a class="btn" href="#/restaurants">Перейти к ресторанам</a>`,
        })}</div>` : ''}
        ${active.length ? html`<h2 class="section-title" style="margin:8px 0 14px">Сейчас в работе</h2><div class="olist">${active.map(row)}</div>` : ''}
        ${done.length ? html`<h2 class="section-title" style="margin:${active.length ? '36px' : '8px'} 0 14px">История</h2><div class="olist">${done.map(row)}</div>` : ''}
      </div>`);
  }

  async function load(force) {
    const data = await api.orders({ limit: 50 });
    const list = data?.items || [];
    const snap = JSON.stringify(list.map((o) => [o.id, o.status, o.updated_at]));
    if (force || snap !== snapshot) {
      snapshot = snap;
      draw(list);
    }
  }

  return {
    title: 'Мои заказы',
    async render(app) { root = app; await load(true); },
    async refresh() { await load(false); },
  };
}
