// Режим формы прихода (ADR-019): нужно ли владельцу выбирать «зарплата/аванс» или «обычный приход».
// Чистые функции от вида окна и даты: проверяются без DOM.
//
//   none      — окон нет вовсе: любое поступление откроет окно, выбор не нужен;
//   inside    — дата внутри показанного окна: поступление просто входит в него, выбор не нужен;
//   ask       — дата вне известных окон: выбор обязателен и без предвыбора;
//   nodate    — даты ещё нет.

const DAY = 24 * 3600 * 1000;

function toTime(iso) { return Date.parse(iso + 'T00:00:00Z'); }

export function addDays(iso, n) {
  return new Date(toTime(iso) + n * DAY).toISOString().slice(0, 10);
}

function inside(w, iso) { return !!w && iso >= w.start && iso <= w.end; }

export function incomeMode(view, iso) {
  if (!iso) return 'nodate';
  if (!view || view.state === 'none') return 'none';
  if (inside(view.window, iso) || inside(view.lastWindow, iso)) return 'inside';
  return 'ask';
}

// Что уйдёт на сервер как opensWindow: при обязательном выборе — выбранное (undefined, пока не выбрано),
// иначе true (прежнее поведение: поступление открывает окно, внутри окна это ни на что не влияет).
export function opensWindowFor(mode, choice) {
  if (mode === 'ask') return choice;
  return true;
}

// Можно ли отправлять: при обязательном выборе он должен быть сделан.
export function canSubmit(mode, choice) {
  if (mode === 'nodate') return false;
  return mode !== 'ask' || typeof choice === 'boolean';
}

// Подпись под выбором — по числам и датам, без предположений о том, к какому именно окну привяжет сервер.
export function caption(mode, choice, iso, view) {
  switch (mode) {
    case 'none': return 'Окна пока нет: это поступление откроет окно на 14 дней с этой даты.';
    case 'inside': {
      const w = inside(view.window, iso) ? view.window : view.lastWindow;
      return 'Поступление войдёт в окно ' + w.start.slice(5).split('-').reverse().join('.') + '–' + w.end.slice(5).split('-').reverse().join('.') + '.';
    }
    case 'ask':
      if (choice === true) return 'Откроет новое окно на 14 дней: до ' + addDays(iso, 13).slice(5).split('-').reverse().join('.') + '.';
      if (choice === false) return 'Новое окно не откроется: приход войдёт в уже существующее окно.';
      return 'Дата вне текущих окон: выбери, это зарплата/аванс (откроет новое окно) или обычный приход (войдёт в существующее).';
    default: return 'Укажи, когда придёт.';
  }
}
