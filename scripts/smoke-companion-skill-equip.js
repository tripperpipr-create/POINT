// Навык помощника доезжает до ядра и не теряется по дороге.
//
// Экран настройки помощника научился отмечать навыки, а бэкенд умел их хранить
// с самого начала. Между ними стоял `companionConfigPayload`, который в тело
// запроса `skillIds` не клал вовсе: отметка жила до нажатия «Сохранить» и
// исчезала молча. Здесь проверяется весь путь — форма, черновик, тело запроса —
// и два правила, которые ядро проверяет у себя: навык с недоступным помощнику
// умением не надевается, а надетый ранее не пропадает из-за того, что каталог
// на экране его сейчас не показывает.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${detail}`)
}

// Каталог тот же, что у ядра: read_file читает проект, run_command исполняет.
// Помощнику выдана только читающая часть.
const toolCatalog = [
  { name: 'read_file', displayName: 'Чтение файлов', category: 'read', risk: 'LOW', description: 'Читает файлы проекта.' },
  { name: 'search_code', displayName: 'Поиск по коду', category: 'index', risk: 'LOW', description: 'Ищет по индексу.' },
  { name: 'run_command', displayName: 'Запуск команд', category: 'execute', risk: 'CRITICAL', description: 'Запускает команду.' },
]
const skills = [
  { id: 'sk-review', name: 'Ревью по чек-листу', description: 'Единый разбор правок', requiredTools: ['read_file'] },
  { id: 'sk-tests', name: 'Прогон тестов', description: 'Запускает и разбирает падения', requiredTools: ['run_command'] },
]
const connection = {
  id: 'conn-companion', provider: 'ollama', presetId: 'ollama',
  displayName: 'Локальный Ollama', baseUrl: 'http://127.0.0.1:11434', status: 'connected',
}
const source = fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'media', 'main.js'), 'utf8')

// Черновик настройки живёт, пока мастер открыт, и переживает обновление boot —
// это его продуктовое поведение. Каждый сценарий поэтому получает своё
// окружение и открывает мастер заново, как это делает человек.
function studio(companion, inputs) {
  const listeners = {}
  const posted = []
  const skillInputs = inputs.map(item => ({ ...item }))
  const root = {
    innerHTML: '',
    addEventListener(type, callback) { listeners['root:' + type] = callback },
    querySelector() { return null },
    querySelectorAll(selector) { return selector === 'input[name="companion-skill"]' ? skillInputs : [] },
  }
  const context = {
    acquireVsCodeApi: () => ({
      postMessage(message) { posted.push(message) },
      getState() { return undefined },
      setState() { },
    }),
    document: { getElementById: id => id === 'root' ? root : undefined, body: { dataset: { layout: 'wide' } } },
    window: { addEventListener(type, callback) { listeners['window:' + type] = callback } },
    console, Date, Map, Set,
    requestAnimationFrame(callback) { callback(); return 0 },
    cancelAnimationFrame() { },
    setTimeout(callback) { callback(); return 0 },
    clearTimeout() { },
  }
  vm.runInNewContext(source, context, { filename: 'media/main.js' })
  listeners['window:message']({
    data: {
      type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture', selectedTab: 'overview',
      boot: {
        blueprints: [], projectAgents: [], profiles: [], runs: [], connections: [connection],
        skills, projectSkills: [], toolCatalog, companion,
      },
      details: undefined,
    },
  })
  const target = dataset => ({ dataset, closest(selector) { return selector === '[data-action]' ? this : null } })
  listeners['root:click']({ target: target({ action: 'open-companion-setup', step: 'skills' }) })
  return {
    context, root, posted,
    submit() { listeners['root:submit']({ target: { id: 'companion-setup-form' }, preventDefault() { } }) },
    lastSave() { return [...posted].reverse().find(item => item && item.type === 'saveCompanionConfig') },
  }
}

const base = {
  id: 'c1', preset: 'balanced', configured: true, skillIds: [],
  connectionId: connection.id, provider: connection.provider, providerPreset: connection.presetId,
  baseUrl: connection.baseUrl, model: 'qwen:7b',
}
const both = [{ value: 'sk-review', checked: false }, { value: 'sk-tests', checked: false }]
const marked = ids => both.map(item => ({ ...item, checked: ids.includes(item.value) }))
const sent = stand => (stand.lastSave() && stand.lastSave().config && stand.lastSave().config.skillIds) || undefined

// 1. Правило доступа — то же, что у ядра: читающее умение проходит,
//    исполняющее нет. Оно решает, какие карточки вообще можно отметить.
{
  const stand = studio(base, both)
  const blockers = skill => stand.context.companionSkillBlockers(skill)
  check('читающий навык помощнику доступен',
    blockers(skills[0]).length === 0,
    'навык с read_file объявлен недоступным: ' + JSON.stringify(blockers(skills[0])))
  check('исполняющий навык назван поимённо',
    blockers(skills[1]).join(',') === 'run_command',
    'помеха названа неверно: ' + JSON.stringify(blockers(skills[1])))
  check('незнакомое умение не выдаётся группой',
    blockers({ requiredTools: ['custom-deploy'] }).join(',') === 'custom-deploy',
    'инструмент вне каталога прошёл как разрешённый')
  check('недоступный навык нельзя отметить',
    /value="sk-tests"[^>]*disabled/.test(stand.root.innerHTML) && !/value="sk-review"[^>]*disabled/.test(stand.root.innerHTML),
    'карточки навыков не различают доступ помощника')
}

// 2. Отметка доезжает до ядра. Это и есть потерянное звено: без skillIds в теле
//    запроса экран показывал выбор, которого сохранение не знало.
{
  const stand = studio(base, marked(['sk-review']))
  stand.submit()
  check('надетый навык уходит в ядро',
    (sent(stand) || []).join(',') === 'sk-review',
    'в теле запроса: ' + JSON.stringify(sent(stand)))
}

// 3. Снятие последнего навыка — тоже решение человека, и доехать оно обязано.
{
  const stand = studio({ ...base, skillIds: ['sk-review'] }, marked([]))
  stand.submit()
  check('снятая отметка не подменяется прежней',
    Array.isArray(sent(stand)) && sent(stand).length === 0,
    'в теле запроса: ' + JSON.stringify(sent(stand)))
}

// 4. Навык, которого каталог сейчас не показывает, остаётся надетым: он
//    приезжает переносом или продвижением выученного, и форма о нём не знает.
{
  const stand = studio({ ...base, skillIds: ['sk-review', 'sk-portable'] }, marked(['sk-review']))
  stand.submit()
  check('невидимый в каталоге навык не снимается формой',
    (sent(stand) || []).includes('sk-portable'),
    'в теле запроса: ' + JSON.stringify(sent(stand)))
}

// 5. Навык, разошедшийся с доступом уже после экипировки, останавливает
//    сохранение с названной причиной, а не отказом ядра после «Сохранить».
{
  const stand = studio({ ...base, skillIds: ['sk-tests'] }, marked(['sk-tests']))
  stand.submit()
  check('конфликтная настройка не уходит в ядро',
    stand.lastSave() === undefined,
    'запрос всё же ушёл: ' + JSON.stringify(sent(stand)))
  check('причина отказа названа на экране',
    /Прогон тестов/.test(stand.root.innerHTML) && /run_command/.test(stand.root.innerHTML),
    'на экране нет ни имени навыка, ни недостающего умения')
}

if (failures.length) {
  console.error('\nПРОВАЛЕНО:')
  for (const line of failures) console.error('  · ' + line)
  process.exit(1)
}
console.log('\nнавык помощника доезжает до ядра')
