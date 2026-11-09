import { h } from './dom.js';

// Ссылка на страницу «Транзакции» (ADR-018): /transactions?focus=<id> открывает её на этой транзакции — строка в
// центре экрана, подсвечена, форма правки открыта. Единое место для всех экранов: строка плана, траты категории и
// любые другие списки. Подходят только оплаченные факты (ключ "transaction:<id>"); плановые записи и повторы
// правятся своими командами, и ссылки у них нет.
const PREFIX = 'transaction:';

export function txHref(key) {
  const k = String(key || '');
  return k.startsWith(PREFIX) ? '/transactions?focus=' + encodeURIComponent(k.slice(PREFIX.length)) : null;
}

export function txLink(key, label) {
  const href = txHref(key);
  if (!href) return null;
  return h('a', { class: 'x', href, title: 'Открыть в транзакциях и изменить' }, label || 'изменить');
}
