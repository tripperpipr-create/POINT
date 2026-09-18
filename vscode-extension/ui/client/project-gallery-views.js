// Галерея миров — домашний экран Чертога.
//
// Чертог открывается первым делом, ещё до того как выбран проект, поэтому у
// него должен быть экран, которому ядро не нужно вовсе: список миров приходит
// от хоста сообщением `projects`, а не из `/api/bootstrap`. Тот же экран
// доступен и с открытым проектом — это переключатель, а не заглушка «проект не
// выбран».
//
// Модуль отдельный намеренно: `main.js` давно упирается в свой бюджет строк, и
// расти ему здесь нечем. Оттуда сюда уходит только маршрут в `paint()`.
export function createProjectGalleryViews(dependencies) {
  const { ui, vscode, esc, countOf } = dependencies

  let galleryOpen = false
  let galleryQuery = ''
  // Список живёт рядом с остальным состоянием вида, но приходит своим
  // сообщением: он нужен раньше `boot`, и связывать его с ядром нельзя.
  let projects = []
  let activeProjectPath = ''
  let switchInfo = undefined

  function isProjectGalleryOpen() { return galleryOpen }
  function setProjectGalleryOpen(value) {
    galleryOpen = Boolean(value)
    if (!galleryOpen) galleryQuery = ''
  }
  function projectSwitchInfo() { return switchInfo }
  function setProjectSwitchInfo(value) { switchInfo = value }

  function receiveProjects(message) {
    projects = Array.isArray(message?.projects) ? message.projects : []
    activeProjectPath = String(message?.active || '')
  }

  // Путь мира по его отпечатку. Каталог чатов приходит от ядра без чужих путей,
  // и это единственный законный способ их узнать: список реестра уже прошёл
  // сверку с диском и с недавними оболочки.
  function projectPathByHash(hash) {
    if (!hash) return ''
    return projects.find(project => project.hash === hash)?.path || ''
  }

  function samePath(left, right) {
    return String(left || '').replace(/[\\/]+$/, '').toLowerCase() === String(right || '').replace(/[\\/]+$/, '').toLowerCase()
  }

  // Относительное время словами. Точная дата человеку здесь не нужна: он
  // выбирает мир, а не читает журнал.
  function whenLabel(value) {
    const stamp = Number(value || 0)
    if (!stamp) return 'ещё не открывали'
    const seconds = Math.max(0, Math.round((Date.now() - stamp) / 1000))
    if (seconds < 90) return 'только что'
    const minutes = Math.round(seconds / 60)
    if (minutes < 60) return `${countOf(minutes, 'минуту', 'минуты', 'минут')} назад`
    const hours = Math.round(minutes / 60)
    if (hours < 24) return `${countOf(hours, 'час', 'часа', 'часов')} назад`
    const days = Math.round(hours / 24)
    if (days < 30) return `${countOf(days, 'день', 'дня', 'дней')} назад`
    return 'давно'
  }

  function coreLabel(core) {
    if (core === 'running') return 'ЯДРО РАБОТАЕТ'
    if (core === 'warm') return 'ЯДРО ТЁПЛОЕ'
    return ''
  }

  function matches(project, query) {
    if (!query) return true
    const hay = `${project.name} ${project.parent} ${project.branch}`.toLowerCase()
    return hay.includes(query)
  }

  function visibleProjects() {
    const query = galleryQuery.trim().toLowerCase()
    return projects.filter(project => matches(project, query))
  }

  function rowHtml(project) {
    const active = samePath(project.path, activeProjectPath) || samePath(project.path, ui.state.workspacePath)
    const core = coreLabel(project.core)
    const marks = [
      project.branch ? `<em class="point-gallery-branch">${esc(project.branch)}</em>` : '',
      core ? `<em class="point-gallery-core is-${esc(project.core)}">${esc(core)}</em>` : '',
      project.slow ? '<em class="point-gallery-slow">ДИСК НЕ ОТВЕЧАЕТ</em>' : '',
      `<em class="point-gallery-when">${esc(whenLabel(project.lastOpenedAt))}</em>`,
    ].filter(Boolean).join('')
    return `<li class="point-gallery-row${active ? ' is-active' : ''}${project.pinned ? ' is-pinned' : ''}">
      <button type="button" class="point-gallery-card" data-action="gallery-open" data-path="${esc(project.path)}" title="${esc(project.path)}">
        <span class="point-gallery-name">${esc(project.name)}${active ? '<i class="point-gallery-dot" aria-hidden="true"></i>' : ''}</span>
        <span class="point-gallery-path">${esc(project.parent)}</span>
        <span class="point-gallery-marks">${marks}</span>
      </button>
      <span class="point-gallery-tools">
        <button type="button" class="point-gallery-tool" data-action="gallery-pin" data-path="${esc(project.path)}" data-pinned="${project.pinned ? '1' : '0'}" aria-pressed="${project.pinned ? 'true' : 'false'}" title="${project.pinned ? 'Открепить мир' : 'Закрепить мир вверху'}">★</button>
        <button type="button" class="point-gallery-tool" data-action="gallery-ide" data-path="${esc(project.path)}" title="Открыть мир в редакторе">⧉</button>
        <button type="button" class="point-gallery-tool" data-action="gallery-forget" data-path="${esc(project.path)}" title="Забыть мир — папка на диске останется">×</button>
      </span>
    </li>`
  }

  function emptyHtml() {
    if (projects.length) {
      return `<div class="point-gallery-empty"><strong>Ничего не нашлось</strong><p>По запросу «${esc(galleryQuery.trim())}» миров нет. Сотрите поиск или откройте папку.</p></div>`
    }
    return `<div class="point-gallery-empty"><strong>Миров пока нет</strong><p>Откройте локальную папку — она станет миром для компаньона и гильдии. Всё остальное появится после этого.</p></div>`
  }

  function projectGallery() {
    const list = visibleProjects()
    const hasProject = Boolean(ui.state.workspace)
    return `<div class="point-gallery">
      ${ui.transientError ? `<div class="error-banner"><span>!</span><p>${esc(ui.transientError)}</p><button data-action="dismiss-error">×</button></div>` : ''}
      <header class="point-gallery-head">
        <div class="point-gallery-brand">
          <span class="point-gallery-mark" aria-hidden="true">◇</span>
          <span class="point-gallery-title"><b>POINT</b><small>ГАЛЕРЕЯ МИРОВ</small></span>
        </div>
        <div class="point-gallery-head-actions">
          ${hasProject ? '<button type="button" class="point-gallery-btn" data-action="gallery-close">Вернуться в мир</button>' : ''}
          <button type="button" class="point-gallery-btn" data-action="gallery-clone">Клонировать из Git…</button>
          <button type="button" class="point-gallery-btn is-primary" data-action="gallery-open-folder">Открыть папку</button>
        </div>
      </header>
      <div class="point-gallery-filter">
        <input id="gallery-search" type="search" class="point-gallery-search" data-gallery-search placeholder="Поиск по мирам" aria-label="Поиск по мирам" value="${esc(galleryQuery)}">
        <span class="point-gallery-count">${esc(countOf(list.length, 'мир', 'мира', 'миров'))}</span>
      </div>
      ${list.length
        ? `<ul class="point-gallery-list" data-keynav="column" aria-label="Миры Point">${list.map(rowHtml).join('')}</ul>`
        : emptyHtml()}
      <footer class="point-gallery-foot">
        <small>Ядро, индекс и агенты живут внутри мира. Переключение подхватывает уже запущенное ядро, если оно ещё тёплое.</small>
      </footer>
    </div>`
  }

  // Пока мир подключается, экран остаётся своим. Показать здесь «Гильдия
  // отдыхает» было бы неправдой: ядро не отдыхает, оно поднимается, и кнопка
  // «Пробудить ядро» в этот момент только сбивает.
  function projectSwitchSkeleton(info) {
    const name = String(info?.name || '')
    return `<div class="point-gallery-switch">
      <div class="point-gallery-switch-head">
        <span class="point-gallery-mark" aria-hidden="true">◇</span>
        <strong>${esc(name)}</strong>
        <small>ПОДКЛЮЧАЕМ МИР…</small>
      </div>
      <div class="point-gallery-skeleton" aria-hidden="true">
        <span></span><span></span><span></span><span></span>
      </div>
    </div>`
  }

  // Чип в шапке Чертога: имя активного мира и вход в галерею.
  function projectSwitcherChipHtml() {
    const name = String(ui.state.workspace || '')
    return `<button type="button" class="hall-wordmark point-gallery-chip" data-action="gallery-toggle" title="Сменить мир — Ctrl+Alt+P">
      <b>${esc(name || 'ЧЕРТОГ')}</b>
      <span>${ui.state.workspaceTrusted === false ? 'БЕЗОПАСНЫЙ РЕЖИМ' : 'СМЕНИТЬ МИР'}</span>
    </button>`
  }

  function handleProjectGalleryAction(action, target) {
    const projectPath = target?.dataset?.path || ''
    if (action === 'gallery-toggle') {
      setProjectGalleryOpen(!galleryOpen)
      if (galleryOpen) vscode.postMessage({ type: 'refreshProjects' })
      return true
    }
    if (action === 'gallery-close') {
      setProjectGalleryOpen(false)
      return true
    }
    if (action === 'gallery-open') {
      setProjectGalleryOpen(false)
      vscode.postMessage({ type: 'openProject', path: projectPath })
      return true
    }
    if (action === 'gallery-ide') {
      vscode.postMessage({ type: 'openProjectInIde', path: projectPath })
      return true
    }
    if (action === 'gallery-open-folder') {
      setProjectGalleryOpen(false)
      vscode.postMessage({ type: 'chooseProjectFolder' })
      return true
    }
    if (action === 'gallery-clone') {
      vscode.postMessage({ type: 'cloneProject' })
      return true
    }
    if (action === 'gallery-pin') {
      vscode.postMessage({ type: 'pinProject', path: projectPath, pinned: target?.dataset?.pinned !== '1' })
      return true
    }
    if (action === 'gallery-forget') {
      vscode.postMessage({ type: 'forgetProject', path: projectPath })
      return true
    }
    return false
  }

  function handleProjectGalleryInput(target) {
    if (!target?.matches?.('[data-gallery-search]')) return false
    galleryQuery = String(target.value || '')
    return true
  }

  return {
    isProjectGalleryOpen,
    setProjectGalleryOpen,
    projectSwitchInfo,
    setProjectSwitchInfo,
    receiveProjects,
    projectPathByHash,
    projectGallery,
    projectSwitchSkeleton,
    projectSwitcherChipHtml,
    handleProjectGalleryAction,
    handleProjectGalleryInput,
  }
}
