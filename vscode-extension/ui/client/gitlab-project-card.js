// Карточка проекта GitLab — вкладка редактора. Сверху — вердикт о локальной
// копии: эта папка, уже склонирован или «копии нет — клонировать». Ниже
// вкладки: обзор с README, файлы ветки, коммиты и ветки.
//
// README — markdown GitLab, недоверенный: он проходит тот же разборщик, что
// описание MR. Файл и diff коммита карточка не рисует: их открывает IDE
// документами только для чтения.

import { esc } from './html-escape.js'
import { glAvatar, glIcon, loadingHtml, problemHtml, shortSha, timeAgo, timeShort, verdictHtml } from './gitlab-common.js'

const TABS = [['overview', 'Обзор'], ['files', 'Файлы'], ['commits', 'Коммиты'], ['branches', 'Ветки']]
const VISIBILITY = { public: 'публичный', internal: 'внутренний', private: 'закрытый' }
const ACCESS = { 10: 'гость', 15: 'планировщик', 20: 'репортёр', 30: 'разработчик', 40: 'сопровождающий', 50: 'владелец' }

function fileMark(file) {
  if (file.new) return ['A', 'is-add', 'добавлен']
  if (file.deleted) return ['D', 'is-del', 'удалён']
  if (file.renamed) return ['R', 'is-ren', 'переименован']
  return ['M', 'is-mod', 'изменён']
}

// Коммиты по дням: «Сегодня», «Вчера», дальше дата.
function dayLabel(value) {
  const at = new Date(value)
  if (Number.isNaN(at.getTime())) return ''
  const today = new Date()
  const start = day => new Date(day.getFullYear(), day.getMonth(), day.getDate()).getTime()
  const diff = Math.round((start(today) - start(at)) / 86400000)
  if (diff === 0) return 'Сегодня'
  if (diff === 1) return 'Вчера'
  return at.toLocaleDateString('ru-RU', { day: 'numeric', month: 'long', year: at.getFullYear() === today.getFullYear() ? undefined : 'numeric' })
}

