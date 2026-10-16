// Токен приходит во фрагменте ссылки (#t=...): фрагмент не уходит на сервер и в
// логи. Кладём в sessionStorage и стираем из адресной строки.
const KEY = 'plan_token';

export function initToken() {
  const m = /[#&]t=([^&]+)/.exec(location.hash);
  if (m) {
    try { sessionStorage.setItem(KEY, decodeURIComponent(m[1])); } catch (e) { /* без хранилища токен живёт до перезагрузки */ }
    history.replaceState(null, '', location.pathname + location.search);
    initToken.mem = decodeURIComponent(m[1]);
  }
}

function token() {
  try { return sessionStorage.getItem(KEY) || initToken.mem || ''; } catch (e) { return initToken.mem || ''; }
}

export class ApiError extends Error {
  constructor(status, message) { super(message); this.status = status; }
}

export async function getWindow(cur) {
  const res = await fetch('/api/plan/window?cur=' + encodeURIComponent(cur), {
    headers: { Authorization: 'Bearer ' + token() },
  });
  let body = null;
  try { body = await res.json(); } catch (e) { /* не JSON */ }
  if (!res.ok) throw new ApiError(res.status, (body && body.error) || 'Ошибка ' + res.status);
  return body;
}
