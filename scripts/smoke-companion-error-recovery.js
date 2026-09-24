// Кнопки под отказом ведут туда, где чинят именно эту причину.
//
// Под любым отказом стояли «Настроить модель» и «Повторить». Когда ядро
// остановлено или не поднялось, оба действия бесполезны: настройка модели этого
// не лечит, а повтор упирается в то же самое. Человек жал их по очереди и
// оставался без ответа и без объяснения.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${detail}`)
}

// Каждый сценарий получает своё окружение: лента помощника намеренно сливает
// приходящую историю с локальной, и ошибка из прошлого сценария осталась бы на
// экране, подменяя проверку.
function dock(messages, after) {
  const listeners = {}
  const root = {
    innerHTML: '',
    addEventListener(type, callback) { listeners['root:' + type] = callback },
    querySelector() { return null },
    querySelectorAll() { return [] },
  }
  const context = {
    acquireVsCodeApi: () => ({ postMessage() {}, getState() { return undefined }, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout: 'companion' } } },
    window: { addEventListener(type, callback) { listeners['window:' + type] = callback } },
    console, Date, Map, Set, CSS: { escape: value => String(value) },
    requestAnimationFrame(callback) { callback(); return 0 },
    cancelAnimationFrame() {},
    setTimeout(callback) { callback(); return 0 },
    clearTimeout() {},
  }
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'media', 'main.js'), 'utf8'), context, { filename: 'media/main.js' })
  const send = payload => listeners['window:message']({ data: payload })
  listeners['window:message']({
    data: {
      type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture', selectedTab: 'overview',
      boot: {
        questProposals: [], companionActionProposals: [], projectAgents: [], profiles: [], flows: [], skills: [],
        runs: [], toolCatalog: [], connections: [],
        companion: { id: 'c1', preset: 'balanced', configured: true, provider: 'ollama', model: 'qwen' },
        companionMessages: messages,
      },
      details: undefined,
    },
  })
  // Сценариям про поток нужен не только снимок ленты, но и ход событий после
  // неё: ответ успевает написаться наполовину и только потом обрывается.
  if (after) after(send)
  return root.innerHTML
}

const failed = content => dock([
  { id: 'm-1', role: 'user', content: 'Что сломалось?' },
  { id: 'm-2', role: 'assistant', content, mode: 'error', level: 'warning' },
])

// Отказ ядра: настройка модели его не лечит.
const core = failed('Компаньон не смог ответить: Локальное ядро остановлено.')
check('у отказа ядра есть запуск ядра', core.includes('Запустить ядро'), 'кнопки запуска нет')
check('у отказа ядра есть журнал', core.includes('Журнал ядра'), 'кнопки журнала ядра нет')
check('настройку модели не предлагают зря',
  !core.includes('Настроить модель'),
  'под отказом ядра всё ещё предлагают настроить модель')

// Отказ модели: здесь настройка и повтор как раз к месту.
const model = failed('Провайдер не знает модель «qwen». Выберите её из списка в настройке компаньона.')
check('у отказа модели есть настройка', model.includes('Настроить модель'), 'кнопки настройки нет')
check('у отказа модели есть повтор', model.includes('Повторить'), 'кнопки повтора нет')
check('запуск ядра не предлагают зря',
  !model.includes('Запустить ядро'),
  'под отказом модели предлагают запускать ядро')


// Закрытая папка: ни настройка модели, ни запуск ядра не помогут — читать всё
// равно нечего, пока доступ не разрешён.
const locked = failed('Сначала разрешите доступ к папке проекта — тогда компаньон сможет ответить.')
check('у закрытой папки есть настройка доступа', locked.includes('Настроить доступ'), 'кнопки доступа нет')
check('закрытую папку не лечат настройкой модели',
  !locked.includes('Настроить модель'),
  'под отказом по доступу предлагают настроить модель')
check('закрытую папку не лечат запуском ядра',
  !locked.includes('Запустить ядро'),
  'под отказом по доступу предлагают запускать ядро')


// Человек сам остановил ответ: вернуться к тому же вопросу должно быть одним
// нажатием, а не повторным набором.
const stopped = dock([
  { id: 'm-1', role: 'user', content: 'Разбери сбой сборки' },
  { id: 'm-2', role: 'assistant', content: 'Запрос остановлен вами.', mode: 'cancelled', level: 'warning' },
])
check('у остановленного ответа есть повтор', stopped.includes('Спросить снова'), 'кнопки повтора нет')
check('остановленный ответ не зовут чинить настройкой',
  !stopped.includes('Настроить модель') && !stopped.includes('Запустить ядро'),
  'под остановленным ответом появились чужие кнопки')

