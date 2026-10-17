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
    h('span', null, 'Категория'), h('span', { title: 'Среднее за 14 дней по последним ' + avgDays + ' дням' }, 'Обычно'),
    h('span', null, last), h('span', null, 'Сейчас')));
  const max = Math.max(1, ...cats.map((c) => Math.max(c.avg, c.last, c.now)));
  cats.forEach((c) => {
    const pct = Math.round((c.now / max) * 100);
    card.append(h('div', { class: 'cr' },
      h('span', null, c.name),
      h('span', { title: c.planned ? 'Обычно ' + money(c.avg, cur) + ', в плане окна ' + money(c.planned, cur) : '' },
        money(c.planned ? c.free : c.avg, cur),
        c.planned ? h('small', { class: 'muted' }, ' (в плане −' + money(c.planned, cur) + ')') : null),
      h('span', null, money(c.last, cur)),
      h('b', null, money(c.now, cur)),
      h('div', { class: 'bar2' }, h('i', { 'data-w': String(pct) }))));
  });
  return h('section', null,
    h('h2', null, 'Как обычно тратишь'),
    h('div', { class: 'hint' }, 'Обычно — среднее за 14 дней по последним ' + avgDays + ' дням; если в окне уже есть плановые платежи этой категории, они вычтены. ', picker),
    card);
}
