// Задание: вкладка в шапке чата и панель справа.
//
// Ядро собирает задание с первой реплики, и карточка с кнопкой «Выполнить
// поручение» вставала в ленту сразу — запертой, пока в задании оставались
// нерешённые вопросы. Здесь проверяется, что до готовности карточки в ленте
// нет, а состав задания доступен вкладкой и панелью; что с готовностью карточка
// возвращается с рабочей кнопкой, а вкладка остаётся; и что редактор брифа
// рисуется в одном месте — иначе readTaskBriefEditor молча читает не тот набор.

const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${String(detail).slice(0, 300)}`)
}

// Узел разметки настолько, насколько его трогает переключение панели.
function node(extra = {}) {
  const classes = new Set()
  const attrs = {}
  return {
    hidden: true,
    classes,
    attrs,
    classList: {
      toggle(name, on) { if (on) classes.add(name); else classes.delete(name) },
      add(name) { classes.add(name) },
      remove(name) { classes.delete(name) },
      contains: name => classes.has(name),
    },
    setAttribute(name, value) { attrs[name] = value },
    getAttribute: name => attrs[name],
    focus() {},
    querySelector: () => null,
    querySelectorAll: () => [],
    insertAdjacentHTML() {},
    ...extra,
  }
}

function open(brief, { history } = {}) {
  const listeners = {}
  const posted = []
  const proposal = { id: 'brief-1', title: 'Развернуть Symfony', status: 'pending', brief }
  const fields = {
    '.hall-dialogue': node(),
    '#master-brief-panel': node(),
    '#master-brief-tab': node(),
  }
  const root = {
    innerHTML: '',
    addEventListener(type, callback) { listeners[`root:${type}`] = callback },
    querySelector: selector => fields[selector] || null,
    querySelectorAll: () => [],
  }
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../vscode-extension/media/main.js'), 'utf8'), {
    acquireVsCodeApi: () => ({ postMessage(message) { posted.push(message) }, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
    window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
    console, Date, Map, Set, structuredClone, CSS: { escape: String }, TextEncoder,
    requestAnimationFrame(callback) { callback(); return 0 },
    cancelAnimationFrame() {}, setTimeout(callback) { callback(); return 0 }, clearTimeout() {},
  }, { filename: 'main.js' })

  const boot = {
    // Ростер не пустой намеренно: готовое задание при пустом ростере — мир, которого
    // ядро не создаёт. Оно само дописывает в такое задание решение «Подготовка
    // исполнителя», и карточка обязана звать готовить исполнителя, а не запускать.
    // Здесь проверяется другое — что готовое задание вообще возвращается в ленту
    // с рабочей кнопкой, — поэтому исполнитель в проекте есть.
    onboarded: true, profiles: [], projectAgents: [{ id: 'pa-1', name: 'Разработчик', provider: 'ollama', baseUrl: 'http://127.0.0.1:11434', primaryModel: 'qwen', maxSteps: 30, allowedTools: ['read_file', 'propose_patch', 'run_command'] }], connections: [], usageRecords: [],
    quests: [], executions: [], runs: [], flows: [], changeSets: [], questProposals: [proposal],
    orchestrator: { id: 'o1', preset: 'conductor', model: 'qwen' },
  }
  const sessions = { items: [{ id: 'c1', title: 'Разговор' }], active: 'c1', mode: 'auto', workMode: 'discuss' }
  const receive = () => listeners['window:message']({ data: { type: 'master', master: {
    configured: true, config: { model: 'qwen' }, sessions,
    history: history || [{ id: 'm1', role: 'assistant', mode: 'model', content: 'Собрал задание.', proposalId: proposal.id }],
    response: { proposal },
  } } })
  listeners['window:message']({ data: { type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture', selectedTab: 'master', boot } })
  receive()
  const click = action => listeners['root:click']({
    target: { closest: selector => (selector === '[data-action]' ? { dataset: { action, id: proposal.id }, closest: () => null } : null) },
    preventDefault() {},
  })
  return { listeners, posted, root, fields, proposal, boot, click, receive }
}

const DISCUSSION = {
  version: 1, state: 'discussion', mode: 'undecided', resultKind: '',
  goal: 'Подготовить локальный запуск Symfony в рабочей области',
  scope: ['создание каркаса Symfony'], outOfScope: ['бизнес-логика'],
  openQuestions: ['Нужен ли PostgreSQL?'],
  criteria: [{ id: 'c1', kind: 'manual', text: 'Приложение отвечает на HTTP-запрос' }],
  permissions: { writeFiles: true, executeCommands: true, networkHosts: [] },
  budget: { tokens: 200000, activeSeconds: 3600, maxParallel: 2, maxAttempts: 3, maxReplans: 6 },
}
const READY = { ...DISCUSSION, version: 2, state: 'ready', mode: 'precise', resultKind: 'workspace_change', openQuestions: [] }

// Разметка ленты — всё, что стоит до панели задания: панель несёт ту же
// карточку и в закрытом виде остаётся в разметке под атрибутом hidden.
const feedOf = html => html.split('hall-brief-panel')[0]

// ── Пока обсуждают: карточки в ленте нет, есть вкладка ─────────────────────
{
  const ui = open({ ...DISCUSSION })
  const html = ui.root.innerHTML
  check('карточки задания в ленте нет',
    !feedOf(html).includes('hall-proposal'),
    'карточка с запертой кнопкой снова занимает экран разговора')
  // Полоса разговора ушла вместе со старой рейкой истории: вкладка задания
  // теперь стоит в шапке экрана чата, рядом с именем разговора.
  check('вкладка стоит в шапке чата',
    /hall-head-chat[\s\S]*hall-brief-tab/.test(html) && html.includes('data-action="master-brief-toggle"'),
    'задание не показано нигде: обсуждать нечего')
  check('вкладка доступна с клавиатуры',
    /<button[^>]*role="tab"[^>]*tabindex="0"[^>]*>/.test(html) && html.includes('data-keynav="row"'),
    'вкладка без tabindex или без разметки перебора — договор 10 в ui/contracts.mjs')
  check('вкладка названа целью задания',
    html.includes('Подготовить локальный') && html.includes('обсуждение'),
    'на вкладке нет ни имени задания, ни его состояния')
  check('панель есть и закрыта',
    html.includes('id="master-brief-panel"') && /id="master-brief-panel"[^>]*hidden/.test(html),
    'панель либо не собрана, либо открыта без спроса')
}

// ── Раскрытая панель: весь состав, но без запуска ──────────────────────────
{
  const ui = open({ ...DISCUSSION })
  ui.click('master-brief-toggle')
  check('раскрытие сужает разговор',
    ui.fields['.hall-dialogue'].classes.has('is-brief-open')
      && ui.fields['#master-brief-panel'].hidden === false
      && ui.fields['#master-brief-tab'].attrs['aria-selected'] === 'true',
    'панель открыта, а разговор её не заметил')
  ui.receive()
  const html = ui.root.innerHTML
  const panel = html.slice(html.indexOf('hall-brief-panel'))
  check('открытая панель переживает ответ ядра',
    html.includes('is-brief-open') && !/id="master-brief-panel"[^>]*hidden/.test(html),
    'следующий ход закрыл панель под читающим')
  check('в панели есть всё, кроме запуска',
    panel.includes('quest-proposal-modify') && panel.includes('quest-proposal-discuss') && panel.includes('quest-proposal-ignore')
      && !panel.includes('quest-proposal-start'),
    'в панели либо нет действий, либо есть кнопка запуска — решение о запуске должно приниматься в одном месте')
  check('в панели виден состав задания',
    panel.includes('Условия готовности') && panel.includes('Входит в задание') && panel.includes('Нужно уточнить'),
    'панель показывает не то же, что карточка: состав задания собран второй разметкой')
}

// ── Готовность: карточка вернулась, вкладка осталась ───────────────────────
//
// precise + ready — тот самый случай, в котором main.js обнуляет
// masterDiscussionProposalId: вкладка, привязанная к нему, исчезла бы именно
// здесь, то есть ровно тогда, когда задание пора запускать.
{
  const ui = open({ ...DISCUSSION })
  ui.proposal.brief = { ...READY }
  ui.receive()
  const html = ui.root.innerHTML
  check('карточка вернулась в ленту с рабочей кнопкой',
    feedOf(html).includes('quest-proposal-start') && feedOf(html).includes('Выполнить поручение')
      && !/data-action="quest-proposal-start"[^>]*disabled/.test(feedOf(html)),
    'готовое задание либо не показано в ленте, либо показано с запертой кнопкой')
  check('вкладка осталась и назвала готовность',
    html.includes('hall-brief-tab') && html.includes('готово к запуску'),
    'вкладка исчезла вместе с обнулением masterDiscussionProposalId')
}

// ── Редактор брифа рисуется в одном месте ─────────────────────────────────
{
  const ui = open({ ...READY })
  ui.click('master-brief-toggle')
  ui.click('quest-proposal-modify')
  const html = ui.root.innerHTML
  const goalFields = (html.match(/data-brief-field="goal"/g) || []).length
  check('набор полей правки один',
    goalFields === 1,
    `полей правки ${goalFields}: readTaskBriefEditor прочтёт первый набор и молча выбросит видимые правки`)
  check('правка идёт в открытой панели',
    html.indexOf('data-brief-field="goal"') > html.indexOf('hall-brief-panel'),
    'редактор остался в карточке ленты, хотя правку начали в панели')
}

// ── Решённое предложение вкладке не принадлежит ────────────────────────────
{
  const ui = open({ ...DISCUSSION })
  ui.proposal.status = 'ignored'
  ui.receive()
  check('у отклонённого задания вкладки нет',
    !ui.root.innerHTML.includes('hall-brief-tab'),
    'вкладка ведёт к заданию, от которого только что отказались')
}

if (failures.length) {
  console.error('\nЗАДАНИЕ ПОТЕРЯЛОСЬ:\n  ' + failures.join('\n  '))
  process.exitCode = 1
} else {
  console.log('\nЗадание: до готовности вкладка и панель, после — карточка с кнопкой: PASS')
}
