// Правка предложения переживает фоновое обновление состояния.
//
// Между открытием формы «Изменить» и нажатием кнопки приходит обновление —
// индекс досчитался, квест сменил статус, живое состояние пришло по таймеру, — и
// интерфейс перерисовывается целиком. Значения полей брались из самого
// предложения, поэтому набранный текст возвращался к исходному: человек правил
// цели, отвлекался на секунду и обнаруживал прежний текст.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${detail}`)
}

const listeners = {}
const posted = []
// Значения полей формы: интерфейс читает их через querySelector по data-атрибутам.
const fields = new Map()
const actionFields = new Map()
const fieldNode = (source, key) => ({
  value: source.get(key) || '',
  get checked() { return false },
  dataset: {},
})
const root = {
  innerHTML: '',
  addEventListener(type, callback) { listeners['root:' + type] = callback },
  querySelector(selector) {
    const text = String(selector || '')
    const proposalField = /\[data-proposal-field="([^"]+)"\]/.exec(text)
    if (proposalField) return fieldNode(fields, proposalField[1])
    const actionField = /\[data-companion-action-field="([^"]+)"\]/.exec(text)
    if (actionField) return fieldNode(actionFields, actionField[1])
    return null
  },
  querySelectorAll() { return [] },
}
const context = {
  acquireVsCodeApi: () => ({ postMessage(message) { posted.push(message) }, getState() { return undefined }, setState() {} }),
  document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'companion' } } },
  window: { addEventListener(type, callback) { listeners['window:' + type] = callback } },
  console, Date, Map, Set, CSS: { escape: value => String(value) },
  requestAnimationFrame(callback) { callback(); return 0 },
  cancelAnimationFrame() {},
  setTimeout(callback) { callback(); return 0 },
  clearTimeout() {},
}
vm.runInNewContext(fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'media', 'main.js'), 'utf8'), context, { filename: 'media/main.js' })

const proposal = {
  id: 'qp-1', workspaceId: 'ws', title: 'Починить вебхук оплаты', status: 'pending',
  importance: 'normal', objectives: ['Разобрать лог'], constraints: [], definitionOfDone: [],
  rationale: 'Оплата не доходит', unknowns: [], teamAgentIds: [], createdAt: new Date().toISOString(),
}
const sendState = () => listeners['window:message']({
  data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture', selectedTab: 'overview',
    boot: { questProposals: [proposal], companionActionProposals: [], projectAgents: [], profiles: [], flows: [], skills: [], runs: [], toolCatalog: [], connections: [], companion: { id: 'c1', preset: 'balanced', configured: true, provider: 'ollama', model: 'qwen' }, companionMessages: [] },
    details: undefined,
  },
})
const click = (action, dataset = {}) => listeners['root:click']({
  target: { dataset: { action, ...dataset }, closest(selector) { return selector === '[data-action]' ? this : null } },
})

sendState()
check('карточка предложения отрисована', root.innerHTML.includes('Починить вебхук оплаты'), root.innerHTML.slice(0, 200))

check('legacy-квест передаётся Мастеру',
  root.innerHTML.includes('Обсудить с Мастером') && root.innerHTML.includes('карточку наряда у Мастера'),
  'нет видимого перехода в основной v2-контур')
check('скрытая legacy-правка не предлагается',
  !root.innerHTML.includes('data-action="quest-proposal-modify"') && !root.innerHTML.includes('data-action="quest-proposal-start"'),
  'карточка всё ещё позволяет обойти WorkOrder v2')

// Карточка действия помощника — вторая форма на том же экране: имя агента,
// инструкции навыка, набор инструментов. Потеря здесь была та же.
const action = {
  id: 'ca-1', workspaceId: 'ws', kind: 'create_skill', status: 'pending',
  title: 'Создать Skill · Ревью по чек-листу', rationale: 'Единый разбор правок',
  skill: { id: 'sk-new', name: 'Ревью по чек-листу', description: 'Единый разбор правок', instructions: 'Проверяй по списку.', requiredTools: ['read_file'] },
  createdAt: new Date().toISOString(), updatedAt: new Date().toISOString(),
}
const sendActionState = () => listeners['window:message']({
  data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture', selectedTab: 'overview',
    boot: {
      questProposals: [], companionActionProposals: [action], projectAgents: [], profiles: [], flows: [], skills: [],
      runs: [], toolCatalog: [], connections: [], companion: { id: 'c1', preset: 'balanced', configured: true, provider: 'ollama', model: 'qwen' },
      companionMessages: [],
    },
    details: undefined,
  },
})

sendActionState()
check('карточка действия отрисована', root.innerHTML.includes('Ревью по чек-листу'), root.innerHTML.slice(0, 200))
click('companion-action-modify', { id: 'ca-1' })
check('форма действия открыта', root.innerHTML.includes('data-companion-action-field'), 'редактор действия не отрисовался')

actionFields.set('name', 'Ревью по чек-листу перед релизом')
actionFields.set('instructions', 'Проверяй по списку и смотри тесты.')
listeners['root:input']({ target: { dataset: { companionActionField: 'name', id: 'ca-1' }, value: actionFields.get('name') } })
sendActionState()
check('правка карточки действия пережила обновление',
  root.innerHTML.includes('Ревью по чек-листу перед релизом'),
  'в форме снова исходное имя')

if (failures.length) {
  console.error('\nПРОВАЛЕНО:')
  for (const item of failures) console.error('  · ' + item)
  process.exit(1)
}
console.log('\nправка предложения переживает обновление состояния')
