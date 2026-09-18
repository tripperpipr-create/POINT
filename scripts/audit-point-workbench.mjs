// Замер оформления оболочки Point на живом окне.
//
// Хаб меряется на статических страницах (build-hub-preview + audit-hub-layout),
// потому что его разметку рисует наш собственный main.js. С оболочкой так
// нельзя: `distribution/resources/point-*.css` правит чужую разметку Code-OSS,
// и воспроизвести её страницей значило бы завести подделку, которая разойдётся
// с настоящей на первом обновлении апстрима. Поэтому зонд идёт в настоящий
// рендерер по CDP — окно поднимает scripts/test-point-workbench-design.ps1.
//
// Меряются четыре вещи, которые глаз не ловит, а правило §19 ТЗ требует:
// контраст надписи против фактического фона, размер цели указателя, наличие
// видимого фокуса и отклик на наведение. Код редактора не трогаем — там
// оформление задаёт тема, а не оболочка.
//
// Состояние окна задаётся сценарием: пустое окно показывает малую часть хрома,
// и первый замер видел 143 элемента из тысяч. Вкладки редактора, крошки,
// терминал и палитра команд существуют только после действия пользователя.
//
//   powershell -File scripts/test-point-workbench-design.ps1
//   node scripts/audit-point-workbench.mjs http://127.0.0.1:<порт> [idle|file|panel|palette]

import { readFileSync } from 'node:fs';
// Подготовка окна вынесена в общий модуль: её обязан вызывать и зонд, иначе он
// меряет окно в покое, какой бы сценарий ему ни передали.
import { applyScenario } from './lib/point-scenario.mjs';

const wait = ms => new Promise(resolve => setTimeout(resolve, ms));

const endpoint = process.argv[2] || 'http://127.0.0.1:9333';
const scenario = process.argv[3] || 'idle';
// Вебвью показывает CSS **выложенного** расширения, а не репозитория: между
// ними легко накапливаются циклы правок. Без подмены любой замер вебвью
// говорит о прошлом состоянии продукта, а не о текущем.
const webviewCss = process.argv[4] || '';

// Точек в окне несколько: сам workbench — цель `page`, а каждый вебвью Point
// (компаньон, Хаб) — отдельная цель `iframe` с origin `vscode-webview://`.
// Зонд девять циклов брал только первую, и всё, что мы знали о вебвью, было
// получено на статических страницах стенда, а не в живом приложении.
const targets = await fetch(`${endpoint}/json/list`).then(response => response.json());
const shellTarget = targets.find(item => item.type === 'page' && !String(item.url).startsWith('devtools://'));
if (!shellTarget?.webSocketDebuggerUrl) throw new Error(`Не найден рендерер оболочки на ${endpoint}`);
const webviewTargets = targets.filter(item =>
  item.type !== 'service_worker' && String(item.url).startsWith('vscode-webview://') && item.webSocketDebuggerUrl);

const connect = async wsUrl => {
  const socket = new WebSocket(wsUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, { once: true });
    socket.addEventListener('error', reject, { once: true });
  });
  let sequence = 0;
  const pending = new Map();
  const events = [];
  socket.addEventListener('message', event => {
    const message = JSON.parse(String(event.data));
    if (!message.id) { events.push(message); return; }
    if (!pending.has(message.id)) return;
    const handlers = pending.get(message.id);
    pending.delete(message.id);
    if (message.error) handlers.reject(new Error(message.error.message));
    else handlers.resolve(message.result);
  });
  const command = (method, params = {}) => {
    const id = ++sequence;
    socket.send(JSON.stringify({ id, method, params }));
    return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
  };
  const evaluate = expression => command('Runtime.evaluate', { expression, returnByValue: true })
    .then(result => {
      if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
      return result.result?.value;
    });
  // Вебвью Point вложенный: внешний документ `vscode-webview://…/index.html`
  // держит только iframe, а разметка расширения живёт в дочернем фрейме со
  // своим контекстом исполнения. Обычный Runtime.evaluate попадает во внешний
  // и видит один элемент, поэтому нужен перебор контекстов.
  const contexts = () => events
    .filter(item => item.method === 'Runtime.executionContextCreated')
    .map(item => item.params.context);
  const evaluateIn = (expression, contextId) =>
    command('Runtime.evaluate', { expression, returnByValue: true, contextId })
      .then(result => (result.exceptionDetails ? null : result.result?.value))
      .catch(() => null);

  return { command, evaluate, evaluateIn, contexts, close: () => socket.close() };
};

