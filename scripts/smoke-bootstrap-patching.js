'use strict'

const assert = require('assert')
const fs = require('fs')
const Module = require('module')
const path = require('path')

const originalLoad = Module._load
Module._load = function load(request, parent, isMain) {
  if (request === 'vscode') {
    return {
      workspace: {
        isTrusted: true,
        workspaceFolders: [],
        getConfiguration: () => ({ get: (_key, fallback) => fallback }),
      },
      window: {},
      commands: {},
      extensions: {},
      Uri: {},
      EventEmitter: class {},
      StatusBarAlignment: { Left: 1, Right: 2 },
      ThemeIcon: class {},
      Disposable: { from: () => ({ dispose() {} }) },
    }
  }
  return originalLoad(request, parent, isMain)
}

const extensionPath = path.resolve(__dirname, '..', 'vscode-extension', 'extension.js')
const { upsertById, removeById } = require(extensionPath).__test

const original = [{ id: 'a', value: 1 }, { id: 'b', value: 2 }]
assert.deepEqual(upsertById(original, { id: 'b', value: 3 }), [{ id: 'a', value: 1 }, { id: 'b', value: 3 }])
assert.deepEqual(upsertById(original, { id: 'c', value: 4 }), [{ id: 'c', value: 4 }, ...original])
assert.deepEqual(removeById(original, 'a'), [{ id: 'b', value: 2 }])
assert.deepEqual(original, [{ id: 'a', value: 1 }, { id: 'b', value: 2 }], 'patch helpers must not mutate the prior snapshot')

const source = fs.readFileSync(extensionPath, 'utf8')
const fullBootstrapCalls = source.match(/request\('\/api\/bootstrap'\)/g) || []
assert.equal(fullBootstrapCalls.length, 1, `full bootstrap regression: ${fullBootstrapCalls.length} calls (cold-start budget 1)`)
assert.ok(source.includes("request('/api/state/runtime')"), 'runtime snapshot endpoint is not used')
assert.ok(source.includes("request('/api/state/guild')"), 'guild snapshot endpoint is not used')

const directPatchCases = [
  'pauseRun', 'resumeRun', 'messageRun', 'forbidFile', 'saveFlow', 'saveMemory',
  'saveProfile', 'saveCustomTool', 'saveWorkflow', 'equipSkill', 'saveTeam', 'saveSkill',
  'saveConnection', 'saveServerProfile', 'saveDBConnection', 'saveCompanionConfig',
  'saveOrchestratorConfig', 'saveBlueprint', 'saveProjectAgent', 'applyBlueprintToAgent',
  'updateBlueprintFromAgent',
]
for (const name of directPatchCases) {
  const start = source.indexOf(`case '${name}'`)
  assert.ok(start >= 0, `missing message handler ${name}`)
  const next = source.indexOf("\n        case '", start + 8)
  const handler = source.slice(start, next < 0 ? source.length : next)
  assert.ok(!handler.includes("request('/api/bootstrap')"), `${name} returned to a full bootstrap refresh`)
}

console.log(JSON.stringify({ bootstrapCalls: fullBootstrapCalls.length, directPatchCases: directPatchCases.length }))
