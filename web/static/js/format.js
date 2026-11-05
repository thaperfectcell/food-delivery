// Форматирование денег, времени и статусов для интерфейса.

const nf = new Intl.NumberFormat('ru-RU', { maximumFractionDigits: 2 });

/** Копейки → «1 297 ₽». */
export function money(kopecks) {
  const v = Number(kopecks || 0) / 100;
  return `${nf.format(v)} ₽`;
}

const timeFmt = new Intl.DateTimeFormat('ru-RU', { hour: '2-digit', minute: '2-digit' });
const dayFmt = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long' });

export function time(iso) {
  if (!iso) return '';
  return timeFmt.format(new Date(iso));
}

/** «сегодня, 14:05» / «вчера, 09:12» / «16 октября, 18:40». */
export function dateTime(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  const today = new Date();
  const y = new Date(); y.setDate(today.getDate() - 1);
  let day = dayFmt.format(d);
  if (d.toDateString() === today.toDateString()) day = 'сегодня';
  else if (d.toDateString() === y.toDateString()) day = 'вчера';
  return `${day}, ${timeFmt.format(d)}`;
}

/** Сколько прошло: «только что», «7 мин», «2 ч 5 мин». */
export function ago(iso, { suffix = true } = {}) {
  if (!iso) return '';
  const min = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 60000));
  if (min < 1) return 'только что';
  let s;
  if (min < 60) s = `${min} мин`;
  else if (min < 60 * 24) s = `${Math.floor(min / 60)} ч${min % 60 ? ` ${min % 60} мин` : ''}`;
  else return dateTime(iso);
  return suffix ? `${s} назад` : s;
}

export function minutesSince(iso) {
  return iso ? Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 60000)) : 0;
}

/** Склонение: plural(5, 'позиция', 'позиции', 'позиций'). */
export function plural(n, one, few, many) {
  const a = Math.abs(n) % 100, b = a % 10;
  if (a > 10 && a < 20) return many;
  if (b > 1 && b < 5) return few;
  if (b === 1) return one;
  return many;
}

export const ORDER_STATUS = {
  created: 'ожидает оплаты',
  paid: 'оплачен',
  accepted: 'готовится',
  ready: 'готов к выдаче',
  in_delivery: 'в пути',
  delivered: 'доставлен',
  cancelled: 'отменён',
};

export const PAYMENT_STATUS = { pending: 'проводится', succeeded: 'оплачено', failed: 'не прошла', refunded: 'возвращено' };
export const DELIVERY_STATUS = { pending: 'ищем курьера', assigned: 'курьер назначен', picked_up: 'курьер в пути', delivered: 'доставлено', cancelled: 'отменена' };
export const COURIER_STATUS = { offline: 'не на смене', available: 'свободен', busy: 'на доставке' };
export const ROLE = { customer: 'клиент', restaurant: 'ресторан', courier: 'курьер', admin: 'администратор' };

const cap = (s) => (s ? s[0].toUpperCase() + s.slice(1) : s);
export const statusLabel = (s) => cap(ORDER_STATUS[s] || s);

/** Короткий номер заказа. */
export const orderNo = (o) => o?.number || String(o?.id || '').slice(0, 8).toUpperCase();

/** Обложки ресторанов по кухне: цвет и эмодзи. */
const COVERS = {
  'итальянская': ['#C2412B', '🍕'],
  'японская': ['#284B5C', '🍣'],
  'американская': ['#D99A1C', '🍔'],
  'грузинская': ['#7A2E3B', '🥟'],
  'китайская': ['#9C2B23', '🥡'],
  'русская': ['#3D5A80', '🥞'],
  'индийская': ['#B5651D', '🍛'],
  'мексиканская': ['#2F7A55', '🌮'],
};

export function cover(cuisine) {
  const [color, emoji] = COVERS[String(cuisine || '').toLowerCase()] || ['#1B6B45', '🍽️'];
  return { color, emoji };
}
