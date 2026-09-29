import { fillAttribute } from './format-units.js'

// Читает текущий снимок UI; выбор файла и статус запроса остаются в main.js.
export function createQuestHistoryViews({ getState, live, esc, shell, runIsActiveNow, vscode }) {
  // ── Боковая панель квеста ──────────────────────────────────────────────────
  // Сводит три вещи, которые до сих пор жили на разных экранах: что агент видел,
  // чем кончились прошлые попытки и кто сейчас в отряде. Без этого отладка агента
  // была гаданием — по одному экрану нельзя понять, почему он сделал глупость.

  function questContextBarsHtml(details) {
    const items = Array.isArray(details?.run?.contextItems) ? details.run.contextItems : []
    if (items.length === 0) {
      return `<p class="hall-note">Вложенного контекста нет — агент работает только с тем, что нашёл сам.</p>`
    }
    // Полоса показывает долю элемента в общем объёме вложений, а не абсолютный
    // размер: важно, что именно съело контекст, а не сколько там килобайт.
    const total = items.reduce((sum, item) => sum + Number(item.extractedSize || item.size || 0), 0) || 1
    return items.map(item => {
      const size = Number(item.extractedSize || item.size || 0)
      const percent = Math.max(2, Math.round((size / total) * 100))
      return `<div class="hall-context-item">
        <div class="hall-spread">
          <span class="hall-context-path">${esc(item.path || item.label || item.kind || 'вложение')}</span>
          <span class="hall-context-share">${percent}%</span>
        </div>
        <div class="hall-track"><i ${fillAttribute(percent)}></i></div>
      </div>`
    }).join('')
  }

  function questRunHistoryHtml(details) {
    const runs = (getState().boot?.runs || []).filter(run => run.id !== details?.run?.id).slice(0, 4)
    if (runs.length === 0) {
      return `<p class="hall-note">Прошлых прогонов нет.</p>`
    }
    const diagnosticsByRun = new Map((getState().boot?.runDiagnostics || []).map(item => [item.runId, item]))
    return runs.map(run => {
      const diagnostics = diagnosticsByRun.get(run.id)
      const verification = diagnostics?.verification
      // Метка честная: «с доказательством» только когда верификатор отработал.
      const mark = !verification?.required ? '·' : verification.recorded ? '✓' : '!'
      const proof = !verification?.required ? 'проверка не требовалась'
        : verification.recorded ? 'проверка зафиксирована' : 'без доказательства'
      return `<button type="button" class="hall-panel-row hall-item" data-action="load-run" data-id="${esc(run.id)}">
        <span class="hall-chip ${mark === '!' ? 'is-hot' : 'is-dim'}">${mark}</span>
        <div class="hall-item-text">
          <b>${esc(run.task || run.id)}</b>
          <small>${esc(proof)}</small>
        </div>
      </button>`
    }).join('')
  }

  function questAsideHtml(details) {
    if (!details?.run) return ''
    const task = details.run.task || ''
    return `<aside class="hall-quest-aside">
      <section>
        <header><b>Что агент видел</b></header>
        <div class="hall-aside-stack">
          ${questContextBarsHtml(details)}
          <button class="hall-btn is-sm" data-action="load-context-inspector" data-run-id="${esc(details.run.id)}">Разбор контекста</button>
          ${runIsActiveNow(details.run)
            ? `<form class="exec-inline-form" data-exec-form="forbid" data-run-id="${esc(details.run.id)}"><input type="text" placeholder="Запретить путь…"><button type="submit" class="small-button" title="Запретить файл">⊘</button></form>`
            : ''}
        </div>
      </section>
      <section>
        <header><b>История прогонов</b></header>
        <div>${questRunHistoryHtml(details)}</div>
        <div class="hall-aside-stack is-tight">
          <button class="hall-btn is-sm" data-action="tab" data-tab="history">Сравнить 2 последних</button>
          <button class="hall-btn is-sm is-dashed" data-action="repeat-quest" data-task="${esc(task)}">Повторить на другой модели</button>
        </div>
      </section>
    </aside>`
  }

  // ── История по файлу ───────────────────────────────────────────────────────
  // Хроника отвечает «что делал прогон #47». Человек спрашивает «что случилось с
  // engine.go». Тот же неизменяемый факт, повёрнутый вокруг файла.

  function filesTouchedByAgents() {
    const paths = new Set()
    for (const patch of getState().boot?.changes || []) if (patch.path) paths.add(patch.path)
    for (const set of getState().boot?.changeSets || []) {
      for (const item of set.items || []) if (item.path) paths.add(item.path)
    }
    return [...paths].sort()
  }

  function fileHistoryEntryHtml(entry) {
    const operations = { create: 'создан', modify: 'изменён', delete: 'удалён' }
    const statuses = { applied: 'применено', reverted: 'откачено', pending: 'ждёт', approved: 'одобрено', rejected: 'отклонено', conflict: 'конфликт', superseded: 'поглощено' }
    const origin = entry.source === 'patch'
      ? `прогон ${entry.runId || '—'}${entry.tool ? ` · ${esc(entry.tool)}` : ''}`
      : `набор ${entry.changeSetId}${entry.title ? ` · ${esc(entry.title)}` : ''}`
    return `<div class="hall-panel-row hall-item">
      <span class="hall-chip ${entry.status === 'reverted' ? 'is-dim' : ''}">${esc(operations[entry.operation] || entry.operation || '')}</span>
      <div class="hall-item-text">
        <b>${esc(statuses[entry.status] || entry.status || '')}</b>
        <small>${origin}</small>
      </div>
      ${entry.revertible
        ? `<button class="hall-btn is-sm" data-action="revert-file-entry" data-path="${esc(entry.revertPath)}" data-id="${esc(entry.id)}">Откатить</button>`
        : `<span class="hall-item-flag">необратимо</span>`}
    </div>`
  }

  function fileHistoryView() {
    const files = filesTouchedByAgents()
    if (live.fileHistoryStatus === 'idle' && !live.fileHistoryPath && files.length) {
      live.fileHistoryPath = files[0]
      live.fileHistoryStatus = 'loading'
      setTimeout(() => vscode.postMessage({ type: 'loadFileHistory', path: live.fileHistoryPath }), 0)
    }
    const entries = live.fileHistoryData?.entries || []
    return shell(`<div class="hall-split">
      <div class="hall-queue">
        <header>
          <b>Файлы · ${files.length}</b>
          <small>тронуты агентами</small>
        </header>
        <div class="hall-queue-list" data-keynav="column" aria-label="Файлы с историей правок">
          ${files.map(path => `<button class="hall-queue-item is-path ${path === live.fileHistoryPath ? 'is-active' : ''}" data-action="pick-file-history" data-path="${esc(path)}"${path === live.fileHistoryPath ? ' aria-current="true"' : ''}><span class="title">${esc(path)}</span></button>`).join('')
            || '<div class="hall-panel-row"><p class="hall-note">Агенты пока ничего не меняли.</p></div>'}
        </div>
      </div>
      <div class="hall-page">
        <div class="hall-title">
          <span class="kicker">История файла</span>
          <h1 class="is-mono">${esc(live.fileHistoryPath || '—')}</h1>
        </div>
        ${live.fileHistoryData ? `<div class="hall-tally">
          <span>всего ${live.fileHistoryData.total}</span><span>применено ${live.fileHistoryData.applied}</span><span>откачено ${live.fileHistoryData.reverted}</span><span>ждёт ${live.fileHistoryData.pending}</span>
        </div>` : ''}
        <section class="hall-panel">
          <header><b>Журнал правок</b><small>неизменяемая история</small></header>
          ${entries.length ? entries.map(fileHistoryEntryHtml).join('')
            : `<div class="hall-panel-row"><p class="hall-note">${live.fileHistoryStatus === 'loading' ? 'Спрашиваю ядро — правки этого файла ещё не пришли.' : live.fileHistoryStatus === 'error' ? 'Не удалось получить историю файла — правки неизвестны, а не отсутствуют.' : 'По этому файлу правок нет.'}</p></div>`}
        </section>
      </div>
    </div>`)
  }

  return { questAsideHtml, fileHistoryView }
}
