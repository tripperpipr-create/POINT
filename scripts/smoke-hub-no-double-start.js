// Одна задача — один запуск.
//
// Форма запуска отправляется и щелчком, и по Enter. Между отправкой и ответом
// ядра проходит время, а задача и отпечаток разведки остаются на месте — три
// отправки давали три прогона одной задачи: три агента в одних файлах и
// тройной расход. В отличие от кнопок, несущих собственный id, повтор здесь
// порождает новую сущность, а не повторяет старую.
//
// Проверяем и обратное: после ответа ядра и после отказа запуск обязан снова
// стать возможным, иначе форма запрётся навсегда.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

function ready() {
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
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
    window: { addEventListener(type, cb) { listeners[`window:${type}`] = cb } },
    console, Date, Map, Set,
    requestAnimationFrame(cb) { cb(); return 0 },
    cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
  }, { filename: 'main.js' })

  const boot = {
    onboarded: true,
    profiles: [{ id: 'p1', name: 'Кузнец', provider: 'openai', model: 'gpt-4', connectionId: 'c1' }],
    connections: [{ id: 'c1', status: 'connected' }],
    projectAgents: [], usageRecords: [], runs: [], quests: [], executions: [], changeSets: [],
  }
  listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true,
    workspace: 'w', selectedTab: 'quests', boot,
  } })
  // Задача приходит готовым примером — тот же путь, что и ввод руками.
  listeners['root:click']({
    target: { closest: sel => (sel === '[data-example]' ? { dataset: { example: 'починить сборку' } } : null) },
    preventDefault() {},
  })
  // Разведка выполнена: у предпросмотра есть отпечаток, без него форма заперта.
  listeners['window:message']({ data: { type: 'agentRunPreview', preview: { fingerprint: 'fp-1', completion: {} } } })

  const submit = () => listeners['root:submit']({ target: { id: 'agent-form' }, preventDefault() {} })
  // Расширение после запуска шлёт runStarted и следом состояние мира
  // (extension.js: this.post({type:'runStarted'}); this.postState()). Фикстура
  // повторяет именно эту пару: по одному лишь runStarted форма не отпирается.
  const answered = () => {
    listeners['window:message']({ data: { type: 'runStarted', runId: 'r1' } })
    listeners['window:message']({ data: {
      type: 'state', service: { state: 'running' }, workspaceTrusted: true,
      workspace: 'w', selectedTab: 'quests', boot,
    } })
  }
  const starts = () => posted.filter(m => m.type === 'startRun').length
  // Следующая задача: снова задача и снова разведка — как у человека.
  const nextTask = text => {
    listeners['root:click']({
      target: { closest: sel => (sel === '[data-example]' ? { dataset: { example: text } } : null) },
      preventDefault() {},
    })
    listeners['window:message']({ data: { type: 'agentRunPreview', preview: { fingerprint: 'fp-2', completion: {} } } })
  }
  return { listeners, submit, starts, nextTask, answered }
}

const failures = []
const check = (name, ok, detail) => { if (!ok) failures.push(`${name}: ${detail}`) }

// Защита от холостого хода: если фикстура не доезжает до запуска, все проверки
// «не более одного» прошли бы на нуле и не значили бы ничего.
{
  const ui = ready()
  ui.submit()
  if (ui.starts() !== 1) {
    console.log(`фикстура не доехала до запуска (${ui.starts()}) — проверки прошли бы вхолостую`)
    process.exit(1)
  }
}

{
  const ui = ready()
  ui.submit()
  ui.submit()
  ui.submit()
  check('три отправки дают один запуск', ui.starts() === 1, `запусков ${ui.starts()}`)
}

// Главный смысл снятия флага: человек запускает задачу, дожидается ответа и
// берётся за следующую. Если флаг не снять, форма заперта до перезагрузки
// панели — вторая задача не запустится никогда.
{
  const ui = ready()
  ui.submit()
  ui.answered()
  ui.nextTask('переписать тесты')
  ui.submit()
  check('следующую задачу можно запустить', ui.starts() === 2, `запусков ${ui.starts()}`)
}

{
  const ui = ready()
  ui.submit()
  ui.listeners['window:message']({ data: { type: 'error', message: 'ядро не ответило' } })
  ui.submit()
  check('после отказа запуск можно повторить', ui.starts() === 2, `запусков ${ui.starts()}`)
}

// Ошибка параллельного фонового запроса не означает, что startRun завершился.
// Иначе она снимет локальный guard, и повторный Enter запустит второго агента
// поверх всё ещё создаваемого первого прогона.
{
  const ui = ready()
  ui.submit()
  ui.listeners['window:message']({ data: { type: 'error', request: 'loadDocker', message: 'Docker не ответил' } })
  ui.submit()
  check('чужой отказ не разрешает второй запуск', ui.starts() === 1, `запусков ${ui.starts()}`)
  ui.listeners['window:message']({ data: { type: 'error', request: 'startRun', message: 'запуск не создан' } })
  ui.submit()
  check('свой отказ разрешает повтор запуска', ui.starts() === 2, `запусков ${ui.starts()}`)
}

if (failures.length) {
  console.log('ЗАПУСК ЗАДАЧИ — ПРОВАЛ:')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('одна задача — один запуск: PASS')