export function createGitLabProjectCard({ getState, markdown }) {
  function verdict(view, project) {
    const button = (action, label, extra = '', primary = false) => `<button type="button" class="gl-btn${primary ? ' is-primary' : ''}" data-action="${action}"${extra}>${esc(label)}</button>`
    const archived = project.archived ? 'проект в архиве — пушить нельзя' : ''
    if (view.cloning === 'running' || view.cloning === 'asking') {
      return verdictHtml({ tone: 'wait', glyph: 'clone', title: view.cloning === 'asking' ? 'Выберите папку для клона' : 'Клонируем…',
        reasons: ['git clone идёт в выводе Git', 'когда он закончит, IDE предложит открыть папку'] })
    }
    if (view.detail?.data?.current) {
      return verdictHtml({ tone: 'ok', title: 'Эта папка — рабочая копия проекта', reasons: ['MR и пайплайны — в окне GitLab', archived],
        actions: button('gitlab-open-window', 'Окно GitLab', '', true) })
    }
    if (view.clone?.exists) {
      return verdictHtml({ tone: 'ok', title: 'Проект уже склонирован', reasons: [view.clone.path, archived],
        actions: button('gitlab-open-clone', 'Открыть', '', true) + button('gitlab-open-clone', 'В новом окне', ' data-new-window="1"') })
    }
    const ssh = project.sshUrl ? button('gitlab-clone', 'Клонировать по SSH', ' data-kind="ssh"', true) : ''
    const https = project.httpUrl ? button('gitlab-clone', project.sshUrl ? 'по HTTPS' : 'Клонировать по HTTPS', ' data-kind="https"', !project.sshUrl) : ''
    return verdictHtml({ tone: project.archived ? 'warn' : 'mute', glyph: 'clone', title: 'Локальной копии нет',
      reasons: [ssh || https ? 'клонирует git этой машины — ваши ключи SSH и учётные данные' : 'у проекта нет адреса клона на этом GitLab', archived],
      actions: ssh + https })
  }

  function header(view, project) {
    const facts = [
      VISIBILITY[project.visibility] || project.visibility,
      ACCESS[project.accessLevel] ? `вы ${ACCESS[project.accessLevel]}` : '',
      project.defaultBranch ? `по умолчанию ${project.defaultBranch}` : '',
      project.lastActivityAt ? `активность ${timeAgo(project.lastActivityAt)}` : '',
    ].filter(Boolean)
    return `<header class="gl-mr-head gl-proj-head">
      <div class="gl-mr-title">
        ${glAvatar({ name: project.name, username: project.path }, { size: 'lg', title: false }).replace('gl-ava', 'gl-ava is-square is-xl')}
        <h1><em>${esc(project.namespace ? `${project.namespace}/` : '')}</em>${esc(project.name || '')}</h1>
        <span class="gl-proj-counts">${project.stars ? `<span title="Звёзды">${glIcon('star', 12)}${Number(project.stars)}</span>` : ''}${project.forks ? `<span title="Форки">${glIcon('branch', 12)}${Number(project.forks)}</span>` : ''}</span>
        ${project.webUrl ? `<button type="button" class="nc-icon-btn" data-action="gitlab-open-browser" data-url="${esc(project.webUrl)}" title="Открыть в GitLab" aria-label="Открыть в GitLab">${glIcon('external', 14)}</button>` : ''}
      </div>
      ${project.description ? `<p class="gl-proj-desc">${esc(project.description)}</p>` : ''}
      <p class="gl-mr-sub">${facts.map(fact => `<span>${esc(fact)}</span>`).join('')}${(project.topics || []).map(topic => `<span class="gl-topic">${esc(topic)}</span>`).join('')}</p>
      ${verdict(view, project)}
    </header>`
  }

  const refChip = view => `<button type="button" class="gl-ref" data-action="gitlab-project-tab" data-tab="branches" title="Сменить ветку">${glIcon('branch', 12)}<span>${esc(view.ref || '—')}</span></button>`

  function cloneUrls(project) {
    const row = (kind, label, url) => url ? `<p><b>${esc(label)}</b><code>${esc(url)}</code><button type="button" class="nc-icon-btn" data-action="gitlab-copy-url" data-kind="${kind}" title="Скопировать адрес" aria-label="Скопировать адрес ${esc(label)}">${glIcon('copy', 13)}</button></p>` : ''
    const rows = row('ssh', 'SSH', project.sshUrl) + row('https', 'HTTPS', project.httpUrl)
    return rows ? `<div class="gl-clone-urls"><span>Адрес клона</span>${rows}</div>` : ''
  }

  function overview(view, project) {
    const readme = view.readme
    const root = view.trees[`${view.ref}:`]
    let text
    if (readme?.state === 'ok') {
      const file = readme.data || {}
      text = file.binary || file.tooBig ? '<p class="gl-muted">README слишком большой или не текст — откройте его во вкладке «Файлы».</p>'
        : /\.(md|markdown)$/i.test(view.readmePath) ? markdown(String(file.content || '')) : `<pre>${esc(file.content || '')}</pre>`
    } else if (readme) text = problemHtml(readme)
    else if (root && root.state === 'ok') text = '<p class="gl-muted">README в корне ветки нет.</p>'
    else if (root) text = problemHtml(root)
    else text = loadingHtml('Ищем README…')
    return `<section class="gl-pane">
      <article class="gl-desc gl-readme">${view.readmePath ? `<header class="gl-readme-head">${glIcon('file', 12)}<span>${esc(view.readmePath)}</span>${refChip(view)}</header>` : ''}${text}</article>
      <aside class="gl-side">${cloneUrls(project)}
        <div class="gl-rules"><span>Проект</span>
          <p><b>Открытых задач</b><small>${Number(project.openIssues || 0)}</small></p>
          <p><b>Звёзд</b><small>${Number(project.stars || 0)}</small></p>
          <p><b>Форков</b><small>${Number(project.forks || 0)}</small></p>
        </div>
      </aside>
    </section>`
  }

  function files(view) {
    const response = view.trees[`${view.ref}:${view.path}`]
    const parts = view.path ? view.path.split('/') : []
    const crumbs = [`<button type="button" class="gl-crumb" data-action="gitlab-tree-path" data-path="">${glIcon('folder', 12)}корень</button>`,
      ...parts.map((part, index) => `<button type="button" class="gl-crumb" data-action="gitlab-tree-path" data-path="${esc(parts.slice(0, index + 1).join('/'))}">${esc(part)}</button>`)].join('<i>/</i>')
    let body
    if (!response) body = loadingHtml('Загружаем папку…')
    else if (response.state !== 'ok') body = problemHtml(response, { retry: 'gitlab-project-reload' })
    else {
      const tree = response.data?.tree || {}
      const entries = Array.isArray(tree.entries) ? tree.entries : []
      const up = view.path ? `<li><button type="button" class="nc-row" data-action="gitlab-tree-path" data-path="${esc(parts.slice(0, -1).join('/'))}">${glIcon('folder', 14)}<span><strong>..</strong></span></button></li>` : ''
      body = entries.length || up ? `<ul class="gl-tree">${up}${entries.map(entry => {
        const glyph = entry.type === 'tree' ? 'folder' : entry.type === 'commit' ? 'commit' : 'file'
        const hint = entry.type === 'commit' ? 'подмодуль — отдельный репозиторий' : entry.type === 'tree' ? 'открыть папку' : 'открыть файл в редакторе — только чтение'
        return `<li><button type="button" class="nc-row is-${esc(entry.type)}" data-action="gitlab-tree-open" data-type="${esc(entry.type)}" data-path="${esc(entry.path)}" title="${esc(hint)}"${entry.type === 'commit' ? ' disabled' : ''}>${glIcon(glyph, 14)}<span><strong>${esc(entry.name)}</strong></span></button></li>`
      }).join('')}</ul>${tree.trimmed ? '<p class="gl-muted">Показаны первые 100 записей — остальное в GitLab.</p>' : ''}`
        : '<p class="gl-muted">Папка пуста — в ветке ещё нет файлов.</p>'
    }
    return `<section class="gl-pane is-single"><header class="gl-files-head"><nav class="gl-crumbs">${crumbs}</nav>${refChip(view)}</header>${body}</section>`
  }

  function commitRow(view, commit) {
    const open = view.openCommit === commit.id
    const merge = (commit.parentIds || []).length > 1
    const stats = commit.stats && !merge ? `<span class="gl-lines"><b>+${Number(commit.stats.additions)}</b><i>−${Number(commit.stats.deletions)}</i></span>` : ''
    let detail = ''
    if (open) {
      const response = view.commitDetails[commit.id]
      if (!response) detail = loadingHtml('Загружаем файлы коммита…')
      else if (response.state !== 'ok') detail = problemHtml(response)
      else {
        const item = response.data?.commit || {}
        const parent = (item.parentIds || [])[0] || ''
        detail = `${item.message ? `<pre class="gl-commit-msg">${esc(item.message)}</pre>` : ''}
          <ul class="gl-files">${(item.files || []).map(file => {
            const [letter, tone, label] = fileMark(file)
            const path = file.newPath || file.oldPath || ''
            return `<li><button type="button" class="nc-row" data-action="gitlab-commit-file" data-sha="${esc(item.id)}" data-parent="${esc(parent)}" data-path="${esc(path)}" data-old-path="${esc(file.oldPath || path)}" data-new-file="${file.new ? '1' : '0'}" data-deleted="${file.deleted ? '1' : '0'}" title="${esc(`${label}: сравнение с родителем в IDE`)}">
              <i class="gl-file-mark ${tone}" aria-label="${esc(label)}">${letter}</i><span><strong>${esc(path.split('/').pop())}</strong><small>${esc(file.renamed ? `${file.oldPath} → ${path}` : path)}</small></span>
              <span class="gl-lines"><b>+${Number(file.additions)}</b><i>−${Number(file.deletions)}</i></span></button></li>`
          }).join('')}</ul>${item.filesTrimmed ? '<p class="gl-muted">GitLab отдал первую страницу файлов — полный список в GitLab.</p>' : ''}`
      }
    }
    return `<article class="gl-commit${open ? ' is-open' : ''}">
      <button type="button" class="nc-row" data-action="gitlab-commit-toggle" data-sha="${esc(commit.id)}" aria-expanded="${open ? 'true' : 'false'}">
        ${glAvatar({ name: commit.authorName, username: commit.authorName }, { size: 'md' })}
        <span><strong>${esc(commit.title)}</strong><small>${merge ? '<span class="gl-tag">слияние</span>' : ''}<span>${esc(commit.authorName)}</span><span>${esc(timeShort(commit.authoredAt))}</span><span class="nc-hash">${esc(commit.shortId || shortSha(commit.id))}</span></small></span>
        ${stats}${glIcon(open ? 'caretDown' : 'caretRight', 12)}
      </button>${detail ? `<div class="gl-commit-body">${detail}</div>` : ''}
    </article>`
  }

  function commits(view) {
    const pages = view.commits[view.ref] || []
    const first = pages[0]
    let body
    if (!first) body = loadingHtml('Загружаем историю…')
    else if (first.state !== 'ok') body = problemHtml(first, { retry: 'gitlab-project-reload' })
    else {
      const items = pages.filter(page => page?.state === 'ok').flatMap(page => page.data?.items || [])
      const last = pages[pages.length - 1]
      let day = ''
      body = items.length ? items.map(commit => {
        const label = dayLabel(commit.authoredAt)
        const head = label !== day ? `<h3 class="gl-day">${esc(label)}</h3>` : ''
        day = label
        return head + commitRow(view, commit)
      }).join('') + (!last ? loadingHtml('Загружаем ещё…') : last.state !== 'ok' ? problemHtml(last) : last.data?.more ? '<button type="button" class="gl-btn gl-more" data-action="gitlab-commits-more">Показать ещё</button>' : '')
        : '<p class="gl-muted">В ветке нет коммитов.</p>'
    }
    return `<section class="gl-pane is-single"><header class="gl-files-head"><span class="gl-muted">История ветки</span>${refChip(view)}</header>${body}</section>`
  }

  function branches(view) {
    const response = view.branches
    if (!response) return `<section class="gl-pane is-single">${loadingHtml('Загружаем ветки…')}</section>`
    if (response.state !== 'ok') return `<section class="gl-pane is-single">${problemHtml(response, { retry: 'gitlab-project-reload' })}</section>`
    const items = Array.isArray(response.data?.items) ? response.data.items : []
    return `<section class="gl-pane is-single">${items.length ? `<ul class="gl-branches">${items.map(branch => `<li class="${branch.name === view.ref ? 'is-current' : ''}">
      <span><strong>${glIcon('branch', 12)}${esc(branch.name)}${branch.default ? '<em class="gl-tag is-ok">по умолчанию</em>' : ''}${branch.protected ? '<em class="gl-tag is-warn">защищена</em>' : ''}${branch.merged ? '<em class="gl-tag">слита</em>' : ''}</strong>
      <small>${esc(branch.commit?.title || '')}<span>${esc(branch.commit?.authorName || '')} · ${esc(timeAgo(branch.commit?.committedAt))}</span></small></span>
      <button type="button" class="gl-btn is-quiet" data-action="gitlab-branch-commits" data-ref="${esc(branch.name)}">Коммиты</button>
      <button type="button" class="gl-btn is-quiet" data-action="gitlab-branch-files" data-ref="${esc(branch.name)}">Файлы</button>
    </li>`).join('')}</ul>` : '<p class="gl-muted">Веток нет — репозиторий пуст.</p>'}</section>`
  }

  function projectView() {
    const state = getState()
    const view = state.card
    const response = view?.detail
    if (!response) return `<main class="nc-app gl-app gl-mr gl-proj">${loadingHtml('Загружаем проект…')}</main>`
    if (response.state !== 'ok') return `<main class="nc-app gl-app gl-mr gl-proj"><div class="gl-scroll gl-pad">${problemHtml(response, { retry: 'gitlab-project-reload' })}</div></main>`
    const project = response.data?.project || {}
    const count = { branches: view.branches?.state === 'ok' ? (view.branches.data?.items || []).length : 0 }
    const tabs = `<nav class="nc-tabs gl-mr-tabs" role="tablist" data-keynav="row">${TABS.map(([id, label]) => `<button type="button" role="tab" class="nc-tab${view.tab === id ? ' is-active' : ''}" aria-selected="${view.tab === id ? 'true' : 'false'}" tabindex="${view.tab === id ? '0' : '-1'}" data-action="gitlab-project-tab" data-tab="${id}"><span>${esc(label)}</span>${count[id] ? `<b>${count[id]}</b>` : ''}</button>`).join('')}</nav>`
    const notice = state.notice
      ? `<div class="nc-notice ${state.notice.tone === 'error' ? 'is-error' : 'is-ok'}">${glIcon(state.notice.tone === 'error' ? 'warning' : 'check', 14)}<p>${esc(state.notice.text)}</p><button type="button" class="nc-icon-btn" data-action="gitlab-dismiss-notice" aria-label="Скрыть">${glIcon('x', 12)}</button></div>`
      : ''
    const body = view.tab === 'files' ? files(view) : view.tab === 'commits' ? commits(view) : view.tab === 'branches' ? branches(view) : overview(view, project)
    return `<main class="nc-app gl-app gl-mr gl-proj">${header(view, project)}${tabs}${notice}<div class="gl-scroll">${body}</div></main>`
  }

  return { projectView }
}
