// Git квеста в ленте: ветка решается до запуска, кнопки — только уместные.
//
// 02.10.2026 квест закоммитил результат в ветку, уже влитую в main и
// удалённую на сервере, и никто ветку не выбирал. Здесь проверяется то, что
// видит человек: раздел «Ветка» с предупреждениями, запуск заперт, пока
// выбор не сделан, и после квеста — коммит, отправка, MR и откат там, где
// они имеют смысл.
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const root = path.resolve(import.meta.dirname, '..')
const client = file => pathToFileURL(path.join(root, 'vscode-extension', 'ui', 'client', file)).href
const { masterWorkOrderCardsHtml } = await import(client('master-work-order-v2.js'))
const { gitChoiceMissing, handleQuestGitAction, questGitHtml } = await import(client('quest-git-views.js'))
const { esc } = await import(client('html-escape.js'))
const fail = message => { throw new Error(message) }

const base = {
  id: 'workorder-git', state: 'ready', version: 3, digest: 'sha256:x', goal: 'Новый эндпоинт',
  scope: [], criteria: [], roster: {}, network: [], secrets: [], sources: [], assumptions: [], outOfScope: [],
  workspace: { mode: 'existing', path: 'C:/p' }, stack: {}, routing: {}, budget: {}, delivery: { commitMode: 'on_completion' },
}
const stale = {
  version: '1', choice: 'required', recommended: 'new-default', branch: 'feat/flag-get-flag',
  repositories: [{ path: 'lk-backend', current: 'merge/reports-v21', headCommit: 'a', currentBase: 'merge/reports-v21', currentBaseCommit: 'a',
    defaultBase: 'origin/main', defaultBaseCommit: 'b', defaultBranch: 'main',
    warnings: ['ветка merge/reports-v21 удалена на сервере (origin/merge/reports-v21)', 'ветка merge/reports-v21 уже влита в main'] }],
}
const ready = masterWorkOrderCardsHtml([{ ...base, git: stale }], esc)
for (const expected of ['Ветка · выберите перед запуском', 'удалена на сервере', 'уже влита в main', 'data-work-order-git-choice', 'Новая ветка от основной (origin/main) · совет git-агента', 'value="feat/flag-get-flag"']) {
  if (!ready.includes(expected)) fail(`branch section lost: ${expected}`)
}
if (!/data-action="approve-master-work-order-v2"[^>]*disabled/.test(ready)) fail('quest can start before the branch is chosen')
if (!gitChoiceMissing({ git: stale }) || gitChoiceMissing({ git: { ...stale, choice: 'chosen', mode: 'new', baseKind: 'default' } })) fail('gitChoiceMissing is wrong')
const chosen = masterWorkOrderCardsHtml([{ ...base, git: { ...stale, choice: 'proposed', mode: 'new', baseKind: 'current', repositories: [{ ...stale.repositories[0], current: 'main', currentBase: 'origin/main', protected: true, warnings: [] }] } }], esc)
if (!chosen.includes('Новая ветка <code>feat/flag-get-flag</code> от origin/main') || /data-action="approve-master-work-order-v2"[^>]*disabled/.test(chosen)) fail('proposed branch must allow the launch')
if (!chosen.includes('Работать в текущей ветке main — ветка защищена')) fail('working in a protected branch must say so')

// Выбор ветки уходит новой версией наряда: только режим, база и имя.
const posted = []
const ui = { masterWorkOrderBusy: new Set(), masterData: { workOrders: [{ ...base, git: stale }] } }
const section = { querySelector: selector => selector.includes('choice') ? { value: 'new-default' } : { value: 'feat/flag-exact' } }
const target = { dataset: { id: 'workorder-git' }, closest: () => section }
if (!handleQuestGitAction({ action: 'choose-work-order-branch-v2', target, ui, vscode: { postMessage: message => posted.push(message) }, render() {} })) fail('branch choice not handled')
const revise = posted.at(-1)
if (revise?.type !== 'reviseMasterWorkOrderV2' || revise.workOrder.git.mode !== 'new' || revise.workOrder.git.baseKind !== 'default' || revise.workOrder.git.branch !== 'feat/flag-exact' || revise.workOrder.digest || revise.workOrder.runtime) {
  fail(`branch choice message wrong: ${JSON.stringify(revise)}`)
}

// После квеста: кнопки — ровно действия, которые ядро сочло уместными.
const run = { ...base, state: 'approved', runtime: { questId: 'quest-git', status: 'completed', git: { commitMode: 'on_completion', repositories: [
  { path: 'lk-backend', branch: 'feat/flag-exact', target: 'main', gitlab: true, commitId: '0123456789abcdef', commitSubject: 'feat: add exact flag lookup', actions: ['push', 'merge_request'] },
] } } }
const card = questGitHtml(run, esc)
for (const expected of ['ветка <code>feat/flag-exact</code> → main', 'коммит <code>0123456789</code> feat: add exact flag lookup', 'data-git-action="push"', 'data-git-action="merge_request"', 'Коммит — сам после успеха, отправка — по вашему решению']) {
  if (!card.includes(expected)) fail(`quest git card lost: ${expected}`)
}
if (card.includes('data-git-action="commit"') || card.includes('data-git-action="revert"')) fail('committed quest offers commit or revert')
if (!questGitHtml(run, esc, true).includes(' disabled')) fail('busy quest must disable git buttons')
const pushed = questGitHtml({ ...run, runtime: { ...run.runtime, git: { commitMode: 'on_completion', repositories: [{ ...run.runtime.git.repositories[0], pushed: true, mrUrl: 'https://gitlab.example.test/a/b/-/merge_requests/7', actions: [] }] } } }, esc)
if (!pushed.includes('отправлено') || !pushed.includes('data-action="open-quest-git-url"') || pushed.includes('data-git-action=')) fail('finished git card is wrong')
const gitTarget = { dataset: { id: 'workorder-git', questId: 'quest-git', gitAction: 'merge_request', repo: 'lk-backend' } }
handleQuestGitAction({ action: 'quest-git-action', target: gitTarget, ui: { masterWorkOrderBusy: new Set() }, vscode: { postMessage: message => posted.push(message) }, render() {} })
const action = posted.at(-1)
if (action?.type !== 'questGitAction' || action.gitAction !== 'merge_request' || action.repo !== 'lk-backend' || action.questId !== 'quest-git') fail(`git action message wrong: ${JSON.stringify(action)}`)
if (questGitHtml({ ...base, runtime: { questId: 'q' } }, esc) !== '') fail('order without git plan must render nothing')

console.log('Quest git views: branch before launch, actions after: PASS')
