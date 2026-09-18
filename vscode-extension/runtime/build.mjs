import fs from 'node:fs'
import { builtinModules } from 'node:module'
import path from 'node:path'
import { build } from 'esbuild'

const extensionRoot = path.resolve(import.meta.dirname, '..')
const outputRoot = path.join(extensionRoot, 'dist')
const sdkSource = path.join(extensionRoot, 'node_modules', '@cursor', 'sdk')
const sdkCjsSource = path.join(sdkSource, 'dist', 'cjs')
const sdkOutput = path.join(outputRoot, 'cursor-sdk')
const stubs = {
  '@bufbuild/protobuf': 'protobuf',
  '@connectrpc/connect': 'connect',
  '@connectrpc/connect-node': 'connectNode',
  '@connectrpc/connect-web': 'connectWeb',
  zod: 'zod',
}

function assertInside(root, target) {
  const relative = path.relative(root, target)
  if (!relative || relative === '..' || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative)) {
    throw new Error(`Unsafe generated runtime path: ${target}`)
  }
}

if (!fs.existsSync(path.join(sdkCjsSource, 'index.js'))) {
  throw new Error('Cursor SDK build input is missing. Run npm install first.')
}
assertInside(extensionRoot, outputRoot)
fs.rmSync(outputRoot, { recursive: true, force: true })
fs.mkdirSync(sdkOutput, { recursive: true })

await build({
  entryPoints: [path.join(import.meta.dirname, 'cursor-deps-entry.cjs')],
  outfile: path.join(outputRoot, 'cursor-deps.cjs'),
  bundle: true,
  platform: 'node',
  format: 'cjs',
  target: 'node22',
  minify: true,
  legalComments: 'eof',
  alias: {
    undici: path.join(import.meta.dirname, 'undici-node22-shim.cjs'),
  },
})

for (const entry of fs.readdirSync(sdkCjsSource, { withFileTypes: true })) {
  if (!entry.isFile()) continue
  if (entry.name === 'sqlite.js') continue
  if (entry.name !== 'index.js' && entry.name !== 'package.json' && !/^\d+\.js(?:\.LICENSE\.txt)?$/.test(entry.name)) continue
  fs.copyFileSync(path.join(sdkCjsSource, entry.name), path.join(sdkOutput, entry.name))
}
fs.copyFileSync(path.join(sdkSource, 'LICENSE.md'), path.join(sdkOutput, 'LICENSE.md'))

// Cursor's Webpack output declares Node externals as `exports=require(...)`.
// Fail the build when a future pinned SDK adds a package that our compact
// runtime does not provide; discovering that only when a dynamic chunk opens
// would turn a packaging optimisation into a user-facing agent failure.
const builtins = new Set([...builtinModules, ...builtinModules.map(name => `node:${name}`)])
const ignoredOptionalRuntimes = new Set(['bun:sqlite', 'node:sqlite'])
const externalPackages = new Set()
for (const entry of fs.readdirSync(sdkOutput, { withFileTypes: true })) {
  if (!entry.isFile() || !entry.name.endsWith('.js')) continue
  const source = fs.readFileSync(path.join(sdkOutput, entry.name), 'utf8')
  for (const match of source.matchAll(/\.exports=require\("([^"]+)"\)/g)) {
    const request = match[1]
    if (!builtins.has(request) && !ignoredOptionalRuntimes.has(request)) externalPackages.add(request)
  }
}
const unsupported = [...externalPackages].filter(name => !Object.hasOwn(stubs, name)).sort()
if (unsupported.length) throw new Error(`Cursor SDK introduced unsupported runtime dependencies: ${unsupported.join(', ')}`)

for (const [packageName, exportName] of Object.entries(stubs)) {
  const packageRoot = path.join(outputRoot, 'node_modules', ...packageName.split('/'))
  fs.mkdirSync(packageRoot, { recursive: true })
  fs.writeFileSync(path.join(packageRoot, 'package.json'), `${JSON.stringify({ name: packageName, private: true, main: 'index.cjs' }, null, 2)}\n`)
  fs.writeFileSync(path.join(packageRoot, 'index.cjs'), `'use strict'\nmodule.exports = require(${JSON.stringify(path.relative(packageRoot, path.join(outputRoot, 'cursor-deps.cjs')).replaceAll('\\', '/'))}).${exportName}\n`)
}

const files = fs.readdirSync(sdkOutput).length
const bundledBytes = fs.statSync(path.join(outputRoot, 'cursor-deps.cjs')).size
console.log(JSON.stringify({ runtime: 'built', cursorSdkFiles: files, dependencyBundleBytes: bundledBytes }))
