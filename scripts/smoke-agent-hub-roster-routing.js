const fs = require('fs')
const path = require('path')
const vm = require('vm')

async function main() {
  const listeners = {}
  const posted = []
  const root = {
    innerHTML: '',
    addEventListener(type, callback) { listeners[`root:${type}`] = callback },
    querySelector() { return null },
    querySelectorAll() { return [] },
  }
  const context = {
    acquireVsCodeApi: () => ({
      postMessage(message) { posted.push(message) },
      getState() { return undefined },
      setState() {},
    }),
    document: {
      getElementById: id => id === 'root' ? root : undefined,
      body: { dataset: { layout: 'wide' } },
    },
    window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
    console, Date, Map, Set,
    CSS: { escape(value) { return String(value) } },
    requestAnimationFrame(callback) { callback(); return 0 },
    cancelAnimationFrame() {},
    setTimeout(callback) { callback(); return 0 },
    clearTimeout() {},
  }
  const source = fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'media', 'main.js'), 'utf8')
  vm.runInNewContext(source, context, { filename: 'media/main.js' })

  const template = {
    id: 'developer-template', name: 'Разработчик', description: 'Исправляет код',
    roleDescription: 'Универсальный разработчик', model: 'auto', provider: 'cursor-cli',
    allowedTools: ['project_map', 'search_code', 'read_file', 'propose_patch', 'run_command'],
    maxSteps: 30, maxDurationSeconds: 600, approvalMode: 'safe',
  }
  const boot = {
    profiles: [], projectAgents: [], blueprints: [], profileTemplates: [template],
    toolCatalog: [], skills: [], providerCatalog: [], runs: [], usageRecords: [],
    indexStatus: { state: 'not_built' },
  }
  listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true,
    workspace: 'fixture', selectedTab: 'agents', onboarding: { complete: true }, boot,
  } })
  if (!root.innerHTML.includes('Добавьте первого агента') || !root.innerHTML.includes('data-action="use-template"')) {
    throw new Error('Clean Hub roster did not offer the first-agent creation flow')
  }

  async function click(action, extra = {}) {
    await listeners['root:click']({ target: { closest(selector) {
      if (selector === '[data-example]') return null
      if (selector === '[data-action]') return { dataset: { action, ...extra } }
      return null
    } } })
  }

  await click('use-template', { template: template.id })
  if (!root.innerHTML.includes('Профиль агента') || !root.innerHTML.includes('Разработчик') || root.innerHTML.includes('profile-wizard create-flow')) {
    throw new Error('Hub template hire was routed to the legacy profile wizard')
  }
  await click('constructor-step', { step: 'review' })
  if (!root.innerHTML.includes('data-action="save-constructor"') || !root.innerHTML.includes('чертёж и проектный агент')) {
    throw new Error('Hub agent constructor did not reach the project-agent review')
  }
  await click('save-constructor')
  const save = posted.findLast(message => message.type === 'saveProjectAgent')
  if (!save?.agent || save.agent.name !== template.name || save.agent.id !== '' || posted.some(message => message.type === 'saveProfile')) {
    throw new Error(`Hub agent was saved through the wrong API: ${JSON.stringify(posted.slice(-5))}`)
  }

  const saved = { ...save.agent, id: 'agent-1', level: 1, tasksCompleted: 0, successCount: 0 }
  listeners['window:message']({ data: { type: 'projectAgentSaved', agentId: saved.id, agent: saved } })
  listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true,
    workspace: 'fixture', selectedTab: 'agents', onboarding: { complete: true },
    boot: { ...boot, projectAgents: [saved] },
  } })
  if (!root.innerHTML.includes('Команда текущего проекта') || !root.innerHTML.includes(saved.name) || !root.innerHTML.includes('Основной профиль и проектная адаптация')) {
    throw new Error('Saved project agent did not appear in the Hub roster')
  }

  await click('fix-profile-step', { step: 'model' })
  if (!root.innerHTML.includes('Профиль агента') || !root.innerHTML.includes('class="on" data-action="constructor-step" data-step="brain"')) {
    throw new Error('Hub readiness fix did not open the project-agent constructor at the model step')
  }
  await click('close-agent-constructor')
  await click('new-profile')
  if (!root.innerHTML.includes('Профиль агента') || !root.innerHTML.includes('value="Новый агент"')) {
    throw new Error('Hub New Agent action did not open a new project-agent constructor')
  }

  process.stdout.write(JSON.stringify({ firstAgent: 'project-agent', roster: 'visible', editRoute: 'constructor' }))
}

main().catch(error => {
  console.error(error)
  process.exitCode = 1
})
