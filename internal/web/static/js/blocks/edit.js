import { h, togglePanel } from '../dom.js';
import { getItem, getCategories } from '../api.js';
import { longDate, plural } from '../fmt.js';

// «Изменить» у плановой записи, повтора и закрытой записи: редактор раскрывается под
// строкой, как «перенести». Показывать ли ссылку, решает сервер (флаг editable строки).
// Значения формы берутся с сервера в исходной валюте записи; проверку и расчёт делает сервер.

const UNITS = {
  day: ['день', 'дня', 'дней'],
  week: ['неделю', 'недели', 'недель'],
  month: ['месяц', 'месяца', 'месяцев'],
  year: ['год', 'года', 'лет'],
};

function repeatText(rep) {
  if (!rep) return '';
  const u = UNITS[rep.unit] || [rep.unit, rep.unit, rep.unit];
  const every = rep.n === 1 ? 'каждый ' + u[0] : 'каждые ' + rep.n + ' ' + plural(rep.n, u[0], u[1], u[2]);
  return '↻ ' + every + (rep.until ? ', до ' + longDate(rep.until) : '');
}

function placeText(item) {
  if (item.date) return 'как сейчас: ' + longDate(item.date);
  if (item.windowStart) return 'как сейчас: в окне с ' + longDate(item.windowStart);
  return 'как сейчас: без даты';
}

function field(label, input, cls) {
  return h('div', { class: 'fi ' + (cls || '') }, h('label', null, label), input);
}

export function editable(r, cur, ctx) {
  const key = String(r.key);
  if (!ctx || !ctx.onEdit || !r.editable) return null;
  return h('a', {
    class: 'x', href: '#', title: key.startsWith('recurring:') ? 'Изменить повторяющийся платёж целиком' : 'Изменить запись',
    onclick: (e) => { e.preventDefault(); toggle(e.target.closest('.it'), r, cur, ctx); },
  }, 'изменить');
}

async function toggle(line, row, cur, ctx) {
  const box = togglePanel(line, 'edit', () => h('div', { class: 'it ed' }, h('div', { class: 'hint' }, 'Загружаю…')));
  if (!box) return;
  try {
    const item = await getItem(row.key);
    if (item.fact) {
      box.replaceChildren(factEditor(item, row, ctx, box));
      return;
    }
    const cats = item.kind === 'payment' ? await getCategories().catch(() => []) : [];
    box.replaceChildren(editor(item, cats, cur, ctx, box));
  } catch (e) {
    box.replaceChildren(h('div', { class: 'hint err' }, e.message || 'Не удалось открыть запись.'));
  }
}

