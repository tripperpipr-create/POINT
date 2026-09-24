const fs = require('fs')
const path = require('path')
const vm = require('vm')

const listeners = {}
const posted = []
const fields = {}
const root = {
  innerHTML: '',
  addEventListener(type, callback) { listeners[`root:${type}`] = callback },
  querySelector(selector) { return fields[selector] || null },
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

const blueprint = { id: 'blueprint-qa', name: 'QA Engineer', allowedTools: ['read_file'] }
const projectAgent = { id: 'agent-qa', blueprintId: blueprint.id, name: blueprint.name, allowedTools: ['read_file'] }
const portable = {
  id: 'memory-portable', workspaceId: '', kind: 'profile', ownerId: blueprint.id,
  content: 'Always reproduce a defect before changing code.', source: 'retrospective',
  confidence: 0.9, pinned: true, createdAt: '2026-08-27T00:00:00Z', updatedAt: '2026-08-27T00:00:00Z',
}
listeners['window:message']({ data: {
  type: 'state', service: { state: 'running' }, workspaceTrusted: true,
  workspace: 'fixture', selectedTab: 'memory', onboarding: { complete: true },
  boot: {
    blueprints: [blueprint], projectAgents: [projectAgent], memories: [portable], quests: [],
    profiles: [projectAgent], profileTemplates: [blueprint], toolCatalog: [], skills: [],
    providerCatalog: [], runs: [], usageRecords: [], indexStatus: { state: 'ready' },
  },
} })

if (!root.innerHTML.includes('Опыт команды и факты проекта') ||
    !root.innerHTML.includes('Основной профиль') ||
    !root.innerHTML.includes('QA Engineer') ||
    !root.innerHTML.includes('все проекты') ||
    !root.innerHTML.includes('Поиск по опыту') ||
    !root.innerHTML.includes('Ручное обучение') ||
    !root.innerHTML.includes('2 Шага')) {
  throw new Error('Portable profile memory is not distinguished from project memory in the Hub')
}

fields['#experience-search-query'] = { value: 'verification' }
listeners['root:submit']({
  target: { id: 'experience-search-form', closest() { return null } },
  preventDefault() {},
})
const search = posted.findLast(message => message.type === 'searchExperience')
if (search?.query !== 'verification') {
  throw new Error(`Experience search emitted an invalid payload: ${JSON.stringify(search)}`)
}

fields['#manual-learning-agent'] = { value: projectAgent.id }
fields['#manual-learning-kind'] = { value: 'instruction' }
fields['#manual-learning-scope'] = { value: 'profile' }
fields['#manual-learning-content'] = { value: 'Keep verification evidence in the final response.' }
listeners['root:submit']({
  target: { id: 'manual-learning-form', closest() { return null } },
  preventDefault() {},
})
const previewRequest = posted.findLast(message => message.type === 'previewManualLearning')
if (previewRequest?.request?.projectAgentId !== projectAgent.id ||
    previewRequest.request.kind !== 'instruction' || previewRequest.request.scope !== 'profile') {
  throw new Error(`Manual learning preview emitted an invalid payload: ${JSON.stringify(previewRequest)}`)
}
listeners['window:message']({ data: {
  type: 'manualLearningPreview', request: previewRequest.request,
  preview: {
    summary: 'Добавить переносимое правило', scope: 'profile',
    content: previewRequest.request.content, changes: ['Обновить основной профиль'],
    confirmationToken: 'confirm-exact-preview',
  },
} })
if (!root.innerHTML.includes('Ручное обучение · preview') ||
    !root.innerHTML.includes('Подтвердить и обучить')) {
  throw new Error('Manual learning does not expose the confirmation step')
}
const applyTarget = {
  dataset: { action: 'apply-manual-learning' },
  closest(selector) { return selector === '[data-action]' ? this : null },
}
listeners['root:click']({ target: applyTarget })
const applyRequest = posted.findLast(message => message.type === 'applyManualLearning')
if (applyRequest?.request?.confirmationToken !== 'confirm-exact-preview' ||
    applyRequest.request.content !== previewRequest.request.content) {
  throw new Error(`Manual learning apply did not preserve the reviewed preview: ${JSON.stringify(applyRequest)}`)
}

fields['#memory-kind'] = { value: 'profile' }
fields['#memory-owner'] = { value: blueprint.id }
fields['#memory-content'] = { value: 'Check accessibility in every UI review.' }
fields['#memory-source'] = { value: 'team rule' }
fields['#memory-confidence'] = { value: '0.85' }
fields['#memory-pinned'] = { checked: true }
listeners['root:submit']({
  target: { id: 'memory-form', closest() { return null } },
  preventDefault() {},
})

const saved = posted.findLast(message => message.type === 'saveMemory')
if (!saved?.memory || saved.memory.kind !== 'profile' || saved.memory.ownerId !== blueprint.id ||
    saved.memory.content !== 'Check accessibility in every UI review.' || saved.memory.workspaceId) {
  throw new Error(`Portable profile memory form emitted an invalid payload: ${JSON.stringify(saved)}`)
}

if (!source.includes('разрешено как урок') || !/payload\.learningIntent\s*===\s*["']correction["']/.test(source)) {
  throw new Error('Run conversation does not distinguish consented corrections from ordinary messages')
}

process.stdout.write(JSON.stringify({
  profileMemory: 'portable', owner: blueprint.id, projectMemory: 'isolated',
  experienceSearch: 'persisted', manualLearning: 'preview-confirm-apply', correctionConsent: 'inspectable',
}))
