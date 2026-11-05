// Страница ресторана: меню в духе бумажного (название — точки — цена) и корзина.
// Корзина хранится в браузере, оформление заказа — POST /orders с ключом
// идемпотентности, который не меняется, пока не изменилось содержимое корзины.

import { api } from '../api.js';
import { cart, session, RULES } from '../state.js';
import { html, mount, icon, toast, toastError, confirmDialog, busy, $ } from '../ui.js';
import { money, plural } from '../format.js';
import { navigate } from '../app.js';
import { coverBox, on, uuid, emptyState } from './common.js';

export default function restaurantView(id) {
  let root;
  let restaurant = null;
  let items = [];
  let comment = '';
  let idemKey = null;
  let drawerOpen = false;
  let unsubscribe = null;

  const canOrder = () => session.role === 'customer';

  function categories() {
    const map = new Map();
    for (const it of items) {
      const c = it.category || 'Другое';
      if (!map.has(c)) map.set(c, []);
      map.get(c).push(it);
    }
    return [...map.entries()];
  }

  function draw() {
    const cats = categories();
    mount(root, html`
      <div class="container" id="shop-page">
        <a class="back" href="#/restaurants">${icon('back')}Все рестораны</a>
        <section class="rhero">
          ${coverBox(restaurant.cuisine)}
          <div class="rhero__body">
            <h1 class="rhero__name">${restaurant.name}</h1>
            <p class="rhero__desc">${restaurant.description}</p>
            <div class="facts">
              <span>${icon('pin')}${restaurant.address}</span>
              <span>${icon('truck')}Доставка ${money(RULES.deliveryFee)}, от ${money(RULES.freeFrom)} бесплатно</span>
              <span>${icon('bag')}Заказ от ${money(RULES.minOrder)}</span>
            </div>
            ${restaurant.is_active ? '' : html`<p class="closed-note">Ресторан сейчас не принимает заказы. Меню доступно для просмотра.</p>`}
          </div>
        </section>

        <div class="shop">
          <div>
            ${cats.length > 1 ? html`
              <nav class="menu-nav" aria-label="Разделы меню">
                <div class="chips">${cats.map(([c], i) => html`<button type="button" class="chip" data-jump="cat-${i}">${c}</button>`)}</div>
              </nav>` : ''}
            ${cats.length ? cats.map(([c, list], i) => html`
              <section class="menu-section" id="cat-${i}" aria-labelledby="cat-${i}-title">
                <h2 class="menu-section__title" id="cat-${i}-title">${c}</h2>
                <div class="dishes">${list.map(dish)}</div>
              </section>`) : html`<div class="panel">${emptyState({ art: '📋', title: 'Меню пока пустое', text: 'Ресторан ещё не добавил блюда.' })}</div>`}
          </div>
          ${canOrder() ? html`<aside class="cart panel ${drawerOpen ? 'is-open' : ''}" id="cart" aria-label="Корзина">${cartInner()}</aside>` : ''}
        </div>
        ${canOrder() ? html`<button type="button" class="btn cart-fab" id="cart-fab">${fabInner()}</button>` : ''}
      </div>`);
    bind();
  }

  function dish(it) {
    const off = !it.is_available;
    return html`
      <article class="dish ${off ? 'is-off' : ''}" data-dish="${it.id}">
        <div class="dish__head">
          <h3 class="dish__name">${it.name}</h3>
          <span class="dish__leader" aria-hidden="true"></span>
          <span class="dish__price">${money(it.price)}</span>
        </div>
        ${it.description ? html`<p class="dish__desc">${it.description}</p>` : ''}
        <div class="dish__action" data-action-for="${it.id}">${dishAction(it)}</div>
      </article>`;
  }

  function dishAction(it) {
    if (!canOrder()) return '';
    if (!it.is_available) return html`<span class="tag-off">Временно нет</span>`;
    if (!restaurant.is_active) return '';
    const qty = cart.data.restaurantId === restaurant.id ? cart.qty(it.id) : 0;
    if (!qty) {
      return html`<button type="button" class="icon-btn icon-btn--add" data-add="${it.id}" aria-label="Добавить «${it.name}» в корзину">${icon('plus')}</button>`;
    }
    return stepper(it.id, qty, it.name);
  }

  function stepper(itemId, qty, name) {
    return html`<div class="stepper" role="group" aria-label="Количество «${name}»">
      <button type="button" class="icon-btn" data-dec="${itemId}" aria-label="Убрать одну порцию">${icon('minus')}</button>
      <span class="stepper__val" aria-live="polite">${qty}</span>
      <button type="button" class="icon-btn" data-inc="${itemId}" aria-label="Добавить ещё одну порцию">${icon('plus')}</button>
    </div>`;
  }

  // ---------------------------------------------------------------- корзина
  function totals() {
    const subtotal = cart.subtotal();
    const fee = subtotal >= RULES.freeFrom || subtotal === 0 ? 0 : RULES.deliveryFee;
    return { subtotal, fee, total: subtotal + fee };
  }

  function cartInner() {
    const lines = cart.data.lines;
    const count = cart.count();
    const { subtotal, fee, total } = totals();
    const foreign = cart.data.restaurantId && cart.data.restaurantId !== restaurant.id;
    if (!lines.length) {
      return html`
        <div class="cart__head">
          <div class="cart__head-row"><h2 class="cart__title">Корзина</h2>${closeBtn()}</div>
        </div>
        <div class="cart-empty"><b>Пока пусто</b>Добавьте блюда из меню, и они появятся здесь.</div>`;
    }
    const left = Math.max(0, RULES.freeFrom - subtotal);
    const fill = Math.min(100, Math.round((subtotal / RULES.freeFrom) * 100));
    const belowMin = subtotal < RULES.minOrder;
    return html`
      <div class="cart__head">
        <div class="cart__head-row"><h2 class="cart__title">Корзина</h2>${closeBtn()}</div>
        <p class="cart__from">${count} ${plural(count, 'порция', 'порции', 'порций')} из ${foreign ? html`<a href="#/restaurants/${cart.data.restaurantId}">${cart.data.restaurantName}</a>` : cart.data.restaurantName}</p>
      </div>
      <div class="cart__items">
        ${lines.map((l) => html`
          <div class="cart-line">
            <span class="cart-line__name">${l.name}</span>
            <span class="cart-line__sum">${money(l.price * l.qty)}</span>
            ${stepper(l.id, l.qty, l.name)}
          </div>`)}
      </div>
      <div class="cart__form">
        <div class="field">
          <label for="address">Адрес доставки</label>
          <input class="input" id="address" autocomplete="street-address" placeholder="Улица, дом, квартира" value="${cart.address}" maxlength="300">
        </div>
        <div class="field">
          <label for="comment">Комментарий для ресторана и курьера</label>
          <textarea class="textarea" id="comment" maxlength="500" placeholder="Например: без лука, домофон 25">${comment}</textarea>
        </div>
      </div>
      <div class="cart__totals">
        <div class="freebar">
          <div class="freebar__text">${left > 0 ? `До бесплатной доставки ${money(left)}` : 'Доставка бесплатная'}</div>
          <div class="freebar__track"><div class="freebar__fill" style="--fill:${fill}%"></div></div>
        </div>
        <div class="trow"><span>Блюда</span><span>${money(subtotal)}</span></div>
        <div class="trow"><span>Доставка</span><span>${fee ? money(fee) : 'бесплатно'}</span></div>
        <div class="trow trow--total"><span>Итого</span><span>${money(total)}</span></div>
        ${belowMin ? html`<p class="cart__warn">Минимальная сумма заказа ${money(RULES.minOrder)}, добавьте блюд ещё на ${money(RULES.minOrder - subtotal)}.</p>` : ''}
        <p class="form-error" id="checkout-error" hidden></p>
        <button type="button" class="btn btn--block" id="checkout" ${belowMin ? html`disabled` : ''}>Оформить и оплатить ${money(total)}</button>
      </div>`;
  }

  function closeBtn() {
    return html`<button type="button" class="icon-btn cart__close" data-close-cart aria-label="Свернуть корзину">${icon('x')}</button>`;
  }

  function fabInner() {
    const count = cart.count();
    return html`<span class="cart-fab__label">${icon('bag')}Корзина<span class="cart-fab__count">${count}</span></span><span>${money(totals().total)}</span>`;
  }

  function redrawCart() {
    const box = $('#cart', root);
    if (!box) return;
    const focusId = document.activeElement?.id;
    mount(box, cartInner());
    const fab = $('#cart-fab', root);
    if (fab) {
      mount(fab, fabInner());
      fab.hidden = cart.count() === 0;
    }
    bindCartForm();
    if (focusId === 'checkout') $('#checkout', root)?.focus();
  }

  function redrawDishes() {
    for (const it of items) {
      const slot = root.querySelector(`[data-action-for="${it.id}"]`);
      if (slot) mount(slot, dishAction(it));
    }
  }

  // ---------------------------------------------------------------- события
  async function add(itemId) {
    const it = items.find((x) => x.id === itemId);
    if (!it) return;
    if (!cart.isFrom(restaurant.id) && cart.count() > 0) {
      const ok = await confirmDialog({
        title: 'Начать новую корзину?',
        text: `В корзине блюда из ${cart.data.restaurantName}. Заказ оформляется в одном ресторане, поэтому корзина будет очищена.`,
        confirm: 'Очистить и добавить',
      });
      if (!ok) return;
    }
    cart.add(restaurant, it);
  }

  function bind() {
    const wrap = $('#shop-page', root);
    on(wrap, 'click', '[data-add]', (e, el) => add(el.dataset.add));
    on(wrap, 'click', '[data-inc]', (e, el) => cart.inc(el.dataset.inc));
    on(wrap, 'click', '[data-dec]', (e, el) => cart.dec(el.dataset.dec));
    on(wrap, 'click', '[data-jump]', (e, el) => document.getElementById(el.dataset.jump)?.scrollIntoView({ behavior: 'smooth', block: 'start' }));
    on(wrap, 'click', '#cart-fab', () => setDrawer(true));
    on(wrap, 'click', '[data-close-cart]', () => setDrawer(false));
    on(wrap, 'click', '#checkout', (e, el) => checkout(el));
    const fab = $('#cart-fab', root);
    if (fab) fab.hidden = cart.count() === 0;
    bindCartForm();
  }

  function bindCartForm() {
    $('#address', root)?.addEventListener('input', (e) => cart.setAddress(e.target.value));
    $('#comment', root)?.addEventListener('input', (e) => { comment = e.target.value; });
  }

  function setDrawer(open) {
    drawerOpen = open;
    $('#cart', root)?.classList.toggle('is-open', open);
  }

  async function checkout(btn) {
    const err = $('#checkout-error', root);
    const showErr = (m) => { err.textContent = m; err.hidden = !m; };
    showErr('');
    const address = (cart.address || '').trim();
    if (!address) {
      showErr('Укажите адрес доставки');
      $('#address', root)?.focus();
      return;
    }
    if (!idemKey) idemKey = uuid();
    const body = {
      restaurant_id: cart.data.restaurantId,
      items: cart.data.lines.map((l) => ({ menu_item_id: l.id, quantity: l.qty })),
      delivery_address: address,
      comment: comment.trim(),
    };
    try {
      const order = await busy(btn, () => api.createOrder(body, idemKey));
      idemKey = null;
      comment = '';
      cart.clear();
      toast(`Заказ №${order.number || order.id.slice(0, 8).toUpperCase()} оформлен`, { body: 'Проводим оплату, это займёт пару секунд.', kind: 'success' });
      navigate(`#/orders/${order.id}`);
    } catch (e) {
      showErr(e.message);
      if (e.status === 422 || e.status === 400) toastError(e);
    }
  }

  return {
    title: 'Меню',
    async render(app) {
      root = app;
      const [r, menu] = await Promise.all([api.restaurant(id), api.menu(id)]);
      restaurant = r;
      items = menu?.items || [];
      this.title = restaurant.name;
      document.title = `${restaurant.name} — FoodFlow`;
      draw();
      unsubscribe = cart.onChange(() => {
        idemKey = null; // корзина изменилась — это уже другой заказ
        redrawDishes();
        redrawCart();
      });
    },
    destroy() {
      unsubscribe?.();
    },
  };
}
