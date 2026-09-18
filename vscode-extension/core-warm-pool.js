// Тёплые ядра между проектами.
//
// Переключение мира в Чертоге не гасит прошлое ядро, а отпускает его
// (`BackendService.detach`): дескриптор и процесс остаются, и возврат к проекту
// стоит одной проверки `/api/health` вместо холодного старта. Плата за это —
// процессы, которых никто не держит: `detach()` зовётся и при закрытии любого
// окна (`deactivate`), а убрать бесхозное ядро до сих пор было некому. Здесь
// живёт и политика («сколько тёплых ядер терпим»), и уборка этой давней течи.
//
// Инвариант ровно один: дескриптор со свежей арендой не трогаем никогда. Аренду
// держит живое окно IDE, и его ядро не бесхозное, сколько бы их ни набралось.
// Потолок считается только по бесхозным.
const fs = require('node:fs')
const path = require('node:path')
const { processIsAlive, readJsonFile, removeFileIfExists } = require('./extension-utils')

// Аренда переписывается раз в 5 с (`BackendService.startLease`). Двадцати секунд
// хватает, чтобы пережить паузу занятого окна и не считать живым то, что умерло.
const LEASE_FRESH_MS = 20_000

function descriptorKey(name) {
  return /^core-([0-9a-f]{24})\.json$/i.exec(name)?.[1]
}

// Сколько окон держат ядро этого мира. Протухшие аренды снимаем по дороге: их
// владельцы уже не вернутся, а файл иначе живёт вечно.
function liveLeaseCount(runtimeDir, workspaceKey) {
  const prefix = `lease-${workspaceKey}-`
  let count = 0
  let names = []
  try { names = fs.readdirSync(runtimeDir) } catch { return 0 }
  for (const name of names) {
    if (!name.startsWith(prefix) || !name.endsWith('.json')) continue
    const file = path.join(runtimeDir, name)
    const lease = readJsonFile(file)
    const fresh = Date.now() - Number(lease?.updatedAt || 0) < LEASE_FRESH_MS
    if (fresh && processIsAlive(lease?.pid)) count += 1
    else removeFileIfExists(file)
  }
  return count
}

function listRuntimeDescriptors(runtimeDir) {
  let names = []
  try { names = fs.readdirSync(runtimeDir) } catch { return [] }
  const descriptors = []
  for (const name of names) {
    const key = descriptorKey(name)
    if (!key) continue
    const file = path.join(runtimeDir, name)
    const descriptor = readJsonFile(file)
    if (!descriptor) {
      removeFileIfExists(file)
      continue
    }
    descriptors.push({
      key,
      file,
      pid: Number(descriptor.pid) || 0,
      baseUrl: String(descriptor.baseUrl || ''),
      workspaceRoot: String(descriptor.workspaceRoot || ''),
      startedAt: String(descriptor.startedAt || ''),
    })
  }
  return descriptors
}

// Кольцо тёплых ядер: свежайшее первым, без повторов. Живёт в globalState, а не
// в памяти, — перезагрузка Чертога не должна терять список к уборке.
function rememberWarmCore(ring, entry, keep = 2) {
  const source = Array.isArray(ring) ? ring : []
  if (!entry?.key) return source.slice(0, Math.max(0, keep))
  const rest = source.filter(item => item?.key && item.key !== entry.key)
  // `busyUntil` — бронь для ядра, в котором остался незаконченный ход мастера.
  // Формально такое ядро бесхозно (аренду мы сняли сами), и третье переключение
  // снесло бы его вместе с ответом, который человек ждёт: ход дописался бы в
  // статус «interrupted». Бронь недолгая и не спасает от закрытия Чертога —
  // она закрывает ровно окно между переключениями.
  const busyUntil = Number(entry.busyUntil) || 0
  return [{ key: entry.key, path: entry.path || '', pid: Number(entry.pid) || 0, busyUntil, since: Date.now() }, ...rest]
    .slice(0, Math.max(0, keep))
}

// Состояние ядра проекта для галереи — без единого запроса по HTTP.
function coreStateByKey(runtimeDir, workspaceKey) {
  const descriptor = listRuntimeDescriptors(runtimeDir).find(item => item.key === workspaceKey)
  if (!descriptor || !processIsAlive(descriptor.pid)) return 'idle'
  return liveLeaseCount(runtimeDir, workspaceKey) > 0 ? 'running' : 'warm'
}

// Уборка. Порядок важен: файл дескриптора снимается ДО убийства процесса, иначе
// соседнее окно успеет подцепиться (`tryAttachSharedCore`) к умирающему ядру и
// получит мёртвый порт вместо честного холодного старта.
async function reapWarmCores({ runtimeDir, ring, keep = 2, forceStopPid, hostLog, awaitStop = true }) {
  const descriptors = listRuntimeDescriptors(runtimeDir)
  const unreferenced = []
  for (const descriptor of descriptors) {
    if (!processIsAlive(descriptor.pid)) {
      removeFileIfExists(descriptor.file)
      continue
    }
    if (liveLeaseCount(runtimeDir, descriptor.key) > 0) continue
    unreferenced.push(descriptor)
  }
  const entries = Array.isArray(ring) ? ring : []
  const order = entries.map(item => String(item?.key || '')).filter(Boolean)
  const rank = new Map(order.map((key, index) => [key, index]))
  const busy = new Set(entries
    .filter(item => Number(item?.busyUntil) > Date.now())
    .map(item => String(item?.key || '')))
  const kept = unreferenced
    .filter(descriptor => rank.has(descriptor.key))
    .sort((left, right) => rank.get(left.key) - rank.get(right.key))
    .slice(0, Math.max(0, keep))
  const keptKeys = new Set(kept.map(descriptor => descriptor.key))
  // Забронированное ядро переживает даже уборку под ноль: ход мастера, начатый
  // человеком, не должен обрываться оттого, что он ушёл смотреть другой мир.
  const doomed = unreferenced.filter(descriptor => !keptKeys.has(descriptor.key) && !busy.has(descriptor.key))
  const heldBusy = unreferenced.filter(descriptor => !keptKeys.has(descriptor.key) && busy.has(descriptor.key))
  for (const descriptor of doomed) removeFileIfExists(descriptor.file)
  const stop = typeof forceStopPid === 'function' ? forceStopPid : undefined
  if (stop) {
    // На закрытии окна ждать нельзя: бюджет `deactivate` короткий, а снятого
    // дескриптора уже хватает, чтобы следующее окно не подцепилось к ядру,
    // которое вот-вот умрёт.
    const kills = doomed.map(descriptor => Promise.resolve()
      .then(() => stop(descriptor.pid))
      .catch(() => { /* ядро могло уйти само */ }))
    if (awaitStop) await Promise.all(kills)
  }
  if (doomed.length && typeof hostLog === 'function') {
    hostLog('info', `[warm] освобождено ядер: ${doomed.length}; тёплыми оставлено ${kept.length}`)
  }
  const survived = [...kept, ...heldBusy]
  const busyUntilByKey = new Map(entries.map(item => [String(item?.key || ''), Number(item?.busyUntil) || 0]))
  return {
    ring: survived.map(descriptor => ({
      key: descriptor.key,
      path: descriptor.workspaceRoot,
      pid: descriptor.pid,
      busyUntil: busyUntilByKey.get(descriptor.key) || 0,
      since: Date.now(),
    })),
    stopped: doomed.length,
    held: heldBusy.length,
  }
}

module.exports = {
  LEASE_FRESH_MS,
  liveLeaseCount,
  listRuntimeDescriptors,
  rememberWarmCore,
  coreStateByKey,
  reapWarmCores,
}
