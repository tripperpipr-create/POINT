// Старт квеста с Мастера остаётся в переписке и показывает работу агентов.
//
// Раньше focusTab('overview') уводил с ленты; под «КВЕСТ ЗАПУЩЕН» не было
// tools / ответов / diffs. Здесь фиксируем origin=master + hall-work embed.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')
const hubRuntime = fs.readFileSync(path.join(repo, 'vscode-extension/hub-runtime-controller.js'), 'utf8')

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${String(detail || '').slice(0, 240)}`)
}

check('extension stays on Master for origin=master', hubRuntime.includes("message.origin === 'master'"))
check('extension posts questProposalStarted', hubRuntime.includes("type: 'questProposalStarted'"))
check('overview focus only when not from Master', hubRuntime.includes('if (!fromMaster) this.focusTab'))

function open() {
  const listeners = {}
  const posted = []
  const field = { value: '', disabled: false, focus() {}, setSelectionRange() {} }
  const sendButton = { disabled: false }
  const discussion = { innerHTML: '' }
  const thread = { innerHTML: '', scrollTop: 0, scrollHeight: 1000, clientHeight: 300, scrollTo() {} }
  let html = ''
  const root = {
    get innerHTML() { return html },
    set innerHTML(value) { html = value },
    addEventListener(type, callback) { listeners[`root:${type}`] = callback },
    querySelector(selector) {
      if (selector === '#master-input') return html.includes('id="master-input"') ? field : null
      if (selector === '#master-thread') return html.includes('id="master-thread"') ? thread : null
      if (selector === '#master-discussion-context') return html.includes('master-discussion-context') ? discussion : null
      if (selector === '.hall-compose .hall-btn.is-primary') return html.includes('hall-compose') ? sendButton : null
      return null
    },
    querySelectorAll: () => [],
    contains: () => true,
  }
  vm.runInNewContext(main, {
    acquireVsCodeApi: () => ({
      postMessage(message) { posted.push(message) },
      getState() { return {} },
      setState() {},
    }),
    document: {
      getElementById: id => (id === 'root' ? root : undefined),
      body: { dataset: { layout: 'wide' } },
      activeElement: null,
    },
    window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
    console, Date, Map, Set, JSON,
    requestAnimationFrame(callback) { callback(); return 0 },
    cancelAnimationFrame() {},
    setTimeout(callback) { callback(); return 0 },
    clearTimeout() {},
  }, { filename: 'main.js' })

  const click = dataset => listeners['root:click']({
    target: { closest: selector => (selector === '[data-action]' ? { dataset } : null) },
    preventDefault() {},
  })

  return { listeners, posted, root, thread, click }
}

const proposal = {
  id: 'prop-master-work',
  title: 'Починить Clamp',
  status: 'pending',
  importance: 'normal',
  objectives: ['вернуть функцию'],
  definitionOfDone: ['тест зелёный'],
  teamAgentIds: ['agent-1'],
}

const ui = open()
ui.listeners['window:message']({
  data: {
    type: 'state',
    service: { state: 'running' },
    workspaceTrusted: true,
    workspace: 'w',
    selectedTab: 'master',
    boot: {
      onboarded: true,
      profiles: [],
      projectAgents: [{ id: 'agent-1', name: 'Coder', roleDescription: 'dev' }],
      usageRecords: [],
      runs: [],
      quests: [],
      executions: [],
      changeSets: [],
      questProposals: [proposal],
      companionActionProposals: [],
      orchestrator: { id: 'o1', preset: 'conductor' },
    },
  },
})

ui.listeners['window:message']({
  data: {
    type: 'master',
    master: {
      configured: true,
      config: { model: 'qwen' },
      history: [
        { role: 'user', content: 'Почини Clamp' },
        { role: 'assistant', content: 'Предлагаю квест', proposalId: 'prop-master-work' },
      ],
      response: { proposal, mode: 'model', model: 'qwen' },
    },
  },
})

const pendingHtml = ui.root.innerHTML + ui.thread.innerHTML
check('pending proposal visible on Master',
  pendingHtml.includes('quest-proposal-start') || pendingHtml.includes('ПРЕДЛОЖЕН'),
  pendingHtml.slice(0, 200))

ui.click({ action: 'quest-proposal-start', id: 'prop-master-work' })
const start = ui.posted.find(item => item.type === 'decideQuestProposal' && item.action === 'start')
check('start posts decideQuestProposal', Boolean(start))
check('start sends origin=master', start?.origin === 'master', JSON.stringify(start))

ui.listeners['window:message']({
  data: {
    type: 'questProposalStarted',
    proposalId: 'prop-master-work',
    questId: 'quest-1',
    runId: 'run-1',
    stayInMaster: true,
  },
})

ui.listeners['window:message']({
  data: {
    type: 'state',
    service: { state: 'running' },
    workspaceTrusted: true,
    workspace: 'w',
    selectedTab: 'master',
    boot: {
      onboarded: true,
      profiles: [],
      projectAgents: [{ id: 'agent-1', name: 'Coder', roleDescription: 'dev' }],
      usageRecords: [],
      runs: [{ id: 'run-1', status: 'running', step: 2, task: 'Почини Clamp', questId: 'quest-1' }],
      quests: [{ id: 'quest-1', title: 'Починить Clamp', status: 'active' }],
      executions: [{ id: 'exec-1', questId: 'quest-1', runId: 'run-1', status: 'running', projectAgentId: 'agent-1' }],
      changeSets: [{
        id: 'cs-1',
        questId: 'quest-1',
        status: 'pending',
        title: 'Правки Clamp',
        createdAt: new Date().toISOString(),
        items: [{ path: 'clamp.go', kind: 'modify', diff: '+func Clamp' }],
      }],
      questProposals: [{ ...proposal, status: 'started' }],
      companionActionProposals: [],
      orchestrator: { id: 'o1', preset: 'conductor' },
    },
    details: {
      run: { id: 'run-1', status: 'running', step: 2, task: 'Почини Clamp', questId: 'quest-1', requestCount: 1 },
      events: [
        { type: 'tool.requested', step: 1, data: { tool: 'read_file' } },
        { type: 'model.responded', step: 1, data: { content: 'Читаю файл и готовлю правку.' } },
        { type: 'patch.proposed', step: 2, data: { id: 'patch-1' } },
      ],
      patches: [{ id: 'patch-1', path: 'clamp.go', status: 'proposed', approvalId: 'ap-1', diff: '+func Clamp(x int) int' }],
      approvals: [{ id: 'ap-1', status: 'pending', toolName: 'propose_patch', reason: 'diff' }],
    },
  },
})

const html = ui.root.innerHTML + ui.thread.innerHTML
check('started work card in Master', html.includes('hall-work') || html.includes('Квест запущен'), html.slice(0, 240))
check('agent transcript visible',
  html.includes('agent-work') || html.includes('Читаю файл') || html.includes('read_file') || html.includes('Агент работает'),
  html.slice(0, 240))
check('diff or change set reachable',
  html.includes('clamp.go') || html.includes('changeset') || html.includes('Показать diff') || html.includes('НАБОР'),
  html.slice(0, 240))

if (failures.length) {
  console.error('FAIL\n' + failures.join('\n'))
  process.exit(1)
}
console.log('smoke-hub-master-work-transcript: ok')