const { command, evaluate, close } = await connect(shellTarget.webSocketDebuggerUrl);
const socket = { close };

await command('Page.enable');
await command('Runtime.enable');
await command('Input.setIgnoreInputEvents', { ignore: false });

await applyScenario({ command, evaluate, scenario });

const colorProbe = readFileSync(new URL('./lib/contrast-probe.js', import.meta.url), 'utf8');

// Хром оболочки, который красит Point. Содержимое редактора исключено
// намеренно: там цвета ставит тема, и её проверяют не здесь.
// Хром оболочки: Point подкрашивает чужую разметку Code-OSS, поэтому область
// перечислена явно. В вебвью разметка своя целиком, и область — весь документ.
const SHELL_PARTS = ['.part.titlebar', '.part.activitybar', '.point-activitybar', '.part.sidebar',
  '.part.statusbar', '.part.panel', '.part.editor .tabs-container', '.part.editor .breadcrumbs-control',
  // Всплывающий хром существует только в своём состоянии, но красит его тоже
  // оболочка: палитра команд, контекстные меню, диалоги.
  '.quick-input-widget', '.monaco-dialog-box', '.context-view .monaco-menu',
  // Нативный стартовый экран — единственная поверхность оболочки, которую
  // Point рисует целиком сам, а не подкрашивает чужую.
  '.gettingStartedContainer.point-native-home'];

