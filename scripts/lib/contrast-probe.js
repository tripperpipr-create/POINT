// Разбор цвета для зондов, исполняемых внутри страницы.
//
// Файл не импортируется, а читается как текст и подставляется в выражение
// Runtime.evaluate: зонд живёт в браузере, а не в Node. Отсюда обычный var/
// function вместо модульного синтаксиса и никакого экспорта.
//
// Смысл считать контраст здесь, а не по токенам: правило может поставить
// тусклый текст на плашку, которой в палитре нет, и проверка токенов об этом
// не узнает. Фон берётся фактический — сложением всех предков с их альфой.
//
// Пользуются: scripts/audit-hub-layout.mjs, scripts/audit-point-workbench.mjs.

function parseColor(value) {
  var m = String(value).match(/rgba?\((\d+(?:\.\d+)?),\s*(\d+(?:\.\d+)?),\s*(\d+(?:\.\d+)?)(?:,\s*([\d.]+))?\)/);
  if (m) return { r: +m[1], g: +m[2], b: +m[3], a: m[4] === undefined ? 1 : +m[4] };
  // color(srgb r g b / a) — так Chrome отдаёт результат color-mix().
  var c = String(value).match(/color\(srgb\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)(?:\s*\/\s*([\d.]+))?\)/);
  if (c) return { r: +c[1] * 255, g: +c[2] * 255, b: +c[3] * 255, a: c[4] === undefined ? 1 : +c[4] };
  return null;
}

function channelValue(value) {
  var s = value / 255;
  return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
}

function luminanceOf(color) {
  return 0.2126 * channelValue(color.r) + 0.7152 * channelValue(color.g) + 0.0722 * channelValue(color.b);
}

function flattenColor(front, back) {
  return {
    r: front.r * front.a + back.r * (1 - front.a),
    g: front.g * front.a + back.g * (1 - front.a),
    b: front.b * front.a + back.b * (1 - front.a),
    a: 1,
  };
}

// Накопленная прозрачность: opacity применяется к поддереву целиком, поэтому
// приглушённая карточка гасит и свой фон, и свой текст. Без этого множителя
// замер считал контраст так, будто элемент нарисован в полную силу, и не видел
// ничего плохого в подписи на непрозрачных 0.55.
function opacityChainOf(element) {
  var value = 1;
  for (var node = element; node && node !== document.documentElement; node = node.parentElement) {
    var own = parseFloat(getComputedStyle(node).opacity);
    if (!isNaN(own)) value *= own;
  }
  return value;
}

// Фактический фон под элементом: складываем фоны предков сверху вниз, каждый —
// с учётом прозрачности, накопленной до него. Точная композиция групп opacity
// требует послойной отрисовки; для порога достаточно этого приближения.
function backdropOf(element, root) {
  var chain = [];
  for (var node = element; node && node !== document.documentElement; node = node.parentElement) chain.push(node);
  var result = root || { r: 8, g: 8, b: 10, a: 1 };
  chain.reverse();
  for (var i = 0; i < chain.length; i++) {
    var color = parseColor(getComputedStyle(chain[i]).backgroundColor);
    if (!color || color.a <= 0) continue;
    var faded = { r: color.r, g: color.g, b: color.b, a: color.a * opacityChainOf(chain[i]) };
    if (faded.a > 0) result = flattenColor(faded, result);
  }
  return result;
}

// Отношение контраста надписи к фактическому фону и требуемый порог WCAG AA.
function contrastOf(element, root) {
  var style = getComputedStyle(element);
  var foreground = parseColor(style.color);
  if (!foreground) return null;
  var background = backdropOf(element, root);
  var fade = opacityChainOf(element);
  var front = flattenColor({ r: foreground.r, g: foreground.g, b: foreground.b, a: foreground.a * fade }, background);
  var a = luminanceOf(front) + 0.05;
  var b = luminanceOf(background) + 0.05;
  var size = parseFloat(style.fontSize);
  var large = size >= 24 || (size >= 18.66 && +style.fontWeight >= 700);
  return {
    ratio: Math.round((Math.max(a, b) / Math.min(a, b)) * 100) / 100,
    need: large ? 3 : 4.5,
    size: size,
    color: style.color,
  };
}
