const Module = require('module')
const path = require('path')
const assert = require('assert')
const fs = require('fs')
const vm = require('vm')

const originalLoad = Module._load
Module._load = function load(request, parent, isMain) {
  if (request === 'vscode') {
    return {
      workspace: {
        isTrusted: true,
        workspaceFolders: [{ name: 'smoke', uri: { scheme: 'file', fsPath: process.cwd() } }],
        getConfiguration: () => ({ get: (_key, fallback) => fallback }),
        asRelativePath: uri => path.basename(String(uri?.fsPath || '')),
        onDidChangeWorkspaceFolders: () => ({ dispose() {} }),
        onDidChangeConfiguration: () => ({ dispose() {} }),
      },
      Uri: {
        file: fsPath => ({ fsPath: String(fsPath), scheme: 'file', toString: () => String(fsPath) }),
        parse: value => ({ fsPath: String(value), scheme: 'file', toString: () => String(value) }),
        joinPath: (...parts) => ({ fsPath: parts.join('/'), scheme: 'file', toString: () => parts.join('/') }),
      },
      window: {
        createOutputChannel: () => ({ append() {}, appendLine() {}, dispose() {} }),
        showInformationMessage: async () => undefined,
        showWarningMessage: async () => undefined,
        showErrorMessage: async () => undefined,
      },
      commands: { registerCommand: () => ({ dispose() {} }), executeCommand: async () => undefined },
      extensions: { getExtension: () => undefined },
      EventEmitter: class {
        constructor() { this.event = () => ({ dispose() {} }) }
        fire() {}
        dispose() {}
      },
      ViewColumn: { One: 1 },
      TreeItemCollapsibleState: { None: 0, Collapsed: 1 },
      StatusBarAlignment: { Left: 1, Right: 2 },
      ThemeIcon: class { constructor(id) { this.id = id } },
      Disposable: { from: (...items) => ({ dispose() { for (const item of items) item?.dispose?.() } }) },
    }
  }
  return originalLoad(request, parent, isMain)
}

const {
  pathIsUnder,
  pickGitRepository,
  pathRelativeToRoot,
  formatVcsError,
  gitListsState,
} = require(path.resolve(__dirname, '..', 'vscode-extension', 'extension.js')).__test

assert.equal(pathIsUnder('C:\\foo\\bar\\file.go', 'C:\\foo\\bar'), true)
assert.equal(pathIsUnder('C:\\foo\\barfile\\x.go', 'C:\\foo\\bar'), false, 'prefix sibling must not match')
assert.equal(pathIsUnder('C:\\Foo\\Bar\\x.go', 'c:\\foo\\bar'), true, 'Windows path compare is case-insensitive')

const nested = pickGitRepository([
  { rootUri: { fsPath: 'C:\\repo' } },
  { rootUri: { fsPath: 'C:\\repo\\packages\\app' } },
], { fsPath: 'C:\\repo\\packages\\app\\main.go' })
assert.equal(nested.rootUri.fsPath, 'C:\\repo\\packages\\app')

assert.equal(pathRelativeToRoot({ fsPath: 'C:\\repo\\src\\a.go' }, 'C:\\repo'), 'src/a.go')
assert.match(formatVcsError('Push недоступен', new Error('remote rejected')), /remote rejected/)

// Папки изменений. Назначение живёт ровно столько, сколько живёт изменение:
// иначе список рос бы вечно и возвращал файл в чужую папку после коммита.
{
  const lists = gitListsState(
    { lists: [{ id: 'default', name: 'Изменения' }, { id: 'l1', name: 'Рефакторинг' }], active: 'l1', assign: { 'a.js': 'l1', 'gone.js': 'l1' } },
    [{ path: 'a.js', area: 'working' }, { path: 'b.js', area: 'working' }, { path: 'n.js', area: 'untracked' }],
  )
  assert.equal(lists.assign['a.js'], 'l1', 'existing assignment must survive a refresh')
  assert.equal(lists.assign['b.js'], 'l1', 'a new change belongs to the active folder')
  assert.ok(!('gone.js' in lists.assign), 'a committed file must not keep its folder')
  assert.ok(!('n.js' in lists.assign), 'untracked files live in their own group')
  const broken = gitListsState({ lists: [{ id: 'l1', name: 'Рефакторинг' }], active: 'zzz', assign: { 'a.js': 'zzz' } }, [{ path: 'a.js', area: 'working' }])
  assert.equal(broken.lists[0].id, 'default', 'the default folder always exists')
  assert.equal(broken.active, 'default', 'an unknown active folder falls back to the default one')
  assert.equal(broken.assign['a.js'], 'default', 'an assignment to a deleted folder falls back too')
}