const buildProbe = parts => `(() => {
${colorProbe}
  var PARTS = ${JSON.stringify(parts)};
  var inEditorText = function (element) { return !!element.closest('.monaco-editor, .xterm, .monaco-list-row .monaco-highlighted-label mark'); };

  var roots = [];
  PARTS.forEach(function (selector) {
    document.querySelectorAll(selector).forEach(function (node) { roots.push(node); });
  });
  if (!roots.length) return { fatal: 'оболочка Point не найдена: нет ни одной части .part.* с оверлеем' };

  var seen = new Set();
  var elements = [];
  roots.forEach(function (root) {
    root.querySelectorAll('*').forEach(function (node) {
      if (seen.has(node) || inEditorText(node)) return;
      seen.add(node); elements.push(node);
    });
  });

  // Невидимость наследуется, а вычисленный стиль — нет. Ряд меню в заголовке
  // Point живёт под opacity: 0 и pointer-events: none, пока его не развернёт
  // гамбургер; у самих кнопок меню при этом opacity: 1, коробка 44px и
  // положение под командным центром. Проверка по собственному стилю объявляла
  // восемь невидимых кнопок перекрытыми, а их подписи — не прошедшими контраст.
  // Смотреть надо всю цепочку до корня.
  var hiddenByAncestor = function (element) {
    for (var node = element; node && node !== document.documentElement; node = node.parentElement) {
      var style = getComputedStyle(node);
      if (style.visibility === 'hidden' || style.display === 'none' || +style.opacity === 0) return true;
    }
    return false;
  };
  var visible = function (element) {
    if (hiddenByAncestor(element)) return false;
    var box = element.getBoundingClientRect();
    return box.width >= 2 && box.height >= 2;
  };

  // ── Правила фокуса и наведения, объявленные в CSS ───────────────────────
  var splitSelector = function (text) {
    var parts = [], depth = 0, buffer = '';
    for (var i = 0; i < text.length; i++) {
      var ch = text[i];
      if (ch === '(') depth++; else if (ch === ')') depth--;
      if (ch === ',' && depth === 0) { parts.push(buffer); buffer = ''; continue; }
      buffer += ch;
    }
    if (buffer.trim()) parts.push(buffer);
    return parts;
  };
  var collect = function (pattern) {
    var out = [];
    for (var s = 0; s < document.styleSheets.length; s++) {
      var rules; try { rules = document.styleSheets[s].cssRules; } catch (e) { continue; }
      (function walk(list) {
        for (var i = 0; i < list.length; i++) {
          var rule = list[i];
          // У обычного CSSStyleRule тоже есть (пустой) cssRules из-за поддержки
          // вложенности — сначала разбираем правило, потом спускаемся.
          if (rule.selectorText && pattern.test(rule.selectorText)) {
            var css = rule.style.cssText;
            var paints = /background|color|border|outline|opacity|box-shadow|text-decoration/.test(css);
            var blank = /outline:\\s*(none|0px|0)\\s*(;|$)/.test(css) && !/box-shadow|border-color|background/.test(css);
            if (paints && !blank) splitSelector(rule.selectorText).forEach(function (sel) {
              sel = sel.trim();
              if (!pattern.test(sel)) return;
              out.push(sel.replace(/:focus-visible/g, '').replace(/:focus(?![\\w-])/g, '').split(':hover')[0] || '*');
            });
          }
          if (rule.cssRules && rule.cssRules.length) walk(rule.cssRules);
        }
      })(rules);
    }
    return out;
  };
  var focusSelectors = collect(/:focus(-visible)?(?![\\w-])/);
  var hoverSelectors = collect(/:hover(?![\\w-])/);
  var covered = function (element, selectors) {
    for (var i = 0; i < selectors.length; i++) {
      try { if (selectors[i] && element.matches(selectors[i])) return true; } catch (e) {}
    }
    return false;
  };

  var name = function (element) {
    return (element.tagName.toLowerCase() +
      (element.className && typeof element.className === 'string'
        ? '.' + element.className.trim().split(/\\s+/).slice(0, 3).join('.') : '')).slice(0, 46);
  };

  var result = { parts: roots.length, scanned: 0, focusRules: focusSelectors.length, hoverRules: hoverSelectors.length,
    contrast: [], tiny: [], noFocus: [], noHover: [], obstructed: [], clipped: [] };

  // Перекрытие и обрезка — §17 ТЗ, «высокий приоритет», и ровно тот класс,
  // который глазом ловится хуже всего: элемент виден, но кликается не он, или
  // подпись обрезана ровно на границе видимого. Зонд девять циклов их не искал.
  //
  // Перекрытым считается интерактивный элемент, у которого документ в центре
  // отвечает чужим узлом: не им самим, не его потомком и не его предком. Предок
  // — законный ответ: клик по нему всё равно доходит.
  //
  // Ловушка, стоившая одного захода: элемент вне видимой области — прокручен за
  // край панели или лежит ниже сгиба — коробку имеет, а точку в окне занимает
  // уже кто-то другой. elementFromPoint честно отвечает про **экран**, и
  // проверка без этой оговорки объявляла перекрытой каждую кнопку под сгибом.
  var obstructedBy = function (element, box) {
    var x = box.left + box.width / 2;
    var y = box.top + box.height / 2;
    if (x < 0 || y < 0 || x > document.documentElement.clientWidth || y > document.documentElement.clientHeight) return null;
    var node = document.elementFromPoint(x, y);
    if (!node || node === element) return null;
    if (element.contains(node) || node.contains(element)) return null;
    return node;
  };
  // Обрезкой считается текст, который не помещается и не помечен многоточием:
  // многоточие — сознательное решение, а тихий обрез — потеря информации.
  var clippedBy = function (element, style) {
    if (element.children.length) return false;
    if (!(element.textContent || '').trim()) return false;
    if (style.overflowX === 'visible' || style.overflow === 'visible') return false;
    if (style.textOverflow === 'ellipsis') return false;
    return element.scrollWidth > element.clientWidth + 1 && element.clientWidth > 0;
  };

  elements.forEach(function (element) {
    if (!visible(element)) return;
    result.scanned++;

    // Контраст: только листья с собственным текстом.
    if (!element.children.length && (element.textContent || '').trim()) {
      var measured = contrastOf(element);
      if (measured && measured.ratio < measured.need) {
        result.contrast.push({ text: (element.textContent || '').trim().slice(0, 30), ratio: measured.ratio,
          need: measured.need, size: measured.size, color: measured.color, where: name(element) });
      }
    }

    // Интерактивные: цель указателя, фокус, наведение.
    var role = element.getAttribute('role');
    var interactive = element.matches('a[href], button, input, select, textarea, [tabindex]:not([tabindex="-1"])')
      || role === 'button' || role === 'tab' || role === 'checkbox' || role === 'menuitem';
    if (!interactive || element.disabled) return;
    var box = element.getBoundingClientRect();
    if (box.height < 24 || box.width < 24) {
      // Размер коробки — не то же самое, что область попадания: невидимый слой
      // (::after с отрицательным inset) расширяет цель, не трогая вид кнопки.
      // Спрашиваем документ, кто отвечает под курсором на пороговом радиусе.
      var reach = 11; // ±11 от центра — это 22px, ближайшее к порогу 24 целое
      var answers = function (dx, dy) {
        var node = document.elementFromPoint(box.left + box.width / 2 + dx, box.top + box.height / 2 + dy);
        return !!(node && (node === element || element.contains(node) ||
          (node.closest && node.closest('a, button, [role="button"]') === element)));
      };
      var reachesX = box.width >= 24 || (answers(-reach, 0) && answers(reach, 0));
      var reachesY = box.height >= 24 || (answers(0, -reach) && answers(0, reach));
      if (!reachesX || !reachesY) {
        result.tiny.push(name(element) + ' [' + Math.round(box.width) + '×' + Math.round(box.height) + ']');
      }
    }
    if (!covered(element, focusSelectors)) result.noFocus.push(name(element));
    if (!covered(element, hoverSelectors)) result.noHover.push(name(element));
    var over = obstructedBy(element, box);
    if (over) result.obstructed.push(name(element) + ' [' + Math.round(box.width) + '×' + Math.round(box.height)
      + '] перекрыт ' + name(over));
  });

  elements.forEach(function (element) {
    if (!visible(element)) return;
    var style = getComputedStyle(element);
    if (clippedBy(element, style)) {
      result.clipped.push(name(element) + ' [' + element.clientWidth + ' из ' + element.scrollWidth + 'px]'
        + ' «' + (element.textContent || '').trim().slice(0, 24) + '»');
    }
  });

  var tally = function (list) {
    var map = new Map();
    list.forEach(function (key) { map.set(key, (map.get(key) || 0) + 1); });
    return [...map.entries()].sort(function (a, b) { return b[1] - a[1]; }).slice(0, 10)
      .map(function (pair) { return pair[1] + '× ' + pair[0]; });
  };
  result.tiny = tally(result.tiny);
  result.noFocus = tally(result.noFocus);
  result.noHover = tally(result.noHover);
  result.obstructed = tally(result.obstructed);
  result.clipped = tally(result.clipped);
  result.contrast = result.contrast.sort(function (a, b) { return a.ratio - b.ratio; }).slice(0, 10);
  return result;
})()`;

