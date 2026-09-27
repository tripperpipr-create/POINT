// Блок доставленного приложения и строка итогового отчёта в карточке прогона.
//
// Запуск, остановка и отчёт стояли одним рядом кнопок под вердиктом и читались
// тремя равными делами, а после нажатия карточка молчала, пока ядро не вернёт
// ответ. Здесь у приложения своя рамка: вид, состояние, адрес, запуск и
// остановка половинами одного переключателя и живой вывод `docker compose`;
// у отчёта — строка с фазами сборки. Состояние приходит из quest-app-state.js,
// нажатия разбирает quest-app-actions.js.

import { countOf, formatElapsed, list } from './format-units.js'
import { masterCardMoreAttrs } from './master-card-open.js'
import { icon } from './ui-icons.js'
import { questAppOf, questReportOf } from './quest-app-state.js'

const APP_KIND = { web: 'Веб-приложение', service: 'Сервис', cli: 'Консольное приложение', desktop: 'Настольное приложение', data: 'Данные' }

// Вид приложения до ответа ядра: по квитанции и стеку договора. Ядро скажет
// точно (compose-файл оно находит и без квитанции) через секунду после
// появления карточки.
function appFallback(order) {
  const receipt = order.runtime?.deliveryReceipt || {}
  const category = String(order.stack?.category || '')
  const kind = receipt.url ? 'web' : category === 'cli' ? 'cli' : category === 'desktop-mobile' ? 'desktop' : category === 'data' ? 'data' : 'service'
  return { kind, launch: 'compose', url: receipt.url || '' }
}

// Состояние одним словом и тоном: что с приложением прямо сейчас.
function appStatus(app, state) {
  const action = app?.pending || (state?.inFlight ? state.action : '')
  if (action === 'start') return ['is-busy', 'Запускается']
  if (action === 'stop') return ['is-busy', 'Останавливается']
  return ({
    running: ['is-on', 'Работает'], stopped: ['is-off', 'Остановлено'], idle: ['is-off', 'Не запущено'],
    failed: ['is-failed', 'Не запустилось'], unknown_outcome: ['is-failed', 'Итог неизвестен'],
  })[state?.status] || (app?.error ? ['is-failed', 'Нет связи с ядром'] : ['is-unknown', 'Проверяем…'])
}

// Строка вывода: команда, ответ, сбой и примечания ядра — своим тоном.
function appLineTone(line) {
  if (line.startsWith('$ ')) return 'is-cmd'
  if (/^Отвечает:/.test(line)) return 'is-ok'
  if (/^(Команда завершилась ошибкой|Не ответил)/.test(line)) return 'is-fail'
  if (/^(Ждём ответа|Работает контейнеров)/.test(line)) return 'is-note'
  return ''
}

