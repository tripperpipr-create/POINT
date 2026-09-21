// Уточнения Мастера: спрашивают в карточке ввода, запись обмена — в ленте.
//
// Неотвеченный пакет уезжал прокруткой вверх с каждым следующим ходом, и
// человек отвечал не на то, что видел. Он переехал в карточку ввода, и второго
// места у него нет: пакет, который там стоит, лента не отмечает вовсе, а сам
// квест до готовности живёт вкладкой справа. Отмечается только пакет, до
// которого карточка ввода не дошла, — иначе он пропал бы молча, а запуск
// задания держит. Отвеченный обмен остаётся записью при своём ходе.
// Здесь проверяются все состояния сразу: живое
// (спрашиваем), отправленное (ход идёт), решённое (ответы в истории) — и то,
// что отвечать на всё разом человек не обязан.
const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')

function open(history, proposals) {
  const listeners = {}
  const posted = []
  const field = { id: 'master-input', value: '', rows: 2, focus() {}, setSelectionRange() {}, closest: () => null, matches: () => false }
  const nodes = { '#master-input': field }
  const root = {
    innerHTML: '',
    addEventListener(type, callback) { listeners[`root:${type}`] = callback },
    querySelector: selector => nodes[selector] || null,
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
    type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w', selectedTab: 'master',
    boot: {
      onboarded: true, profiles: [], projectAgents: [], usageRecords: [],
      runs: [], quests: [], executions: [], changeSets: [], questProposals: proposals || [],
      orchestrator: { id: 'o1', preset: 'conductor', model: 'qwen' },
    },
  } })
  listeners['window:message']({ data: { type: 'master', master: { configured: true, config: { model: 'qwen' }, history } } })
  return { listeners, posted, root, field }
}

const ASKED = [
  { id: 'u1', role: 'user', content: 'Почини вебхук' },
  { id: 'a1', role: 'assistant', mode: 'model', content: 'Уточню детали.', questions: ['С какой стороны отказ?', 'Что считать готовым?'] },
]
const ANSWERS = 'Вопрос: С какой стороны отказ?\nМой ответ: Со стороны обработчика\n\nВопрос: Что считать готовым?\nМой ответ: Регресс зелёный'

