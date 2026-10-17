import { h } from '../dom.js';
import { money, shortDate } from '../fmt.js';

const PERIODS = [30, 90, 180];

// «Как обычно тратишь»: по категориям обычные траты за окно (среднее за выбранный
// период), траты последнего окна (с датами) и текущего окна.
export function render(view, cur, ctx) {
  const cats = view.categories || [];
  if (!cats.length) return h('div');
  const avgDays = view.avgDays || 90;
  const last = view.lastWindow ? 'Окно ' + shortDate(view.lastWindow.start) + '–' + shortDate(view.lastWindow.end) : 'Прошлое окно';
  const picker = h('span', { class: 'seg' }, PERIODS.map((d) => h('button', {
    type: 'button', class: d === avgDays ? 'on' : '',
    title: 'Обычные траты — среднее за 14 дней по последним ' + d + ' дням',
    onclick: () => { if (ctx && ctx.onAvg) ctx.onAvg(d); },
  }, d + ' дн.')));
  const card = h('div', { class: 'card' }, h('div', { class: 'ch' },
    h('span', null, 'Категория'), h('span', { title: 'Траты за последние ' + avgDays + ' дн., пересчитанные на 14 дней: сумма ÷ ' + avgDays + ' × 14' }, 'Обычно'),
    h('span', null, last), h('span', null, 'Сейчас')));
  const max = Math.max(1, ...cats.map((c) => Math.max(c.avg, c.last, c.now)));
  cats.forEach((c) => {
    const pct = Math.round((c.now / max) * 100);
    card.append(h('div', { class: 'cr' },
      h('span', null, c.name),
      h('span', null, money(c.avg, cur)),
      h('span', null, money(c.last, cur)),
      h('b', null, money(c.now, cur)),
      c.planned ? h('div', { class: 'note' }, 'в плане ' + money(c.planned, cur) + ' · свободно ' + money(c.free, cur)) : null,
      h('div', { class: 'bar2' }, h('i', { 'data-w': String(pct) }))));
  });
  return h('section', null,
    h('h2', null, 'Как обычно тратишь'),
    h('div', { class: 'hint' }, 'Обычно — траты за последние ' + avgDays + ' дн., пересчитанные на 14 дней, без обязательных платежей (аренда, кредит, подписки). «Свободно» = обычно минус плановые платежи категории в этом окне. ', picker),
    card);
}
