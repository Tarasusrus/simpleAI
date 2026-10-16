// Единственное место форматирования: сервер отдаёт целые числа и ISO-даты.
const SIGN = { RUB: '₽', THB: '฿' };
const nf = new Intl.NumberFormat('ru-RU');

export function money(n, cur) {
  if (n == null) return '—';
  const body = nf.format(Math.abs(n)) + ' ' + (SIGN[cur] || cur);
  return n < 0 ? '−' + body : body;
}

// «2026-10-05» → «05.10»
export function shortDate(iso) {
  if (!iso) return '';
  const [, m, d] = iso.split('-');
  return d + '.' + m;
}

export function longDate(iso) {
  if (!iso) return '';
  const [y, m, d] = iso.split('-');
  return d + '.' + m + '.' + y;
}

const MONTHS = ['январь', 'февраль', 'март', 'апрель', 'май', 'июнь', 'июль', 'август', 'сентябрь', 'октябрь', 'ноябрь', 'декабрь'];
export function monthName(m) { return MONTHS[m - 1] || String(m); }

export function plural(n, one, few, many) {
  const a = Math.abs(n) % 100, b = a % 10;
  if (a > 10 && a < 20) return many;
  if (b > 1 && b < 5) return few;
  if (b === 1) return one;
  return many;
}
