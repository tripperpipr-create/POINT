// Один выбор «подключение → модель» вместо четырёх копий.
//
// Раньше провайдера выбирали заново на четырёх экранах — связи, онбординг,
// настройка компаньона, конструктор агента, — и эти экраны расходились между
// собой. Контекстное окно при этом спрашивали числом, которого человек знать не
// может: провайдеры не отдают лимиты в списке моделей.
//
// Здесь проверяется то, ради чего это делалось: подключением можно управлять
// (проверить, переименовать, назначить по умолчанию, удалить), а пределы модели
// подставляются с честной подписью источника — либо «известна», либо прямое
// признание, что неизвестна.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

function open(tab, boot, layout = 'wide') {
  const listeners = {}
  const posted = []
  const root = {
    innerHTML: '',
    addEventListener(type, cb) { listeners[`root:${type}`] = cb },
    querySelector: () => null,
    querySelectorAll: () => [],
  }
  vm.runInNewContext(main, {
    acquireVsCodeApi: () => ({ postMessage(m) { posted.push(m) }, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout } } },
    window: { addEventListener(type, cb) { listeners[`window:${type}`] = cb } },
    console, Date, Map, Set,
    requestAnimationFrame(cb) { cb(); return 0 },
    cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
  }, { filename: 'main.js' })

  listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w', selectedTab: tab, boot,
  } })
  const click = dataset => listeners['root:click']({
    target: { closest: selector => (selector === '[data-action]' ? { dataset } : null) },
    preventDefault() {},
  })
  return { listeners, posted, root, click }
}

