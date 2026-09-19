// Аренды окон решают судьбу ядра.
//
// Общее тёплое ядро поднимает одно окно Point, остальные присоединяются по
// дескриптору, а живость каждого окна подтверждается файлом аренды с отметкой
// времени. Из этого же следует, кого можно гасить: ядро без чужих свежих
// аренд — бесхозное.
//
// Ошибиться тут можно в обе стороны, и обе дорого. Посчитать лишнюю аренду —
// бесхозные ядра никогда не гаснут и копятся процессами. Не посчитать живую —
// у соседнего окна убьют ядро посреди работы.
//
// До сегодняшнего дня у модуля не было ни одной проверки: ни Go-теста, ни
// смоука. Здесь настоящая файловая система во временном каталоге и поддельная
// служба — ровно та, что приходит первым доводом.
const assert = require('node:assert/strict')
const crypto = require('node:crypto')
const fs = require('node:fs')
const os = require('node:os')
const path = require('node:path')

const { createCoreLease } = require('../vscode-extension/core-lease')

const root = fs.mkdtempSync(path.join(os.tmpdir(), 'point-lease-'))
const alive = new Set([process.pid])

const lease = createCoreLease({
  fs,
  path,
  crypto,
  normalizedWorkspaceRoot: value => path.resolve(String(value || '')).replace(/[\/]+$/, ''),
  removeFileIfExists: file => { try { fs.rmSync(file, { force: true }) } catch { /* уже нет */ } },
  readJsonFile: file => { try { return JSON.parse(fs.readFileSync(file, 'utf8')) } catch { return undefined } },
  processIsAlive: pid => alive.has(Number(pid)),
})

const folder = at => ({ uri: { fsPath: at } })
const newService = (leaseId, dataDir = root) => {
  const service = { dataDirPath: dataDir, runtimeLeaseId: leaseId }
  service.runtimePaths = dir => lease.runtimePaths(service, dir)
  service.tryAttachSharedCore = async () => false
  return service
}

// Верхнеуровневый await в CommonJS недоступен, отсюда обёртка.
;(async () => {
  // ── Ключ мира ───────────────────────────────────────────────────────────────
  // Два окна одного проекта обязаны сойтись на одном дескрипторе, разных —
  // разойтись. Иначе либо не найдут друг друга, либо поделят чужое ядро.
  const first = newService('win-1')
  const second = newService('win-2')
  const alpha = lease.runtimePaths(first, folder(path.join(root, 'alpha')))
  const alphaAgain = lease.runtimePaths(second, folder(path.join(root, 'alpha')))
  const beta = lease.runtimePaths(newService('win-3'), folder(path.join(root, 'beta')))
  assert.equal(alpha.workspaceKey, alphaAgain.workspaceKey, 'один проект — один ключ мира')
  assert.notEqual(alpha.workspaceKey, beta.workspaceKey, 'разные проекты не должны делить дескриптор')
  assert.notEqual(first.runtimeLeasePath, second.runtimeLeasePath, 'у каждого окна своя аренда')

  // ── Замок запуска ───────────────────────────────────────────────────────────
  assert.equal(await lease.acquireRuntimeLock(first, folder(path.join(root, 'alpha'))), true, 'первое окно берёт замок')
  assert.equal(first.ownsRuntimeLock, true)
  assert.ok(fs.existsSync(alpha.lockPath), 'замок должен лежать файлом')

  // Замок умершего окна не держит ядро вечно: он отпускается по возрасту и
  // непроверяемому pid. Возраст подделываем, потому что ждать 35 секунд в
  // проверке нельзя.
  lease.releaseRuntimeLock(first)
  assert.ok(!fs.existsSync(alpha.lockPath), 'свой замок снимается')
  fs.writeFileSync(alpha.lockPath, JSON.stringify({ pid: 999_001, createdAt: new Date().toISOString() }))
  const old = new Date(Date.now() - 120_000)
  fs.utimesSync(alpha.lockPath, old, old)
  assert.equal(await lease.acquireRuntimeLock(second, folder(path.join(root, 'alpha'))), true,
    'замок мёртвого окна обязан отпускаться, иначе проект не запустится уже никогда')
  lease.releaseRuntimeLock(second)

  // ── Дескриптор ──────────────────────────────────────────────────────────────
  first.baseUrl = 'http://127.0.0.1:41234'
  first.process = { pid: process.pid }
  lease.writeRuntimeDescriptor(first, folder(path.join(root, 'alpha')))
  const descriptor = JSON.parse(fs.readFileSync(alpha.descriptorPath, 'utf8'))
  assert.equal(descriptor.pid, process.pid)
  assert.equal(descriptor.baseUrl, 'http://127.0.0.1:41234')
  assert.ok(!fs.readdirSync(path.dirname(alpha.descriptorPath)).some(name => name.endsWith('.tmp')),
    'запись дескриптора идёт через временное имя и не оставляет мусора')

  // Чужой pid дескриптор не трогает: иначе одно закрывающееся окно снесло бы
  // запись о ядре, которое подняло другое.
  lease.removeRuntimeDescriptorForPid(first, process.pid + 7)
  assert.ok(fs.existsSync(alpha.descriptorPath), 'дескриптор чужого ядра остаётся на месте')
  lease.removeRuntimeDescriptorForPid(first, process.pid)
  assert.ok(!fs.existsSync(alpha.descriptorPath), 'свой дескриптор снимается')

  // ── Счёт чужих аренд ────────────────────────────────────────────────────────
  lease.writeRuntimeDescriptor(first, folder(path.join(root, 'alpha')))
  lease.startLease(first)
  assert.ok(fs.existsSync(first.runtimeLeasePath), 'аренда пишется сразу, а не через пять секунд')
  assert.equal(lease.otherLiveLeaseCount(first), 0, 'собственная аренда чужой не считается')

  lease.startLease(second)
  assert.equal(lease.otherLiveLeaseCount(first), 1, 'живое соседнее окно обязано считаться — иначе у него убьют ядро')

  // Аренда мёртвого окна не считается и убирается с дороги.
  const deadPath = path.join(path.dirname(alpha.descriptorPath), `lease-${alpha.workspaceKey}-win-dead.json`)
  fs.writeFileSync(deadPath, JSON.stringify({ pid: 999_002, updatedAt: Date.now() }))
  assert.equal(lease.otherLiveLeaseCount(first), 1, 'аренда мёртвого pid не продлевает жизнь ядру')
  assert.ok(!fs.existsSync(deadPath), 'протухшая аренда убирается, а не копится')

  // Аренда живого, но застывшего окна тоже не в счёт: файл не обновлялся.
  const stalePath = path.join(path.dirname(alpha.descriptorPath), `lease-${alpha.workspaceKey}-win-stale.json`)
  fs.writeFileSync(stalePath, JSON.stringify({ pid: process.pid, updatedAt: Date.now() - 60_000 }))
  assert.equal(lease.otherLiveLeaseCount(first), 1, 'аренда старше двадцати секунд не считается живой')
  assert.ok(!fs.existsSync(stalePath))

  lease.releaseLease(second)
  assert.equal(lease.otherLiveLeaseCount(first), 0, 'закрытое окно перестаёт держать ядро')
  lease.releaseLease(first)
  assert.ok(!fs.existsSync(first.runtimeLeasePath), 'своя аренда снимается при закрытии')

  fs.rmSync(root, { recursive: true, force: true })
  console.log('smoke-core-lease: ok')
})().catch(error => { console.error(error); process.exitCode = 1 })