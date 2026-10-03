// Проекты GitLab: раздел «Проекты» окна и карточка проекта.
//
// Что проверяется:
// - раздел «Проекты» открыт и в папке, не связанной с GitLab: список
//   спрашивается, а MR и пайплайны — нет;
// - поиск и «Мои / Свои» уходят хосту от имени окна (`surface: tool`);
// - проект открывается карточкой с тем путём, что нажат;
// - карточка спрашивает проект от своего имени (`project:<путь>`), корень
//   ветки по умолчанию и README из него; описание и README проходят
//   экранирование и разборщик — `<script>` не становится разметкой;
// - клон уходит хосту без адреса: адрес хост берёт у ядра; «уже
//   склонирован» открывает папку; эта папка клона не предлагает;
// - папка открывается внутри карточки, файл — документом IDE на ветке;
// - коммит раскрывается файлами, файл коммита — diff с первым родителем;
// - ветка из «Веток» переключает историю и файлы.
//
//   node scripts/smoke-gitlab-projects.js   (после npm run build)

const { bootWebview } = require('./lib/webview-harness')

const failures = []
const check = (name, ok, detail = '') => { if (!ok) failures.push(`${name}${detail ? `: ${detail}` : ''}`) }
const ok = data => ({ state: 'ok', data })
const head = 'a1b2c3d4e5f60718293a4b5c6d7e8f9012345678'
const parent = '9'.repeat(40)
const project = {
  id: 42, path: 'billing/payments', name: 'payments', namespace: 'billing', description: 'Платежи <img src=x onerror=alert(1)>',
  defaultBranch: 'main', webUrl: 'https://gitlab.example.test/billing/payments', httpUrl: 'https://gitlab.example.test/billing/payments.git',
  sshUrl: 'git@gitlab.example.test:billing/payments.git', accessLevel: 30, stars: 12,
}

// ── Окно: раздел «Проекты» в несвязанной папке ─────────────────────────────
const tool = bootWebview({ layout: 'tool-gitlab' })
tool.state({ workspacePath: 'C:/work/dotfiles' })
tool.take()
tool.send({ type: 'gitlabStatus', response: ok({ configured: true, linked: false, binding: { mode: 'off', workspace: 'dotfiles' } }) })
let sent = tool.take()
let html = tool.root.innerHTML
check('у несвязанной папки есть вкладка «Проекты»', html.includes('data-section="projects"'))
check('MR несвязанной папки не спрашиваются', !sent.some(m => m.action === 'mergeRequests'), JSON.stringify(sent))
tool.click({ action: 'gitlab-section', section: 'projects' })
sent = tool.take()
check('раздел спрашивает проекты от имени окна', sent.some(m => m.action === 'projects' && m.scope === 'member' && m.search === '' && m.surface === 'tool'), JSON.stringify(sent))
check('раздел проектов не спрашивает MR', !sent.some(m => m.action === 'mergeRequests' || m.action === 'pipelines'))
tool.send({ type: 'gitlabProjects', scope: 'member', search: '', response: ok({ scope: 'member', current: '', items: [
  project, { ...project, id: 80, path: 'legacy/old', name: 'old', description: '', archived: true, accessLevel: 10 },
] }) })
html = tool.root.innerHTML
check('проект в списке', html.includes('data-action="gitlab-open-project"') && html.includes('data-project="billing/payments"'))
check('описание проекта экранировано', html.includes('&lt;img') && !html.includes('<img'))
check('архив сгруппирован отдельно', html.includes('Архив') && html.includes('в архиве — только чтение'))
tool.type('projectSearch', 'billing/pay')
tool.click({ action: 'gitlab-projects-search' })
check('поиск уходит хосту', tool.take().some(m => m.action === 'projects' && m.search === 'billing/pay'))
tool.click({ action: 'gitlab-projects-scope', scope: 'owned' })
check('«Свои» — отдельный список', tool.take().some(m => m.action === 'projects' && m.scope === 'owned'))
tool.click({ action: 'gitlab-open-project', project: 'billing/payments', name: 'payments' })
check('проект открывается карточкой', tool.take().some(m => m.action === 'openProject' && m.project === 'billing/payments'))

