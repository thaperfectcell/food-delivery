// Клиент REST API шлюза (/api/v1). Добавляет токен, разбирает ошибки
// формата {"error":{"code","message"}} и превращает их в исключения ApiError.

import { session } from './state.js';

const BASE = '/api/v1';

export class ApiError extends Error {
  constructor(message, code, status) {
    super(message);
    this.code = code;
    this.status = status;
  }
}

let onUnauthorized = () => {};
export function setUnauthorizedHandler(fn) { onUnauthorized = fn; }

function capitalize(s) { return s ? s[0].toUpperCase() + s.slice(1) : s; }

export async function request(path, { method = 'GET', body, headers = {}, auth = true } = {}) {
  const h = { Accept: 'application/json', ...headers };
  if (body !== undefined) h['Content-Type'] = 'application/json';
  const token = session.token;
  if (auth && token) h.Authorization = `Bearer ${token}`;

  let res;
  try {
    res = await fetch(BASE + path, { method, headers: h, body: body !== undefined ? JSON.stringify(body) : undefined });
  } catch {
    throw new ApiError('Нет связи с сервером. Проверьте подключение и повторите.', 'network', 0);
  }
  if (res.status === 204) return null;
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = null; }
  if (!res.ok) {
    let msg = data?.error?.message;
    if (!msg) msg = res.status >= 500 ? 'Сервис временно недоступен. Повторите через минуту.' : `Ошибка ${res.status}`;
    if (res.status === 429) msg = 'Слишком много запросов. Подождите немного.';
    const err = new ApiError(capitalize(msg), data?.error?.code, res.status);
    if (res.status === 401 && auth && token) onUnauthorized(err);
    throw err;
  }
  return data;
}

const qs = (params) => {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params || {})) if (v !== undefined && v !== null && v !== '') p.set(k, v);
  const s = p.toString();
  return s ? `?${s}` : '';
};

export const api = {
  login: (email, password) => request('/auth/login', { method: 'POST', body: { email, password }, auth: false }),
  register: (data) => request('/auth/register', { method: 'POST', body: data, auth: false }),
  me: () => request('/auth/me'),

  restaurants: (params) => request('/restaurants' + qs(params)),
  restaurant: (id) => request(`/restaurants/${id}`),
  menu: (id, params) => request(`/restaurants/${id}/menu` + qs(params)),
  myRestaurants: () => request('/my/restaurants'),
  createRestaurant: (data) => request('/restaurants', { method: 'POST', body: data }),
  updateRestaurant: (id, data) => request(`/restaurants/${id}`, { method: 'PATCH', body: data }),
  addMenuItem: (id, data) => request(`/restaurants/${id}/menu`, { method: 'POST', body: data }),
  updateMenuItem: (id, itemId, data) => request(`/restaurants/${id}/menu/${itemId}`, { method: 'PATCH', body: data }),
  deleteMenuItem: (id, itemId) => request(`/restaurants/${id}/menu/${itemId}`, { method: 'DELETE' }),

  createOrder: (data, key) => request('/orders', { method: 'POST', body: data, headers: { 'Idempotency-Key': key } }),
  orders: (params) => request('/orders' + qs(params)),
  order: (id) => request(`/orders/${id}`),
  cancelOrder: (id, reason) => request(`/orders/${id}/cancel`, { method: 'POST', body: { reason } }),
  acceptOrder: (id) => request(`/orders/${id}/accept`, { method: 'POST' }),
  rejectOrder: (id, reason) => request(`/orders/${id}/reject`, { method: 'POST', body: { reason } }),
  readyOrder: (id) => request(`/orders/${id}/ready`, { method: 'POST' }),

  courier: () => request('/courier/me'),
  setShift: (online) => request('/courier/status', { method: 'POST', body: { online } }),
  deliveries: (params) => request('/courier/deliveries' + qs(params)),
  pickup: (id) => request(`/courier/deliveries/${id}/pickup`, { method: 'POST' }),
  deliver: (id) => request(`/courier/deliveries/${id}/deliver`, { method: 'POST' }),

  notifications: (params) => request('/notifications' + qs(params)),
  markRead: (data) => request('/notifications/read', { method: 'POST', body: data }),

  stats: () => request('/admin/stats'),
  users: (params) => request('/admin/users' + qs(params)),
};