const providerCatalog = [
  { id: 'openai', name: 'OpenAI', kind: 'openai-compatible', baseUrl: 'https://api.openai.com/v1', requiresApiKey: true },
  { id: 'anthropic', name: 'Anthropic', kind: 'anthropic', baseUrl: 'https://api.anthropic.com/v1', requiresApiKey: true },
  { id: 'azure-openai', name: 'Azure OpenAI', kind: 'azure-openai', baseUrl: '', requiresApiKey: true },
]
// Каталог семейств приходит из ядра: ключ — префикс, а не полный идентификатор.
const modelCatalog = [
  { model: 'claude-sonnet-4', family: 'Claude Sonnet 4', contextWindow: 200000, maxOutput: 64000, capabilities: ['chat', 'tools'], state: 'known' },
  { model: 'gpt-4o', family: 'GPT-4o', contextWindow: 128000, maxOutput: 16384, capabilities: ['chat', 'tools'], state: 'known' },
]
const connections = [
  { id: 'c-work', provider: 'openai-compatible', presetId: 'openai', displayName: 'Рабочий ключ', baseUrl: 'https://api.openai.com/v1', status: 'connected', secretRef: 'point.connection.a', isDefault: true, models: [{ id: 'gpt-4o-mini', state: 'unknown' }] },
  { id: 'c-personal', provider: 'anthropic', presetId: 'anthropic', displayName: 'Личный ключ', baseUrl: 'https://api.anthropic.com/v1', status: 'unknown', secretRef: 'point.connection.b', models: [] },
]
const baseBoot = {
  onboarded: true, profiles: [], usageRecords: [], runs: [], quests: [],
  executions: [], changeSets: [], questProposals: [], serverProfiles: [], dbConnections: [],
  providerCatalog, modelCatalog, connections,
}

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${String(detail).slice(0, 220)}`)
}

// ── Экраном подключений можно управлять, а не только создавать ────────────
{
  const ui = open('connections', baseBoot, 'connections')
  const html = ui.root.innerHTML
  if (!html.includes('Рабочий ключ')) {
    console.error('экран связей не отрисовался — проверки прошли бы вхолостую')
    process.exit(1)
  }
  for (const [action, what] of [
    ['probe-connection', 'проверка'],
    ['edit-connection', 'правка'],
    ['default-connection', 'выбор по умолчанию'],
    ['delete-connection', 'удаление'],
  ]) {
    check(`подключением можно управлять: ${what}`,
      new RegExp(`data-action="${action}"[^>]*data-id="c-work"`).test(html),
      `нет действия ${action} у подключения`)
  }
  check('подключение по умолчанию помечено',
    /class="[^"]*is-default/.test(html),
    'пометка «по умолчанию» не видна — выбор снова придётся угадывать')

  // Каждое действие уходит ядру, а не остаётся мёртвой кнопкой.
  ui.click({ action: 'probe-connection', id: 'c-personal' })
  ui.click({ action: 'default-connection', id: 'c-personal' })
  ui.click({ action: 'delete-connection', id: 'c-personal' })
  for (const type of ['probeConnection', 'defaultConnection', 'deleteConnection']) {
    check(`действие подключено: ${type}`,
      ui.posted.some(message => message.type === type && message.id === 'c-personal'),
      `сообщение ${type} не отправлено`)
  }

  // Правка держится в интерфейсе и открывает форму с этим подключением.
  ui.click({ action: 'edit-connection', id: 'c-work' })
  check('правка открывает форму выбранного подключения',
    ui.root.innerHTML.includes('id="connection-id-edit"') && ui.root.innerHTML.includes('value="c-work"'),
    'форма правки не получила идентификатор подключения')
}

// ── Пределы модели подставляются с честным источником ─────────────────────
{
  const agent = {
    id: 'a1', name: 'SAGE-7', roleDescription: 'Разведка', systemPrompt: 'роль',
    connectionId: 'c-personal', provider: 'anthropic', providerPreset: 'anthropic',
    model: 'claude-sonnet-4-5', primaryModel: 'claude-sonnet-4-5',
    allowedTools: ['read_file'], maxSteps: 20, maxDurationSeconds: 600, approvalMode: 'safe',
    temperature: 0.2, maxOutputTokens: 4096, contextWindowTokens: 0, reasoningEffort: 'none',
  }
  const ui = open('agents', { ...baseBoot, profiles: [agent] })
  ui.click({ action: 'select-roster-profile', id: 'a1' })
  ui.click({ action: 'edit-roster-profile', id: 'a1' })
  ui.click({ action: 'profile-step', step: 'model' })
  const html = ui.root.innerHTML
  if (!html.includes('id="connection-id"')) {
    console.error('конструктор не показал выбор подключения — проверять нечего')
    process.exit(1)
  }
  // toLocaleString ставит неразрывный пробел — сравниваем по нормализованному
  // тексту, иначе проверка ловила бы формат, а не факт.
  const plain = html.replace(/[  ]/g, ' ')
  check('известная модель называет контекст и источник',
    plain.includes('200 000') && plain.includes('справочник') && plain.includes('Claude Sonnet 4'),
    'пределы известной модели не подставились или не подписан источник')
  check('в конструкторе нет своей сетки провайдеров',
    !html.includes('name="provider-preset"'),
    'вернулся отдельный выбор провайдера — экранов снова два')
  check('подключение выбирается по имени',
    html.includes('Личный ключ') && html.includes('Рабочий ключ'),
    'список подключений в конструкторе пуст')
}

// ── Конструктор Хаба выбирает связь тем же контролом ─────────────────────────
// Это была последняя своя копия: сетка провайдеров радиокнопками, отдельное
// поле адреса и свой список моделей. Связь при этом никуда не сохранялась —
// domain.ProjectAgent.ConnectionID оставался пустым, и ключ подбирался
// совпадением пресета: при двух ключах одного провайдера запрос уходил с чужим.
{
  const agent = {
    id: 'a3', blueprintId: 'b3', name: 'SAGE-7', roleDescription: 'Разведка', systemPrompt: 'роль',
    connectionId: 'c-personal', provider: 'anthropic', providerPreset: 'anthropic',
    model: 'claude-sonnet-4-5', primaryModel: 'claude-sonnet-4-5',
    allowedTools: ['read_file'], maxSteps: 20, maxDurationSeconds: 600, approvalMode: 'safe',
    temperature: 0.2, maxOutputTokens: 4096, contextWindowTokens: 0, reasoningEffort: 'none',
  }
  const ui = open('agents', { ...baseBoot, projectAgents: [agent], blueprints: [], profileTemplates: [] })
  ui.click({ action: 'select-roster-profile', id: 'a3' })
  ui.click({ action: 'open-agent-constructor-edit', id: 'a3' })
  ui.click({ action: 'constructor-step', step: 'brain' })
  const html = ui.root.innerHTML
  if (!html.includes('data-step-panel="brain"')) {
    console.error('шаг «Мозг» конструктора Хаба не отрисован — проверять нечего')
    process.exit(1)
  }
  check('конструктор Хаба показывает общий выбор связи',
    html.includes('id="connection-id"') && html.includes('id="model"'),
    'на шаге «Мозг» нет общего контрола «подключение -> модель»')
  check('в конструкторе Хаба нет своей сетки провайдеров',
    !html.includes('name="constructor-provider"') && !html.includes('id="constructor-base-url"'),
    'вернулись радиокнопки провайдера или своё поле адреса')
  // Вложенный <form> запрещён в HTML: разбор молча выбрасывает внутренний тег,
  // поля остаются, а кнопка отправляет объемлющую форму. Панели конструктора
  // живут внутри #constructor-form, поэтому связь здесь заводится теми же
  // полями, но без своего <form> и с сохранением по действию.
  check('связь заводится тут же, не уходя на другой экран',
    html.includes('id="connection-provider"') && html.includes('id="connection-api-key"') && html.includes('data-action="save-connection"'),
    'нет полей подключения рядом с выбором или нечем сохранить')
  check('внутри формы конструктора нет второй формы',
    (html.match(/<form/g) || []).length === 1,
    `<form> в разметке конструктора: ${(html.match(/<form/g) || []).length} — вложенную браузер выбросит молча`)

  ui.click({ action: 'constructor-step', step: 'review' })
  ui.click({ action: 'save-constructor' })
  const save = ui.posted.findLast(message => message.type === 'saveProjectAgent')
  check('сохранённый агент несёт ссылку на подключение',
    save?.agent?.connectionId === 'c-personal',
    `в payload connectionId=${JSON.stringify(save?.agent?.connectionId)}`)
}

// ── Незнакомая модель признаётся незнакомой ───────────────────────────────
{
  const agent = {
    id: 'a2', name: 'FORGE', roleDescription: 'Реализация', systemPrompt: 'роль',
    connectionId: 'c-work', provider: 'openai-compatible', providerPreset: 'openai',
    model: 'внутренняя-модель-компании', primaryModel: 'внутренняя-модель-компании',
    allowedTools: ['read_file'], maxSteps: 20, maxDurationSeconds: 600, approvalMode: 'safe',
    temperature: 0.2, maxOutputTokens: 4096, contextWindowTokens: 0, reasoningEffort: 'none',
  }
  const ui = open('agents', { ...baseBoot, profiles: [agent] })
  ui.click({ action: 'select-roster-profile', id: 'a2' })
  ui.click({ action: 'edit-roster-profile', id: 'a2' })
  ui.click({ action: 'profile-step', step: 'model' })
  const html = ui.root.innerHTML
  check('незнакомая модель не получает выдуманных пределов',
    html.includes('Модель незнакома'),
    'интерфейс промолчал о том, что пределы модели неизвестны')
  check('в этом случае человека просят указать значение',
    html.includes('вручную'),
    'не сказано, что контекстное окно нужно задать самому')
}

if (failures.length) {
  console.error('\nВЫБОР МОДЕЛИ ПРОВАЛЕН:')
  for (const message of failures) console.error('  · ' + message)
  process.exit(1)
}
console.log('\nподключение и модель выбираются одним контролом')