// Остановка новым сообщением — не повод возвращаться: человек уже спросил другое.
const superseded = dock([
  { id: 'm-3', role: 'assistant', content: 'Предыдущий запрос остановлен новым сообщением.', mode: 'cancelled', level: 'warning', superseded: true },
])
check('замещённый ответ не зовёт спрашивать снова',
  !superseded.includes('Спросить снова'),
  'под замещённым ответом предлагают повтор')


// Уровень ответа: критичное предупреждение обязано отличаться от совета за
// секунду, иначе «critical» существует только в данных.
const criticalAnswer = dock([{ id: 'm-c', role: 'assistant', content: 'В репозитории лежит приватный ключ.', mode: 'model', level: 'critical' }])
check('критичный ответ помечен в шапке', criticalAnswer.includes('критично'), 'подписи нет')
check('критичный ответ выделен рамкой', criticalAnswer.includes('warning critical'), 'класса нет')

const warningAnswer = dock([{ id: 'm-w', role: 'assistant', content: 'Сборка падает на втором шаге.', mode: 'model', level: 'warning' }])
check('предупреждение помечено мягче', warningAnswer.includes('важно') && !warningAnswer.includes('критично'), 'подпись предупреждения неверна')

const adviceAnswer = dock([{ id: 'm-s', role: 'assistant', content: 'Ветки: master и dev.', mode: 'model', level: 'suggestion' }])
check('обычный совет ничем не помечен',
  !adviceAnswer.includes('companion-msg-level'),
  'у обычного совета появилась подпись уровня')

// Реплика человека уровня не носит: уровень ставит модель своему ответу.
const humanTurn = dock([{ id: 'm-u', role: 'user', content: 'Что важно?', level: 'critical' }])
check('реплика человека не помечается уровнем',
  !humanTurn.includes('companion-msg-level'),
  'подпись уровня появилась у реплики человека')


// Отказ инструмента виден в самом ответе: полоса активности показывает его
// секунду и исчезает вместе с ответом.
const blind = dock([{
  id: 'm-b', role: 'assistant', content: 'Отвечаю по тому, что есть.', mode: 'model', level: 'suggestion',
  factsUsed: ['gatherMode=full', 'toolsUsed=read_file', 'toolFailures=git_branches,git_log'],
}])
check('под ответом видно, чего посмотреть не удалось',
  blind.includes('Не удалось посмотреть') && blind.includes('git_branches, git_log'),
  'оговорки об отказе нет')

// Обрезанная выдача — не отказ, но ответ по ней тоже неполон.
const partial = dock([{
  id: 'm-p', role: 'assistant', content: 'В логе видно начало сборки.', mode: 'model', level: 'suggestion',
  factsUsed: ['gatherMode=full', 'toolsUsed=read_file', 'toolsTruncated=read_file'],
}])
check('под ответом видно, что прочитано частично',
  partial.includes('Прочитано частично: read_file'),
  'оговорки о частичном чтении нет')

// Обе оговорки уживаются в одной строке, а не спорят за место.
const both = dock([{
  id: 'm-bp', role: 'assistant', content: 'Отвечаю по тому, что есть.', mode: 'model', level: 'suggestion',
  factsUsed: ['toolFailures=git_log', 'toolsTruncated=read_file'],
}])
check('обе оговорки показываются вместе',
  both.includes('Не удалось посмотреть: git_log') && both.includes('Прочитано частично: read_file'),
  'оговорки не ужились')

// Ответ, обрезанный потолком длины, кончается на полуслове: без оговорки
// половина разбора читается как законченная мысль.
const cutAnswer = dock([{
  id: 'm-cut', role: 'assistant', content: 'Сборка падает на шаге тестов: не найден пакет', mode: 'model', level: 'suggestion',
  factsUsed: ['gatherMode=full', 'replyTruncated=true', 'replyLimitTokens=1200'],
}])
check('под оборванным ответом сказано, что он оборван',
  cutAnswer.includes('Ответ оборван на пределе длины (1200 токенов)'),
  'оговорки об обрыве нет или в ней не назван предел')
check('оборванный ответ можно дописать одним нажатием',
  cutAnswer.includes('companion-continue') && cutAnswer.includes('Продолжить'),
  'кнопки продолжения нет')
check('оборванный ответ не зовут чинить настройкой',
  !cutAnswer.includes('Настроить модель') && !cutAnswer.includes('Запустить ядро'),
  'под оборванным ответом появились чужие кнопки')

