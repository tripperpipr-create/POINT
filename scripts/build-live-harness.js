// Живой стенд: main.js исполняется в настоящем браузере, а не в заглушке.
//
// Проверки из scripts/ гоняют main.js в node-контексте и кликают синтетически —
// мимо попадания курсора, мимо настоящего DOM, мимо ошибок, которые возникают
// только в браузере. Здесь скрипт грузится так же, как в webview, клики —
// настоящие, а всё упавшее попадает на экран.
//
//   node scripts/build-live-harness.js > build/preview/live.html

const fs = require('fs')
const path = require('path')

const repo = path.join(__dirname, '..')
const read = file => fs.readFileSync(path.join(repo, 'vscode-extension', file), 'utf8')

// Значения accept/reject — те же, что шлёт ядро (internal/app/decisions.go):
// это коды для API, а не подписи. Выдуманные подписи в фикстуре однажды уже
// заставили меня искать несуществующий дефект.
// Очередь с разными видами решений: применить набор, ответить на approval,
// решить судьбу предложения квеста.
const now = Date.now()
const decisions = {
  total: 3, blocking: 1, oldestMs: 45 * 60 * 1000,
  byKind: { changeSet: 1, approval: 1, quest: 1 },
  generatedAt: new Date(now).toISOString(),
  items: [
    { id: 'cs-1', kind: 'changeSet', label: 'НАБОР ИЗМЕНЕНИЙ', title: 'Правка обработчика вебхука',
      detail: '2 файла · internal/billing', risk: 'MEDIUM', who: 'FORGE', blocking: true,
      createdAt: new Date(now - 45 * 60 * 1000).toISOString(), waitingMs: 45 * 60 * 1000,
      resolve: { path: '/api/change-sets/cs-1/resolve', field: 'decision', accept: 'apply', reject: 'ignore' } },
    { id: 'ap-1', kind: 'approval', label: 'ПОДТВЕРЖДЕНИЕ', title: 'Запуск команды: go test ./...',
      detail: 'агент просит разрешения', risk: 'HIGH', who: 'FORGE', blocking: false,
      createdAt: new Date(now - 6 * 60 * 1000).toISOString(), waitingMs: 6 * 60 * 1000,
      resolve: { path: '/api/approvals/ap-1/resolve', field: 'decision', accept: 'approve', reject: 'deny' } },
    { id: 'qp-1', kind: 'quest', label: 'ПРЕДЛОЖЕНИЕ КВЕСТА', title: 'Починить оплату подписки',
      detail: 'мастер собрал отряд из 2 агентов', risk: 'LOW', who: 'МАСТЕР', blocking: false,
      createdAt: new Date(now - 90 * 1000).toISOString(), waitingMs: 90 * 1000,
      resolve: { path: '/api/quest-proposals/decide', field: 'decision', accept: 'start', reject: 'ignore' } },
  ],
}

const boot = {
  onboarded: true,
  // Два агента, а не один: цикл возникал именно на нескольких профилях —
  // каждый перезаписывал ключ соседа.
  profiles: [
    { id: 'sage', name: 'SAGE-7', roleDescription: 'Проектирует безопасные изменения.',
      provider: 'ollama', model: 'qwen2.5-coder', allowedTools: ['read_file'], maxSteps: 24 },
    { id: 'forge', name: 'FORGE', roleDescription: 'Правит код и проверяет сборкой.',
      provider: 'ollama', model: 'coder', allowedTools: ['read_file', 'propose_patch', 'run_command'], maxSteps: 40 },
  ],
  runs: [], usageRecords: [], quests: [], changeSets: [], executions: [],
  toolCatalog: [{ name: 'read_file', displayName: 'Чтение файлов', category: 'read', risk: 'LOW' }],
  blueprints: [], projectSkills: [], connections: [], providerCatalog: [],
  companion: { id: 'c1', preset: 'balanced', provider: 'ollama', model: 'qwen2.5-coder' },
  orchestrator: { id: 'o1', preset: 'conductor', provider: 'ollama', model: 'qwen2.5-coder' },
  indexStatus: { state: 'ready', files: 812 },
}

