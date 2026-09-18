// Composer должен показывать те же кнопки Run, что и обзор.
//
// Раньше на экране квеста не было extend-active-time: продление жило только в
// overview. При паузе по лимиту активного времени человек на вкладке quest
// не мог продолжить работу.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

function screen() {
  const listeners = {}
  const chatMain = { innerHTML: '', scrollHeight: 0, scrollTop: 0, clientHeight: 0 }
  const root = {
    innerHTML: '',
    addEventListener(type, cb) { listeners[`root:${type}`] = cb },
    querySelector: sel => (sel === '.chat-main' ? chatMain : null),
    querySelectorAll: () => [],
  }
  vm.runInNewContext(main, {
    acquireVsCodeApi: () => ({ postMessage() {}, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
    window: { addEventListener(type, cb) { listeners[`window:${type}`] = cb } },
    console, Date, Map, Set,
    requestAnimationFrame(cb) { cb(); return 0 },
    cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
  }, { filename: 'main.js' })

  const run = {
    id: 'r-exhausted',
    status: 'paused',
    task: 'долгая работа',
    agentId: 'a1',
    profileId: 'a1',
    controller: {
      pauseReason: 'active_time_exhausted',
      activeSecondsRemaining: 0,
      activeSecondsBudget: 60,
      resumable: true,
    },
  }
  const boot = {
    onboarded: true,
    service: { state: 'running' },
    workspace: { id: 'ws', path: 'C:/proj', name: 'proj' },
    profiles: [{ id: 'a1', name: 'Dev', provider: 'ollama', model: 'qwen', allowedTools: ['read_file'] }],
    projectAgents: [{ id: 'a1', name: 'Dev', provider: 'ollama', primaryModel: 'qwen', model: 'qwen', allowedTools: ['read_file'] }],
    runs: [run],
    executions: [{ id: 'ex1', runId: run.id, status: 'paused', projectAgentId: 'a1' }],
    quests: [{ id: 'q1', title: 'Quest', status: 'active' }],
    teams: [], flows: [], skills: [], connections: [], usageRecords: [], changeSets: [],
  }
  listeners['window:message']({
    data: {
      type: 'state',
      service: { state: 'running' },
      boot,
      selectedTab: 'quests',
      details: { run },
      workspaceTrusted: true,
      workspace: 'ws',
    },
  })
  return root.innerHTML + chatMain.innerHTML
}

const html = screen()
if (!html.includes('data-action="extend-active-time"')) {
  console.error('quest composer missing extend-active-time when budget exhausted')
  console.error(html.slice(0, 2500))
  process.exit(1)
}
if (!html.includes('data-exec-form="message"') || !html.includes('data-exec-form="forbid"')) {
  console.error('quest composer missing message/forbid forms from execControlsHtml')
  process.exit(1)
}
console.log('smoke-hub-composer-extend-active-time: ok')
