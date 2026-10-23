import { h } from '../dom.js';
import { addLimit, deleteLimit, previewLimit, suggestLimit } from '../api.js';
import { money } from '../fmt.js';

// Форма «Запланировать» под строкой «Не запланировано» (ADR-015 §4–5). Тексты по числам сервера:
// avgCount (трат за окно по истории) и count (трат категории в этом окне). Сумма по умолчанию:
// подсказка по истории, если она больше уже потраченного, иначе prefill сервера (2 × потрачено) —
// лимит, не больший потраченного, ничего не откладывает, и «Свободно» не изменилось бы.

const SIGN = { RUB: '₽', THB: '฿' };
const STEP = { RUB: 500, THB: 100 };

function debounce(fn, ms) {
  let t = null;
  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}

// Заголовок и пояснение формы; чистая функция от чисел сервера — проверяется в браузере и глазами.
export function prompts(sug, row, cur) {
  if (sug && sug.enough && sug.avgCount > 0) {
    const title = row.count >= sug.avgCount
      ? 'Это уже ' + row.count + '-я трата, обычно ' + sug.avgCount + ' за окно'
      : 'Это трата ' + row.count + ' из примерно ' + sug.avgCount + ' за окно';
    return { title, sub: 'По истории на категорию уходит около ' + money(sug.amount, cur) + ' за окно. Распределить деньги на остальные сейчас?' };
  }
  return { title: 'Распределить деньги на эту категорию?', sub: 'Истории пока мало, чтобы предложить сумму: поставь свою, ' + money(row.prefill, cur) + ' — это ещё столько же, сколько уже потрачено.' };
}

export function defaultAmount(sug, row) {
  return sug && sug.enough && sug.amount > row.spent ? sug.amount : row.prefill;
}

export function planForm(row, cur, ctx, onClose) {
  const head = h('div', { class: 'pf-t' }, 'Распределить деньги на эту категорию?');
  const sub = h('div', { class: 'pf-s' }, 'Загружаю подсказку…');
  const amount = h('input', {
    name: 'amount', type: 'number', step: '0.01', min: '0', inputmode: 'decimal', class: 'pf-in', required: true,
    'aria-label': 'Сумма на окно, ' + SIGN[cur], value: String(row.prefill),
  });
  const step = STEP[cur] || 100;
  const bump = (d) => { amount.value = String(Math.max(0, Number(amount.value || 0) + d)); refresh(); };
  const minus = h('button', { type: 'button', class: 'pf-b', 'aria-label': 'Уменьшить сумму', onclick: () => bump(-step) }, '−');
  const plus = h('button', { type: 'button', class: 'pf-b', 'aria-label': 'Увеличить сумму', onclick: () => bump(step) }, '+');
  const rep = h('input', { type: 'checkbox', class: 'pf-c' });
  const prev = h('div', { class: 'pf-p' });
  const status = h('div', { class: 'hint' });
  const save = h('button', { type: 'submit', class: 'pf-go' }, 'Запланировать');

  let seq = 0;
  const run = debounce(async () => {
    const my = ++seq;
    const v = Number(amount.value);
    if (!(v > 0)) { prev.replaceChildren(); return; }
    try {
      const pv = await previewLimit(cur, { category: row.category, amount: amount.value, currency: cur, repeat: rep.checked });
      if (my !== seq) return;
      prev.classList.remove('err');
      prev.replaceChildren(h('span', { class: 'mut' }, 'Свободно после плана'),
        h('b', null, money(pv.canSpendBefore, cur) + ' → ' + money(pv.canSpendAfter, cur)));
      if (pv.exceedsFree) prev.append(h('div', { class: 'bad pf-w' }, 'Сумма больше свободных денег: «Свободно» уйдёт в минус.'));
    } catch (e) {
      if (my !== seq) return;
      prev.classList.add('err');
      prev.replaceChildren(e.message || 'Не удалось посчитать.');
    }
  }, 250);
  const refresh = () => run();
  amount.addEventListener('input', refresh);
  rep.addEventListener('change', refresh);

  suggestLimit(cur, row.category).catch(() => null).then((sug) => {
    const p = prompts(sug, row, cur);
    head.textContent = p.title;
    sub.textContent = p.sub;
    amount.value = String(defaultAmount(sug, row));
    refresh();
  });

  const form = h('form', { class: 'pf' },
    head, sub,
    h('div', { class: 'pf-r' },
      h('div', null, h('div', { class: 'pf-k' }, 'Отложить на окно')),
      h('div', { class: 'pf-a' }, minus, amount, plus)),
    prev,
    h('label', { class: 'pf-rep' }, rep, 'Повторять каждое окно'),
    status,
    h('div', { class: 'pf-bt' }, save, h('button', { type: 'button', class: 'ghost pf-ng', onclick: onClose }, 'Не сейчас')));

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    if (!(Number(amount.value) > 0)) { status.textContent = 'Введи сумму больше нуля.'; status.classList.add('err'); return; }
    status.textContent = '';
    status.classList.remove('err');
    save.disabled = true;
    const sum = Number(amount.value);
    const repeat = rep.checked;
    try {
      const view = await addLimit(cur, { category: row.category, amount: amount.value, currency: cur, repeat });
      ctx.show(view);
      // «Отменить» для повторяющегося лимита не показываем: иначе молча уйдёт план на будущие окна.
      if (!repeat && view.limitId) {
        const id = view.limitId;
        ctx.toast('Запланировано ' + money(sum, cur) + ' на ' + row.category, 'Отменить', async () => {
          try { ctx.show(await deleteLimit(cur, id)); } catch (err) {
            if (err && err.status === 404) ctx.reload(); // лимита уже нет — цель достигнута
            else alert((err && err.message) || 'Не удалось отменить.');
          }
        });
      } else {
        ctx.toast('Запланировано ' + money(sum, cur) + ' на ' + row.category + ' · каждое окно');
      }
    } catch (err) {
      status.textContent = err.message || 'Не удалось сохранить.';
      status.classList.add('err');
      save.disabled = false;
    }
  });
  return form;
}