const clean = dock([{
  id: 'm-ok', role: 'assistant', content: 'Ветка одна: master.', mode: 'model', level: 'suggestion',
  factsUsed: ['gatherMode=full', 'toolsUsed=git_branches'],
}])
check('удачный ответ оговорки не носит',
  !clean.includes('Не удалось посмотреть'),
  'оговорка появилась там, где всё получилось')

// Ответ шёл потоком и оборвался: прочитанное человеком остаётся на экране.
// Раньше на его месте появлялась одна дежурная строка, и разобранное исчезало
// вместе с причиной отказа.
const brokenStream = dock([{ id: 'm-1', role: 'user', content: 'Разбери сбой сборки' }], send => {
  send({ type: 'companionChatDelta', reply: 'Сборка падает на шаге тестов: ' })
  send({ type: 'companionChatError', message: 'Провайдер не знает модель «qwen».' })
})
check('начатый ответ не пропадает вместе с отказом',
  brokenStream.includes('Сборка падает на шаге тестов'),
  'написанное до сбоя стёрлось')
check('к сохранённому куску приложена причина',
  brokenStream.includes('Провайдер не знает модель') && brokenStream.includes('Написанное сохранено'),
  'причина отказа рядом с текстом не показана')

// Кнопки выбираются по причине отказа, а не по словам сохранённого ответа:
// «ядро» в тексте про сборку не значит, что ядро надо запускать.
const coreWord = dock([{ id: 'm-1', role: 'user', content: 'Как собирается ядро?' }], send => {
  send({ type: 'companionChatDelta', reply: 'Ядро собирается командой npm run build:core.' })
  send({ type: 'companionChatError', message: 'Провайдер не знает модель «qwen».' })
})
check('слово «ядро» в ответе не подменяет причину отказа',
  coreWord.includes('Настроить модель') && !coreWord.includes('Запустить ядро'),
  'кнопки выбраны по тексту ответа, а не по причине отказа')

// Модель не ответила, и разбор написал сам Point. Без метки этот ответ читается
// как модельный: причина отката лежит в «Сведениях», куда человек не заглядывает.
const fellBack = dock([{
  id: 'm-fb', role: 'assistant', content: 'Ветки: master и dev. Отвечаю по собранному контексту.',
  mode: 'deterministic', level: 'suggestion',
  fallbackReason: 'companion model call failed: dial tcp 127.0.0.1:11434: connection refused',
}])
check('местный ответ не выдают за модельный',
  fellBack.includes('Ответил движок Point'),
  'метки происхождения ответа нет')
check('причина отката рядом с ответом',
  fellBack.includes('connection refused'),
  'причина не показана даже подсказкой')
check('из отката есть выход',
  fellBack.includes('Настроить модель') && fellBack.includes('Повторить'),
  'кнопок выхода из отката нет')

// Модель не подключена вовсе: ответ пишет встроенный разбор Point. Это не сбой,
// а выбранный режим, поэтому метка есть, а кнопок починки нет — чинить нечего.
const withoutModel = dock([{
  id: 'm-loc', role: 'assistant', content: 'Ветки: master и dev.', mode: 'deterministic', level: 'suggestion',
}])
check('ответ без модели помечен так же честно',
  withoutModel.includes('Ответил движок Point'),
  'местный ответ выдаётся за модельный, когда модели просто нет')
check('без модели не обещают ответить иначе',
  !withoutModel.includes('Ответить иначе'),
  'кнопка другого пути стоит там, где путь один')
check('выбранный локальный режим не зовут чинить',
  !withoutModel.includes('companion-msg-recovery'),
  'под ответом рабочего локального режима появились кнопки починки')

// Ответ самой модели метки происхождения не носит: она нужна там, где ответ
// пришёл не оттуда, откуда человек ждёт.
const fromModel = dock([{ id: 'm-mm', role: 'assistant', content: 'Ветки: master и dev.', mode: 'model', model: 'qwen' }])
check('модельный ответ метки не носит',
  !fromModel.includes('Ответил движок Point'),
  'метка появилась у ответа модели')

// Обычный ответ вообще не носит кнопок починки.
const normal = dock([{ id: 'm-3', role: 'assistant', content: 'Ветки: master и dev.', mode: 'model' }])
check('у обычного ответа кнопок починки нет',
  !normal.includes('companion-msg-recovery'),
  'под обычным ответом появились кнопки починки')

if (failures.length) {
  console.error('\nПРОВАЛЕНО:')
  for (const item of failures) console.error('  · ' + item)
  process.exit(1)
}
console.log('\nкнопки под отказом ведут к его причине')
