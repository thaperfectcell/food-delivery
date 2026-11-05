// Страница заказа: «маршрут» статусов от оформления до двери, состав заказа,
// оплата и доставка (ответ API Gateway объединяет данные трёх сервисов).

import { api } from '../api.js';
import { session } from '../state.js';
import { html, mount, icon, toast, toastError, confirmDialog, busy, $ } from '../ui.js';
import { money, time, dateTime, orderNo } from '../format.js';
import { pill, on, statusTime } from './common.js';

const STOPS = [
  ['created', 'Оформлен'],
  ['paid', 'Оплачен'],
  ['accepted', 'Готовится'],
  ['ready', 'Готов'],
  ['in_delivery', 'В пути'],
  ['delivered', 'Доставлен'],
];
const INDEX = Object.fromEntries(STOPS.map(([s], i) => [s, i]));

function message(o) {
  const courier = o.courier_name || o.delivery?.courier_name;
  switch (o.status) {
    case 'created':
      return ['💳', 'Проводим оплату', 'Обычно это занимает несколько секунд.'];
    case 'paid':
      return ['🧾', 'Ждём подтверждения ресторана', 'Ресторан уже видит заказ и скоро возьмёт его в работу.'];
    case 'accepted':
      return ['🍳', 'Ресторан готовит заказ', courier ? `Курьер ${courier} заберёт его, как только всё будет готово.` : 'Тем временем подбираем курьера.'];
    case 'ready':
      return ['🛍️', 'Заказ готов', courier ? `${courier} уже едет в ресторан за заказом.` : 'Ищем свободного курьера.'];
    case 'in_delivery':
      return ['🛵', 'Курьер в пути', `${courier || 'Курьер'} везёт заказ по адресу ${o.delivery_address}.`];
    case 'delivered':
      return ['✅', 'Заказ доставлен', 'Приятного аппетита! Спасибо, что заказали в FoodFlow.'];
    case 'cancelled': {
      const refund = o.payment?.status === 'refunded' ? ' Деньги вернулись на карту.' : '';
      return ['✖️', 'Заказ отменён', `${o.cancel_reason ? capitalize(o.cancel_reason) + '.' : ''}${refund}`];
    }
    default:
      return ['📦', 'Заказ', ''];
  }
}
const capitalize = (s) => (s ? s[0].toUpperCase() + s.slice(1) : s);

/** Отметка, до которой заказ дошёл перед отменой. */
function lastReached(o) {
  let idx = 0;
  for (const h of o.history || []) if (h.status in INDEX) idx = Math.max(idx, INDEX[h.status]);
  return idx;
}

function route(o) {
  const cancelled = o.status === 'cancelled';
  const cur = cancelled ? lastReached(o) : INDEX[o.status] ?? 0;
  const delivered = o.status === 'delivered';
  const rider = o.status === 'in_delivery';
  return html`
    <div class="route ${cancelled ? 'is-cancelled' : ''}" style="--n:${STOPS.length};--done:${cancelled ? cur + 1 : cur};--rider:4.45" role="list" aria-label="Этапы заказа">
      ${rider ? html`<span class="rider" aria-hidden="true">🛵</span>` : ''}
      ${STOPS.map(([status, label], i) => {
        let cls = '';
        let mark = html`${i + 1}`;
        let at = statusTime(o, status);
        let text = label;
        if (cancelled && i === cur + 1) {
          cls = 'is-cancel';
          mark = icon('x');
          text = 'Отменён';
          at = statusTime(o, 'cancelled');
        } else if (i < cur || (i === cur && (delivered || cancelled))) {
          cls = 'is-done';
          mark = icon('check');
        } else if (i === cur && !cancelled) {
          cls = 'is-current';
        }
        return html`<div class="stop ${cls}" role="listitem" ${cls === 'is-current' ? html`aria-current="step"` : ''}>
          <span class="stop__dot">${mark}</span>
          <span class="stop__label">${text}</span>
          <span class="stop__time">${at ? time(at) : ''}</span>
        </div>`;
      })}
    </div>`;
}

