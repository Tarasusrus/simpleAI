// Сборка узлов без вставки разметки: любой текст попадает в страницу как текстовый узел,
// поэтому название платежа с разметкой остаётся текстом (C39).
export function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? '' : String(v));
  }
  for (const kid of kids.flat()) {
    if (kid == null || kid === false) continue;
    el.append(kid);
  }
  return el;
}

// Панель под строкой («перенести», «изменить»). Повторное нажатие той же кнопки
// закрывает её; другая кнопка закрывает чужую панель и открывает свою, а не просто
// закрывает чужую. build() строит панель, возвращается она же (null — закрыли).
export function togglePanel(line, name, build) {
  const next = line.nextElementSibling;
  if (next && next.classList.contains('ed')) {
    const same = next.dataset.panel === name;
    next.remove();
    if (same) return null;
  }
  const box = build();
  box.dataset.panel = name;
  line.after(box);
  return box;
}