// ── Окно без папки: каталог первым, избранное и фильтр групп ───────────────
const all = bootWebview({ layout: 'tool-gitlab' })
all.state()
all.take()
all.send({ type: 'gitlabStatus', response: ok({ configured: true, url: 'https://gitlab.example.test', linked: true, binding: { mode: 'all' } }) })
sent = all.take()
html = all.root.innerHTML
check('без папки окно открывает каталог', sent.some(m => m.action === 'projects') && !sent.some(m => m.action === 'mergeRequests'), JSON.stringify(sent))
check('без папки вкладки «Пайплайны» нет', !html.includes('data-section="pipelines"') && html.includes('data-section="mrs"'))
check('избранное спрашивается по серверу', sent.some(m => m.action === 'prefs' && m.server === 'https://gitlab.example.test'))
const invoices = { ...project, id: 51, path: 'billing/invoices', name: 'invoices', namespace: 'billing' }
const bot = { ...project, id: 64, path: 'platform/tools/deploy-bot', name: 'deploy-bot', namespace: 'platform/tools', description: '' }
all.send({ type: 'gitlabProjects', scope: 'member', search: '', local: { 'billing/invoices': 'D:/work/invoices' }, response: ok({ scope: 'member', current: '', items: [project, invoices, bot] }) })
all.send({ type: 'gitlabPrefs', server: 'https://gitlab.example.test', prefs: { favorites: ['platform/tools/deploy-bot'], groups: [], groupBy: 'ns' } })
html = all.root.innerHTML
check('избранное — своей группой сверху', html.indexOf('Избранное') > -1 && html.indexOf('Избранное') < html.indexOf('data-project="billing/payments"'), html.slice(0, 600))
check('проекты разложены по группам', html.includes('<span>billing</span>'))
check('склонированный проект отмечен', html.includes('локально'))
all.click({ action: 'gitlab-favorite', project: 'billing/invoices' })
let saved = all.take().find(m => m.action === 'savePrefs')
check('звезда сохраняет избранное', saved?.prefs?.favorites?.includes('billing/invoices') && saved.server === 'https://gitlab.example.test', JSON.stringify(saved))
all.click({ action: 'gitlab-filters-toggle' })
check('фильтр показывает дерево групп', all.root.innerHTML.includes('data-group="platform/tools"'))
all.click({ action: 'gitlab-group', group: 'platform' })
html = all.root.innerHTML
saved = all.take().find(m => m.action === 'savePrefs')
check('группа фильтрует список и сохраняется', saved?.prefs?.groups?.includes('platform') && !html.includes('data-project="billing/payments"'), JSON.stringify(saved))
check('выбранная группа стоит чипом', html.includes('class="gl-chip"'))
all.click({ action: 'gitlab-filters-clear' })
check('сброс снимает фильтр', all.take().some(m => m.action === 'savePrefs' && m.prefs.groups.length === 0))
all.click({ action: 'gitlab-section', section: 'mrs' })
all.click({ action: 'gitlab-mr-favorites' })
check('у MR без папки есть «только избранные»', all.take().some(m => m.action === 'savePrefs' && m.prefs.mrFav === true))

// ── Карточка проекта ───────────────────────────────────────────────────────
const card = bootWebview({ layout: 'gitlab-project', dataset: { gitlabProject: 'billing/payments' } })
const surface = 'project:billing/payments'
card.state()
sent = card.take()
check('карточка спрашивает проект от своего имени', sent.some(m => m.action === 'project' && m.project === 'billing/payments' && m.surface === surface), JSON.stringify(sent))
card.send({ type: 'gitlabProject', response: ok({ project, current: false }), clone: { exists: false } })
sent = card.take()
check('карточка спрашивает корень ветки по умолчанию', sent.some(m => m.action === 'tree' && m.path === '' && m.ref === 'main' && m.surface === surface), JSON.stringify(sent))
html = card.root.innerHTML
check('вердикт: копии нет, клон по SSH и HTTPS', html.includes('Локальной копии нет') && html.includes('data-kind="ssh"') && html.includes('data-kind="https"'))
card.send({ type: 'gitlabTree', path: '', ref: 'main', response: ok({ tree: { path: '', ref: 'main', entries: [
  { name: 'internal', path: 'internal', type: 'tree' }, { name: 'README.md', path: 'README.md', type: 'blob' },
] } }) })
check('README ищется в корне и спрашивается', card.take().some(m => m.action === 'readme' && m.path === 'README.md' && m.ref === 'main'))
card.send({ type: 'gitlabReadme', path: 'README.md', response: ok({ content: '# payments\n\n**важно**\n\n<script>alert(1)</script>' }) })
html = card.root.innerHTML
check('README разобран как markdown', html.includes('<strong>важно</strong>'))
check('<script> из README не стал разметкой', !html.includes('<script>') && html.includes('&lt;script&gt;'))