export default function orderView(id) {
  let root;
  let order = null;
  let snapshot = '';

  const backLink = () => {
    switch (session.role) {
      case 'restaurant': return ['#/kitchen', 'К заказам ресторана'];
      case 'courier': return ['#/courier', 'К доставкам'];
      case 'admin': return ['#/admin', 'К обзору'];
      default: return ['#/orders', 'Мои заказы'];
    }
  };

  function draw() {
    const o = order;
    const [emoji, title, sub] = message(o);
    const [backHref, backText] = backLink();
    const p = o.payment;
    const d = o.delivery;
    mount(root, html`
      <div class="container" id="order-page">
        <a class="back" href="${backHref}">${icon('back')}${backText}</a>
        <div class="order-head">
          <div>
            <h1 class="order-head__title">Заказ №${orderNo(o)}</h1>
            <p class="order-head__meta">${o.restaurant_name}, оформлен ${dateTime(o.created_at)}</p>
          </div>
          ${pill(o.status)}
        </div>

        <section class="trip ${o.status === 'cancelled' ? 'trip--cancelled' : ''} ${o.status === 'delivered' ? 'trip--delivered' : ''}">
          <div class="trip__message">
            <span class="trip__icon" aria-hidden="true">${emoji}</span>
            <div><h2>${title}</h2>${sub ? html`<p>${sub}</p>` : ''}</div>
          </div>
          ${route(o)}
        </section>

        <div class="order-grid">
          <section class="panel">
            <div class="panel__head"><h2 class="panel__title">Состав заказа</h2><span class="muted small">${o.items?.length || 0} поз.</span></div>
            <div class="panel__body">
              ${(o.items || []).map((it) => html`
                <div class="receipt-line">
                  <span class="receipt-line__name">${it.name}</span>
                  <span class="receipt-line__qty">${it.quantity} × ${money(it.price)}</span>
                  <span class="receipt-line__sum">${money(it.total ?? it.price * it.quantity)}</span>
                </div>`)}
              <div class="receipt-totals">
                <div class="trow"><span>Блюда</span><span>${money(o.items_total)}</span></div>
                <div class="trow"><span>Доставка</span><span>${o.delivery_fee ? money(o.delivery_fee) : 'бесплатно'}</span></div>
                <div class="trow trow--total"><span>Итого</span><span>${money(o.total)}</span></div>
              </div>
              ${o.comment ? html`<p class="note" style="margin-top:16px"><b>Комментарий:</b> ${o.comment}</p>` : ''}
            </div>
          </section>

          <div class="side-stack">
            <section class="panel">
              <div class="panel__head"><h2 class="panel__title">Доставка</h2>${d ? pill(d.status, 'delivery') : ''}</div>
              <div class="panel__body">
                <div class="addr">
                  <div class="addr__point"><span class="addr__mark"></span><div><b>${o.restaurant_name}</b><span>${o.restaurant_address}</span></div></div>
                  <div class="addr__point addr__point--to"><span class="addr__mark"></span><div><b>Адрес доставки</b><span>${o.delivery_address}</span></div></div>
                </div>
                <dl style="margin:16px 0 0">
                  <div class="info-row"><dt>Курьер</dt><dd>${o.courier_name || d?.courier_name || (o.status === 'cancelled' ? 'не назначался' : 'подбираем')}</dd></div>
                  ${d?.picked_up_at ? html`<div class="info-row"><dt>Забрал заказ</dt><dd>${time(d.picked_up_at)}</dd></div>` : ''}
                  ${d?.delivered_at ? html`<div class="info-row"><dt>Доставил</dt><dd>${time(d.delivered_at)}</dd></div>` : ''}
                </dl>
              </div>
            </section>

            <section class="panel">
              <div class="panel__head"><h2 class="panel__title">Оплата</h2>${p ? pill(p.status, 'payment') : pill('pending', 'payment')}</div>
              <div class="panel__body">
                <dl style="margin:0">
                  <div class="info-row"><dt>Сумма</dt><dd>${money(p?.amount ?? o.total)}</dd></div>
                  ${p?.provider_ref ? html`<div class="info-row"><dt>Транзакция</dt><dd class="num">${p.provider_ref}</dd></div>` : ''}
                  ${p?.failure_reason ? html`<div class="info-row"><dt>Причина</dt><dd>${capitalize(p.failure_reason)}</dd></div>` : ''}
                </dl>
              </div>
            </section>

            ${actions(o)}
          </div>
        </div>
      </div>`);
    bind();
  }

  function actions(o) {
    const role = session.role;
    if (role === 'customer' && (o.status === 'created' || o.status === 'paid')) {
      return html`<button type="button" class="btn btn--danger btn--block" data-act="cancel">Отменить заказ</button>`;
    }
    if (role === 'restaurant') {
      if (o.status === 'paid') {
        return html`<div class="order-actions"><button type="button" class="btn" data-act="accept">Принять заказ</button><button type="button" class="btn btn--danger" data-act="reject">Отклонить</button></div>`;
      }
      if (o.status === 'accepted') {
        return html`<div class="order-actions"><button type="button" class="btn" data-act="ready">Заказ готов</button><button type="button" class="btn btn--danger" data-act="reject">Отклонить</button></div>`;
      }
    }
    return '';
  }

  function bind() {
    const wrap = $('#order-page', root);
    on(wrap, 'click', '[data-act]', async (e, btn) => {
      const act = btn.dataset.act;
      try {
        if (act === 'cancel') {
          const reason = await confirmDialog({
            title: 'Отменить заказ?',
            text: order.status === 'paid' ? 'Заказ уже оплачен: деньги вернутся на карту автоматически.' : 'Оплата ещё не прошла, списания не будет.',
            confirm: 'Отменить заказ', cancel: 'Не отменять', danger: true,
            input: { label: 'Причина (необязательно)', placeholder: 'Например, передумал', suggestions: ['Передумал', 'Ошибся в адресе', 'Долго ждать'] },
          });
          if (reason === null) return;
          await busy(btn, () => api.cancelOrder(id, reason));
          toast('Заказ отменён', { kind: 'success' });
        } else if (act === 'accept') {
          await busy(btn, () => api.acceptOrder(id));
          toast('Заказ принят', { body: 'Курьер будет назначен автоматически.', kind: 'success' });
        } else if (act === 'ready') {
          await busy(btn, () => api.readyOrder(id));
          toast('Заказ готов к выдаче', { kind: 'success' });
        } else if (act === 'reject') {
          const reason = await confirmDialog({
            title: 'Отклонить заказ?', text: 'Клиенту вернутся деньги, курьер освободится.', confirm: 'Отклонить', danger: true,
            input: { label: 'Причина', placeholder: 'Что случилось', suggestions: ['Закончились ингредиенты', 'Кухня перегружена', 'Ресторан закрывается'] },
          });
          if (reason === null) return;
          await busy(btn, () => api.rejectOrder(id, reason));
          toast('Заказ отклонён', { kind: 'success' });
        }
        await load(true);
      } catch (err) {
        toastError(err);
      }
    });
  }

  async function load(force = false) {
    const o = await api.order(id);
    const snap = JSON.stringify(o);
    if (force || snap !== snapshot) {
      snapshot = snap;
      order = o;
      draw();
    }
  }

  return {
    title: 'Заказ',
    async render(app) {
      root = app;
      await load(true);
      this.title = `Заказ №${orderNo(order)}`;
      document.title = `${this.title} — FoodFlow`;
    },
    async refresh() {
      if (document.querySelector('dialog[open]')) return;
      await load();
    },
    onNotification(n) {
      return !n.order_id || n.order_id === id;
    },
  };
}
