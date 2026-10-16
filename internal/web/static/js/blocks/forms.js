import { h } from '../dom.js';
import { addIncome, addPayment, getCategories } from '../api.js';

// Кнопки «＋ Поступление» / «＋ Платёж» и форма под ними. Всё, что владелец
// вводит, уходит на сервер как есть; проверку и расчёт делает сервер.

let open = null; // 'income' | 'payment' | null — переживает перерисовку после сохранения

function field(label, input, cls) {
  return h('div', { class: 'fi ' + (cls || '') }, h('label', null, label), input);
}

function curSelect(cur) {
  return h('select', { name: 'currency' },
    h('option', { value: 'RUB', selected: cur === 'RUB' }, '₽ рубли'),
    h('option', { value: 'THB', selected: cur === 'THB' }, '฿ баты'));
}

function incomeForm(view, cur, ctx, status) {
  const name = h('input', { name: 'name', value: 'Зарплата', required: true });
  const amount = h('input', { name: 'amount', type: 'number', step: '0.01', min: '0', inputmode: 'decimal', placeholder: '0', required: true, class: 'md' });
  const currency = curSelect(cur);
  const date = h('input', { name: 'date', type: 'date', required: true, class: 'md' });
  const form = h('form', { class: 'top' },
    h('div', { class: 'row' }, h('b', null, 'Ожидаемое поступление')),
    h('div', { class: 'row' },
      field('Название', name, 'wide'), field('Сумма', amount), field('Валюта', currency), field('Когда придёт', date)),
    h('div', { class: 'hint' }, 'Оно откроет окно на 14 дней с этой даты. Когда придёт — внесёшь факт как обычную транзакцию.'),
    status,
    h('div', { class: 'row' }, h('button', { type: 'submit' }, 'Добавить'), h('button', { type: 'button', class: 'ghost', onclick: () => { open = null; ctx.redraw(); } }, 'Закрыть')));
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    await submit(status, () => addIncome(cur, {
      name: name.value, amount: amount.value, currency: currency.value, date: date.value,
    }), ctx);
  });
  return form;
}

function paymentForm(view, cur, ctx, status, categories) {
  const hasWindow = view.state === 'current' || view.state === 'upcoming' || view.state === 'between';
  const name = h('input', { name: 'name', required: true, placeholder: 'что оплатить' });
  const amount = h('input', { name: 'amount', type: 'number', step: '0.01', min: '0', inputmode: 'decimal', placeholder: 'можно позже', class: 'md' });
  const currency = curSelect(cur);
  const category = h('select', { name: 'category' }, h('option', { value: '' }, 'без категории'),
    categories.map((c) => h('option', { value: c }, c)));
  const when = h('select', { name: 'when' },
    hasWindow ? h('option', { value: 'current' }, 'в это окно') : null,
    hasWindow ? h('option', { value: 'next' }, 'в следующее окно') : null,
    h('option', { value: 'date' }, 'на дату'),
    h('option', { value: 'none' }, 'пока без даты'));
  const date = h('input', { name: 'date', type: 'date', class: 'md hide' });
  when.addEventListener('change', () => date.classList.toggle('hide', when.value !== 'date'));
  const form = h('form', { class: 'top' },
    h('div', { class: 'row' }, h('b', null, 'Плановый платёж')),
    h('div', { class: 'row' },
      field('Название', name, 'wide'), field('Сумма', amount), field('Валюта', currency)),
    h('div', { class: 'row' },
      field('Категория', category), field('Когда', when), field('Дата', date)),
    h('div', { class: 'hint' }, hasWindow
      ? 'Платёж «в это окно» войдёт в расчёт «можно тратить». Без суммы — как напоминание, в расчёт не входит.'
      : 'Окна пока нет: чтобы ставить платёж в окно, сначала добавь ожидаемое поступление с датой.'),
    status,
    h('div', { class: 'row' }, h('button', { type: 'submit' }, 'Добавить'), h('button', { type: 'button', class: 'ghost', onclick: () => { open = null; ctx.redraw(); } }, 'Закрыть')));
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const body = { name: name.value, currency: currency.value, category: category.value };
    if (amount.value !== '') body.amount = amount.value;
    if (when.value === 'date') body.date = date.value;
    else if (when.value === 'current' || when.value === 'next') body.window = when.value;
    await submit(status, () => addPayment(cur, body), ctx);
  });
  return form;
}

async function submit(status, send, ctx) {
  status.textContent = '';
  status.classList.remove('err');
  try {
    const view = await send();
    open = null;
    ctx.show(view);
  } catch (e) {
    status.textContent = e.message || 'Не удалось сохранить.';
    status.classList.add('err');
  }
}

export function render(view, cur, ctx) {
  const box = h('div', { class: 'forms' });
  box.append(h('div', { class: 'bar' },
    h('button', { type: 'button', class: 'add' + (open === 'income' ? ' on' : ''), onclick: () => { open = open === 'income' ? null : 'income'; ctx.redraw(); } }, '＋ Поступление'),
    h('button', { type: 'button', class: 'add' + (open === 'payment' ? ' on' : ''), onclick: () => { open = open === 'payment' ? null : 'payment'; ctx.redraw(); } }, '＋ Платёж')));
  const status = h('div', { class: 'hint' });
  if (open === 'income') {
    box.append(incomeForm(view, cur, ctx, status));
  } else if (open === 'payment') {
    const slot = h('div');
    box.append(slot);
    getCategories().catch(() => []).then((cats) => slot.replaceChildren(paymentForm(view, cur, ctx, status, cats)));
  }
  return box;
}
