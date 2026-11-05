// Вход и регистрация. Демо-аккаунты позволяют за один клик посмотреть систему
// глазами клиента, ресторана, курьера и администратора.

import { api } from '../api.js';
import { html, raw, mount, brandMark, busy, $ } from '../ui.js';
import { signIn } from '../app.js';
import { on } from './common.js';

const DEMO_PASSWORD = 'demo12345';
const DEMO = [
  { email: 'client@foodflow.local', role: 'Клиент', who: 'Алексей Иванов' },
  { email: 'mario@foodflow.local', role: 'Ресторан', who: 'Пиццерия «Марио»' },
  { email: 'courier@foodflow.local', role: 'Курьер', who: 'Иван Петров' },
  { email: 'admin@foodflow.local', role: 'Администратор', who: 'Обзор платформы' },
];

const ROLES = [
  { value: 'customer', title: 'Клиент', hint: 'заказываю еду' },
  { value: 'restaurant', title: 'Ресторан', hint: 'готовлю заказы' },
  { value: 'courier', title: 'Курьер', hint: 'доставляю' },
];

function routeArt() {
  return raw(`<svg class="auth__route" viewBox="0 0 560 210" role="img" aria-label="Путь заказа: ресторан, курьер, дверь клиента">
    <path d="M70 150 C 160 150, 205 92, 290 92 S 420 52, 492 52" fill="none" stroke="#F0B323" stroke-width="5" stroke-linecap="round" stroke-dasharray="0.1 15"/>
    <g transform="translate(70 150)">
      <rect x="-30" y="-30" width="60" height="60" rx="18" fill="#fff"/>
      <text y="2" font-size="30" text-anchor="middle" dominant-baseline="central">🍕</text>
      <text y="56" font-size="15" fill="#CFE3D6" text-anchor="middle" font-family="Inter, sans-serif">Ресторан</text>
    </g>
    <g transform="translate(290 92)">
      <circle r="30" fill="#12502F" stroke="#F0B323" stroke-width="3"/>
      <text y="2" font-size="28" text-anchor="middle" dominant-baseline="central">🛵</text>
      <text y="56" font-size="15" fill="#CFE3D6" text-anchor="middle" font-family="Inter, sans-serif">Курьер</text>
    </g>
    <g transform="translate(492 52)">
      <circle r="30" fill="#F0B323"/>
      <text y="2" font-size="28" text-anchor="middle" dominant-baseline="central">🏠</text>
      <text y="56" font-size="15" fill="#CFE3D6" text-anchor="middle" font-family="Inter, sans-serif">Ваша дверь</text>
    </g>
  </svg>`);
}

