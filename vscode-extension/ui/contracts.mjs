// Сверка договорённостей между ядром и интерфейсом.
//
// Тексты и идентификаторы живут в двух языках. Пока связь держится на памяти
// автора, она рвётся молча: переформулировали причину в Go — кнопка в JS увела
// не туда, и оба теста остались зелёными. Здесь проверяется то, что ни один из
// них проверить не может в одиночку.

import fs from 'node:fs'
import { execFileSync } from 'node:child_process'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const root = path.resolve(here, '..', '..')
const read = file => fs.readFileSync(path.join(root, file), 'utf8')
// То же дерево, но пофайлово: часть договорённостей спрашивает не «есть ли это
// где-то в интерфейсе», а «лежит ли это рядом друг с другом» — например, роль
// списка и перебор стрелками.
const readSourceFiles = directory => {
  const files = []
  const visit = current => {
    for (const entry of fs.readdirSync(current, { withFileTypes: true })) {
      const target = path.join(current, entry.name)
      if (entry.isDirectory()) visit(target)
      else if (entry.isFile() && entry.name.endsWith('.js')) files.push({ name: entry.name, text: fs.readFileSync(target, 'utf8') })
    }
  }
  visit(path.join(root, directory))
  return files.sort((a, b) => a.name.localeCompare(b.name))
}
const readSources = directory => {
  const files = []
  const visit = current => {
    for (const entry of fs.readdirSync(current, { withFileTypes: true })) {
      const target = path.join(current, entry.name)
      if (entry.isDirectory()) visit(target)
      else if (entry.isFile() && entry.name.endsWith('.js')) files.push(target)
    }
  }
  visit(path.join(root, directory))
  return files.sort().map(file => fs.readFileSync(file, 'utf8')).join('\n')
}
// Go-исходник читается пакетом, а не файлом.
//
// Договорённости смотрят в ядро текстом, и привязка к имени файла уже дважды
// оказывалась хрупкой: обработчик или константа переезжают в соседний файл
// того же пакета, проверка перестаёт что-либо находить и молча проходит
// вхолостую. Пакет — та единица, которая не меняется от перекладывания.
const readGoPackage = directory => {
  const full = path.join(root, directory)
  return fs.readdirSync(full, { withFileTypes: true })
    .filter(entry => entry.isFile() && entry.name.endsWith('.go') && !entry.name.endsWith('_test.go'))
    .map(entry => entry.name)
    .sort()
    .map(name => fs.readFileSync(path.join(full, name), 'utf8'))
    .join('\n')
}

// Static invariants belong to the editable module tree. Runtime/render checks
// still execute media/main.js, so the source contract and the shipped bundle
// are both covered without depending on esbuild's formatting.
const webviewSource = readSources('vscode-extension/ui/client')

// Расширение читается тем же правилом, что и пакет Go: корнем каталога, а не
// именем файла. 19 сентября `formatIndexStatus` уехала в
// `project-index-controller.js`, и договорённость о состояниях индекса
// перестала находить словарь — сверять стало не с чем. Модули верхнего уровня
// `vscode-extension/` и есть одна единица: `extension.js` их только связывает.
const extensionPackage = fs.readdirSync(path.join(root, 'vscode-extension'), { withFileTypes: true })
  .filter(entry => entry.isFile() && entry.name.endsWith('.js'))
  .map(entry => entry.name)
  .sort()
  .map(name => fs.readFileSync(path.join(root, 'vscode-extension', name), 'utf8'))
  .join('\n')

const failures = []
const fail = message => failures.push(message)

