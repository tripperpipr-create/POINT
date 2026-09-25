// Окно GitLab и карточка merge request.
//
// Что проверяется:
// - окно спрашивает состояние, а после ответа — список своего раздела, и
//   каждый запрос помечен поверхностью `tool`: ответ придёт этому окну;
// - сбой — состояние с причиной и кнопкой следующего шага, а не пустой список;
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
tool.send({ type: 'gitlabStatus', response: ok({ configured: true, url: 'https://gitlab.example.test', user: anna,
  binding: { mode: 'auto', project: 'billing/payments', branch: 'fix/webhook-retry' } }) })
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
