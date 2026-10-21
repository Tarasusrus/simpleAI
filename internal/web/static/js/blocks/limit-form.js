import { h, field } from '../dom.js';
import { addLimit, editLimit, deleteLimit, previewLimit, suggestLimit } from '../api.js';
import { money, plural } from '../fmt.js';

// Форма лимита категории: создание («+ Лимит») и правка (строка в блоке «Лимиты»).
// Сумма-подсказка и эффект на «Свободно» приходят с сервера; здесь только показ.

const SIGN = { RUB: '₽', THB: '฿' };

function debounce(fn, ms) {
  let t = null;
  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}

function repeatBox(checked) {
  const input = h('input', { type: 'checkbox', checked: checked ? true : null });
  input.checked = checked;
  return { input, node: h('label', { class: 'chk' }, input, 'повторять каждое окно') };
}

function amountInput(value) {
  return h('input', { name: 'amount', type: 'number', step: '0.01', min: '0', inputmode: 'decimal', class: 'md', placeholder: 'сумма', value: value == null ? '' : String(value), required: true });
}

// Подсказка среднего по категории. Возвращает { sug, text } — text пуст, пока данных нет.
function suggestText(sug, category, cur) {
  if (!sug.enough) {
    return 'данных мало, чтобы предложить среднее' + (sug.months > 0 ? ' (есть ' + sug.months + ' ' + plural(sug.months, 'месяц', 'месяца', 'месяцев') + ', нужно 2)' : '');
  }
  return 'среднее по категории «' + category + '»: ' + money(sug.amount, cur) + ' за ' + sug.days + ' ' + plural(sug.days, 'день', 'дня', 'дней')
    + ' · данные за ' + sug.months + ' ' + plural(sug.months, 'месяц', 'месяца', 'месяцев');
}

// Строит предпросмотр: перезапрашивается при смене суммы/категории, устаревшие ответы отбрасываются.
function previewer(box, cur, collect, render) {
  let seq = 0;
  const run = debounce(async () => {
    const my = ++seq;
    const body = collect();
    if (!body) { box.replaceChildren(); box.classList.remove('err'); return; }
    try {
      const pv = await previewLimit(cur, body);
      if (my !== seq) return;
      box.classList.remove('err');
      box.replaceChildren(...render(pv));
    } catch (e) {
      if (my !== seq) return;
      box.classList.add('err');
      box.replaceChildren(e.message || 'Не удалось посчитать.');
    }
  }, 250);
  return run;
}

function exceedNote(pv) {
  return pv.exceedsFree ? [h('div', { class: 'bad' }, 'Лимит больше свободных денег: «Свободно» уйдёт в минус.')] : [];
}

// Создание лимита. cats — категории расходов; taken — категории, у которых лимит уже есть.
export function createForm(view, cur, ctx, cats, onDone) {
  const taken = new Set((view.limits || []).map((l) => l.category.trim().toLowerCase()));
  const free = cats.filter((c) => !taken.has(c.trim().toLowerCase()));
  const category = h('select', { name: 'category', required: true },
    h('option', { value: '' }, 'выбери категорию'),
    free.map((c) => h('option', { value: c }, c)));
  const amount = amountInput('');
  const rep = repeatBox(true);
  const hint = h('div', { class: 'hint' });
  const prev = h('div', { class: 'hint' });
  const status = h('div', { class: 'hint' });
  let touched = false;

  const collect = () => {
    const v = Number(amount.value);
    if (!category.value || !(v > 0)) return null;
    return { category: category.value, amount: amount.value, currency: cur, repeat: rep.input.checked };
  };
  const refresh = previewer(prev, cur, collect, (pv) => [
    'Свободно до конца окна: ' + money(pv.canSpendBefore, cur) + ' → ' + money(pv.canSpendAfter, cur) + '. Эти деньги перейдут в лимит.',
    ...exceedNote(pv),
  ]);

  let sugSeq = 0;
  category.addEventListener('change', async () => {
    const my = ++sugSeq;
    touched = false;
    hint.textContent = '';
    hint.classList.remove('err');
    if (!category.value) { refresh(); return; }
    try {
      const sug = await suggestLimit(cur, category.value);
      if (my !== sugSeq) return;
      hint.textContent = suggestText(sug, category.value, cur);
      if (sug.enough && !touched) amount.value = String(sug.amount);
    } catch (e) {
      if (my !== sugSeq) return;
      hint.textContent = e.message || 'Подсказка недоступна.';
    }
    refresh();
  });
  amount.addEventListener('input', () => { touched = true; refresh(); });

  const save = h('button', { type: 'submit' }, 'Создать лимит');
  const form = h('form', { class: 'top' },
    h('div', { class: 'row' }, h('b', null, 'Новый лимит на категорию')),
    h('div', { class: 'row' }, field('Категория', category, 'wide'), field('Лимит на окно, ' + SIGN[cur], amount)),
    hint, rep.node, prev, status,
    h('div', { class: 'row' }, save, h('button', { type: 'button', class: 'ghost', onclick: onDone }, 'Отмена')));
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const body = collect();
    if (!body) { status.textContent = 'Выбери категорию и введи сумму.'; status.classList.add('err'); return; }
    status.textContent = '';
    status.classList.remove('err');
    save.disabled = true;
    try {
      const v = await addLimit(cur, body);
      onDone();
      ctx.show(v);
    } catch (err) {
      status.textContent = err.message || 'Не удалось сохранить.';
      status.classList.add('err');
    } finally {
      save.disabled = false;
    }
  });
  return form;
}

