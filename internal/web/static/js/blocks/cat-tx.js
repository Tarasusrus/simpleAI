import { h } from '../dom.js';
import { getCategoryTransactions } from '../api.js';
import { money, shortDate } from '../fmt.js';
import { txLink } from '../txlink.js';

// Раскрытие категории (ADR-015 §6): траты окна, «Показать все траты категории» и «Впереди в категории».
// Окно определяет сервер; здесь только показ и подгрузка страниц по курсору.

const PREVIEW = 3; // сколько трат видно до «Показать все»

function txRow(r, cur) {
  return h('div', { class: 'tr' },
    h('span', { class: 'td' }, shortDate(r.date)),
    h('span', { class: 'tn' }, r.name),
    h('span', { class: 'ta' }, money(r.amount, cur)),
    txLink(r.key));
}

function aheadRow(r, cur) {
  return h('div', { class: 'tr' },
    h('span', { class: 'td' }, r.date ? shortDate(r.date) : ''),
    h('span', { class: 'tn' }, r.name),
    h('span', { class: 'ta mut' }, r.amount == null ? '—' : money(r.amount, cur)));
}

export function txPanel(category, cur) {
  const box = h('div', { class: 'txp' }, h('div', { class: 'hint' }, 'Загружаю траты…'));
  let rows = [];
  let ahead = [];
  let next = '';
  let hasMore = false;
  let all = false; // раскрыты ли все загруженные траты
  let busy = false;

  const draw = () => {
    const shown = all ? rows : rows.slice(0, PREVIEW);
    const kids = [h('div', { class: 'cap2' }, 'Траты в этом окне')];
    if (!rows.length) kids.push(h('div', { class: 'hint' }, 'В этом окне трат нет.'));
    kids.push(...shown.map((r) => txRow(r, cur)));
    if ((!all && rows.length > PREVIEW) || hasMore) {
      kids.push(h('button', {
        type: 'button', class: 'lnk',
        onclick: async () => {
          if (busy) return;
          if (!all) { all = true; draw(); if (!hasMore) return; }
          await more();
        },
      }, all ? 'Показать ещё' : 'Показать все траты категории'));
    }
    if (ahead.length) {
      kids.push(h('div', { class: 'cap2 top2' }, 'Впереди в категории'), ...ahead.map((r) => aheadRow(r, cur)));
    }
    box.replaceChildren(...kids);
  };

  const fail = (e) => {
    box.append(h('div', { class: 'hint err' }, (e && e.message) || 'Не удалось загрузить траты.'));
  };

  const more = async () => {
    busy = true;
    try {
      const page = await getCategoryTransactions(cur, category, next);
      rows = rows.concat(page.rows || []);
      next = page.next || '';
      hasMore = !!page.hasMore;
      draw();
    } catch (e) { fail(e); } finally { busy = false; }
  };

  (async () => {
    try {
      const page = await getCategoryTransactions(cur, category, '');
      rows = page.rows || [];
      ahead = page.ahead || [];
      next = page.next || '';
      hasMore = !!page.hasMore;
      draw();
    } catch (e) { box.replaceChildren(); fail(e); }
  })();
  return box;
}
