// Хаб на кривых данных.
//
// Все мои фикстуры были аккуратными: у квеста есть отряд, у исполнения — агент,
// у набора — файлы. Настоящее состояние таким не бывает: поля пропадают, ссылки
// указывают в никуда, списки приходят null. Проверяем, что интерфейс не падает
// и не показывает бессмыслицу.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

const cases = {
  'квест без полей': {
    quests: [{ id: 'q-x' }],
    executions: null,
    changeSets: undefined,
  },
  'квест со ссылкой на несуществующего агента': {
    quests: [{ id: 'q-y', title: 'Задача', status: 'active', teamAgentIds: ['нет-такого'] }],
    executions: [{ id: 'ex-y', questId: 'q-y', projectAgentId: 'призрак', status: 'running' }],
    changeSets: [{ id: 'cs-y', questId: 'q-y', status: 'pending' }],
  },
  'пустые списки': { quests: [], executions: [], changeSets: [] },
  'null вместо списков': { quests: null, executions: null, changeSets: null },
  'решение без resolve': {
    quests: [], executions: [], changeSets: [],
    decisions: { items: [{ id: 'd1', kind: 'approval', title: 'Без решения', risk: 'HIGH' }], total: 1 },
  },
}

let failures = 0
for (const [name, extra] of Object.entries(cases)) {
  const listeners = {}
  const posted = []
  const root = {
    innerHTML: '',
    addEventListener(type, callback) { listeners[`root:${type}`] = callback },
    querySelector() { return null },
    querySelectorAll() { return [] },
  }
  const context = {
    acquireVsCodeApi: () => ({ postMessage(m) { posted.push(m) }, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
    window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
    console, Date, Map, Set,
    requestAnimationFrame(cb) { cb(); return 0 },
    cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
  }
  try {
    vm.runInNewContext(main, context, { filename: 'main.js' })
  } catch (error) {
    console.log(`${name}: ЗАГРУЗКА УПАЛА — ${error.message}`)
    failures += 1
    continue
  }

  const boot = { onboarded: true, profiles: [], projectAgents: [], runs: [], usageRecords: [], ...extra }
  // Список — все вкладки рейки Чертога, а не выборка. Арсенал в него не входил,
  // и падение отрисовки на проекте без своих инструментов никто не ловил.
  const tabs = ['overview', 'master', 'quests', 'quest', 'decisions', 'changesets', 'changes',
    'journal', 'filehistory', 'flows', 'workflows', 'history', 'agents', 'teams', 'skills',
    'memory', 'connections', 'databases', 'tools', 'statistics', 'docker', 'onboarding']
  for (const tab of tabs) {
    try {
      listeners['window:message']({ data: {
        type: 'state', service: { state: 'running' }, workspaceTrusted: true,
        workspace: 'w', selectedTab: tab, boot, details: undefined,
      } })
      if (extra.decisions) {
        listeners['window:message']({ data: { type: 'decisions', decisions: extra.decisions } })
      }
      if (!root.innerHTML.length) throw new Error('пустая разметка')
      if (tab === 'quests' && (extra.quests || []).length && !root.innerHTML.includes('hall-quest-row')) {
        throw new Error('список квестов не отрисовался — проверка прошла бы вхолостую')
      }
      // Пропущенная дата даёт «Invalid Date», а пустая (null) — «01.01.1970»:
      // второе хуже первого, потому что выглядит настоящим временем. Ни то, ни
      // другое человек видеть не должен — в разметке ожидается прочерк.
      if (/undefined|NaN|\[object Object\]|Invalid Date|01\.01\.1970/.test(root.innerHTML)) {
        throw new Error('в разметке видно ' + (root.innerHTML.match(/undefined|NaN|\[object Object\]|Invalid Date|01\.01\.1970/) || [])[0])
      }
    } catch (error) {
      console.log(`${name} / ${tab}: ${error.message}`)
      failures += 1
    }
  }
  // Раскрытие квеста — путь, который я добавил последним.
  try {
    const quest = (extra.quests || [])[0]
    if (quest) {
      listeners['root:click']({ target: { closest(selector) {
        if (selector === '[data-example]') return null
        if (selector === '[data-action]') return { dataset: { action: 'toggle-quest', id: quest.id } }
        return null
      } } })
    }
  } catch (error) {
    console.log(`${name} / раскрытие квеста: ${error.message}`)
    failures += 1
  }
}

// ── Пустой Хаб ведёт дальше, а не молчит ──────────────────────────────────
// Разбор состояния (hubSituation) вычислялся, но использовался только когда
// что-то уже ждало решения. В спокойном случае показывалось «ВСЁ СПОКОЙНО», и
// подсказка «сначала агент, потом квест» не доходила до экрана именно тогда,
// когда она нужнее всего — на первом экране нового проекта.
{
  const listeners = {}
  const root = {
    innerHTML: '',
    addEventListener(type, callback) { listeners[`root:${type}`] = callback },
    querySelector() { return null },
    querySelectorAll() { return [] },
  }
  const context = {
    acquireVsCodeApi: () => ({ postMessage() {}, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
    window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
    console, Date, Map, Set,
    requestAnimationFrame(cb) { cb(); return 0 },
    cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
  }
  vm.runInNewContext(main, context, { filename: 'main.js' })
  listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'новый',
    selectedTab: 'overview',
    boot: {
      onboarded: true, profiles: [], projectAgents: [], quests: [], changeSets: [],
      executions: [], runs: [], usageRecords: [],
      companion: { id: 'c1', preset: 'balanced', provider: 'ollama', model: 'q' },
      orchestrator: { id: 'o1', preset: 'conductor', provider: 'ollama', model: 'q' },
      indexStatus: { state: 'not_built', files: 0 },
    }, details: undefined,
  } })
  if (!root.innerHTML.includes('Соберите гильдию')) {
    throw new Error('Empty Hub does not tell the newcomer what to do first')
  }
  if (root.innerHTML.includes('ВСЁ СПОКОЙНО')) {
    throw new Error('Empty Hub claims calm while nothing can happen without an agent')
  }
  // Состояние индекса называется по-русски: набор состояний закрыт ядром.
  if (root.innerHTML.includes('not_built')) {
    throw new Error('Raw index state leaked to the screen')
  }
}


// ── Сломанное окружение объясняет себя ────────────────────────────────────
// Ядро остановлено, ядро упало, папка не доверена — это и есть «ничего не
// работает» с точки зрения человека. Экран обязан назвать причину и дать
// действие, а не показать пустоту или сырое состояние процесса.
{
  const broken = [
    { имя: 'ядро остановлено', world: { service: { state: 'stopped' }, workspaceTrusted: true },
      ждём: ['Гильдия отдыхает', 'Пробудить ядро'] },
    { имя: 'ядро упало', world: { service: { state: 'error', error: 'exit status 1' }, workspaceTrusted: true },
      ждём: ['Ядро не поднялось', 'Повторить запуск', 'Хроника ядра'] },
    { имя: 'папка не доверена', world: { service: { state: 'running' }, workspaceTrusted: false },
      ждём: ['БЕЗОПАСНЫЙ РЕЖИМ', 'Настроить доступ'] },
  ]
  for (const кейс of broken) {
    const listeners = {}
    const root = {
      innerHTML: '',
      addEventListener(type, callback) { listeners[`root:${type}`] = callback },
      querySelector() { return null },
      querySelectorAll() { return [] },
    }
    const context = {
      acquireVsCodeApi: () => ({ postMessage() {}, getState() {}, setState() {} }),
      document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
      window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
      console, Date, Map, Set,
      requestAnimationFrame(cb) { cb(); return 0 },
      cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
    }
    vm.runInNewContext(main, context, { filename: 'main.js' })
    listeners['window:message']({ data: {
      type: 'state', ...кейс.world, workspace: 'проект', selectedTab: 'overview',
      boot: { onboarded: true, profiles: [], projectAgents: [], runs: [], usageRecords: [] },
      details: undefined,
    } })
    for (const нужно of кейс.ждём) {
      if (!root.innerHTML.includes(нужно)) {
        throw new Error(`Broken environment "${кейс.имя}" does not say «${нужно}»`)
      }
    }
    // Состояние процесса не показывается человеку как есть.
    const text = root.innerHTML.replace(/<[^>]*>/g, ' ')
    for (const сырое of ['exit status', 'stopped', 'starting']) {
      if (text.includes(сырое)) {
        throw new Error(`Broken environment "${кейс.имя}" leaked raw state «${сырое}»`)
      }
    }
  }
}


// ── Кнопка «Связь» объясняет, почему нельзя ───────────────────────────────
// Связь соединяет два узла. При одном кнопка оставалась живой, а клик уходил в
// пустоту: обработчик выходил молча. «Нажал — ничего не произошло» читается как
// поломка, а не как «пока нельзя».
{
  const flowWith = nodes => {
    const listeners = {}
    const root = {
      innerHTML: '',
      addEventListener(type, callback) { listeners[`root:${type}`] = callback },
      querySelector() { return null },
      querySelectorAll() { return [] },
    }
    const context = {
      acquireVsCodeApi: () => ({ postMessage() {}, getState() {}, setState() {} }),
      document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
      window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
      console, Date, Map, Set,
      requestAnimationFrame(cb) { cb(); return 0 },
      cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
    }
    vm.runInNewContext(main, context, { filename: 'main.js' })
    listeners['window:message']({ data: {
      type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w',
      selectedTab: 'flows',
      boot: {
        onboarded: true, profiles: [], projectAgents: [], runs: [], usageRecords: [], flowRuns: [],
        flows: [{ id: 'fl-1', name: 'Тест', description: '', nodes, edges: [] }],
      }, details: undefined,
    } })
    return {
      button: (root.innerHTML.match(/<button[^>]*data-action="add-flow-edge"[^>]*>/) || [''])[0],
      html: root.innerHTML,
    }
  }

  const single = flowWith([{ id: 'n1', kind: 'agent', name: 'A' }])
  if (single.button || !single.html.includes('Запуск идёт через карточку наряда')) {
    throw new Error('Hidden legacy Flow editor leaked an edge action instead of the Hub v2 redirect')
  }
  const pair = flowWith([{ id: 'n1', kind: 'agent', name: 'A' }, { id: 'n2', kind: 'agent', name: 'B' }])
  if (pair.button || !pair.html.includes('Открыть Мастера')) {
    throw new Error('Legacy Flow editor returned when enough nodes were present')
  }
}


// ── Пустая задача объясняется одинаково обоими способами запуска ──────────
// Обычный запуск показывал баннер, запуск через Cursor выходил молча: одна и та
// же ошибка на одном экране вела себя двумя разными способами.
{
  const listeners = {}
  const posted = []
  const root = {
    innerHTML: '',
    addEventListener(type, callback) { listeners[`root:${type}`] = callback },
    querySelector() { return null },
    querySelectorAll() { return [] },
  }
  const context = {
    acquireVsCodeApi: () => ({ postMessage(m) { posted.push(m) }, getState() {}, setState() {} }),
    document: {
      getElementById: id => (id === 'root' ? root : undefined),
      body: { dataset: { layout: 'wide' } },
      addEventListener() {},
    },
    window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
    console, Date, Map, Set,
    requestAnimationFrame(cb) { cb(); return 0 },
    cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
  }
  vm.runInNewContext(main, context, { filename: 'main.js' })
  const agent = { id: 'a1', name: 'A', provider: 'cursor-cli', model: 'auto', allowedTools: ['read_file'], maxSteps: 20 }
  listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w',
    selectedTab: 'quests',
    boot: { onboarded: true, profiles: [agent], projectAgents: [agent], runs: [], usageRecords: [] },
    details: undefined,
  } })
  listeners['root:click']({ target: { closest(selector) {
    if (selector === '[data-example]') return null
    if (selector === '[data-action]') return { dataset: { action: 'launch-cursor' } }
    return null
  } } })
  if (!root.innerHTML.includes('Сформулируйте задачу')) {
    throw new Error('Cursor launch with an empty task says nothing')
  }
  if (posted.some(message => /^(startCursorRun|launchCursorAgent|startRun)$/.test(message.type || ''))) {
    throw new Error('Empty task must not start a quest')
  }
}


