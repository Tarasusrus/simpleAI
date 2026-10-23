import { h } from '../dom.js';
import { dismissCategory, restoreCategory } from '../api.js';
import { money, plural } from '../fmt.js';
import { txPanel } from './cat-tx.js';
import { planForm } from './plan-form.js';

// Блок «Не запланировано · N» (ADR-015 §3): категории без лимита, в которых уже есть траты окна.
// Нейтральный вид: это очередь на разнесение, а не предупреждение. Числа считает сервер.

const TOP = 2; // при трёх и более категориях видны две самые крупные
let expanded = false; // «ещё N» раскрыто
let showHidden = false; // раскрыт список «Скрытые · N»
const open = new Set(); // раскрытые категории: переживают перерисовку после команды

function chevron(isOpen) {
  return h('span', { class: 'chev' + (isOpen ? ' on' : ''), 'aria-hidden': 'true' });
}

function rowOf(r, cur, ctx) {
  const key = r.category.trim().toLowerCase();
  const isOpen = open.has(key);
  const head = h('button', {
    type: 'button', class: 'urow', 'aria-expanded': isOpen ? 'true' : 'false',
    'aria-label': (isOpen ? 'Свернуть' : 'Раскрыть') + ' траты категории «' + r.category + '»',
  },
  h('span', { class: 'un' }, chevron(isOpen), r.category, h('span', { class: 'tag mut' }, 'без плана')),
  h('span', { class: 'ua' }, money(r.spent, cur)));
  const wrap = h('div', { class: 'unp' }, head);
  head.addEventListener('click', () => {
    if (open.has(key)) open.delete(key); else open.add(key);
    ctx.redraw();
  });
  if (isOpen) {
    wrap.append(h('div', { class: 'ubody' },
      planForm(r, cur, ctx, () => { open.delete(key); ctx.redraw(); }, () => open.delete(key)),
      txPanel(r.category, cur),
      h('div', { class: 'ufoot' },
        h('button', {
          type: 'button', class: 'lnk',
          onclick: () => once(ctx, () => dismissCategory(cur, r.category), 'Не удалось скрыть.'),
        }, 'Не планировать'),
        h('span', { class: 'mut' }, ' — скрыть из блока'))));
  }
  return wrap;
}

async function once(ctx, run, fallback) {
  try { ctx.show(await run()); } catch (e) { alert((e && e.message) || fallback); }
}

function hiddenRow(r, cur, ctx) {
  return h('div', { class: 'hrow' },
    h('span', { class: 'un' }, r.category),
    h('span', { class: 'ua' }, money(r.spent, cur)),
    h('button', {
      type: 'button', class: 'ghost hbtn', 'aria-label': 'Вернуть «' + r.category + '» в блок',
      onclick: () => once(ctx, () => restoreCategory(cur, r.category), 'Не удалось вернуть.'),
    }, 'Вернуть'));
}

export function render(view, cur, ctx) {
  if (view.state === 'none' || !view.window) return h('div');
  const rows = view.unplanned || [];
  const hidden = view.hidden || [];
  if (!rows.length && !hidden.length) return h('div');
  const nodes = [];
  if (rows.length) {
    const shown = expanded ? rows : rows.slice(0, TOP);
    const rest = expanded ? 0 : rows.length - shown.length;
    const card = h('div', { class: 'card' }, shown.map((r) => rowOf(r, cur, ctx)));
    if (rest > 0) {
      const sum = rows.slice(TOP).reduce((s, r) => s + r.spent, 0);
      card.append(h('div', { class: 'it fold' },
        h('span', null, 'ещё ' + rest + ' ' + plural(rest, 'категория', 'категории', 'категорий')),
        h('span', null, h('button', { type: 'button', class: 'lnk', onclick: () => { expanded = true; ctx.redraw(); } }, 'показать все'), ' · ' + money(sum, cur))));
    } else if (expanded && rows.length > TOP) {
      card.append(h('div', { class: 'it fold' },
        h('span', null, h('button', { type: 'button', class: 'lnk', onclick: () => { expanded = false; ctx.redraw(); } }, 'свернуть'))));
    }
    nodes.push(h('h2', null, 'Не запланировано · ' + rows.length, h('span', { class: 'rt' }, 'уже вычтено из «Свободно»')), card);
  }
  if (hidden.length) {
    nodes.push(h('div', { class: 'hlink' },
      h('button', {
        type: 'button', class: 'lnk', 'aria-expanded': showHidden ? 'true' : 'false',
        onclick: () => { showHidden = !showHidden; ctx.redraw(); },
      }, 'Скрытые · ' + hidden.length)));
    if (showHidden) nodes.push(h('div', { class: 'card' }, hidden.map((r) => hiddenRow(r, cur, ctx))));
  }
  return h('section', null, nodes);
}
