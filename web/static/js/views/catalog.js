// Каталог ресторанов: поиск, фильтр по кухне и баннер текущего заказа.

import { api } from '../api.js';
import { session } from '../state.js';
import { html, mount, icon, $ } from '../ui.js';
import { money, statusLabel, orderNo } from '../format.js';
import { coverBox, on, emptyState, ACTIVE_STATUSES } from './common.js';

export default function catalogView() {
  let root;
  let all = [];
  let liveOrder = null;
  let q = '';
  let cuisine = '';

  const cuisines = () => [...new Set(all.map((r) => r.cuisine).filter(Boolean))].sort((a, b) => a.localeCompare(b, 'ru'));

  function filtered() {
    const needle = q.trim().toLowerCase();
    return all.filter((r) =>
      (!cuisine || r.cuisine === cuisine) &&
      (!needle || r.name.toLowerCase().includes(needle) || (r.description || '').toLowerCase().includes(needle)));
  }

  function draw() {
    mount(root, html`
      <div class="container" id="catalog">
        <div class="page-head">
          <div>
            <h1 class="page-title">Рестораны</h1>
            <p class="page-sub">Блюда готовят в ресторане, а путь заказа от кухни до двери виден на одной странице.</p>
          </div>
        </div>
        <div id="live-order">${liveBanner()}</div>
        <div class="catalog-tools">
          <label class="visually-hidden" for="search">Поиск ресторана</label>
          <input class="input input--search" id="search" type="search" placeholder="Ресторан или блюдо" value="${q}" autocomplete="off">
          <div class="chips" role="group" aria-label="Кухня">
            <button type="button" class="chip" data-cuisine="" aria-pressed="${String(!cuisine)}">Все кухни</button>
            ${cuisines().map((c) => html`<button type="button" class="chip" data-cuisine="${c}" aria-pressed="${String(cuisine === c)}">${c}</button>`)}
          </div>
        </div>
        <div id="grid">${grid()}</div>
      </div>`);
    const wrap = $('#catalog', root);
    $('#search', wrap).addEventListener('input', (e) => { q = e.target.value; mount($('#grid', wrap), grid()); });
    on(wrap, 'click', '[data-cuisine]', (e, el) => {
      cuisine = el.dataset.cuisine;
      wrap.querySelectorAll('[data-cuisine]').forEach((b) => b.setAttribute('aria-pressed', String(b === el)));
      mount($('#grid', wrap), grid());
    });
  }

  function liveBanner() {
    if (!liveOrder) return '';
    return html`
      <a class="live-order" href="#/orders/${liveOrder.id}">
        <span class="live-order__pulse" aria-hidden="true"></span>
        <span class="live-order__text">
          <b>Заказ №${orderNo(liveOrder)} из ${liveOrder.restaurant_name}</b>
          <span>${statusLabel(liveOrder.status)}, ${money(liveOrder.total)}</span>
        </span>
        <span class="btn btn--sm btn--saffron">Следить за заказом</span>
      </a>`;
  }

  function grid() {
    const list = filtered();
    if (!list.length) {
      return emptyState({
        art: '🔍',
        title: all.length ? 'Ничего не нашлось' : 'Рестораны пока не подключены',
        text: all.length ? 'Попробуйте другое название или выберите все кухни.' : 'Когда ресторан начнёт принимать заказы, он появится здесь.',
      });
    }
    return html`<div class="rgrid">
      ${list.map((r) => html`
        <a class="rcard" href="#/restaurants/${r.id}">
          ${coverBox(r.cuisine)}
          <div class="rcard__body">
            <h2 class="rcard__name">${r.name}</h2>
            <p class="rcard__desc">${r.description}</p>
            <div class="rcard__meta">
              <span>${icon('pin')}${r.address}</span>
            </div>
          </div>
        </a>`)}
    </div>`;
  }

  async function loadLive() {
    if (session.role !== 'customer') return null;
    try {
      const data = await api.orders({ status: ACTIVE_STATUSES.join(','), limit: 1 });
      return data?.items?.[0] || null;
    } catch {
      return null;
    }
  }

  return {
    title: 'Рестораны',
    async render(app) {
      root = app;
      const [data, live] = await Promise.all([api.restaurants({ limit: 100 }), loadLive()]);
      all = data?.items || [];
      liveOrder = live;
      draw();
    },
    async refresh() {
      const live = await loadLive();
      const box = $('#live-order', root);
      if (!box) return;
      if (JSON.stringify(live) !== JSON.stringify(liveOrder)) {
        liveOrder = live;
        mount(box, liveBanner());
      }
    },
  };
}
