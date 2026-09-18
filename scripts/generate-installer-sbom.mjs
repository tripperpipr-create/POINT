import crypto from 'node:crypto'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const normalize = value => value.split(path.sep).join('/')

export async function sha256File(file) {
  const hash = crypto.createHash('sha256')
  await new Promise((resolve, reject) => {
    const input = fs.createReadStream(file)
    input.on('error', reject)
    input.on('data', chunk => hash.update(chunk))
    input.on('end', resolve)
  })
  return hash.digest('hex').toUpperCase()
}

function readPackage(file) {
  try {
    const manifest = JSON.parse(fs.readFileSync(file, 'utf8'))
    if (typeof manifest.name !== 'string' || !manifest.name.trim()) return undefined
    if (typeof manifest.version !== 'string' || !manifest.version.trim()) return undefined
    return { name: manifest.name.trim(), version: manifest.version.trim() }
  } catch {
    return undefined
  }
}

function npmPurl(name, version) {
  const parts = name.startsWith('@') ? name.slice(1).split('/') : [name]
  const encodedName = parts.length > 1
    ? `%40${encodeURIComponent(parts[0])}/${encodeURIComponent(parts.slice(1).join('/'))}`
    : encodeURIComponent(parts[0])
  return `pkg:npm/${encodedName}@${encodeURIComponent(version)}`
}

function componentRef(type, name, version) {
  const digest = crypto.createHash('sha256').update(`${type}\0${name}\0${version}`).digest('hex').slice(0, 24)
  return `point:${type}:${digest}`
}

function addPackage(packages, type, manifest, relativePath) {
  if (!manifest) return
  const key = `${type}\0${manifest.name}\0${manifest.version}`
  const existing = packages.get(key) ?? {
    type,
    name: manifest.name,
    version: manifest.version,
    paths: new Set(),
  }
  existing.paths.add(normalize(relativePath))
  packages.set(key, existing)
}

function scanNodeModules(packages, portableRoot, initialDirectory) {
  const pending = [initialDirectory]
  const visited = new Set()
  while (pending.length) {
    const nodeModules = pending.pop()
    const resolved = path.resolve(nodeModules)
    if (visited.has(resolved) || !fs.existsSync(resolved)) continue
    visited.add(resolved)
    for (const entry of fs.readdirSync(resolved, { withFileTypes: true })) {
      if (!entry.isDirectory() || entry.isSymbolicLink() || entry.name === '.bin') continue
      if (entry.name.startsWith('@')) {
        const scopeRoot = path.join(resolved, entry.name)
        for (const scoped of fs.readdirSync(scopeRoot, { withFileTypes: true })) {
          if (!scoped.isDirectory() || scoped.isSymbolicLink()) continue
          const packageRoot = path.join(scopeRoot, scoped.name)
          addPackage(packages, 'library', readPackage(path.join(packageRoot, 'package.json')), path.relative(portableRoot, packageRoot))
          pending.push(path.join(packageRoot, 'node_modules'))
        }
        continue
      }
      if (entry.name.startsWith('.')) continue
      const packageRoot = path.join(resolved, entry.name)
      addPackage(packages, 'library', readPackage(path.join(packageRoot, 'package.json')), path.relative(portableRoot, packageRoot))
      pending.push(path.join(packageRoot, 'node_modules'))
    }
  }
}

