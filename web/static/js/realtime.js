// Уведомления в реальном времени: Server-Sent Events от API Gateway.
// EventSource сам переподключается при обрыве; при смене пользователя поток
// пересоздаётся.

let source = null;
const handlers = new Set();

export function onNotification(fn) {
  handlers.add(fn);
  return () => handlers.delete(fn);
}

export function connect(token) {
  disconnect();
  if (!token || typeof EventSource === 'undefined') return;
  source = new EventSource(`/api/v1/notifications/stream?access_token=${encodeURIComponent(token)}`);
  source.addEventListener('notification', (e) => {
    let n;
    try { n = JSON.parse(e.data); } catch { return; }
    handlers.forEach((fn) => fn(n));
  });
}

export function disconnect() {
  if (source) source.close();
  source = null;
}