// 1. Шаги починки. Ядро называет шаг в block("<step>", ...); интерфейс обязан
//    знать этот идентификатор, иначе кнопка «исправить» ведёт в никуда.
{
  const go = read('internal/app/agent_capability.go')
  const js = webviewSource

  const emitted = [...go.matchAll(/\bblock\(\s*"[^"]+"\s*,\s*"([a-z]+)"/g)].map(match => match[1])
  if (emitted.length === 0) fail('в ядре не найдено ни одной причины блокировки — сверка шагов ослепла')

  const knownBlock = js.match(/const PROFILE_STEP_IDS = new Set\(\[([^\]]*)\]\)/)
  if (!knownBlock) fail('интерфейс больше не объявляет PROFILE_STEP_IDS — сверять не с чем')
  const known = knownBlock ? new Set([...knownBlock[1].matchAll(/'([a-z]+)'/g)].map(m => m[1])) : new Set()

  for (const step of new Set(emitted)) {
    if (!known.has(step)) fail(`ядро отдаёт шаг "${step}", которого нет в PROFILE_STEP_IDS`)
  }

  // Идентификатор должен существовать как настоящий шаг формы, а не только в
  // списке допустимых.
  const stageIds = new Set([...js.matchAll(/id: '([a-z]+)'/g)].map(m => m[1]))
  for (const step of known) {
    if (!stageIds.has(step)) fail(`шаг "${step}" объявлен допустимым, но такого шага формы нет`)
  }

  // Разбор причины по словам однажды уже разошёлся с текстом. Возврата быть
  // не должно.
  if (/function readinessIssueStep\b/.test(js)) {
    fail('вернулось определение шага по словам в тексте причины')
  }
}

// 2. Граница поверхностей. Чат компаньона не рендерится в Гильдии ни в одном
//    состоянии — включая открытую настройку, которая рендерит ту же функцию.
{
  const js = webviewSource
  const overview = js.match(/function overview\(\)\s*\{[\s\S]*?\n\}/)
  if (!overview) fail('функция overview не найдена — границу поверхностей не проверить')
  else if (/companionChatHtml\(\)/.test(overview[0]) && !/companionSetupOpen/.test(overview[0])) {
    fail('overview рендерит чат компаньона вне режима настройки')
  }
}

// 3. Русский текст интерфейса — по тому, что видно на экране.
//    Найдено глазами на живых поверхностях: «1 наборов ждут Apply», «2 файл.»,
//    «нет verifier», «КАНАЛИРУЕТ». Проверять исходник бессмысленно: те же слова
//    законно живут в идентификаторах, атрибутах и комментариях. Смотрим на текст
//    между тегами — ровно то, что читает человек.
{
  // Все поверхности Чертога, а не выборка. Проверка охватывала семь из
  // четырнадцати, и английский текст в «Памяти» я нашёл руками — она бы его не
  // увидела, потому что этот экран не рендерился вовсе.
  // Конструктор агента и карточка персонажа — экраны, где человек решает, что
  // агенту можно. Их в списке не было, и проверка ни разу их не видела.
  const surfaces = ['overview', 'master', 'decisions', 'changesets', 'quests', 'agents',
    'teams', 'skills', 'flows', 'journal', 'filehistory', 'connections', 'history', 'memory', 'statistics',
    'constructor', 'character',
    // Задание до готовности: вкладка разговора, правая панель и пакет вопросов
    // в карточке ввода. Ни одна из трёх поверхностей не рендерилась отдельно, а
    // русский текст у них свой — имя состояния на вкладке, подпись панели,
    // счётчик отвеченного у кнопки.
    ['master', 'brief-panel'],
    // Первый запуск: пустые состояния шагов. Их текст не проверялся ни разу —
    // мир фикстуры всегда был обжитым, и «ещё не настроено» не рендерилось.
    ['onboarding', 'orchestrator-brain', 'fresh'], ['onboarding', 'orchestrator-choose', 'fresh'],
    // Студия компаньона, шаг «Проверка»: образец ответа на выбранную сцену.
    // Все пять сцен, а не одна: половина текста была английской ровно в тех
    // сценах, которые по умолчанию не показываются.
    ['companion-studio', 'examples'], ['companion-studio', 'examples@code'],
    ['companion-studio', 'examples@agent'], ['companion-studio', 'examples@quest'],
    ['companion-studio', 'examples@flow'], ['companion-studio', 'role'],
    // Шаг «Навыки»: карточки каталога и причина, по которой навык помощнику не
    // надевается. Текст причины виден только здесь.
    ['companion-studio', 'skills']]
  // Поверхность — либо имя, либо имя с аргументами отрисовки: у мастера первого
  // запуска половина текста живёт в состоянии «ничего не настроено», и без
  // `fresh` он не рендерился никогда.
  const visibleText = surface => {
    const args = Array.isArray(surface) ? surface : [surface]
    const page = execFileSync(process.execPath,
      [path.join(root, 'scripts/render-hub-surface.js'), ...args],
      { encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 })
    const body = page.slice(page.indexOf('<div id="root"'), page.lastIndexOf('</body>'))
    return body
      .replace(/<(script|style|pre)[\s\S]*?<\/\1>/g, ' ')
      .replace(/<[^>]*>/g, '')   // теги и все их атрибуты — прочь
      .split('').join(' ')
      .replace(/&[a-z]+;/g, ' ')
      .replace(/\s+/g, ' ')
  }


  // Латиница, пришедшая из данных, — не проза интерфейса. Имена агентов, пути
  // файлов, адреса и название проекта задаёт фикстура; придираться к ним значит
  // утопить настоящие находки в шуме. Берём словарь прямо из неё.
  const fixture = read('scripts/render-hub-surface.js')
  const trim = word => word.toLowerCase().replace(/-+$/, '')
  const fromData = new Set([...fixture.matchAll(/[A-Za-z][A-Za-z-]{2,}/g)].map(m => trim(m[0])))

  for (const surface of surfaces) {
    let text
    try {
      text = visibleText(surface)
    } catch (error) {
      fail(`не удалось отрисовать поверхность «${[].concat(surface).join(' ')}»: ${error.message.split('\n')[0]}`)
      continue
    }

    // Число подставляется вместе с формой слова, а не в заранее выбранную.
    for (const hit of text.matchAll(/\b\d+ (файл|набор|квест|агент|шаг|прогон)\.(?!\w)/g)) {
      fail(`${[].concat(surface).join(' ')}: «${hit[0]}» — счётчик с усечённым словом, используйте countOf()`)
    }
    for (const hit of text.matchAll(/\b1 [А-Яа-яЁё]+(ов|ев|ей)\b/g)) {
      fail(`${[].concat(surface).join(' ')}: «${hit[0]}» — единица с формой множественного числа`)
    }

    // Английские слова в русской фразе — по принципу «запрещено всё, кроме
    // названного». Список запрещённых слов я расширял задним числом дважды:
    // сначала пропустил «execution» рядом с «executions», потом «Definition».
    // Перечислять законное надёжнее: новое английское слово в прозе попадётся
    // само, а имена продуктов и сокращения перечислимы и почти не меняются.
    const legitimate = new Set([
      // Memory, Rule и Skill — виды артефактов обучения, имена рядом с «Blueprint».
      'point', 'flow', 'flows', 'skill', 'skills', 'memory', 'rule', 'rules',
      'sandbox', 'ide', 'llm', 'llmux',
      'ssh', 'api', 'git', 'cursor', 'ollama', 'anthropic', 'openai', 'azure', 'secretstorage', 'sha',
      'secretref', 'diff', 'ctrl', 'shift', 'alt', 'enter', 'esc',
      'jetbrains', 'code', 'oss', 'docker', 'sql', 'json', 'yaml', 'http', 'https',
      // Markdown — имя формата рядом с JSON и YAML: «Экспорт Markdown» в меню
      // разговора. Не рисовалось ни одной страницей стенда, пока задание не
      // получило свою — поэтому и всплыло только сейчас.
      'markdown',
      'url', 'hub', 'home', 'agents', 'change', 'journal', 'sage', 'forge',
      // Названия инструментов и протоколов — это имена, а не английская проза.
      'openssh', 'ssh-agent', 'remote-ssh', 'bearer', 'token',
      'coder', 'qwen', 'claude', 'sonnet', 'gateway', 'example', 'localhost',
      // Skill outcome — четвёртый вид артефакта обучения рядом с Memory, Rule и
      // Skill; SQLite — имя движка; CANARY — имя релизных ворот.
      'outcome', 'outcomes', 'sqlite', 'canary',
      // Google и OAuth — компания и протокол в примере задачи компаньона.
      'google', 'oauth', 'oidc',
    ])
    // Искали «кириллица, затем латинское слово» одной регуляркой — и она съедала
    // контекст собственным совпадением: из двух английских слов подряд
    // проверялось только первое. На «Memory, сигналы, Skill outcomes и изменения»
    // выдавалось ["Memory","Skill"], а «outcomes» не видел никто. Идём наоборот:
    // по каждому латинскому слову смотрим окно с обеих сторон.
    const CONTEXT = 24
    const seen = new Set()
    for (const hit of text.matchAll(/\b[A-Za-z][A-Za-z-]{2,}\b/g)) {
      const word = hit[0]
      const before = text.slice(Math.max(0, hit.index - CONTEXT), hit.index)
      const after = text.slice(hit.index + word.length, hit.index + word.length + CONTEXT)
      if (!/[А-Яа-яЁё]/.test(before) && !/[А-Яа-яЁё]/.test(after)) continue
      const key = word.toLowerCase().replace(/-+$/, '')
      if (legitimate.has(key) || fromData.has(key) || seen.has(key)) continue
      // Путь или расширение файла: рядом стоит слэш или точка с буквой.
      if (/[/.][A-Za-z]/.test(after)) continue
      // Слово целиком заглавными рядом с путём или идентификатором — почти
      // всегда данные, а не проза. Пропускаем только очевидные аббревиатуры.
      if (/^[A-Z]{2,5}$/.test(word)) continue
      seen.add(key)
      fail(`${[].concat(surface).join(' ')}: английское «${word}» в русской фразе — «${(before + word + after).trim()}»`)
    }
  }
}

// Объявленная шкала кеглей — источник правды для проверки ниже. Читается из
// токенов, а не переписывается сюда: список в двух местах разъедется.
const TYPE_SCALE = new Set(
  [...read('vscode-extension/ui/tokens.css').matchAll(/--t-[a-z0-9-]+:\s*(\d+(?:\.\d+)?)px/g)].map(hit => Number(hit[1])),
)

// 4. Размер шрифта берётся из шкалы. 8px прописными с разрядкой не читается, и
//    таких правил было 39 — мимо объявленной шкалы, где минимум --t-micro: 9px.
{
  const layers = fs.readdirSync(path.join(root, 'vscode-extension/ui/layers'))
    .filter(name => name.endsWith('.css'))
  for (const name of layers) {
    const css = read(`vscode-extension/ui/layers/${name}`)
    const tooSmall = [...css.matchAll(/font(?:-size)?:[^;{}]*?\b([0-7](?:\.\d+)?|8(?:\.\d+)?)px/g)]
    for (const hit of tooSmall) {
      fail(`${name}: шрифт ${hit[1]}px мельче шкалы — используйте var(--t-micro)`)
    }
    // Мельче шкалы проверялось, а мимо шкалы — нет.
    //
    // Замер живых поверхностей: у Мастера четыре кегля (9, 10, 11, 15), у
    // Гильдии — десять, и среди них 14, 16, 20, которых в шкале нет вовсе. Это и
    // есть разница между «собрано по системе» и «как легло»: каждый отдельный
    // размер сам по себе не мешает, поэтому его никто и не правит, а вместе они
    // делают соседние экраны непохожими на один продукт.
    for (const hit of css.matchAll(/font(?:-size)?:[^;{}]*?\b(\d{2,}(?:\.\d+)?)px/g)) {
      const size = Number(hit[1])
      if (size <= 8 || TYPE_SCALE.has(size)) continue
      fail(`${name}: кегль ${size}px мимо шкалы (${[...TYPE_SCALE].sort((a, b) => a - b).join(', ')}) — возьмите ступень или заведите токен`)
    }
  }
}

// 5. Точки вклада панелей. Оболочка принимает ровно три ключа viewsContainers —
//    "activitybar", "panel", "secondarySidebar" — и объявляет
//    additionalProperties: false. Неизвестный ключ не вызывает ошибки: он молча
//    отбрасывается. Расширение объявляло "auxiliaryBar", и панели компаньона
//    справа просто не существовало, хотя провайдер был зарегистрирован.
{
  const manifest = JSON.parse(read('vscode-extension/package.json'))
  const allowed = new Set(['activitybar', 'panel', 'secondarySidebar'])
  const declared = Object.keys(manifest?.contributes?.viewsContainers || {})
  for (const key of declared) {
    if (!allowed.has(key)) {
      fail(`viewsContainers."${key}" — оболочка такой ключ не принимает и молча его отбрасывает`)
    }
  }
  // Панель компаньона справа — обещание продукта, а не деталь оформления.
  const views = manifest?.contributes?.views || {}
  const secondary = (manifest?.contributes?.viewsContainers?.secondarySidebar || []).map(item => item.id)
  if (!secondary.length) fail('нет контейнера во вторичной панели: чат компаньона справа обещан продуктом')
  for (const container of secondary) {
    if (!(views[container] || []).length) fail(`контейнер "${container}" объявлен, но ни одного вида в нём нет`)
  }
}

// 6. Подписи кнопок очереди решений. Ядро присылает коды действий
//    (apply/approve/start/deny/reject/ignore) — не подписи. Интерфейс переводит
//    их в глаголы: «ПРИНЯТЬ» одинаково называло применение правки, разрешение
//    команды и старт квеста. Новый код без перевода снова даст обезличенное
//    «ПРИНЯТЬ», и заметить это можно только на живой очереди.
{
  const go = read('internal/app/decisions.go')
  const js = webviewSource
  // Коды приходят и полем структуры, и через переменную: `accept := "apply"`.
  // Проверка знала только первую форму и молча пропустила код "resolve",
  // добавленный второй — слепое пятно ровно того рода, что она и ищет.
  const codes = new Set([
    ...[...go.matchAll(/Accept:\s*"([a-z]+)"/g)].map(m => m[1]),
    ...[...go.matchAll(/Reject:\s*"([a-z]+)"/g)].map(m => m[1]),
    ...[...go.matchAll(/\b(?:accept|reject)\s*:?=\s*"([a-z]+)"/g)].map(m => m[1]),
  ])
  if (!codes.size) fail('в ядре не найдено ни одного кода решения — сверка подписей ослепла')
  const verbsBlock = js.match(/const DECISION_VERBS = \{([\s\S]*?)\}/)
  if (!verbsBlock) fail('интерфейс больше не переводит коды решений в глаголы')
  const translated = new Set(verbsBlock ? [...verbsBlock[1].matchAll(/([a-z]+):/g)].map(m => m[1]) : [])
  for (const code of codes) {
    if (!translated.has(code)) fail(`код решения "${code}" не переведён в глагол — кнопка скажет обезличенное «ПРИНЯТЬ»`)
  }
}

// 7. Пути разрешения решений. Ядро кладёт в каждый элемент очереди свой путь,
//    а extension.js пропускает только пути из закрытого списка. Расходятся —
//    и элемент виден, но принять его нечем: очередь показывает работу, которую
//    невозможно разблокировать, и молчит об этом.
{
  const go = read('internal/app/decisions.go')
  const ext = extensionPackage

  const patterns = [
    ...[...go.matchAll(/(?:Path|Reject):\s*fmt\.Sprintf\("([^"]+)"/g)].map(m => m[1]),
    ...[...go.matchAll(/(?:Path|Reject):\s*"(\/api\/[^"]+)"/g)].map(m => m[1]),
  ]
  if (!patterns.length) fail('в ядре не найдено ни одного пути разрешения — сверка маршрутов ослепла')

  // Закрывающую скобку ищем в начале строки: внутри самих маршрутов есть «]»
  // от классов символов вроде [A-Za-z0-9_-], и ленивый поиск обрывался на ней.
  const listBlock = ext.match(/DECISION_RESOLVE_ROUTES = \[([\s\S]*?)\n\]/)
  if (!listBlock) fail('extension.js больше не объявляет закрытый список путей решения')
  const routes = listBlock
    ? [...listBlock[1].matchAll(/\/\^(.+?)\$\/[gimsuy]*/g)].map(m => new RegExp(`^${m[1]}$`))
    : []
  if (listBlock && !routes.length) fail('закрытый список путей решения оказался пустым')

  // Идентификаторы ядра — префикс_hex (domain.NewID), поэтому образец такой же.
  for (const pattern of new Set(patterns)) {
    const sample = pattern.replace(/%s/g, 'run_0123abcd')
    if (!routes.some(route => route.test(sample))) {
      fail(`путь решения "${pattern}" не проходит закрытый список extension.js — элемент очереди будет виден, но неразрешим`)
    }
  }
}

// 8. Что можно отменить — по видам решений. Панель обещала метку «необратимо»,
//    которой на этом экране не бывает: у Decision нет такого поля. Человек читал
//    это как «всё здесь откатывается», хотя выполненную команду не отменить.
//    Новый вид решения без своего ответа тихо уедет в общий случай.
{
  const go = read('internal/app/decisions.go')
  const js = webviewSource
  const kinds = [...go.matchAll(/DecisionKind = "([a-z-]+)"/g)].map(m => m[1])
  if (!kinds.length) fail('в ядре не найдено ни одного вида решения — сверка ответов ослепла')

  const noteBlock = js.match(/function decisionReversibilityNote[\s\S]*?\n\}/)
  if (!noteBlock) fail('интерфейс больше не объясняет обратимость решения')
  const covered = new Set(noteBlock ? [...noteBlock[0].matchAll(/case '([a-z-]+)'/g)].map(m => m[1]) : [])
  for (const kind of kinds) {
    if (!covered.has(kind)) {
      fail(`вид решения "${kind}" не назван в decisionReversibilityNote — уедет в общий ответ`)
    }
  }
  // Комментарии не в счёт: объяснение починки цитирует убранное обещание, и
  // проверка по исходнику ловила собственный комментарий.
  const withoutComments = js.replace(/\/\*[\s\S]*?\*\//g, ' ').replace(/^\s*\/\/.*$/gm, ' ')
  if (/кроме помеченного/.test(withoutComments)) {
    fail('вернулось обещание метки «необратимо», которой на экране решений не бывает')
  }
}

// 9. Ни один раздел не остаётся в вечной «загрузке». Раздел уходит в 'loading'
//    перед запросом и выходит только ответом; при отказе ответа нет. Канал
//    отказа один — notify шлёт {type:'error'}, — и каждый раздел, умеющий
//    уходить в загрузку, обязан из неё этим сообщением спасаться. Проверяем
//    механически, потому что новый раздел добавляют одной строкой 'loading',
//    а про спасение забывают: сам по себе он замрёт молча.
//
//    Предел этой проверки: она читает текст, а не поведение. Забытое спасение
//    она ловит, отключённое (`if (false)` вокруг живой строки) — нет. Поведение
//    проверяется смоуком, но только для очереди решений:
//    scripts/smoke-hub-failed-request-recovery.js. Остальные восемь разделов
//    держатся на этой текстовой проверке.
{
  const ext = read('vscode-extension/extension.js')
  const js = webviewSource

  // Расширение обязано называть упавший запрос: без имени интерфейс не знает,
  // какой раздел винить, и гасит всё ждущее скопом.
  if (!/this\.post\(\{ type: 'error', message, request, \.\.\.detail \}\)/.test(ext)) {
    fail('notify больше не сообщает имя упавшего запроса — раздел не узнает себя')
  }
  // Перехватов `catch (error)` в файле несколько; нужен именно тот, что
  // закрывает разбор сообщений от webview, иначе проверка смотрит не туда.
  const handleAt = ext.indexOf('async handleMessage(')
  const catchBlock = handleAt < 0 ? null : ext.slice(handleAt).match(/\} catch \(error\) \{[\s\S]{0,600}?\n {4}\}/)
  if (!catchBlock) fail('не найден перехват ошибок обработчика сообщений — шов отказа проверить не на чем')
  else if (!/this\.notify\(error, String\(message\?\.type \|\| ''\), \{[\s\S]*?workOrderId: String\(message\?\.workOrderId \|\| ''\)/.test(catchBlock[0])) {
    fail('перехват ошибок не передаёт имя запроса в notify — интерфейс получит отказ без адресата')
  }

  const handler = js.match(/if \(message\.type === 'error'\) \{[\s\S]*?\n {2}\}/)
  if (!handler) fail('webview не обрабатывает {type:\'error\'} — сообщение об отказе некому услышать')
  else {
    const rescued = new Set([...handler[0].matchAll(/(\w+)Status = 'error'/g)].map(m => m[1]))
    const loading = new Set([...js.matchAll(/(\w+)Status = 'loading'/g)].map(m => m[1]))
    for (const section of loading) {
      if (!rescued.has(section)) {
        fail(`раздел "${section}" уходит в загрузку, но не спасается при отказе — замрёт в «загрузка…» навсегда`)
      }
    }
  }

  const table = js.match(/const FAILED_REQUEST_SECTIONS = \{[\s\S]*?\n\}/)
  if (!table) fail('пропала таблица FAILED_REQUEST_SECTIONS — некому связать отказ с разделом')
  else {
    const entries = [...table[0].matchAll(/^\s*([a-zA-Z]+): '([a-zA-Z]+)',/gm)]
    if (!entries.length) fail('таблица FAILED_REQUEST_SECTIONS пуста — восстановление ни к чему не привязано')
    for (const [, request, section] of entries) {
      if (!js.includes(`type: '${request}'`)) {
        fail(`в таблице отказов есть запрос "${request}", которого webview не шлёт — раздел останется в загрузке`)
      }
      if (!js.includes(`${section}Status = 'loading'`)) {
        fail(`в таблице отказов есть раздел "${section}", который никогда не уходит в загрузку`)
      }
    }
  }
}

// 10. Состояния индекса называются одинаково во всех трёх местах. Ядро умеет
//     сказать «ошибка» и «папка не открыта», строка состояния IDE их различает
//     давно, а заголовок Чертога сваливал всё незнакомое в «БЕЗ ИНДЕКСА»: отказ
//     индексации человек читал как «индекс просто не построен» и шёл строить
//     его заново. Один продукт не может называть одно и то же по-разному.
{
  const indexGo = readGoPackage('internal/workspace')
  const appGo = read('internal/app/app.go')
  const ext = extensionPackage
  const js = webviewSource

  const coreStates = new Set([
    ...[...indexGo.matchAll(/IndexStatus\{State: "([a-z_]+)"/g)].map(m => m[1]),
    ...[...indexGo.matchAll(/status\.State = "([a-z_]+)"/g)].map(m => m[1]),
    ...[...appGo.matchAll(/IndexStatus\{State: "([a-z_]+)"/g)].map(m => m[1]),
  ])
  coreStates.add('ready') // строится успешно — состояние по умолчанию для готового индекса
  if (coreStates.size < 4) fail('в ядре нашлось подозрительно мало состояний индекса — проверка вхолостую')

  const formatBlock = ext.match(/function formatIndexStatus\([\s\S]*?\n\}/)
  if (!formatBlock) fail('пропала formatIndexStatus — не с чем сверять словарь заголовка')
  const barStates = new Set(formatBlock ? [...formatBlock[0].matchAll(/status\.state === '([a-z_]+)'/g)].map(m => m[1]) : [])

  const labelLine = js.split('\n').find(line => line.includes('const indexLabel ='))
  if (!labelLine) fail('пропал indexLabel — заголовку Чертога нечем назвать состояние индекса')
  const hallStates = new Set(labelLine ? [...labelLine.matchAll(/state===?'([a-z_]+)'/g)].map(m => m[1]) : [])

  for (const state of new Set([...coreStates, ...barStates])) {
    if (!hallStates.has(state)) {
      fail(`состояние индекса "${state}" известно ядру или строке состояния IDE, но заголовок Чертога его не различает`)
    }
  }
}

// 11. У каждого значения статуса есть русская подпись. Словари статусов живут в
//     ядре (internal/domain), а подписи — в интерфейсе, и при отсутствии
//     подписи карточка печатает само значение: английское слово среди русских.
//     Так уже случалось с индексом («ИНДЕКС not_built» на первом же экране), и
//     повторится с любым новым статусом, добавленным в ядро.
//
//     Лишние подписи — не ошибка: probing и waiting существуют только на
//     стороне клиента, у ядра их нет и быть не должно.
{
  const js = webviewSource
  const domain = ['internal/domain/types.go', 'internal/domain/hub.go']
    .map(file => read(file)).join('\n')

  const vocabularies = {}
  // Не только *Status: вид памяти печатается тем же способом и тем же
  // подстрочником, поэтому словари ищем и по *Kind.
  for (const hit of domain.matchAll(/[A-Za-z]+\s+([A-Za-z]+(?:Status|Kind))\s*=\s*"([a-z_]+)"/g)) {
    ;(vocabularies[hit[1]] ||= new Set()).add(hit[2])
  }

  const pairs = [
    ['RunStatus', 'statusLabels'],
    ['QuestStatus', 'questStatusLabels'],
    ['ChangeSetStatus', 'changeSetStatusLabels'],
    ['ConnectionStatus', 'connectionStatusLabels'],
    // Вид памяти печатается тем же способом: `memoryKindLabels[kind] || kind`.
    // Без подписи на карточке появится сырое «PROJECT» — ровно тот случай,
    // ради которого эта проверка и написана.
    ['MemoryKind', 'memoryKindLabels'],
  ]
  for (const [enumName, mapName] of pairs) {
    const values = vocabularies[enumName]
    if (!values || values.size < 3) {
      fail(`словарь ${enumName} не найден в ядре — сверка подписей прошла бы вхолостую`)
      continue
    }
    const at = js.indexOf(`const ${mapName} =`)
    if (at < 0) {
      fail(`в интерфейсе нет словаря подписей ${mapName}`)
      continue
    }
    const block = js.slice(at, js.indexOf('}', at) + 1)
    const labels = new Set([...block.matchAll(/([a-z_]+)\s*:/g)].map(m => m[1]))
    for (const value of values) {
      if (!labels.has(value)) {
        fail(`статус ${enumName}.${value} без подписи в ${mapName} — на экране покажется сырое «${value}»`)
      }
    }
  }
}

// 12. Объяснения ошибок ядра одинаковы в обоих местах. Ядро отвечает
//     по-английски, а Хаб русскоязычный, и один и тот же сбой человек может
//     увидеть дважды: красной полосой (extension.js) и строкой в ленте прогона
//     (media/main.js). Списки перевода вынужденно живут в двух файлах — у
//     расширения и webview разные среды. Разойдутся — один и тот же отказ будет
//     назван по-разному, а то и переведён лишь в одном месте.
{
  const ext = read('vscode-extension/extension.js')
  const js = webviewSource

  // Сверяются и образцы, и сами тексты. Одних образцов мало: таблицы уже
  // разошлись формулировкой — красная полоса звала в «Бюджет проекта», а лента
  // прогона про него молчала. Один и тот же отказ не может называться
  // по-разному в зависимости от того, на каком экране его встретили.
  const hintsOf = (source, where) => {
    const block = source.match(/const CORE_FAILURE_HINTS = \[[\s\S]*?\n\s*\]/)
    if (!block) {
      fail(`в ${where} нет таблицы CORE_FAILURE_HINTS — переводить ошибки ядра нечем`)
      return null
    }
    const hints = new Map()
    for (const row of block[0].matchAll(/\[\/([^/]+)\/i,\s*'([^']*)'\]/g)) {
      hints.set(row[1], row[2])
    }
    return hints
  }

  const inExtension = hintsOf(ext, 'extension.js')
  const inWebview = hintsOf(js, 'ui/client')
  if (inExtension && inWebview) {
    if (inExtension.size < 3 || inWebview.size < 3) {
      fail('таблицы объяснений подозрительно малы — сверка прошла бы вхолостую')
    }
    // Webview переводит и то, что видит только он (причины неудачи прогона),
    // поэтому требуем не равенства, а общего ядра: всё, что знает webview про
    // общие случаи, обязано быть известно и расширению — теми же словами.
    for (const [pattern, russian] of inWebview) {
      if (/completion gate|deadline exceeded/.test(pattern)) continue
      if (!inExtension.has(pattern)) {
        fail(`объяснение "${pattern}" есть в webview, но не в extension.js — один и тот же отказ переведут не везде`)
        continue
      }
      if (inExtension.get(pattern) !== russian) {
        fail(`объяснение "${pattern}" расходится словами: extension.js «${inExtension.get(pattern)}», webview «${russian}»`)
      }
    }
  }
}

// 13. Подсказка инструмента адресована либо модели, либо человеку, и различить
//     их можно только по языку: указания агенту пишутся по-английски («retry
//     search_code with max_chunks between 1 and 20»), советы человеку — по-русски
//     («сохраните профиль в Гильдии → Базы данных»). Интерфейс показывает
//     человеку только вторые.
//
//     Соглашение держится на дисциплине авторов, поэтому проверяем его форму:
//     русская подсказка, обращённая к модели, или английская, обращённая к
//     человеку, сломает разделение молча. Полную проверку смысла сделать нельзя
//     — ловим хотя бы то, что признак вообще используется и обе группы
//     непусты.
{
  const js = webviewSource
  if (!/hint && \/\[а-яё\]\/i\.test\(hint\)/.test(js)) {
    fail('интерфейс снова показывает человеку любые подсказки — включая указания модели')
  }

  const hints = { human: 0, agent: 0 }
  for (const file of ['internal/tools/workspace_tools.go', 'internal/tools/sql_tools.go', 'internal/tools/docker.go', 'internal/agent/engine.go']) {
    const source = read(file)
    for (const hit of source.matchAll(/FailWithHint\([^,]+,\s*[^,]+,\s*"([^"]{10,200})"/g)) {
      if (/[а-яё]/i.test(hit[1])) hints.human += 1
      else hints.agent += 1
    }
  }
  if (hints.human === 0 || hints.agent === 0) {
    fail(`подсказки перестали делиться на две группы (человеку: ${hints.human}, модели: ${hints.agent}) — признак языка больше не работает`)
  }
}

// 10. Значения пресетов компаньона и мастера. Одни и те же шесть (и четыре)
//     чисел записаны дважды: в COMPANION_PRESETS/ORCHESTRATOR_PRESETS для
//     экрана настройки и в PresetDefaults ядра, которым оно нормализует конфиг.
//     Разъедутся — человек выберет «Наставника», увидит на ползунках одно, а
//     помощник станет вести себя по другому набору. По отдельности этого не
//     заметит ни один тест: обе стороны внутри себя непротиворечивы.
{
  const js = webviewSource

  const uiPresets = name => {
    const at = js.indexOf(`const ${name} = [`)
    if (at < 0) return null
    const body = js.slice(at, js.indexOf('\n]', at))
    const found = new Map()
    for (const line of body.split('\n')) {
      const id = line.match(/\{ id: '([a-z-]+)'/)
      const values = line.match(/values: \{([^}]+)\}/)
      if (!id || !values) continue
      const numbers = new Map()
      for (const pair of values[1].matchAll(/(\w+):\s*(-?\d+)/g)) numbers.set(pair[1], Number(pair[2]))
      found.set(id[1], numbers)
    }
    // Пресет без values выпал бы из разбора молча, и сверка прошла бы вхолостую
    // именно там, где значения и разъехались. Объявлено на экране столько же,
    // сколько разобрано, — иначе разбор сломан.
    const declared = (body.match(/{ id: '[a-z-]+'/g) || []).length
    return { presets: found, declared }
  }

  // Ядро пишет пресеты парами строк: «cfg.A, cfg.B, cfg.C = 1, 2, 3».
  const goPresets = (file, funcName) => {
    const source = read(file)
    const at = source.indexOf(`func ${funcName}(`)
    if (at < 0) return null
    const body = source.slice(at, source.indexOf('\n}', at))
    const found = new Map()
    let current = ''
    for (const line of body.split('\n')) {
      const label = line.match(/^\s*case "([a-z-]+)":/)
      if (label) { current = label[1]; found.set(current, new Map()); continue }
      const assign = line.match(/^\s*cfg\.(.+?)\s*=\s*(.+?)\s*$/)
      if (!assign || !current) continue
      const fields = assign[1].split(',').map(item => item.replace('cfg.', '').trim())
      const values = assign[2].split(',').map(item => Number(item.trim()))
      if (fields.length !== values.length || values.some(Number.isNaN)) continue
      fields.forEach((field, index) => {
        found.get(current).set(field[0].toLowerCase() + field.slice(1), values[index])
      })
    }
    return found
  }

  for (const [label, uiName, file, funcName] of [
    ['компаньона', 'COMPANION_PRESETS', 'internal/companion/interventions.go', 'PresetDefaults'],
    ['мастера', 'ORCHESTRATOR_PRESETS', 'internal/orchestrator/config.go', 'PresetDefaults'],
  ]) {
    const parsed = uiPresets(uiName)
    const ui = parsed?.presets
    const core = goPresets(file, funcName)
    if (!ui || !ui.size) { fail(`не разобрать ${uiName} — сверять пресеты ${label} не с чем`); continue }
    if (ui.size !== parsed.declared) { fail(`в ${uiName} объявлено ${parsed.declared} пресетов, разобрано ${ui.size} — разбор сломан, сверка прошла бы вхолостую`); continue }
    if (!core || !core.size) { fail(`не разобрать ${funcName} в ${file} — сверять пресеты ${label} не с чем`); continue }
    for (const [id, values] of ui) {
      if (!core.has(id)) { fail(`пресет ${label} «${id}» есть на экране, но ядро его не знает`); continue }
      // Разбор, вернувший пустой набор, прошёл бы молча и вхолостую.
      if (values.size < 4) { fail(`пресет ${label} «${id}»: с экрана разобрано ${values.size} значений — сверять нечем`); continue }
      for (const [field, value] of values) {
        const mine = core.get(id).get(field)
        if (mine === undefined) fail(`пресет ${label} «${id}»: ядро не задаёт ${field}`)
        else if (mine !== value) fail(`пресет ${label} «${id}»: на экране ${field}=${value}, у ядра ${mine}`)
      }
    }
  }
}

// 11. Наборы «уже спросили». Договорённость 9 сторожит разделы, которые ждут
//     ответа через переменную *Status. Но ждут и иначе: годность персонажа и
//     политика мастера кладут ключ запроса в Set и снимают его только приходом
//     ответа. При отказе ответа не будет — ключ остаётся ждать вечно, а повтор
//     блокирует сам себя, потому что смотрит в тот же Set. Экран настройки
//     навсегда застревал на «Политика считается ядром…», а готовность, которая
//     при неполученном ответе намеренно не отрицается, навсегда заявляла
//     «Персонаж готов к квесту» по данным, которых никто не присылал.
//
//     Проверка текстовая, как и девятая: каждый Set с именем *Inflight обязан
//     упоминаться в обработчике {type:'error'}. Что именно там с ним делают —
//     проверяет смоук scripts/smoke-setup-survives-core-refusal.js.
{
  const js = webviewSource

  const inflight = new Set([...js.matchAll(/const (\w+Inflight) = new Set\(\)/g)].map(m => m[1]))
  if (!inflight.size) fail('в интерфейсе не осталось наборов *Inflight — проверка смотрит не туда')

  const handler = js.match(/if \(message\.type === 'error'\) \{[\s\S]*?\n {2}\}/)
  if (!handler) fail('webview не обрабатывает {type:\'error\'} — спасать наборы запросов некому')
  else {
    for (const name of inflight) {
      if (!handler[0].includes(name)) {
        fail(`набор "${name}" копит ключи запросов, но при отказе о нём никто не вспоминает — запрос застрянет навсегда`)
      }
    }
  }
}

// 10. Клавиатурный маршрут. Списки Хаба объявляли себя как role="listbox" с
//     role="option" на обычных кнопках: скринридеру обещали listbox, а вели
//     себя они как набор кнопок и стрелок не знали вовсе. Ложное объявление
//     снято, перебор добавлен — и обе половины этого решения должны держаться
//     вместе, иначе вернётся либо враньё, либо мёртвая стрелка.
//
//     Проверка статическая: что именно делает стрелка, меряет
//     scripts/smoke-hub-keyboard-navigation.js.
{
  const js = webviewSource

  // Запрет не на сами роли, а на ложное объявление. Список файлов под кареткой
  // (master-mention-ui.js) стрелки как раз знает — их перебор написан в нём же,
  // — и объявить себя списком имеет право. Остальным по-прежнему нельзя: они
  // обещали бы скринридеру перебор, которого нет.
  //
  // Право проверяется в том же файле, где стоит роль: объявил listbox — покажи
  // обработку ArrowDown и ArrowUp рядом.
  for (const role of ['listbox', 'option']) {
    for (const file of readSourceFiles('vscode-extension/ui/client')) {
      if (!file.text.includes(`role="${role}"`)) continue
      const walks = file.text.includes("'ArrowDown'") && file.text.includes("'ArrowUp'")
      if (!walks) fail(`${file.name}: объявлен role="${role}", а перебора стрелками в этом файле нет — кнопка выдаёт себя за элемент списка`)
    }
  }

  const marked = [...js.matchAll(/data-keynav="([a-z]*)"/g)].map(match => match[1])
  if (!marked.length) fail('в интерфейсе не осталось ни одного data-keynav — перебирать стрелками нечего')
  for (const axis of new Set(marked)) {
    if (axis !== 'column' && axis !== 'row') fail(`data-keynav="${axis}" — ось не column и не row, обработчик такой не знает`)
  }

  const handler = js.match(/root\.addEventListener\('keydown'[\s\S]*?\n\}\)/)
  if (!handler) fail('в интерфейсе нет обработчика keydown — перебор подключать некуда')
  else if (!handler[0].includes('handleListKeydown')) {
    fail('обработчик keydown не вызывает handleListKeydown — списки помечены, а стрелки мертвы')
  }

  // Полоса вкладок обязана быть одним пунктом табуляции, а не пятью: без
  // роумингового tabindex Tab обходит каждую вкладку по очереди.
  for (const tag of js.match(/<button[^>]*role="tab"[^>]*>/g) || []) {
    if (!tag.includes('tabindex')) fail('вкладка с role="tab" не объявляет tabindex — полоса ловит табуляцию целиком')
  }

  // Фокус обязан пережить перерисовку, вызванную самой клавишей. Найдено в
  // браузере на живом стенде: клавиша меняет выбор, выбор вызывает полную
  // отрисовку, отрисовка уничтожает элемент с фокусом — и следующая клавиша
  // приходит в body мимо обработчика на root. Так были сломаны J и K, которые
  // подсказка на экране обещает с самого появления очереди.
  const capture = js.match(/function captureUi\(\)[\s\S]*?\n\}/)
  const restore = js.match(/function restoreUi\(snapshot\)[\s\S]*?\n\}/)
  if (!capture || !restore) fail('снимок интерфейса больше не собирается функциями captureUi/restoreUi — проверять нечего')
  else {
    if (!capture[0].includes('data-keynav')) fail('captureUi не запоминает фокус внутри перебираемого списка — клавиша сработает один раз')
    if (!restore[0].includes('snapshot.keynav')) fail('restoreUi не возвращает фокус в список — клавиша сработает один раз')
  }
}

// 14. Конструктор агента показывает ровно один шаг. Панели прячутся подстановкой
//     is-hidden в атрибут class, и подстановка шла по образцу
//     `class="profile-config-block`. У панели «Инструменты» перед ним стоит ещё
//     один класс — образец не совпадал, замена молча не срабатывала, и шаг 07
//     был виден на каждом шаге рядом с активным. Навигация обещала «ШАГ 01 / 10»,
//     а на экране стояли две несмежные панели.
{
  const page = execFileSync(process.execPath,
    [path.join(root, 'scripts/render-hub-surface.js'), 'constructor'],
    { encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 })
  // Только сами панели: profile-step-footer/-left/-right/-nav — это обвязка.
  const panels = [...page.matchAll(/class="([^"]*profile-step(?![-\w])[^"]*)"/g)].map(hit => hit[1])
  const constructorSource = read('vscode-extension/ui/client/agent-constructor.js')
  const stepsAt = constructorSource.indexOf('export const CONSTRUCTOR_STEPS = [')
  const stepsEnd = stepsAt < 0 ? -1 : constructorSource.indexOf('\n]', stepsAt)
  const stepsBlock = stepsEnd < 0 ? '' : constructorSource.slice(stepsAt, stepsEnd)
  const steps = [...stepsBlock.matchAll(/\{ id: '[a-z]+',/g)].length
  if (panels.length !== steps || !steps) {
    fail(`конструктор отрисовал ${panels.length} панелей при ${steps} шагах — проверка шага прошла бы вхолостую`)
  }
  const visible = panels.filter(cls => !/\bis-hidden\b/.test(cls))
  if (visible.length !== 1) {
    fail(`конструктор показывает ${visible.length} панелей одновременно (${visible.join(' | ')}) — навигация обещает один шаг`)
  }
}

// 15. Вкладку Хаба меняют либо по прямому указанию человека, либо через
//     focusTab, который не выбрасывает из онбординга.
//
//     Полтора десятка обработчиков ставили вкладку как следствие своего дела:
//     сохранил навык — «Навыки», запустил флоу — «Обзор». Пока человек в
//     настройке системных агентов, любое такое следствие закрывало её на
//     середине: нажали «перестроить индекс» — и настройки нет. Новый прямой
//     `this.selectedTab = …` вернёт ту же болезнь молча, поэтому список мест
//     закрыт. Поведение проверяет scripts/smoke-hub-onboarding-not-closed.js.
//     19 сентября `focusTab` и `showWideHere` уехали в
//     `hub-surfaces-controller.js`, где провайдер приходит доводом.
//     Счёт теперь идёт по всему пакету и сводит `provider.selectedTab`
//     с `this.selectedTab`: смена приёмника не меняет опасности.
{
  const ext = extensionPackage.split('provider.selectedTab = ').join('this.selectedTab = ')
  const allowed = new Map([
    ["this.selectedTab = this.onboardingComplete ? 'master' : 'onboarding'", 1], // конструктор
    ["this.selectedTab = 'master'", 1],     // completeOnboarding
    ["this.selectedTab = 'onboarding'", 2], // restartOnboarding и отказ selectTab на первом запуске
    ['this.selectedTab = message.tab', 1],  // выбор вкладки с рейки
    ['this.selectedTab = tab', 2],          // focusTab и showWideHere
  ])
  const found = new Map()
  for (const hit of ext.matchAll(/this\.selectedTab = [^\r\n]*/g)) {
    const text = hit[0].trim().replace(/;\s*.*$/, '')
    found.set(text, (found.get(text) || 0) + 1)
  }
  if (!found.size) fail('в extension.js не осталось ни одной установки вкладки — проверка смотрит не туда')
  for (const [text, count] of found) {
    const expected = allowed.get(text) || 0
    if (count > expected) {
      fail(`«${text}» встречается ${count} раз при ${expected} разрешённых — побочная навигация закроет онбординг; используйте focusTab()`)
    }
  }
  const guard = ext.match(/function focusTab\([^)]*\) {[\s\S]*?\n {2}}/)
  if (!guard) fail('в пакете расширения нет focusTab — побочной навигации нечем себя сдержать')
  else if (!/selectedTab === 'onboarding'/.test(guard[0])) {
    fail('focusTab больше не защищает онбординг — вкладку снесёт первым же следствием')
  }
}

// 16. CLI-агенты сняты. Карточка исполнения показывает гарантии Point sandbox
//     (рабочая копия / процесс / сеть / секреты), а не границу Cursor.
{
  for (const label of ['Рабочая копия', 'Процесс', 'Сеть', 'Секреты']) {
    if (!webviewSource.includes(label)) fail(`в Execution пропала фактическая гарантия ${label}`)
  }
  if (webviewSource.includes('Cursor Agent работает в отдельной копии проекта')) {
    fail('карточка Cursor осталась после API-only cutover')
  }
}

// 17. Фоновое обновление boot не имеет права менять исполнителя уже
//     подготовленного квеста. В Hub выбранный id принадлежит projectAgents.
{
  // Проверка идёт по дереву `ui/client`, а не по `main.js`: 19 сентября разбор
  // снимка мира уехал в `world-state-inbox.js`, и вместе с ним имя состояния
  // получило приставку мешка — отсюда `(?:ui\.)?`.
  const hall = read('vscode-extension/ui/client/hall-onboarding-views.js')
  if (!/!agentById\((?:ui\.)?selectedProfileId\)/.test(webviewSource)) {
    fail('boot больше не проверяет выбранного исполнителя через общий projectAgent/profile lookup')
  }
  if (!hall.includes("preferredIds.find(id => id && profiles.some(item => item.id === id))")) {
    fail('компоновщик квеста может снова выбрать defaultProfileId, которого нет в текущем ростере')
  }
  if (!hall.includes("if (!profile) return '<button type=\"button\" class=\"send\" disabled>")) {
    fail('пустой ростер снова разрешает начать разведку без исполнителя')
  }
}

// 18. Доступ помощника. Ядро выдаёт его группами каталога — read, index, git
//     (policy.ProjectReadingGroups, из неё собран CompanionGrants). Экран
//     решает по тому же списку, какой навык можно надеть. Разъедься эти два
//     места — человек отметит навык, который ядро отвергнет вместе со всей
//     настройкой, и отказ придёт уже после «Сохранить».
{
  const go = read('internal/policy/grants.go')
  const core = go.match(/func ProjectReadingGroups\(\) \[\]string \{[\s\S]*?return \[\]string\{([^}]*)\}/)
  const ui = webviewSource.match(/const COMPANION_TOOL_GROUPS = \[([^\]]*)\]/)
  if (!core) fail('не разобрать ProjectReadingGroups — сверять доступ помощника не с чем')
  if (!ui) fail('интерфейс больше не объявляет COMPANION_TOOL_GROUPS — сверять доступ помощника не с чем')
  if (core && ui) {
    const names = source => [...source.matchAll(/["']([a-z_]+)["']/g)].map(match => match[1]).sort()
    const mine = names(core[1])
    const shown = names(ui[1])
    if (!mine.length) fail('в ProjectReadingGroups не разобрано ни одной группы — сверка прошла бы вхолостую')
    else if (mine.join(',') !== shown.join(',')) {
      fail(`доступ помощника разошёлся: ядро выдаёт ${mine.join(', ')}, экран считает ${shown.join(', ')}`)
    }
  }
  // Выбор на экране бесполезен, если тело запроса о нём не знает: ровно так
  // `skillIds` и терялись — молча, между «Сохранить» и ядром.
  const payload = read('vscode-extension/companion-chat-controller.js').match(/function companionConfigPayload\([\s\S]*?\n\}/)
  if (!payload) fail('не найден companionConfigPayload — путь настройки помощника не проверить')
  else if (!/skillIds/.test(payload[0])) fail('companionConfigPayload снова теряет skillIds — выбор навыков не доедет до ядра')
}

// 19. Фокус не двигает ленту. Поле ввода получает фокус после каждой отправки,
//     а снимок интерфейса возвращает его после каждой отрисовки. Обычный
//     `focus()` при этом подтягивает поле в видимую область и прокручивает
//     ближайшего предка; в боковой панели помощника это контейнер с
//     `overflow: hidden`, скроллбара у него нет, и разговор уезжал вверх на
//     каждой реплике. Замер на живом стенде: 241px за одну отправку.
{
  const js = webviewSource
  const focusFn = js.match(/function focusCompanionInput\(\)[\s\S]*?\n\}/)
  if (!focusFn) fail('не найдена focusCompanionInput — возврат фокуса проверить нечем')
  else if (!/focus\(\{ preventScroll: true \}\)/.test(focusFn[0])) {
    fail('поле помощника снова получает фокус без preventScroll — лента уедет при каждой отправке')
  }
  if (!/el\.focus\(\{ preventScroll: true \}\)/.test(js)) {
    fail('восстановление фокуса после отрисовки снова прокручивает раскладку')
  }
}

// 20. Уточняющий вопрос помощника — это его реплика, а не заготовленная реплика
//     человека. Чип уточнения отправлял вопрос обратно как сказанное человеком:
//     помощник получал собственный вопрос словами собеседника, отвечал на него
//     сам и спрашивал снова. Нажатие обязано готовить ответ, а не отправлять.
{
  const html = webviewSource.match(/function companionQuestionsHtml\([\s\S]*?\n\}/)
  if (!html) fail('не найдена companionQuestionsHtml — проверить чипы уточнений нечем')
  else {
    if (/data-action="companion-answer"/.test(html[0])) {
      fail('чип уточнения снова отправляет вопрос помощника как реплику человека')
    }
    if (!/data-action="companion-prefill"/.test(html[0])) {
      fail('чип уточнения больше не готовит ответ в поле ввода')
    }
  }
}

// 21. «Ответить иначе» просит другой путь, а не повторяет вопрос. Кнопка
//     отправляла ту же реплику заново, и при низкой температуре модель
//     возвращала тот же ответ: человек нажимал, ничего не менялось.
//     Признак повтора обязан доезжать до ядра — там он превращается в
//     указание не повторять ход.
{
  const js = webviewSource
  const handler = js.match(/action === .regenerate-companion-message.[\s\S]*?\n  }/)
  if (!handler) fail('не найден обработчик «Ответить иначе» — проверить повтор нечем')
  else if (!/retry: true/.test(handler[0])) fail('«Ответить иначе» снова шлёт тот же вопрос без просьбы о другом пути')
  if (!/retry: Boolean\(options.retry\)/.test(js)) fail('признак повтора не уходит расширению')
  const host = [
    read('vscode-extension/extension.js'),
    read('vscode-extension/companion-chat-controller.js'),
    read('vscode-extension/master-chat-controller.js'),
  ].join('\n')
  if (!/previousAnswerRejected:\s*(Boolean\(message\.retry\)|!!message\.retry)/.test(host)) {
    fail('расширение больше не передаёт просьбу о другом пути в ядро')
  }
}

// 22. Предупреждение о долгом ответе обязано прозвучать раньше, чем ядро сдастся.
//     Полоса ожидания молчит, пока модель думает, и человек не знает, идёт ли
//     ещё ответ. Порог предупреждения в интерфейсе и таймаут обращения к модели
//     в ядре — два числа об одном и том же ожидании: разъехавшись, они дадут
//     предупреждение уже после отката на местный разбор, то есть никогда.
{
  const core = read('internal/companion/read_tools.go')
  const timeout = core.match(/companionProviderTimeoutSeconds = (\d+)/)
  const ui = webviewSource.match(/COMPANION_SLOW_ANSWER_SECONDS\s*=\s*(\d+)/)
  if (!timeout) fail('в ядре не найден таймаут обращения к модели — сверить порог не с чем')
  else if (!ui) fail('в интерфейсе нет порога предупреждения о долгом ответе')
  else if (Number(ui[1]) >= Number(timeout[1])) {
    fail(`порог предупреждения (${ui[1]} с) не раньше таймаута ядра (${timeout[1]} с)`)
  }
}

// 23. Предел длины сообщения у интерфейса и у ядра — одно число. Ядро
//     отклоняет реплику длиннее 32 КБ, и до этой правки узнавал об этом
//     человек уже после отправки: текст уходил из поля, а возвращать его
//     приходилось копированием из ленты. Разъехавшись, числа вернут ровно ту
//     же потерю — проверка в интерфейсе пропустит то, что ядро отвергнет.
{
  const core = read('internal/companion/service.go')
  const coreLimit = core.match(/len\(req\.Message\) > (\d+)\s*\*\s*1024/)
  const uiLimit = webviewSource.match(/COMPANION_MESSAGE_LIMIT_BYTES = (\d+) \* 1024/)
  if (!coreLimit) fail('в ядре не найден предел длины сообщения помощника')
  else if (!uiLimit) fail('в интерфейсе нет предела длины сообщения помощника')
  else if (coreLimit[1] !== uiLimit[1]) {
    fail(`предел сообщения разъехался: интерфейс ${uiLimit[1]} КБ, ядро ${coreLimit[1]} КБ`)
  }
}

// 24. Четыре числа об одном ожидании обязаны идти по возрастанию: ожидание
//     заголовков < таймаут одного
//     обращения к провайдеру < бюджет обращений к модели < срок, после которого
//     расширение рвёт запрос к ядру. Разъехавшись, они дают ровно то, что было
//     на живом замере: первая попытка сжигает свой таймаут, вторая начинается на
//     остатке и умирает вместе с оборванным запросом, а местный разбор не
//     успевает ни выполниться, ни сохраниться — человек остаётся ни с чем.
{
  const tools = read('internal/companion/read_tools.go')
  // Срок ожидания живёт там же, где чтение потока: сначала в extension.js,
  // после выделения — в core-stream.js. Число одно, и проверка не должна
  // слепнуть от того, в каком из двух файлов оно сейчас лежит.
  const host = read('vscode-extension/extension.js') + '\n' + read('vscode-extension/core-stream.js')
  const headers = tools.match(/companionProviderHeaderTimeoutSeconds = (\d+)/)
  const attempt = tools.match(/companionProviderTimeoutSeconds = (\d+)/)
  const budget = tools.match(/companionModelBudget = (\d+) \* time\.Second/)
  const wait = host.match(/timeoutMs = (\d+)_000, allowStart = true, signal: externalSignal, onProgress/)
  if (!headers) fail('в ядре не найден срок ожидания заголовков провайдера')
  else if (!attempt) fail('в ядре не найден таймаут обращения к провайдеру')
  else if (!budget) fail('в ядре не найден бюджет обращений к модели')
  else if (!wait) fail('в расширении не найден срок ожидания ответа ядра')
  else {
    const [head, one, all, limit] = [Number(headers[1]), Number(attempt[1]), Number(budget[1]), Number(wait[1])]
    if (!(head < one)) fail(`ожидание заголовков (${head} с) не короче всей попытки (${one} с)`)
    if (!(one < all)) fail(`попытка (${one} с) не короче бюджета обращений к модели (${all} с)`)
    if (!(all < limit)) fail(`бюджет модели (${all} с) не короче ожидания расширения (${limit} с)`)
  }
}

// 25. Помощник — только модель по API. CLI и «встроенный разбор» как выбор
//     сняты: сохранённый Claude Code больше не должен опознаваться отдельным
//     режимом, а сборка настройки всегда идёт через connection/provider.
{
  const { companionConfigForBrain, companionBrainMode, normalizeBrainMode, COMPANION_BRAIN_MODES } = await import('./client/companion-compose.js')
  if (COMPANION_BRAIN_MODES.join(',') !== 'model') fail(`виды мозга не API-only: ${COMPANION_BRAIN_MODES.join(',')}`)
  if (normalizeBrainMode('cli') !== 'model') fail('устаревший CLI-режим не сводится к model')
  if (normalizeBrainMode('local') !== 'model') fail('устаревший local-режим не сводится к model')
  if (normalizeBrainMode('model') !== 'model') fail('нормализация теряет выбор сетевой модели')
  if (companionBrainMode({ provider: 'claude-code-cli', model: 'opus' }) !== 'model') fail('сохранённый CLI не должен оставаться отдельным режимом')
  if (companionBrainMode({ provider: 'openai-compatible', model: 'qwen' }) !== 'model') fail('сетевая модель опознаётся неверно')
  if (companionBrainMode({}) !== 'model') fail('пустая настройка обязана вести на API-мозг')

  const network = companionConfigForBrain({ mode: 'model', connectionId: 'c1', model: 'qwen', preset: 'balanced', temperature: 0.2, maxOutputTokens: 1200, skillIds: [] },
    { current: {}, connection: { id: 'c1', provider: 'openai-compatible', presetId: 'llmux', baseUrl: 'https://gate/v1' } })
  if (network.provider !== 'openai-compatible' || network.baseUrl !== 'https://gate/v1') {
    fail(`сетевая модель собрана неверно: ${JSON.stringify(network.provider)} ${JSON.stringify(network.baseUrl)}`)
  }
  if (network.model !== 'qwen') fail(`выбранная модель потерялась: ${JSON.stringify(network.model)}`)

  // CLI-провайдер во входе больше не переключает режим: сборка идёт через
  // connection/provider черновика, а mode: 'cli' нормализуется снаружи.
  const fromCliishDraft = companionConfigForBrain(
    { mode: 'cli', model: 'opus', provider: 'claude-code-cli', preset: 'balanced', temperature: 0.2, maxOutputTokens: 1200, skillIds: [] },
    { current: {}, connection: { id: 'c2', provider: 'openai-compatible', presetId: 'custom', baseUrl: 'https://api.example/v1' } },
  )
  if (fromCliishDraft.provider !== 'openai-compatible') {
    fail(`CLI-провайдер во входе снова победил connection: ${JSON.stringify(fromCliishDraft.provider)}`)
  }
  if (companionBrainMode({ provider: 'claude-code-cli', model: 'opus' }) === 'cli') {
    fail('companionBrainMode снова возвращает cli')
  }
}

// 25. Предел длины реплики Мастера у интерфейса и у ядра — одно число. Ядро
//     отклоняет реплику длиннее maxChatMessage, и без парного числа в
//     композере человек узнавал бы об этом уже после отправки: текст ушёл бы из
//     поля, а возвращать его пришлось бы копированием из ленты. Ровно та же
//     потеря, от которой договорённость 23 бережёт разговор с помощником.
{
  const core = readGoPackage('internal/orchestrator')
  const coreLimit = core.match(/maxChatMessage\s*=\s*(\d+)\s*\*\s*1024/)
  const uiLimit = webviewSource.match(/MASTER_MESSAGE_LIMIT_BYTES = (\d+) \* 1024/)
  if (!coreLimit) fail('в ядре не найден предел длины реплики Мастера')
  else if (!uiLimit) fail('в интерфейсе нет предела длины реплики Мастера')
  else if (coreLimit[1] !== uiLimit[1]) {
    fail(`предел реплики Мастера разъехался: интерфейс ${uiLimit[1]} КБ, ядро ${coreLimit[1]} КБ`)
  }
}

// 26. Предупреждение о долгом ответе Мастера звучит раньше, чем ядро сдаётся.
//     Ход Мастера идёт несколько минут, и всё это время в ленте стоит одно
//     слово «Думает…»: без счёта секунд раздел неотличим от зависшего, а
//     предупреждение, выданное после отката на движок Point, — это уже не
//     предупреждение, а объяснение задним числом.
{
  const core = read('internal/orchestrator/task_intake.go')
  const budget = core.match(/masterIntakeTimeoutSeconds\s+=\s+(\d+)/)
  const ui = webviewSource.match(/MASTER_SLOW_ANSWER_SECONDS = (\d+)/)
  if (!budget) fail('в ядре не найден срок хода Мастера')
  else if (!ui) fail('в интерфейсе нет порога предупреждения о долгом ответе Мастера')
  else if (Number(ui[1]) >= Number(budget[1])) {
    fail(`порог предупреждения Мастера (${ui[1]} с) не раньше срока хода в ядре (${budget[1]} с)`)
  }
}

// 27. Русские имена инструментов Мастера сходятся с тем, чем он располагает.
//     Набор закрыт ядром: companionReadTools собирает читающие инструменты,
//     masterReadTools добавляет чтение сущностей. Пока словарь интерфейса
//     держался на памяти, в нём жил несуществующий `index_search`, а трёх
//     настоящих не было — и разговор писал «обратился к инструменту» там, где
//     мог назвать инструмент. Ни один тест по отдельности этого не видит.
{
  const companion = read('internal/app/companion_tools.go')
  const master = read('internal/app/master_tools.go')
  const coreTools = new Set()
  for (const match of companion.matchAll(/workbenchtools\.(\w+)\{/g)) {
    // Имя инструмента ядро пишет змейкой, а тип — горбами: ReadFile → read_file.
    // Tool и Registry — не инструменты, а тип среза и сам реестр.
    if (['Tool', 'Registry', 'NewRegistry'].includes(match[1])) continue
    coreTools.add(match[1].replace(/([a-z0-9])([A-Z])/g, '$1_$2').toLowerCase())
  }
  for (const match of master.matchAll(/masterEntityDefinition\("(\w+)"/g)) coreTools.add(match[1])
  // Инструменты разговора — задание, уточнения, память — объявлены
  // константами оркестратора. Они такие же шаги хода, и в ленте их надо
  // называть, а не писать «обратился к инструменту».
  const actions = read('internal/orchestrator/master_actions.go')
  for (const match of actions.matchAll(/masterAction\w+\s*=\s*"(\w+)"/g)) coreTools.add(match[1])
  // read_skill доступен компаньону с навыками, Мастеру его не дают
  // (newCompanionReadTools(fs, nil)) — но подпись у него общая, и держать её
  // в словаре честнее, чем ловить «обратился к инструменту» при первой же
  // выдаче навыков Мастеру.
  coreTools.delete('read_skill')
  const dictionary = read('vscode-extension/ui/client/master-tool-names.js')
  const known = new Set()
  const body = dictionary.slice(dictionary.indexOf('MASTER_TOOL_NAMES = {'), dictionary.indexOf('MASTER_TOOL_NAMES_NOW'))
  for (const match of body.matchAll(/^\s{2}(\w+):/gm)) known.add(match[1])
  const missing = [...coreTools].filter(name => !known.has(name)).sort()
  const extra = [...known].filter(name => !coreTools.has(name)).sort()
  if (!coreTools.size) fail('в ядре не найден ни один читающий инструмент Мастера — сверка имён впустую')
  if (missing.length) fail(`у инструментов Мастера нет русских имён: ${missing.join(', ')} — разговор назовёт их «обратился к инструменту»`)
  if (extra.length) fail(`в словаре интерфейса есть имена, которых нет у Мастера: ${extra.join(', ')}`)
  // Настоящее время — второй словарь, и расходиться они не должны: строка
  // ожидания иначе молчит там, где готовый шаг умеет назвать инструмент.
  const now = dictionary.slice(dictionary.indexOf('MASTER_TOOL_NAMES_NOW = {'), dictionary.indexOf('export function masterToolName'))
  const nowNames = new Set([...now.matchAll(/^\s{2}(\w+):/gm)].map(match => match[1]))
  const nowMissing = [...known].filter(name => !nowNames.has(name)).sort()
  if (nowMissing.length) fail(`у инструментов нет имени в настоящем времени: ${nowMissing.join(', ')} — строка ожидания промолчит`)
}

// 28. Формы ответа и режимы работы, названные в композере, совпадают с теми,
//     что принимает ядро. Расхождение молчит в обе стороны: непоказанная форма
//     недостижима (так три года были недостижимы «План» и «Сначала вопросы»), а
//     показанная лишняя уедет в ядро и вернётся отказом «неизвестная форма
//     ответа». Хуже того, сохранённый разговор с непоказанной формой рисуется
//     как «Авто» — интерфейс врёт о собственном состоянии.
{
  const go = read('internal/app/master_sessions.go')
  const views = read('vscode-extension/ui/client/master-session-views.js')
  // Ядро перечисляет допустимые значения в `case "a", "b", …:` внутри своей
  // ветки; интерфейс — парами ['id','Подпись'] в объявлении списка.
  // Отсчёт идёт за самим якорем: `case "mode":` — это внешняя ветка, а
  // перечисление лежит во вложенном switch строкой ниже.
  const coreList = after => {
    const at = go.indexOf(after)
    const match = at < 0 ? null : go.slice(at + after.length).match(/case ((?:"[a-z_]+"(?:, )?)+):/)
    return match ? match[1].match(/"([a-z_]+)"/g).map(item => item.replaceAll('"', '')) : []
  }
  const uiList = declaration => {
    const at = views.indexOf(declaration)
    if (at < 0) return []
    const line = views.slice(at, views.indexOf('\n', at))
    return [...line.matchAll(/\['([a-z_]+)',/g)].map(match => match[1])
  }
  const pairs = [
    ['форма ответа', coreList('case "mode":'), uiList('const modes=[')],
    ['режим работы', coreList('case "workMode":'), uiList('const works=[')],
  ]
  for (const [what, core, ui] of pairs) {
    if (!core.length) { fail(`в ядре не найдено перечисление «${what}» — сверять не с чем`); continue }
    if (!ui.length) { fail(`в интерфейсе не найден список «${what}»`); continue }
    const missing = core.filter(item => !ui.includes(item))
    const extra = ui.filter(item => !core.includes(item))
    if (missing.length) fail(`${what}: ядро принимает ${missing.join(', ')}, а композер этого не предлагает — значение недостижимо, а сохранённый разговор покажет чужую подпись`)
    if (extra.length) fail(`${what}: композер предлагает ${extra.join(', ')}, а ядро таких не знает — выбор вернётся отказом`)
  }
}

// 29. Две связки «вебвью — расширение», которые рвутся молча.
//
//     Картинка вложения рисуется как data:-адрес, и если CSP вебвью перестанет
//     их пускать, она просто не появится: ни ошибки на экране, ни красного в
//     сборке. Ровно так вложенные изображения и не показывались.
//
//     Кнопка «Показать раньше» просит у ядра весь хвост флагом full, а строит
//     адрес расширение. Пока флаг терялся, кнопка перезапрашивала те же
//     шестьдесят реплик и оставалась на месте — тоже молча.
{
  const contextUi = read('vscode-extension/ui/client/master-context-ui.js')
  const host = read('vscode-extension/extension.js')
  if (contextUi.includes('src="data:')) {
    const rules = [...host.matchAll(/img-src ([^;]+);/g)].map(match => match[1])
    if (!rules.length) fail('в расширении не найдено правило img-src — проверять нечего')
    const blind = rules.filter(rule => !rule.includes('data:'))
    if (blind.length) fail(`вебвью рисует картинку вложения через data:, а CSP её не пускает (${blind.length} правил img-src без data:) — картинка не появится и никто об этом не скажет`)
  }
  const webview = webviewSource
  const transport = read('vscode-extension/master-chat-controller.js')
  const asksFull = /postMessage\(\{\s*type:\s*'loadMaster'[^}]*full:\s*true/.test(webview)
  if (asksFull) {
    const history = transport.slice(transport.indexOf("case 'loadMaster':"), transport.indexOf("case 'masterPage':"))
    if (!history.includes('full')) fail('вебвью просит полный хвост истории флагом full, а транспорт его не передаёт — «Показать раньше» вернёт ту же страницу')
  }
}

// 30. Первый запуск Hub v2 глобален. Workspace-scoped флаг заставляет человека
//     повторять настройку Мастера в каждой новой папке и нарушает master-first
//     контракт ещё до первого сообщения.
{
  const host = read('vscode-extension/extension.js')
  const key = 'point.agentHubV2.onboardingComplete'
  if (!host.includes(`globalState?.get?.('${key}'`)) {
    fail('Hub v2 не читает глобальный флаг onboarding — новая папка снова покажет первый запуск')
  }
  const writes = [...host.matchAll(new RegExp(`globalState\\?\\.update\\?\\.\\('${key.replaceAll('.', '\\.')}'`, 'g'))].length
  if (writes !== 2) {
    fail(`глобальный флаг onboarding записывается ${writes} раз вместо двух (завершить/перезапустить)`)
  }
  if (host.includes("workspaceState?.update?.('point.agentOnboardingComplete'")) {
    fail('legacy workspace-флаг onboarding снова записывается и делает первый запуск проектным')
  }
  if (host.includes("onboardingComplete ? 'overview' : 'onboarding'")) {
    fail('после onboarding Hub снова открывает Обзор вместо основной точки входа — Мастера')
  }
  const masterEntrances = [...host.matchAll(/onboardingComplete \? 'master' : 'onboarding'/g)].length
  if (masterEntrances !== 5 || !host.includes("this.selectedTab = 'master'")) {
    fail(`master-first входов ${masterEntrances} вместо пяти или завершение onboarding не открывает Мастера`)
  }
}

// 31. Экран чата — дом, и рейки разделов на нём нет. Но срочное вместе с ней
//     пропасть не имеет права: агент, застрявший на подтверждении, должен быть
//     виден оттуда, где человек сидит, а не только из настроек проекта.
{
  const js = webviewSource
  const shellSource = js.match(/if \(ui\.state\.selectedTab === 'master'\) \{[\s\S]*?\n {4}\}/)
  if (!shellSource) fail('в shell() не нашлась ветка экрана чата — проверка смотрит не туда')
  else {
    const chat = shellSource[0]
    if (chat.includes('hall-rail')) fail('на экране чата снова рейка разделов — дом спорит с разговором за ширину')
    if (!chat.includes('projectChatDirectoryHtml(')) fail('экран чата остался без списка чатов всех проектов')
    if (!chat.includes('hallAlarmHtml(') || !chat.includes('hallChangesAlarmHtml(')) {
      fail('с экрана чата пропал значок срочного — очередь решений или непроверенные изменения будут молчать')
    }
    const settingsEntries = [...chat.matchAll(/data-tab="overview"/g)].length
    if (settingsEntries !== 1) fail(`входов в настройки проекта ${settingsEntries} вместо одного`)
  }
  // Мастер больше не раздел настроек: два входа в чат разошлись бы подсветкой.
  if (/\{ id: 'master', icon:/.test(js)) fail('Мастер вернулся в HALL_SECTIONS — в чат появился второй вход')
  if (!js.includes('data-tab="master"')) fail('из настроек проекта нечем вернуться в чат')
}

// 32. Чат чужого мира открывается только через реестр миров. Ядро отдаёт чужие
//     миры без путей именно ради этого: путь, пришедший из вебвью, никогда не
//     становится путём, который открывают.
{
  const js = webviewSource
  const host = read('vscode-extension/master-chat-controller.js')
  for (const hit of js.matchAll(/data-action="chat-open"[^>]*/g)) {
    if (!hit[0].includes('data-world=') || !hit[0].includes('data-chat=')) {
      fail('строка чата не несёт мир или разговор — клик уйдёт в пустоту')
    }
  }
  const handler = host.match(/case 'openProjectChat': \{[\s\S]*?\n {8}\}/)
  if (!handler) fail('в хосте нет обработчика openProjectChat — чужие чаты не откроются')
  else if (!handler[0].includes('this.knownProject(')) {
    fail('openProjectChat открывает путь из вебвью без сверки с реестром миров')
  }
}

// 33. Каталог чатов — единственное место ядра, смотрящее за пределы текущего
//     мира. Послабление ограничено чтением метаданных: как только сюда попадёт
//     запись или открытие мира, инвариант «ровно один текущий мир», на котором
//     стоят все вызовы guardWorld, перестанет быть правдой.
{
  const appSource = read('internal/app/master_directory.go')
  const storeSource = read('internal/storage/master_chat_directory.go')
  const routes = read('internal/httpapi/server.go')
  for (const [name, source] of [['app', appSource], ['storage', storeSource]]) {
    const body = source.split('\n').filter(line => !line.trim().startsWith('//')).join('\n')
    for (const forbidden of ['guardWorld(', 'OpenWorkspace(', 'SaveSetting(', 'INSERT ', 'UPDATE ', 'DELETE ']) {
      if (body.includes(forbidden)) fail(`каталог чатов (${name}) делает «${forbidden.trim()}» — он обязан только читать`)
    }
  }
  if (!routes.includes('"GET /api/master/directory"')) {
    fail('маршрут каталога чатов объявлен не только как GET')
  }
  if (!appSource.includes('WorkspacePath = ""')) {
    fail('каталог перестал скрывать пути чужих миров')
  }
}

if (failures.length) {
  console.error('СВЕРКА ДОГОВОРЁННОСТЕЙ ПРОВАЛЕНА:')
  for (const message of failures) console.error('  · ' + message)
  process.exit(1)
}
console.log('договорённости ядра и интерфейса сходятся')
