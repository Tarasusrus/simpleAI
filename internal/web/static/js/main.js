import { h } from './dom.js';
import { initToken, getWindow } from './api.js';
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

function draw(view) {
  const nodes = blocks.map((b) => b.render(view, view.meta.cur));
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
