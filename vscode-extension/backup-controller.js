const path = require('path')
const { spawn } = require('child_process')

const CONFIRM_RESTORE = 'Создать recovery point и восстановить'
const SNAPSHOT_ID = /^point-\d{8}T\d{6}\.\d{9}Z-[a-z0-9-]+\.db$/

async function restoreSystemBackup({
  service, window, processIsAlive, refresh, loadStatisticsSnapshot, post,
  runTool = runPointDatabaseTool,
}) {
  const snapshots = await service.request('/api/system/backups')
  if (!Array.isArray(snapshots) || snapshots.length === 0) {
    void window.showInformationMessage('Проверенных snapshot для восстановления пока нет.')
    return { status: 'empty' }
  }
  const choice = await window.showQuickPick(snapshots.map(snapshot => ({
    label: new Date(snapshot.createdAt).toLocaleString('ru-RU'),
    description: String(snapshot.reason || 'backup'),
    detail: `${(Number(snapshot.sizeBytes || 0) / 1024 / 1024).toFixed(1)} MB · SHA-256 ${String(snapshot.sha256 || '').slice(0, 20)}…`,
    snapshot,
  })), { title: 'Восстановление SQLite', placeHolder: 'Выберите проверенный snapshot' })
  if (!choice) return { status: 'cancelled' }
  const confirmation = await window.showWarningMessage(
    `Восстановить состояние Point на ${new Date(choice.snapshot.createdAt).toLocaleString('ru-RU')}? Активные Runs будут прерваны; перед остановкой будет создан дополнительный recovery point.`,
    { modal: true }, CONFIRM_RESTORE,
  )
  if (confirmation !== CONFIRM_RESTORE) return { status: 'cancelled' }
  if (service.otherLiveLeaseCount() > 0) {
    throw new Error('Восстановление заблокировано: Point Core используют другие окна. Закройте их и повторите операцию.')
  }
  await service.request('/api/system/backups', {
    method: 'POST', body: JSON.stringify({ reason: 'recovery-point' }),
  })
  const current = await service.request('/api/system/backups')
  const selected = Array.isArray(current) && current.find(snapshot => snapshot.id === choice.snapshot.id && snapshot.sha256 === choice.snapshot.sha256)
  if (!selected) throw new Error('Выбранный snapshot больше не входит в retention. База не изменялась; выберите snapshot заново.')
  const id = String(selected.id || '')
  if (path.basename(id) !== id || !SNAPSHOT_ID.test(id)) {
    throw new Error('Ядро вернуло небезопасный идентификатор snapshot. Восстановление отменено.')
  }
  const tool = service.databaseToolPath()
  const backupPath = path.join(service.dataDirPath, 'backups', id)
  const databasePath = path.join(service.dataDirPath, 'workbench.db')
  const corePID = service.corePid()
  await service.stop()
  let report
  let restoreError
  try {
    if (corePID && processIsAlive(corePID)) throw new Error('Point Core не остановлен полностью; восстановление живой базы запрещено.')
    report = await runTool(tool, ['restore', '--backup', backupPath, '--db', databasePath, '--confirm-offline'])
    if (report?.backup?.sha256 !== selected.sha256 || report?.backup?.integrity !== 'ok' || report?.restored?.integrity !== 'ok') {
      throw new Error('Database recovery tool не подтвердил SHA-256 и целостность восстановленной базы.')
    }
  } catch (error) {
    restoreError = error
  }
  try {
    await service.start()
    await refresh()
  } catch (error) {
    if (!restoreError) restoreError = error
    else service.hostLog('error', `[restore] restart failed: ${error?.message || error}`)
  }
  if (restoreError) throw restoreError
  const statistics = await loadStatisticsSnapshot()
  post({ type: 'statistics', statistics })
  void window.showInformationMessage('SQLite восстановлена и проверена; Point Core снова запущен.')
  return { status: 'restored', id, report }
}

function runPointDatabaseTool(binary, args, timeoutMs = 120_000) {
  return new Promise((resolve, reject) => {
    const child = spawn(binary, args, { windowsHide: true, shell: false, stdio: ['ignore', 'pipe', 'pipe'] })
    const stdout = []
    const stderr = []
    let size = 0
    let settled = false
    const finish = callback => value => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      callback(value)
    }
    const timer = setTimeout(() => {
      if (settled) return
      settled = true
      child.kill()
      reject(new Error('Database recovery tool не завершился за 120 секунд.'))
    }, timeoutMs)
    const collect = target => chunk => {
      size += chunk.length
      if (size > 1024 * 1024) {
        child.kill()
        finish(reject)(new Error('Ответ database recovery tool превысил безопасный предел.'))
        return
      }
      target.push(chunk)
    }
    child.stdout.on('data', collect(stdout))
    child.stderr.on('data', collect(stderr))
    child.once('error', finish(reject))
    child.once('exit', code => {
      if (settled) return
      if (code !== 0) {
        finish(reject)(new Error(Buffer.concat(stderr).toString('utf8').trim() || `Database recovery tool завершился с кодом ${code}`))
        return
      }
      try {
        finish(resolve)(JSON.parse(Buffer.concat(stdout).toString('utf8')))
      } catch {
        finish(reject)(new Error('Database recovery tool вернул некорректный отчёт.'))
      }
    })
  })
}

module.exports = { restoreSystemBackup, runPointDatabaseTool }
