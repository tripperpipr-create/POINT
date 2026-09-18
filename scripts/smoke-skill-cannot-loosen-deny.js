// Навык не ослабляет запрет, поставленный человеком.
//
// На шаге «Навыки» написано: «Отметка навыка включает его required tools в
// allowlist. Skill сам не обходит DENY». Первое было правдой, второе — нет:
// у встроенного навыка Code Review есть permissionDelta {read_file: ALLOW}, и
// отметка навыка молча переписывала им политику. Человек ставил read_file в
// DENY осознанно — «этот агент файлы не читает», — а после галочки в профиле
// стояло ALLOW, и ни строчки об этом на экране.
//
// Правило: навык может дозаписать политику там, где её не было, и может
// ужесточить. Ослабить выбранное человеком — нет; вместо этого остаётся
// предупреждение о нехватке, которое конструктор и так умеет показывать.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

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
    setState() { },
  }),
  document: { getElementById: id => id === 'root' ? root : undefined, body: { dataset: { layout: 'narrow' } } },
  window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
  console, Date, Map, Set,
  requestAnimationFrame(callback) { callback(); return 0 },
  cancelAnimationFrame() { },
  setTimeout(callback) { callback(); return 0 },
  clearTimeout() { },
}

const source = fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'media', 'main.js'), 'utf8')
vm.runInNewContext(source, context, { filename: 'media/main.js' })

// Навыки и их дельты приходят из состояния мира — как в настоящем ядре.
listeners['window:message']({
  data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture', selectedTab: 'agents',
    boot: {
      skills: [
        { id: 'skill-code-review', name: 'Code Review', requiredTools: ['read_file', 'search_text'], permissionDelta: { read_file: 'ALLOW' } },
        { id: 'skill-test-runner', name: 'Test Runner', requiredTools: ['run_command'], permissionDelta: { run_command: 'ASK' } },
      ],
      profiles: [], projectAgents: [], blueprints: [], runs: [], toolCatalog: [],
    },
    details: undefined,
  },
})

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${detail}`)
}

const apply = context.applySkillToolGrants
if (typeof apply !== 'function') throw new Error('applySkillToolGrants недоступна — проверять нечего')

// 1. Человек запретил чтение файлов и отмечает навык, который его требует.
const denied = { allowedTools: ['search_text'], toolPolicies: { read_file: 'DENY' }, skillIds: ['skill-code-review'] }
apply(denied, [])
check('DENY человека переживает отметку навыка',
  denied.toolPolicies.read_file === 'DENY',
  `после навыка политика read_file стала ${JSON.stringify(denied.toolPolicies.read_file)}`)

// 2. Нехватка обязана быть названа: молчаливого запрета мало, человеку нужно
//    понимать, почему навык не заработает.
const warning = context.constructorSkillGapHtml(denied)
check('нехватка названа человеку',
  warning.includes('read_file') && /политика запрещает/.test(warning),
  `конструктор не объяснил запрещённый read_file: ${JSON.stringify(warning)}`)

// 3. Там, где человек ничего не выбирал, навык политику дозаписывает.
const blank = { allowedTools: [], toolPolicies: {}, skillIds: ['skill-test-runner'] }
apply(blank, [])
check('пустую политику навык заполняет',
  blank.toolPolicies.run_command === 'ASK',
  `ожидали ASK, получили ${JSON.stringify(blank.toolPolicies.run_command)}`)

// 4. Ужесточение проходит: ALLOW → ASK навык поставить вправе.
const loose = { allowedTools: ['run_command'], toolPolicies: { run_command: 'ALLOW' }, skillIds: ['skill-test-runner'] }
apply(loose, [])
check('ужесточение навыком разрешено',
  loose.toolPolicies.run_command === 'ASK',
  `ожидали ASK, получили ${JSON.stringify(loose.toolPolicies.run_command)}`)

// 5. Умение в allowlist навык добавляет — это обещание экрана и оно остаётся.
check('required tools попадают в allowlist',
  blank.allowedTools.includes('run_command'),
  `allowlist после навыка: ${JSON.stringify(blank.allowedTools)}`)

if (failures.length) {
  console.error('\nПРОВАЛЕНО:')
  for (const line of failures) console.error('  · ' + line)
  process.exit(1)
}
console.log('\nнавык не ослабляет запрет человека')
