import assert from 'node:assert/strict';
import { carryNote } from '../static/js/blocks/kpi.js';

// Перенос показывается со знаком и только когда он есть.
assert.equal(carryNote({ carry: 0 }, 'RUB'), '');
assert.match(carryNote({ carry: 1500 }, 'RUB'), /перенос из прошлого окна \+/);
assert.match(carryNote({ carry: -700 }, 'RUB'), /перенос из прошлого окна −/);
console.log('ok');
