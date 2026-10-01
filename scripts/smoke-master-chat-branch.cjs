// Ветка плана в папке без своего Git, но с вложенными репозиториями.
//
// 30.09.2026 человек открыл папку с двумя Git-проектами внутри и получил
// «Ветка не создана: fatal: not a git repository»: хост спрашивал
// `rev-parse --show-toplevel` только у корня. Здесь настоящие репозитории:
// ветка обязана лечь в каждый, рабочая копия — повторить их пути, а сбой на
// втором — убрать то, что уже создано в первом.
const fs = require('node:fs')
const os = require('node:os')
const path = require('node:path')
const Module = require('node:module')
const assert = require('node:assert/strict')
const { execFileSync } = require('node:child_process')

const picks = []
const inputs = []
const vscodeStub = {
  window: {
    showQuickPick: async (items, options) => { picks.push({ items, options }); return items.find(item => item.key === 'main') },
    showInputBox: async options => {
      inputs.push(options)
      assert.equal(await options.validateInput('bad..name'), 'Недопустимое имя ветки Git')
      return options.value
    },
  },
}
const originalLoad = Module._load
Module._load = function (request, ...rest) { return request === 'vscode' ? vscodeStub : originalLoad.call(this, request, ...rest) }
const { offerMasterChatBranch } = require('../vscode-extension/master-chat-branch.js')

const git = (dir, ...args) => execFileSync('git', ['-C', dir, ...args], { encoding: 'utf8' }).trim()
const temp = fs.mkdtempSync(path.join(os.tmpdir(), 'point-branch-'))
const source = path.join(temp, 'front')
const heads = {}
for (const rel of ['cf-pages', 'apps/cf-vue-apps']) {
  const dir = path.join(source, ...rel.split('/'))
  fs.mkdirSync(dir, { recursive: true })
  git(dir, 'init', '-q', '-b', rel === 'cf-pages' ? 'master' : 'main')
  git(dir, 'config', 'user.email', 't@example.com')
  git(dir, 'config', 'user.name', 't')
  fs.writeFileSync(path.join(dir, 'README.md'), rel)
  git(dir, 'add', 'README.md')
  git(dir, 'commit', '-q', '-m', 'init')
  heads[rel] = git(dir, 'rev-parse', 'HEAD')
}
fs.mkdirSync(path.join(source, 'node_modules', 'dep', '.git'), { recursive: true })

function host(dataDir) {
  const calls = { requests: [], posted: [], switched: '' }
  return {
    calls,
    workspaceFolder: () => ({ uri: { fsPath: source } }),
    switchToProject: async target => { calls.switched = target },
    post: message => calls.posted.push(message),
    service: {
      dataDirPath: dataDir,
      ensureStarted: async () => {},
      request: async (url, options) => { calls.requests.push({ url, body: JSON.parse(options.body) }); return {} },
    },
  }
}

;(async () => {
  const dataDir = path.join(temp, 'data')
  const ok = host(dataDir)
  const chat = { id: 'chat-abc123', title: 'Новый план', branchOffer: 'pending', workMode: 'plan' }
  assert.equal(await offerMasterChatBranch(ok, chat), true, JSON.stringify(ok.calls.posted))
  assert.match(picks[0].items[0].description, /apps\/cf-vue-apps: main/)
  const target = path.join(dataDir, 'managed-workspaces', 'chat-chat-abc123')
  const name = inputs[0].value
  for (const rel of ['cf-pages', 'apps/cf-vue-apps']) {
    const dir = path.join(target, ...rel.split('/'))
    assert.equal(git(dir, 'branch', '--show-current'), name)
    assert.equal(git(dir, 'rev-parse', 'HEAD'), heads[rel])
  }
  assert.equal(fs.existsSync(path.join(target, '.git')), false, 'рабочая копия сама не репозиторий')
  assert.equal(fs.existsSync(path.join(target, 'node_modules')), false, 'зависимости за репозиторий не приняты')
  const bind = ok.calls.requests.find(item => item.url.endsWith('/bind-branch'))
  assert.deepEqual(bind.body.repositories, [
    { path: 'apps/cf-vue-apps', base: 'main', commit: heads['apps/cf-vue-apps'] },
    { path: 'cf-pages', base: 'master', commit: heads['cf-pages'] },
  ])
  assert.equal(ok.calls.switched, target)
  assert.equal(ok.calls.posted.at(-1).tone, 'ok')
  assert.equal(await inputs[0].validateInput(name), 'Ветка уже есть в apps/cf-vue-apps')

  // Сбой на втором репозитории: apps/cf-vue-apps идёт первым и получает
  // ветку, а в cf-pages её успели занять между проверкой имени и созданием.
  // Ветка первого и каталог чата обязаны исчезнуть.
  const failing = host(path.join(temp, 'data-fail'))
  vscodeStub.window.showInputBox = async () => 'point/second'
  git(path.join(source, 'cf-pages'), 'branch', 'point/second')
  const failChat = { id: 'chat-fail', title: 'x', branchOffer: 'pending', workMode: 'plan' }
  assert.equal(await offerMasterChatBranch(failing, failChat), false)
  assert.equal(failing.calls.posted.at(-1).tone, 'error')
  assert.equal(fs.existsSync(path.join(temp, 'data-fail', 'managed-workspaces', 'chat-chat-fail')), false, 'каталог чата остался')
  assert.equal(git(path.join(source, 'apps', 'cf-vue-apps'), 'branch', '--list', 'point/second'), '', 'ветка первого репозитория осталась')
  assert.ok(failing.calls.requests.some(item => item.url.endsWith('/branch-offer') && item.body.state === 'skipped'))

  console.log('Master chat branch: nested repositories get one branch each, partial failure rolls back: PASS')
})().catch(error => { console.error(error); process.exitCode = 1 }).finally(() => {
  try { fs.rmSync(temp, { recursive: true, force: true }) } catch {}
})
