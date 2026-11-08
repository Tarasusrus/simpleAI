import assert from 'node:assert/strict';
import { canEditWindow, lengthNote, dateReady } from '../static/js/blocks/window.js';

// Править можно окно на экране: текущее, будущее, следующее в зазоре; закончившееся и отсутствующее — нет.
const w = { start: '2026-10-05', end: '2026-10-20', days: 16, adjusted: true };
assert.equal(canEditWindow({ state: 'current', window: w }), true);
assert.equal(canEditWindow({ state: 'upcoming', window: w }), true);
assert.equal(canEditWindow({ state: 'between', window: w }), true);
assert.equal(canEditWindow({ state: 'ended', window: w }), false);
assert.equal(canEditWindow({ state: 'none' }), false);

// Подпись длины только у поправленного окна, со склонением.
assert.equal(lengthNote(w), 'окно 16 дней (изменено)');
assert.equal(lengthNote({ days: 21, adjusted: true }), 'окно 21 день (изменено)');
assert.equal(lengthNote({ days: 14, adjusted: false }), '');
// Неполный год, пока его набирают, и та же дата не отправляются.
assert.equal(dateReady('0002-10-25', '2026-10-20'), false);
assert.equal(dateReady('0202-10-25', '2026-10-20'), false);
assert.equal(dateReady('2026-10-20', '2026-10-20'), false);
assert.equal(dateReady('', '2026-10-20'), false);
assert.equal(dateReady('2026-10-25', '2026-10-20'), true);
console.log('ok');
