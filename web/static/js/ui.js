// Небольшой набор средств построения интерфейса без фреймворков:
// безопасные HTML-шаблоны, иконки, всплывающие сообщения и диалоги.

const ESC = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };

class Raw {
  constructor(s) { this.s = s; }
  toString() { return this.s; }
}

/** Помечает строку как уже безопасный HTML. */
export const raw = (s) => new Raw(String(s));

/** Экранирует значение для вставки в HTML. */
export function esc(v) {
  return String(v ?? '').replace(/[&<>"']/g, (c) => ESC[c]);
}

function renderValue(v) {
  if (v === null || v === undefined || v === false) return '';
  if (v instanceof Raw) return v.s;
  if (Array.isArray(v)) return v.map(renderValue).join('');
  return esc(v);
}

/** Тегированный шаблон: интерполяции экранируются, если это не raw()/html``. */
export function html(strings, ...values) {
  let out = strings[0];
  for (let i = 0; i < values.length; i++) out += renderValue(values[i]) + strings[i + 1];
  return new Raw(out);
}

/** Заменяет содержимое элемента. */
export function mount(el, content) {
  el.innerHTML = renderValue(content);
}

export const $ = (sel, root = document) => root.querySelector(sel);
export const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

// ------------------------------------------------------------------ icons
const paths = {
  bell: '<path d="M6 8a6 6 0 1 1 12 0c0 7 3 9 3 9H3s3-2 3-9"/><path d="M10.3 21a1.94 1.94 0 0 0 3.4 0"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  minus: '<path d="M5 12h14"/>',
  check: '<path d="m5 12.5 4.5 4.5L19 7.5"/>',
  x: '<path d="M18 6 6 18M6 6l12 12"/>',
  back: '<path d="m15 18-6-6 6-6"/>',
  pin: '<path d="M12 21s-7-6.2-7-11.5a7 7 0 0 1 14 0C19 14.8 12 21 12 21z"/><circle cx="12" cy="9.5" r="2.5"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  truck: '<path d="M3 6h11v9H3zM14 9h4l3 3v3h-7"/><circle cx="7" cy="17.5" r="1.8"/><circle cx="17" cy="17.5" r="1.8"/>',
  bag: '<path d="M6 8h12l-1 12H7L6 8z"/><path d="M9 8V6a3 3 0 0 1 6 0v2"/>',
  trash: '<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/>',
  logout: '<path d="M15 4h4v16h-4M10 8l-4 4 4 4M6 12h10"/>',
};

export function icon(name, cls = '') {
  return raw(`<svg class="${cls}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name] || ''}</svg>`);
}

/** Знак бренда: маршрут от ресторана до двери. */
export function brandMark(cls = 'brand__mark') {
  return raw(`<svg class="${cls}" viewBox="0 0 32 32" aria-hidden="true">
    <rect width="32" height="32" rx="9" fill="#1B6B45"/>
    <path d="M9 22c0-6 14-4 14-11" fill="none" stroke="#F0B323" stroke-width="3" stroke-linecap="round" stroke-dasharray="0.1 5.2"/>
    <circle cx="9" cy="22" r="3.2" fill="#fff"/><circle cx="23" cy="10" r="3.2" fill="#F0B323"/></svg>`);
}

// ------------------------------------------------------------------ toasts
export function toast(title, { body = '', kind = 'info', timeout = 5000 } = {}) {
  const box = document.getElementById('toasts');
  if (!box) return;
  const el = document.createElement('div');
  el.className = `toast toast--${kind}`;
  el.innerHTML = renderValue(html`<div class="toast__title">${title}</div>${body ? html`<div class="toast__body">${body}</div>` : ''}<button class="toast__close" type="button" aria-label="Закрыть">×</button>`);
  el.querySelector('.toast__close').addEventListener('click', () => el.remove());
  box.appendChild(el);
  while (box.children.length > 4) box.firstElementChild.remove();
  if (timeout) setTimeout(() => el.remove(), timeout);
}

export const toastError = (e) => toast(e?.message || 'Что-то пошло не так', { kind: 'error', timeout: 7000 });

// ------------------------------------------------------------------ dialogs
/**
 * Модальный диалог подтверждения. Если задан input, возвращает введённый текст
 * (или null при отмене), иначе — true/false.
 */
export function confirmDialog({ title, text = '', confirm = 'Подтвердить', cancel = 'Отмена', danger = false, input = null }) {
  const dlg = document.getElementById('dialog');
  return new Promise((resolve) => {
    dlg.innerHTML = renderValue(html`
      <form method="dialog" class="dialog__form">
        <div class="dialog__body">
          <h2 class="dialog__title">${title}</h2>
          ${text ? html`<p class="dialog__text">${text}</p>` : ''}
          ${input ? html`
            <div class="field" style="margin-top:16px">
              <label for="dlg-input">${input.label}</label>
              <textarea id="dlg-input" class="textarea" maxlength="200" placeholder="${input.placeholder || ''}"></textarea>
            </div>
            ${input.suggestions?.length ? html`<div class="dialog__chips">${input.suggestions.map((s) => html`<button type="button" class="chip" data-suggest="${s}">${s}</button>`)}</div>` : ''}` : ''}
        </div>
        <div class="dialog__actions">
          <button type="button" class="btn btn--ghost" data-act="cancel">${cancel}</button>
          <button type="submit" class="btn ${danger ? 'btn--danger' : ''}" data-act="ok">${confirm}</button>
        </div>
      </form>`);
    const area = dlg.querySelector('#dlg-input');
    dlg.querySelectorAll('[data-suggest]').forEach((b) => b.addEventListener('click', () => { area.value = b.dataset.suggest; area.focus(); }));
    const close = (val) => { dlg.close(); resolve(val); };
    dlg.querySelector('[data-act="cancel"]').addEventListener('click', () => close(input ? null : false));
    dlg.querySelector('form').addEventListener('submit', (e) => { e.preventDefault(); close(input ? area.value.trim() : true); });
    dlg.addEventListener('cancel', () => resolve(input ? null : false), { once: true });
    dlg.showModal();
    (area || dlg.querySelector('[data-act="ok"]')).focus();
  });
}

/** Блокирует кнопку на время асинхронного действия. */
export async function busy(btn, fn) {
  if (btn) btn.disabled = true;
  try { return await fn(); } finally { if (btn && btn.isConnected) btn.disabled = false; }
}
