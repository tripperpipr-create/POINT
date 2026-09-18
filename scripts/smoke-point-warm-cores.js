// Уборщик тёплых ядер.
//
// Переключение мира отпускает ядро прошлого проекта, а не гасит его: возврат
// тогда стоит одной проверки здоровья вместо холодного старта. Плата —
// процессы, за которые никто не отвечает, и правило, по которому их убирают,
// проверяется здесь, а не глазами в диспетчере задач.
//
// Инвариант один и он важнее потолка: дескриптор со свежей арендой не трогаем
// никогда — аренду держит живое окно IDE, и его ядро не бесхозное.
const assert = require('assert')
const fs = require('fs')
const os = require('os')
const path = require('path')

const { listRuntimeDescriptors, liveLeaseCount, rememberWarmCore, coreStateByKey, reapWarmCores } =
  require(path.resolve(__dirname, '..', 'vscode-extension', 'core-warm-pool.js'))

const key = index => String(index).repeat(24).slice(0, 24)
const LEASED = key(1)
const NEWEST = key(2)
const OLDER = key(3)
const OLDEST = key(4)
const DEAD = key(5)

// Живой pid без выдумок: этот процесс точно жив. «Мёртвый» берётся заведомо
// недостижимым, чтобы проверка не зависела от того, что запущено в системе.
const ALIVE = process.pid
const GONE = 0x7ffffff

