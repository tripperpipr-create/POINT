// Карточка merge request — вкладка редактора. Шапка с состоянием и
// действиями, ниже вкладки: обзор, обсуждение, изменения, пайплайн.
//
// Описание и заметки — markdown GitLab, недоверенный: он проходит тот же
// разборщик, что реплики Мастера (companion-markdown.js), — он экранирует всё,
// кроме узнанной разметки, и пускает только ссылки http(s). Diff файла карточка
// не рисует: строка файла открывает штатное сравнение IDE.

import { esc } from './html-escape.js'
import { draftId, glIcon, mergeStatus, pipelineStatus, problemHtml, loadingHtml, shortSha, timeAgo } from './gitlab-views.js'

const TABS = [['overview', 'Обзор'], ['discussion', 'Обсуждение'], ['changes', 'Изменения'], ['pipeline', 'Пайплайн']]

function fileMark(file) {
  if (file.new) return ['A', 'is-add', 'добавлен']
  if (file.deleted) return ['D', 'is-del', 'удалён']
  if (file.renamed) return ['R', 'is-ren', 'переименован']
  return ['M', 'is-mod', 'изменён']
}

export function createGitLabMergeRequestView({ getState, markdown, pipelineRows }) {
  function chip(tone, text, title = '') {
    return `<span class="gl-chip is-${esc(tone)}"${title ? ` title="${esc(title)}"` : ''}>${esc(text)}</span>`
  }

  function header(state, view) {
    const mr = view.mergeRequest || {}
    const status = mergeStatus(mr.mergeStatus)
    const approvals = view.approvals
    const required = (approvals?.rules || []).reduce((sum, rule) => sum + Number(rule.required || 0), 0)
    const given = (approvals?.approvedBy || []).length
    const pipeline = (view.pipelines || [])[0]
    const open = mr.state === 'opened'
    const chips = [
      chip(open ? 'ok' : mr.state === 'merged' ? 'merged' : 'mute', { opened: 'открыт', merged: 'слит', closed: 'закрыт', locked: 'заблокирован' }[mr.state] || mr.state || '—'),
      open ? chip(status.tone, status.label) : '',
      mr.draft ? chip('mute', 'черновик') : '',
      mr.hasConflicts ? chip('bad', 'конфликт') : '',
      approvals ? chip(required && given >= required ? 'ok' : 'warn', required ? `одобрено ${given} из ${required}` : `одобрений ${given}`) : '',
      pipeline ? chip(pipelineStatus(pipeline.status).tone, `пайплайн: ${pipelineStatus(pipeline.status).label}`) : '',
    ].join('')
    const busy = Boolean(state.busy)
    const sha = mr.diffRefs?.headSha || mr.sha || ''
    const actions = open ? `
      <button type="button" class="gl-btn" data-action="gitlab-approve" data-approve="${view.approvedByMe ? '0' : '1'}" data-sha="${esc(sha)}"${busy ? ' disabled' : ''} title="${esc(view.approvedByMe ? 'Снять ваше одобрение' : 'Одобрить текущую голову MR — можно ли одобрять свой MR, решают настройки проекта в GitLab')}">${glIcon('check', 13)}<span>${view.approvedByMe ? 'Снять одобрение' : 'Одобрить'}</span></button>
      <label class="gl-check" title="Удалить исходную ветку после слияния"><input type="checkbox" id="${draftId('removeSource')}" data-draft="removeSource"${state.drafts.removeSource ?? mr.removeSourceBranch ? ' checked' : ''}><span>удалить ветку</span></label>
      <button type="button" class="gl-btn is-primary" data-action="gitlab-merge" data-sha="${esc(sha)}"${busy || mr.draft || mr.hasConflicts ? ' disabled' : ''} title="${esc(mr.draft ? 'Черновик не сливается' : mr.hasConflicts ? 'Сначала разрешите конфликт' : 'Слить после подтверждения')}">${glIcon('mr', 13)}<span>Merge</span></button>` : ''
    return `<header class="gl-mr-head">
      <div class="gl-mr-title">
        <h1><em>!${Number(mr.iid) || 0}</em> ${esc(mr.title || '')}</h1>
        ${mr.webUrl ? `<button type="button" class="nc-icon-btn" data-action="gitlab-open-browser" data-url="${esc(mr.webUrl)}" title="Открыть в GitLab" aria-label="Открыть в GitLab">${glIcon('external', 14)}</button>` : ''}
      </div>
      <p class="gl-mr-sub"><span class="gl-branch">${glIcon('branch', 12)}${esc(mr.sourceBranch || '')} → ${esc(mr.targetBranch || '')}</span><span>${esc(mr.author?.name || mr.author?.username || '')}</span><span>обновлён ${esc(timeAgo(mr.updatedAt))}</span><span class="nc-hash">${esc(shortSha(sha))}</span></p>
      <div class="gl-mr-bar"><div class="gl-chips">${chips}</div><i class="nc-gap"></i>${actions}</div>
      ${mr.mergeError ? `<p class="gl-mr-error">${esc(mr.mergeError)}</p>` : ''}
    </header>`
  }

  function people(label, list) {
    if (!list?.length) return ''
    return `<div class="gl-people"><span>${esc(label)}</span>${list.map(user => `<b title="@${esc(user.username || '')}">${esc(user.name || user.username || '')}</b>`).join('')}</div>`
  }

  function overview(view) {
    const mr = view.mergeRequest || {}
    const rules = view.approvals?.rules || []
    return `<section class="gl-pane">
      <article class="gl-desc">${mr.description ? markdown(mr.description) : '<p class="gl-muted">Описания нет.</p>'}${mr.descriptionTrimmed ? '<p class="gl-muted">Описание длиннее 64 КБ — конец в GitLab.</p>' : ''}</article>
      <aside class="gl-side">
        ${people('Ревьюеры', mr.reviewers)}${people('Исполнители', mr.assignees)}
        ${rules.length ? `<div class="gl-rules"><span>Правила одобрения</span>${rules.map(rule => `<p class="${rule.approved ? 'is-ok' : ''}">${rule.approved ? glIcon('check', 12) : glIcon('x', 12)}<b>${esc(rule.name || '')}</b><small>${Number((rule.approvedBy || []).length)} из ${Number(rule.required || 0)}</small></p>`).join('')}</div>` : ''}
        ${(view.missing || []).map(line => `<p class="gl-muted">${glIcon('warning', 12)} ${esc(line)}</p>`).join('')}
      </aside>
    </section>`
  }

  function note(item) {
    const where = item.position ? `<span class="gl-where-chip" title="${esc(item.position.newPath || item.position.oldPath || '')}">${esc(String(item.position.newPath || item.position.oldPath || '').split('/').pop())}:${Number(item.position.newLine || item.position.oldLine) || ''}</span>` : ''
    return `<div class="gl-note${item.system ? ' is-system' : ''}">
      <header><b>${esc(item.author?.name || item.author?.username || '')}</b><small>${esc(timeAgo(item.createdAt))}</small>${where}</header>
      ${item.system ? `<p>${esc(item.body || '')}</p>` : `<div class="gl-note-body">${markdown(item.body || '')}</div>`}
    </div>`
  }

  function discussion(state) {
    const response = state.discussions
    if (!response) return loadingHtml('Загружаем обсуждение…')
    if (response.state !== 'ok') return problemHtml(response, { retry: 'gitlab-mr-reload' })
    const threads = Array.isArray(response.data?.discussions) ? response.data.discussions : []
    const busy = Boolean(state.busy)
    const list = threads.map(thread => {
      const first = thread.notes?.[0]
      if (first?.system && thread.individual) return `<div class="gl-event">${glIcon('check', 11)}<span><b>${esc(first.author?.name || '')}</b> ${esc(first.body || '')}</span><small>${esc(timeAgo(first.createdAt))}</small></div>`
      const replyKey = `reply:${thread.id}`
      return `<article class="gl-thread${thread.resolved ? ' is-resolved' : ''}">
        ${thread.resolvable ? `<span class="gl-thread-state">${thread.resolved ? 'решено' : 'открыто'}</span>` : ''}
        ${(thread.notes || []).map(note).join('')}
        ${thread.individual ? '' : `<div class="gl-reply"><textarea class="gl-input" rows="2" id="${draftId(replyKey)}" data-draft="${esc(replyKey)}" placeholder="Ответить в нить…">${esc(state.drafts[replyKey] || '')}</textarea><button type="button" class="gl-btn" data-action="gitlab-comment" data-discussion="${esc(thread.id)}"${busy ? ' disabled' : ''}>Ответить</button></div>`}
      </article>`
    }).join('')
    return `<section class="gl-pane is-single">
      ${list || '<p class="gl-muted">Обсуждения пока нет.</p>'}
      <div class="gl-reply is-new"><textarea class="gl-input" rows="3" id="${draftId('comment')}" data-draft="comment" placeholder="Комментарий к MR (markdown GitLab)…">${esc(state.drafts.comment || '')}</textarea><button type="button" class="gl-btn is-primary" data-action="gitlab-comment"${busy ? ' disabled' : ''}>${glIcon('comment', 13)}<span>Комментировать</span></button></div>
    </section>`
  }

  function changes(state, view) {
    const response = state.changes
    if (!response) return loadingHtml('Загружаем список файлов…')
    if (response.state !== 'ok') return problemHtml(response, { retry: 'gitlab-mr-reload' })
    const files = Array.isArray(response.data?.files) ? response.data.files : []
    const refs = view.mergeRequest?.diffRefs || {}
    if (!files.length) return '<p class="gl-muted">Изменённых файлов нет.</p>'
    return `<section class="gl-pane is-single"><p class="gl-muted">Файл открывается сравнением IDE: слева база MR (${esc(shortSha(refs.baseSha))}), справа голова (${esc(shortSha(refs.headSha))}).</p>
      <ul class="gl-files">${files.map(file => {
        const [letter, tone, label] = fileMark(file)
        const path = file.newPath || file.oldPath || ''
        return `<li><button type="button" class="nc-row" data-action="gitlab-open-diff" data-path="${esc(path)}" data-old-path="${esc(file.oldPath || path)}" data-new-file="${file.new ? '1' : '0'}" data-deleted="${file.deleted ? '1' : '0'}" title="${esc(`${label}: ${path}`)}">
          <i class="gl-file-mark ${tone}" aria-label="${esc(label)}">${letter}</i>
          <span><strong>${esc(path.split('/').pop())}</strong><small>${esc(file.renamed ? `${file.oldPath} → ${path}` : path)}</small></span>
        </button></li>`
      }).join('')}</ul></section>`
  }

  function pipelines(state, view) {
    const project = view.mergeRequest?.projectPath || state.project
    const items = view.pipelines || []
    const fake = { state: 'ok', data: { project, ref: view.mergeRequest?.sourceBranch || '', items } }
    return `<section class="gl-pane is-single">${items.length ? pipelineRows(state, fake) : '<p class="gl-muted">У этого MR пайплайнов нет.</p>'}</section>`
  }

  function mrView() {
    const state = getState()
    const response = state.mr
    if (!response) return `<main class="nc-app gl-app gl-mr">${loadingHtml('Загружаем merge request…')}</main>`
    if (response.state !== 'ok') return `<main class="nc-app gl-app gl-mr"><div class="gl-scroll">${problemHtml(response, { retry: 'gitlab-mr-reload' })}</div></main>`
    const view = response.data || {}
    const counts = {
      discussion: (state.discussions?.data?.discussions || []).filter(thread => !(thread.individual && thread.notes?.[0]?.system)).length,
      changes: (state.changes?.data?.files || []).length,
      pipeline: (view.pipelines || []).length,
    }
    const tabs = `<nav class="nc-tabs gl-mr-tabs" role="tablist" data-keynav="row">${TABS.map(([id, label]) => `<button type="button" role="tab" class="nc-tab${state.mrTab === id ? ' is-active' : ''}" aria-selected="${state.mrTab === id ? 'true' : 'false'}" tabindex="${state.mrTab === id ? '0' : '-1'}" data-action="gitlab-mr-tab" data-tab="${id}"><span>${esc(label)}</span>${counts[id] ? `<b>${counts[id]}</b>` : ''}</button>`).join('')}</nav>`
    const notice = state.notice
      ? `<div class="nc-notice ${state.notice.tone === 'error' ? 'is-error' : 'is-ok'}">${glIcon(state.notice.tone === 'error' ? 'warning' : 'check', 14)}<p>${esc(state.notice.text)}</p><button type="button" class="nc-icon-btn" data-action="gitlab-dismiss-notice" aria-label="Скрыть">${glIcon('x', 12)}</button></div>`
      : ''
    const body = state.mrTab === 'discussion' ? discussion(state)
      : state.mrTab === 'changes' ? changes(state, view)
        : state.mrTab === 'pipeline' ? pipelines(state, view)
          : overview(view)
    return `<main class="nc-app gl-app gl-mr">${header(state, view)}${tabs}${notice}<div class="gl-scroll">${body}</div></main>`
  }

  return { mrView }
}
