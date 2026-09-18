// Выбор мозга обязан пережить путь до ядра.
//
// Настройка помощника начинается с выбора мозга, и от него зависит всё
// остальное. Пока правило «какие бывают мозги» было записано в нескольких
// местах, выбранный Claude Code превращался во встроенный разбор: человек
// выбирал одно, а ядро получало пустого провайдера и отвечало местным разбором.
// Проверяется не намерение, а то, что уходит в ядро.

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
const root = {
  innerHTML: '',
  addEventListener(type, callback) { listeners[`root:${type}`] = callback },
  querySelector() { return null },
  querySelectorAll() { return [] },
}
vm.runInNewContext(fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'media', 'main.js'), 'utf8'), {
  acquireVsCodeApi: () => ({ postMessage(message) { posted.push(message) }, getState() { return undefined }, setState() {} }),
  document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
  window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
  console, Date, Map, Set, CSS: { escape: value => String(value) },
  requestAnimationFrame(callback) { callback(); return 0 },
  cancelAnimationFrame() {}, setTimeout(callback) { callback(); return 0 }, clearTimeout() {},
}, { filename: 'media/main.js' })

const click = (action, extra = {}) => listeners['root:click']({
  target: {
    closest(selector) {
      if (selector === '[data-action]') return { dataset: { action, ...extra } }
      return null
    },
  },
})

listeners['window:message']({
  data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture', selectedTab: 'overview',
    boot: {
      questProposals: [], companionActionProposals: [], projectAgents: [], profiles: [], flows: [], skills: [],
      runs: [], toolCatalog: [],
      connections: [{ id: 'conn-cli', provider: 'claude-code-cli', presetId: 'claude-code', displayName: 'Claude Code CLI' }],
      providerCatalog: [
        { id: 'llmux', name: 'LLMux', kind: 'openai-compatible', baseUrl: '', requiresApiKey: true },
        { id: 'claude-code', name: 'Claude Code CLI', kind: 'claude-code-cli', baseUrl: '', local: true },
      ],
      companion: { id: 'c1', preset: 'balanced', configured: true },
      companionMessages: [],
    },
    details: undefined,
  },
})

// Человек открывает настройку, выбирает Claude Code и подтверждает модель.
click('open-companion-setup')
const wizardHtmlAfterOpen = root.innerHTML
click('companion-select-mode', { mode: 'cli' })
click('companion-select-model', { model: 'opus' })

const before = posted.length
click('save-test-companion')
const saved = posted.slice(before).find(message => message.type === 'saveCompanionConfig' || message.type === 'saveCompanionConfigAndChat')

check('настройка уходит в ядро', Boolean(saved), 'сохранение не отправило ничего')
if (saved) {
  const config = saved.config || {}
  check('в ядро уходит именно локальный CLI',
    config.provider === 'claude-code-cli',
    `провайдер ${JSON.stringify(config.provider)} вместо claude-code-cli`)
  check('выбранная модель доезжает',
    config.model === 'opus',
    `модель ${JSON.stringify(config.model)} вместо opus`)
  check('адрес и подключение остаются пустыми',
    !config.baseUrl && !config.connectionId,
    `адрес ${JSON.stringify(config.baseUrl)}, подключение ${JSON.stringify(config.connectionId)}`)
}

// И тот же выбор виден на экране: мастер открывается с мозга, а после выбора
// показывает форму локального CLI, а не чужую. Пока кнопка открывала настройку
// на «Роли», человек выбирал Claude Code вслепую и видел прежний режим.
check('мастер открывается с выбора мозга',
  wizardHtmlAfterOpen.includes('Мозг компаньона'),
  'настройка открывается не с того шага, от которого зависит остальное')
check('после выбора виден локальный CLI',
  root.innerHTML.includes('Отвечает Claude Code на этой машине'),
  'мастер показывает другой мозг')

if (failures.length) {
  console.error('\nПРОВАЛЕНО:')
  for (const item of failures) console.error('  · ' + item)
  process.exit(1)
}
console.log('\nвыбор мозга доезжает до ядра')
