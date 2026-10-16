import { h } from '../dom.js';
import { money } from '../fmt.js';

// «Как обычно тратишь»: траты по категориям в прошлом окне и сейчас.
export function render(view, cur) {
  const cats = view.categories || [];
  if (!cats.length) return h('div');
  const max = Math.max(1, ...cats.map((c) => Math.max(c.last, c.now)));
  const card = h('div', { class: 'card' }, h('div', { class: 'ch' }, h('span', null, 'Категория'), h('span', null, 'Прошлое'), h('span', null, 'Сейчас'), h('span', null, '')));
  cats.forEach((c) => {
    const pct = Math.round((c.now / max) * 100);
    card.append(h('div', { class: 'cr' },
      h('span', null, c.name),
      h('span', null, money(c.last, cur)),
      h('b', null, money(c.now, cur)),
      h('span', null, ''),
      h('div', { class: 'bar2' }, h('i', { 'data-w': String(pct) }))));
  });
  return h('section', null, h('h2', null, 'Как обычно тратишь'), card);
}
