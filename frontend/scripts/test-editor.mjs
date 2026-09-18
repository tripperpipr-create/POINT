import fs from 'node:fs'
import vm from 'node:vm'
import assert from 'node:assert/strict'
import ts from '../node_modules/typescript/lib/typescript.js'
const source = fs.readFileSync(new URL('../src/editor-session.ts', import.meta.url), 'utf8')
const code = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
const exports = {}; vm.runInNewContext(code, { exports })
const { EditorSession } = exports
let resolveSave, saveCalls = 0, discard = false, opens = 0
const api = { readFile: async path => ({ path, content: 'original' }), saveFile: (path, content) => {
  saveCalls++; return new Promise(resolve => { resolveSave = () => resolve({path, content}) })
} }
const session = new EditorSession(api, () => {}, () => discard)
await session.openFile('a.go'); session.setDraft('submitted')
const pending = session.save(); await session.save()
assert.equal(saveCalls, 1, 'duplicate save must not race')
session.setDraft('typed during save')
await session.changeWorkspace(async () => { opens++; return {} })
assert.equal(opens, 0, 'workspace cannot switch while save is pending')
resolveSave(); await pending
assert.equal(session.state.draft, 'typed during save'); assert.equal(session.state.saved, 'submitted'); assert.equal(session.dirty, true)
await session.changeWorkspace(async () => { opens++; return {} })
await session.openFile('b.go')
assert.equal(opens, 0); assert.equal(session.state.file.path, 'a.go')
discard = true
await assert.rejects(session.changeWorkspace(async () => { throw Error('open failed') }))
assert.equal(session.state.draft, 'typed during save'); assert.equal(session.state.busy, false)
await session.changeWorkspace(async () => ({ workspace: { path: 'next' }, tree: [] }))
assert.equal(session.state.file, undefined); assert.equal(session.dirty, false)
let readReply
const ordered = new EditorSession({ ...api, readFile: path => new Promise(resolve => { readReply = () => resolve({path, content:'next'}) }) }, () => {}, () => true)
const loading = ordered.openFile('first'); await ordered.openFile('second'); ordered.setDraft('ignored while switching'); readReply(); await loading
assert.equal(ordered.state.file.path, 'first'); assert.equal(ordered.state.draft, 'next')
console.log('frontend editor: save typing, duplicate saves, discard/cancel, failed switch, serialized opens PASS')
