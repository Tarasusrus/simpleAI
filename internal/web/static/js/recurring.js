import { h } from './dom.js';
import { initToken, getRecurring } from './api.js';
import { money, longDate, plural, every } from './fmt.js';

// Страница «Регулярные платежи»: все правила повтора, включая приостановленные. Только чтение.
// Суммы «в месяц» и ближайшие сроки приходят готовыми, здесь только показ.

initToken();

const app = document.getElementById('app');
const curBox = document.getElementById('cur');
const SIGN = { RUB: '₽', THB: '฿' };
const nf = new Intl.NumberFormat('ru-RU', { minimumFractionDigits: 0, maximumFractionDigits: 2 });
const showAmount = (a, cur) => nf.format(Number(a)) + ' ' + (SIGN[cur] || cur);

let cur = 'RUB';
try { cur = sessionStorage.getItem('plan_cur') || 'RUB'; } catch (e) { /* без хранилища — рубли */ }

function drawCurrency() {
  curBox.replaceChildren(...['RUB', 'THB'].map((c) => h('button', {
    type: 'button',
    class: c === cur ? 'on' : '',
    onclick: () => {
      cur = c;
      try { sessionStorage.setItem('plan_cur', c); } catch (e) { /* не страшно */ }
      load();
    },
  }, SIGN[c])));
}

function rowEl(r) {
  const state = r.paused ? h('span', { class: 'tag mut' }, 'на паузе')
    : r.invalid ? h('span', { class: 'tag bad' }, 'не читается')
      : null;
  const when = r.invalid ? 'Правило повреждено' : [
    every(r.every, r.unit),
    r.paused ? null : r.next ? 'ближайший ' + longDate(r.next) : 'серия закончилась',
    r.until && !r.paused ? 'до ' + longDate(r.until) : null,
  ].filter(Boolean).join(' · ');
  return h('div', { class: 'tr' + (r.paused || r.invalid ? ' mut' : '') },
    h('span', { class: 'tn' }, r.name || '—',
      r.category ? h('span', { class: 'tag mut' }, r.category) : null,
      r.kind === 'income' ? h('span', { class: 'tag ex' }, 'приход') : null,
      state,
      h('span', { class: 'hint' }, when)),
    h('span', { class: 'ta' }, showAmount(r.amount, r.currency),
      r.monthly != null ? h('span', { class: 'hint' }, '≈ ' + money(r.monthly, cur) + ' в месяц') : null));
}

function draw(data) {
  const kids = [];
  if (data.rows.length) {
    kids.push(h('div', { class: 'card rsum' },
      h('div', { class: 'hint' }, 'Расходы в месяц'),
      h('div', { class: 'rtot' }, money(data.total, cur)),
      data.missing ? h('div', { class: 'hint' }, 'Не вошло: ' + data.missing + ' ' + plural(data.missing, 'платёж', 'платежа', 'платежей') + ' — нет курса. Задай курс на главной странице.') : null));
    kids.push(h('div', { class: 'card txlist' }, ...data.rows.map(rowEl)));
  } else {
    kids.push(h('div', { class: 'card' }, h('div', { class: 'hint' }, 'Регулярных платежей пока нет.')));
  }
  app.replaceChildren(...kids);
}

async function load() {
  drawCurrency();
  try {
    draw(await getRecurring(cur));
  } catch (e) {
    app.replaceChildren(h('div', { class: 'hint err' }, (e && e.message) || 'Не удалось загрузить.'));
  }
}

load();
