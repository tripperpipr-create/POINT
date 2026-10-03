// Окно GitLab и карточка merge request.
//
// Что проверяется:
// - окно спрашивает состояние, а после ответа — список своего раздела, и
//   каждый запрос помечен поверхностью `tool`: ответ придёт этому окну;
// - сбой — состояние с причиной и кнопкой следующего шага, а не пустой список;
// - проект, не связанный с GitLab, — спокойная строка и «Связать…», а не
//   сбой; списки при этом не спрашиваются, «Не связывать» уходит хосту;
// - смена мира (другой workspacePath) и перезапуск ядра перечитывают
//   состояние окна; найденный по origin проект связывается одним кликом;
// - MR открывается карточкой с тем проектом и номером, что нажат;
// - карточка спрашивает MR, обсуждение и изменения от своего имени
//   (`mr:<проект>!<номер>`), описание GitLab проходит разборщик и `<script>`
//   не становится разметкой;
// - merge уходит с головой MR, которую владелец видел, и с решением про
//   ветку; одобрение — с той же головой;
// - пустой комментарий не уходит, непустой — уходит в нужную нить;
// - diff файла открывается с базой и головой из diff_refs.
//
//   node scripts/smoke-gitlab-tool-window.js   (после npm run build)

const { bootWebview } = require('./lib/webview-harness')

const failures = []
const check = (name, ok, detail = '') => { if (!ok) failures.push(`${name}${detail ? `: ${detail}` : ''}`) }
const ok = data => ({ state: 'ok', data })
const head = 'a1b2c3d4e5f60718293a4b5c6d7e8f9012345678'
const anna = { id: 7, username: 'anna', name: 'Анна' }

// ── Окно ───────────────────────────────────────────────────────────────────
const tool = bootWebview({ layout: 'tool-gitlab' })
tool.state()
let sent = tool.take()
check('окно спрашивает состояние GitLab', sent.some(m => m.type === 'gitlabAction' && m.action === 'status' && m.surface === 'tool'), JSON.stringify(sent))
check('до ответа список не спрашивается', !sent.some(m => m.action === 'mergeRequests'))

tool.send({ type: 'gitlabStatus', response: { state: 'error', reason: 'not_trusted', problem: 'запуск сервера GitLab не одобрен', fix: 'нажмите «Доверяю»', data: { configured: true } } })
let html = tool.root.innerHTML
check('сбой назван словами', html.includes('запуск сервера GitLab не одобрен') && html.includes('нажмите «Доверяю»'))
check('у сбоя доверия есть кнопка в Интеграции', html.includes('data-action="gitlab-open-integrations"'))
tool.click({ action: 'gitlab-open-integrations' })
check('кнопка открывает Интеграции разрешённой командой', tool.take().some(m => m.type === 'toolCommand' && m.command === 'localAgent.openIntegrations'))

tool.send({ type: 'gitlabChanged' })
check('смена подключения перечитывает состояние', tool.take().some(m => m.action === 'status'))
tool.send({ type: 'gitlabStatus', response: ok({ configured: true, url: 'https://gitlab.example.test', user: anna, linked: true,
  binding: { mode: 'auto', project: 'billing/payments', branch: 'fix/webhook-retry', workspace: 'payments' } }) })
sent = tool.take()
check('после состояния окно спрашивает «Мои»', sent.some(m => m.action === 'mergeRequests' && m.scope === 'mine' && m.surface === 'tool'), JSON.stringify(sent))
tool.send({ type: 'gitlabMergeRequests', scope: 'mine', response: ok({ scope: 'mine', project: 'billing/payments', items: [
  { projectPath: 'billing/payments', iid: 12, title: 'Идемпотентность <b>вебхука</b>', sourceBranch: 'fix/webhook-retry', targetBranch: 'main', author: anna, mergeStatus: 'mergeable' },
] }) })
html = tool.root.innerHTML
check('MR в списке', html.includes('!12') && html.includes('fix/webhook-retry'))
check('название MR экранировано', html.includes('&lt;b&gt;вебхука') && !html.includes('<b>вебхука'))
check('проект окна виден', html.includes('billing/payments'))

tool.click({ action: 'gitlab-open-mr', project: 'billing/payments', iid: '12', title: 'Идемпотентность' })
sent = tool.take()
check('MR открывается карточкой', sent.some(m => m.action === 'openMr' && m.project === 'billing/payments' && m.iid === 12), JSON.stringify(sent))