export default function loginView() {
  let tab = 'login';
  let root;

  function draw() {
    mount(root, html`
      <div class="auth">
        <section class="auth__brand">
          <a class="brand" href="#/login">${brandMark()}<span class="brand__name">FoodFlow</span></a>
          <div class="auth__pitch">
            <h1>От кухни ресторана до вашей двери</h1>
            <p>Закажите в любимом ресторане и следите за каждым шагом: оплата, приготовление, курьер в пути.</p>
          </div>
          ${routeArt()}
          <ol class="auth__legend">
            <li><b>Оплата</b> сразу при оформлении</li>
            <li><b>Кухня</b> видит заказ мгновенно</li>
            <li><b>Курьер</b> назначается автоматически</li>
          </ol>
        </section>

        <section class="auth__panel">
          <div class="auth__card">
            <div class="tabs" role="tablist" aria-label="Вход или регистрация">
              <button type="button" role="tab" data-tab="login" aria-selected="${String(tab === 'login')}">Вход</button>
              <button type="button" role="tab" data-tab="register" aria-selected="${String(tab === 'register')}">Регистрация</button>
            </div>
            ${tab === 'login' ? loginForm() : registerForm()}
            <div class="demo">
              <div class="demo__title">Демо-доступ</div>
              <p class="demo__hint">Войдите одним кликом под готовой ролью. Пароль у всех демо-аккаунтов: ${DEMO_PASSWORD}</p>
              <div class="demo__list">
                ${DEMO.map((d) => html`
                  <button type="button" class="demo__btn" data-demo="${d.email}">
                    <b>${d.role}</b><span>${d.who}</span>
                  </button>`)}
              </div>
            </div>
          </div>
        </section>
      </div>`);
    bind();
  }

  function loginForm() {
    return html`
      <form id="auth-form" novalidate>
        <h1 class="auth__title">С возвращением</h1>
        <p class="auth__sub">Войдите, чтобы заказать еду или продолжить работу.</p>
        <div class="field">
          <label for="email">Email</label>
          <input class="input" id="email" name="email" type="email" autocomplete="username" required placeholder="name@example.ru">
        </div>
        <div class="field">
          <label for="password">Пароль</label>
          <input class="input" id="password" name="password" type="password" autocomplete="current-password" required>
        </div>
        <p class="form-error" id="form-error" hidden></p>
        <button class="btn btn--block" type="submit" style="margin-top:20px">Войти</button>
      </form>`;
  }

  function registerForm() {
    return html`
      <form id="auth-form" novalidate>
        <h1 class="auth__title">Новый аккаунт</h1>
        <p class="auth__sub">Выберите, как будете пользоваться FoodFlow.</p>
        <fieldset class="field" style="border:0;padding:0;margin:0">
          <legend class="field__label" style="margin-bottom:6px">Роль</legend>
          <div class="roles">
            ${ROLES.map((r, i) => html`
              <label><input type="radio" name="role" value="${r.value}" ${i === 0 ? raw('checked') : ''}>
                <span>${r.title}<small>${r.hint}</small></span></label>`)}
          </div>
        </fieldset>
        <div class="field">
          <label for="name">Имя</label>
          <input class="input" id="name" name="name" autocomplete="name" required maxlength="100">
        </div>
        <div class="form-row" style="margin-top:14px">
          <div class="field">
            <label for="email">Email</label>
            <input class="input" id="email" name="email" type="email" autocomplete="email" required>
          </div>
          <div class="field" style="margin-top:0">
            <label for="phone">Телефон</label>
            <input class="input" id="phone" name="phone" type="tel" autocomplete="tel" placeholder="+7">
          </div>
        </div>
        <div class="field">
          <label for="password">Пароль</label>
          <input class="input" id="password" name="password" type="password" autocomplete="new-password" required minlength="8">
          <span class="field__hint">Не короче 8 символов</span>
        </div>
        <p class="form-error" id="form-error" hidden></p>
        <button class="btn btn--block" type="submit" style="margin-top:20px">Создать аккаунт</button>
      </form>`;
  }

  function showError(msg) {
    const el = $('#form-error', root);
    el.textContent = msg;
    el.hidden = !msg;
  }

  function bind() {
    const wrap = root.firstElementChild;
    on(wrap, 'click', '[data-tab]', (e, el) => {
      if (tab === el.dataset.tab) return;
      tab = el.dataset.tab;
      draw();
      $('#auth-form input:not([type=radio])', root)?.focus();
    });
    on(wrap, 'click', '[data-demo]', async (e, el) => {
      showError('');
      try {
        const resp = await busy(el, () => api.login(el.dataset.demo, DEMO_PASSWORD));
        await signIn(resp);
      } catch (err) {
        showError(err.message);
      }
    });
    const form = $('#auth-form', root);
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      showError('');
      const f = new FormData(form);
      const email = String(f.get('email') || '').trim();
      const password = String(f.get('password') || '');
      const btn = form.querySelector('[type=submit]');
      try {
        if (tab === 'login') {
          if (!email || !password) { showError('Введите email и пароль'); return; }
          const resp = await busy(btn, () => api.login(email, password));
          await signIn(resp);
        } else {
          const name = String(f.get('name') || '').trim();
          if (!name || !email) { showError('Заполните имя и email'); return; }
          if (password.length < 8) { showError('Пароль должен быть не короче 8 символов'); return; }
          const resp = await busy(btn, () => api.register({
            email, password, name, phone: String(f.get('phone') || '').trim(), role: f.get('role'),
          }));
          await signIn(resp);
        }
      } catch (err) {
        showError(err.message);
      }
    });
  }

  return {
    title: 'Вход',
    async render(app) {
      root = app;
      draw();
      $('#email', root)?.focus({ preventScroll: true });
    },
  };
}