export async function collectInstallerInventory(installerPath, portableRoot) {
  const installer = path.resolve(installerPath)
  const portable = path.resolve(portableRoot)
  const appRoot = path.join(portable, 'resources', 'app')
  for (const required of [installer, path.join(portable, 'Point.exe'), path.join(appRoot, 'package.json')]) {
    if (!fs.existsSync(required)) throw new Error(`required packaged artifact is missing: ${required}`)
  }

  const packages = new Map()
  const appManifest = readPackage(path.join(appRoot, 'package.json'))
  if (!appManifest) throw new Error('packaged resources/app/package.json has no name and version')
  addPackage(packages, 'application', appManifest, path.relative(portable, appRoot))

  const extensionsRoot = path.join(appRoot, 'extensions')
  let extensionCount = 0
  for (const entry of fs.readdirSync(extensionsRoot, { withFileTypes: true })) {
    if (!entry.isDirectory() || entry.isSymbolicLink()) continue
    const extensionRoot = path.join(extensionsRoot, entry.name)
    const manifest = readPackage(path.join(extensionRoot, 'package.json'))
    if (manifest) {
      extensionCount += 1
      addPackage(packages, 'application', manifest, path.relative(portable, extensionRoot))
    }
    scanNodeModules(packages, portable, path.join(extensionRoot, 'node_modules'))
  }
  scanNodeModules(packages, portable, path.join(appRoot, 'node_modules'))

  const keyRelativeFiles = [
    'Point.exe',
    'resources/app/extensions/local-agent-workbench/extension.js',
    'resources/app/extensions/local-agent-workbench/bin/point-core.exe',
    'resources/app/extensions/local-agent-workbench/bin/point-db.exe',
  ]
  const files = []
  for (const relative of keyRelativeFiles) {
    const absolute = path.join(portable, ...relative.split('/'))
    if (!fs.existsSync(absolute)) throw new Error(`key packaged file is missing: ${absolute}`)
    files.push({
      type: 'file',
      name: relative,
      version: '1',
      sha256: await sha256File(absolute),
      size: fs.statSync(absolute).size,
    })
  }

  return {
    installer,
    portable,
    installerName: path.basename(installer),
    installerSha256: await sha256File(installer),
    installerSize: fs.statSync(installer).size,
    installerTimestamp: fs.statSync(installer).mtime.toISOString(),
    productVersion: appManifest.version,
    extensionCount,
    packages: [...packages.values()].sort((a, b) => `${a.type}\0${a.name}\0${a.version}`.localeCompare(`${b.type}\0${b.name}\0${b.version}`)),
    files,
  }
}

function deterministicSerial(installerSha256) {
  const bytes = Buffer.from(installerSha256.slice(0, 32), 'hex')
  bytes[6] = (bytes[6] & 0x0f) | 0x50
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = bytes.toString('hex')
  return `urn:uuid:${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

export function createBom(inventory) {
  const packageComponents = inventory.packages.map(item => ({
    type: item.type,
    'bom-ref': componentRef(item.type, item.name, item.version),
    name: item.name,
    version: item.version,
    ...(item.type === 'library' ? { purl: npmPurl(item.name, item.version) } : {}),
    properties: [{
      name: 'point:installedPaths',
      value: [...item.paths].sort().join(';'),
    }],
  }))
  const fileComponents = inventory.files.map(item => ({
    type: 'file',
    'bom-ref': componentRef('file', item.name, item.sha256),
    name: item.name,
    hashes: [{ alg: 'SHA-256', content: item.sha256 }],
    properties: [{ name: 'point:file:size', value: String(item.size) }],
  }))
  const libraryCount = inventory.packages.filter(item => item.type === 'library').length
  const components = [...packageComponents, ...fileComponents]
    .sort((a, b) => `${a.type}\0${a.name}\0${a.version ?? ''}`.localeCompare(`${b.type}\0${b.name}\0${b.version ?? ''}`))
  return {
    $schema: 'http://cyclonedx.org/schema/bom-1.7.schema.json',
    bomFormat: 'CycloneDX',
    specVersion: '1.7',
    serialNumber: deterministicSerial(inventory.installerSha256),
    version: 1,
    metadata: {
      timestamp: inventory.installerTimestamp,
      tools: { components: [{ type: 'application', name: 'point-installer-sbom-generator', version: '1.0.0' }] },
      component: {
        type: 'application',
        'bom-ref': `point:installer:${inventory.installerSha256.toLowerCase()}`,
        name: 'Point IDE Installer',
        version: inventory.productVersion,
        hashes: [{ alg: 'SHA-256', content: inventory.installerSha256 }],
        properties: [
          { name: 'point:installer:fileName', value: inventory.installerName },
          { name: 'point:installer:size', value: String(inventory.installerSize) },
          { name: 'point:installer:sha256', value: inventory.installerSha256 },
          { name: 'point:portable:componentCount', value: String(components.length) },
          { name: 'point:portable:extensionCount', value: String(inventory.extensionCount) },
          { name: 'point:portable:libraryCount', value: String(libraryCount) },
        ],
      },
    },
    components,
  }
}

async function main() {
  const [installer, portable, output] = process.argv.slice(2)
  if (!installer || !portable || !output) {
    throw new Error('usage: node scripts/generate-installer-sbom.mjs <installer> <portable-root> <output>')
  }
  const inventory = await collectInstallerInventory(installer, portable)
  const bom = createBom(inventory)
  fs.mkdirSync(path.dirname(path.resolve(output)), { recursive: true })
  fs.writeFileSync(path.resolve(output), `${JSON.stringify(bom, null, 2)}\n`)
  console.log(`Installer SBOM: ${path.resolve(output)} (${bom.components.length} components)`)
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch(error => {
    console.error(error instanceof Error ? error.message : String(error))
    process.exitCode = 1
  })
}
