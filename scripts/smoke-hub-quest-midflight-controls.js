// Mid-flight replan / revise controls must be visible on an active structured quest.
//
// Backend APIs existed without Hub UI. Operators could not drive replan or goal
// change from the quest card.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

function screen(selectedTab = 'overview') {
  const listeners = {}
  const posted = []
  const chatMain = { innerHTML: '', scrollHeight: 0, scrollTop: 0, clientHeight: 0 }
  const root = {
    innerHTML: '',
    addEventListener(type, cb) { listeners[`root:${type}`] = cb },
    querySelector: sel => (sel === '.chat-main' ? chatMain : null),
    querySelectorAll: () => [],
  }
  vm.runInNewContext(main, {
    acquireVsCodeApi: () => ({
      postMessage(msg) { posted.push(msg) },
      getState() { return {} },
      setState() {},
    }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
    window: { addEventListener(type, cb) { listeners[`window:${type}`] = cb } },
    console, Date, Map, Set,
    requestAnimationFrame(cb) { cb(); return 0 },
    cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
  }, { filename: 'main.js' })

  const brief = {
    version: 2,
    mode: 'project',
    goal: 'Ship hub mid-flight controls',
    resultKind: 'workspace_change',
    state: 'approved',
    approvedVersion: 2,
    approvedDigest: 'digest',
    criteria: [{ id: 'c1', text: 'Controls work', kind: 'manual' }],
    budget: { tokens: 1000, activeSeconds: 60, maxParallel: 1, maxReplans: 6, maxAttempts: 2 },
    permissions: { writeFiles: true },
  }
  const boot = {
    onboarded: true,
    service: { state: 'running' },
    workspace: { id: 'ws', path: 'C:/proj', name: 'proj' },
    profiles: [{ id: 'a1', name: 'Dev', provider: 'ollama', model: 'qwen', allowedTools: ['read_file'] }],
    projectAgents: [{ id: 'a1', name: 'Dev', provider: 'ollama', primaryModel: 'qwen', model: 'qwen', allowedTools: ['read_file'] }],
    quests: [{
      id: 'q-active',
      title: 'Active structured',
      status: 'active',
      flowId: 'flow-1',
      brief,
      teamAgentIds: ['a1'],
    }],
    flows: [{
      id: 'flow-1',
      nodes: [
        { id: 'in', kind: 'input', name: 'In' },
        { id: 'stage-1', kind: 'agent', name: 'Implement', agentId: 'a1', config: { instruction: 'old' } },
        { id: 'out', kind: 'output', name: 'Out' },
      ],
    }],
    flowRuns: [{
      id: 'fr-1',
      flowId: 'flow-1',
      questId: 'q-active',
      status: 'waiting',
      nodeStates: { 'stage-1': { status: 'blocked' } },
      snapshot: {
        graph: {
          id: 'flow-1',
          nodes: [
            { id: 'in', kind: 'input', name: 'In' },
            { id: 'stage-1', kind: 'agent', name: 'Implement', agentId: 'a1', config: { instruction: 'old' } },
            { id: 'out', kind: 'output', name: 'Out' },
          ],
        },
      },
    }],
    executions: [],
    changeSets: [],
    teams: [],
    skills: [],
    connections: [],
    usageRecords: [],
    runs: [],
  }
  listeners['window:message']({
    data: {
      type: 'state',
      service: { state: 'running' },
      boot,
      selectedTab,
      workspaceTrusted: true,
      workspace: 'ws',
    },
  })
  return { html: root.innerHTML + chatMain.innerHTML, posted }
}

const { html, posted } = screen('overview')
if (!html.includes('ПЕРЕПЛАНИРОВАТЬ ЭТАП')) {
  console.error('mid-flight replan control missing on overview for active structured quest')
  console.error(html.slice(0, 4000))
  process.exit(1)
}
if (!html.includes('УТВЕРДИТЬ НОВУЮ ЦЕЛЬ')) {
  console.error('mid-flight revise control missing on overview')
  process.exit(1)
}
if (!html.includes('Implement') && !html.includes('stage-1')) {
  console.error('agent stage options missing')
  process.exit(1)
}
if (!posted.some(message => message.type === 'loadQuestReplans' && message.questId === 'q-active')) {
  console.error('expected loadQuestReplans for active quest')
  process.exit(1)
}
console.log('smoke-hub-quest-midflight-controls: ok')