// ── Число на рейке и содержимое раздела сходятся ──────────────────────────
// Значок «КВЕСТЫ» считал активные исполнения, а раздел перечисляет квесты: два
// квеста давали пустой значок, а запущенное исполнение без записи квеста —
// значок «1» над пустым экраном. Число, противоречащее экрану, подрывает
// доверие к обоим.
{
  const questsSection = boot => {
    const listeners = {}
    const root = {
      innerHTML: '',
      addEventListener(type, callback) { listeners[`root:${type}`] = callback },
      querySelector() { return null },
      querySelectorAll() { return [] },
    }
    const context = {
      acquireVsCodeApi: () => ({ postMessage() {}, getState() {}, setState() {} }),
      document: {
        getElementById: id => (id === 'root' ? root : undefined),
        body: { dataset: { layout: 'wide' } },
        addEventListener() {},
      },
      window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
      console, Date, Map, Set,
      requestAnimationFrame(cb) { cb(); return 0 },
      cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
    }
    vm.runInNewContext(main, context, { filename: 'main.js' })
    listeners['window:message']({ data: {
      type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w',
      selectedTab: 'quests',
      boot: { onboarded: true, profiles: [], projectAgents: [], runs: [], usageRecords: [], ...boot },
      details: undefined,
    } })
    const badge = (root.innerHTML.match(/data-tab="quests"[^>]*>[\s\S]*?<b>([^<]*)<\/b>/) || [])[1] || ''
    const rows = (root.innerHTML.match(/hall-quest-row/g) || []).length
    return { badge, rows }
  }

  const listed = questsSection({
    quests: [{ id: 'q1', title: 'A', status: 'active' }, { id: 'q2', title: 'B', status: 'proposed' }],
    executions: [],
  })
  if (listed.badge !== '2' || listed.rows !== 2) {
    throw new Error(`Quests badge disagrees with the list: badge=${listed.badge}, rows=${listed.rows}`)
  }

  const orphan = questsSection({
    quests: [],
    executions: [{ id: 'e1', status: 'running', task: 'Работа без квеста' }],
  })
  if (orphan.badge !== '1' || orphan.rows !== 1) {
    throw new Error(`Badge points at work the section hides: badge=${orphan.badge}, rows=${orphan.rows}`)
  }
}


