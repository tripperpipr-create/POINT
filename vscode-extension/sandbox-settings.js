const fs = require('node:fs')
const path = require('node:path')

// Moby ships inside Point: the pinned pack lies beside the core in bin/moby and
// lives and dies with this installation. A manifest setting stays only as an
// operator override for a separately verified pack.
const BUNDLED_PACK = 'moby'
const ENGINE_SETTINGS = ['sandboxBackend', 'embeddedRuntimeManifest', 'sandboxCPUs', 'sandboxMemoryGiB', 'verificationReuse']

// Engine selection is an application setting. A project must not override its
// manifest, security boundary or resource limits through workspace settings.
function globalSetting(config, name, inherited, fallback) {
  const value = config.inspect?.(name)
  if (value) return value.globalValue ?? inherited ?? value.defaultValue ?? fallback
  return config.get(name, inherited ?? fallback)
}

function explicitGlobal(config, name) {
  const value = config.inspect?.(name)
  return value ? value.globalValue : undefined
}

function bundledManifest(coreDir) {
  if (!coreDir) return ''
  const manifest = path.join(coreDir, BUNDLED_PACK, 'runtime.json')
  return fs.existsSync(manifest) ? manifest : ''
}

function checkMobyManifest(manifest) {
  if (!path.isAbsolute(manifest)) throw new Error('Путь к пакету Moby должен быть абсолютным')
  if (!fs.existsSync(manifest)) return false
  const file = fs.lstatSync(manifest)
  if (!file.isFile() || file.isSymbolicLink() || file.size > 65536) throw new Error('Пакет Moby должен быть обычным локальным файлом runtime.json')
  let engine
  try { engine = JSON.parse(fs.readFileSync(manifest, 'utf8')).engine } catch { throw new Error('runtime.json пакета Moby повреждён') }
  if (engine !== 'moby') throw new Error('Встроенная среда Point работает на Moby; выбран пакет другого движка')
  return true
}

function sandboxEnvironment(config, inherited, bridge) {
  const coreDir = bridge ? path.dirname(bridge) : ''
  const bundled = bundledManifest(coreDir)
  // Without an explicit choice a Point that carries its Moby pack runs on it.
  const preferred = explicitGlobal(config, 'sandboxBackend') ?? inherited.POINT_SANDBOX_BACKEND ?? (bundled ? 'embedded' : undefined)
  const backend = String(globalSetting(config, 'sandboxBackend', preferred, 'filtered-copy')).toLowerCase()
  if (!['filtered-copy', 'docker', 'container', 'embedded'].includes(backend)) throw new Error(`Неизвестная среда исполнения Point: ${backend}`)
  // Reuse of passed checks applies to every backend, not only to Moby.
  const verify = String(globalSetting(config, 'verificationReuse', inherited.POINT_VERIFY_SERVICE, 'on'))
  if (!['off', 'shadow', 'on'].includes(verify)) throw new Error(`Неизвестный режим переиспользования проверок: ${verify}`)
  const env = { POINT_SANDBOX_BACKEND: backend === 'container' ? 'docker' : backend, POINT_VERIFY_SERVICE: verify }
  const image = String(config.get('sandboxImage', '') || '').trim()
  if (image) env.POINT_SANDBOX_IMAGE = image
  if (backend !== 'embedded') return env
  let manifest = String(globalSetting(config, 'embeddedRuntimeManifest', inherited.POINT_EMBEDDED_RUNTIME, '') || '').trim()
  // A stale override (moved or cleaned pack) must not hide the pack Point ships with.
  if (!manifest || (!checkMobyManifest(manifest) && bundled)) manifest = bundled
  if (manifest) checkMobyManifest(manifest)
  const cpus = Number(globalSetting(config, 'sandboxCPUs', undefined, 2))
  const memory = Number(globalSetting(config, 'sandboxMemoryGiB', undefined, 2))
  if (!Number.isInteger(cpus) || cpus < 1 || cpus > 16) throw new Error('Лимит CPU Moby должен быть целым числом от 1 до 16')
  if (!Number.isInteger(memory) || memory < 1 || memory > 32) throw new Error('Лимит памяти Moby должен быть целым числом ГиБ от 1 до 32')
  return { ...env, POINT_EMBEDDED_RUNTIME: manifest, POINT_RUNTIME_BRIDGE: bridge,
    POINT_SANDBOX_WORKSPACE: 'volume', POINT_LIVE_WORKSPACE: '0', POINT_SANDBOX_REQUIRE_STRONG: 'true',
    POINT_SANDBOX_MEMORY: `${memory}g`, POINT_SANDBOX_CPUS: String(cpus), POINT_SANDBOX_PIDS: '256',
    POINT_SANDBOX_USER: '10001:10001', POINT_SANDBOX_WARM_CONTAINER: 'on', POINT_SANDBOX_DOWNLOAD_CACHE: 'on' }
}

function configureSandbox(service, config, inherited, binary) {
  try {
    return sandboxEnvironment(config, inherited,
      path.join(path.dirname(binary), process.platform === 'win32' ? 'point-runtime.exe' : 'point-runtime'))
  } catch (error) {
    service.releaseRuntimeLock()
    service.setState('error', error.message)
    throw error
  }
}

// The core reads the environment once at launch, so a changed engine setting
// would silently wait for the next start. Say so and offer the restart.
function watchSandboxSettings(vscode) {
  return vscode.workspace.onDidChangeConfiguration(async event => {
    if (!ENGINE_SETTINGS.some(name => event.affectsConfiguration(`localAgent.${name}`))) return
    const choice = await vscode.window.showInformationMessage(
      'Настройки среды исполнения применятся после перезапуска ядра Point. Идущие квесты сохранят прежнюю среду.',
      'Перезапустить ядро',
    )
    if (choice === 'Перезапустить ядро') await vscode.commands.executeCommand('localAgent.restartServer')
  })
}

module.exports = { sandboxEnvironment, configureSandbox, watchSandboxSettings, bundledManifest }