// Принятые отступления держатся файлом, а не памятью. Без него аудит возвращал
// exit 1 всегда — из-за двух мелких целей статус-бара, принятых ещё в цикле 11,
// — и его код возврата не значил ничего: новая находка утонула бы среди старых.
const accepted = JSON.parse(readFileSync(new URL('./lib/shell-accepted.json', import.meta.url), 'utf8')).accepted;
const usedAcceptances = new Set();
const acceptanceOf = (surface, check, text) => accepted.some((item, index) => {
  if (!surface.startsWith(item.surface) || item.check !== check) return false;
  if (!String(text).includes(item.match)) return false;
  usedAcceptances.add(index);
  return true;
});

const section = (surface, title, rows, render) => {
  const rendered = rows.map(row => render(row));
  const fresh = rendered.filter(text => !acceptanceOf(surface, title, text));
  const known = rendered.length - fresh.length;
  if (!rendered.length) { console.log(`  ${title}: чисто`); return 0; }
  if (!fresh.length) { console.log(`  ${title}: новых нет · ${known} принято`); return 0; }
  console.log(`  ${title}: новых ${fresh.length}${known ? ` · ${known} принято` : ''}`);
  fresh.forEach(text => console.log('    ' + text));
  return fresh.length;
};

const report = (label, measured) => {
  if (measured?.fatal) { console.error(`${label}: ${measured.fatal}`); return false; }
  console.log(`\n══ ${label} · осмотрено элементов: ${measured.scanned} · ` +
    `правил фокуса: ${measured.focusRules} · правил наведения: ${measured.hoverRules}`);
  let fresh = 0;
  fresh += section(label, 'надписи ниже порога контраста', measured.contrast,
    row => `${String(row.ratio).padStart(5)} при ${row.need} · ${row.size}px · ${row.color} · ${row.where} · «${row.text}»`);
  fresh += section(label, 'цель указателя меньше 24px', measured.tiny, row => row);
  fresh += section(label, 'без видимого фокуса', measured.noFocus, row => row);
  fresh += section(label, 'без отклика на наведение', measured.noHover, row => row);
  fresh += section(label, 'перекрыт другим элементом', measured.obstructed, row => row);
  fresh += section(label, 'текст обрезан без многоточия', measured.clipped, row => row);
  return fresh === 0;
};

