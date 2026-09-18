import fs from 'node:fs'
import path from 'node:path'
import { collectInstallerInventory } from './generate-installer-sbom.mjs'

const [installer, portable, sbomPath] = process.argv.slice(2)
if (!installer || !portable || !sbomPath) {
  console.error('usage: node scripts/check-installer-sbom.mjs <installer> <portable-root> <sbom>')
  process.exit(1)
}

const errors = []
let bom
try {
  bom = JSON.parse(fs.readFileSync(path.resolve(sbomPath), 'utf8'))
} catch (error) {
  console.error(`installer SBOM is unreadable: ${error instanceof Error ? error.message : String(error)}`)
  process.exit(1)
}
const inventory = await collectInstallerInventory(installer, portable)
const properties = Object.fromEntries((bom.metadata?.component?.properties ?? []).map(item => [item.name, item.value]))
const installerHash = (bom.metadata?.component?.hashes ?? []).find(item => item.alg === 'SHA-256')?.content

if (bom.bomFormat !== 'CycloneDX') errors.push('bomFormat must be CycloneDX')
if (bom.specVersion !== '1.7') errors.push('specVersion must be 1.7')
if (bom.$schema !== 'http://cyclonedx.org/schema/bom-1.7.schema.json') errors.push('CycloneDX 1.7 schema URI is missing')
if (bom.metadata?.component?.name !== 'Point IDE Installer') errors.push('metadata.component must describe Point IDE Installer')
if (bom.metadata?.component?.version !== inventory.productVersion) errors.push('installer SBOM product version does not match the packaged app')
if (installerHash !== inventory.installerSha256) errors.push('installer hash does not match the release installer')
if (properties['point:installer:sha256'] !== inventory.installerSha256) errors.push('installer SHA-256 property does not match')
if (properties['point:installer:fileName'] !== inventory.installerName) errors.push('installer file name does not match')
if (properties['point:installer:size'] !== String(inventory.installerSize)) errors.push('installer size does not match')

const components = Array.isArray(bom.components) ? bom.components : []
const refs = new Set()
for (const component of components) {
  if (!component['bom-ref']) errors.push(`component ${component.name ?? '<unnamed>'} has no bom-ref`)
  else if (refs.has(component['bom-ref'])) errors.push(`duplicate bom-ref ${component['bom-ref']}`)
  refs.add(component['bom-ref'])
}
const componentCount = inventory.packages.length + inventory.files.length
const libraryCount = inventory.packages.filter(item => item.type === 'library').length
if (components.length !== componentCount) errors.push(`component count ${components.length} does not match packaged inventory ${componentCount}`)
if (componentCount < 100) errors.push(`packaged inventory is unexpectedly small: ${componentCount}`)
if (libraryCount < 1) errors.push('transitive packaged library inventory is empty')
if (properties['point:portable:componentCount'] !== String(componentCount)) errors.push('component count property does not match')
if (properties['point:portable:extensionCount'] !== String(inventory.extensionCount)) errors.push('extension count property does not match')
if (properties['point:portable:libraryCount'] !== String(libraryCount)) errors.push('library count property does not match')

const actualPackages = new Map(inventory.packages.map(item => [`${item.type}\0${item.name}\0${item.version}`, [...item.paths].sort().join(';')]))
const sbomPackages = new Map()
for (const component of components.filter(item => item.type === 'application' || item.type === 'library')) {
  const key = `${component.type}\0${component.name}\0${component.version}`
  const installedPaths = (component.properties ?? []).find(item => item.name === 'point:installedPaths')?.value
  if (sbomPackages.has(key)) errors.push(`duplicate package component ${component.name}@${component.version}`)
  sbomPackages.set(key, installedPaths)
}
for (const [key, installedPaths] of actualPackages) {
  if (!sbomPackages.has(key)) errors.push(`missing packaged component ${key.replaceAll('\0', ' ')}`)
  else if (sbomPackages.get(key) !== installedPaths) errors.push(`installed paths do not match for ${key.replaceAll('\0', ' ')}`)
}
for (const key of sbomPackages.keys()) {
  if (!actualPackages.has(key)) errors.push(`SBOM contains package absent from the portable build: ${key.replaceAll('\0', ' ')}`)
}

const sbomFiles = new Map(components.filter(item => item.type === 'file').map(item => [item.name, item]))
for (const file of inventory.files) {
  const component = sbomFiles.get(file.name)
  if (!component) errors.push(`missing key file component ${file.name}`)
  else if ((component.hashes ?? []).find(item => item.alg === 'SHA-256')?.content !== file.sha256) errors.push(`key file hash does not match: ${file.name}`)
}
for (const name of sbomFiles.keys()) {
  if (!inventory.files.some(item => item.name === name)) errors.push(`SBOM contains unexpected key file component ${name}`)
}

if (errors.length) {
  console.error(errors.join('\n'))
  process.exit(1)
}
console.log(`Installer SBOM verified: ${componentCount} components (${libraryCount} libraries, ${inventory.extensionCount} extensions)`)
