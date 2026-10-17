import { h } from './dom.js';
import { initToken, getWindow, deleteItem, settleItem, skipItem } from './api.js';
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

function draw(view) {
  lastView = view;
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
