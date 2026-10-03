const assert = require('node:assert/strict')
const fs = require('node:fs')
const os = require('node:os')
const path = require('node:path')
const { sandboxEnvironment, configureSandbox } = require('../vscode-extension/sandbox-settings')

const root = fs.mkdtempSync(path.join(os.tmpdir(), 'point-runtime-settings-'))
try {
  const manifest = path.join(root, 'runtime.json')
  fs.writeFileSync(manifest, JSON.stringify({ engine: 'moby' }))
  const global = { sandboxBackend: 'embedded', embeddedRuntimeManifest: manifest }
  const config = {
    inspect: key => ({ globalValue: global[key], workspaceValue: key === 'sandboxBackend' ? 'filtered-copy' : '../hostile.json' }),
    get: (key, fallback) => key === 'sandboxImage' ? '' : fallback,
  }
  const env = sandboxEnvironment(config, { POINT_LIVE_WORKSPACE: '1', POINT_SANDBOX_WARM_CONTAINER: 'off', POINT_RUNTIME_BRIDGE: '../fake.exe' }, path.join(root, 'point-runtime.exe'))
  assert.equal(env.POINT_SANDBOX_BACKEND, 'embedded')
  assert.equal(env.POINT_EMBEDDED_RUNTIME, manifest)
  assert.equal(env.POINT_RUNTIME_BRIDGE, path.join(root, 'point-runtime.exe'))
  assert.equal(env.POINT_LIVE_WORKSPACE, '0')
  assert.equal(env.POINT_SANDBOX_WORKSPACE, 'volume')
  assert.equal(env.POINT_SANDBOX_REQUIRE_STRONG, 'true')
  assert.equal(env.POINT_SANDBOX_MEMORY, '2g')
  assert.equal(env.POINT_SANDBOX_CPUS, '2')
  assert.equal(env.POINT_SANDBOX_WARM_CONTAINER, 'on')
  assert.equal(env.POINT_SANDBOX_DOWNLOAD_CACHE, 'on')
  assert.equal(env.POINT_VERIFY_SERVICE, 'on')
  fs.writeFileSync(manifest, JSON.stringify({ engine: 'podman' }))
  assert.throws(() => sandboxEnvironment(config, {}, ''), /другого движка/)
  fs.writeFileSync(manifest, JSON.stringify({ engine: 'moby' }))
  global.embeddedRuntimeManifest = 'relative/runtime.json'
  assert.throws(() => sandboxEnvironment(config, {}, ''), /абсолютным/)
  let released = 0
  const states = []
  assert.throws(() => configureSandbox({ releaseRuntimeLock: () => released++, setState: (...args) => states.push(args) }, config, {}, path.join(root, 'point-core.exe')), /абсолютным/)
  assert.equal(released, 1)
  assert.equal(states[0][0], 'error')
  global.embeddedRuntimeManifest = manifest
  global.sandboxCPUs = 0
  assert.throws(() => sandboxEnvironment(config, {}, ''), /Лимит CPU/)
  delete global.sandboxCPUs
  global.verificationReuse = 'unknown'
  assert.throws(() => sandboxEnvironment(config, {}, ''), /переиспользования проверок/)
  delete global.verificationReuse
  global.embeddedRuntimeManifest = path.join(root, 'missing.json')
  assert.equal(sandboxEnvironment(config, {}, '').POINT_SANDBOX_BACKEND, 'embedded', 'missing runtime must stay unavailable, never fall back to host')
  global.sandboxBackend = 'docker'
  assert.equal(sandboxEnvironment(config, {}, '').POINT_SANDBOX_BACKEND, 'docker')
  assert.equal(sandboxEnvironment(config, {}, '').POINT_VERIFY_SERVICE, 'on', 'reuse of passed checks is not Moby-only')
  global.sandboxBackend = 'filtered-copy'
  assert.equal(sandboxEnvironment(config, {}, '').POINT_SANDBOX_BACKEND, 'filtered-copy')
  delete global.sandboxBackend
  assert.equal(sandboxEnvironment(config, { POINT_SANDBOX_BACKEND: 'embedded' }, '').POINT_SANDBOX_BACKEND, 'embedded', 'explicit operator opt-in remains supported')
  // Moby shipped with Point: found beside the core, chosen without a setting.
  const shipped = path.join(root, 'moby', 'runtime.json')
  fs.mkdirSync(path.dirname(shipped))
  fs.writeFileSync(shipped, JSON.stringify({ engine: 'moby' }))
  const bridge = path.join(root, 'point-runtime.exe')
  const unset = { inspect: key => ({ defaultValue: key === 'sandboxBackend' ? 'filtered-copy' : undefined }), get: (key, fallback) => fallback }
  const bundledEnv = sandboxEnvironment(unset, {}, bridge)
  assert.equal(bundledEnv.POINT_SANDBOX_BACKEND, 'embedded', 'Point with its Moby pack runs on it by default')
  assert.equal(bundledEnv.POINT_EMBEDDED_RUNTIME, shipped)
  global.sandboxBackend = 'embedded'
  global.embeddedRuntimeManifest = path.join(root, 'moved', 'runtime.json')
  assert.equal(sandboxEnvironment(config, {}, bridge).POINT_EMBEDDED_RUNTIME, shipped, 'stale override falls back to the shipped pack')
  global.embeddedRuntimeManifest = ''
  assert.equal(sandboxEnvironment(config, {}, bridge).POINT_EMBEDDED_RUNTIME, shipped)
  global.sandboxBackend = 'filtered-copy'
  assert.equal(sandboxEnvironment(config, {}, bridge).POINT_SANDBOX_BACKEND, 'filtered-copy', 'explicit choice beats the shipped pack')
  assert.equal(sandboxEnvironment(unset, {}, path.join(root, 'elsewhere', 'point-runtime.exe')).POINT_SANDBOX_BACKEND, 'filtered-copy', 'no pack, old default')
  console.log(JSON.stringify({ sandboxSettings: 'ok', engine: 'moby', projectOverridesIgnored: true, hostFallback: false, shippedPack: true }))
} finally {
  fs.rmSync(root, { recursive: true, force: true })
}