tool.click({ action: 'gitlab-scope', scope: 'review' })
check('раздел «На ревью» спрашивается один раз', tool.take().filter(m => m.action === 'mergeRequests' && m.scope === 'review').length === 1)
tool.click({ action: 'gitlab-section', section: 'pipelines' })
check('раздел пайплайнов спрашивает пайплайны', tool.take().some(m => m.action === 'pipelines' && m.surface === 'tool'))
tool.send({ type: 'gitlabPipelines', response: ok({ project: 'billing/payments', ref: 'fix/webhook-retry', items: [{ id: 3301, status: 'failed', ref: 'fix/webhook-retry', sha: head }] }) })
tool.click({ action: 'gitlab-toggle-pipeline', project: 'billing/payments', pipeline: '3301' })
check('раскрытый пайплайн спрашивает джобы', tool.take().some(m => m.action === 'jobs' && m.pipeline === 3301 && m.project === 'billing/payments'))
tool.send({ type: 'gitlabJobs', pipeline: 3301, response: ok({ jobs: [{ id: 77002, name: 'go-test', stage: 'test', status: 'failed' }] }) })
html = tool.root.innerHTML
check('джоб с логом и перезапуском', html.includes('data-action="gitlab-job-log"') && html.includes('data-action="gitlab-retry-job"'))
tool.click({ action: 'gitlab-retry-job', project: 'billing/payments', job: '77002', pipeline: '3301' })
check('перезапуск джоба уходит хосту', tool.take().some(m => m.action === 'retry' && m.job === 77002 && m.pipeline === 3301))
tool.send({ type: 'gitlabActionResult', action: 'retry', project: 'billing/payments', pipeline: 3301, response: ok({ job: { id: 77010 } }) })
check('после перезапуска джобы перечитываются', tool.take().some(m => m.action === 'jobs' && m.pipeline === 3301 && m.project === 'billing/payments'))

// ── Проект вне GitLab ──────────────────────────────────────────────────────
const quiet = bootWebview({ layout: 'tool-gitlab' })
quiet.state({ workspacePath: 'C:/work/dotfiles' })
quiet.take()
quiet.send({ type: 'gitlabStatus', response: ok({ configured: true, url: 'https://gitlab.example.test', linked: false,
  binding: { mode: 'off', workspace: 'dotfiles', remote: 'github.com/anna/dotfiles', note: 'origin ведёт на github.com, а плагин подключён к gitlab.example.test' } }) })
html = quiet.root.innerHTML
sent = quiet.take()
check('несвязанный проект назван спокойно', html.includes('Проект «dotfiles» не связан с GitLab') && html.includes('origin ведёт на github.com'), html.slice(0, 400))
check('несвязанный проект — не сбой', !html.includes('gl-problem'))
check('несвязанному проекту списки не спрашиваются', !sent.some(m => m.action === 'mergeRequests' || m.action === 'pipelines'), JSON.stringify(sent))
check('есть «Связать…»', html.includes('data-action="gitlab-binding-toggle"') && html.includes('Связать…'))
quiet.click({ action: 'gitlab-binding-toggle' })
html = quiet.root.innerHTML
check('редактор связи предлагает «Не связывать» и отмечает его', /value="off" data-action="gitlab-binding-mode" checked/.test(html), html.slice(0, 600))
check('подсказка git remote называет чужой узел', html.includes('origin ведёт на github.com — не на этот GitLab'))
quiet.click({ action: 'gitlab-binding-mode' }, { value: 'manual' })
quiet.type('bindingProject', 'dotfiles/mirror')
quiet.click({ action: 'gitlab-binding-save' })
sent = quiet.take()
check('ручной проект уходит хосту', sent.some(m => m.action === 'binding' && m.mode === 'manual' && m.project === 'dotfiles/mirror' && m.surface === 'tool'), JSON.stringify(sent))
quiet.send({ type: 'gitlabBinding', response: ok({ mode: 'manual', project: 'dotfiles/mirror' }) })
check('сохранённая связь перечитывает состояние', quiet.take().some(m => m.action === 'status'))
quiet.send({ type: 'gitlabBinding', response: ok({ mode: 'off' }) })
check('чужое сохранение из Хаба не даёт второго запроса', !quiet.take().some(m => m.action === 'status'))
quiet.send({ type: 'gitlabStatus', response: ok({ configured: true, linked: false, binding: { mode: 'off', workspace: 'dotfiles' } }) })
quiet.state({ workspacePath: 'C:/work/dotfiles' })
check('тот же мир не перечитывает состояние', !quiet.take().some(m => m.action === 'status'))
quiet.state({ workspacePath: 'C:/work/payments' })
check('смена мира перечитывает состояние окна', quiet.take().some(m => m.action === 'status' && m.surface === 'tool'))

