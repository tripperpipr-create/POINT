// Git квеста в ленте Мастера: ветка до запуска и коммит, отправка, MR после.
//
// Раньше наряд ветки не знал, и квест коммитил в ту, что была открыта, —
// однажды в ветку, уже влитую в main и удалённую на сервере. Теперь ветку
// решает git-агент в наряде: защищённую он предлагает не трогать и завести
// новую по ТЗ, про незащищённую спрашивает, и запуск ждёт выбора. После
// квеста карточка показывает, что сделано с git, и только уместные кнопки.

import { list } from './format-units.js'

const MODE_LABEL = { 'new-current': 'Новая ветка от текущей', 'new-default': 'Новая ветка от основной', current: 'Работать в текущей ветке', none: 'Не трогать ветку и не коммитить' }
const ACTION_LABEL = { commit: 'Закоммитить', push: 'Отправить', merge_request: 'Создать MR', revert: 'Откатить' }

function planChoice(plan) {
  if (!plan) return ''
  if (plan.mode === 'current') return 'current'
  if (plan.mode === 'none') return 'none'
  if (plan.mode === 'new') return plan.baseKind === 'default' ? 'new-default' : 'new-current'
  return plan.recommended || ''
}

// Запуск ждёт выбора ветки — кнопка «Запустить квест» говорит об этом сама.
export function gitChoiceMissing(order) {
  const plan = order?.git
  return Boolean(plan && (plan.choice === 'required' || !plan.mode))
}

function repoName(repo) {
  return repo.path === '.' ? 'проект' : repo.path
}

function baseLabel(repo, kind) {
  return kind === 'default' ? (repo.defaultBase || repo.defaultBranch || 'основная') : (repo.currentBase || repo.current || 'HEAD')
}

// Раздел «Ветка» в карточке наряда до запуска.
export function workOrderGitHtml(order, esc) {
  const plan = order?.git
  if (!plan || order.state === 'approved') return ''
  const repos = list(plan.repositories)
  const first = repos[0] || {}
  const chosen = planChoice(plan)
  const required = gitChoiceMissing(order)
  const options = ['new-current', 'new-default', 'current', 'none'].filter(key => {
    if (key === 'new-default') return repos.some(repo => repo.defaultBaseCommit && repo.defaultBase !== repo.currentBase)
    if (key === 'current') return repos.every(repo => repo.current)
    return true
  }).map(key => {
    let label = MODE_LABEL[key]
    if (key === 'new-current') label += ` (${esc(baseLabel(first, 'current'))})`
    if (key === 'new-default') label += ` (${esc(baseLabel(first, 'default'))})`
    if (key === 'current') label += ` ${esc(first.current || '')}${repos.some(repo => repo.protected) ? ' — ветка защищена' : ''}`
    return `<option value="${key}"${key === chosen ? ' selected' : ''}>${label}${key === plan.recommended && plan.choice === 'required' ? ' · совет git-агента' : ''}</option>`
  }).join('')
  const status = repos.map(repo => `<li><b>${esc(repoName(repo))}</b>: ${esc(repo.current || 'HEAD отсоединён')}${repo.protected ? ' · защищена' : ''}${list(repo.warnings).length ? `<ul>${list(repo.warnings).map(item => `<li>${esc(item)}</li>`).join('')}</ul>` : ''}</li>`).join('')
  const notes = list(plan.notes).map(item => `<p>${esc(item)}</p>`).join('')
  const summary = plan.mode === 'new'
    ? `Новая ветка <code>${esc(plan.branch)}</code> от ${esc(baseLabel(first, plan.baseKind))}`
    : plan.mode === 'current' ? `Работа в текущей ветке <code>${esc(first.current || '')}</code>`
      : plan.mode === 'none' ? 'Ветка не меняется, коммита не будет' : 'Ветка не выбрана'
  return `<section class="master-v2-warning quest-git-plan" data-work-order-git="${esc(order.id)}">
      <div class="hall-quest-kick${required ? ' is-ask' : ''}"><span class="hall-quest-dot" aria-hidden="true"></span><b>Ветка · ${required ? 'выберите перед запуском' : 'git-агент'}</b></div>
      <p>${summary}</p>
      <ul>${status}</ul>${notes}
      <div class="hall-quest-acts">
        <select data-work-order-git-choice="${esc(order.id)}" aria-label="Ветка квеста">${required && !chosen ? '<option value="" selected>Выберите…</option>' : ''}${options}</select>
        <input type="text" data-work-order-git-branch="${esc(order.id)}" value="${esc(plan.branch || '')}" aria-label="Имя новой ветки" spellcheck="false">
        <button type="button" class="hall-btn is-sm" data-action="choose-work-order-branch-v2" data-id="${esc(order.id)}">Сохранить выбор</button>
      </div>
    </section>`
}

