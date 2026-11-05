// Состояние клиента: сессия пользователя и корзина. Хранится в localStorage
// (только как удобство конкретного браузера; при недоступности — в памяти).

function load(key, fallback) {
  try {
    const v = localStorage.getItem(key);
    return v ? JSON.parse(v) : fallback;
  } catch {
    return fallback;
  }
}

function save(key, value) {
  try {
    if (value === null || value === undefined) localStorage.removeItem(key);
    else localStorage.setItem(key, JSON.stringify(value));
  } catch { /* приватный режим или запрет хранения — работаем в памяти */ }
}

// ------------------------------------------------------------------ session
export const session = {
  token: load('ff.token', null),
  user: load('ff.user', null),

  set(token, user) {
    this.token = token;
    this.user = user;
    save('ff.token', token);
    save('ff.user', user);
  },
  setUser(user) {
    this.user = user;
    save('ff.user', user);
  },
  clear() {
    this.token = null;
    this.user = null;
    save('ff.token', null);
    save('ff.user', null);
  },
  get role() { return this.user?.role || null; },
};

// ------------------------------------------------------------------ cart
const listeners = new Set();

export const cart = {
  data: load('ff.cart', { restaurantId: null, restaurantName: '', lines: [] }),
  address: load('ff.address', ''),

  onChange(fn) { listeners.add(fn); return () => listeners.delete(fn); },
  emit() { save('ff.cart', this.data); listeners.forEach((fn) => fn(this)); },

  qty(itemId) { return this.data.lines.find((l) => l.id === itemId)?.qty || 0; },
  count() { return this.data.lines.reduce((s, l) => s + l.qty, 0); },
  subtotal() { return this.data.lines.reduce((s, l) => s + l.price * l.qty, 0); },
  isFrom(restaurantId) { return !this.data.restaurantId || this.data.restaurantId === restaurantId; },

  add(restaurant, item) {
    if (this.data.restaurantId !== restaurant.id) {
      this.data = { restaurantId: restaurant.id, restaurantName: restaurant.name, lines: [] };
    }
    const line = this.data.lines.find((l) => l.id === item.id);
    if (line) line.qty = Math.min(50, line.qty + 1);
    else this.data.lines.push({ id: item.id, name: item.name, price: item.price, qty: 1 });
    this.emit();
  },
  dec(itemId) {
    const line = this.data.lines.find((l) => l.id === itemId);
    if (!line) return;
    line.qty -= 1;
    if (line.qty <= 0) this.data.lines = this.data.lines.filter((l) => l.id !== itemId);
    if (!this.data.lines.length) this.data.restaurantId = null;
    this.emit();
  },
  inc(itemId) {
    const line = this.data.lines.find((l) => l.id === itemId);
    if (line) { line.qty = Math.min(50, line.qty + 1); this.emit(); }
  },
  clear() {
    this.data = { restaurantId: null, restaurantName: '', lines: [] };
    this.emit();
  },
  setAddress(a) { this.address = a; save('ff.address', a); },
};

// Бизнес-правила доставки (совпадают с доменной моделью Order Service).
export const RULES = {
  minOrder: 300_00,
  deliveryFee: 99_00,
  freeFrom: 1_500_00,
};
