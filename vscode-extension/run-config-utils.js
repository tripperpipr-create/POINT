'use strict'

// Pure run-configuration discovery helpers. Host I/O stays in extension.js;
// this module only turns already-read manifests into deterministic commands.
function parseJsonc(raw) {
  const text = String(raw || '')
  try { return JSON.parse(text) } catch { /* comments or trailing commas */ }
  let out = ''
  let inString = false
  let quote = ''
  let escaped = false
  for (let i = 0; i < text.length; i += 1) {
    const ch = text[i]
    const next = text[i + 1]
    if (inString) {
      out += ch
      if (escaped) escaped = false
      else if (ch === '\\') escaped = true
      else if (ch === quote) inString = false
      continue
    }
    if (ch === '"' || ch === "'") {
      inString = true
      quote = ch
      out += ch
      continue
    }
    if (ch === '/' && next === '/') {
      while (i < text.length && text[i] !== '\n') i += 1
      continue
    }
    if (ch === '/' && next === '*') {
      i += 2
      while (i < text.length && !(text[i] === '*' && text[i + 1] === '/')) i += 1
      i += 1
      continue
    }
    out += ch
  }
  try { return JSON.parse(out.replace(/,\s*([}\]])/g, '$1')) } catch { return undefined }
}

function makefileTargets(raw) {
  const targets = []
  for (const line of String(raw || '').split(/\r?\n/)) {
    const match = /^([A-Za-z_][A-Za-z0-9._-]*)\s*:/.exec(line)
    if (!match || match[1] === 'PHONY' || targets.includes(match[1])) continue
    targets.push(match[1])
    if (targets.length >= 24) break
  }
  return targets
}

function shellQuote(value, platform = process.platform) {
  const text = String(value)
  if (!/[\s"'&|<>^]/.test(text)) return text
  return platform === 'win32' ? `"${text.replace(/"/g, '\\"')}"` : `'${text.replace(/'/g, `'\\''`)}'`
}

function runConfigId(kind, folder, name) {
  return `${kind}:${folder.uri?.fsPath || folder.name}:${name}`
}

function buildRunConfigurations(folder, manifests = {}) {
  const cwd = folder.uri?.fsPath || ''
  const folderName = folder.name || 'project'
  const items = []
  const launchConfigs = Array.isArray(manifests.launch?.configurations) ? manifests.launch.configurations : []
  for (const config of launchConfigs) {
    if (!config?.name) continue
    items.push({
      id: runConfigId('launch', folder, config.name), kind: 'launch', label: config.name, short: config.name,
      detail: [config.type, config.request, config.program].filter(Boolean).join(' · '), folderName, cwd,
      launchName: config.name, channel: 'Запуск',
    })
  }
  const tasks = Array.isArray(manifests.tasks?.tasks) ? manifests.tasks.tasks : []
  for (const task of tasks) {
    const label = task.label || task.taskName
    if (!label) continue
    const group = typeof task.group === 'string' ? task.group : task.group?.kind
    items.push({
      id: runConfigId('task', folder, label), kind: 'task', label, short: label,
      detail: [task.type, task.command, group].filter(Boolean).join(' · '), folderName, cwd,
      taskLabel: label, group, channel: group === 'test' ? 'Тесты' : 'Сборка',
    })
  }
  const scripts = manifests.packageJson?.scripts && typeof manifests.packageJson.scripts === 'object'
    ? Object.keys(manifests.packageJson.scripts) : []
  for (const script of scripts.slice(0, 40)) {
    items.push({
      id: runConfigId('npm', folder, script), kind: 'npm', label: `npm run ${script}`, short: script,
      detail: String(manifests.packageJson.scripts[script] || ''), folderName, cwd, script,
      command: `npm run ${shellQuote(script)}`,
      channel: /test|lint|spec/.test(script) ? 'Тесты' : /build|compile|pack/.test(script) ? 'Сборка' : 'Запуск',
    })
  }
  if (manifests.goMod !== undefined && manifests.goMod !== null) {
    for (const [name, label, command, channel] of [
      ['run', 'go run .', 'go run .', 'Запуск'], ['test', 'go test ./...', 'go test ./...', 'Тесты'],
      ['build', 'go build .', 'go build .', 'Сборка'], ['tidy', 'go mod tidy', 'go mod tidy', 'Сборка'],
    ]) {
      items.push({ id: runConfigId('go', folder, name), kind: 'go', label, short: label, detail: folderName, folderName, cwd, command, channel })
    }
  }
  for (const target of makefileTargets(manifests.makefile)) {
    items.push({
      id: runConfigId('make', folder, target), kind: 'make', label: `make ${target}`, short: target,
      detail: 'Makefile', folderName, cwd, command: `make ${shellQuote(target)}`,
      channel: /test|check|lint/.test(target) ? 'Тесты' : 'Сборка',
    })
  }
  if (typeof manifests.cargo === 'string' && /\[package\]/.test(manifests.cargo)) {
    for (const [name, label, command, channel] of [
      ['run', 'cargo run', 'cargo run', 'Запуск'], ['test', 'cargo test', 'cargo test', 'Тесты'],
      ['build', 'cargo build', 'cargo build', 'Сборка'],
    ]) {
      items.push({ id: runConfigId('cargo', folder, name), kind: 'cargo', label, short: label, detail: folderName, folderName, cwd, command, channel })
    }
  }
  if (typeof manifests.pyproject === 'string' && manifests.pyproject.trim()) {
    items.push({
      id: runConfigId('python', folder, 'pytest'), kind: 'python', label: 'python -m pytest', short: 'pytest',
      detail: 'pyproject.toml', folderName, cwd, command: 'python -m pytest', channel: 'Тесты',
    })
  }
  return items
}

function pickDefaultRunConfiguration(configs = []) {
  const prefer = [
    item => item.kind === 'launch', item => item.kind === 'task' && item.group === 'build',
    item => item.kind === 'npm' && ['start', 'dev', 'test'].includes(item.script),
    item => item.kind === 'go' && item.id.endsWith(':run'), item => item.kind === 'cargo' && item.id.endsWith(':run'),
    item => item.kind === 'make' && ['run', 'start', 'all', 'build'].includes(item.short),
  ]
  for (const test of prefer) {
    const hit = configs.find(test)
    if (hit) return hit
  }
  return configs[0]
}

module.exports = { parseJsonc, makefileTargets, shellQuote, runConfigId, buildRunConfigurations, pickDefaultRunConfiguration }
