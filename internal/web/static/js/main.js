import { h } from './dom.js';
import { initToken, getWindow, deleteItem, settleItem, skipItem, setRate, setPast, setAvg } from './api.js';
import { blocks } from './blocks/index.js';

initToken();

const app = document.getElementById('app');
const curBox = document.getElementById('cur');
let cur = 'RUB';
try { cur = sessionStorage.getItem('plan_cur') || 'RUB'; } catch (e) { /* без хранилища — рубли */ }

function drawCurrency() {
  curBox.replaceChildren(
    ...['RUB', 'THB'].map((c) => h('button', {
      type: 'button',
      class: c === cur ? 'on' : '',
      onclick: () => {
        cur = c;
        try { sessionStorage.setItem('plan_cur', c); } catch (e) { /* не страшно */ }
        load();
      },
    }, c === 'RUB' ? '₽' : '฿')));
}

try { setAvg(Number(sessionStorage.getItem('plan_avg')) || 0); } catch (e) { /* без хранилища — 90 дней */ }

let lastView = null;

// ctx для блоков: show — показать свежий вид (ответ на команду), redraw — перерисовать
// тот же, onDelete — убрать плановую запись, onSettle — «оплачено» / «пришло»,
// onSkip — пропустить этот срок повтора. Ошибку команды показываем текстом сервера.
const ctx = {
  show: (view) => draw(view),
  redraw: () => { if (lastView) draw(lastView); },
  onDelete: async (key) => {
    try { draw(await deleteItem(cur, key)); } catch (e) { alert(e.message || 'Не удалось убрать.'); }
  },
  onAvg: (days) => { setAvg(days); try { sessionStorage.setItem('plan_avg', String(days)); } catch (e) { /* не страшно */ } load(); },
  onShowPast: () => { setPast(true); load(); },
  onHidePast: () => { setPast(false); load(); },
  onSettle: (key, body) => once(() => settleItem(cur, key, body), 'Не удалось отметить.'),
  onSkip: (key) => once(() => skipItem(cur, key), 'Не удалось пропустить.'),
};

// Одна команда за раз: второй тап, пока первая в пути, не должен дать «не найдено».
let busy = false;
async function once(run, fallback) {
  if (busy) return;
  busy = true;
  try { draw(await run()); } catch (e) { alert(e.message || fallback); } finally { busy = false; }
}

// Поле «Курс ฿→₽»: показывает действующий курс, по уходу из поля сохраняет новый.
// Пустое поле или то же число — ничего не отправляем.
function drawRate(view) {
  const rateBox = document.getElementById('rate');
  const input = h('input', {
    type: 'number', step: '0.01', min: '0', inputmode: 'decimal', class: 'sm',
    placeholder: view.meta.hasRate ? '' : 'нет курса',
    title: view.meta.ratesManual ? 'Курс задан вручную' : 'Курс обновляется автоматически, пока не задан вручную',
  });
  input.value = view.meta.rate > 0 ? String(view.meta.rate) : '';
  input.addEventListener('change', () => {
    const v = input.value.trim().replace(',', '.');
    if (v === '' || Number(v) === view.meta.rate) return;
    once(() => setRate(cur, v), 'Не удалось сохранить курс.');
  });
  rateBox.replaceChildren('Курс ฿→₽ ', input);
}

function draw(view) {
  lastView = view;
  drawRate(view);
  const nodes = blocks.map((b) => b.render(view, view.meta.cur, ctx));
  if (view.meta.skipped > 0) {
    nodes.push(h('div', { class: 'hint' }, 'Не учтено записей: ' + view.meta.skipped + ' (валюта не бат и не рубль или поступление без даты).'));
  }
  app.replaceChildren(...nodes);
  // Ширина полос задаётся атрибутом, а не style="": CSP запрещает inline-стили.
  app.querySelectorAll('i[data-w]').forEach((el) => { el.style.width = el.dataset.w + '%'; });
}

async function load() {
  drawCurrency();
  try {
    draw(await getWindow(cur));
  } catch (e) {
    app.replaceChildren(h('div', { class: 'err' }, e.message || 'Не удалось загрузить план.'));
  }
}

load();
