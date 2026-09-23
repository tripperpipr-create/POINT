// Повторная отправка формы не создаёт вторую сущность.
//
// Четырнадцать форм Хаба создают сущности — агента, отряд, навык, подключение.
// Двойной щелчок или второе Enter между отправкой и ответом ядра порождали
// второго агента и второй отряд. Заводить флаг в каждой форме значило бы
// четырнадцать раз повторить одно правило, поэтому запрет один на всех.
//
// Тонкое место — форма, не прошедшая проверку. Она ничего не отправляет, и
// запирать её нельзя: человек исправит поле и отправит снова. Поэтому запирает
// не нажатие, а состоявшаяся отправка.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

function open() {
  const listeners = {}
  const posted = []
  const fields = {
    '#team-name': { value: '' }, '#team-description': { value: '' },
    '#experience-search-query': { value: '' },
    '#manual-learning-agent': { value: 'agent-1' },
    '#manual-learning-kind': { value: 'memory' },
    '#manual-learning-scope': { value: 'project' },
    '#manual-learning-content': { value: '' },
    '#budget-daily': { value: '' }, '#budget-monthly': { value: '' },
    '#budget-hard-stop': { checked: false },
  }
  const root = {
    innerHTML: '',
    addEventListener(type, cb) { listeners[`root:${type}`] = cb },
    querySelector: sel => fields[sel] || null,
    querySelectorAll: () => [],
  }
  vm.runInNewContext(main, {
    acquireVsCodeApi: () => ({ postMessage(m) { posted.push(m) }, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
    window: { addEventListener(type, cb) { listeners[`window:${type}`] = cb } },
    console, Date, Map, Set,
    requestAnimationFrame(cb) { cb(); return 0 },
    cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
  }, { filename: 'main.js' })

  const boot = {
    onboarded: true, profiles: [], projectAgents: [], usageRecords: [],
    runs: [], quests: [], executions: [], changeSets: [], teams: [],
  }
  const state = () => listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true,
    workspace: 'w', selectedTab: 'teams', boot,
  } })
  state()
  return {
    listeners, state,
    ack: type => listeners['window:message']({ data: { type } }),
    name: value => { fields['#team-name'].value = value },
    field: (selector, value) => { fields[selector].value = value },
    submitForm: id => listeners['root:submit']({ target: { id }, preventDefault() {} }),
    submit: () => listeners['root:submit']({ target: { id: 'team-form' }, preventDefault() {} }),
    saves: () => posted.filter(m => m.type === 'saveTeam').length,
    sent: type => posted.filter(m => m.type === type).length,
  }
}

const failures = []
const check = (name, ok, detail) => { if (!ok) failures.push(`${name}: ${detail}`) }

// Защита от холостого хода: если форма вообще не отправляется, все проверки
// «не более одного» сойдутся на нуле и не будут значить ничего.
{
  const ui = open()
  ui.name('Кузнецы')
  ui.submit()
  if (ui.saves() !== 1) {
    console.log(`форма отряда не отправилась (${ui.saves()}) — проверки прошли бы вхолостую`)
    process.exit(1)
  }
}

{
  const ui = open()
  ui.name('Кузнецы')
  ui.submit()
  ui.submit()
  ui.submit()
  check('три отправки создают один отряд', ui.saves() === 1, `сохранений ${ui.saves()}`)
}

{
  const ui = open()
  ui.name('Кузнецы')
  ui.submit()
  ui.ack('teamSaved')
  ui.state()
  ui.submit()
  check('после ответа форма снова работает', ui.saves() === 2, `сохранений ${ui.saves()}`)
}

// Фоновый state — не ответ формы. Polling может прислать его, пока saveTeam
// ещё выполняется; разблокирование здесь снова разрешило бы дубль.
{
  const ui = open()
  ui.name('Кузнецы')
  ui.submit()
  ui.state()
  ui.submit()
  check('чужой state не снимает замок формы', ui.saves() === 1, `сохранений ${ui.saves()}`)
  ui.ack('teamSaved')
  ui.submit()
  check('точное подтверждение снимает замок формы', ui.saves() === 2, `сохранений ${ui.saves()}`)
}

