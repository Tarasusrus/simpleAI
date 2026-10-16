import { h } from '../dom.js';
import { row } from './row.js';

const SHOWN = 8; // сколько строк видно сразу; остальное за кнопкой

export function render(view, cur) {
  const list = view.timeline || [];
  const card = h('div', { class: 'card' });
  if (!list.length) {
    card.append(h('div', { class: 'empty' }, 'В этом окне платежей нет.'));
  } else {
    list.slice(0, SHOWN).forEach((r) => card.append(row(r, cur)));
    if (list.length > SHOWN) {
      const rest = list.slice(SHOWN);
      const btn = h('button', { class: 'more', type: 'button' }, 'Показать ещё ' + rest.length);
      btn.addEventListener('click', () => {
        rest.forEach((r) => card.insertBefore(row(r, cur), btn));
        btn.remove();
      });
      card.append(btn);
    }
  }
  return h('section', null, h('h2', null, 'Ближайшие платежи'), card);
}