// ── Заблокированный агент виден с любого экрана ───────────────────────────
// Очередь решений запрашивал только её собственный раздел, а счётчик на рейке
// считался локально и не знал про подтверждения. Агент, ждущий разрешения, не
// отражался нигде: работа стояла молча — ровно то, ради чего очередь и нужна.
{
  const listeners = {}
  const posted = []
  const root = {
    innerHTML: '',
    addEventListener(type, callback) { listeners[`root:${type}`] = callback },
    querySelector() { return null },
    querySelectorAll() { return [] },
  }
  const context = {
    acquireVsCodeApi: () => ({ postMessage(m) { posted.push(m) }, getState() {}, setState() {} }),
    document: {
      getElementById: id => (id === 'root' ? root : undefined),
      body: { dataset: { layout: 'wide' } },
      addEventListener() {},
    },
    window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
    console, Date, Map, Set,
    requestAnimationFrame(cb) { cb(); return 0 },
    cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
  }
  vm.runInNewContext(main, context, { filename: 'main.js' })
  listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w',
    selectedTab: 'overview',
    boot: {
      onboarded: true, profiles: [], projectAgents: [], usageRecords: [], quests: [],
      changeSets: [], executions: [],
      runs: [{ id: 'r1', status: 'waiting_approval', task: 'Запуск команды', startedAt: new Date().toISOString() }],
    }, details: undefined,
  } })

  if (!posted.some(message => message.type === 'loadDecisions')) {
    throw new Error('Decision queue is not requested outside its own section')
  }
  const badge = (root.innerHTML.match(/data-tab="decisions"[^>]*>[\s\S]*?<b>([^<]*)<\/b>/) || [])[1]
  if (badge !== '1') {
    throw new Error(`Blocked agent invisible on the rail: badge=${JSON.stringify(badge)}`)
  }
  // Повторных запросов быть не должно: ответ вызывает отрисовку, отрисовка —
  // снова запрос, и это уже дважды укладывало интерфейс.
  if (posted.filter(message => message.type === 'loadDecisions').length !== 1) {
    throw new Error('Decision queue requested more than once — render loop hazard')
  }
}


