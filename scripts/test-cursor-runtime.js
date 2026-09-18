'use strict'

const assert = require('assert')
const path = require('path')
const sdkEntry = require.resolve('../vscode-extension/dist/cursor-sdk/index.js')
const runtime = require('../vscode-extension/cursor-runtime')

// Loading Point itself must not parse the large Cursor SDK. The SDK is used
// only after the user opens its integration or starts a Cursor-backed agent.
assert.equal(require.cache[sdkEntry], undefined)
assert.equal(runtime.available, true)

assert.equal(runtime.__test.isAuthenticated({ authenticated: true }), true)
assert.equal(runtime.__test.isAuthenticated({ loggedIn: true }), true)
assert.equal(runtime.__test.isAuthenticated({ status: 'logged-in' }), true)
assert.equal(runtime.__test.isAuthenticated({ status: 'logged-out' }), false)
assert.equal(runtime.__test.isAuthenticated(false), false)

const mapped = runtime.__test.mapAllowedTools(['read_file', 'propose_patch', 'run_command'])
assert.deepEqual(mapped.tools.sort(), ['delete', 'edit', 'read', 'shell', 'write'].sort())
assert.ok(mapped.disallowedTools.includes('grep'))

const prompt = runtime.__test.buildPrompt({
  roleDescription: 'Developer',
  systemPrompt: 'Be careful',
  goals: ['Ship', ''],
  rules: ['Ask first'],
}, 'Fix the bug')
assert.ok(prompt.includes('Role:\nDeveloper'))
assert.ok(prompt.includes('Goals:\n- Ship'))
assert.ok(prompt.includes('User task:\nFix the bug'))

const fakeSdk = {
  Cursor: {
    auth: {
      status: async () => ({ status: 'logged-in', email: 'dev@example.com' }),
      login: async () => ({ apiKey: 'cursor_test', email: 'dev@example.com' }),
      logout: async () => {},
    },
    models: {
      list: async () => [{ id: 'auto' }, { id: 'composer-2' }],
    },
  },
  Agent: {
    async create() {
      return {
        async send() {
          return {
            id: 'run-1',
            agentId: 'agent-1',
            async *stream() {
              yield { type: 'assistant', agent_id: 'agent-1', run_id: 'run-1', message: { content: [{ type: 'text', text: 'hello' }] } }
            },
            async wait() { return { status: 'finished', result: 'ok' } },
            async cancel() {},
            supports() { return true },
          }
        },
        async [Symbol.asyncDispose]() {},
      }
    },
  },
}

async function main() {
  let lazyLoads = 0
  const lazy = runtime.__test.createRuntime({
    sdkAvailable: true,
    sdkLoader: () => {
      lazyLoads += 1
      return { sdk: fakeSdk }
    },
  })
  assert.equal(lazy.available, true)
  assert.equal(lazyLoads, 0)
  await lazy.status({ includeModels: false })
  assert.equal(lazyLoads, 1)
  await lazy.status({ includeModels: false })
  assert.equal(lazyLoads, 1)

  const created = runtime.__test.createRuntime({ sdk: fakeSdk })
  const status = await created.status()
  assert.equal(status.available, true)
  assert.equal(status.authenticated, true)
  assert.equal(status.model, 'auto')

  const events = []
  const handle = created.startRun({
    profile: { model: 'auto', allowedTools: ['read_file'], roleDescription: 'Dev', systemPrompt: 'Go' },
    task: 'Say hi',
    cwd: process.cwd(),
    onEvent: event => events.push(event),
  })
  const result = await handle.done
  assert.equal(result.status, 'finished')
  assert.ok(events.some(event => event.type === 'assistant' && event.text === 'hello'))
  assert.ok(events.some(event => event.type === 'done'))
  process.stdout.write(JSON.stringify({ cursorRuntime: 'ok', events: events.map(event => event.type) }) + '\n')
}

main().catch(error => {
  console.error(error)
  process.exit(1)
})
