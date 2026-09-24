// «Пусто» — это не «неизвестно».
//
// Разделы выводились из вечной загрузки, но приходили не туда: Docker после
// отказа запроса уверенно писал «CLI НЕ НАЙДЕН» и «Контейнеров нет», и человек
// делал вывод про свою машину по нашей неудаче; очередь решений рядом с
// сообщением об отказе сообщала, что никто не ждёт решения и «это нормальное
// состояние, а не отсутствие данных».
//
// Раздел обязан различать три вещи, а не две: данные есть, данных честно нет,
// спросить не удалось. Проверяем все три — иначе правка вылечит ложь одного
// состояния, сломав верный диагноз другого.
import fs from 'node:fs'
import path from 'node:path'
import vm from 'node:vm'

const repo = path.resolve(import.meta.dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension', 'media', 'main.js'), 'utf8')

function drive({ layout, tab, messages, boot: extraBoot, act }) {
  const listeners = {}
  // Форма SQL читает поле ввода: без него раздел не дойдёт до запроса.
  const root = {
    innerHTML: '',
    addEventListener(t, cb) { listeners[`root:${t}`] = cb },
    querySelector: sel => (sel === '#db-sql' ? { value: 'select 1' } : null),
    querySelectorAll: () => [],
  }
  const context = {
    acquireVsCodeApi: () => ({ postMessage() {}, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout } } },
    window: { addEventListener(t, cb) { listeners[`window:${t}`] = cb } },
    console, Date, Map, Set,
    requestAnimationFrame(cb) { cb(); return 0 }, cancelAnimationFrame() {}, setTimeout(cb) { cb(); return 0 }, clearTimeout() {},
  }
  vm.runInNewContext(main, context, { filename: 'main.js' })
  const boot = {
    onboarded: true, profiles: [], projectAgents: [], usageRecords: [],
    runs: [], quests: [], executions: [], changeSets: [], ...extraBoot,
  }
  listeners['window:message']({ data: { type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w', selectedTab: tab, boot } })
  if (act) act(listeners)
  // Шаг — это либо сообщение от расширения, либо действие человека. Порядок
  // важен: «загрузилось, потом человек нажал обновить, и обновление упало» —
  // не то же самое, что «упало сразу».
  for (const step of messages) {
    if (typeof step === 'function') step(listeners)
    else listeners['window:message']({ data: step })
  }
  return root.innerHTML.replace(/<div class="error-banner">[\s\S]*?<\/div>/g, '').replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ')
}

const cases = [
  {
    имя: 'docker: ядро ответило, Docker честно не установлен',
    got: drive({ layout: 'docker', tab: 'overview', messages: [{ type: 'docker', docker: { status: { available: false }, containers: [], images: [] } }] }),
    обязано: ['CLI не найден', 'Контейнеров нет'],
    запрещено: ['Неизвестно', 'Не удалось спросить ядро'],
  },
  {
    имя: 'docker: ядро ответило, контейнер есть',
    got: drive({ layout: 'docker', tab: 'overview', messages: [{ type: 'docker', docker: { status: { available: true, daemon: true, clientVersion: '27.0' }, containers: [{ Names: 'web', Status: 'Up 2 hours', Image: 'nginx' }], images: [] } }] }),
    обязано: ['Демон доступен', 'web'],
    запрещено: ['Неизвестно', 'Не удалось спросить ядро'],
  },
  {
    имя: 'docker: запрос не удался',
    got: drive({ layout: 'docker', tab: 'overview', messages: [{ type: 'error', message: 'ядро не ответило' }] }),
    обязано: ['Неизвестно', 'Не удалось спросить ядро'],
    запрещено: ['CLI не найден', 'Контейнеров нет (или демон недоступен)'],
  },
  {
    имя: 'решения: ядро ответило, очередь честно пуста',
    got: drive({ layout: 'wide', tab: 'decisions', messages: [{ type: 'decisions', decisions: { total: 0, items: [] } }] }),
    обязано: ['Очередь пуста', 'нормальное состояние'],
    запрещено: ['Очередь не загружена', 'здесь неизвестно'],
  },
  {
    имя: 'решения: запрос не удался',
    got: drive({ layout: 'wide', tab: 'decisions', messages: [{ type: 'error', message: 'ядро не ответило' }] }),
    обязано: ['Очередь не загружена', 'здесь неизвестно'],
    запрещено: ['нормальное состояние'],
  },
  {
    имя: 'история файла: ядро ответило, правок по файлу нет',
    got: drive({ tab: 'filehistory', layout: 'wide', boot: { changes: [{ path: 'main.go' }] },
      messages: [{ type: 'fileHistory', history: { entries: [] } }] }),
    обязано: ['По этому файлу правок нет'],
    запрещено: ['правки неизвестны'],
  },
  {
    имя: 'история файла: запрос не удался',
    got: drive({ tab: 'filehistory', layout: 'wide', boot: { changes: [{ path: 'main.go' }] },
      messages: [{ type: 'error', message: 'ядро не ответило' }] }),
    обязано: ['правки неизвестны, а не отсутствуют'],
    запрещено: ['По этому файлу правок нет'],
  },
  {
    имя: 'SQL: запрос не удался',
    got: drive({ tab: 'databases', layout: 'wide', boot: { dbConnections: [{ id: 'db1', driver: 'sqlite', name: 'база' }] },
      act: listeners => listeners['root:submit']({ target: { id: 'db-query-form' }, preventDefault() {} }),
      messages: [{ type: 'error', message: 'ядро не ответило' }] }),
    обязано: ['Запрос не выполнен', 'это не пустая выборка'],
    запрещено: ['Выполняется SQL'],
  },
  {
    имя: 'мастер: запрос не удался',
    got: drive({ tab: 'master', layout: 'wide', messages: [{ type: 'error', message: 'ядро не ответило' }] }),
    обязано: ['Переписка не загрузилась', 'настройки мастера не загрузились'],
    запрещено: ['модель мастера не выбрана', 'Мастер подберёт отряд'],
  },
  // Ещё одно состояние, стареющее молча: ответ «мастер не настроен» верен ровно
  // до того, как его настроят — а настраивают его в другом разделе. Раздел
  // разговора держал прежний ответ и продолжал звать настраивать, хотя человек
  // из этой настройки только что вернулся. Выход был один — переоткрыть панель.
  {
    имя: 'мастер: настроили в другом разделе',
    got: drive({ tab: 'master', layout: 'wide', messages: [
      { type: 'master', master: { configured: false, history: [] } },
      { type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'w', selectedTab: 'master',
        boot: { onboarded: true, profiles: [], projectAgents: [], usageRecords: [], runs: [], quests: [],
          executions: [], changeSets: [], orchestrator: { id: 'o1', preset: 'conductor', provider: 'ollama', model: 'qwen' } } },
    ] }),
    обязано: ['Открываем переписку'],
    запрещено: ['Мастер не настроен'],
  },
  // Третье состояние рядом с «пусто» и «неизвестно»: данные есть, но они от
  // прошлого успешного опроса. Стирать их не нужно — врать про их свежесть
  // нельзя. Раньше рядом с «демон не отвечает» раздел писал «ДЕМОН ДОСТУПЕН»,
  // показывал контейнер работающим и предлагал кнопку Stop.
  {
    имя: 'docker: обновление не удалось, прежние данные остались',
    got: drive({ layout: 'docker', tab: 'overview', messages: [
      { type: 'docker', docker: { status: { available: true, daemon: true, clientVersion: '27.0' }, containers: [{ Names: 'web', Status: 'Up 2 hours', Image: 'nginx' }], images: [] } },
      listeners => listeners['root:click']({ target: { closest: sel => (sel === '[data-action]' ? { dataset: { action: 'reload-docker' } } : null) }, preventDefault() {} }),
      { type: 'error', message: 'демон не отвечает' },
    ] }),
    обязано: ['web', 'Обновить не удалось'],
    запрещено: ['Не удалось спросить ядро — какие контейнеры есть'],
  },
  {
    имя: 'docker: успешная загрузка не помечается несвежей',
    got: drive({ layout: 'docker', tab: 'overview', messages: [
      { type: 'docker', docker: { status: { available: true, daemon: true, clientVersion: '27.0' }, containers: [{ Names: 'web', Status: 'Up 2 hours', Image: 'nginx' }], images: [] } },
    ] }),
    обязано: ['web'],
    запрещено: ['Обновить не удалось'],
  },

  // Деньги: сумма считается только по записям с известной ценой, и подписана
  // она была просто «РАСХОД». Ядро в том же месте осторожнее — поле называется
  // knownCostCents. Крупное число без оговорки читается как «столько всего
  // потрачено», хотя часть запусков в него не вошла.
  {
    имя: 'расход: у всех запусков цена известна',
    got: drive({ tab: 'overview', layout: 'wide', messages: [],
      boot: { usageRecords: [{ costCents: 120, totalTokens: 100 }, { costCents: 80, totalTokens: 50 }] } }),
    обязано: ['Расход'],
    запрещено: ['без цены', 'известная стоимость'],
  },
  {
    имя: 'расход: часть запусков без цены',
    got: drive({ tab: 'overview', layout: 'wide', messages: [],
      boot: { usageRecords: [{ costCents: 120, totalTokens: 100 }, { totalTokens: 50 }, { totalTokens: 7 }] } }),
    обязано: ['известная стоимость', '2 запуска без цены'],
    запрещено: [],
  },

  // Заголовок оболочки виден на каждом экране, и он тоже обязан различать
  // «не построен», «сломался» и «неизвестно».
  {
    имя: 'индекс: ядро сообщило об ошибке индексации',
    got: drive({ tab: 'overview', layout: 'wide', boot: { indexStatus: { state: 'error' } }, messages: [] }),
    обязано: ['Индекс · ошибка'],
    запрещено: ['Без индекса', 'Индекс неизвестен'],
  },
  {
    имя: 'индекс: папка не открыта',
    got: drive({ tab: 'overview', layout: 'wide', boot: { indexStatus: { state: 'no_workspace' } }, messages: [] }),
    обязано: ['Папка не открыта'],
    запрещено: ['Без индекса'],
  },
  {
    имя: 'индекс: честно не построен',
    got: drive({ tab: 'overview', layout: 'wide', boot: { indexStatus: { state: 'not_built' } }, messages: [] }),
    обязано: ['Без индекса'],
    запрещено: ['Индекс неизвестен', 'Индекс · ошибка'],
  },
  {
    имя: 'индекс: состояние не пришло',
    got: drive({ tab: 'overview', layout: 'wide', messages: [] }),
    обязано: ['Индекс неизвестен'],
    запрещено: ['Без индекса'],
  },
  {
    имя: 'статистика: обновление не удалось, прежние числа остались',
    got: drive({ layout: 'statistics', tab: 'overview', messages: [
      { type: 'statistics', statistics: { totalRuns: 3 } },
      listeners => listeners['root:click']({ target: { closest: sel => (sel === '[data-action]' ? { dataset: { action: 'reload-statistics' } } : null) }, preventDefault() {} }),
      { type: 'error', message: 'ядро не ответило' },
    ] }),
    обязано: ['показаны числа последнего успешного запроса'],
    запрещено: ['числа ниже неизвестны'],
  },
  {
    имя: 'статистика: ядро ответило',
    got: drive({ layout: 'statistics', tab: 'overview', messages: [{ type: 'statistics', statistics: { totalRuns: 3 } }] }),
    обязано: ['Статистика'],
    запрещено: ['числа ниже неизвестны'],
  },
]

// Защита от холостого хода: если бы ответ ядра не доходил, раздел остался бы в
// загрузке, и «CLI НЕ НАЙДЕН» совпало бы с ожиданием случайно. Имя контейнера
// взяться неоткуда, кроме доставленных данных.
if (!cases[1].got.includes('web')) {
  console.log('ответ ядра не дошёл до раздела — проверки прошли бы вхолостую')
  process.exit(1)
}

let bad = 0
for (const c of cases) {
  const нет = c.обязано.filter(t => !c.got.includes(t))
  const лишнее = c.запрещено.filter(t => c.got.includes(t))
  const ok = !нет.length && !лишнее.length
  if (!ok) bad += 1
  console.log(`${ok ? 'ok  ' : 'ПЛОХО'} ${c.имя}`)
  if (нет.length) console.log(`      не сказано: ${нет.join(' | ')}`)
  if (лишнее.length) console.log(`      сказано лишнее: ${лишнее.join(' | ')}`)
}
process.exit(bad ? 1 : 0)
