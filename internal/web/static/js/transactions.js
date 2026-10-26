import { h, field } from './dom.js';
import { initToken, listTransactions, getTransaction, editTransaction, deleteTransaction, getCategories } from './api.js';
import { longDate } from './fmt.js';

// Страница «Транзакции» (ADR-018, R18): список всех транзакций, правка любого поля и удаление.
// Ссылка /transactions?focus=<id> открывает страницу на этой транзакции: она в центре экрана,
// подсвечена, форма правки открыта. Проверку и пересчёт делает сервер; здесь только показ и ввод.

initToken();

const app = document.getElementById('app');
const SIGN = { RUB: '₽', THB: '฿' };
const nf = new Intl.NumberFormat('ru-RU', { minimumFractionDigits: 0, maximumFractionDigits: 2 });
const showAmount = (a, cur) => nf.format(Number(a)) + ' ' + (SIGN[cur] || cur);

let categories = [];
let rows = [];
let next = '';
let filters = { text: '', category: '', from: '', to: '' };
let focusId = new URLSearchParams(location.search).get('focus') || '';
let busy = false;

const listBox = h('div', { class: 'card txlist' });
const note = h('div', { class: 'hint' });
const moreBtn = h('button', { type: 'button', class: 'lnk', onclick: () => loadMore() }, 'Показать ещё');
moreBtn.hidden = true;

function filterBar() {
  const text = h('input', { type: 'search', placeholder: 'Поиск по названию', class: 'wide', value: filters.text });
  const cat = h('select', null, h('option', { value: '' }, 'Все категории'), ...categories.map((c) => h('option', { value: c }, c)));
  cat.value = filters.category;
  const from = h('input', { type: 'date', value: filters.from });
  const to = h('input', { type: 'date', value: filters.to });
  let timer = null;
  const apply = () => {
    filters = { text: text.value.trim(), category: cat.value, from: from.value, to: to.value };
    focusId = '';
    history.replaceState(null, '', location.pathname);
    load();
  };
  text.addEventListener('input', () => { clearTimeout(timer); timer = setTimeout(apply, 300); });
  for (const el of [cat, from, to]) el.addEventListener('change', apply);
  return h('div', { class: 'top' }, h('div', { class: 'row' },
    field('Поиск', text, 'wide'), field('Категория', cat), field('С', from), field('По', to)));
}

function rowEl(t) {
  const line = h('div', { class: 'tr txr', 'data-id': t.id, tabindex: '0', role: 'button', title: 'Изменить' },
    h('span', { class: 'td txd' }, longDate(t.date)),
    h('span', { class: 'tn' }, t.description || '—', t.category ? h('span', { class: 'tag mut' }, t.category) : null,
      t.kind === 'income' ? h('span', { class: 'tag ex' }, 'доход') : null,
      t.fromRule ? h('span', { class: 'tag rg' }, '↻') : null),
    h('span', { class: 'ta' }, showAmount(t.amount, t.currency)));
  const open = () => toggleEditor(line, t);
  line.addEventListener('click', open);
  line.addEventListener('keydown', (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); open(); } });
  return line;
}

function drawList() {
  listBox.replaceChildren(...(rows.length ? rows.map(rowEl) : [h('div', { class: 'hint' }, 'Транзакций нет.')]));
  moreBtn.hidden = !next;
}

function replaceRow(line, t) {
  const idx = rows.findIndex((r) => r.id === t.id);
  if (idx >= 0) rows[idx] = t;
  const fresh = rowEl(t);
  fresh.classList.add('hl');
  line.nextElementSibling?.classList.contains('txed') && line.nextElementSibling.remove();
  line.replaceWith(fresh);
}

function toggleEditor(line, t) {
  const after = line.nextElementSibling;
  if (after && after.classList.contains('txed')) { after.remove(); return; }
  document.querySelectorAll('.txed').forEach((el) => el.remove());
  line.after(editor(line, t));
}

