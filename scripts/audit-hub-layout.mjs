// Замер вёрстки Хаба на отрисованных страницах: переполнение по горизонтали,
// общий левый край и контраст надписей на фактически видимом фоне.
//
// Смоуки проверяют, что нужная строка есть в разметке, а стенд из VISUAL-LOOP
// показывает страницу глазу. Ни то, ни другое не отвечает на три вопроса,
// которые в отдельном окне решают всё: не уехала ли колонка за край, стоит ли
// содержимое раздела на том же левом краю, что крошка над ним, и читается ли
// самая мелкая надпись. Первое маскируется вертикальной прокруткой, второе
// заметно только при переключении разделов, третье глазом не измеряется вовсе.
//
// Страницы берутся из build/preview — те самые, что делает render-hub-surface.
// Браузер нужен настоящий: медиазапросы, шкала и вычисленный фон существуют
// только в нём.
//
//   node scripts/build-hub-preview.js                        # разложить страницы
//   chrome --headless=new --remote-debugging-port=9333 about:blank
//   cd build/preview && python -m http.server 8791 --bind 127.0.0.1
//   node scripts/audit-hub-layout.mjs http://127.0.0.1:9333 http://127.0.0.1:8791

import { readFileSync } from 'node:fs';

const endpoint = process.argv[2] || 'http://127.0.0.1:9333';
const base = process.argv[3] || 'http://127.0.0.1:8791';

// Все вкладки рейки, а не выборка: разъезжается обычно та, на которую не смотрят.
// Список общий с build-hub-preview.js — тем самым скриптом, который эти
// страницы и раскладывает. Пока списков было два, аудит мерил страницу,
// которую сборка не перерисовывала.
//
// Компаньон живёт в окне IDE, а не в Чертоге, и разметка у него своя: корень
// `.app`, ни рейки, ни крошки. Корень задаётся на страницу — добавление стоит
// одной строки, а не второго скрипта.
const surfaces = JSON.parse(readFileSync(new URL('./lib/hub-surfaces.json', import.meta.url), 'utf8'))
  .surfaces.filter(item => item.audit !== false);
const pages = surfaces.map(item => item.page);
// Корень и размеры окна — свойства поверхности, а не второй список внутри
// аудита. До цикла 34 они лежали здесь, и добавление окна инструмента значило
// правку в двух файлах вместо одного.
const surfaceOf = page => surfaces.find(item => item.page === page) || {};
const rootOf = page => surfaceOf(page).root || '.hall';

// Принятые отступления держатся файлом, а не памятью — по образцу
// shell-accepted.json (цикл 33). Проверка, которая всегда возвращает одни и те
// же находки, перестаёт что-либо значить: новая тонет среди старых.
// Размер сверяется целиком («22×22») или по высоте («22»): высота строки — то,
// о чём принято решение, а ширина зависит от окна.
const acceptedList = JSON.parse(readFileSync(new URL('./lib/hub-accepted.json', import.meta.url), 'utf8')).accepted;
const usedAccepted = new Set();
const acceptedTarget = (page, row) => acceptedList.some((item, index) => {
  if (item.page !== page || item.check !== 'цель' || item.selector !== row.selector) return false;
  const height = String(row.size).split('×')[1];
  if (item.size !== String(row.size) && item.size !== height) return false;
  usedAccepted.add(index);
  return true;
});
// Рейка Чертога — clamp(164px, 15vw, 208px), поэтому узкие ширины проверяют не
// только медиазапросы, но и то, что колонкам осталось после рейки.
const widths = [1600, 1280, 1024, 860];
// Раскладки, где левый край другой по замыслу: сплит очереди решений и истории
// по файлу прижат к краю, диалог с Мастером выключен по центру. У компаньона
// крошки нет вовсе — сравнивать нечего.
const splitLayouts = new Set(['decisions', 'filehistory', 'master', 'companion', 'companion-sidebar']);
// У компаньона два регистра, и форма окна у них противоположная: боковая
// панель — узкая и высокая колонка, док — широкая и низкая полоса. Мерить оба
// на 900px высоты, как Чертог, значит не мерить док вовсе: в жизни ему
// достаётся около 300, и именно там содержимое перестаёт помещаться.
// Высоты дока компаньона взяты с живого окна: нижняя панель там 296px. 340 —
// с запасом, 260 — стресс: ниже док в продукте не опускают. Теперь эти числа
// лежат вместе с поверхностью, в hub-surfaces.json.
// Своё свойство, а не унаследованное: одна из страниц называется
// `constructor`, и обычный `SIZES[page]` вернул бы функцию-конструктор из
// `Object.prototype` вместо undefined — размеры страницы перестали бы быть
// списком, а падало бы это строкой ниже и без объяснения.
const sizesOf = page => surfaceOf(page).sizes || widths.map(width => [width, 900]);