function editor(item, cats, cur, ctx, box) {
  const payment = item.kind === 'payment';
  const name = h('input', { name: 'name', required: true, value: item.name, placeholder: 'название' });
  const amount = h('input', {
    name: 'amount', type: 'number', step: '0.01', min: '0', inputmode: 'decimal', class: 'md',
    value: item.amount, placeholder: payment && !item.recurring ? 'можно пусто' : '0',
  });
  const currency = h('select', { name: 'currency' },
    ['RUB', 'THB'].map((c) => h('option', { value: c, selected: (item.currency || cur) === c }, c === 'RUB' ? '₽ рубли' : '฿ баты')));
  const fields = [field('Название', name, 'wide'), field('Сумма', amount), field('Валюта', currency)];

  let category = null;
  if (payment) {
    // Категория записи, которой нет в списке, остаётся выбранной, а не теряется.
    const names = cats.includes(item.category) || !item.category ? cats : [item.category, ...cats];
    category = h('select', { name: 'category' }, h('option', { value: '' }, 'без категории'),
      names.map((c) => h('option', { value: c, selected: c === item.category }, c)));
    fields.push(field('Категория', category));
  }

  let when = null;
  let date = null;
  const rows = [h('div', { class: 'row' }, fields)];
  if (item.recurring) {
    rows.push(h('div', { class: 'hint' }, repeatText(item.repeat),
      '. Меняется само правило — с ближайшего неоплаченного срока, прошлые оплаты не меняются. ',
      'Дата не правится; чтобы пропустить один срок — «пропустить».'));
  } else {
    const windows = payment && ctx.canWindow && ctx.canWindow();
    when = h('select', { name: 'when' },
      h('option', { value: 'keep' }, placeText(item)),
      windows ? h('option', { value: 'current' }, 'в это окно') : null,
      windows ? h('option', { value: 'next' }, 'в следующее окно') : null,
      h('option', { value: 'date' }, 'на дату'),
      payment ? h('option', { value: 'none' }, 'пока без даты') : null);
    date = h('input', { name: 'date', type: 'date', class: 'md', value: item.date });
    // Прячется поле целиком, с подписью: иначе «Дата» висит без поля ввода.
    const dateField = field('Дата', date, 'hide');
    when.addEventListener('change', () => dateField.classList.toggle('hide', when.value !== 'date'));
    rows.push(h('div', { class: 'row' }, field('Когда', when), dateField));
  }

  const status = h('div', { class: 'hint' });
  const save = h('button', { type: 'submit' }, 'Сохранить');
  const form = h('form', null, ...rows, status,
    h('div', { class: 'row' }, save, h('button', { type: 'button', class: 'ghost', onclick: () => box.remove() }, 'Отмена')));
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const body = {
      name: name.value,
      amount: amount.value === '' ? null : amount.value,
      currency: currency.value,
      category: category ? category.value : '',
    };
    if (when && when.value === 'date') {
      if (!date.value) { status.textContent = 'Укажи дату.'; status.classList.add('err'); return; }
      body.date = date.value;
    } else if (when && when.value !== 'keep') {
      body.window = when.value;
    }
    status.textContent = '';
    status.classList.remove('err');
    save.disabled = true;
    try {
      if (!(await ctx.onEdit(item.key, body))) status.textContent = 'Другая команда ещё выполняется — сохрани ещё раз.';
    } catch (err) {
      status.textContent = err.message || 'Не удалось сохранить.';
      status.classList.add('err');
    } finally {
      save.disabled = false;
    }
  });
  return form;
}

// Закрытая запись: правятся название, сумма и дата оплаты. Валюта и категория остаются,
// поэтому их в форме нет; сумма — в валюте самой записи. «Вернуть в план» показывается,
// только если сервер разрешил его у строки.
function factEditor(item, row, ctx, box) {
  const name = h('input', { name: 'name', required: true, value: item.name, placeholder: 'название' });
  const amount = h('input', {
    name: 'amount', type: 'number', step: '0.01', min: '0', inputmode: 'decimal', class: 'md', required: true, value: item.amount,
  });
  const date = h('input', { name: 'date', type: 'date', class: 'md', required: true, value: item.date });
  const cur = item.currency === 'THB' ? '฿' : '₽';
  const status = h('div', { class: 'hint' });
  const save = h('button', { type: 'submit' }, 'Сохранить');
  const actions = [save, h('button', { type: 'button', class: 'ghost', onclick: () => box.remove() }, 'Отмена')];
  if (row.reopenable && ctx.onReopen) {
    actions.push(h('a', {
      class: 'x', href: '#',
      onclick: (e) => {
        e.preventDefault();
        if (confirm('Вернуть «' + row.name + '» в план? Запись будет удалена из учёта.')) ctx.onReopen(item.key);
      },
    }, 'вернуть в план'));
  }
  const form = h('form', null,
    h('div', { class: 'hint' }, 'Запись уже учтена как оплаченная: правка пересчитает «потрачено» и остаток.'),
    h('div', { class: 'row' }, field('Название', name, 'wide'), field('Сумма, ' + cur, amount), field('Когда оплачено', date)),
    status, h('div', { class: 'row' }, actions));
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    status.textContent = '';
    status.classList.remove('err');
    save.disabled = true;
    try {
      const body = { name: name.value, amount: amount.value, currency: item.currency, category: '', date: date.value };
      if (!(await ctx.onEdit(item.key, body))) status.textContent = 'Другая команда ещё выполняется — сохрани ещё раз.';
    } catch (err) {
      status.textContent = err.message || 'Не удалось сохранить.';
      status.classList.add('err');
    } finally {
      save.disabled = false;
    }
  });
  return form;
}
