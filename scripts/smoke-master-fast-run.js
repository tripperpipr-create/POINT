// Прогон Быстрого агента в разговоре Мастера.
//
// 03.10 человек видел одну строку «Fast Agent · running · шаг 16» и не знал,
// что агент делает; удаление ветки прошло без его решения. Карточка обязана
// назвать статус по-русски, показать команды с доводами, держать «Остановить»
// и в ожидании решения дать кнопки подтверждения с риском.
//
// События — в той форме, в какой их отдаёт /api/runs/{id} (снято с прогона
// run_72f08b27276d361367e8f195): arguments — объект, а не строка.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')
const sessions = { workspaceId: 'ws-lk', active: 'conv-1', items: [{ id: 'conv-1', title: 'git' }] }

function open() {
  const listeners = {}
  const posted = []
  const field = { id: 'master-input', value: '', rows: 2, focus() {}, setSelectionRange() {}, closest: () => null, matches: () => false }
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
    boot: { onboarded: true, profiles: [], projectAgents: [], usageRecords: [], runs: [], quests: [], executions: [], changeSets: [], questProposals: [], orchestrator: { id: 'o1', preset: 'conductor', model: 'qwen' } },
  } })
  listeners['window:message']({ data: { type: 'master', master: {
    configured: true, config: { model: 'qwen' }, history: [{ id: 'm1', role: 'assistant', mode: 'model', content: 'Передаю задачу Fast Agent.', createdAt: new Date().toISOString() }], sessions,
  }, conversationId: 'conv-1', loaded: true } })
  const send = message => listeners['window:message']({ data: { type: 'masterFastRun', conversationId: 'conv-1', ...message } })
  return { root, send, posted }
}

const task = 'В репозитории centrofinans-lk выполнить две git-операции на текущей ветке master:\n\n1. Удалить локальную ветку PRODLK-8449.'
const run = (status, step) => ({ id: 'run-fast', workspaceId: 'ws-lk', status, step, task })
const event = (type, step, data) => ({ id: `${type}-${step}-${Math.random()}`, runId: 'run-fast', type, step, data })
const events = [
  event('run.started', 0, {}),
  event('model.responded', 1, { content: 'Проверю состояние репозитория.' }),
  event('tool.requested', 1, { tool: 'run_command', callId: 'c1', arguments: { command: 'git status', reason: 'Inspect current branch and working tree state' } }),
  event('tool.finished', 1, { tool: 'run_command', callId: 'c1', result: { ok: true, output: { exitCode: 0, stdout: 'On branch master' } } }),
  event('tool.requested', 2, { tool: 'run_command', callId: 'c2', arguments: { command: 'git branch -D PRODLK-8449', reason: 'Ветка влита через squash' } }),
  event('approval.requested', 2, { id: 'approval-1' }),
]
const approval = { id: 'approval-1', runId: 'run-fast', toolName: 'run_command', status: 'pending', reason: 'Удаление ветки без проверки слияния: несмерженные коммиты потеряются', arguments: { command: 'git branch -D PRODLK-8449', reason: 'Ветка влита через squash' } }

{
  const ui = open()
  // Первый снимок приходит из истории разговора: хроники ещё нет.
  ui.send({ run: run('running', 1) })
  let html = ui.root.innerHTML
  if (!html.includes('Быстрый агент') || !html.includes('Загружаем хронику')) throw new Error('fast run card without details is not shown')
  if (html.includes('Fast Agent · running')) throw new Error('the old one-line English status is still rendered')

  ui.send({ run: run('waiting_approval', 2), details: { run: run('waiting_approval', 2), events, approvals: [approval], patches: [] } })
  html = ui.root.innerHTML
  const checks = [
    ['Нужно решение', 'status is not localized'],
    ['ход 2', 'step is not shown as a move'],
    ['выполнить две git-операции на текущей ветке master:', 'task headline is missing'],
    ['<code title="git status">git status</code>', 'command of a tool call is not shown'],
    ['Inspect current branch and working tree state', 'reason of a tool call is not shown'],
    ['data-action="cancel" data-id="run-fast"', 'Stop disappeared while the run waits for approval'],
    ['data-action="resolve" data-id="approval-1" data-allow="true"', 'approval buttons are missing'],
    ['class="approval-risk"', 'approval risk line is missing'],
  ]
  for (const [needle, failure] of checks) {
    if (!html.includes(needle)) throw new Error(failure)
  }
  if (html.split('data-id="approval-1" data-allow="true"').length !== 2) throw new Error('approval card is rendered twice')

  ui.send({ run: run('completed', 3), details: { run: run('completed', 3), events: [...events, event('run.completed', 3, {})], approvals: [{ ...approval, status: 'denied' }], patches: [] } })
  html = ui.root.innerHTML
  if (html.includes('data-action="cancel" data-id="run-fast"')) throw new Error('Stop stays on a finished run')
  if (!html.includes('Завершён')) throw new Error('finished status is not localized')
  if (/class="hall-work-log" open/.test(html)) throw new Error('finished chronicle stays expanded')
}

// Хроника чужого прогона не рисуется под текущим.
{
  const ui = open()
  ui.send({ run: run('running', 1), details: { run: { ...run('running', 1), id: 'run-old' }, events, approvals: [], patches: [] } })
  if (ui.root.innerHTML.includes('git status')) throw new Error('details of another run leaked into the card')
}

console.log('master fast run smoke: ok')
