// Карточка прогона квеста: читается ли она человеком.
//
// Прежний прогон рисовал каждый вызов инструмента рамкой «↳ Карта проекта ·
// шаг 1» без того, над чем работал агент и чем кончилось; предупреждения шли
// английским текстом ядра («skill is not equipped for this agent», «сначала
// search_code»); diff был сырым <pre>; этапы звались «Verify result» и
// «Implementation review»; готовый квест показывал «EvidenceBundle»,
// «acceptance · exit 0» и «$0.00», а над карточкой стоял отчёт ядра стеной
// текста, повторявшей её же.
//
// Проверка рендерит страницы стенда (scripts/render-hub-surface.js) — то есть
// собранный media/main.js с настоящими зависимостями, а не модуль с заглушками,
// — и смотрит и в разметку, и в видимый текст. Образцы стенда взяты из договора
// ядра: события прогона, коды защиты и отказов, виды проверок завершения.
import { execFileSync } from 'node:child_process'
import path from 'node:path'

const root = path.resolve(import.meta.dirname, '..')
const render = variant => execFileSync(process.execPath, [path.join(root, 'scripts', 'render-hub-surface.js'), 'master', variant], {
  cwd: root, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024,
})
// Видимый текст: без разметки, стилей и атрибутов (подсказки по наведению
// остаются в атрибутах и сюда не попадают).
const visible = html => html
  .replace(/<style[\s\S]*?<\/style>/g, ' ').replace(/<script[\s\S]*?<\/script>/g, ' ')
  .replace(/<[^>]+>/g, ' ').replace(/&quot;/g, '"').replace(/&#0?39;/g, "'").replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&')
  .replace(/\s+/g, ' ')
const cardOf = html => {
  const start = html.indexOf('<section class="master-v2-run')
  if (start < 0) throw new Error('run card is not rendered at all')
  return html.slice(start)
}
const failures = []
const expect = (condition, message) => { if (!condition) failures.push(message) }

// 1. Идущий квест.
const runningHtml = cardOf(render('work-order-running'))
const running = visible(runningHtml)
expect(runningHtml.includes('class="quest-journal"'), 'running quest lost its stage journal')
expect(runningHtml.includes('hall-trail quest-trail'), 'journal actions are not grouped into a trail summary')
expect(running.includes('3 действия') && running.includes('2 предупреждения'), 'trail summary does not count actions and warnings')
expect(running.includes('composer create-project symfony/skeleton .') && running.includes('код 0 · 41 с'), 'step row lost its argument, exit code or duration')
expect(running.includes('навык не выдан этому агенту'), 'skill_not_equipped is not named in Russian')
expect(!running.includes('skill is not equipped'), 'core English failure text leaked into the journal')
expect(running.includes('сначала «') && !/\bsearch_code\b/.test(running), 'guardrail still names the raw tool id')
expect(runningHtml.includes('diff is-whole is-new') && runningHtml.includes('Новый файл') && runningHtml.includes('class="diff-n">11<'), 'new file lost its badge or its numbered code')
expect(running.includes('создан в песочнице'), 'patch row does not say what happened to the file')
expect((runningHtml.match(/<span class="quest-track"[\s\S]*?<\/span>/)?.[0].match(/<i class="is-/g) || []).length === 6, 'stage track does not draw one segment per stage')
expect(running.includes('Этап 2 из 6') && running.includes('Разработчик проекта · Qwen3.8-27B'), 'now line lost stage position, agent or model')
for (const russian of ['Проверка результата', 'Ревью реализации', 'Приёмка', 'Вход', 'Выход']) expect(running.includes(russian), `template stage is not named in Russian: ${russian}`)
for (const english of ['Verify result', 'Implementation review', 'Input', 'Output']) expect(!new RegExp(`\\b${english}\\b`).test(running), `template stage name leaked: ${english}`)
expect(running.includes('Работа агента') && running.includes('Scaffold Symfony app with health endpoint'), 'model-authored stage lost its Russian role or its own name')
expect(/class="quest-now"[\s\S]{0,400}vendor\/bin\/phpunit/.test(runningHtml), 'now row does not show the running call')
const trail = runningHtml.slice(runningHtml.indexOf('class="quest-journal"'), runningHtml.indexOf('class="quest-now"'))
expect(trail && !trail.includes('vendor/bin/phpunit'), 'running call is drawn twice — in the trail and in the now row')
expect(runningHtml.includes('data-control="pause"') && runningHtml.includes('data-control="cancel"') && runningHtml.includes('data-work-order-message'), 'running quest lost its controls')
expect(running.includes('The workspace is empty'), 'agent prose must stay in the model language, not be dropped')

// 2. Взятый квест.
const approvedPage = render('work-order-approved')
const approvedHtml = cardOf(approvedPage)
const approved = visible(approvedHtml)
for (const chip of ['2/3 условия', '3 файла', '284 тыс. токенов', 'бесплатно']) expect(approved.includes(chip), `outcome chip is missing: ${chip}`)
for (const raw of ['EvidenceBundle', '$0.00', 'exit 0', 'acceptance', 'automated_tests', 'service_start']) expect(!approved.includes(raw), `raw core word is visible: ${raw}`)
expect(approved.includes('curl -fsS http://localhost:8080/health · код 0 · 240 мс'), 'criterion lost the command, code and time that proved it')
expect(approved.includes('Автотесты') && approved.includes('Запуск сервисов') && approved.includes('Проверка здоровья'), 'completion checks lost their human names')
expect(approved.includes('Перенесено в') && approved.includes('Подтверждено 2 из 3 условий'), 'verdict does not say where the work went and what is proven')
expect(approvedHtml.includes('data-control="start"') && approved.includes('Веб-приложение'), 'delivered application cannot be started from the card')
expect(approved.includes('Пакет доказательств') && approved.includes('Расход') && approved.includes('Технические детали'), 'folded outcome sections are missing')
expect(!approved.includes('Вне scope') && !approved.includes('Workspace'), 'composition still speaks English')
const report = visible(approvedPage.slice(approvedPage.indexOf('class="quest-report"'), approvedPage.indexOf('<section class="master-v2-run')))
expect(report.startsWith(' Готово') || report.includes(' Готово Полный отчёт ядра'), 'core report is not folded to its verdict above the card')
expect(/<details class="quest-fold" data-master-open="card" data-id="quest-report:wo-3">/.test(approvedPage), 'full core report is not behind a closed disclosure')

// 2b. Приложение: запуск и остановка — половины одного переключателя, а не
// кнопки в ряду с отчётом; видно, что с приложением сейчас и что печатал запуск.
const segment = approvedHtml.match(/<div class="quest-app-seg"[\s\S]*?<\/div>/)?.[0] || ''
expect(segment.includes('data-control="start"') && segment.includes('data-control="stop"'), 'start and stop are not one switch')
expect((approvedHtml.match(/class="quest-app /g) || []).length === 1, 'application block must be single')
expect(approved.includes('Работает · 2 контейнера · отвечает 200 · открыто в браузере'), 'running application does not say how it is and where it opened')
expect(/data-control="start"[^>]*disabled/.test(segment), 'a running application still offers start')
expect(approvedHtml.includes('data-control="open"') && approved.includes('http://localhost:8080'), 'running web application has no way to open it')
expect(approved.includes('Отвечает: 200 · text/html'), 'launch output is not kept on the card')
expect(!approved.includes('Запустить приложение'), 'the old detached start button is back')
expect(approved.includes('Отчёт открыт в браузере') && approvedHtml.includes('data-action="open-work-order-report"'), 'ready report does not say where it is or how to reopen it')
const startingHtml = cardOf(render('work-order-app-starting'))
const starting = visible(startingHtml)
expect(starting.includes('Запускается…') && startingHtml.includes('class="quest-spin"'), 'starting application shows no progress')
expect(starting.includes('Вывод запуска · идёт') && starting.includes('Container systemio-db-1 Started'), 'live launch output is not visible while starting')
expect(/data-control="stop"[^>]*disabled/.test(startingHtml), 'stop is offered in the middle of a start')
expect(starting.includes('Собираем отчёт…') && !/data-action="generate-work-order-report"[^>]*>[^<]*Собрать отчёт/.test(startingHtml), 'report in progress can be requested again')

// 3. Остановленный шлюзом.
const blockedHtml = cardOf(render('work-order-blocked'))
const blocked = visible(blockedHtml)
const verdict = visible(blockedHtml.match(/<div class="quest-verdict[\s\S]*?<\/div>/)?.[0] || '')
expect(verdict.includes('не доказано: «GET /health возвращает HTTP 200 с JSON-телом»') && verdict.includes('доставка не подтверждена'), 'gate reason is not named in human words')
expect(!verdict.includes('work is not proven') && !verdict.includes('criterion:'), 'gate reason leaked as a machine string')
expect(/<li class="is-failed"[^>]*>[\s\S]*?GET \/health/.test(blockedHtml), 'unproven criterion is not marked as failed')
// Недоставленная работа — не «нет изменений» и не доставка (Q03, E3).
expect(blocked.includes('Подготовлено, не доставлено · 2 файла') && blocked.includes('src/Controller/HealthController.php'), 'undelivered prepared files are not shown')
expect(!blocked.includes('Изменения ·'), 'prepared files are presented as delivered changes')
// Q13: у подготовленного файла — его diff, рядом причина недоставки, выход — новая версия наряда.
expect(/<details class="quest-prepared-file">[\s\S]*?HealthController\.php[\s\S]*?final class HealthController/.test(blockedHtml), 'prepared file does not open its diff')
const preparedReason = visible(blockedHtml.match(/<p class="quest-prepared-reason">[\s\S]*?<\/p>/)?.[0] || '')
expect(preparedReason.includes('Не перенесено: Работа заблокирована') && !preparedReason.includes('work is not proven'), 'reason for non-delivery is missing or machine-worded')
expect(/data-action="master-ask"[^>]*data-question="Подготовь новую версию наряда[^"]*"[^>]*>Новая версия наряда/.test(blockedHtml), 'no way to a new work order version')
expect(/data-action="open-prepared-diff"[^>]*data-change-set="[^"]+"[^>]*data-item="[^"]+"[^>]*>Открыть в редакторе/.test(blockedHtml), 'prepared file cannot be opened in the IDE diff editor')
expect(!/<li class="is-failed"[^>]*>[\s\S]{0,300}composer\.json содержит/.test(blockedHtml), 'a passed check is painted as failed on a blocked quest')
expect(!blocked.includes('Загружаем журнал'), 'a quest stopped by a verdict promises a journal that will never load')
expect(!blockedHtml.includes('data-control="resume"'), 'a quest with a final verdict offers a retry the core refuses')

// 4. Общее: CSP вебвью выбрасывает style="", и полоса без ширины показала бы победу.
for (const [name, html] of [['running', runningHtml], ['approved', approvedHtml], ['blocked', blockedHtml]]) {
  expect(!/\sstyle="/.test(html), `${name} card carries an inline style that CSP drops`)
}

if (failures.length) {
  console.error(failures.map(item => `FAIL ${item}`).join('\n'))
  process.exit(1)
}
console.log('quest run card: journal, stages, verdict, proofs and folded core report: PASS')