// Связь выключили вручную, а origin ведёт на этот GitLab: вернуть её — один клик.
quiet.send({ type: 'gitlabStatus', response: ok({ configured: true, linked: false,
  binding: { mode: 'off', workspace: 'payments', detected: 'billing/payments', note: 'связь с GitLab отключена в настройках проекта' } }) })
html = quiet.root.innerHTML
check('найденный проект связывается одной кнопкой', html.includes('data-action="gitlab-link-detected"') && html.includes('Связать с billing/payments'), html.slice(0, 400))
check('рядом остаётся выбор другого проекта', html.includes('Другой проект…'))
quiet.take()
quiet.click({ action: 'gitlab-link-detected' })
check('один клик сохраняет связь «по git remote»', quiet.take().some(m => m.action === 'binding' && m.mode === 'auto' && m.surface === 'tool'))
quiet.send({ type: 'gitlabBinding', response: ok({ mode: 'auto', project: 'billing/payments' }) })
quiet.take()

// Перезапуск ядра: статус прошлого процесса больше не правда — окно спрашивает заново.
quiet.send({ type: 'gitlabStatus', response: ok({ configured: true, linked: false, binding: { mode: 'off', workspace: 'payments' } }) })
quiet.take()
quiet.state({ workspacePath: 'C:/work/payments', service: { state: 'stopped' } })
check('остановленное ядро ничего не спрашивает', !quiet.take().some(m => m.action === 'status'))
quiet.state({ workspacePath: 'C:/work/payments' })
check('поднятое ядро перечитывает состояние окна', quiet.take().some(m => m.action === 'status' && m.surface === 'tool'))

// ── Широкое окно: MR открывается справа от списка ──────────────────────────
const wide = bootWebview({ layout: 'tool-gitlab', wide: true })
wide.state()
wide.send({ type: 'gitlabStatus', response: ok({ configured: true, url: 'https://gitlab.example.test', user: anna, linked: true,
  binding: { mode: 'auto', project: 'billing/payments', branch: 'fix/webhook-retry', workspace: 'payments' } }) })
wide.send({ type: 'gitlabMergeRequests', scope: 'mine', response: ok({ scope: 'mine', items: [
  { projectPath: 'billing/payments', iid: 12, title: 'Идемпотентность', sourceBranch: 'fix/webhook-retry', author: anna, mergeStatus: 'mergeable' }] }) })
check('широкое окно без выбора зовёт выбрать MR', wide.root.innerHTML.includes('Выберите merge request'))
wide.take()
wide.click({ action: 'gitlab-open-mr', project: 'billing/payments', iid: '12', title: 'Идемпотентность' })
sent = wide.take()
check('в широком окне MR не уходит во вкладку', !sent.some(m => m.action === 'openMr'), JSON.stringify(sent))
for (const action of ['mr', 'discussions', 'changes']) {
  check(`деталь окна спрашивает ${action}`, sent.some(m => m.action === action && m.surface === 'tool' && m.project === 'billing/payments' && m.iid === 12), JSON.stringify(sent))
}
const detailMr = { iid: 12, title: 'Идемпотентность', state: 'opened', sourceBranch: 'fix/webhook-retry', targetBranch: 'main', author: anna, mergeStatus: 'mergeable', diffRefs: { headSha: head }, sha: head }
wide.send({ type: 'gitlabMr', project: 'billing/payments', iid: 99, response: ok({ mergeRequest: { ...detailMr, iid: 99, title: 'Чужой MR из второго окна' }, approvals: { rules: [] }, pipelines: [] }) })
check('ответ о чужом MR отброшен', !wide.root.innerHTML.includes('Чужой MR'))
wide.send({ type: 'gitlabMr', project: 'billing/payments', iid: 12, response: ok({ mergeRequest: detailMr, approvals: { rules: [] }, pipelines: [] }) })
html = wide.root.innerHTML
check('деталь MR справа: строка состояния и merge', html.includes('gl-line') && html.includes('Готов к слиянию') && html.includes('data-action="gitlab-merge"'), html.slice(0, 400))
check('строка списка выделена', html.includes('gl-row is-selected'))
wide.click({ action: 'gitlab-open-mr', project: 'billing/payments', iid: '12', tabOpen: '1' })
check('значок шапки детали открывает вкладку', wide.take().some(m => m.action === 'openMr' && m.iid === 12))

