import { h, togglePanel } from '../dom.js';
import { money, plural } from '../fmt.js';
import { editForm } from './limit-form.js';
import { txPanel } from './cat-tx.js';

// Блок «Лимиты · N». Тонкие строки только для тех, где расход уже заметен (флаг attention
// считает сервер: перерасход, остаток меньше 30%, траты за последние дни); остальные
// свёрнуты в «ещё N лимитов» с раскрытием. Здесь только показ чисел сервера.

let expanded = false; // переживает перерисовку после команды

function barOf(l) {
  // Заполнение = израсходованная доля лимита; при перерасходе красный хвост пропорционален.
  let inner;
  if (l.over > 0) {
    const ok = Math.round((l.amount * 100) / (l.amount + l.over));
    inner = [h('i', { class: 'ok', 'data-w': String(ok) }), h('i', { class: 'bad', 'data-w': String(100 - ok) })];
  } else {
    const used = Math.min(100, Math.max(0, 100 - l.leftPct));
    inner = [h('i', { class: l.leftPct < 30 ? 'warn' : 'ok', 'data-w': String(used) })];
  }
  const label = l.over > 0
    ? 'Перерасход ' + l.over + ' сверх лимита ' + l.amount
    : 'Осталось ' + l.left + ' из ' + l.amount;
  return h('div', { class: 'lbar', role: 'img', 'aria-label': label }, inner);
}

function rowOf(l, cur, ctx) {
  const over = l.over > 0;
  const chev = h('button', { type: 'button', class: 'chevb', 'aria-label': 'Траты категории «' + l.category + '» в этом окне', title: 'Траты в этом окне' }, h('span', { class: 'chev', 'aria-hidden': 'true' }));
  const name = h('span', { class: 'ln' }, chev, l.category,
    over ? h('span', { class: 'tag bad' }, 'перерасход') : null,
    l.repeat ? h('span', { class: 'tag mut', title: 'Повторяется каждое окно' }, '↻') : null);
  const right = h('span', { class: 'lr' + (over ? ' bad' : l.leftPct < 30 ? ' warn' : '') }, over ? money(-l.over, cur) : money(l.left, cur));
  const row = h('div', { class: 'it lim' + (over ? ' over' : ''), role: 'button', tabindex: '0', title: 'Изменить лимит', 'aria-label': 'Изменить лимит «' + l.category + '»' }, name, barOf(l), right);
  const open = () => togglePanel(row, 'limit', () => h('div', { class: 'it ed' }));
  const toggle = () => {
    const box = open();
    if (box) box.replaceChildren(editForm(l, cur, ctx, box));
  };
  chev.addEventListener('click', (e) => {
    e.stopPropagation();
    const box = togglePanel(row, 'tx', () => h('div', { class: 'it ed' }));
    chev.classList.toggle('on', !!box);
    if (box) box.replaceChildren(txPanel(l.category, cur));
  });
  row.addEventListener('click', toggle);
  row.addEventListener('keydown', (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggle(); } });
  return row;
}

export function render(view, cur, ctx) {
  const limits = view.limits || [];
  if (view.state === 'none' || !view.window) return h('div');
  if (!limits.length) {
    return h('div', { class: 'hint' }, 'Лимитов нет. «＋ Лимит» отложит деньги на категорию: пока лимит не кончился, траты в ней не уменьшают «Свободно».');
  }
  // Внимание — выше; порядок внутри групп остаётся серверным.
  const ordered = [...limits].sort((a, b) => Number(b.attention) - Number(a.attention));
  const shown = expanded ? ordered : ordered.filter((l) => l.attention);
  const rest = expanded ? [] : ordered.filter((l) => !l.attention);
  const card = h('div', { class: 'card' }, shown.map((l) => rowOf(l, cur, ctx)));
  if (rest.length) {
    const left = rest.reduce((s, l) => s + l.left, 0);
    card.append(h('div', { class: 'it fold' },
      h('span', null, 'ещё ' + rest.length + ' ' + plural(rest.length, 'лимит', 'лимита', 'лимитов')),
      h('span', null, h('a', { class: 'x', href: '#', onclick: (e) => { e.preventDefault(); expanded = true; ctx.redraw(); } }, 'показать все'), ' · осталось ' + money(left, cur))));
  } else if (expanded && limits.length > 1) {
    card.append(h('div', { class: 'it fold' },
      h('span', null, h('a', { class: 'x', href: '#', onclick: (e) => { e.preventDefault(); expanded = false; ctx.redraw(); } }, 'свернуть'))));
  }
  return h('section', null,
    h('h2', null, 'Лимиты · ' + limits.length, h('span', { class: 'rt' }, 'осталось ' + money(view.kpi.limitHold, cur))),
    card);
}
