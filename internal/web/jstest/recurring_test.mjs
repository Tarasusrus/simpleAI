import assert from 'node:assert/strict';
import { every } from '../static/js/fmt.js';

assert.equal(every(1, 'month'), 'каждый месяц');
assert.equal(every(1, 'week'), 'каждую неделю');
assert.equal(every(2, 'week'), 'каждые 2 недели');
assert.equal(every(5, 'month'), 'каждые 5 месяцев');
assert.equal(every(3, 'year'), 'каждые 3 года');
assert.equal(every(10, 'year'), 'каждые 10 лет');
assert.equal(every(14, 'day'), 'каждые 14 дней');
assert.equal(every(1, 'bogus'), '—');
console.log('ok');