// ── Общее окно из общих настроек: все проекты, без привязки к папке ───────
const general = bootWebview({ layout: 'tool-gitlab', dataset: { gitlabScope: 'all' } })
general.state()
sent = general.take()
check('общее окно спрашивает от своей поверхности', sent.some(m => m.action === 'status' && m.surface === 'tool-all'), JSON.stringify(sent))
general.send({ type: 'gitlabStatus', response: ok({ configured: true, url: 'https://gitlab.example.test', user: anna, linked: true, binding: { mode: 'all' } }) })
html = general.root.innerHTML
check('общее окно — «Все проекты» без выбора связи папки', html.includes('Все проекты') && !html.includes('data-action="gitlab-binding-toggle"'), html.slice(0, 600))
general.take()
general.click({ action: 'gitlab-section', section: 'mrs' })
check('MR общего окна — от поверхности tool-all', general.take().some(m => m.action === 'mergeRequests' && m.surface === 'tool-all'))
const hub = bootWebview({ layout: 'wide' })
hub.click({ action: 'gitlab-open-window', scope: 'all' })
check('кнопка общих настроек открывает общее окно', hub.take().some(m => m.action === 'openWindow' && m.scope === 'all'))

// ── Карточка MR ────────────────────────────────────────────────────────────
const card = bootWebview({ layout: 'gitlab-mr', dataset: { gitlabProject: 'billing/payments', gitlabIid: '12' } })
card.state()
sent = card.take()
const surface = 'mr:billing/payments!12'
for (const action of ['mr', 'discussions', 'changes']) {
  check(`карточка спрашивает ${action}`, sent.some(m => m.action === action && m.surface === surface && m.project === 'billing/payments' && m.iid === 12), JSON.stringify(sent))
}
card.send({ type: 'gitlabMr', response: ok({
  mergeRequest: { iid: 12, title: 'Идемпотентность', state: 'opened', sourceBranch: 'fix/webhook-retry', targetBranch: 'main', author: anna, mergeStatus: 'mergeable',
    description: 'Повтор больше **не создаёт** платёж.\n\n<script>alert(1)</script>', diffRefs: { baseSha: '0'.repeat(40), headSha: head }, sha: head, removeSourceBranch: true },
  approvals: { rules: [], approvedBy: [] }, pipelines: [], mine: true, approvedByMe: false,
}) })
html = card.root.innerHTML
check('описание разобрано как markdown', html.includes('<strong>не создаёт</strong>'), html.slice(0, 300))
check('<script> из описания не стал разметкой', !html.includes('<script>') && html.includes('&lt;script&gt;'))
check('merge доступен открытому MR', html.includes('data-action="gitlab-merge"'))

card.click({ action: 'gitlab-merge', sha: head })
sent = card.take()
const merge = sent.find(m => m.action === 'merge')
check('merge уходит с головой, которую видел владелец', merge?.sha === head && merge?.removeSourceBranch === true && merge?.surface === surface, JSON.stringify(sent))
card.click({ action: 'gitlab-approve', approve: '1', sha: head })
check('одобрение — с той же головой', card.take().some(m => m.action === 'approval' && m.approve === true && m.sha === head))

card.click({ action: 'gitlab-mr-tab', tab: 'discussion' })
card.send({ type: 'gitlabDiscussions', response: ok({ discussions: [{ id: 'd3b4', individual: false, notes: [{ id: 1, author: anna, body: 'Нужен таймаут' }] }] }) })
card.click({ action: 'gitlab-comment' })
check('пустой комментарий не уходит', !card.take().some(m => m.action === 'comment'))
check('и объясняется', card.root.innerHTML.includes('Комментарий пуст'))
card.type('reply:d3b4', 'Добавила')
card.click({ action: 'gitlab-comment', discussion: 'd3b4' })
check('ответ уходит в свою нить', card.take().some(m => m.action === 'comment' && m.discussionId === 'd3b4' && m.body === 'Добавила'))
card.send({ type: 'gitlabDiscussions', posted: true, response: ok({ discussions: [] }) })
check('после публикации черновик ответа очищен', !card.root.innerHTML.includes('Добавила'))

card.click({ action: 'gitlab-mr-tab', tab: 'changes' })
card.send({ type: 'gitlabChanges', response: ok({ files: [{ oldPath: 'a.go', newPath: 'a.go' }] }) })
card.click({ action: 'gitlab-open-diff', path: 'a.go', oldPath: 'a.go', newFile: '0', deleted: '0' })
const diff = card.take().find(m => m.action === 'openDiff')
check('diff — база и голова из diff_refs', diff?.base === '0'.repeat(40) && diff?.head === head && diff?.path === 'a.go', JSON.stringify(diff))

if (failures.length) {
  console.error('Окно GitLab: проверки провалены')
  for (const line of failures) console.error('  · ' + line)
  process.exit(1)
}
console.log('окно GitLab и карточка MR: ok')
