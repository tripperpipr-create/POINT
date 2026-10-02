import fs from 'node:fs'
import path from 'node:path'
import { createHash } from 'node:crypto'
import { spawnSync } from 'node:child_process'

const root = path.resolve(import.meta.dirname, '..')
const output = path.join(root, 'vscode-extension/bin')
fs.mkdirSync(output, { recursive: true })
const binaries = {}
for (const arch of ['amd64', 'arm64']) {
  const name = `point-sandboxd-linux-${arch}`
  const result = spawnSync('go', ['build', '-trimpath', '-ldflags', '-s -w', '-o', path.join(output, name), './cmd/point-sandboxd'], {
    cwd: root, env: { ...process.env, GOOS: 'linux', GOARCH: arch, CGO_ENABLED: '0' }, stdio: 'inherit', windowsHide: true,
  })
  if (result.error) throw result.error
  if (result.status !== 0) process.exit(result.status ?? 1)
  binaries[name] = `sha256:${createHash('sha256').update(fs.readFileSync(path.join(output, name))).digest('hex')}`
}
fs.writeFileSync(path.join(output, 'point-sandboxd-manifest.json'), JSON.stringify({ protocol: 1, fileRules: 'portable-v2', binaries }, null, 2) + '\n')
console.log('static Linux sandbox helpers built for amd64 and arm64')
