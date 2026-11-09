import assert from 'node:assert/strict';
import { incomeMode, opensWindowFor, canSubmit, caption, addDays } from '../static/js/blocks/income-mode.js';

const view = { state: 'current', window: { start: '2026-10-05', end: '2026-10-18' }, lastWindow: { start: '2026-09-20', end: '2026-10-03' } };

// Режимы по дате (ADR-019): нет окон; внутри показанного или прошлого окна; вне окон — выбор обязателен; даты нет.
assert.equal(incomeMode({ state: 'none' }, '2026-10-10'), 'none');
assert.equal(incomeMode(view, '2026-10-10'), 'inside');
assert.equal(incomeMode(view, '2026-10-18'), 'inside', 'последний день окна');
assert.equal(incomeMode(view, '2026-09-25'), 'inside', 'прошлое окно');
assert.equal(incomeMode(view, '2026-10-19'), 'ask', 'на день после окна');
assert.equal(incomeMode(view, '2026-10-04'), 'ask', 'зазор между окнами');
assert.equal(incomeMode(view, ''), 'nodate');

// Вне окон выбор обязателен без предвыбора; внутри окна и без окон отправка свободна, флаг true (как раньше).
assert.equal(canSubmit('ask', undefined), false);
assert.equal(canSubmit('ask', true), true);
assert.equal(canSubmit('ask', false), true);
assert.equal(canSubmit('inside', undefined), true);
assert.equal(canSubmit('none', undefined), true);
assert.equal(canSubmit('nodate', true), false);
assert.equal(opensWindowFor('ask', false), false, 'выбранное уходит как есть');
assert.equal(opensWindowFor('ask', true), true);
assert.equal(opensWindowFor('ask', undefined), undefined, 'без выбора ничего не уходит');
assert.equal(opensWindowFor('inside', undefined), true);
assert.equal(opensWindowFor('none', undefined), true);

// Подписи: не врут про то, что откроется.
assert.match(caption('ask', true, '2026-11-01', view), /Откроет новое окно.*до 14\.11/);
assert.match(caption('ask', false, '2026-11-01', view), /не откроется/);
assert.match(caption('ask', undefined, '2026-11-01', view), /выбери/);
assert.match(caption('inside', undefined, '2026-10-10', view), /войдёт в окно 05\.10–18\.10/);
assert.match(caption('none', undefined, '2026-10-10', { state: 'none' }), /откроет окно/);
assert.equal(addDays('2026-10-31', 1), '2026-11-01');
console.log('ok');
