// Что можно сделать с готовой репликой Мастера.
//
// Разговор был доступен только для чтения: ответ нельзя было ни скопировать, ни
// проверить, ни оценить, ни переспросить иначе. Панель под репликой закрывает
// всё это разом, и у каждой её кнопки своё правило, которое глазами не видно:
// «Ответить иначе» обязана переспросить прежний вопрос и попросить другой путь,
// «Сведения» — принести с собой вопрос, на который отвечали, а вторая оценка
// того же вида — снять отметку, а не подтвердить её.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

const HISTORY = [
  { id: 'mu-1', role: 'user', content: 'Что можешь сказать по проекту?' },
  { id: 'ma-1', role: 'assistant', content: 'В ростере один агент.', mode: 'model', model: 'qwen', inputTokens: 900, outputTokens: 120, latencyMs: 4200 },
  { id: 'mu-2', role: 'user', content: 'Расскажи про сам проект' },
  { id: 'ma-2', role: 'assistant', content: 'Не понял вопроса.', mode: 'deterministic', fallbackReason: 'invalid character' },
]

function open(history = HISTORY) {
  const listeners = {}
  const posted = []
  const field = { id: 'master-input', value: '', rows: 2, focus() {}, setSelectionRange() {}, closest: () => null, matches: () => false }
  const root = {
    innerHTML: '',
    addEventListener(type, callback) { listeners[`root:${type}`] = callback },
    querySelector: selector => (selector === '#master-input' ? field : null),
    querySelectorAll: () => [],
  }
  vm.runInNewContext(main, {
    acquireVsCodeApi: () => ({ postMessage(message) { posted.push(message) }, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'wide' } } },
    window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
    console, Date, Map, Set, TextEncoder,
    requestAnimationFrame(callback) { callback(); return 0 },
    cancelAnimationFrame() {}, setTimeout(callback) { callback(); return 0 }, clearTimeout() {},
  }, { filename: 'main.js' })

  listeners['window:message']({ data: {
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w',
    selectedTab: 'master',
    boot: {
      onboarded: true, profiles: [], projectAgents: [], usageRecords: [],
      runs: [], quests: [], executions: [], changeSets: [], questProposals: [],
      orchestrator: { id: 'o1', preset: 'conductor', model: 'qwen' },
    },
  } })
  listeners['window:message']({ data: { type: 'master', master: {
    configured: true, config: { model: 'qwen' }, history,
  } } })

  const click = dataset => listeners['root:click']({
    target: { closest: selector => (selector === '[data-action]' ? { dataset } : null) },
    preventDefault() {},
  })
  return { listeners, posted, root, field, click }
}

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${String(detail).slice(0, 260)}`)
}
const last = (ui, type) => [...ui.posted].reverse().find(message => message.type === type)

// ── Панель есть у обеих сторон, но состав разный ───────────────────────────
{
  const ui = open()
  const html = ui.root.innerHTML
  check('панель стоит под репликой', html.includes('hall-msg-tools'),
    'разговор снова только для чтения')
  check('панель не спрятана из потока', !/hall-msg-tools[^"]*is-hidden/.test(html),
    'убранная из потока панель выпадает из обхода клавиатурой')
  check('копирование есть у каждой реплики',
    (html.match(/copy-master-message/g) || []).length === HISTORY.length,
    'копировать можно не всё, что сказано')
  check('оценка только у ответов Мастера',
    (html.match(/data-action="master-feedback"/g) || []).length === 4,
    'по две кнопки на каждый из двух ответов — иначе оценивают чужое')
  // Происхождение ответа и действия над ним — один подвал хода, а не две
  // строки: каждый ход занимал двумя строками больше, чем нужно.
  check('чем отвечено и что сделать — одна строка',
    /hall-turn-foot[\s\S]{0,600}?hall-answer-badge[\s\S]{0,600}?hall-msg-tools/.test(html),
    'подвал хода снова разъехался на две строки')
  check('имя говорящего не дублирует кружок',
    html.includes('hall-speaker-name'),
    'имя выведено голым текстом: скрыть его для глаза, не потеряв для читалки, нечем')
  check('своей реплике оценка не предлагается',
    !/is-mine[\s\S]{0,400}master-feedback/.test(html),
    'человеку предложено оценить самого себя')
}

// ── «Ответить иначе» — только там, где путей больше одного ─────────────────
{
  const ui = open()
  check('модельному ответу предложен другой путь',
    (ui.root.innerHTML.match(/regenerate-master-message/g) || []).length === 1,
    'кнопка стоит либо нигде, либо и у детерминированного ответа тоже')

  ui.click({ action: 'regenerate-master-message', message: 'Что можешь сказать по проекту?' })
  const sent = last(ui, 'masterChat')
  check('переспрошен прежний вопрос', sent?.message === 'Что можешь сказать по проекту?',
    'ушло не то, на что отвечали: ' + JSON.stringify(sent))
  check('просьба о другом пути доехала', sent?.retry === true,
    'без признака повтора модель вернёт тот же ответ слово в слово')
}

// ── Копирование и сведения ─────────────────────────────────────────────────
{
  const ui = open()
  ui.click({ action: 'copy-master-message', id: 'ma-1' })
  check('копируется текст той реплики, которую просили',
    last(ui, 'copyMasterText')?.text === 'В ростере один агент.',
    JSON.stringify(last(ui, 'copyMasterText')))

  ui.click({ action: 'master-message-details', id: 'ma-2' })
  const details = last(ui, 'openMasterMessageDetails')
  check('сведения несут саму реплику', details?.item?.id === 'ma-2',
    JSON.stringify(details?.item))
  check('сведения несут вопрос, на который отвечали',
    details?.request === 'Расскажи про сам проект',
    'ответ объясняет сам себя: ' + JSON.stringify(details?.request))
}

// ── Оценка ставится и снимается ────────────────────────────────────────────
{
  const ui = open()
  ui.click({ action: 'master-feedback', id: 'ma-1', value: 'up' })
  check('оценка ушла в ядро',
    last(ui, 'masterFeedback')?.messageId === 'ma-1' && last(ui, 'masterFeedback')?.value === 'up',
    JSON.stringify(last(ui, 'masterFeedback')))

  // Ядро отвечает обновлённой перепиской — оценка живёт при реплике, а не в
  // состоянии рабочей области, как у компаньона.
  const marked = HISTORY.map(item => (item.id === 'ma-1' ? { ...item, feedback: 'up' } : item))
  ui.listeners['window:message']({ data: { type: 'master', master: {
    configured: true, config: { model: 'qwen' }, history: marked,
  } } })
  check('поставленная отметка видна без наведения',
    /hall-msg-tools is-marked/.test(ui.root.innerHTML),
    'отметка исчезает вместе с курсором — её поставят второй раз')
  check('отмечена та кнопка, которую нажали',
    /class="is-on" data-action="master-feedback" data-id="ma-1" data-value="up"/.test(ui.root.innerHTML),
    'подсвечена не та кнопка или ни одной')

  ui.click({ action: 'master-feedback', id: 'ma-1', value: 'up' })
  check('вторая такая же оценка снимает отметку',
    last(ui, 'masterFeedback')?.value === '',
    'передумать нельзя: ' + JSON.stringify(last(ui, 'masterFeedback')))
}

if (failures.length) {
  console.log('ПАНЕЛЬ ДЕЙСТВИЙ МАСТЕРА СЛОМАНА:')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('панель действий под репликой Мастера: PASS')