// ── Очередь не отстаёт от мира ────────────────────────────────────────────
// Она загружалась один раз, и её число имело приоритет навсегда: агент начинал
// ждать разрешения уже после загрузки, состояние мира об этом знало, а рейка
// показывала прежний ноль. Работа стояла молча — то же, от чего очередь и
// защищает.
{
  const listeners = {}
  const posted = []
  const root = {
    innerHTML: '',
    addEventListener(type, callback) { listeners[`root:${type}`] = callback },
    querySelector() { return null },
    querySelectorAll() { return [] },
  }
  const context = {
    acquireVsCodeApi: () => ({ postMessage(m) { posted.push(m) }, getState() {}, setState() {} }),
    document: {
      getElementById: id => (id === 'root' ? root : undefined),
      body: { dataset: { layout: 'wide' } },
      addEventListener() {},
    },
    window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
    console, Date, Map, Set,
    requestAnimationFrame(cb) { cb(); return 0 },
    cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
  }
  vm.runInNewContext(main, context, { filename: 'main.js' })

  const badge = () => (root.innerHTML.match(/data-tab="decisions"[^>]*>[\s\S]*?<b>([^<]*)<\/b>/) || [])[1]
  const push = runs => listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w',
    selectedTab: 'overview',
    boot: { onboarded: true, profiles: [], projectAgents: [], usageRecords: [], quests: [],
      changeSets: [], executions: [], runs },
    details: undefined,
  } })
  const answer = total => listeners['window:message']({ data: {
    type: 'decisions',
    decisions: { total, items: [], byKind: {}, generatedAt: new Date().toISOString() },
  } })

  push([])
  answer(0)
  if (badge() !== '·') throw new Error('Empty world should show an empty decisions badge')

  // Агент начинает ждать уже после того, как очередь загрузилась.
  push([{ id: 'r1', status: 'waiting_approval', task: 'Команда', startedAt: new Date().toISOString() }])
  if (badge() !== '1') {
    throw new Error(`Queue went stale: a newly blocked agent shows ${JSON.stringify(badge())}`)
  }

  // Согласие сторон не должно порождать поток запросов.
  const before = posted.filter(m => m.type === 'loadDecisions').length
  answer(1)
  for (let i = 0; i < 4; i += 1) push([{ id: 'r1', status: 'waiting_approval', task: 'Команда', startedAt: new Date().toISOString() }])
  const after = posted.filter(m => m.type === 'loadDecisions').length
  if (after > before) throw new Error(`Agreeing states still re-request the queue: ${before} -> ${after}`)
}

if (failures) {
  console.error(`ПРОБЛЕМ: ${failures}`)
  process.exit(1)
}
console.log(JSON.stringify({ degenerateData: 'hub-holds' }))
