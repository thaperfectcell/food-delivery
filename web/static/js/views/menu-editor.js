// Экран ресторана «Меню и ресторан»: управление позициями меню (цена,
// доступность, удаление, добавление) и параметрами ресторана.

import { api } from '../api.js';
import { html, mount, icon, toast, toastError, confirmDialog, busy, $ } from '../ui.js';
import { money } from '../format.js';
import { on } from './common.js';

const CUISINES = ['Итальянская', 'Японская', 'Американская', 'Грузинская', 'Китайская', 'Русская', 'Индийская', 'Мексиканская'];

/** «549» или «549,90» → копейки. */
function toKopecks(v) {
  const n = Number(String(v).replace(/\s/g, '').replace(',', '.'));
  return Number.isFinite(n) && n > 0 ? Math.round(n * 100) : null;
}
const rub = (k) => String(k / 100).replace('.', ',');

export default function menuEditorView() {
  let root;
  let restaurants = [];
  let restaurant = null;
  let items = [];

  function groups() {
    const by = new Map();
    items.forEach((it) => {
      const c = it.category || 'Без категории';
      if (!by.has(c)) by.set(c, []);
      by.get(c).push(it);
    });
    return [...by.entries()];
  }

  function itemRow(it) {
    return html`
      <tr data-id="${it.id}">
        <td>
          <div class="mtable__name">${it.name}</div>
          ${it.description ? html`<div class="mtable__desc">${it.description}</div>` : ''}
        </td>
        <td>
          <input class="input price-input" type="text" inputmode="decimal" value="${rub(it.price)}"
            data-price aria-label="Цена «${it.name}», ₽">
        </td>
        <td>
          <label class="switch" title="${it.is_available ? 'Доступно для заказа' : 'Скрыто из заказа'}">
            <input type="checkbox" data-avail ${it.is_available ? html`checked` : ''}>
            <span class="switch__track"></span>
            <span class="small">${it.is_available ? 'В продаже' : 'Стоп-лист'}</span>
          </label>
        </td>
        <td class="right">
          <button class="icon-btn" type="button" data-del title="Удалить" aria-label="Удалить «${it.name}»">${icon('trash')}</button>
        </td>
      </tr>`;
  }

  function createForm() {
    return html`
      <div class="container">
        <div class="page-head"><div>
          <h1 class="page-title">Создание ресторана</h1>
          <p class="page-sub">После создания добавьте блюда — ресторан сразу появится в каталоге.</p>
        </div></div>
        <form class="panel panel__body" id="create-rest" style="max-width:560px">
          <div class="field"><label for="r-name">Название</label><input class="input" id="r-name" required maxlength="100"></div>
          <div class="field"><label for="r-addr">Адрес</label><input class="input" id="r-addr" required maxlength="200"></div>
          <div class="field"><label for="r-cuisine">Кухня</label>
            <select class="input" id="r-cuisine">${CUISINES.map((c) => html`<option>${c}</option>`)}</select></div>
          <div class="field"><label for="r-desc">Описание</label><textarea class="textarea" id="r-desc" maxlength="300"></textarea></div>
          <button class="btn" type="submit" style="margin-top:18px">Создать ресторан</button>
        </form>
      </div>`;
  }

  function draw() {
    if (!restaurant) { mount(root, createForm()); return; }
    mount(root, html`
      <div class="container">
        <div class="page-head">
          <div>
            <h1 class="page-title">${restaurant.name}</h1>
            <p class="page-sub">${restaurant.cuisine} кухня · ${restaurant.address}</p>
          </div>
          <label class="switch switch--lg">
            <input type="checkbox" id="rest-active" ${restaurant.is_active ? html`checked` : ''}>
            <span class="switch__track"></span>
            <b>${restaurant.is_active ? 'Принимаем заказы' : 'Приём закрыт'}</b>
          </label>
        </div>
        <div class="editor">
          <div class="panel table-wrap">
            <table class="mtable">
              <thead><tr><th>Блюдо</th><th>Цена, ₽</th><th>Доступность</th><th></th></tr></thead>
              <tbody>
                ${groups().map(([cat, list]) => html`
                  <tr class="cat-row"><td colspan="4">${cat}</td></tr>
                  ${list.map(itemRow)}`)}
              </tbody>
            </table>
            ${!items.length ? html`<div class="lane__empty">В меню пока нет блюд</div>` : ''}
          </div>
          <form class="panel" id="add-item">
            <div class="panel__head"><h2 class="panel__title">Новое блюдо</h2></div>
            <div class="panel__body">
              <div class="field"><label for="i-name">Название</label><input class="input" id="i-name" required maxlength="100"></div>
              <div class="form-row" style="margin-top:14px">
                <div class="field"><label for="i-cat">Категория</label>
                  <input class="input" id="i-cat" list="cats" maxlength="50" placeholder="Пицца"></div>
                <div class="field"><label for="i-price">Цена, ₽</label><input class="input" id="i-price" inputmode="decimal" required placeholder="450"></div>
              </div>
              <datalist id="cats">${groups().map(([c]) => html`<option value="${c}">`)}</datalist>
              <div class="field" style="margin-top:14px"><label for="i-desc">Описание</label><textarea class="textarea" id="i-desc" maxlength="300" placeholder="Состав, вес, порция"></textarea></div>
              <button class="btn btn--block" type="submit" style="margin-top:18px">${icon('plus')} Добавить в меню</button>
            </div>
          </form>
        </div>
      </div>`);
  }

  async function reload() {
    const data = await api.myRestaurants();
    restaurants = data?.items || [];
    restaurant = restaurants.find((r) => r.id === restaurant?.id) || restaurants[0] || null;
    items = restaurant ? (await api.menu(restaurant.id))?.items || [] : [];
    draw();
  }

  async function savePrice(input) {
    const id = input.closest('[data-id]').dataset.id;
    const it = items.find((x) => x.id === id);
    const price = toKopecks(input.value);
    if (!price) { toast('Цена должна быть положительным числом', { kind: 'error' }); input.value = rub(it.price); return; }
    if (price === it.price) return;
    try {
      const upd = await api.updateMenuItem(restaurant.id, id, { price });
      Object.assign(it, upd);
      toast('Цена обновлена', { body: `${it.name}: ${money(it.price)}`, kind: 'success' });
    } catch (e) { toastError(e); input.value = rub(it.price); }
  }

  function bind() {
    on(root, 'change', '[data-price]', (e, el) => savePrice(el));
    on(root, 'keydown', '[data-price]', (e, el) => { if (e.key === 'Enter') el.blur(); });
    on(root, 'change', '[data-avail]', async (e, el) => {
      const id = el.closest('[data-id]').dataset.id;
      try {
        await api.updateMenuItem(restaurant.id, id, { is_available: el.checked });
        await reload();
      } catch (err) { toastError(err); el.checked = !el.checked; }
    });
    on(root, 'click', '[data-del]', async (e, el) => {
      const id = el.closest('[data-id]').dataset.id;
      const it = items.find((x) => x.id === id);
      const ok = await confirmDialog({ title: `Удалить «${it.name}»?`, text: 'Блюдо исчезнет из меню. Уже оформленные заказы не изменятся.', confirm: 'Удалить', danger: true });
      if (!ok) return;
      try { await api.deleteMenuItem(restaurant.id, id); await reload(); } catch (err) { toastError(err); }
    });
    on(root, 'change', '#rest-active', async (e, el) => {
      try {
        restaurant = await api.updateRestaurant(restaurant.id, { is_active: el.checked });
        toast(el.checked ? 'Ресторан снова принимает заказы' : 'Приём заказов приостановлен', { kind: 'success' });
        draw();
      } catch (err) { toastError(err); el.checked = !el.checked; }
    });
    on(root, 'submit', '#add-item', async (e, form) => {
      e.preventDefault();
      const price = toKopecks($('#i-price', form).value);
      if (!price) { toast('Укажите цену', { kind: 'error' }); return; }
      await busy(form.querySelector('button[type=submit]'), async () => {
        try {
          const it = await api.addMenuItem(restaurant.id, {
            name: $('#i-name', form).value.trim(),
            category: $('#i-cat', form).value.trim(),
            description: $('#i-desc', form).value.trim(),
            price,
          });
          toast('Блюдо добавлено', { body: `${it.name} — ${money(it.price)}`, kind: 'success' });
          await reload();
        } catch (err) { toastError(err); }
      });
    });
    on(root, 'submit', '#create-rest', async (e, form) => {
      e.preventDefault();
      await busy(form.querySelector('button[type=submit]'), async () => {
        try {
          restaurant = await api.createRestaurant({
            name: $('#r-name', form).value.trim(),
            address: $('#r-addr', form).value.trim(),
            cuisine: $('#r-cuisine', form).value,
            description: $('#r-desc', form).value.trim(),
          });
          toast('Ресторан создан', { kind: 'success' });
          await reload();
        } catch (err) { toastError(err); }
      });
    });
  }

  return {
    title: 'Меню и ресторан',
    async render(app) {
      root = document.createElement('div');
      app.replaceChildren(root);
      bind();
      await reload();
    },
    // Меню меняет только сам ресторан — фоновое обновление не требуется.
    onNotification: () => false,
  };
}