try {
  console.log(`сценарий: ${scenario} · вебвью найдено: ${webviewTargets.length}`);
  let clean = report(`оболочка (${SHELL_PARTS.length} частей хрома)`, await evaluate(buildProbe(SHELL_PARTS)));

  // Разметка вебвью целиком наша, поэтому область — весь документ. Контекст
  // выбирается по факту: тот, где разметка Point действительно есть.
  let viewIndex = 0;
  for (const item of webviewTargets) {
    const view = await connect(item.webSocketDebuggerUrl);
    try {
      await view.command('Runtime.enable');
      await wait(400);
      for (const context of view.contexts()) {
        const surface = await view.evaluateIn(
          `(document.body && (document.body.dataset.layout || document.body.className) || '').slice(0, 40)`, context.id);
        const count = await view.evaluateIn('document.querySelectorAll("body *").length', context.id);
        if (!count || count < 5) continue; // внешняя обёртка вебвью — только iframe внутри
        viewIndex++;
        if (webviewCss) {
          const css = readFileSync(webviewCss, 'utf8');
          const injected = await view.evaluateIn(`(() => {
            var old = document.getElementById('point-audit-css');
            if (old) old.remove();
            var style = document.createElement('style');
            style.id = 'point-audit-css';
            style.textContent = ${JSON.stringify(css)};
            document.head.appendChild(style);
            // Вставить узел мало: у вебвью Point строгий CSP, и заблокированный
            // лист остаётся в DOM, но правил не даёт. Отличать надо по ним.
            var applied = 0;
            try { applied = (style.sheet && style.sheet.cssRules ? style.sheet.cssRules.length : 0); } catch (e) { applied = -1; }
            return { bytes: style.textContent.length, rules: applied };
          })()`, context.id);
          if (!injected) console.error(`  подмена CSS вебвью не удалась (${webviewCss})`);
          else if (injected.rules > 0) console.log(`  CSS вебвью подменён из репозитория: ${injected.bytes} байт, ${injected.rules} правил`);
          else console.error(`  CSS вебвью НЕ применён (${injected.bytes} байт вставлено, правил ${injected.rules}) — вероятно, CSP вебвью. Замер ниже относится к ВЫЛОЖЕННОМУ расширению, а не к репозиторию`);
          await wait(300);
        }
        const measured = await view.evaluateIn(buildProbe(['body']), context.id);
        if (!measured) { console.error(`  вебвью ${viewIndex}: зонд не выполнился`); clean = false; continue; }
        clean = report(`вебвью ${viewIndex}: ${surface || 'без data-layout'}`, measured) && clean;
      }
    } finally {
      view.close();
    }
  }
  if (!viewIndex) console.log('\nвебвью с разметкой Point на экране нет');

  console.log(clean ? '\nвсё окно: надписи читаются, цели крупные, фокус и наведение на месте' : '');
  // Запись, которой больше не соответствует ни одна находка, объясняет то, чего
  // нет: обычно отступление закрыли правкой, а строка осталась висеть.
  const stale = accepted.filter((item, index) => !usedAcceptances.has(index));
  if (stale.length) {
    console.log(`принятые отступления без находки — ${stale.length}: строку пора убрать`);
    for (const item of stale) console.log(`  ${item.surface} · ${item.check} · ${item.match}`);
  }
  if (!clean) process.exitCode = 1;
} finally {
  socket.close();
}
