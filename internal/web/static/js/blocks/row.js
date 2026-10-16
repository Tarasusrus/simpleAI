import { h } from '../dom.js';
import { money, shortDate, plural } from '../fmt.js';

// Подпись к строке по флагам, которые выставил сервер.
function note(r) {
  if (r.overdueDays > 0) return 'просрочен ' + r.overdueDays + ' ' + plural(r.overdueDays, 'день', 'дня', 'дней');
  if (r.notArrived) return 'ещё не пришло';
  if (r.windowPassed) return 'окно прошло, не оплачено';
  if (r.windowMissing) return 'окна ещё нет';
  return '';
}

// Одна строка ленты / списка. Название только текстом.
export function row(r, cur, ctx) {
  const tags = [];
  if (r.expected) tags.push(h('span', { class: 'tag ex' }, 'ожидается'));
  if (r.kind === 'income' && !r.expected) tags.push(h('span', { class: 'tag ex' }, 'получено'));
  const amount = r.amount == null
    ? h('span', { class: 'a' }, h('small', null, 'без суммы'))
    : h('span', { class: 'a' }, (r.kind === 'income' ? '+' : '') + money(r.amount, cur));
  const n = note(r);
  return h('div', { class: 'it' + (r.noAmount ? ' dim' : '') },
    h('span', { class: 'd' }, shortDate(r.date)),
    h('span', null, r.name, tags),
    amount,
    h('span', { class: 'r' + (r.overdueDays > 0 ? ' bad' : '') }, n, removable(r, ctx)));
}

// Плановую запись (её ключ planned:...) можно убрать; факты и повторы — нет.
function removable(r, ctx) {
  if (!ctx || !ctx.onDelete || !String(r.key).startsWith('planned:')) return null;
  return h('a', {
    class: 'x', href: '#', title: 'Убрать из плана',
    onclick: (e) => { e.preventDefault(); if (confirm('Убрать «' + r.name + '» из плана?')) ctx.onDelete(r.key); },
  }, 'убрать');
}
