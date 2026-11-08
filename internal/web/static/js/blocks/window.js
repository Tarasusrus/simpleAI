import { h } from '../dom.js';
import { longDate, plural } from '../fmt.js';

// Окно можно править (ADR-020), пока оно на экране: текущее, будущее или следующее в зазоре.
export function canEditWindow(view) {
  return !!view && !!view.window && ['current', 'upcoming', 'between'].includes(view.state);
}

// Подпись длины: стандартное окно молчит, поправленное говорит, сколько в нём дней. Идущее окно (закончилось, а
// зарплаты нет) про длину не говорит: она растёт каждый день (ADR-022).
export function lengthNote(w) {
  if (!w || w.ongoing || !w.adjusted || !w.days) return '';
  return 'окно ' + w.days + ' ' + plural(w.days, 'день', 'дня', 'дней') + ' (изменено)';
}

// Дата из поля годится к отправке: полная (пока год набирается, браузер шлёт change с «годом» 0002, 0020…) и
// отличается от текущей.
export function dateReady(value, current) {
  return /^[12]\d{3}-\d{2}-\d{2}$/.test(value || '') && value !== current;
}

// Кнопки правки окна: на день короче / длиннее, начало и конец датой, удалить окно.
function editControls(w, ctx) {
  const start = h('input', { type: 'date', class: 'sm', value: w.start, max: w.end, 'aria-label': 'первый день окна' });
  start.addEventListener('change', () => { if (dateReady(start.value, w.start)) ctx.onWindowStart(start.value); });
  const end = h('input', { type: 'date', class: 'sm', value: w.end, min: w.start, 'aria-label': 'последний день окна' });
  end.addEventListener('change', () => { if (dateReady(end.value, w.end)) ctx.onWindowEnd(end.value); });
  const btn = (label, title, run) => h('button', { type: 'button', class: 'x', title, onclick: run }, label);
  return h('div', { class: 'win-edit' },
    // У идущего окна конец — сегодня: сократить его некуда.
    w.ongoing ? null : btn('−1 день', 'Сократить окно на день', () => ctx.onWindowResize(-1)),
    btn('+1 день', 'Продлить окно на день', () => ctx.onWindowResize(1)),
    h('label', null, 'с ', start),
    h('label', null, 'до ', end),
    ctx.onWindowDelete ? btn('удалить окно', 'Удалить это окно', () => ctx.onWindowDelete()) : null);
}

// Строка «окно 05.10 — 18.10 · осталось N дней».
export function render(view, cur, ctx) {
  if (view.state === 'ended' && !view.window) {
    // Прошлое окно по умолчанию не показываем: владелец планирует будущее.
    return h('div', { class: 'win' },
      'Окно закончилось ' + longDate(view.meta.lastEnd) + ', нового ещё нет. Добавь ожидаемое поступление — откроется новое окно.',
      ctx && ctx.onShowPast ? h('a', { class: 'x', href: '#', onclick: (e) => { e.preventDefault(); ctx.onShowPast(); } }, 'показать прошлое окно') : null);
  }
  if (view.state === 'none' || !view.window) {
    return h('div', { class: 'win' }, 'Окна пока нет: добавь поступление, и план посчитается.');
  }
  const w = view.window;
  const left = w.daysLeft + ' ' + plural(w.daysLeft, 'день', 'дня', 'дней');
  const state = {
    current: w.ongoing ? 'идёт до следующего поступления' : 'осталось ' + left,
    upcoming: 'окно ещё не началось',
    between: 'окно закончилось, следующее ещё не открыто',
    ended: 'окно закончилось',
  }[view.state] || '';
  const parts = ['Окно ' + longDate(w.start) + ' — ' + longDate(w.end), state, lengthNote(w)];
  if (w.anchorPlanned) parts.push('открыто ожидаемым поступлением');
  const back = view.state === 'ended' && ctx && ctx.onHidePast
    ? h('a', { class: 'x', href: '#', onclick: (e) => { e.preventDefault(); ctx.onHidePast(); } }, 'к будущему окну')
    : null;
  const edit = canEditWindow(view) && ctx && ctx.onWindowResize ? editControls(w, ctx) : null;
  return h('div', { class: 'win' }, parts.filter(Boolean).join(' · '), back, edit);
}
