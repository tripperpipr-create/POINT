// Гильдия → «GitLab»: связь этого проекта с GitLab.
//
// Подключение GitLab одно на машину и живёт в общих настройках («Интеграции
// и MCP»). Здесь только выбор проекта: по git remote, вручную, все мои
// проекты или не связывать. Не каждый проект обязан жить в GitLab, поэтому
// «не связан» — законное состояние, а не сбой: без красного и без запуска
// сервера плагина.

import { esc } from './html-escape.js'
import { glIcon, loadingHtml } from './gitlab-views.js'

const MODES = { auto: 'по git remote', manual: 'выбран вручную' }

export function createGitLabProjectView({ getState, shell, toolPageHeading, bindingEditor }) {
  const settingsButton = (label, primary = false) =>
    `<button type="button" class="gl-btn${primary ? ' is-primary' : ''}" data-action="tab" data-tab="integrations">${esc(label)}</button>`

  function facts(data, binding) {
    const link = binding.mode === 'all' ? 'все мои проекты — MR, где вы автор или ревьюер'
      : data.linked ? `${binding.project} · ${MODES[binding.mode] || binding.mode}` : 'не связан'
    return `<dl class="int-facts">
      <dt>Проект GitLab</dt><dd>${esc(link)}</dd>
      ${data.linked && binding.branch && binding.mode !== 'all' ? `<dt>Ветка</dt><dd><code>${esc(binding.branch)}</code></dd>` : ''}
      ${binding.remote ? `<dt>git remote</dt><dd><code>${esc(binding.remote)}</code></dd>` : ''}
      ${!data.linked && binding.note ? `<dt>Почему</dt><dd>${esc(binding.note)}</dd>` : ''}
    </dl>`
  }

  function card(state) {
    const status = state.status
    if (!status) return loadingHtml('Спрашиваем ядро…')
    const data = status.data || {}
    const binding = data.binding || {}
    if (!data.configured) {
      return `<article class="int-plugin">
        <header><span class="int-plugin-mark">${glIcon('mr', 18)}</span><div><strong>GitLab не подключён</strong><small>Адрес сервера и личный токен задаются один раз для всех проектов</small></div><span class="int-state is-mute">нет подключения</span></header>
        <footer class="int-actions">${settingsButton('Подключить в общих настройках', true)}</footer>
      </article>`
    }
    const failed = status.state !== 'ok'
    const pill = failed ? ['bad', 'ошибка'] : data.linked ? ['ok', 'связан'] : ['mute', 'не связан']
    return `<article class="int-plugin">
      <header><span class="int-plugin-mark">${glIcon('mr', 18)}</span><div><strong>${esc(binding.workspace || 'Этот проект')}</strong><small>${esc(data.url || '')}</small></div><span class="int-state is-${pill[0]}">${esc(pill[1])}</span></header>
      ${facts(data, binding)}
      ${failed ? `<div class="int-problem">${glIcon('warning', 13)}<p><b>${esc(status.problem || 'GitLab не ответил')}</b>${status.fix ? `<span>${esc(status.fix)}</span>` : ''}</p></div>` : ''}
      ${bindingEditor(state, binding, { cancel: false })}
      <footer class="int-actions">${data.linked ? '<button type="button" class="gl-btn" data-action="gitlab-open-window">Окно GitLab</button>' : ''}${!data.linked && binding.detected ? `<button type="button" class="gl-btn" data-action="gitlab-link-detected"${state.busy ? ' disabled' : ''}>Связать с ${esc(binding.detected)}</button>` : ''}<i class="nc-gap"></i>${settingsButton('Подключение GitLab')}</footer>
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
