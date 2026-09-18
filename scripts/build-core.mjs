import fs from 'node:fs'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
const root = path.resolve(import.meta.dirname, '..')
const suffix = process.platform === 'win32' ? '.exe' : ''
fs.mkdirSync(path.join(root, 'vscode-extension/bin'), { recursive: true })
for (const [binary, target] of [['point-core', './cmd/server'], ['point-db', './cmd/point-db']]) {
  const result = spawnSync('go', ['build', '-trimpath', '-ldflags', '-s -w', '-o', 'vscode-extension/bin/' + binary + suffix, target], { cwd: root, stdio: 'inherit', windowsHide: true })
  if (result.error) throw result.error
  if (result.status !== 0) process.exit(result.status ?? 1)
}
console.log('core and database tool built from current sources')