// Блок приложения: что это, в каком оно состоянии, как его запустить и
// остановить и что происходит при запуске. Запуск и остановка — две половины
// одного переключателя: они про одно и то же, и порознь читались как два
// разных дела рядом с отчётом.
export function questApplicationHtml(order, controls, esc) {
  const runtime = order.runtime || {}
  const receipt = runtime.deliveryReceipt
  if (runtime.status !== 'completed' || !receipt?.id) return ''
  const app = questAppOf(runtime.questId)
  const state = app?.state || null
  const view = state || appFallback(order)
  const [tone, word] = appStatus(app, state)
  const busy = tone === 'is-busy'
  const attrs = `data-action="control-master-application-v2" data-id="${esc(order.id)}" data-quest-id="${esc(runtime.questId)}" data-version="${Number(order.version) || 1}" data-digest="${esc(order.digest || '')}" data-receipt-id="${esc(receipt.id)}"`
  // Время действия — от нажатия или от начала, записанного ядром; нулевая
  // дата ядра («не начиналось») временем не считается.
  const started = Date.parse(state?.inFlight ? state.startedAt || '' : '')
  const since = app?.pending && app.since ? app.since : started > Date.UTC(2020, 0) ? started : NaN
  const facts = [
    busy && !Number.isNaN(since) ? formatElapsed(Date.now() - since) : '',
    !busy && state?.status === 'running' && state.probed ? countOf(state.services, 'контейнер', 'контейнера', 'контейнеров') : '',
    !busy && state?.status === 'running' && state.ready ? `отвечает ${Number(state.httpStatus) || ''}`.trim() : '',
    !busy && app?.opened === 'browser' ? 'открыто в браузере' : !busy && app?.opened === 'terminal' ? 'запущено в терминале' : '',
  ].filter(Boolean)
  const running = state?.status === 'running'
  const url = String(view.url || '')
  const openLink = view.launch === 'compose' && url
    ? `<button type="button" class="quest-app-url" ${attrs} data-control="open" title="Открыть ${esc(url)} в браузере"${running ? '' : ' disabled'}>${esc(url)}${icon('chevron-right')}</button>` : ''
  let switcher
  if (view.launch === 'terminal') {
    switcher = `<div class="quest-app-seg"><button type="button" class="quest-app-btn is-primary" ${attrs} data-control="terminal">${icon('terminal')}<span>Запустить в терминале</span></button></div><code class="quest-app-cmd" title="${esc(view.command || '')}">${esc(view.command || '')}</code>`
  } else if (view.launch === 'folder') {
    switcher = `<div class="quest-app-seg"><button type="button" class="quest-app-btn" ${attrs} data-control="open">${icon('folder')}<span>Открыть папку</span></button></div>`
  } else {
    const startable = !busy && !running && !controls.busy
    const stoppable = !busy && !controls.busy && state?.status !== 'stopped' && state?.status !== 'idle'
    switcher = `<div class="quest-app-seg" role="group" aria-label="Управление приложением">
      <button type="button" class="quest-app-btn${startable ? ' is-primary' : ''}" ${attrs} data-control="start"${startable ? '' : ' disabled'}>${app?.pending === 'start' || (state?.inFlight && state.action === 'start') ? '<i class="quest-spin" aria-hidden="true"></i>' : icon('play')}<span>${word === 'Запускается' ? 'Запускается…' : 'Запустить'}</span></button>
      <button type="button" class="quest-app-btn" ${attrs} data-control="stop"${stoppable ? '' : ' disabled'}>${word === 'Останавливается' ? '<i class="quest-spin" aria-hidden="true"></i>' : icon('stop')}<span>${word === 'Останавливается' ? 'Останавливается…' : 'Остановить'}</span></button>
    </div>`
  }
  const lines = list(state?.lines)
  const log = lines.length
    ? `<details class="quest-app-log"${masterCardMoreAttrs(`app-log:${runtime.questId}`, { esc, open: busy || tone === 'is-failed' })}><summary>${busy ? 'Вывод запуска · идёт' : 'Вывод последнего действия'} · ${countOf(lines.length, 'строка', 'строки', 'строк')}</summary><div class="quest-app-lines">${lines.slice(-40).map(line => `<span class="${appLineTone(line)}">${esc(line)}</span>`).join('')}</div></details>`
    : ''
  const error = app?.error ? `<p class="quest-app-error">${esc(app.error)}</p>` : ''
  return `<section class="quest-app ${tone}" aria-label="Приложение">
    <header>
      <i class="quest-app-dot" aria-hidden="true"></i>
      <b>${esc(view.kind === 'web' && /json/i.test(String(state?.contentType || app?.contentType || '')) ? 'API-сервис' : APP_KIND[view.kind] || 'Приложение')}</b>
      <span class="quest-app-state">${esc(word)}${facts.length ? ` · ${esc(facts.join(' · '))}` : ''}</span>
      ${openLink}
    </header>
    <div class="quest-app-row">${switcher}</div>
    ${error}${log}
  </section>`
}

// Итоговый отчёт: одно нажатие — и HTML открывается в браузере. Строка
// показывает, что идёт сборка, где лёг файл и как открыть его снова.
export function questReportHtml(order, esc) {
  if (order.runtime?.status !== 'completed') return ''
  const report = questReportOf(order.id)
  const again = `data-action="generate-work-order-report" data-id="${esc(order.id)}"`
  if (report?.phase === 'working') {
    return `<div class="quest-report-line is-busy"><button type="button" class="hall-btn" disabled><i class="quest-spin" aria-hidden="true"></i>Собираем отчёт…</button><span>Архивариус пишет HTML — он откроется в браузере сам</span></div>`
  }
  if (report?.phase === 'ready') {
    return `<div class="quest-report-line is-done">${icon('check')}<span>Отчёт открыт в браузере</span><code title="${esc(report.path)}">${esc(report.path)}</code><button type="button" class="hall-btn is-sm" data-action="open-work-order-report" data-uri="${esc(report.uri)}">Открыть снова</button><button type="button" class="hall-btn is-sm" ${again}>Собрать заново</button></div>`
  }
  if (report?.phase === 'failed') {
    return `<div class="quest-report-line is-failed">${icon('warning')}<span>Отчёт не собран: ${esc(report.error)}</span><button type="button" class="hall-btn is-sm" ${again}>Повторить</button></div>`
  }
  return `<div class="quest-report-line"><button type="button" class="hall-btn" ${again} title="Архивариус соберёт итоговый HTML-отчёт по квесту и сразу откроет его в браузере">${icon('file')}Собрать отчёт</button></div>`
}
