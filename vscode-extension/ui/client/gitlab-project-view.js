// Гильдия → «GitLab»: связь этого проекта с GitLab.
//
// Подключение GitLab одно на машину и живёт в общих настройках («Интеграции
// и MCP»). Здесь только выбор проекта: по git remote, вручную, все мои
// проекты или не связывать. Не каждый проект обязан жить в GitLab, поэтому
// «не связан» — законное состояние, а не сбой: без красного и без запуска
// сервера плагина.

import { esc } from './html-escape.js'
import { glIcon, loadingHtml, verdictHtml } from './gitlab-views.js'

const MODES = { auto: 'по git remote', manual: 'выбран вручную' }

export function createGitLabProjectView({ getState, shell, toolPageHeading, bindingEditor }) {
  const settingsButton = (label, primary = false) =>
    `<button type="button" class="gl-btn${primary ? ' is-primary' : ''}" data-action="tab" data-tab="integrations">${esc(label)}</button>`

  // Проект, режим и ветку уже назвал вердикт; здесь — только то, из чего
  // Point их вывел.
  function facts(data, binding) {
    return binding.remote ? `<dl class="int-facts"><dt>git remote</dt><dd><code>${esc(binding.remote)}</code></dd></dl>` : ''
  }

  // Вердикт вместо плашки «связан»: что сейчас со связью и что сделать.
  function verdict(state, status, data, binding) {
    if (status.state !== 'ok') {
      return verdictHtml({ tone: 'bad', title: status.problem || 'GitLab не ответил', reasons: [status.fix].filter(Boolean) })
    }
    if (data.linked && binding.mode === 'all') {
      return verdictHtml({ tone: 'ok', title: 'Окно GitLab показывает все ваши проекты', reasons: ['MR, где вы автор или ревьюер', 'пайплайнов ветки в этом режиме нет'],
        actions: '<button type="button" class="gl-btn" data-action="gitlab-open-window">Окно GitLab</button>' })
    }
    if (data.linked) {
      return verdictHtml({ tone: 'ok', title: `Проект связан с ${binding.project}`, reasons: [MODES[binding.mode] || binding.mode, binding.branch ? `ветка ${binding.branch}` : ''],
        actions: '<button type="button" class="gl-btn is-primary" data-action="gitlab-open-window">Окно GitLab</button>' })
    }
    return verdictHtml({ tone: 'mute', glyph: 'mr', title: 'Проект не связан с GitLab', reasons: [binding.note || 'окно GitLab его не показывает, сервер плагина ради него не запускается'],
      actions: binding.detected ? `<button type="button" class="gl-btn is-primary" data-action="gitlab-link-detected"${state.busy ? ' disabled' : ''}>Связать с ${esc(binding.detected)}</button>` : '' })
  }

  function card(state) {
    const status = state.status
    if (!status) return loadingHtml('Спрашиваем ядро…')
    const data = status.data || {}
    const binding = data.binding || {}
    if (!data.configured) {
      return `<article class="int-plugin">
        <header><span class="int-plugin-mark">${glIcon('mr', 18)}</span><div><strong>${esc(binding.workspace || 'Этот проект')}</strong><small>GitLab ещё не подключён</small></div></header>
        ${verdictHtml({ tone: 'mute', glyph: 'settings', title: 'GitLab не подключён', reasons: ['адрес сервера и личный токен задаются один раз для всех проектов'], actions: settingsButton('Подключить в общих настройках', true) })}
      </article>`
    }
    return `<article class="int-plugin">
      <header><span class="int-plugin-mark">${glIcon('mr', 18)}</span><div><strong>${esc(binding.workspace || 'Этот проект')}</strong><small>${esc(data.url || '')}</small></div></header>
      ${verdict(state, status, data, binding)}
      ${facts(data, binding)}
      ${bindingEditor(state, binding, { cancel: false })}
      <footer class="int-actions"><i class="nc-gap"></i>${settingsButton('Подключение GitLab')}</footer>
    </article>`
  }

  function projectView() {
    const state = getState()
    return shell(`<main class="hub-page int-page int-project-gitlab">
      ${toolPageHeading('GitLab', 'Связь проекта с GitLab', 'Подключение GitLab общее для всех проектов. Здесь решается, показывать ли этот проект в окне GitLab и какой проект GitLab ему соответствует. Без выбора Point связывает проект сам, если git remote origin ведёт на подключённый сервер.', '')}
      ${state.error ? `<div class="int-problem is-banner">${glIcon('warning', 13)}<p><b>${esc(state.error)}</b></p><button type="button" class="nc-icon-btn" data-action="mcp-dismiss-error" aria-label="Скрыть">${glIcon('x', 12)}</button></div>` : ''}
      <section class="int-section" aria-label="Этот проект">${card(state)}</section>
    </main>`)
  }

  return { projectView }
}
