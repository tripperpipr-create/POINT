import { icon } from './ui-icons.js'
import { countOf } from './format-units.js'

// Общие настройки — список, как в JetBrains: разделы, строка «подпись и
// пояснение слева — управление справа», тонкие линии между строками. Облик
// выбрал владелец по снимкам стенда 30 сентября 2026 (вариант A). До этого
// страница была набором разнокалиберных карточек: моноширинный заголовок
// вразрядку, «Быстрый чат» посередине страницы, бюджет рамкой в рамке.
// Поля остаются теми же элементами с теми же id — их читают form-submit.js
// и обработчик «save-quick-chat».

function dollars(cents) { return cents > 0 ? (cents / 100).toFixed(2) : '' }
function money(cents) { return (Number(cents || 0) / 100).toFixed(2).replace('.', ',') }

const escapeText = value => String(value ?? '').replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char]))

function head(title, text, aside = '') {
  return `<header class="gs-head"><div><h1>${title}</h1><p>${text}</p></div>${aside}</header>`
}

function linkRow(glyph, title, text, tab) {
  return `<button type="button" class="gs-row is-link" data-action="tab" data-tab="${tab}">${icon(glyph)}<span><b>${title}</b><small>${text}</small></span><em>Настроить${icon('chevron-right')}</em></button>`
}

export function generalSettingsView({ shell, profiles, quickChatSettingsHtml, stats = {}, status = 'idle', connections = [] }) {
  const linked = connections.length ? countOf(connections.length, 'подключение', 'подключения', 'подключений') : 'Подключений пока нет'
  const ready = status === 'ready'
  return shell(`<main class="hub-page general-settings-page gs">
    ${head('Общие настройки', 'Действуют во всех проектах Point. Настройки отдельного проекта — в «Настройках проекта».')}
    <section class="gs-section"><h2>Модели и инструменты</h2>
      ${linkRow('chip', 'Подключения к моделям', `${linked} · провайдеры, адреса, ключи и каталог моделей`, 'model-connections')}
      ${linkRow('plug', 'Интеграции и свои MCP', 'GitLab, MCP-серверы и доступные инструменты', 'integrations')}
    </section>
    <section class="gs-section"><h2>Быстрый чат <kbd>Ctrl+Shift+L</kbd></h2>
      <div class="gs-row">${icon('bolt')}<span><b>Исполнитель по умолчанию</b><small>Кто возьмёт задачу, если в быстром чате не назвать агента</small></span>${quickChatSettingsHtml(profiles)}</div>
    </section>
    <section class="gs-section"><h2>Общий бюджет</h2>
      ${status === 'error' ? '<p class="gs-alert" role="alert">Не удалось загрузить общий бюджет. Повторите загрузку, прежде чем менять лимиты. <button type="button" class="gs-button" data-action="reload-statistics">Повторить</button></p>' : ''}
      <form id="global-budget-form" class="gs-form">
        <label class="gs-row">${icon('wallet')}<span><b>Дневной лимит</b><small>Сегодня потрачено ${money(stats.globalDailySpentCents)} $ · в резерве ${money(stats.globalDailyReservedCents)} $</small></span><span class="gs-money"><input id="global-budget-daily" type="number" min="0" step="0.01" value="${dollars(Number(stats.globalBudgetDailyCents || 0))}" placeholder="Без лимита" aria-label="Дневной лимит в долларах"><i>$</i></span></label>
        <label class="gs-row"><i></i><span><b>Месячный лимит</b><small>В этом месяце ${money(stats.globalMonthlySpentCents)} $ · в резерве ${money(stats.globalMonthlyReservedCents)} $</small></span><span class="gs-money"><input id="global-budget-monthly" type="number" min="0" step="0.01" value="${dollars(Number(stats.globalBudgetMonthlyCents || 0))}" placeholder="Без лимита" aria-label="Месячный лимит в долларах"><i>$</i></span></label>
        <label class="gs-row"><i></i><span><b>Останавливать новые запуски</b><small>При превышении лимита; проверяется вместе с бюджетом проекта</small></span><span class="gs-switch"><input id="global-budget-hard-stop" type="checkbox" ${stats.globalBudgetHardStop ? 'checked' : ''}><span aria-hidden="true"></span></span></label>
        <footer class="gs-foot"><small>Пустое поле — без лимита. Лимиты считаются суммарно по всем проектам.</small><button type="submit" class="gs-button" ${ready ? '' : 'disabled'}>${status === 'loading' ? 'Загружаем…' : 'Сохранить лимиты'}</button></footer>
      </form>
      ${stats.globalDailyBudgetWarning || stats.globalMonthlyBudgetWarning ? '<p class="gs-alert" role="status">Общий бюджет приближается к лимиту или исчерпан. Проверьте учтённый расход и резервы.</p>' : ''}
    </section>
  </main>`)
}

export function modelConnectionsSettingsView({ shell, connectionManagerHtml, editingId, count, defaults, connections = [], esc = escapeText, note = '' }) {
  const rows = [['master', 'Мастер', 'Отвечает в чате и собирает задания'], ['archivist', 'Архивариус', 'Сводит историю и память'], ['agent', 'Новые персонажи', 'Модель для агентов, нанятых после этого']].map(([role, label, hint]) => {
    const choice = defaults?.[role] || {}
    return `<div class="gs-row">${icon(role === 'agent' ? 'team' : role === 'master' ? 'chat' : 'memory')}<span><b>${label}</b><small>${hint}</small></span><span class="gs-pair"><select data-global-model-connection="${role}" aria-label="Подключение: ${label}"><option value="">${role === 'master' ? 'Движок Point' : 'Наследовать'}</option>${connections.map(item => `<option value="${esc(item.id)}"${item.id === choice.connectionId ? ' selected' : ''}>${esc(item.displayName || item.id)}</option>`).join('')}</select><input data-global-model-id="${role}" value="${esc(choice.model || '')}" placeholder="Модель из подключения" aria-label="Модель: ${label}"></span></div>`
  }).join('')
  return shell(`<main class="hub-connections hub-page general-settings-page gs">
    ${head('Подключения к моделям', 'Доступны во всех проектах Point. Ключи хранятся в защищённом хранилище IDE.', `<em class="gs-count">${countOf(Number(count || 0), 'подключение', 'подключения', 'подключений')}</em>`)}
    <section class="gs-section"><h2>Модели по умолчанию</h2>
      ${rows}
      <footer class="gs-foot"><small>Выбираются один раз для всех проектов. Существующие персонажи сохраняют свои модели.</small><button type="button" class="gs-button" data-action="save-global-models">Сохранить модели</button></footer>
      ${note ? `<p class="gs-alert" role="status">${esc(note)}</p>` : ''}
    </section>
    ${connectionManagerHtml({ editingId })}
  </main>`)
}