// Незаполненная форма ничего не отправляет — и запираться не должна.
{
  const ui = open()
  ui.submit()
  check('пустая форма ничего не создаёт', ui.saves() === 0, `сохранений ${ui.saves()}`)
  ui.name('Кузнецы')
  ui.submit()
  check('после исправления поля форма отправляется', ui.saves() === 1, `сохранений ${ui.saves()}`)
}

// Отказ ядра отпирает форму: иначе повторить будет нечем.
{
  const ui = open()
  ui.name('Кузнецы')
  ui.submit()
  ui.listeners['window:message']({ data: { type: 'error', message: 'ядро не ответило' } })
  ui.submit()
  check('после отказа можно повторить', ui.saves() === 2, `сохранений ${ui.saves()}`)
}

// Отказ соседнего фонового запроса не является ответом на сохранение. Пока
// saveTeam ещё выполняется, такой отказ не должен разрешать второй submit.
{
  const ui = open()
  ui.name('Кузнецы')
  ui.submit()
  ui.listeners['window:message']({ data: { type: 'error', request: 'loadDocker', message: 'Docker не ответил' } })
  ui.submit()
  check('чужой отказ не снимает замок формы', ui.saves() === 1, `сохранений ${ui.saves()}`)
  ui.listeners['window:message']({ data: { type: 'error', request: 'saveTeam', message: 'отряд не сохранён' } })
  ui.submit()
  check('свой отказ снимает замок формы', ui.saves() === 2, `сохранений ${ui.saves()}`)
}

// Некоторые повторяемые формы получают не полный `state`, а свой точный ответ.
// Визуально кнопка уже снова активна, поэтому и общий замок обязан отпуститься
// на этом ответе. Именно эти три маршрута раньше работали только один раз за
// открытие панели.
{
  const ui = open()
  ui.field('#experience-search-query', 'verification')
  ui.submitForm('experience-search-form')
  ui.listeners['window:message']({ data: { type: 'experienceSearch', query: 'verification', items: [] } })
  ui.submitForm('experience-search-form')
  check('поиск по опыту можно повторить после результата', ui.sent('searchExperience') === 2,
    `поисков ${ui.sent('searchExperience')}`)
}

{
  const ui = open()
  ui.field('#manual-learning-content', 'Всегда проверять миграции')
  ui.submitForm('manual-learning-form')
  ui.listeners['window:message']({ data: { type: 'manualLearningPreview', request: {}, preview: {} } })
  ui.submitForm('manual-learning-form')
  check('preview ручного урока можно пересобрать', ui.sent('previewManualLearning') === 2,
    `предпросмотров ${ui.sent('previewManualLearning')}`)
}

{
  const ui = open()
  ui.field('#budget-daily', '10')
  ui.submitForm('budget-form')
  ui.ack('budgetSaved')
  ui.listeners['window:message']({ data: { type: 'statistics', statistics: {} } })
  ui.submitForm('budget-form')
  check('бюджет можно сохранить повторно после ответа', ui.sent('saveBudget') === 2,
    `сохранений ${ui.sent('saveBudget')}`)
}

// Ошибка локальной валидации не является начавшимся запросом. После неё форма
// должна остаться рабочей: человек исправляет число и сохраняет без перезапуска
// панели. Проверяем не только отсутствие первого запроса, но и успешный второй.
{
  const ui = open()
  ui.field('#budget-daily', '-1')
  ui.submitForm('budget-form')
  check('отрицательный бюджет не отправляется', ui.sent('saveBudget') === 0,
    `сохранений ${ui.sent('saveBudget')}`)
  ui.field('#budget-daily', '12.50')
  ui.submitForm('budget-form')
  check('после исправления бюджет отправляется', ui.sent('saveBudget') === 1,
    `сохранений ${ui.sent('saveBudget')}`)
}

if (failures.length) {
  console.log('ПОВТОРНАЯ ОТПРАВКА ФОРМ — ПРОВАЛ:')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('повторная отправка форм: PASS')
