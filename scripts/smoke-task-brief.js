const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')
const listeners = {}, posted = [], fields = {}
const root = { innerHTML: '', addEventListener(type, cb) { listeners[`root:${type}`] = cb }, querySelector(s) { return fields[s] || null }, querySelectorAll() { return [] } }
const context = {
  acquireVsCodeApi: () => ({ postMessage(m) { posted.push(m) }, getState() {}, setState() {} }),
  document: { getElementById: id => id === 'root' ? root : undefined, body: { dataset: { layout: 'wide' } } },
  window: { addEventListener(type, cb) { listeners[`window:${type}`] = cb } },
  console, Date, Map, Set, structuredClone, CSS: { escape: String },
  requestAnimationFrame(cb) { cb(); return 0 }, cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
}
vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../vscode-extension/media/main.js'), 'utf8'), context)
const proposal = { id: 'brief-1', title: 'Audit', status: 'pending', brief: { version: 3, state: 'ready', mode: 'project', resultKind: 'report', goal: 'Find reproducible bugs', scope: ['Audit only'], outOfScope: ['Fixes'], openQuestions: [], criteria: [{ id: 'c1', kind: 'manual', text: 'Report with reproductions' }], permissions: { writeFiles: false, executeCommands: true, networkHosts: [] }, budget: { tokens: 200000, activeSeconds: 3600, maxParallel: 2, maxAttempts: 3, maxReplans: 6 } } }
// Ростер не пустой намеренно: готовое задание при пустом ростере — мир, которого
// ядро не создаёт. Оно само дописывает в такое задание решение «Подготовка
// исполнителя», и карточка обязана звать готовить исполнителя, а не запускать.
// Здесь проверяется другое — что готовое задание вообще возвращается в ленту
// с рабочей кнопкой, — поэтому исполнитель в проекте есть.
const boot = { onboarded: true, profiles: [], projectAgents: [{ id: 'pa-1', name: 'Разработчик', provider: 'ollama', baseUrl: 'http://127.0.0.1:11434', primaryModel: 'qwen', maxSteps: 30, allowedTools: ['read_file', 'propose_patch', 'run_command'] }], connections: [], quests: [], executions: [], runs: [], flows: [], questProposals: [proposal] }
listeners['window:message']({ data: { type: 'state', service: { state: 'running' }, workspace: 'fixture', selectedTab: 'master', boot } })
listeners['window:message']({ data: { type: 'master', master: { configured: true, history: [{ id: 'm1', role: 'assistant', content: 'Ready', proposalId: proposal.id }], response: { proposal } } } })
assert.match(root.innerHTML, /Утвердить и запустить/)
assert.match(root.innerHTML, /Версия 3/)
function click(action) { return listeners['root:click']({ target: { closest: s => s === '[data-action]' ? { dataset: { action, id: proposal.id } } : null }, preventDefault() {} }) }
click('quest-proposal-modify')
const field = (name, value, checked) => fields[`[data-brief-field="${name}"][data-id="brief-1"]`] = { value, checked }
field('mode', 'project'); field('resultKind', 'report'); field('goal', 'Narrowed audit'); field('scope', 'Audit only'); field('outOfScope', 'Fixes'); field('openQuestions', ''); field('criterion-0', 'Report with reproductions'); field('writeFiles', '', false); field('executeCommands', '', true); field('networkHosts', ''); field('tokens', '100000'); field('activeMinutes', '30')
click('quest-proposal-start')
assert.equal(posted.filter(m => m.type === 'decideQuestProposal').length, 0, 'editing card launched before saving')
click('quest-proposal-modify')
const save = posted.find(m => m.type === 'decideQuestProposal')
assert.equal(save.action, 'modify'); assert.equal(save.expectedVersion, 3); assert.equal(save.brief.goal, 'Narrowed audit'); assert.equal(save.brief.budget.tokens, 100000); assert.equal(save.brief.permissions.writeFiles, false)
listeners['window:message']({ data: { type: 'questProposalModified', proposalId: proposal.id, action: 'modify' } })
proposal.brief = { ...save.brief, version: 4 }
listeners['window:message']({ data: { type: 'state', service: { state: 'running' }, workspace: 'fixture', selectedTab: 'master', boot } })
click('quest-proposal-start')
const starts = posted.filter(m => m.type === 'decideQuestProposal' && m.action === 'start')
assert.equal(starts.length, 1)
assert.equal(starts[0].expectedVersion, 4); assert.equal(starts[0].approveVersion, 4)
click('quest-proposal-start')
assert.equal(posted.filter(m => m.type === 'decideQuestProposal' && m.action === 'start').length, 1, 'double start')
console.log('PASS: task brief edit, versioned approval and double-start protection')

// Each answer stays bound to the same brief, including after a model failure.
proposal.brief.state = 'discussion'
const receiveDiscussion = () => listeners['window:message']({ data: { type: 'master', master: { configured: true, history: [], response: { proposal } } } })
receiveDiscussion()
fields['#master-input'] = { value: 'Only for me', disabled: false }
click('master-send')
assert.equal(posted.filter(m => m.type === 'masterChat').at(-1).proposalId, proposal.id)
listeners['window:message']({ data: { type: 'master', turnFinished: true, master: { configured: true, response: { mode: 'deterministic', reply: 'Model unavailable' } } } })
fields['#master-input'].value = 'Continue with that answer'
click('master-send')
assert.equal(posted.filter(m => m.type === 'masterChat').at(-1).proposalId, proposal.id, 'failure lost discussion binding')
listeners['window:message']({ data: { type: 'master', turnFinished: true, master: { configured: true, history: [], response: { proposal } } } })
click('master-new-discussion')
fields['#master-input'].value = 'A separate task'
click('master-send')
assert.equal(posted.filter(m => m.type === 'masterChat').at(-1).proposalId, undefined, 'new discussion edited old task')
console.log('PASS: discussion continuity, model failure and explicit new discussion')
