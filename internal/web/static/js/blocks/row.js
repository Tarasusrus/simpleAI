import { h } from '../dom.js';
import { money, shortDate, plural } from '../fmt.js';
import { editable } from './edit.js';

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
    h('span', { class: 'r' + (r.overdueDays > 0 ? ' bad' : '') }, n, settleable(r, cur, ctx), movable(r, ctx), editable(r, cur, ctx), skippable(r, ctx), removable(r, ctx)));
}

// Плановую запись (её ключ planned:...) можно убрать; факты и повторы — нет.
function removable(r, ctx) {
  if (!ctx || !ctx.onDelete || !String(r.key).startsWith('planned:')) return null;
  return h('a', {
    class: 'x', href: '#', title: 'Убрать из плана',
    onclick: (e) => { e.preventDefault(); if (confirm('Убрать «' + r.name + '» из плана?')) ctx.onDelete(r.key); },
  }, 'убрать');
}

// «Оплачено» / «пришло»: только плановая запись. Если суммы у неё не было, спрашиваем
// её в валюте показа; пустой ответ или не число — отмена, на сервер ничего не уходит.
function settleable(r, cur, ctx) {
  if (!ctx || !ctx.onSettle || !String(r.key).startsWith('planned:')) return null;
  const income = r.kind === 'income';
  return h('a', {
    class: 'x', href: '#', title: income ? 'Отметить: деньги пришли' : 'Отметить: оплачено',
    onclick: (e) => {
      e.preventDefault();
      if (r.amount != null) {
        if (confirm((income ? 'Пришло «' : 'Оплачено «') + r.name + '»?')) ctx.onSettle(r.key);
        return;
      }
      const raw = prompt('Сколько (' + cur + ') по «' + r.name + '»?');
      const amount = Number((raw || '').replace(/\s/g, '').replace(',', '.'));
      if (!raw || !(amount > 0)) return;
      ctx.onSettle(r.key, { amount, currency: cur });
    },
  }, income ? 'пришло' : 'оплачено');
}

// «Пропустить этот раз»: срок повторяющегося правила (ключ recurring:...).
function skippable(r, ctx) {
  if (!ctx || !ctx.onSkip || !String(r.key).startsWith('recurring:')) return null;
  return h('a', {
    class: 'x', href: '#', title: 'Пропустить только этот срок',
    onclick: (e) => { e.preventDefault(); if (confirm('Пропустить «' + r.name + '» в этот раз?')) ctx.onSkip(r.key); },
  }, 'пропустить');
}

// «Перенести»: плановый платёж можно поставить в текущее или следующее окно, на дату
// или вернуть в «без даты». Редактор раскрывается под строкой.
function movable(r, ctx) {
  if (!ctx || !ctx.onMove || r.kind !== 'payment' || !String(r.key).startsWith('planned:')) return null;
  return h('a', {
    class: 'x', href: '#', title: 'Перенести в другое окно или на другую дату',
    onclick: (e) => {
      e.preventDefault();
      const line = e.target.closest('.it');
      const next = line.nextElementSibling;
      if (next && next.classList.contains('ed')) { next.remove(); return; }
      line.after(moveEditor(r, ctx));
    },
  }, 'перенести');
}

function moveEditor(r, ctx) {
  const windows = ctx.canWindow && ctx.canWindow();
  const when = h('select', { name: 'when' },
    windows ? h('option', { value: 'current' }, 'в это окно') : null,
    windows ? h('option', { value: 'next' }, 'в следующее окно') : null,
    h('option', { value: 'date' }, 'на дату'),
    h('option', { value: 'none' }, 'пока без даты'));
  const date = h('input', { type: 'date', class: 'md hide' });
  when.addEventListener('change', () => date.classList.toggle('hide', when.value !== 'date'));
  const save = h('button', { type: 'button' }, 'Перенести');
  save.addEventListener('click', () => {
    const body = {};
    if (when.value === 'date') {
      if (!date.value) return;
      body.date = date.value;
    } else if (when.value !== 'none') {
      body.window = when.value;
    }
    ctx.onMove(r.key, body);
  });
  return h('div', { class: 'it ed' },
    h('div', { class: 'row' }, h('span', null, 'Куда перенести «' + r.name + '»:'), when, date, save));
}
