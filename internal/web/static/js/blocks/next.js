import { h } from '../dom.js';
import { money, monthName, plural } from '../fmt.js';
import { row } from './row.js';

function fold(title, count, sum, items, cur) {
  const body = h('div', { class: 'hide' }, items.map((r) => row(r, cur)));
  const head = h('div', { class: 'it fold' },
    h('span', null, title, ' · ', h('b', null, count + ' ' + plural(count, 'платёж', 'платежа', 'платежей'))),
    h('span', null, sum ? money(sum, cur) : ''));
  head.addEventListener('click', () => body.classList.toggle('hide'));
  return [head, body];
}

// Следующее окно и «позже» по месяцам — сворачиваемые строки.
export function render(view, cur) {
  const card = h('div', { class: 'card' });
  if (view.next && view.next.count > 0) card.append(...fold('Следующее окно', view.next.count, view.next.sum, view.next.items, cur));
  (view.later || []).forEach((m) => {
    if (m.count > 0) card.append(...fold(monthName(m.month) + ' ' + m.year, m.count, m.sum, m.items, cur));
  });
  if (!card.childNodes.length) return h('div');
  return h('section', null, h('h2', null, 'Дальше'), card);
}
