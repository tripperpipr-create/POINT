// Разговор с Мастером не теряет набранное и не запирается насмерть.
//
// Поле ввода пересобирается из состояния на каждой отрисовке, а отрисовку
// вызывает и чужое: ответ ядра, обновление очереди, приход состояния мира. Пока
// набранное не попадало в это состояние, любой такой ответ посреди набора стирал
// описание задачи — молча и целиком.
//
// Отправка запирает поле и кнопку до ответа ядра — иначе один вопрос ушёл бы
// дважды. Ответа при отказе не будет, и без снятия замка разговор вставал
// навсегда: «Думает…» висело, писать было нечем, а разморозить это могло только
// переоткрытие панели. Реплика при этом обязана остаться в поле: набирать её
// заново из-за чужого сбоя — плата, которой человек не заказывал.
//
// Второй тупик того же рода: не загрузилась переписка. Состояние отказа сделано
// тупиковым намеренно — автоповтор превратил бы постоянный отказ в бесконечный
// цикл запросов, — но выход из него обязан быть под рукой, а не в переоткрытии
// панели.
//
// Проверяем поведением: шлём, роняем запрос, смотрим, можно ли продолжать.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

// Каждый случай — свой чистый мир: состояние раздела живёт в модуле.
function open({ input = '' } = {}) {
  const listeners = {}
  const posted = []
  const field = { value: input, focus() {}, setSelectionRange() {} }
  const root = {
    innerHTML: '',
    addEventListener(type, callback) { listeners[`root:${type}`] = callback },
    querySelector: selector => (selector === '#master-input' ? field : null),
    querySelectorAll: () => [],
  }
  vm.runInNewContext(main, {
    acquireVsCodeApi: () => ({ postMessage(message) { posted.push(message) }, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
    window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
    console, Date, Map, Set,
    requestAnimationFrame(callback) { callback(); return 0 },
    cancelAnimationFrame() {}, setTimeout(callback) { callback(); return 0 }, clearTimeout() {},
  }, { filename: 'main.js' })

  listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w',
    selectedTab: 'master',
    boot: {
      onboarded: true, profiles: [], projectAgents: [], usageRecords: [],
      runs: [], quests: [], executions: [], changeSets: [], questProposals: [],
      orchestrator: { id: 'o1', preset: 'conductor' },
    },
  } })
  const click = dataset => listeners['root:click']({
    target: { closest: selector => (selector === '[data-action]' ? { dataset } : null) },
    preventDefault() {},
  })
  const type = value => {
    field.value = value
    listeners['root:input']({ target: { id: 'master-input', value, closest: () => null, matches: () => false } })
  }
  const enter = () => listeners['root:keydown']({
    key: 'Enter', code: 'Enter', shiftKey: false, isComposing: false,
    target: { id: 'master-input', value: field.value, tagName: 'TEXTAREA', closest: () => null },
    preventDefault() {}, ctrlKey: false, metaKey: false, altKey: false,
  })
  return { listeners, posted, root, click, type, enter }
}

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${String(detail).slice(0, 200)}`)
}

// ── Отправка, на которую ядро не ответило ─────────────────────────────────
{
  const ui = open({ input: 'Почини флаки-тест оплаты' })
  ui.listeners['window:message']({ data: { type: 'master', master: {
    configured: true, config: { model: 'qwen' }, history: [],
  } } })
  ui.click({ action: 'master-send' })

  check('реплика ушла в расширение',
    ui.posted.some(message => message.type === 'masterChat' && message.message === 'Почини флаки-тест оплаты'),
    'сообщения masterChat нет среди отправленных')
  // Предохранитель от двойной отправки переехал с поля на кнопку: поле во время
  // хода открыто, потому что мысль, пришедшую, пока модель думает, надо где-то
  // записать. Заперта именно кнопка отправки, а не «где-то в разметке есть
  // disabled» — и именно настоящий атрибут, а не подстрока внутри aria-disabled.
  const fieldLocked = html => /<textarea[^>]*id="master-input"[^>]*\sdisabled/.test(html)
  const sendLocked = html => /<button[^>]*hall-compose-send[^>]*\sdisabled/.test(html)
  check('до ответа заперта кнопка, а не поле',
    ui.root.innerHTML.includes('Думаю…') && sendLocked(ui.root.innerHTML) && !fieldLocked(ui.root.innerHTML),
    'ход идёт, а отправка не заперта или поле заперто — либо реплика уйдёт дважды, либо записать следующую некуда')
  check('во время хода сказано, что набранное уйдёт в очередь',
    ui.root.innerHTML.includes('в очередь'),
    'поле открыто и молчит о судьбе набранного')

  ui.listeners['window:message']({ data: {
    type: 'error', request: 'masterChat', message: 'ядро не ответило',
  } })

  check('после отказа отправка отперта',
    !sendLocked(ui.root.innerHTML) && !fieldLocked(ui.root.innerHTML),
    'поле или кнопка остались запертыми — разговор встал навсегда')
  check('ожидание убрано',
    !ui.root.innerHTML.includes('Думаю…'),
    'мастер продолжает «думать» над запросом, которого больше нет')
  check('реплика осталась в поле',
    ui.root.innerHTML.includes('Почини флаки-тест оплаты'),
    'набранное пропало — придётся печатать заново')
  check('причина отказа названа',
    ui.root.innerHTML.includes('ядро не ответило'),
    'отказ не показан человеку')
}

// ── Переписка, которая не загрузилась ─────────────────────────────────────
{
  const ui = open()
  // Раздел сам запрашивает переписку при открытии.
  check('переписка запрошена',
    ui.posted.some(message => message.type === 'loadMaster'),
    'раздел не спросил ядро о переписке')
  ui.listeners['window:message']({ data: {
    type: 'error', request: 'loadMaster', message: 'ядро не ответило',
  } })

  check('пустота не выдана за отсутствие разговора',
    ui.root.innerHTML.includes('Переписка не загрузилась'),
    'после отказа раздел молчит или врёт про пустой диалог')
  check('выход из отказа под рукой',
    ui.root.innerHTML.includes('retry-master'),
    'повторить нечем — только переоткрывать панель')

  const before = ui.posted.filter(message => message.type === 'loadMaster').length
  ui.click({ action: 'retry-master' })
  check('повтор спрашивает ядро заново',
    ui.posted.filter(message => message.type === 'loadMaster').length > before,
    'кнопка есть, а запроса нет')
}

// ── Набор, которому помешал ответ ядра ────────────────────────────────────
{
  const ui = open()
  ui.listeners['window:message']({ data: { type: 'master', master: {
    configured: true, config: { model: 'qwen' }, history: [],
  } } })

  // Человек печатает: поле сообщает об этом тем же событием, что и в вебвью.
  ui.type('Добавь метрики в вебхук биллинга')

  // Пока он думает, приходит обычное обновление состояния — отрисовка целиком.
  ui.listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w',
    selectedTab: 'master',
    boot: {
      onboarded: true, profiles: [], projectAgents: [], usageRecords: [],
      runs: [], quests: [], executions: [], changeSets: [], questProposals: [],
      orchestrator: { id: 'o1', preset: 'conductor' },
      indexStatus: { state: 'ready', files: 12 },
    },
  } })

  check('набранное пережило чужую отрисовку',
    ui.root.innerHTML.includes('Добавь метрики в вебхук биллинга'),
    'текст пропал из поля — придётся набирать заново')

  // Enter отправляет, как и у компаньона: иначе единственный способ — мышь.
  ui.enter()
  check('Enter отправляет реплику',
    ui.posted.some(message => message.type === 'masterChat' && message.message === 'Добавь метрики в вебхук биллинга'),
    'нажатие Enter не отправило ничего')
}

if (failures.length) {
  console.log('РАЗГОВОР С МАСТЕРОМ ЗАПЕРСЯ:')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('разговор с Мастером держит набранное и переживает отказ ядра: PASS')
