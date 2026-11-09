import { h, field } from '../dom.js';
import { addIncome, addPayment, getCategories } from '../api.js';
import { createForm } from './limit-form.js';
import { incomeMode, opensWindowFor, canSubmit, caption } from './income-mode.js';

// Кнопки «＋ Поступление» / «＋ Платёж» и форма под ними. Всё, что владелец
// вводит, уходит на сервер как есть; проверку и расчёт делает сервер.

let open = null; // 'income' | 'payment' | 'limit' | null — переживает перерисовку после сохранения

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
  let choice; // true — зарплата/аванс (открывает окно), false — обычный приход; undefined — не выбрано
  const salary = h('button', { type: 'button', 'data-opens': 'true' }, 'Зарплата или аванс (новое окно)');
  const regular = h('button', { type: 'button', 'data-opens': 'false' }, 'Обычный приход (в текущее окно)');
  const seg = h('div', { class: 'seg inc hide', role: 'group', 'aria-label': 'Вид поступления' }, salary, regular);
  const note = h('div', { class: 'hint' });
  const save = h('button', { type: 'submit' }, 'Добавить');
  const refresh = () => {
    const mode = incomeMode(view, date.value);
    seg.classList.toggle('hide', mode !== 'ask');
    salary.classList.toggle('on', choice === true);
    regular.classList.toggle('on', choice === false);
    note.textContent = caption(mode, choice, date.value, view);
    save.disabled = !canSubmit(mode, choice);
  };
  salary.addEventListener('click', () => { choice = true; refresh(); });
  regular.addEventListener('click', () => { choice = false; refresh(); });
  date.addEventListener('input', refresh);
  const form = h('form', { class: 'top' },
    h('div', { class: 'row' }, h('b', null, 'Ожидаемое поступление')),
    h('div', { class: 'row' },
      field('Название', name, 'wide'), field('Сумма', amount), field('Валюта', currency), field('Когда придёт', date)),
    seg,
    note,
    status,
    h('div', { class: 'row' }, save, h('button', { type: 'button', class: 'ghost', onclick: () => { open = null; ctx.redraw(); } }, 'Закрыть')));
  refresh();
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const mode = incomeMode(view, date.value);
    if (!canSubmit(mode, choice)) return;
    await submit(status, () => addIncome(cur, {
      name: name.value, amount: amount.value, currency: currency.value, date: date.value,
      opensWindow: opensWindowFor(mode, choice),
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
    h('button', { type: 'button', class: 'add' + (open === 'payment' ? ' on' : ''), onclick: () => { open = open === 'payment' ? null : 'payment'; ctx.redraw(); } }, '＋ Платёж'),
    h('button', { type: 'button', class: 'add' + (open === 'limit' ? ' on' : ''), onclick: () => { open = open === 'limit' ? null : 'limit'; ctx.redraw(); } }, '＋ Лимит')));
  const status = h('div', { class: 'hint' });
  if (open === 'income') {
    box.append(incomeForm(view, cur, ctx, status));
  } else if (open === 'payment') {
    const slot = h('div');
    box.append(slot);
    getCategories().catch(() => []).then((cats) => slot.replaceChildren(paymentForm(view, cur, ctx, status, cats)));
  } else if (open === 'limit') {
    const slot = h('div');
    box.append(slot);
    getCategories().catch(() => []).then((cats) => slot.replaceChildren(createForm(view, cur, ctx, cats, () => { open = null; ctx.redraw(); })));
  }
  return box;
}
