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

async function call(method, path, payload) {
  // Без токена заголовок не шлём: за nginx с логином браузер сам подставит Basic-доступ,
  // а токен nginx добавит на своей стороне.
  const headers = {};
  if (token()) headers.Authorization = 'Bearer ' + token();
  const init = { method, headers };
  if (payload !== undefined) {
    headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(payload);
  }
  const res = await fetch(path, init);
  let body = null;
  try { body = await res.json(); } catch (e) { /* не JSON */ }
  if (!res.ok) throw new ApiError(res.status, (body && body.error) || 'Ошибка ' + res.status);
  return body;
}

// past — показать закончившееся последнее окно вместо «нового окна ещё нет».
let past = false;
export const setPast = (v) => { past = v; };
const q = (cur) => '?cur=' + encodeURIComponent(cur) + (past ? '&past=1' : '');

export const getWindow = (cur) => call('GET', '/api/plan/window' + q(cur));
export const getCategories = () => call('GET', '/api/plan/categories').then((b) => b.categories || []);
export const addIncome = (cur, body) => call('POST', '/api/plan/income' + q(cur), body);
export const addPayment = (cur, body) => call('POST', '/api/plan/payment' + q(cur), body);
export const deleteItem = (cur, key) => call('DELETE', '/api/plan/item/' + encodeURIComponent(key) + q(cur));
export const setRate = (cur, rate) => call('POST', '/api/plan/rate' + q(cur), { rate });
export const settleItem = (cur, key, body) => call('POST', '/api/plan/item/' + encodeURIComponent(key) + '/done' + q(cur), body);
export const skipItem = (cur, key) => call('POST', '/api/plan/item/' + encodeURIComponent(key) + '/skip' + q(cur));