async function main() {
  const runtimeDir = fs.mkdtempSync(path.join(os.tmpdir(), 'point-warm-cores-'))
  const writeDescriptor = (workspaceKey, pid, workspaceRoot) => {
    fs.writeFileSync(path.join(runtimeDir, `core-${workspaceKey}.json`), JSON.stringify({
      version: 1, pid, baseUrl: 'http://127.0.0.1:41000', workspaceRoot, startedAt: new Date().toISOString(),
    }))
  }
  const writeLease = (workspaceKey, pid, updatedAt) => {
    fs.writeFileSync(path.join(runtimeDir, `lease-${workspaceKey}-${Math.random().toString(36).slice(2)}.json`),
      JSON.stringify({ pid, updatedAt }))
  }

  writeDescriptor(LEASED, ALIVE, 'c:/worlds/leased')
  writeDescriptor(NEWEST, ALIVE, 'c:/worlds/newest')
  writeDescriptor(OLDER, ALIVE, 'c:/worlds/older')
  writeDescriptor(OLDEST, ALIVE, 'c:/worlds/oldest')
  writeDescriptor(DEAD, GONE, 'c:/worlds/dead')

  // Окно IDE держит своё ядро: аренда свежая, владелец жив.
  writeLease(LEASED, ALIVE, Date.now())
  // Протухшая аренда — владельца давно нет; такой файл уборщик снимает сам.
  writeLease(OLDEST, ALIVE, Date.now() - 120_000)

  assert.equal(listRuntimeDescriptors(runtimeDir).length, 5, 'дескрипторы не прочитались')
  assert.equal(liveLeaseCount(runtimeDir, LEASED), 1, 'свежая аренда не засчитана')
  assert.equal(liveLeaseCount(runtimeDir, OLDEST), 0, 'протухшая аренда засчитана как живая')
  assert.ok(!fs.readdirSync(runtimeDir).some(name => name.startsWith(`lease-${OLDEST}-`)),
    'протухшая аренда осталась на диске')

  assert.equal(coreStateByKey(runtimeDir, LEASED), 'running', 'арендованное ядро не опознано как рабочее')
  assert.equal(coreStateByKey(runtimeDir, NEWEST), 'warm', 'бесхозное живое ядро не опознано как тёплое')
  assert.equal(coreStateByKey(runtimeDir, DEAD), 'idle', 'мёртвое ядро выдано за живое')

  // Кольцо: свежайшее первым, без повторов, длиной не больше потолка.
  let ring = []
  ring = rememberWarmCore(ring, { key: OLDEST, path: 'c:/worlds/oldest', pid: ALIVE }, 2)
  ring = rememberWarmCore(ring, { key: OLDER, path: 'c:/worlds/older', pid: ALIVE }, 3)
  ring = rememberWarmCore(ring, { key: NEWEST, path: 'c:/worlds/newest', pid: ALIVE }, 3)
  assert.deepEqual(ring.map(item => item.key), [NEWEST, OLDER, OLDEST], 'кольцо не держит порядок «свежайшее первым»')
  assert.deepEqual(rememberWarmCore(ring, { key: OLDER, path: 'c:/worlds/older', pid: ALIVE }, 3).map(item => item.key),
    [OLDER, NEWEST, OLDEST], 'повторное касание не подняло мир наверх')

  const stopped = []
  const result = await reapWarmCores({
    runtimeDir,
    ring,
    keep: 2,
    forceStopPid: async pid => { stopped.push(pid) },
  })

  // Убит ровно один — самый старый бесхозный. Арендованный жив, два свежих
  // остались тёплыми, мёртвый снят без убийства.
  assert.deepEqual(stopped, [ALIVE], 'убит не ровно один бесхозный процесс')
  assert.deepEqual(result.ring.map(item => item.key).sort(), [NEWEST, OLDER].sort(), 'тёплыми оставлены не те миры')
  const left = fs.readdirSync(runtimeDir).filter(name => name.startsWith('core-')).sort()
  assert.deepEqual(left, [`core-${LEASED}.json`, `core-${NEWEST}.json`, `core-${OLDER}.json`].sort(),
    'на диске остались не те дескрипторы')

  // Незаконченный ход мастера бронирует ядро. Формально оно бесхозно — аренду
  // сняли при переключении сами, — и без брони уборка оборвала бы ответ,
  // который человек ждёт в другом мире.
  // Свой каталог: предыдущая уборка уже снесла часть дескрипторов, и мерить
  // бронь на её остатках значит мерить не то.
  const busyDir = fs.mkdtempSync(path.join(os.tmpdir(), 'point-warm-busy-'))
  fs.writeFileSync(path.join(busyDir, `core-${OLDEST}.json`), JSON.stringify({
    version: 1, pid: ALIVE, baseUrl: 'http://127.0.0.1:41001', workspaceRoot: 'c:/worlds/oldest', startedAt: new Date().toISOString(),
  }))
  const busyStopped = []
  const busyRing = rememberWarmCore([], { key: OLDEST, path: 'c:/worlds/oldest', pid: ALIVE, busyUntil: Date.now() + 60_000 }, 2)
  const busyResult = await reapWarmCores({ runtimeDir: busyDir, ring: busyRing, keep: 0, forceStopPid: async pid => { busyStopped.push(pid) } })
  assert.deepEqual(busyStopped, [], 'забронированное ядро убито вместе с незаконченным ходом')
  assert.equal(busyResult.held, 1, 'бронь не учтена в отчёте уборщика')
  assert.ok(fs.existsSync(path.join(busyDir, `core-${OLDEST}.json`)), 'дескриптор забронированного ядра снят')
  // Протухшая бронь не спасает: иначе одна забытая запись держала бы ядро вечно.
  const staleStopped = []
  const staleRing = rememberWarmCore([], { key: OLDEST, path: 'c:/worlds/oldest', pid: ALIVE, busyUntil: Date.now() - 1000 }, 2)
  const staleResult = await reapWarmCores({ runtimeDir: busyDir, ring: staleRing, keep: 0, forceStopPid: async pid => { staleStopped.push(pid) } })
  assert.equal(staleResult.held, 0, 'протухшая бронь всё ещё держит ядро')
  assert.deepEqual(staleStopped, [ALIVE], 'ядро с протухшей бронью не убрано')
  fs.rmSync(busyDir, { recursive: true, force: true })

  // Закрытие Чертога: тёплых не остаётся, арендованное окном IDE ядро — цело.
  const closing = await reapWarmCores({ runtimeDir, ring: result.ring, keep: 0, forceStopPid: async () => {} })
  assert.deepEqual(closing.ring, [], 'после закрытия Чертога кольцо не опустело')
  const survivors = fs.readdirSync(runtimeDir).filter(name => name.startsWith('core-'))
  assert.deepEqual(survivors, [`core-${LEASED}.json`], 'уборщик тронул ядро, арендованное окном IDE')

  fs.rmSync(runtimeDir, { recursive: true, force: true })
  process.stdout.write(JSON.stringify({
    leasedKept: true, warmKept: 2, stopped: stopped.length, deadDescriptorsRemoved: 1, staleLeasesRemoved: 1,
  }) + '\n')
}

main().catch(error => {
  console.error(error)
  process.exitCode = 1
})
