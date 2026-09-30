import { formatDateTime } from './format-units.js'
import { diffCountHtml, diffHtml, diffPathHtml, diffStats } from './diff-view.js'
import { icon } from './ui-icons.js'
// Ревью sandbox-изменений и журнал откатов — одна Git-adjacent поверхность.
// Renderer получает состояние и словари явно; действий с файлами здесь нет.
export function createChangeSetViews({
  getState,
  shell,
  escapeHtml,
  countOf,
  pendingChangeSets,
  changeSetStatusLabels,
  toolName,
  agentById,
  getStatusLabels,
}) {
  const esc = escapeHtml

  function changeSetConflictHtml(set) {
    if (set.status !== 'conflict') return ''
    const resolutions = set.resolutions || []
    const unresolvedPaths = new Set(resolutions.filter(item => item.strategy === 'unresolved').map(item => item.path))
    const resolvedPaths = new Set(resolutions.filter(item => item.strategy !== 'unresolved').map(item => item.path))
    const unresolved = (set.items || []).filter(item =>
      unresolvedPaths.size ? unresolvedPaths.has(item.path) : !resolvedPaths.has(item.path)
    )
    if (!unresolved.length) return '<p class="muted">Все конфликты разрешены — применение завершается.</p>'
    return `<div class="changeset-conflicts"><strong>Конфликты</strong>${unresolved.map(item => `<article class="conflict-row"><span>${esc(item.path)}</span><textarea class="conflict-manual-content" rows="4" placeholder="Полное содержимое файла для ручного разрешения"></textarea><div><button type="button" class="secondary" data-action="resolve-changeset" data-id="${esc(set.id)}" data-path="${esc(item.path)}" data-strategy="keep_ours">Оставить workspace</button><button type="button" class="secondary" data-action="resolve-changeset" data-id="${esc(set.id)}" data-path="${esc(item.path)}" data-strategy="keep_theirs">Взять sandbox</button><button type="button" class="secondary" data-action="resolve-changeset" data-id="${esc(set.id)}" data-path="${esc(item.path)}" data-strategy="manual">Применить вручную</button></div></article>`).join('')}</div>`
  }

  const CHANGE_KIND_LABELS = {
    add: 'добавлен', modify: 'правка', delete: 'удалён', kept: 'без изменений', rename: 'переименован',
  }

  function changeKindLabel(kind) {
    const key = String(kind || '').trim().toLowerCase()
    return CHANGE_KIND_LABELS[key] || key || 'правка'
  }


  // Строка файла — та же, что у файла в карточке квеста: значок, путь с тихой
  // папкой, «Новый файл» или счёт с полоской, раскрытие с diff. Открыть файл —
  // отдельная кнопка: путь внутри <summary> раскрывал бы и открывал сразу.
  function changeSetFileHtml(item, compact) {
    const diff = String(item.diff || '').trim()
    const kind = String(item.kind || '').trim().toLowerCase()
    const stats = diff
      ? diffStats(diff)
      : { known: Boolean(item.additions || item.deletions || kind === 'add' || kind === 'delete'), additions: Number(item.additions || 0), deletions: Number(item.deletions || 0), created: kind === 'add', deleted: kind === 'delete' }
    if (kind === 'add') stats.created = true
    if (kind === 'delete') stats.deleted = true
    const label = stats.created || stats.deleted ? '' : changeKindLabel(item.kind)
    const head = `<span class="diff-file-icon${stats.created ? ' is-new' : ''}">${icon(stats.created ? 'file-plus' : 'file-edit')}</span><span class="diff-file-path" title="${esc(item.path)}">${diffPathHtml(item.path, esc)}</span>${diffCountHtml(stats)}${label && label !== 'правка' ? `<small>${esc(label)}</small>` : '<small></small>'}<button type="button" class="diff-file-open" data-action="open-file" data-path="${esc(item.path)}" aria-label="Открыть ${esc(item.path)}" title="Открыть в редакторе">${icon('file')}</button>`
    if (!diff) return `<li class="diff-file"><div class="diff-file-head">${head}<span></span></div></li>`
    return `<li class="diff-file"><details ${compact ? '' : 'open'}><summary class="diff-file-head">${head}<span class="diff-file-chevron">${icon('chevron-right')}</span></summary>${diffHtml(diff, esc)}</details></li>`
  }


  // Перечень файлов набора. Раскрывашка над ним убрана: число файлов уже стоит
  // строкой выше, в подписи карточки, и второе «2 файла» прятало за собой сами
  // файлы. Дифф по-прежнему за раскрытием — он длинный, и разворачивать его
  // каждому набору незачем; убран только уровень, ничего не сообщавший.
  function changeSetFilesHtml(set, compact) {
    const items = Array.isArray(set.items) ? set.items : []
    if (!items.length) return ''
    const shown = compact ? items.slice(0, 3) : items.slice(0, 24)
    const more = compact && items.length > 3 ? `<li class="muted">ещё ${items.length - 3} · откройте все наборы</li>` : ''
    return `<ul class="diff-files">${shown.map(item => changeSetFileHtml(item, compact)).join('')}${more}</ul>`
  }

  function changeSetDependencyChain(set) {
    const byId = new Map((getState().boot?.changeSets || []).map(item => [item.id, item]))
    const ordered = []
    const visited = new Set()
    const visit = id => {
      if (!id || visited.has(id)) return
      visited.add(id)
      const dependency = byId.get(id)
      if (!dependency) {
        ordered.push({ id, status: 'missing', title: id })
        return
      }
      for (const parentId of dependency.dependsOn || []) visit(parentId)
      ordered.push(dependency)
    }
    for (const dependencyId of set.dependsOn || []) visit(dependencyId)
    return ordered
  }

  function changeSetGraphHtml(set) {
    const chain = changeSetDependencyChain(set)
    if (chain.length === 0) return ''
    const nodes = [...chain, set]
    const applied = { applied: 'применён', pending: 'ждёт', approved: 'одобрен', conflict: 'конфликт', rejected: 'отклонён', reverted: 'откачен', superseded: 'поглощён', missing: 'не найден' }
    // Граф — продолжение карточки над ним, а не отдельный раздел: он
    // описывает цепочку именно этого набора. Заголовок уровня секции делал
    // его похожим на самостоятельный блок между двумя наборами.
    return `<section class="hall-panel is-graph is-continuation">
      <header>
        <small>порядок применения · откат в обратном порядке</small>
      </header>
      <div class="hall-panel-row">
        <div class="hall-graph">${nodes.map((node, index) => `
          <div class="hall-graph-cell">
            <div class="hall-node ${node.status === 'applied' ? '' : 'is-pending'}">
              <span class="id">${esc(node.id)}</span>
              <span class="name">${esc(node.title || node.id)}</span>
              <span class="hall-meta">${esc(applied[node.status] || node.status || '')}</span>
            </div>
            ${index < nodes.length - 1 ? '<span class="hall-link"></span>' : ''}
          </div>`).join('')}</div>
        <p class="hall-note is-wide">${esc(set.id)} зависит от ${chain.map(item => item.id).join(', ')}: применить по одному нельзя — кнопка «Применить» собирает топологический порядок сама.</p>
      </div>
    </section>`
  }

  function changeSetCardHtml(set, compact) {
    const items = Array.isArray(set.items) ? set.items : []
    const dependencies = changeSetDependencyChain(set)
    const unresolvedDependencies = dependencies.filter(item => item.status !== 'applied')
    const dependencyNote = dependencies.length
      ? `<small class="changeset-lineage">Связанная цепочка: ${dependencies.length} предыдущ. этап${dependencies.length === 1 ? '' : 'а'}${unresolvedDependencies.length ? ` · ${unresolvedDependencies.length} ещё не применено` : ' · готово'}</small>`
      : ''
    const mergeNote = set.kind === 'merge'
      ? `<small class="changeset-lineage">Слияние параллельных веток${(set.supersedes || []).length ? ` · включает ${countOf((set.supersedes || []).length, 'набор', 'набора', 'наборов')}` : ''}</small>`
      : set.status === 'superseded' && set.supersededBy
        ? `<small class="changeset-lineage">Включён в агрегирующий набор ${esc(set.supersededBy)}</small>`
        : ''
    const actions = set.status === 'pending' || set.status === 'approved'
      ? `<button type="button" class="primary" data-action="${unresolvedDependencies.length ? 'apply-changeset-chain' : 'apply-changeset'}" data-id="${esc(set.id)}">${unresolvedDependencies.length ? `Применить цепочку · ${unresolvedDependencies.length + 1}` : 'Применить'}</button><button type="button" class="secondary" data-action="reject-changeset" data-id="${esc(set.id)}">Отклонить</button>`
      : set.status === 'conflict'
        ? `<button type="button" class="primary" data-action="${unresolvedDependencies.length ? 'apply-changeset-chain' : 'apply-changeset'}" data-id="${esc(set.id)}">${unresolvedDependencies.length ? `Применить цепочку · ${unresolvedDependencies.length + 1}` : 'Повторить применение'}</button>`
        : set.status === 'applied'
          ? `<button type="button" class="danger-button" data-action="revert-changeset" data-id="${esc(set.id)}">Откатить набор</button>`
          : ''
    return `<article class="changeset-card status-${esc(set.status)}"><header><strong>${esc(set.title || 'Набор изменений')}</strong><span>${esc(changeSetStatusLabels[set.status] || set.status)}</span></header><small>${countOf(items.length, 'файл', 'файла', 'файлов')} · ${formatDateTime(set.createdAt)}</small>${dependencyNote}${mergeNote}${changeSetConflictHtml(set)}${changeSetFilesHtml(set, compact)}<footer>${actions}</footer></article>`
  }

  function changeSetsView() {
    const sets = getState().boot?.changeSets || []
    const pending = pendingChangeSets().length
    return shell(`<main class="hub-changesets-page"><header class="changes-heading"><div><h1>Ревью перед применением</h1><p>Sandbox-изменения агентов. Применение записывает файлы в рабочую копию.</p></div><div><strong>${sets.length}</strong><small>всего</small><b>${pending}</b><small>на ревью</small></div></header>${sets.length ? sets.map(set => changeSetCardHtml(set, false) + changeSetGraphHtml(set)).join('') : `<div class="empty compact point-frame"><span class="empty-glyph">▣</span><h3>Наборов пока нет</h3><p>После изолированных запусков здесь появятся diff для ревью.</p><div class="empty-next"><button type="button" class="secondary" data-action="tab" data-tab="quests">К квестам →</button><button type="button" class="secondary" data-action="tab" data-tab="overview">← Обзор</button></div></div>`}</main>`)
  }

  function journalActionHtml(change) {
    const revert = change.status === 'applied'
      ? `<button type="button" class="danger-button" data-action="revert-patch" data-id="${esc(change.id)}">Откатить действие</button>`
      : ''
    const diff = String(change.diff || '').trim()
    const preview = diff ? `<details class="changeset-files"><summary>Посмотреть diff</summary>${diffHtml(diff, esc)}</details>` : ''
    return `<article class="journal-action status-${esc(change.status || '')}"><div><button type="button" data-action="open-file" data-path="${esc(change.path)}">${esc(change.path)}</button><small>${esc(change.sourceTool && change.sourceTool !== 'propose_patch' ? toolName(change.sourceTool) : 'diff агента')} · ${esc(change.status || '')}</small>${preview}</div>${revert}</article>`
  }

  function journalView() {
    const state = getState()
    const quests = state.boot?.quests || []
    const executions = state.boot?.executions || []
    const sets = state.boot?.changeSets || []
    const patches = state.boot?.changes || []
    const runStatusLabels = getStatusLabels()
    const questById = new Map(quests.map(item => [item.id, item]))
    const grouped = new Map()
    const push = (questId, execution) => {
      const key = questId || '_none'
      if (!grouped.has(key)) grouped.set(key, [])
      grouped.get(key).push(execution)
    }
    for (const execution of executions) push(execution.questId, execution)
    for (const quest of quests) {
      if (!grouped.has(quest.id)) grouped.set(quest.id, [])
    }
    const orphanPatches = patches.filter(item => !executions.some(execution => execution.runId && execution.runId === item.runId))
    if (orphanPatches.length) grouped.set('_files', [])
    const keys = [...grouped.keys()].sort((left, right) => {
      if (left === '_none') return 1
      if (right === '_none') return -1
      if (left === '_files') return 1
      if (right === '_files') return -1
      return String(questById.get(left)?.title || left).localeCompare(String(questById.get(right)?.title || right), 'ru')
    })
    const sections = keys.map(key => {
      const quest = questById.get(key)
      const title = key === '_none' ? 'Без квеста' : key === '_files' ? 'Файловые diff без execution' : (quest?.title || 'Квест')
      // Метка группы называла всё «КВЕСТОМ», включая группы «Без квеста» и
      // «Файловые diff»: заголовок и метка над ним противоречили друг другу.
      const kicker = key === '_none' ? 'Вне квеста' : key === '_files' ? 'Файловые diff' : 'Квест'
      const questRevert = quest && quest.status !== 'cancelled'
        ? `<button type="button" class="danger-button" data-action="revert-quest" data-id="${esc(quest.id)}">Откатить квест</button>`
        : ''
      // Мера группы вместо общего правила, которое повторялось в каждой:
      // сколько здесь запусков и правок, видно до раскрытия.
      const groupRuns = (grouped.get(key) || []).filter(Boolean)
      const groupPatchCount = key === '_files'
        ? orphanPatches.length
        : groupRuns.reduce((total, execution) =>
          total + patches.filter(item => execution.runId && item.runId === execution.runId).length, 0)
      // Пустая группа не подписывается «0 запусков»: под заголовком и так стоит
      // строка «Пока нет запусков и действий», и счётчик её только повторял.
      const measure = key === '_files'
        ? countOf(orphanPatches.length, 'правка', 'правки', 'правок')
        : [groupRuns.length ? countOf(groupRuns.length, 'запуск', 'запуска', 'запусков') : '',
          groupPatchCount ? countOf(groupPatchCount, 'правка', 'правки', 'правок') : ''].filter(Boolean).join(' · ')
      const note = measure ? `<small>${esc(measure)}</small>` : ''
      const body = groupRuns.map(execution => {
        const execSets = sets.filter(item => item.executionId === execution.id)
        const execPatches = patches.filter(item => execution.runId && item.runId === execution.runId)
        const flowBtn = execution.flowRunId && execution.flowNodeId
          ? `<button type="button" class="secondary" data-action="revert-flow-node" data-flow-run-id="${esc(execution.flowRunId)}" data-node-id="${esc(execution.flowNodeId)}">Откатить узел Flow</button>`
          : ''
        return `<article class="journal-execution"><header><div><strong>${esc(execution.task || 'Запуск')}</strong><small>${esc(agentById(execution.projectAgentId)?.name || execution.projectAgentId || 'агент')} · ${esc(runStatusLabels[execution.status] || execution.status || '')}</small></div><div>${execution.id ? `<button type="button" class="secondary" data-action="revert-execution" data-id="${esc(execution.id)}">Откатить запуск</button>` : ''}${flowBtn}</div></header>${execSets.map(item => changeSetCardHtml(item, true)).join('')}${execPatches.map(journalActionHtml).join('')}</article>`
      }).join('')
      const fileOnly = key === '_files' ? orphanPatches.map(journalActionHtml).join('') : ''
      return `<section class="journal-quest"><header><div><span>${kicker}</span><h2>${esc(title)}</h2>${note}</div>${questRevert}</header>${body || fileOnly || '<p class="muted">Пока нет запусков и действий.</p>'}</section>`
    }).join('')
    const empty = !keys.length ? '<div class="empty compact point-frame"><span class="empty-glyph">≡</span><h3>Журнал пуст</h3><p>После квестов и sandbox-изменений здесь появятся откаты уровня Quest → Execution → Action.</p><div class="empty-next"><button type="button" class="secondary" data-action="tab" data-tab="quests">Новый квест →</button><button type="button" class="secondary" data-action="tab" data-tab="overview">← Обзор</button></div></div>' : ''
    return shell(`<main class="hub-journal"><header class="changes-heading"><div><h1>Журнал правок</h1><p>Собственный журнал правок агентов, не Git. Откат действия, запуска, узла Flow или квеста. Откат квеста откатывает связанные запуски; отдельного отката Flow нет — откатывайте узел или квест.</p></div></header>${sections || empty}</main>`)
  }

  return {
    changeSetConflictHtml,
    changeSetFileHtml,
    changeSetFilesHtml,
    changeSetDependencyChain,
    changeSetGraphHtml,
    changeSetCardHtml,
    changeSetsView,
    journalActionHtml,
    journalView,
  }
}
