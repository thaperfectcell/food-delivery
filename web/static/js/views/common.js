// Общие элементы экранов: статусы, обложки ресторанов, сводки по заказам.

import { html } from '../ui.js';
import { ORDER_STATUS, PAYMENT_STATUS, DELIVERY_STATUS, COURIER_STATUS, cover } from '../format.js';

const LABELS = { order: ORDER_STATUS, payment: PAYMENT_STATUS, delivery: DELIVERY_STATUS, courier: COURIER_STATUS };

export const cap = (s) => (s ? s[0].toUpperCase() + s.slice(1) : s);

/** Плашка статуса заказа, оплаты, доставки или курьера. */
export function pill(status, kind = 'order') {
  const label = LABELS[kind]?.[status] || status || '—';
  return html`<span class="pill pill--${status}">${cap(label)}</span>`;
}

/** Обложка ресторана: цвет и эмодзи по кухне. */
export function coverBox(cuisine, { label = true } = {}) {
  const c = cover(cuisine);
  return html`<div class="cover" style="--cover:${c.color}">
    <span class="cover__emoji" aria-hidden="true">${c.emoji}</span>
    ${label && cuisine ? html`<span class="cover__cuisine">${cuisine} кухня</span>` : ''}
  </div>`;
}

/** «Маргарита ×2, Лимонад». */
export function itemsSummary(items = []) {
  return items.map((i) => (i.quantity > 1 ? `${i.name} ×${i.quantity}` : i.name)).join(', ');
}

export const countItems = (items = []) => items.reduce((s, i) => s + (i.quantity || 0), 0);

/** Время перехода заказа в статус (по истории), если оно известно. */
export function statusTime(order, status) {
  const h = (order?.history || []).filter((x) => x.status === status);
  return h.length ? h[h.length - 1].at : null;
}

export const ACTIVE_STATUSES = ['created', 'paid', 'accepted', 'ready', 'in_delivery'];
export const isActive = (o) => ACTIVE_STATUSES.includes(o?.status);

/** Делегирование событий внутри корня экрана. */
export function on(root, event, selector, fn) {
  root.addEventListener(event, (e) => {
    const el = e.target.closest(selector);
    if (el && root.contains(el)) fn(e, el);
  });
}

/** UUID v4 для заголовка Idempotency-Key (с запасным вариантом без randomUUID). */
export function uuid() {
  if (globalThis.crypto?.randomUUID) return crypto.randomUUID();
  const b = new Uint8Array(16);
  (globalThis.crypto || window.msCrypto).getRandomValues(b);
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const h = [...b].map((x) => x.toString(16).padStart(2, '0')).join('');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

/** Пустое состояние экрана. */
export function emptyState({ art = '🍽️', title, text = '', action = '' }) {
  return html`<div class="empty">
    <div class="empty__art" aria-hidden="true">${art}</div>
    <h2 class="empty__title">${title}</h2>
    ${text ? html`<p>${text}</p>` : ''}
    ${action}
  </div>`;
}
