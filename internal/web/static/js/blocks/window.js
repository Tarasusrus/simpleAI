import { h } from '../dom.js';
import { longDate, plural } from '../fmt.js';

// Строка «окно 05.10 — 18.10 · осталось N дней».
export function render(view) {
  if (view.state === 'none' || !view.window) {
    return h('div', { class: 'win' }, 'Окна пока нет: добавь поступление, и план посчитается.');
  }
  const w = view.window;
  const left = w.daysLeft + ' ' + plural(w.daysLeft, 'день', 'дня', 'дней');
  const state = {
    current: 'осталось ' + left,
    upcoming: 'окно ещё не началось',
    between: 'окно закончилось, следующее ещё не открыто',
    ended: 'окно закончилось',
  }[view.state] || '';
  const parts = ['Окно ' + longDate(w.start) + ' — ' + longDate(w.end), state];
  if (w.anchorPlanned) parts.push('открыто ожидаемым поступлением');
  return h('div', { class: 'win' }, parts.filter(Boolean).join(' · '));
}