// Git после квеста: ветка, коммит, отправка, MR и уместные кнопки.
export function questGitHtml(order, esc, busy = false) {
  const runtime = order?.runtime || {}
  const git = runtime.git
  if (!git || !runtime.questId) return ''
  const rows = list(git.repositories).map(repo => {
    const facts = [
      repo.branch ? `ветка <code>${esc(repo.branch)}</code>${repo.target ? ` → ${esc(repo.target)}` : ''}` : '',
      repo.commitId ? `коммит <code>${esc(String(repo.commitId).slice(0, 10))}</code> ${esc(repo.commitSubject || '')}` : (repo.reverted ? 'файлы откатаны' : 'без коммита'),
      repo.pushed ? 'отправлено' : '',
      repo.mrUrl ? `<button type="button" class="hall-btn is-sm" data-action="open-quest-git-url" data-url="${esc(repo.mrUrl)}">MR в GitLab</button>` : '',
    ].filter(Boolean).join(' · ')
    const buttons = list(repo.actions).map(action => `<button type="button" class="hall-btn${action === 'merge_request' || (action === 'push' && !list(repo.actions).includes('merge_request')) ? ' is-primary' : ''}" data-action="quest-git-action" data-git-action="${esc(action)}" data-repo="${esc(repo.path)}" data-quest-id="${esc(runtime.questId)}" data-id="${esc(order.id)}"${busy ? ' disabled' : ''}>${esc(ACTION_LABEL[action] || action)}</button>`).join('')
    const error = repo.lastError ? `<p class="quest-git-error">Последняя попытка: ${esc(repo.lastError)}</p>` : ''
    return `<li><b>${esc(repoName(repo))}</b>: ${facts}${error}${buttons ? `<div class="hall-quest-acts">${buttons}</div>` : ''}</li>`
  }).join('')
  const notes = list(git.notes).map(item => `<p>${esc(item)}</p>`).join('')
  const mode = git.commitMode === 'on_request' ? 'Коммит и отправка — по вашей просьбе' : git.commitMode === 'on_completion' ? 'Коммит — сам после успеха, отправка — по вашему решению' : ''
  return `<div class="quest-verdict is-quiet quest-git"><p><b>Git</b>${mode ? ` · ${esc(mode)}` : ''}</p><ul>${rows}</ul>${notes}</div>`
}

// Нажатия: выбор ветки (новая версия наряда) и git-действия квеста.
export function handleQuestGitAction({ action, target, ui, vscode, render }) {
  if (action === 'open-quest-git-url') {
    vscode.postMessage({ type: 'openQuestGitUrl', url: String(target.dataset.url || '') })
    return true
  }
  if (action === 'quest-git-action') {
    const id = String(target.dataset.id || '')
    if (!id || ui.masterWorkOrderBusy.has(id)) return true
    ui.masterWorkOrderBusy.add(id)
    vscode.postMessage({ type: 'questGitAction', workOrderId: id, questId: String(target.dataset.questId || ''), gitAction: String(target.dataset.gitAction || ''), repo: String(target.dataset.repo || '') })
    render()
    return true
  }
  if (action !== 'choose-work-order-branch-v2') return false
  const id = String(target.dataset.id || '')
  const order = (Array.isArray(ui.masterData?.workOrders) ? ui.masterData.workOrders : []).find(item => item.id === id)
  const section = target.closest?.('[data-work-order-git]')
  const choice = String(section?.querySelector?.('[data-work-order-git-choice]')?.value || '')
  const branch = String(section?.querySelector?.('[data-work-order-git-branch]')?.value || '').trim()
  if (!id || !order?.git || ui.masterWorkOrderBusy.has(id)) return true
  if (!choice) { ui.masterComposeNote = 'Выберите, в какой ветке работать квесту'; render(); return true }
  const draft = JSON.parse(JSON.stringify(order))
  delete draft.digest; delete draft.runtime; delete draft.approvedVersion; delete draft.approvedDigest
  draft.git = { ...draft.git, mode: choice === 'current' || choice === 'none' ? choice : 'new', baseKind: choice === 'new-default' ? 'default' : choice === 'new-current' ? 'current' : '', branch }
  ui.masterWorkOrderBusy.add(id)
  const idempotencyKey = globalThis.crypto?.randomUUID?.() || `branch-${Date.now()}-${Math.random().toString(36).slice(2)}`
  vscode.postMessage({ type: 'reviseMasterWorkOrderV2', workOrderId: id, expectedVersion: Number(order.version), expectedDigest: String(order.digest || ''), idempotencyKey, workOrder: draft })
  render()
  return true
}