card.click({ action: 'gitlab-clone', kind: 'ssh' })
const clone = card.take().find(m => m.action === 'clone')
check('клон уходит хосту без адреса', clone?.kind === 'ssh' && clone.project === 'billing/payments' && clone.url === undefined, JSON.stringify(clone))
check('пока выбирается папка, вердикт об этом говорит', card.root.innerHTML.includes('Выберите папку для клона'))
card.send({ type: 'gitlabClone', state: 'done', clone: { exists: true, path: 'C:\\work\\payments' } })
html = card.root.innerHTML
check('после клона — «уже склонирован» с папкой', html.includes('Проект уже склонирован') && html.includes('C:\\work\\payments'))
card.click({ action: 'gitlab-open-clone', newWindow: '1' })
check('«В новом окне» уходит хосту', card.take().some(m => m.action === 'openClone' && m.newWindow === true))

card.click({ action: 'gitlab-project-tab', tab: 'files' })
card.click({ action: 'gitlab-tree-open', type: 'tree', path: 'internal' })
check('папка открывается в карточке', card.take().some(m => m.action === 'tree' && m.path === 'internal' && m.ref === 'main'))
card.click({ action: 'gitlab-tree-open', type: 'blob', path: 'internal/a.go' })
check('файл — документом IDE на ветке', card.take().some(m => m.action === 'openFile' && m.path === 'internal/a.go' && m.ref === 'main'))

card.click({ action: 'gitlab-project-tab', tab: 'commits' })
sent = card.take()
check('история ветки спрашивается с первой страницы', sent.some(m => m.action === 'commits' && m.ref === 'main' && m.page === 1), JSON.stringify(sent))
card.send({ type: 'gitlabCommits', ref: 'main', page: 1, response: ok({ ref: 'main', page: 1, more: true, items: [
  { id: head, shortId: 'a1b2c3d4', title: 'Таймаут <b>ретрая</b>', authorName: 'Анна', authoredAt: new Date().toISOString(), parentIds: [parent], stats: { additions: 3, deletions: 1 } },
] }) })
html = card.root.innerHTML
check('заголовок коммита экранирован', html.includes('&lt;b&gt;ретрая') && !html.includes('<b>ретрая'))
card.click({ action: 'gitlab-commits-more' })
check('«Показать ещё» спрашивает вторую страницу', card.take().some(m => m.action === 'commits' && m.page === 2))
card.click({ action: 'gitlab-commit-toggle', sha: head })
check('раскрытый коммит спрашивает файлы', card.take().some(m => m.action === 'commit' && m.sha === head))
card.send({ type: 'gitlabCommit', sha: head, response: ok({ commit: { id: head, parentIds: [parent], files: [
  { oldPath: 'a.go', newPath: 'a.go', additions: 3, deletions: 1 },
] } }) })
card.click({ action: 'gitlab-commit-file', sha: head, parent, path: 'a.go', oldPath: 'a.go', newFile: '0', deleted: '0' })
const diff = card.take().find(m => m.action === 'openCommitDiff')
check('diff файла коммита — с первым родителем', diff?.sha === head && diff?.parent === parent && diff?.path === 'a.go', JSON.stringify(diff))

card.click({ action: 'gitlab-branch-commits', ref: 'fix/webhook-retry' })
check('ветка переключает историю', card.take().some(m => m.action === 'commits' && m.ref === 'fix/webhook-retry' && m.page === 1))