process.stdout.write(`<!doctype html>
<html lang="ru"><head><meta charset="UTF-8"><title>Point — живой стенд</title>
<style>${read('media/rpg-tokens.css')}\n${read('media/style.css')}</style>
<style>
html, body { height: 100%; margin: 0; }
#errors { position: fixed; inset: auto 0 0 0; z-index: 99999; max-height: 42vh; overflow: auto;
  padding: 8px 12px; background: #2a0d0d; color: #ffb4a8; border-top: 2px solid #ff5c47;
  font: 12px/1.5 ui-monospace, monospace; white-space: pre-wrap; }
#errors:empty { display: none; }
</style></head>
<body data-layout="wide">
<div id="root" class="app"></div>
<pre id="errors"></pre>
<script>
// Всё упавшее показываем, а не глотаем: молчаливая ошибка в webview выглядит
// как «ничего не переключается».
const errors = document.getElementById('errors')
const show = (kind, detail) => { errors.textContent += kind + ': ' + detail + '\\n' }
window.addEventListener('error', e => show('ОШИБКА', (e.message || '') + ' @ ' + (e.filename || '') + ':' + e.lineno))
window.addEventListener('unhandledrejection', e => show('ОТКАЗ', String(e.reason && e.reason.message || e.reason)))
const realError = console.error.bind(console)
console.error = (...args) => { show('console.error', args.map(String).join(' ')); realError(...args) }

// В неактивной панели предпросмотра кадры не идут, а render() откладывает
// отрисовку через requestAnimationFrame. Делаем кадры синхронными, иначе стенд
// показывает пустоту вместо интерфейса.
window.requestAnimationFrame = callback => { callback(); return 0 }

window.__posted = []
window.__tab = 'overview'
// Расширение отвечает на selectTab новым состоянием — без этого клик по разделу
// выглядит бесполезным, и стенд врёт про «не переключается».
window.acquireVsCodeApi = () => ({
  postMessage(message) {
    window.__posted.push(message)
    // Расширение отвечает на запрос годности — и этот ответ вызывает render().
    // Без ответа цикл «отрисовка → запрос → ответ → отрисовка» не воспроизвести,
    // и именно поэтому он дожил до пользователя.
    if (message && message.type === 'agentCapability') {
      window.__capabilityRequests = (window.__capabilityRequests || 0) + 1
      if (window.__capabilityRequests < 400) {
        setTimeout(() => window.dispatchEvent(new MessageEvent('message', { data: {
          type: 'agentCapability', key: message.key || '',
          capability: { canRead: true, canWrite: false, canRunCommands: false, canVerify: false,
            blockers: [], blocking: [], warnings: [], lines: ['Сможет: читать код.'] },
        } })), 0)
      }
    }
    // Политика Мастера — тот же класс запроса «во время отрисовки».
    if (message && message.type === 'orchestratorPolicy') {
      window.__policyRequests = (window.__policyRequests || 0) + 1
      if (window.__policyRequests < 400) {
        setTimeout(() => window.dispatchEvent(new MessageEvent('message', { data: {
          type: 'orchestratorPolicy', key: message.key || '',
          policy: { lines: ['Отряд: до 2 агентов.', 'Запуск остаётся за вами.'] },
        } })), 0)
      }
    }
    // Очередь решений — операционный центр Хаба. Без ответа она навсегда
    // остаётся в состоянии «загрузка…», и увидеть её работу нельзя.
    if (message && message.type === 'loadDecisions') {
      setTimeout(() => window.dispatchEvent(new MessageEvent('message', { data: {
        type: 'decisions', decisions: window.__decisions,
      } })), 0)
    }
    if (message && message.type === 'selectTab') {
      window.__tab = message.tab
      setTimeout(() => window.dispatchEvent(new MessageEvent('message', { data: {
        type: 'state', service: { state: 'running' }, workspaceTrusted: true,
        workspace: 'ai-ide', selectedTab: window.__tab, boot: window.__boot, details: undefined,
      } })), 0)
    }
  },
  getState() { return window.__persisted },
  setState(value) { window.__persisted = value },
})
window.__boot = ${JSON.stringify(boot)}
window.__decisions = ${JSON.stringify(decisions)}
</script>
<script>${read('media/main.js')}</script>
<script>
// Первое состояние приходит так же, как из расширения.
window.dispatchEvent(new MessageEvent('message', { data: {
  type: 'state', service: { state: 'running' }, workspaceTrusted: true,
  workspace: 'ai-ide', selectedTab: 'overview', boot: window.__boot, details: undefined,
} }))

// Самопроверка: заходим в Гильдию, где ростер перебирает несколько профилей,
// и смотрим, останавливается ли поток запросов годности. Раньше он не
// останавливался никогда — интерфейс был занят собой и не отвечал на нажатия.
window.__verdict = 'идёт…'
setTimeout(function () {
  var guild = Array.prototype.slice.call(document.querySelectorAll('.hall-nav button'))
    .filter(function (b) { return /ГИЛЬДИЯ/.test(b.textContent) })[0]
  if (!guild) { window.__verdict = 'рейка не отрисовалась'; return }
  window.__capabilityRequests = 0
  guild.click()
  setTimeout(function () {
    var first = window.__capabilityRequests
    setTimeout(function () {
      var second = window.__capabilityRequests
      // Порог, а не только рост: у стенда есть предохранитель на 400 ответов,
      // и после него счётчик замирает — «роста нет» перестаёт что-либо значить.
      var runaway = second > first || second > 8
      window.__verdict = runaway
        ? 'ЦИКЛ: запросов годности ' + second + ' (ожидалось по одному на агента)'
        : 'тихо: запросов ' + second + ', роста нет'
      document.title = 'Point — ' + window.__verdict
    }, 900)
  }, 500)
}, 100)
</script>
</body></html>
`)
