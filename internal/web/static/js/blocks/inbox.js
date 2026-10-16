import { h } from '../dom.js';
import { money, plural } from '../fmt.js';
import { row } from './row.js';

// Платежи без даты и окна: в расчёт не входят.
export function render(view, cur) {
  const items = view.inbox || [];
  if (!items.length) return h('div');
  const k = view.kpi;
  const card = h('div', { class: 'card' }, items.map((r) => row(r, cur)));
  return h('section', null,
    h('h2', null, 'Без даты', ' · не входят в расчёт'),
    card,
    h('div', { class: 'hint' }, k.undatedCount + ' ' + plural(k.undatedCount, 'пункт', 'пункта', 'пунктов') + ', с суммой ' + money(k.undatedSum, cur)));
}
