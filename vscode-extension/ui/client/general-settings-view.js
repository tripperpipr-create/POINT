function dollars(cents) { return cents > 0 ? (cents / 100).toFixed(2) : '' }
function money(cents) { return (Number(cents || 0) / 100).toFixed(2) }

export function generalSettingsView({ shell, profiles, quickChatSettingsHtml, stats = {}, status = 'idle' }) {
  return shell(`<main class="hub-page general-settings-page">
    <header class="hub-page-head"><div><span>Point</span><h1>Общие настройки</h1><p>Параметры, которыми пользуются все проекты. Настройки отдельного проекта остаются в его разделах.</p></div></header>
    <div class="general-settings-links">
      <button type="button" class="general-settings-link" data-action="tab" data-tab="model-connections"><strong>Подключения к моделям</strong><span>Провайдеры, адреса, ключи и каталог моделей</span><i aria-hidden="true">→</i></button>
      <button type="button" class="general-settings-link" data-action="tab" data-tab="integrations"><strong>Интеграции и свои MCP</strong><span>GitLab, MCP-серверы и доступные инструменты</span><i aria-hidden="true">→</i></button>
    </div>
    ${quickChatSettingsHtml(profiles)}
    <section class="general-settings-project-note"><h2>Общий бюджет</h2><p>Лимиты действуют суммарно для всех проектов. Пустое поле отключает лимит периода. При жёсткой остановке запуск с неизвестной стоимостью потребует профиль цены.</p>
      ${status === 'error' ? '<p role="alert">Не удалось загрузить общий бюджет. Повторите загрузку, прежде чем менять лимиты.</p><button type="button" class="secondary" data-action="reload-statistics">Повторить загрузку</button>' : ''}
      <form id="global-budget-form" class="budget-form"><label>Дневной лимит, $<input id="global-budget-daily" type="number" min="0" step="0.01" value="${dollars(Number(stats.globalBudgetDailyCents || 0))}" placeholder="Не ограничен"></label><label>Месячный лимит, $<input id="global-budget-monthly" type="number" min="0" step="0.01" value="${dollars(Number(stats.globalBudgetMonthlyCents || 0))}" placeholder="Не ограничен"></label><label class="budget-hard-stop"><input id="global-budget-hard-stop" type="checkbox" ${stats.globalBudgetHardStop ? 'checked' : ''}><span><strong>Останавливать новые запуски</strong><small>Общий лимит проверяется вместе с бюджетом проекта.</small></span></label><button type="submit" class="primary" ${status !== 'ready' ? 'disabled' : ''}>${status === 'loading' ? 'Загрузка…' : 'Сохранить общий бюджет'}</button></form>
      <p class="general-budget-usage">Сегодня: ${money(stats.globalDailySpentCents)} $ расхода · ${money(stats.globalDailyReservedCents)} $ зарезервировано. Месяц: ${money(stats.globalMonthlySpentCents)} $ расхода · ${money(stats.globalMonthlyReservedCents)} $ зарезервировано.</p>
      ${stats.globalDailyBudgetWarning || stats.globalMonthlyBudgetWarning ? '<p role="status">Общий бюджет приближается к лимиту или исчерпан. Проверьте учтённый расход и резервы.</p>' : ''}
    </section>
  </main>`)
}

export function modelConnectionsSettingsView({ shell, connectionManagerHtml, editingId, count, defaults, connections = [], esc = value => String(value ?? '').replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char])), note = '' }) {
  const rows = [['master', 'Мастер'], ['archivist', 'Архивариус'], ['agent', 'Новые персонажи']].map(([role, label]) => {
    const choice = defaults?.[role] || {}
    return `<label><strong>${label}</strong><select data-global-model-connection="${role}"><option value="">${role === 'master' ? 'Движок Point' : 'Наследовать'}</option>${connections.map(item => `<option value="${esc(item.id)}"${item.id === choice.connectionId ? ' selected' : ''}>${esc(item.displayName || item.id)}</option>`).join('')}</select><input data-global-model-id="${role}" value="${esc(choice.model || '')}" placeholder="Model ID из подключения"></label>`
  }).join('')
  return shell(`<main class="hub-connections hub-page general-settings-page"><header class="hub-page-head"><div><span>Общие настройки</span><h1>Подключения к моделям</h1><p>Подключения доступны во всех проектах Point. Ключи хранятся в защищённом хранилище IDE.</p></div><em>${count}</em></header><section class="general-settings-project-note"><h2>Модели по умолчанию</h2><p>Выбираются один раз для всех проектов. Существующие персонажи сохраняют свои модели; проект может явно задать исключение для Мастера.</p>${rows}<button type="button" class="primary" data-action="save-global-models">Сохранить модели</button>${note ? `<p role="status">${esc(note)}</p>` : ''}</section>${connectionManagerHtml({ editingId })}</main>`)
}
