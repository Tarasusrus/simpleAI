import { h } from '../dom.js';
import { money, plural, shortDate } from '../fmt.js';

function card(cls, k, v, s) {
  return h('div', { class: 'kpi ' + cls }, h('div', { class: 'k' }, k), h('div', { class: 'v' }, v), s ? h('div', { class: 's' }, s) : null);
}

// Главное число и три вспомогательных. Все суммы уже посчитаны сервером.
export function render(view, cur) {
  const k = view.kpi;
  if (view.state === 'none' || !view.window) return h('div', { class: 'kpis' });
  const short = k.canSpend < 0;
  // Окно, которое уже закончилось, «до конца» не тратят: это итог окна.
  const closed = view.state === 'ended' || view.state === 'between';
  const upcoming = view.state === 'upcoming';
  // С лимитами в герое только свободное вне лимитов (ADR-011): деньги в лимитах уже вычтены.
  const limited = (view.limits || []).length > 0;
  const title = closed
    ? (short ? 'Итог окна: перерасход' : 'Итог окна: остаток')
    : upcoming
      ? (short ? 'В будущем окне не хватит' : 'Можно будет потратить в окне')
      : (short ? 'Не хватает до конца окна' : (limited ? 'Свободно до конца окна' : 'Можно тратить до конца окна'));
  const main = card('main',
    title,
    money(short ? -k.canSpend : k.canSpend, cur),
    closed ? 'добавь поступление — откроется новое окно'
    : (k.perDay != null && view.window)
      ? money(k.perDay, cur) + ' в день · ' + view.window.daysLeft + ' ' + plural(view.window.daysLeft, 'день', 'дня', 'дней')
      : (short ? (k.limitsExceedFree ? 'лимиты откладывают больше, чем было свободно' : 'платежи и траты больше поступлений') : ''));
  if (limited && !closed && !short) main.append(h('div', { class: 's note' }, 'свободные деньги вне лимитов: можно тратить на что угодно'));
  const income = card('', 'Поступления', money(k.received + k.expectedIncome, cur),
    k.expectedIncome > 0 ? 'из них ожидается ' + money(k.expectedIncome, cur) : 'получено');
  const out = card('', 'Платежи и траты', money(k.payments + k.spent, cur),
    'платежи ' + money(k.payments, cur) + ' · потрачено ' + money(k.spent, cur)
      + (k.reserve ? ' · резерв ' + money(k.reserve, cur) : '')
      + (k.spentBefore > 0 ? ' · до начала окна (с ' + shortDate(k.spentBeforeFrom) + ') потрачено ' + money(k.spentBefore, cur) + ', в окно не входит' : ''));
  return h('div', { class: 'kpis' }, main, income, out);
}
