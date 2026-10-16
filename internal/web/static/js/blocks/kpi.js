import { h } from '../dom.js';
import { money, plural } from '../fmt.js';

function card(cls, k, v, s) {
  return h('div', { class: 'kpi ' + cls }, h('div', { class: 'k' }, k), h('div', { class: 'v' }, v), s ? h('div', { class: 's' }, s) : null);
}

// Главное число и три вспомогательных. Все суммы уже посчитаны сервером.
export function render(view, cur) {
  const k = view.kpi;
  if (view.state === 'none') return h('div', { class: 'kpis' });
  const short = k.canSpend < 0;
  const main = card('main',
    short ? 'Не хватает до конца окна' : 'Можно тратить до конца окна',
    money(short ? -k.canSpend : k.canSpend, cur),
    (k.perDay != null && view.window)
      ? money(k.perDay, cur) + ' в день · ' + view.window.daysLeft + ' ' + plural(view.window.daysLeft, 'день', 'дня', 'дней')
      : (short ? 'платежи и траты больше поступлений' : ''));
  const income = card('', 'Поступления', money(k.received + k.expectedIncome, cur),
    k.expectedIncome > 0 ? 'из них ожидается ' + money(k.expectedIncome, cur) : 'получено');
  const out = card('', 'Платежи и траты', money(k.payments + k.spent, cur),
    'платежи ' + money(k.payments, cur) + ' · потрачено ' + money(k.spent, cur)
      + (k.reserve ? ' · резерв ' + money(k.reserve, cur) : ''));
  return h('div', { class: 'kpis' }, main, income, out);
}
