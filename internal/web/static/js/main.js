import { h } from './dom.js';
import { initToken, getWindow, deleteItem, settleItem, reopenItem, skipItem, setRate, setPast, setAvg, moveItem, editItem } from './api.js';
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
// onSkip — пропустить этот срок повтора, onReopen — вернуть закрытую запись в план. Ошибку команды показываем текстом сервера.
const ctx = {
  show: (view) => draw(view),
  redraw: () => { if (lastView) draw(lastView); },
  onDelete: async (key) => {
    try { draw(await deleteItem(cur, key)); } catch (e) { alert(e.message || 'Не удалось убрать.'); }
  },
  // Можно ли ставить платёж «в окно»: окно должно быть (текущее, будущее или между).
  canWindow: () => !!lastView && ['current', 'upcoming', 'between'].includes(lastView.state),
  onMove: (key, body) => once(() => moveItem(cur, key, body), 'Не удалось перенести.'),
  onAvg: (days) => { setAvg(days); try { sessionStorage.setItem('plan_avg', String(days)); } catch (e) { /* не страшно */ } load(); },
  // reload — перечитать окно с сервера (например, после «Отменить», когда лимита уже нет).
  reload: () => load(),
  // toast — короткое сообщение с необязательным действием; «Отменить» действует, пока тост на экране.
  toast: showToast,
  onShowPast: () => { setPast(true); load(); },
  onHidePast: () => { setPast(false); load(); },
  onSettle: (key, body) => once(() => settleItem(cur, key, body), 'Не удалось отметить.'),
  onSkip: (key) => once(() => skipItem(cur, key), 'Не удалось пропустить.'),
  onReopen: (key) => once(() => reopenItem(cur, key), 'Не удалось вернуть в план.'),
  // onEdit — «изменить»: та же очередь, что у прочих команд; рисует ответ и отдаёт
  // true, false — занято другой командой. Ошибку показывает сам редактор.
  onEdit: async (key, body) => {
    const view = await exclusive(() => editItem(cur, key, body));
    if (view) draw(view);
    return !!view;
  },
};

let toastEl = null;
let toastTimer = null;
function showToast(text, actionLabel, onAction) {
  if (!toastEl) {
    toastEl = h('div', { class: 'toast', role: 'status', 'aria-live': 'polite' });
    document.body.append(toastEl);
  }
  clearTimeout(toastTimer);
  const hide = () => { toastEl.classList.remove('on'); toastEl.replaceChildren(); };
  toastEl.replaceChildren(h('span', null, text),
    actionLabel ? h('button', { type: 'button', class: 'tact', onclick: () => { hide(); onAction(); } }, actionLabel) : null);
  toastEl.classList.add('on');
  toastTimer = setTimeout(hide, 10000);
}

// Одна команда за раз: второй тап, пока первая в пути, не должен дать «не найдено».
let busy = false;
async function exclusive(run) {
  if (busy) return null;
  busy = true;
  try { return await run(); } finally { busy = false; }
}
async function once(run, fallback) {
  try { const view = await exclusive(run); if (view) draw(view); } catch (e) { alert(e.message || fallback); }
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
