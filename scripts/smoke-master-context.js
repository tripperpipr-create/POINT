// Вложения Мастера: что приложено, сколько это весит и когда приложить нельзя.
//
// До этой проверки вложения не покрывал ни один смоук — ни приём контекста, ни
// пределы, ни снятие, ни очистка после хода. Цена известна поимённо: «@» в поле
// клал вложение в чужой мешок и оно не уходило с репликой; после хода на экране
// оставались чипы уже снятых файлов; про предел в четыре изображения интерфейс
// не знал вовсе и узнавал о нём отказом ядра после нажатия «Отправить».
//
// Проверяется поведением на собранном бандле: вложения живут в состоянии
// модуля, и увидеть их можно только через разметку, которую он рисует.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

function open() {
  const listeners = {}
  const posted = []
  const field = { id: 'master-input', value: '', rows: 1, focus() {}, setSelectionRange() {}, closest: () => null, matches: () => false }
  const nodes = { '#master-input': field }
  const root = {
    innerHTML: '',
    addEventListener(type, callback) { listeners[`root:${type}`] = callback },
    querySelector: selector => nodes[selector] || null,
    querySelectorAll: () => [],
  }
  vm.runInNewContext(main, {
    acquireVsCodeApi: () => ({ postMessage(message) { posted.push(message) }, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
    window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
    console, Date, Map, Set, TextEncoder,
    requestAnimationFrame(callback) { callback(); return 0 },
    cancelAnimationFrame() {}, setTimeout(callback) { callback(); return 0 }, clearTimeout() {},
  }, { filename: 'main.js' })

  listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w', selectedTab: 'master',
    boot: {
      onboarded: true, profiles: [], projectAgents: [], usageRecords: [],
      runs: [], quests: [], executions: [], changeSets: [], questProposals: [],
      orchestrator: { id: 'o1', preset: 'conductor', model: 'qwen' },
    },
  } })
  const master = extra => ({ configured: true, config: { model: 'qwen' }, contextBudgetChars: 12000, history: [], sessions: { active: 'pay', items: [{ id: 'pay', title: 'Оплата' }] }, ...extra })
  listeners['window:message']({ data: { type: 'master', master: master() } })

  const attach = contexts => listeners['window:message']({ data: { type: 'masterContext', conversationId: 'pay', contexts } })
  // Цель отвечает на closest сама собой: доставка ищет ближайшее действие, а
  // обработчик снятия — ещё и чип вокруг кнопки. Подставь на второй вопрос
  // объект без closest — и он падает на первом же нажатии.
  const click = dataset => {
    const target = { dataset, closest: selector => (selector === '[data-action]' ? target : null) }
    listeners['root:click']({ target, preventDefault() {} })
  }
  const type = value => { field.value = value; listeners['root:input']({ target: field }) }
  // Клавиши списка перехватываются раньше отправки по Enter, и проверять их надо
  // тем же обработчиком, что и в продукте.
  const key = name => listeners['root:keydown']({
    key: name, code: name, shiftKey: false, isComposing: false,
    target: { id: 'master-input', value: field.value, tagName: 'TEXTAREA', closest: () => null },
    preventDefault() {}, ctrlKey: false, metaKey: false, altKey: false,
  })
  return { listeners, posted, root, field, attach, click, type, key, master }
}

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${String(detail).slice(0, 260)}`)
}
const chips = html => (html.match(/hall-context-file/g) || []).length
const note = html => (html.match(/hall-compose-note[^>]*>([^<]*)</) || [, ''])[1]

// ── Приложенный файл виден, назван и взвешен ───────────────────────────────
{
  const ui = open()
  ui.attach([{ name: 'internal/app/payments.go', kind: 'file', content: 'package app\n'.repeat(200) }])
  const html = ui.root.innerHTML
  check('вложение появилось в ряду', chips(html) === 1,
    `чипов ${chips(html)} вместо одного — приложенный файл не виден`)
  check('путь отделён от имени',
    html.includes('<em>internal/app/</em>payments.go'),
    'путь и имя слиты в одну серую строку — не читается ни то, ни другое')
  check('вес вложения назван',
    /<small>2,3 КБ<\/small>/.test(html),
    'сколько весит вложение, не сказано: правка на три строки и файл на сорок килобайт выглядят одинаково')
  ui.click({ action: 'master-context-remove', session: 'pay', id: 'нет-такого' })
  check('чужой идентификатор ничего не снимает', chips(ui.root.innerHTML) === 1,
    'снялось вложение, которого не просили')
}

// ── Картинка показывается картинкой ───────────────────────────────────────
{
  const ui = open()
  ui.attach([{ name: 'shot.png', kind: 'image', mime: 'image/png', content: 'iVBORw0KGgo=' }])
  const html = ui.root.innerHTML
  check('у картинки есть миниатюра',
    html.includes('hall-context-thumb') && html.includes('src="data:image/png;base64,iVBORw0KGgo="'),
    'имя снимка о содержимом не говорит ничего, а миниатюры нет')

  const dirty = open()
  dirty.attach([{ name: 'shot.png', kind: 'image', mime: 'image/svg+xml', content: 'PHN2Zz4=' }])
  check('чужой тип картинки в адрес data: не попадает',
    !dirty.root.innerHTML.includes('hall-context-thumb'),
    'в src="data:" уехал тип, которого ядро не принимает')
}

// ── Пределы названы до отправки, а не после ───────────────────────────────
{
  const ui = open()
  const image = index => ({ name: `shot-${index}.png`, kind: 'image', mime: 'image/png', content: 'iVBORw0KGgo=' })
  ui.attach([image(1), image(2), image(3), image(4)])
  check('четыре картинки принимаются', chips(ui.root.innerHTML) === 4,
    'предел ядра — четыре изображения, а приняли меньше')
  ui.attach([image(5)])
  check('пятая картинка отклонена на месте',
    chips(ui.root.innerHTML) === 4 && note(ui.root.innerHTML).includes('изображени'),
    `пятая картинка уехала бы в ход и вернулась отказом ядра: ${note(ui.root.innerHTML)}`)
  check('предел картинок виден в счётчике',
    /картинки 4 \/ 4/.test(ui.root.innerHTML),
    'счётчик молчит о том, что картинок больше не примут')
}

{
  const ui = open()
  // Ядро присылает свой бюджет знаков; счётчик обязан показывать меньший из
  // двух — расширение режет набор строже.
  ui.attach([{ name: 'big.log', kind: 'file', content: 'x'.repeat(9000) }])
  check('счётчик появляется, когда запас кончается',
    /hall-context-budget/.test(ui.root.innerHTML) && ui.root.innerHTML.includes('9000 / 12000'),
    'запас почти кончился, а счётчика нет — отказ придёт уже после нажатия')
  ui.attach([{ name: 'more.log', kind: 'file', content: 'x'.repeat(5000) }])
  check('перебор бюджета отмечен',
    /hall-context-budget[^"]*is-over/.test(ui.root.innerHTML),
    'бюджет превышен, а счётчик молчит')
}

// ── «@» открывает список файлов прямо под кареткой ────────────────────────
//
// Раньше «@» срабатывало только в конце строки и открывало нативное окно поверх
// редактора, а вложение из него падало в чужой мешок: запрос уходил без
// разговора. Теперь это список в самой карточке.
{
  const ui = open()
  ui.type('Посмотри @web')
  const ask = ui.posted.filter(message => message.type === 'searchMasterContext').at(-1)
  check('«@» спросил файлы и назвал разговор',
    ask?.query === 'web' && ask?.conversationId === 'pay',
    `запрос файлов ушёл не тем (${JSON.stringify(ask)})`)
  check('список открылся в карточке',
    ui.root.innerHTML.includes('hall-mention'),
    'список не нарисован — «@» по-прежнему ведёт в окно поверх редактора')

  ui.listeners['window:message']({ data: { type: 'masterContextSuggestions', conversationId: 'pay', query: 'web', items: [
    { path: 'internal/billing/webhook.go', open: true },
    { path: 'internal/billing/webhook_test.go', open: false },
  ] } })
  const listed = ui.root.innerHTML
  check('строки списка показаны',
    (listed.match(/hall-mention-row/g) || []).length === 2 && listed.includes('webhook_test.go'),
    'подсказки пришли, а в списке их нет')
  check('открытый файл отмечен',
    /hall-mention-row[^>]*>[\s\S]{0,160}?<small>открыт<\/small>/.test(listed),
    'чаще всего прикладывают то, что и так открыто, — а по списку этого не видно')
  check('первая строка выбрана',
    /hall-mention-row is-active/.test(listed),
    'ни одна строка не выбрана — Enter не будет знать, что прикладывать')

  // Стрелка ведёт по списку, Enter прикладывает выбранное.
  ui.key('ArrowDown')
  ui.key('Enter')
  const attach = ui.posted.filter(message => message.type === 'attachMasterContextPath').at(-1)
  check('Enter приложил выбранный файл',
    attach?.path === 'internal/billing/webhook_test.go' && attach?.conversationId === 'pay',
    `Enter ушёл не туда: ${JSON.stringify(attach)}`)
  check('список закрылся после выбора',
    !ui.root.innerHTML.includes('hall-mention-row'),
    'список остался открытым поверх поля')
  check('«собачка» с запросом убрана из черновика',
    /<textarea[^>]*id="master-input"[^>]*>Посмотри <\/textarea>/.test(ui.root.innerHTML),
    'знак «@» и запрос остались в реплике и уедут в ядро вместе с ней')

  ui.attach([{ name: 'internal/billing/webhook_test.go', kind: 'file', content: 'package billing' }])
  check('приложенное по «@» встало в ряд', chips(ui.root.innerHTML) === 1,
    'файл выбран, а в композере его нет')
}

// ── Escape закрывает список, не трогая набранное ──────────────────────────
{
  const ui = open()
  ui.type('Посмотри @web')
  ui.listeners['window:message']({ data: { type: 'masterContextSuggestions', conversationId: 'pay', query: 'web', items: [{ path: 'a.go', open: false }] } })
  ui.key('Escape')
  check('Escape закрыл список',
    !ui.root.innerHTML.includes('hall-mention'),
    'список не закрывается — из него нет выхода без выбора')
  check('набранное после Escape цело',
    /<textarea[^>]*id="master-input"[^>]*>Посмотри @web<\/textarea>/.test(ui.root.innerHTML),
    'отказ от выбора стёр набранное')
}

// ── После хода не остаётся ни одного чипа ─────────────────────────────────
{
  const ui = open()
  ui.attach([
    { name: 'a.go', kind: 'file', content: 'package a' },
    { name: 'b.go', kind: 'file', content: 'package b' },
    { name: 'c.go', kind: 'file', content: 'package c' },
  ])
  check('приложены все три', chips(ui.root.innerHTML) === 3, 'приняты не все вложения')
  ui.listeners['window:message']({ data: { type: 'master', turnFinished: true, master: ui.master({
    history: [{ id: 'u1', role: 'user', content: 'Собери' }, { id: 'a1', role: 'assistant', content: 'Собрал.' }],
  }) } })
  check('после хода ряд вложений пуст', chips(ui.root.innerHTML) === 0,
    'на экране остались чипы уже снятых вложений — они не уйдут со следующей репликой, но выглядят приложенными')
}

if (failures.length) {
  console.error('\nВЛОЖЕНИЯ ПОТЕРЯЛИСЬ:\n  ' + failures.join('\n  '))
  process.exitCode = 1
} else {
  console.log('\nвложения Мастера: видны, взвешены, пределы названы до отправки: PASS')
}