// Лента — то, что между разговором и карточкой ввода. Считать по разметке
// целиком нельзя: имя задания законно стоит и во вкладке справа, и в самой
// панели, а проверка «имени в ленте нет» тогда спорила бы с тем местом, куда
// это имя и переносили.
const feedOf = html => html.slice(html.indexOf('id="master-thread"'), html.indexOf('hall-compose'))

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${String(detail).slice(0, 300)}`)
}

// ── Спрашивают в карточке ввода, лента помнит, что спросили ────────────────
{
  const ui = open(ASKED)
  const html = ui.root.innerHTML
  check('неотвеченный пакет стоит в карточке ввода',
    /hall-compose[\s\S]*hall-questions is-inline/.test(html) && html.includes('master-answer-question'),
    'уточнения снова в ленте: их унесёт прокруткой следующим же ходом')
  // Считается показанное, а не разметка целиком: разбор пакета едет в
  // data-pack, и текст второго вопроса лежит там по праву.
  const shown = html.replace(/\sdata-[a-z-]+="[^"]*"/g, '')
  check('на экране один вопрос пакета',
    shown.includes('С какой стороны отказ?') && !shown.includes('Что считать готовым?'),
    'пакет показан целиком: два развёрнутых поля поднимают карточку ввода')
  check('место в пакете названо',
    html.includes('вопрос 1 из 2') && html.includes('master-question-next'),
    'сколько ещё спросят — не видно, а это первая причина, по которой курсор снимали')
  check('с первого вопроса назад не зовут',
    !html.includes('master-question-prev'),
    '«Назад» с первого вопроса ведёт в никуда')
  check('пакет из карточки ввода не продублирован в ленте',
    !html.includes('hall-quest-ask'),
    'над карточкой ввода снова стоит пометка о том же пакете: два места об одном вопросе')
}

// ── Квест на уточнении в ленту не входит вовсе ────────────────────────────
//
// До готовности он живёт вкладкой справа. Пока лента отмечала развилку именем
// задания, на экране было два места об одном и том же: карточка с названием и
// кнопкой «Ответить» — и форма, к которой она вела, прямо под ней.
{
  const quest = {
    id: 'qp-1', workspaceId: 'w', status: 'pending', title: 'Повторная доставка вебхука',
    brief: {
      version: 1, state: 'discussion', mode: 'precise', resultKind: 'workspace_change',
      goal: 'Сделать повторную доставку вебхука безопасной',
      scope: [], outOfScope: [], decisions: [], criteria: [],
      openQuestions: ['С какой стороны отказ?'],
      permissions: {}, budget: { tokens: 31000 },
    },
  }
  const html = open([ASKED[0], { ...ASKED[1], proposalId: 'qp-1' }], [quest]).root.innerHTML
  check('имени задания в ленте нет',
    !feedOf(html).includes('Сделать повторную доставку вебхука безопасной'),
    'квест снова назван в ленте, хотя до готовности он живёт вкладкой справа')
  check('пометки о развилке в ленте нет',
    !html.includes('hall-quest-ask'),
    'над карточкой ввода снова стоит карточка о том же вопросе')
  check('необсуждённый квест в ленту не вошёл',
    !html.includes('quest-proposal-start'),
    'карточка задания снова заняла ленту вместо вкладки справа')
  check('вкладка задания на месте',
    html.includes('master-brief-toggle'),
    'квеста нет ни в ленте, ни во вкладке — открыть его состав негде')
}

// ── Пакет, до которого карточка ввода не дошла, из разговора не пропадает ──
//
// Карточка ввода держит один пакет — последний. Прежний неотвеченный остаётся
// только в ленте, и без пометки он исчез бы молча: человек не узнал бы, что
// запуск задания чем-то держится. Имени квеста у пометки нет (он во вкладке),
// кнопки тоже: вести к форме, которой на экране нет, некуда.
{
  const html = open([
    ...ASKED,
    { id: 'u2', role: 'user', content: 'Давай позже' },
    { id: 'a2', role: 'assistant', mode: 'model', content: 'Тогда уточню другое.', questions: ['Какой срок?'] },
  ]).root.innerHTML
  check('прежний неотвеченный пакет отмечен',
    html.includes('Уточнения остались без ответа'),
    'прежние уточнения пропали из разговора молча, а запуск задания они держат')
  check('у прежнего пакета нет кнопки к чужой форме',
    !html.includes('master-focus-ask'),
    'кнопка ведёт к форме, которой на экране нет')
}

// ── Ответы ушли: обмен решён, своей реплики в ленте нет ────────────────────
{
  const ui = open(ASKED)
  ui.field.value = ANSWERS
  ui.listeners['root:input']({ target: ui.field })
  ui.listeners['root:click']({
    target: { closest: selector => (selector === '[data-action]' ? { dataset: { action: 'master-send' } } : null) },
    preventDefault() {},
  })
  const html = ui.root.innerHTML
  check('ответы ушли в ядро',
    ui.posted.some(message => message.type === 'masterChat' && message.message === ANSWERS),
    'ответы не отправлены: ' + JSON.stringify(ui.posted.map(message => message.type)))
  check('обмен показан решённым сразу',
    html.includes('hall-questions is-resolved') && html.includes('отвечено 2 из 2'),
    'пока ход идёт, блок спрашивает второй раз то, на что уже ответили')
  check('служебный формат отправки не виден',
    !html.includes('Вопрос: С какой стороны отказ?'),
    'своя реплика встала в ленту сырым текстом отправки')
  check('ответы читаются на месте',
    html.includes('Со стороны обработчика') && html.includes('Регресс зелёный'),
    'ответов не видно ни под вопросами, ни репликой')
}

// ── История: реплика-ответ не дублирует блок уточнений ─────────────────────
{
  const ui = open([...ASKED, { id: 'u2', role: 'user', content: ANSWERS }, { id: 'a2', role: 'assistant', mode: 'model', content: 'Понял, собираю задание.' }])
  const html = ui.root.innerHTML
  check('прежний обмен читается сверху вниз',
    html.indexOf('С какой стороны отказ?') < html.indexOf('Со стороны обработчика')
      && html.indexOf('Со стороны обработчика') < html.indexOf('Понял, собираю задание.'),
    'ответы стоят не под своими вопросами')
  // Считается показанное, а не разметка целиком: «Ещё раз» носит прежнюю
  // реплику в data-message, чтобы переспросить ядро тем же текстом, и это не
  // второе появление ответов на экране.
  const shown = html.replace(/\sdata-message="[^"]*"/g, '')
  check('реплика-ответ не повторена отдельной карточкой',
    (shown.match(/Со стороны обработчика/g) || []).length === 1,
    'ответы показаны дважды: и в блоке уточнений, и своей репликой')
  check('неполный разбор оставляет реплику на месте',
    open([...ASKED, { id: 'u2', role: 'user', content: 'Вопрос: С какой стороны отказ?\nМой ответ: Со стороны обработчика\n\nа второе решим позже' }])
      .root.innerHTML.includes('а второе решим позже'),
    'реплика убрана из ленты, хотя разобрана только наполовину — остаток потерян молча')
}

// ── Неотвеченный вопрос назван неотвеченным ────────────────────────────────
{
  const ui = open([...ASKED, { id: 'u2', role: 'user', content: 'Вопрос: С какой стороны отказ?\nМой ответ: Со стороны обработчика' }])
  const html = ui.root.innerHTML
  check('счётчик честен',
    html.includes('отвечено 1 из 2'),
    'счётчик обещает больше ответов, чем дано')
  check('без ответа осталось названным',
    html.includes('нет ответа') && html.includes('Что считать готовым?'),
    'пропавший вопрос читается как отвеченный')
}

// ── Варианты не выдумываются ───────────────────────────────────────────────
{
  const ui = open([
    { id: 'u1', role: 'user', content: 'Разверни Symfony' },
    { id: 'a1', role: 'assistant', mode: 'model', content: 'Уточню.', questions: ['С какой стороны воспроизводится отказ?'] },
  ])
  check('вопрос без перечисления получает поле, а не «Да / Нет»',
    !ui.root.innerHTML.includes('master-pick-option'),
    'человеку предложено выбрать из того, чего модель не предлагала')
  const listed = open([
    { id: 'u1', role: 'user', content: 'Разверни Symfony' },
    { id: 'a1', role: 'assistant', mode: 'model', content: 'Уточню.', questions: ['Чем разворачиваем: Docker, Kubernetes или вручную?'] },
  ])
  check('названный в вопросе выбор становится вариантами',
    listed.root.innerHTML.includes('data-option="Docker"') && listed.root.innerHTML.includes('data-option="вручную"'),
    'перечисление из самого вопроса не разобрано')
}

// ── Вопрос без вариантов отвечают словами, и слов бывает много ─────────────
{
  const ui = open([
    { id: 'u1', role: 'user', content: 'Разверни Symfony' },
    { id: 'a1', role: 'assistant', mode: 'model', content: 'Уточню.', questions: ['Что считать готовым результатом?'] },
  ])
  const html = ui.root.innerHTML
  check('свободный ответ пишут в растущее поле',
    /<textarea[^>]*class="hall-question-extra"/.test(html),
    'развёрнутый ответ набирают в строку: видно последние сорок знаков')
  const listed = open([
    { id: 'u1', role: 'user', content: 'Разверни Symfony' },
    { id: 'a1', role: 'assistant', mode: 'model', content: 'Уточню.', questions: ['Чем разворачиваем: Docker, Kubernetes или вручную?'] },
  ])
  check('поправка к выбору остаётся строкой',
    /<input[^>]*class="hall-question-extra"/.test(listed.root.innerHTML),
    'своё поверх названных вариантов раздувает карточку ввода без нужды')
}

// ── Отвечать на всё разом человек не обязан ────────────────────────────────
//
// Прежде кнопка молчала, пока не заполнены все поля: нажатие просто переставляло
// курсор, и это читалось как поломка. Теперь уходит отвеченное, а неотвеченное
// возвращается из ядра в «Нужно уточнить» и держит запуск — там ему и место.
{
  const pack = [{ text: 'С какой стороны отказ?', options: [], kind: 'text' }, { text: 'Что считать готовым?', options: [], kind: 'text' }]
  // Место в пакете читается из того, что нарисовано: подставив его руками, мы
  // проверяли бы свою догадку о курсоре, а не курсор.
  const shownCursor = ui => Number((ui.root.innerHTML.match(/data-cursor="(\d+)"/) || [0, '0'])[1])
  const press = (ui, action, value) => {
    const at = shownCursor(ui)
    const question = {
      dataset: { questionKey: 'a1:' + at },
      closest: () => null,
      querySelector: selector => (selector === '.hall-question-extra' ? { value: value || '', focus() {} } : null),
      querySelectorAll: () => [],
    }
    const group = {
      dataset: { pack: JSON.stringify(pack), owner: 'a1', total: String(pack.length), cursor: String(at) },
      querySelector: selector => (selector === '.hall-question' ? question : null),
      querySelectorAll: selector => (selector === '.hall-question' ? [question] : []),
    }
    ui.listeners['root:click']({
      target: { closest: selector => (selector === '[data-action]'
        ? { dataset: { action }, closest: inner => (inner === '.hall-questions' ? group : null) }
        : null) },
      preventDefault() {},
    })
    return ui.posted.filter(message => message.type === 'masterChat')
  }
  // Ответ на каждый вопрос набирается на своём экране, как у человека.
  const answer = (ui, values) => {
    values.forEach((value, index) => {
      if (index < values.length - 1) press(ui, 'master-question-next', value)
    })
    return press(ui, 'master-answer-question', values[values.length - 1])
  }

  const paged = open(ASKED)
  press(paged, 'master-question-next', 'Со стороны обработчика')
  check('«Далее» листает на второй вопрос',
    paged.root.innerHTML.includes('Что считать готовым?') && paged.root.innerHTML.includes('вопрос 2 из 2'),
    'перелистывание не дошло до второго вопроса')
  check('со второго вопроса зовут назад',
    paged.root.innerHTML.includes('master-question-prev') && !paged.root.innerHTML.includes('master-question-next'),
    'вернуться к отвеченному нельзя — вторая причина, по которой курсор снимали')
  press(paged, 'master-question-prev', '')
  check('«Назад» возвращает набранное',
    paged.root.innerHTML.includes('вопрос 1 из 2') && paged.root.innerHTML.includes('Со стороны обработчика'),
    'ответ на первый вопрос потерялся при перелистывании')

  const partial = answer(open(ASKED), ['Со стороны обработчика', ''])
  check('частичный ответ уходит',
    partial.length === 1 && partial[0].message === 'Вопрос: С какой стороны отказ?\nМой ответ: Со стороны обработчика',
    'отвеченное не отправлено или отправлено пустым блоком: ' + JSON.stringify(partial.map(message => message.message)))
  const empty = answer(open(ASKED), ['', ''])
  check('пустой пакет никуда не уходит',
    empty.length === 0,
    'в ядро ушла реплика без единого ответа')
}

// ── Enter листает пакет и отправляет только с последнего вопроса ──────────
//
// Кнопки пакета были исправны, а клавиша — нет: Enter всегда нажимал отправку.
// Человек отвечал на первый вопрос из двух, жал Enter — и пакет уходил с одним
// ответом, а оставшийся возвращался из ядра в «Нужно уточнить» и держал запуск.
{
  const enter = (ui, at, total) => {
    const clicked = []
    const group = {
      dataset: { cursor: String(at), total: String(total) },
      querySelector: selector => ({ click() { clicked.push(selector) } }),
    }
    ui.listeners['root:keydown']({
      key: 'Enter', shiftKey: false, isComposing: false,
      target: {
        id: '', tagName: 'TEXTAREA', dataset: {},
        classList: { contains: name => name === 'hall-question-extra' },
        closest: selector => (selector === '.hall-questions' ? group : null),
      },
      preventDefault() {},
    })
    return clicked.join(' ')
  }
  check('Enter на первом вопросе листает пакет',
    enter(open(ASKED), 0, 2) === '[data-action="master-question-next"]',
    'Enter отправил пакет с первого вопроса: остальные вернутся нерешёнными и задержат запуск')
  check('Enter на последнем вопросе отправляет ответы',
    enter(open(ASKED), 1, 2) === '[data-action="master-answer-question"]',
    'на последнем вопросе клавиша перестала отправлять — отвечать стало нечем, кроме мыши')
  check('пакет из одного вопроса отправляется сразу',
    enter(open(ASKED), 0, 1) === '[data-action="master-answer-question"]',
    'единственный вопрос листается в никуда')
  // Клавиша и подпись на ней обязаны обещать одно и то же.
  const asking = open(ASKED).root.innerHTML
  check('подпись клавиши ввода называет шаг по пакету',
    asking.includes('enterkeyhint="next"'),
    'экранная клавиатура обещает отправку там, где Enter листает')
  check('кнопка названа тем, что делает',
    asking.includes('Отправить ответы') && !asking.includes('>Продолжить<'),
    'кнопка отправки снова читается шагом по пакету — её и нажимают вместо «Далее»')
}

if (failures.length) {
  console.error('\nУТОЧНЕНИЯ ПОТЕРЯЛИСЬ:\n  ' + failures.join('\n  '))
  process.exitCode = 1
} else {
  console.log('\nУточнения Мастера: спрашивают в карточке ввода, лента помнит вопрос, ответ бывает неполным: PASS')
}