const extensionSource = fs.readFileSync(path.resolve(__dirname, '..', 'vscode-extension', 'extension.js'), 'utf8')
const infraSource = fs.readFileSync(path.resolve(__dirname, '..', 'vscode-extension', 'infra-controller.js'), 'utf8')
const gitHostSource = `${extensionSource}\n${infraSource}`
const uiEntrySource = fs.readFileSync(path.resolve(__dirname, '..', 'vscode-extension', 'ui', 'client', 'main.js'), 'utf8')
const gitViewSource = fs.readFileSync(path.resolve(__dirname, '..', 'vscode-extension', 'ui', 'client', 'git-views.js'), 'utf8')
const uiSource = `${uiEntrySource}\n${gitViewSource}`
const gitCss = fs.readFileSync(path.resolve(__dirname, '..', 'vscode-extension', 'ui', 'layers', '96-tool-windows.css'), 'utf8')

assert.match(extensionSource, /case 'gitAction'/, 'Git tool window must route its own actions')
assert.match(extensionSource, /repo\.state\.untrackedChanges/, 'untracked files must be shown')
assert.match(gitHostSource, /commitPaths\(root, commitMessage, paths, amend\)/, 'the commit must be assembled from the checked paths')
assert.match(extensionSource, /args\.push\('--', \.\.\.paths\)/, 'commit must not go through the index')
assert.match(gitHostSource, /useTrash: true/, 'discarding an untracked file must use the OS trash')
assert.match(extensionSource, /repo\.push\(remoteName, head\.name, !head\.upstream\)/, 'first push must publish the current branch')
// Переименование Git держит двумя записями: коммит обязан нести обе, иначе
// старый путь остаётся висеть удалённым.
assert.match(gitHostSource, /\[item\.path, item\.originalPath\]\.filter\(Boolean\)/, 'a renamed file must commit both of its paths')
assert.match(gitHostSource, /action === 'commitAndPush'/, 'commit and push must be one action')
// Расширение Git принимает то Uri, то строки. Ошибка возникает до запуска git,
// поэтому вторая попытка безопасна — но она обязана существовать.
assert.match(gitHostSource, /gitFileCommand\(repo, 'add'/, 'staging must survive both shapes of the Git API')
assert.match(extensionSource, /list\.map\(uri => uri\.fsPath\)/, 'the fallback must pass plain paths')
assert.doesNotMatch(uiSource, /function gitToolView\(\)[\s\S]*?localAgent\.vcsCommit[\s\S]*?function terminalToolView/, 'Git flow must stay inside the right tool window')
assert.match(uiSource, /id="git-commit-form"/, 'Git tool must include an inline commit form')
// Различия смотрят в редакторе: у него подсветка, навигация по изменениям и
// правка прямо в сравнении. Панель отвечает за то, что войдёт в коммит.
assert.match(uiSource, /data-action="git-select"/, 'a change row must be selectable')
assert.match(uiSource, /action: 'openChange', path: file/, 'a change row must open the diff in the editor')
assert.doesNotMatch(uiSource, /gitDiffBodyHtml/, 'the panel must not render diffs on its own')
assert.doesNotMatch(gitHostSource, /gitDiffRows/, 'the core must not parse diffs for the panel')
assert.match(extensionSource, /'stash', 'list'/, 'the panel must read the stash')
// Журнал коммитов — вкладка редактора (Летопись, Alt+9), а не вторая лента в
// узком окне: два списка одного и того же расходятся на первой же правке.
// Поэтому у окна Git нет ни вкладки «История», ни запроса состава коммита.
assert.doesNotMatch(uiSource, /id: 'history', label: 'История'/, 'the Git window must not carry a second commit log')
assert.doesNotMatch(uiSource, /action: 'loadCommit'/, 'the Git window must not read commit contents on its own')
assert.match(uiSource, /data-git-action="history"/, 'the Git window must open the journal instead')
assert.match(gitHostSource, /action === 'history'[\s\S]{0,120}localAgent\.openChronicle/, 'the journal action must open the chronicle')
assert.match(uiSource, /name="git-file"/, 'every change must carry its own commit checkbox')
// Папка — одна строка с полным путём, под ней её файлы: как в макете.
assert.match(uiSource, /function gitRowsHtml/, 'files must be grouped by their directory')
assert.doesNotMatch(uiSource, /gitCompactDirs/, 'the nested tree must be gone')
// Расширение git просыпается по встроенной «Летописи», а она скрыта: без
// собственного толчка заголовок и строка состояния молчат о ветке до первого
// открытия панели Git.
assert.match(extensionSource, /void provider\.gitContext\(\)\.catch/, 'the extension must wake the Git extension on startup')
const overlaySource = fs.readFileSync(path.resolve(__dirname, '..', 'distribution', 'apply-overlay.mjs'), 'utf8')
assert.match(overlaySource, /point-branch-chip/, 'the title bar must carry the branch chip')
assert.match(overlaySource, /scmActiveRepositoryBranchName/, 'the chip must read the branch from the SCM context key')
assert.match(extensionSource, /if \(amend\) args\.push\('--amend'\)/, 'the panel must be able to amend the last commit')
assert.match(uiSource, /gitActionButton\('moveToList'/, 'changes must be movable between folders')
assert.match(uiSource, /action: 'moveToList', path, list/, 'dragging a file between folders must move it')
assert.match(uiSource, /Вне репозитория/, 'files unknown to Git must have their own group')
assert.match(uiSource, /message\.type === 'gitActionResult'/, 'Git result must unlock and refresh the interface')
// Панель собрана по макету Nocturne и живёт в своём пространстве имён.
assert.match(gitCss, /\.nc-commit \{/, 'the commit bar must have its own styles')
assert.match(gitCss, /\.nc-file \{/, 'change rows must have compact styles')
assert.match(gitCss, /\.nc-file\.is-deleted \.nc-file-main strong \{ text-decoration: line-through; \}/, 'a deleted file must read as deleted')
assert.match(gitCss, /--nc-list-w/, 'the list column must keep the width from the design')

const listeners = {}
const posted = []
const root = {
  innerHTML: '',
  addEventListener(type, callback) { listeners[`root:${type}`] = callback },
  querySelector() { return null },
  querySelectorAll() { return [] },
}
const webviewContext = {
  acquireVsCodeApi: () => ({
    postMessage(message) { posted.push(message) },
    getState() { return undefined },
    setState() {},
  }),
  document: { getElementById: id => id === 'root' ? root : undefined, body: { dataset: { layout: 'tool-git' } } },
  window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
  console,
  Date,
  Map,
  Set,
  CSS: { escape(value) { return String(value) } },
  requestAnimationFrame(callback) { callback(); return 0 },
  cancelAnimationFrame() {},
  // Отложенные таймеры в смоуке не срабатывают: иначе уведомление, которое в
  // панели гаснет через четыре секунды, исчезает в тот же миг, и проверить
  // «панель объяснила, что сделала» становится нечем.
  setTimeout(callback, delay) { if (!delay) callback(); return 0 },
  clearTimeout() {},
}
const webviewBundle = fs.readFileSync(path.resolve(__dirname, '..', 'vscode-extension', 'media', 'main.js'), 'utf8')
vm.runInNewContext(webviewBundle, webviewContext, { filename: 'media/main.js' })
listeners['window:message']({ data: {
  type: 'state', service: { state: 'stopped' }, workspaceTrusted: true, workspace: 'fixture',
  selectedTab: 'overview', boot: undefined, details: undefined,
} })
const gitSnapshot = {
  kind: 'git', available: true, repository: 'fixture', root: 'C:\fixture', branch: 'main',
  remote: 'origin/main', ahead: 1, behind: 2, repositories: [{ root: 'C:\fixture', name: 'fixture', selected: true }],
  activeList: 'default',
  changeLists: [
    { id: 'default', name: 'Изменения', active: true, count: 1 },
    { id: 'l1', name: 'Рефакторинг', active: false, count: 1 },
  ],
  changes: [
    { path: 'src/staged.js', area: 'staged', staged: true, status: 0, list: 'default' },
    { path: 'src/working.js', area: 'working', staged: false, status: 5, list: 'l1' },
    { path: 'src/new.js', area: 'untracked', staged: false, status: 7, list: 'untracked' },
  ],
  commits: [{ shortHash: 'abc12345', message: 'Initial commit', author: 'Point', date: '2026-01-01T10:00:00Z', insertions: 4, deletions: 1 }],
}
listeners['window:message']({ data: { type: 'toolWindowState', snapshot: gitSnapshot } })
for (const required of ['git-commit-form', 'Изменения', 'Рефакторинг', 'Вне репозитория', 'staged.js', 'new.js', 'Коммит и пуш', 'data-git-action="push"', 'data-git-action="pull"', 'data-action="git-tab"', 'class="nc-dir"', 'class="nc-icon"']) {
  assert.ok(root.innerHTML.includes(required), `Git tool UI is missing: ${required}`)
}
// Отмечено то, что Git уже видел; новый файл ждёт отдельного решения.
assert.ok(root.innerHTML.includes('выбрано 2 из 3'), 'tracked changes must be checked by default')
assert.match(root.innerHTML, /value="src\/new\.js"(?![^>]*checked)/, 'an untracked file must not be checked on its own')

function clickGit(action, extra = {}) {
  listeners['root:click']({ target: { closest(selector) {
    if (selector === '[data-example]') return null
    if (selector === '[data-action]') return { dataset: { action, ...extra } }
    return null
  } } })
}
function changeGit(target) {
  listeners['root:change']({ target: { closest() { return null }, ...target } })
}

// Дерево папок: вложенность, сворачивание и кнопка новой папки изменений.
// Панель рисует дерево по сегментам пути, поэтому у каждой папки своя строка со
// своим ключом сворачивания — по нему и проверяем, что она сворачивается.
assert.ok(root.innerHTML.includes('data-list="dir:src"'), 'the tree must offer a collapsible folder row')
assert.ok(root.innerHTML.includes('data-git-action="createList"'), 'the panel must offer a new change folder')
assert.ok(root.innerHTML.includes('staged.js'), 'a file inside the folder must be visible while it is expanded')
clickGit('git-collapse', { list: 'dir:src' })
assert.ok(!root.innerHTML.includes('staged.js'), 'a collapsed folder must hide its files')
assert.ok(root.innerHTML.includes('data-list="dir:src"'), 'a collapsed folder keeps its own row')
clickGit('git-collapse', { list: 'dir:src' })
assert.ok(root.innerHTML.includes('staged.js'), 'a folder must expand back')

// Перенос файла в другую папку — то же действие, что и перетаскивание.
clickGit('git-action', { gitAction: 'moveToList', path: 'src/working.js', list: 'default' })
assert.deepEqual(
  posted.find(message => message.type === 'gitAction' && message.action === 'moveToList'),
  { type: 'gitAction', action: 'moveToList', path: 'src/working.js', list: 'default', stash: '', target: '', paths: [], repoRoot: 'C:\fixture' },
)
listeners['window:message']({ data: { type: 'gitActionResult', ok: true, action: 'moveToList', message: 'Перенесено', snapshot: gitSnapshot } })

// Снятая отметка — решение человека: коммит собирается из оставшихся файлов.
changeGit({ name: 'git-file', value: 'src/working.js', checked: false })
listeners['root:input']({ target: { id: 'git-commit-message', value: 'Понятный Git-поток', closest() { return null } } })
listeners['root:submit']({ preventDefault() {}, target: { id: 'git-commit-form' } })
const commit = posted.find(message => message.type === 'gitAction' && message.action === 'commit')
assert.ok(commit, 'Git commit form was not wired')
assert.equal(commit.message, 'Понятный Git-поток')
assert.deepEqual(commit.paths, ['src/staged.js'], 'only the checked files must reach the commit')

listeners['window:message']({ data: { type: 'gitActionResult', ok: true, action: 'commit', message: 'Коммит создан', snapshot: { ...gitSnapshot, changes: [] } } })
assert.ok(root.innerHTML.includes('Коммит создан'), 'successful Git action was not explained in the panel')

// «Только эту» собирает коммит из одной папки, не трогая остальные отметки.
listeners['window:message']({ data: { type: 'toolWindowState', snapshot: gitSnapshot } })
clickGit('git-select-only', { list: 'l1' })
assert.ok(root.innerHTML.includes('выбрано 1 из 3'), 'selecting a single folder must narrow the commit')
listeners['root:input']({ target: { id: 'git-commit-message', value: 'Только рефакторинг', closest() { return null } } })
clickGit('git-commit-push')
const pushed = posted.find(message => message.type === 'gitAction' && message.action === 'commitAndPush')
assert.ok(pushed, 'commit and push must be reachable in one click')
assert.deepEqual(pushed.paths, ['src/working.js'], 'commit and push must use the same selection')

console.log('smoke-git-workflow: ok')