const targets = await fetch(`${endpoint}/json/list`).then(response => response.json());
const target = targets.find(item => item.type === 'page' && !String(item.url).startsWith('devtools://'));
if (!target?.webSocketDebuggerUrl) throw new Error(`Не найдена вкладка браузера на ${endpoint}`);

const socket = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((resolve, reject) => {
  socket.addEventListener('open', resolve, { once: true });
  socket.addEventListener('error', reject, { once: true });
});
let sequence = 0;
const pending = new Map();
socket.addEventListener('message', event => {
  const message = JSON.parse(String(event.data));
  if (!message.id || !pending.has(message.id)) return;
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
  .then(result => result.result?.value);

await command('Page.enable');
await command('Runtime.enable');

const geometry = root => `(() => {
  const ROOT = ${JSON.stringify(root)};
  const doc = document.documentElement;
  // Уехавшее за край и прокручиваемое вбок — разные вещи. Содержимое полосы с
  // overflow-x: auto заведомо шире её самой: это и есть способ показать ряд
  // чипов в узком окне, и до него можно доскроллить. Проверка ищет то, что
  // ушло за край **без** такого предка, то есть стало недостижимым.
  const scrollsSideways = element => {
    for (let node = element.parentElement; node; node = node.parentElement) {
      const style = getComputedStyle(node);
      if (style.overflowX === 'auto' || style.overflowX === 'scroll') return true;
      if (node.matches(ROOT)) break;
    }
    return false;
  };
  const spill = [...document.querySelectorAll(ROOT + ' *')].filter(element => {
    const box = element.getBoundingClientRect();
    return box.width > 0 && box.right > doc.clientWidth + 1 && !scrollsSideways(element);
  }).slice(0, 3).map(element => (element.tagName + '.' + String(element.className)).slice(0, 60));
  const crumb = document.querySelector('.hall-crumb')?.getBoundingClientRect().left;
  const content = document.querySelector('.hall-body > * > *')?.getBoundingClientRect().left;
  return {
    // Нашёлся ли вообще корень страницы. Молчание зонда и пустая страница
    // выглядят одинаково — «находок нет», — и однажды это стоило целого прогона:
    // сервер страниц умер, все запросы вернули 404, замер отчитался зелёным по
    // сорока пяти страницам подряд. Пустую страницу надо называть пустой.
    rooted: Boolean(document.querySelector(ROOT)),
    overflow: Math.max(0, doc.scrollWidth - doc.clientWidth),
    spill,
    crumb: crumb == null ? null : Math.round(crumb),
    content: content == null ? null : Math.round(content),
  };
})()`;

// Контраст считается против фактически видимого фона: правило может поставить
// тусклый текст на плашку, которой нет в токенах, и палитра об этом не узнает.
// Сам разбор цвета — в scripts/lib/contrast-probe.js: его же читает зонд
// оболочки, и две копии одной математики разошлись бы на первой правке.
const colorProbe = readFileSync(new URL('./lib/contrast-probe.js', import.meta.url), 'utf8');
const contrast = root => `(() => {
  const ROOT = ${JSON.stringify(root)};
${colorProbe}
  // Прозрачность держит родитель, а не сам узел: инструменты реплики скрыты
  // через opacity: 0 на обёртке (.hall-msg-tools), и проверка только своего
  // стиля считала их видимыми. Отсюда на каждой странице Мастера пять записей
  // про контраст 1 у кнопок, которых на экране нет. Смотрим всю цепочку.
  const fadedAway = node => {
    for (let item = node; item && item !== document.documentElement; item = item.parentElement) {
      if (+getComputedStyle(item).opacity === 0) return true;
    }
    return false;
  };
  const found = [];
  for (const element of document.querySelectorAll(ROOT + ' *')) {
    if (element.children.length) continue;
    if (!(element.textContent || '').trim()) continue;
    const style = getComputedStyle(element);
    if (style.visibility === 'hidden' || style.display === 'none' || fadedAway(element)) continue;
    const box = element.getBoundingClientRect();
    if (box.width < 2 || box.height < 2) continue;
    const measured = contrastOf(element);
    if (!measured) continue;
    if (measured.ratio < measured.need) {
      found.push({
        text: (element.textContent || '').trim().slice(0, 34),
        ratio: measured.ratio,
        need: measured.need,
        size: measured.size,
        color: measured.color,
        selector: (element.tagName + '.' + String(element.className)).slice(0, 40),
        // WCAG 1.4.3 не предъявляет требований к неактивному контролу. Но
        // подпись внутри него всё равно должна быть узнаваема, поэтому такие
        // случаи не выбрасываются, а считаются отдельно.
        inactive: !!element.closest(':disabled, [disabled], [aria-disabled="true"]'),
      });
    }
  }
  return found.slice(0, 12);
})()`;

// Размер цели клика. Стенд Хаба его не мерил вовсе, и в разделах жили кнопки
// 39×17 и 28×18 — обе выглядели как текст со ссылкой, обеими надо было попасть.
// Флажок сам по себе всегда 13×13: у него цель — обнимающий label, поэтому за
// размер отвечает ближайший label, а не сам вход.
const targetProbe = root => `(() => {
  const ROOT = ${JSON.stringify(root)};
  const MIN = 24;
  const found = [];
  for (const element of document.querySelectorAll(['button', 'a[href]', 'summary', '[role="button"]', 'input', 'select'].map(part => ROOT + ' ' + part).join(', '))) {
    const style = getComputedStyle(element);
    if (style.visibility === 'hidden' || style.display === 'none') continue;
    const label = element.closest('label');
    const box = (label || element).getBoundingClientRect();
    if (box.width < 2 || box.height < 2) continue;
    if (box.width >= MIN && box.height >= MIN) continue;
    found.push({
      selector: (element.tagName + '.' + String(element.className)).slice(0, 46),
      size: Math.round(box.width) + '×' + Math.round(box.height),
      text: (element.textContent || element.value || '').trim().slice(0, 24),
      viaLabel: !!label,
    });
  }
  return found.slice(0, 12);
})()`;

// Повтор одной и той же надписи на экране. Самый частый дефект этого продукта
// по журналу: общее правило в каждой карточке журнала (цикл 15), три сводки
// готовности в онбординге (цикл 20), контекст IDE дважды у компаньона (цикл 21).
// Каждый раз находилось глазами и по одному. Считать умеет машина.
//
// Порог в 14 знаков отсекает служебное — «0», «Go», «✓», названия колонок.
// Повторяющиеся значения внутри одного списка (пути файлов, имена агентов) от
// повтора подписи отличаются общим родителем, поэтому пара учитывается только
// при разных родителях.
const duplicateProbe = root => `(() => {
  const ROOT = ${JSON.stringify(root)};
  // Прозрачность держит родитель, а не сам узел: инструменты реплики скрыты
  // через opacity: 0 на обёртке (.hall-msg-tools), и проверка только своего
  // стиля считала их видимыми. Отсюда на каждой странице Мастера пять записей
  // про контраст 1 у кнопок, которых на экране нет. Смотрим всю цепочку.
  const fadedAway = node => {
    for (let item = node; item && item !== document.documentElement; item = item.parentElement) {
      if (+getComputedStyle(item).opacity === 0) return true;
    }
    return false;
  };
  const seen = new Map();
  for (const element of document.querySelectorAll(ROOT + ' *')) {
    if (element.children.length) continue;
    const style = getComputedStyle(element);
    if (style.visibility === 'hidden' || style.display === 'none' || fadedAway(element)) continue;
    const box = element.getBoundingClientRect();
    if (box.width < 2 || box.height < 2) continue;
    const text = (element.textContent || '').trim().replace(/\s+/g, ' ');
    if (text.length < 14) continue;
    if (!seen.has(text)) seen.set(text, []);
    seen.get(text).push(element);
  }
  const found = [];
  for (const [text, list] of seen) {
    if (list.length < 2) continue;
    const parents = new Set(list.map(item => item.parentElement));
    if (parents.size < 2) continue;
    found.push({ text: text.slice(0, 52), count: list.length,
      where: [...new Set(list.map(item => (item.tagName + '.' + String(item.className)).slice(0, 34)))].join(' | ') });
  }
  return found.sort((a, b) => b.count - a.count).slice(0, 6);
})()`;

const problems = [];
// Неактивные контролы WCAG 1.4.3 от требования освобождает, но подпись на них
// всё равно должна быть узнаваема — держим их отдельным списком, а не молчим.
const dimmed = [];
// Повтор — не всегда ошибка (значение в списке, парная подпись), поэтому он
// идёт отдельным списком и сборку не роняет.
const repeats = [];
let checks = 0;
try {
  // Порядок обхода перевёрнут: ширины теперь свои у каждой страницы, поэтому
  // внешним стал перебор страниц. Переходов между страницами столько же.
  for (const page of pages) {
    for (const [width, height] of sizesOf(page)) {
      await command('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: false });
      // Запрос без метки времени браузер обслуживает из памяти, и замер
      // показывает вычисленные стили от прошлой сборки CSS — правка выглядит
      // «не сработавшей». Ловушка стоила двух циклов, поэтому она здесь.
      await command('Page.navigate', { url: `${base}/hub-${page}.html?v=${Date.now()}` });
      await new Promise(resolve => setTimeout(resolve, 280));
      const value = await evaluate(geometry(rootOf(page)));
      checks += 1;
      if (!value) { problems.push(`${width} ${page}: страница не отдала замер`); continue; }
      // Страница открылась, но корня на ней нет — мерить нечего. Без этой
      // строки пустая страница проходит как «находок нет».
      if (!value.rooted) { problems.push(`${width} ${page}: на странице нет корня ${rootOf(page)} — мерить нечего (страница не собрана или сервер её не отдал)`); continue; }
      // Прокрутка страницы и вылет элемента — разные симптомы: элемент может
      // уехать за край, а документ этого не показать, если ему обрезали overflow.
      if (value.overflow > 1) problems.push(`${width} ${page}: страница прокручивается вбок на ${value.overflow}px`);
      if (value.spill.length) problems.push(`${width} ${page}: за правым краем — ${value.spill.join(' | ')}`);
      if (!splitLayouts.has(page) && value.crumb != null && value.content != null
          && Math.abs(value.crumb - value.content) > 2) {
        problems.push(`${width} ${page}: левый край крошки ${value.crumb}, содержимого ${value.content}`);
      }
      // Контраст от ширины не зависит, поэтому считается один раз на страницу.
      if (width === sizesOf(page)[0][0]) {
        for (const row of await evaluate(contrast(rootOf(page))) || []) {
          const line = `${page}: контраст ${row.ratio} при пороге ${row.need} — «${row.text}» (${row.selector})`;
          if (row.inactive) dimmed.push(line); else problems.push(line);
        }
      }
      // Повторы от ширины не зависят — считаются один раз на страницу.
      if (width === sizesOf(page)[0][0]) {
        for (const row of await evaluate(duplicateProbe(rootOf(page))) || []) {
          repeats.push({ page, text: row.text, count: row.count, where: row.where });
        }
      }
      // Размер цели от ширины окна зависит — узкое окно сжимает ряды кнопок, —
      // поэтому меряется на каждой ширине, в отличие от контраста.
      for (const row of await evaluate(targetProbe(rootOf(page))) || []) {
        if (acceptedTarget(page, row)) continue;
        problems.push(`${width} ${page}: цель ${row.size} — «${row.text}» (${row.selector}${row.viaLabel ? ', считано по label' : ''})`);
      }
    }
  }
} finally {
  socket.close();
}

// Поверхности со своими размерами перечисляются явно: ширины Чертога к ним не
// относятся, и без этой строки непонятно, на чём именно они мерились.
const ownSizes = surfaces.filter(item => item.sizes)
  .map(item => `${item.page} ${item.sizes.map(([w, h]) => `${w}×${h}`).join('/')}`);
console.log(`замеров: ${checks} · страниц: ${pages.length} · Чертог: ${widths.join(', ')}`
  + (ownSizes.length ? `
  свои размеры: ${ownSizes.join(' · ')}` : ''));
const staleAccepted = acceptedList.filter((item, index) => !usedAccepted.has(index));
if (staleAccepted.length) {
  console.log(`принятые отступления без находки — ${staleAccepted.length}: строку пора убрать`);
  for (const item of staleAccepted) console.log(`  ${item.page} · ${item.selector} · ${item.size}`);
}
if (acceptedList.length > staleAccepted.length) {
  console.log(`принятых отступлений: ${acceptedList.length - staleAccepted.length}`);
}
if (problems.length) {
  console.error(`\nвёрстка Хаба разъехалась в ${problems.length} местах:`);
  for (const line of problems) console.error('  ' + line);
  process.exitCode = 1;
} else {
  console.log('переполнений нет, левый край общий, надписи читаются, цели не меньше 24px'
    + (acceptedList.length > staleAccepted.length ? ' — кроме принятых выше' : ''));
}
if (dimmed.length) {
  console.log(`\nприглушённые контролы — ${dimmed.length} подписей ниже порога (WCAG 1.4.3 их не требует, но узнаваемость страдает):`);
  for (const line of dimmed) console.log('  ' + line);
}
// Разобранные повторы держатся файлом, а не памятью: пять циклов подряд список
// печатался целиком, и объяснения давались заново в журнале. Одна пара так и
// простояла в «объяснимых», не будучи объяснённой, и оказалась настоящим
// дефектом (цикл 29). Стенд печатает только неразобранные.
const explained = JSON.parse(readFileSync(new URL('./lib/hub-duplicate-labels.json', import.meta.url), 'utf8')).explained;
const explainedKeys = new Set(explained.map(item => `${item.page} ${item.label}`));
const known = repeats.filter(item => explainedKeys.has(`${item.page} ${item.text}`));
const fresh = repeats.filter(item => !explainedKeys.has(`${item.page} ${item.text}`));
const knownWord = known.length === 1 ? 'разобран' : 'разобраны';
// «1 неразобранных» читается как опечатка и подрывает доверие к остальному
// выводу — форма склоняется по последней цифре, кроме 11–14.
const freshWord = count => {
  const tail = count % 100;
  return tail !== 11 && count % 10 === 1 ? 'неразобранный' : 'неразобранных';
};
if (fresh.length) {
  console.log(`
повторы надписей — ${fresh.length} ${freshWord(fresh.length)}, ${known.length} ${knownWord} (scripts/lib/hub-duplicate-labels.json):`);
  for (const item of fresh) console.log(`  ${item.page}: «${item.text}» — ${item.count}× (${item.where})`);
  console.log('  → разберите каждый: либо это дефект, либо причина идёт в hub-duplicate-labels.json');
} else {
  console.log(`повторы надписей: новых нет · ${known.length} ${knownWord}`);
}
// Запись, которой больше не соответствует ни один повтор, объясняет то, чего
// нет: чаще всего повтор снят правкой, и строка осталась висеть.
const seen = new Set(repeats.map(item => `${item.page} ${item.text}`));
const stale = explained.filter(item => !seen.has(`${item.page} ${item.label}`));
if (stale.length) {
  console.log(`
разобранные записи без повтора — ${stale.length}: повтор снят, строку пора убрать:`);
  for (const item of stale) console.log(`  ${item.page}: «${item.label}»`);
}
