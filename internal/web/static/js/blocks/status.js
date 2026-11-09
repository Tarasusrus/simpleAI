import { h } from '../dom.js';

// Кружок статуса слева от строки: пустой — не закрыто, заполненный — закрыто. Состояние
// выводится из ключа строки, а право вернуть в план — из флага reopenable, который
// выставляет сервер: фронт его не угадывает. Новый вид строки — новая ветка здесь, row.js
// не меняется.
const kindOf = (key) => String(key).split(':')[0];

export function statusMark(r, cur, ctx) {
  switch (kindOf(r.key)) {
    case 'planned': return openMark(r, cur, ctx);
    case 'transaction': return closedMark(r, ctx);
    default: return pendingMark();
  }
}

function mark({ done = false, label, onclick }) {
  return h('button', {
    type: 'button', class: done ? 'mk done' : 'mk', title: label, 'aria-label': label,
    disabled: !onclick, onclick,
  }, done ? '✓' : null);
}

// «Оплачено» / «пришло»: только плановая запись. Если суммы у неё не было, спрашиваем
// её в валюте показа; пустой ответ или не число — отмена, на сервер ничего не уходит.
function openMark(r, cur, ctx) {
  if (!ctx || !ctx.onSettle) return mark({ label: 'Не закрыто' });
  const income = r.kind === 'income';
  return mark({ label: income ? 'Отметить: деньги пришли' : 'Отметить: оплачено', onclick: (e) => {
    e.preventDefault();
    if (r.amount != null) {
      if (confirm((income ? 'Пришло «' : 'Оплачено «') + r.name + '»?')) ctx.onSettle(r.key);
      return;
    }
    const raw = prompt('Сколько (' + cur + ') по «' + r.name + '»?');
    const amount = Number((raw || '').replace(/\s/g, '').replace(',', '.'));
    if (!raw || !(amount > 0)) return;
    ctx.onSettle(r.key, { amount, currency: cur });
  } });
}

// Закрытая запись: если сервер разрешил вернуть в план, кружок это делает; иначе он
// просто показывает, что запись закрыта.
function closedMark(r, ctx) {
  if (!r.reopenable || !ctx || !ctx.onReopen) return mark({ done: true, label: 'Закрыто' });
  const what = r.kind === 'income' ? 'Запись о поступлении' : 'Запись об оплате';
  return mark({ done: true, label: 'Вернуть в план', onclick: (e) => {
    e.preventDefault();
    if (confirm('Вернуть «' + r.name + '» в план? ' + what + ' будет удалена из учёта.')) ctx.onReopen(r.key);
  } });
}

// Срок повторяющегося платежа: закрывается не вручную, а когда записывается факт.
function pendingMark() {
  return mark({ label: 'Повторяющийся платёж' });
}