// Эта папка — рабочая копия: клон не предлагается.
const here = bootWebview({ layout: 'gitlab-project', dataset: { gitlabProject: 'billing/payments' } })
here.state()
here.send({ type: 'gitlabProject', response: ok({ project, current: true }), clone: { exists: false } })
html = here.root.innerHTML
check('своя папка не предлагает клон', html.includes('Эта папка — рабочая копия проекта') && !html.includes('data-action="gitlab-clone"'))

// ── Хост: клон берёт адрес у ядра, находит папку по .git/config ───────────
const fs = require('fs')
const os = require('os')
const path = require('path')
const { createGitLabProjectController, findClone } = require('../vscode-extension/gitlab-project-controller')

async function hostChecks() {
  const parentDir = fs.mkdtempSync(path.join(os.tmpdir(), 'point-gitlab-clone-'))
  const coreUrl = 'git@gitlab.example.test:billing/payments.git'
  const posted = []
  const executed = []
  const routes = []
  const stored = {}
  const vscode = {
    ViewColumn: { Active: 1 },
    Uri: { file: value => ({ fsPath: value }), from: value => value, parse: value => value },
    window: { showOpenDialog: async () => [{ fsPath: parentDir }] },
    commands: {
      executeCommand: async (command, ...args) => {
        executed.push([command, ...args])
        if (command === 'git.clone') {
          const folder = path.join(args[1], 'payments')
          fs.mkdirSync(path.join(folder, '.git'), { recursive: true })
          fs.writeFileSync(path.join(folder, '.git', 'config'), `[remote "origin"]\n\turl = ${args[0]}\n`)
        }
      },
    },
    env: { clipboard: { writeText: async () => {} } },
    workspace: {},
  }
  const provider = {
    toolWindows: new Map(), post: message => posted.push(message),
    context: { globalState: { get: key => stored[key], update: async (key, value) => { stored[key] = value } } },
  }
  const request = async route => {
    routes.push(route)
    return ok({ project: { ...project, sshUrl: coreUrl }, current: false })
  }
  const controller = createGitLabProjectController({ vscode, provider, request, unlock: async () => {} })
  check('чужое действие хост проектов не забирает', !(await controller.handle({ type: 'gitlabAction', action: 'merge' })))
  await controller.handle({ type: 'gitlabAction', action: 'clone', surface: 'hub', project: 'billing/payments', kind: 'ssh', url: 'git@evil.example.org:x.git' })
  const call = executed.find(item => item[0] === 'git.clone')
  check('git.clone получил адрес ядра, а не вебвью', call?.[1] === coreUrl && call?.[2] === parentDir, JSON.stringify(executed))
  const done = posted.find(message => message.type === 'gitlabClone' && message.state === 'done')
  check('клон найден по .git/config', done?.clone?.path === path.join(parentDir, 'payments'), JSON.stringify(posted))
  check('findClone не путает адреса', findClone(parentDir, 'git@gitlab.example.test:other/repo.git') === '')

  posted.length = 0
  await controller.handle({ type: 'gitlabAction', action: 'project', surface: 'hub', project: 'billing/payments' })
  check('карточка узнаёт о копии после перезапуска', posted.some(message => message.type === 'gitlabProject' && message.clone?.exists), JSON.stringify(posted))

  posted.length = 0
  routes.length = 0
  await controller.handle({ type: 'gitlabAction', action: 'tree', surface: 'hub', project: 'billing/payments', path: 'docs/../../etc', ref: 'main' })
  check('путь с .. до ядра не доходит', !routes.length && posted.some(message => message.state === 'error'), JSON.stringify(posted))
  await controller.handle({ type: 'gitlabAction', action: 'commits', surface: 'hub', project: '../../etc', ref: 'main' })
  check('неверный проект до ядра не доходит', !routes.length)
  fs.rmSync(parentDir, { recursive: true, force: true })
}

hostChecks().then(() => {
  if (failures.length) {
    console.error('Проекты GitLab: проверки провалены')
    for (const line of failures) console.error('  · ' + line)
    process.exit(1)
  }
  console.log('проекты GitLab: ok')
})