// Правка лимита (под строкой блока «Лимиты»): сумма, повтор, удаление.
export function editForm(limit, cur, ctx, box) {
  const amount = amountInput(limit.amount);
  const rep = repeatBox(limit.repeat);
  const hint = h('div', { class: 'hint' }, 'Загружаю подсказку…');
  const prev = h('div', { class: 'hint' });
  const status = h('div', { class: 'hint' });

  const collect = () => {
    const v = Number(amount.value);
    return v > 0 ? { id: limit.id, amount: amount.value, currency: cur, repeat: rep.input.checked } : null;
  };
  const refresh = previewer(prev, cur, collect, (pv) => [
    'Что изменится при ' + money(Number(amount.value), cur) + ':',
    h('div', null, 'Осталось на «' + limit.category + '»: ' + money(limit.left, cur) + ' → ' + money(pv.limit ? pv.limit.left : 0, cur)),
    h('div', null, 'Свободно до конца окна: ' + money(pv.canSpendBefore, cur) + ' → ' + money(pv.canSpendAfter, cur)),
    ...exceedNote(pv),
  ]);
  amount.addEventListener('input', refresh);
  rep.input.addEventListener('change', refresh);

  suggestLimit(cur, limit.category).then((sug) => {
    hint.replaceChildren(suggestText(sug, limit.category, cur), ' ',
      sug.enough ? h('a', { class: 'x', href: '#', onclick: (e) => { e.preventDefault(); amount.value = String(sug.amount); refresh(); } }, 'подставить') : null);
  }).catch(() => hint.replaceChildren());

  const save = h('button', { type: 'submit' }, 'Сохранить');
  const remove = h('a', {
    class: 'x', href: '#',
    onclick: async (e) => {
      e.preventDefault();
      if (!confirm('Убрать лимит на «' + limit.category + '»?')) return;
      try { ctx.show(await deleteLimit(cur, limit.id)); } catch (err) { status.textContent = err.message || 'Не удалось убрать.'; status.classList.add('err'); }
    },
  }, 'убрать лимит');
  const form = h('form', null,
    h('div', { class: 'row' }, field('Лимит на окно, ' + SIGN[cur], amount), hint),
    rep.node, prev, status,
    h('div', { class: 'row' }, save, h('button', { type: 'button', class: 'ghost', onclick: () => box.remove() }, 'Отмена'), h('span', { class: 'sp' }), remove));
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const body = collect();
    if (!body) { status.textContent = 'Введи сумму больше нуля.'; status.classList.add('err'); return; }
    status.textContent = '';
    status.classList.remove('err');
    save.disabled = true;
    try {
      ctx.show(await editLimit(cur, limit.id, { amount: body.amount, currency: cur, repeat: body.repeat }));
    } catch (err) {
      status.textContent = err.message || 'Не удалось сохранить.';
      status.classList.add('err');
    } finally {
      save.disabled = false;
    }
  });
  return form;
}

