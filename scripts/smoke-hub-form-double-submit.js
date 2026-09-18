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
  const fields = { '#team-name': { value: '' }, '#team-description': { value: '' } }
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
    name: value => { fields['#team-name'].value = value },
    submit: () => listeners['root:submit']({ target: { id: 'team-form' }, preventDefault() {} }),
    saves: () => posted.filter(m => m.type === 'saveTeam').length,
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
  ui.state()
  ui.submit()
  check('после ответа форма снова работает', ui.saves() === 2, `сохранений ${ui.saves()}`)
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

if (failures.length) {
  console.log('ПОВТОРНАЯ ОТПРАВКА ФОРМ — ПРОВАЛ:')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('повторная отправка форм: PASS')