function editor(line, t) {
  const name = h('input', { type: 'text', value: t.description, maxlength: '200', class: 'wide' });
  const amount = h('input', { type: 'text', inputmode: 'decimal', value: t.amount, class: 'md' });
  const currency = h('select', null, ...['RUB', 'THB'].map((c) => h('option', { value: c }, SIGN[c] + ' ' + c)));
  currency.value = t.currency;
  const date = h('input', { type: 'date', value: t.date });
  const names = categories.includes(t.category) || !t.category ? categories : [t.category, ...categories];
  const cat = h('select', null, ...names.map((c) => h('option', { value: c }, c)));
  cat.value = t.category;
  if (t.kind === 'income') cat.disabled = true; // категории дохода здесь не меняются
  const msg = h('div', { class: 'hint' });
  const warn = h('div', { class: 'hint' });
  const save = h('button', { type: 'button' }, 'Сохранить');
  const cancel = h('button', { type: 'button', class: 'ghost' }, 'Отмена');
  const del = h('button', { type: 'button', class: 'ghost bad' }, 'Удалить');

  const updateWarn = () => {
    if (currency.value === t.currency) { warn.textContent = ''; return; }
    warn.textContent = 'Валюта меняется вместе с суммой: впишите сумму в новой валюте (' + currency.value + '). Было: '
      + showAmount(t.amount, t.currency) + ', станет: ' + showAmount(amount.value || '0', currency.value) + '.';
  };
  currency.addEventListener('change', updateWarn);
  amount.addEventListener('input', updateWarn);

  const fail = (e) => { msg.className = 'hint err'; msg.textContent = e.message || 'Не удалось сохранить.'; };
  const guard = async (fn) => {
    if (busy) return;
    busy = true;
    save.disabled = del.disabled = true;
    try { await fn(); } catch (e) {
      fail(e);
      if (e.status === 409) { // строку изменили: даём перечитать
        msg.append(' ', h('button', { type: 'button', class: 'lnk', onclick: async () => {
          try { replaceRow(line, await getTransaction(t.id)); } catch (err) { fail(err); }
        } }, 'Перечитать строку'));
      }
    } finally { busy = false; save.disabled = del.disabled = false; }
  };

  cancel.addEventListener('click', () => box.remove());
  save.addEventListener('click', () => guard(async () => {
    const body = {
      version: t.version, description: name.value, amount: amount.value.trim().replace(',', '.'),
      currency: currency.value, date: date.value,
    };
    if (t.kind !== 'income') body.category = cat.value;
    replaceRow(line, await editTransaction(t.id, body));
  }));
  let armed = false;
  del.addEventListener('click', () => {
    if (!armed) {
      armed = true;
      del.textContent = 'Точно удалить?';
      setTimeout(() => { armed = false; del.textContent = 'Удалить'; }, 5000);
      return;
    }
    guard(async () => {
      await deleteTransaction(t.id, t.version);
      rows = rows.filter((r) => r.id !== t.id);
      box.remove();
      line.remove();
      note.textContent = 'Удалено: ' + (t.description || '—') + ', ' + showAmount(t.amount, t.currency)
        + '. Снимок строки сохранён в журнале.';
    });
  });

  const box = h('div', { class: 'txed' },
    h('div', { class: 'row' }, field('Название', name, 'wide'), field('Сумма', amount), field('Валюта', currency), field('Дата', date),
      field('Категория', cat)),
    warn, msg,
    h('div', { class: 'row top2' }, save, cancel, h('span', { class: 'sp' }), del),
    t.linked ? h('div', { class: 'hint' }, 'Эта оплата закрывала плановый платёж: при удалении платёж снова откроется в плане.') : null);
  return box;
}

async function load(keepNote) {
  rows = []; next = '';
  if (!keepNote) note.textContent = '';
  try {
    const page = await listTransactions(focusId ? { focus: focusId } : filters);
    rows = page.rows || []; next = page.next || '';
    drawList();
    if (focusId) focusRow();
  } catch (e) {
    if (focusId && e.status === 404) { // ссылка на удалённую транзакцию: показываем обычный список
      focusId = '';
      note.textContent = 'Такой транзакции нет: возможно, её уже удалили.';
      return load(true);
    }
    listBox.replaceChildren(h('div', { class: 'err' }, e.message || 'Не удалось загрузить транзакции.'));
  }
}

async function loadMore() {
  if (busy || !next) return;
  busy = true;
  try {
    const page = await listTransactions({ ...filters, cursor: next });
    rows = rows.concat(page.rows || []); next = page.next || '';
    drawList();
  } catch (e) { note.textContent = e.message || 'Не удалось загрузить.'; } finally { busy = false; }
}

function focusRow() {
  const line = listBox.querySelector('[data-id="' + CSS.escape(focusId) + '"]');
  const t = rows.find((r) => r.id === focusId);
  if (!line || !t) return;
  line.classList.add('hl');
  toggleEditor(line, t);
  requestAnimationFrame(() => line.scrollIntoView({ block: 'center' })); // после того, как форма заняла место
}

(async () => {
  try { categories = await getCategories(); } catch (e) { categories = []; }
  app.replaceChildren(h('h1', { class: 'ttl' }, 'Транзакции'), filterBar(), note, listBox, moreBtn);
  await load();
})();
