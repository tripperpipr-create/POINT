// Мастер настройки компаньона: навыки, шаги и сводка последнего экрана.
// Он только читает состояние — черновик, шаг и результат пробы остаются в
// main.js, сюда они заходят готовыми значениями и геттерами. Разметку роли,
// стиля и черт считает companion-studio-views.js; здесь — рамка вокруг неё.

export function createCompanionSetupWizard({
  root,
  esc,
  getState,
  COMPANION_PRESETS,
  COMPANION_EXAMPLES,
  COMPANION_SETUP_STEPS,
  COMPANION_TOOL_GROUPS,
  COMPANION_SKILL_LIMIT,
  companionModeCardsHtml,
  normalizeCompanionSkillIds,
  sanitizeCompanionSetupDraft,
  companionConnections,
  ensureCompanionSetupDraft,
  companionConnectionFieldsHtml,
  companionPresetStudioHtml,
  companionStyleStudioHtml,
  companionPersonalityControlsHtml,
  companionPersonalityPreviewHtml,
  companionRoleShowcaseHtml,
  companionActCompareHtml,
  companionPredictedReplyHtml,
  companionSetupTestAnswerHtml,
  getCompanionLoading,
  getSetupStep,
  getProviderProbe,
  getSetupStatus,
  getSetupQuiet,
}) {
  function companionToolGranted(name) {
    const tool = (getState().boot?.toolCatalog || []).find(item => item.name === name)
    if (!tool) return false
    return COMPANION_TOOL_GROUPS.includes(String(tool.category || '').toLowerCase())
  }
  function companionSkillBlockers(skill) {
    const required = Array.isArray(skill?.requiredTools) ? skill.requiredTools : []
    return required.filter(tool => !companionToolGranted(tool))
  }
  // Навык, надетый до того, как у него появилось лишнее умение, остаётся в
  // конфиге. Молча снять его нельзя — это решение человека, — поэтому он виден
  // отмеченным и с причиной, а сохранение до снятия не пускает.
  function companionBlockedSkills(value) {
    const skills = getState().boot?.skills || []
    return normalizeCompanionSkillIds(value?.skillIds)
      .map(id => skills.find(item => item.id === id))
      .filter(item => item && companionSkillBlockers(item).length)
  }
  // Каталог навыков приходит из ядра и может отстать от конфига: навык приезжает
  // переносом или продвижением выученного. Невидимую отметку форма не отдаёт, и
  // прочитать её как «снято» значило бы потерять чужой навык на ровном месте.
  function companionSkillSelection(value) {
    const draft = normalizeCompanionSkillIds(value?.skillIds)
    const inputs = [...root.querySelectorAll('input[name="companion-skill"]')]
    if (!inputs.length) return draft
    const shown = new Set(inputs.map(item => item.value))
    return [...inputs.filter(item => item.checked).map(item => item.value), ...draft.filter(id => !shown.has(id))]
  }
  function companionSkillNoteLines(skill, instance) {
    const notes = []
    const blockers = companionSkillBlockers(skill)
    if (blockers.length) notes.push(`нужны умения вне доступа помощника: ${blockers.join(', ')}`)
    if (instance && instance.enabled === false) notes.push('выключен в этом проекте — надетым он не применится')
    if (skill.configuration?.lifecycleStatus === 'deprecated') notes.push('устарел')
    return notes
  }
  function companionSkillsPanelHtml(value) {
    const draft = sanitizeCompanionSetupDraft(value)
    const skills = getState().boot?.skills || []
    const instances = getState().boot?.projectSkills || []
    const selected = new Set(draft.skillIds)
    const blockedLines = companionBlockedSkills(draft)
      .map(item => `${esc(item.name || item.id)}: ${companionSkillBlockers(item).map(esc).join(', ')} — снимите отметку, иначе ядро не примет настройку.`)
    const warning = blockedLines.length ? `<aside class="create-step-error">${blockedLines.join('<br>')}</aside>` : ''
    const cards = skills.map(item => {
      const on = selected.has(item.id)
      const blocked = companionSkillBlockers(item).length > 0
      const deprecated = item.configuration?.lifecycleStatus === 'deprecated'
      const notes = companionSkillNoteLines(item, instances.find(entry => entry.skillId === item.id))
      // Отметить нечего, если навык помощнику не по доступу, устарел или список
      // уже полон. Снять отметку можно всегда — иначе из тупика нет выхода.
      const locked = !on && (blocked || deprecated || selected.size >= COMPANION_SKILL_LIMIT)
      const classes = ['tool-toggle', on ? 'is-on' : '', deprecated ? 'is-deprecated' : ''].filter(Boolean).join(' ')
      const description = esc(item.description || item.summary || 'Описание не указано')
      return `<label class="${classes}"><input type="checkbox" name="companion-skill" value="${esc(item.id)}" ${on ? 'checked' : ''} ${locked ? 'disabled' : ''}><span><strong>${esc(item.name || item.id)}</strong><small>${description}</small>${notes.length ? `<small>${esc(notes.join(' · '))}</small>` : ''}</span></label>`
    }).join('')
    const grid = skills.length
      ? `<div class="constructor-skill-grid">${cards}</div>`
      : '<p class="muted">Каталог навыков пуст. Помощник ответит своими практиками; навык создаётся в разделе «Навыки».</p>'
    return `<section class="companion-setup-panel${getSetupQuiet() ? '' : ' companion-step-enter'}"><header class="companion-step-heading"><h3>Навыки помощника</h3><p>Практики, которые помощник применяет в ответах. Они его собственные: у агента практики свои, и отсюда они не переносятся.</p></header><p class="skill-form-hint">Отметка навыка не выдаёт помощнику новых умений: он по-прежнему только читает проект, индекс и справку по git. Поэтому навык, которому нужно умение вне этого круга, ему не надевается. Полные инструкции навыка помощник подгружает отдельным вызовом уже в разговоре. Надето ${selected.size} из ${skills.length}; ядро принимает не больше ${COMPANION_SKILL_LIMIT}.</p>${warning}${grid}</section>`
  }
  function companionSetupStepHtml(value) {
    const draft = sanitizeCompanionSetupDraft(value)
    if (getSetupStep() === 'role') {
      return `<section class="companion-setup-panel${getSetupQuiet() ? '' : ' companion-step-enter'}"><header class="companion-step-heading"><h3>Выберите роль компаньона</h3><p>Пресет задаёт тон во всей IDE. Сцену ниже можно переключать — ответ меняется сразу.</p></header>${companionPresetStudioHtml(draft.preset)}${companionRoleShowcaseHtml(draft)}</section>`
    }
    if (getSetupStep() === 'personality') {
      return `<section class="companion-setup-panel${getSetupQuiet() ? '' : ' companion-step-enter'}"><header class="companion-step-heading"><h3>Характер компаньона</h3><p>Шесть черт. Сдвиг слайдера переключает роль на «Свой характер».</p></header><div class="companion-personality-layout single">${companionStyleStudioHtml(draft.preset)}${companionPersonalityControlsHtml(draft)}</div></section>`
    }
    if (getSetupStep() === 'skills') {
      return companionSkillsPanelHtml(draft)
    }
    if (getSetupStep() === 'brain') {
      return `<section class="companion-setup-panel${getSetupQuiet() ? '' : ' companion-step-enter'}"><header class="companion-step-heading"><h3>Мозг компаньона</h3><p>Помощник отвечает только через HTTP API: выберите связь и модель. CLI-исполнители сняты.</p></header>${companionModeCardsHtml(draft)}${companionConnectionFieldsHtml(draft)}<aside class="companion-safety-strip"><b>Всегда безопасно</b><span>Компаньон только рекомендует. Запуск остаётся за вами.</span></aside></section>`
    }
    if (getSetupStep() === 'boundaries') {
      return `<section class="companion-setup-panel${getSetupQuiet() ? '' : ' companion-step-enter'}"><header class="companion-step-heading"><h3>Границы действий</h3><p>По умолчанию компаньон советует. Действия — только если вы явно разрешите готовить их по команде.</p></header><div class="companion-boundary-layout"><label class="companion-auto-act featured"><input id="companion-auto-act" type="checkbox" ${draft.autoAct ? 'checked' : ''}><span><strong>Разрешить готовить действия по явной команде</strong><small>Например: «создай Flow». Результат всё равно появится как карточка на ревью; скрытых изменений не будет.</small></span></label><label class="companion-auto-act"><input id="companion-auto-open-critical" type="checkbox" ${draft.autoOpenChatOnCritical ? 'checked' : ''}><span><strong>Авто-открывать чат при критичном сигнале</strong><small>По умолчанию выкл. Компаньон молча наблюдает; при critical может открыть быстрый peek-чат с черновиком.</small></span></label><label class="companion-auto-act"><input id="companion-auto-send-model" type="checkbox" ${draft.autoSendModelPrompt ? 'checked' : ''}><span><strong>Авто-отправлять вопрос модели</strong><small>По умолчанию выкл. Работает только вместе с авто-открытием; иначе черновик останется без отправки.</small></span></label>${companionActCompareHtml(draft)}<div class="companion-guardrails"><strong>Границы, которые нельзя отключить</strong><span><b>01</b><small>Спрашивает только про архитектуру, безопасность и критичный результат</small></span><span><b>02</b><small>Все предложения привязаны к фактам текущего workspace</small></span><span><b>03</b><small>Quest, Agent, Team, Flow и Skill проходят явный просмотр</small></span><span><b>04</b><small>Ключ модели хранится в SecretStorage и не уходит в Point Core</small></span></div></div><details class="companion-advanced-model"><summary>Дополнительно: температура и длина ответа</summary><div class="companion-response-tuning"><label>Температура<input id="companion-setup-temperature" type="range" min="0" max="2" step="0.05" value="${Number(draft.temperature)}"><output>${Number(draft.temperature).toFixed(2)}</output><small>0 — строго и стабильно · 2 — максимально вариативно</small></label><label>Максимальный ответ<input id="companion-setup-max-output" type="number" min="128" max="8192" step="128" value="${Number(draft.maxOutputTokens)}"><small>От 128 до 8192 токенов</small></label></div></details></section>`
    }
    const connection = companionConnections().find(item => item.id === draft.connectionId)
    const selectedExample = COMPANION_EXAMPLES.find(item => item.prompt === draft.examplePrompt) || COMPANION_EXAMPLES[0]
    return `<section class="companion-setup-panel${getSetupQuiet() ? '' : ' companion-step-enter'}"><header class="companion-step-heading"><h3>Проверьте компаньона в деле</h3><p>Выберите безопасный пример. Сначала виден прогноз ответа, затем можно сохранить и запустить его в чате.</p></header><div class="companion-final-summary"><span><small>РОЛЬ</small><strong>${esc(COMPANION_PRESETS.find(item => item.id === draft.preset)?.label || 'Свой')}</strong></span><span><small>МОЗГ</small><strong>Модель по API</strong></span><span><small>МОДЕЛЬ</small><strong>${esc(draft.model || connection?.displayName || draft.providerPreset || 'не выбрана')}</strong></span><span><small>НАВЫКИ</small><strong>${draft.skillIds.length ? String(draft.skillIds.length) : 'нет'}</strong></span><span><small>ДЕЙСТВИЯ</small><strong>${draft.autoAct ? 'Готовит по команде' : 'Только советует'}</strong></span></div><p class="companion-orchestration-note">Компаньон рекомендует план. Запуск — через карточку наряда у Мастера.</p><div class="companion-scenario-grid">${COMPANION_EXAMPLES.map(item => `<button type="button" class="companion-scenario ${item.prompt === selectedExample.prompt ? 'selected' : ''}" data-action="companion-select-example" data-prompt="${esc(item.prompt)}"><span>${esc(item.icon)}</span><strong>${esc(item.title)}</strong><p>${esc(item.prompt)}</p><small>${esc(item.result)}</small></button>`).join('')}</div><aside class="companion-test-console"><header><span>ПРОБНЫЙ ЗАПРОС</span><em>модель + контекст проекта</em></header><p>${esc(selectedExample.prompt)}</p>${companionPredictedReplyHtml(draft, selectedExample)}${companionSetupTestAnswerHtml()}<button type="button" class="primary" data-action="save-test-companion" ${getCompanionLoading() ? 'disabled' : ''}>${getCompanionLoading() ? 'Компаньон отвечает…' : 'Сохранить и запустить пример'}</button></aside></section>`
  }
  function companionSetupStatusHtml() {
    if (!getSetupStatus()) return ''
    const text = String(getSetupStatus())
    const ok = /сохранен|проверен|работает|добавлен в историю|выберите модель/i.test(text)
      && !/не удалось|нужен токен|укажите|должна|должен|не ответила|локальный ответ/i.test(text)
    if (getSetupStep() === 'brain' && getProviderProbe() && ok && /проверен|работает|выберите модель/i.test(text)) return ''
    return `<p class="companion-setup-status ${ok ? 'ok' : 'error'}">${esc(text)}</p>`
  }
  function companionSetupWizardHtml() {
    const draft = ensureCompanionSetupDraft()
    const index = Math.max(0, COMPANION_SETUP_STEPS.findIndex(item => item.id === getSetupStep()))
    const current = COMPANION_SETUP_STEPS[index] || COMPANION_SETUP_STEPS[0]
    const showDock = getSetupStep() === 'role' || getSetupStep() === 'personality' || getSetupStep() === 'examples'
    const footerPrimary = index < COMPANION_SETUP_STEPS.length - 1
      ? `<button type="button" class="primary" data-action="companion-setup-move" data-direction="1">Далее →</button>`
      : `<button type="submit" class="secondary">Сохранить без проверки</button>`
    const back = index > 0 ? `<button type="button" class="secondary" data-action="companion-setup-move" data-direction="-1">← Назад</button>` : ''
    return `<form id="companion-setup-form" class="companion-setup-shell${showDock ? ' has-dock' : ''}"><header class="companion-setup-bar"><div><span>Настройка компаньона</span><strong>${esc(current.label)}</strong><small>Шаг ${index + 1} из ${COMPANION_SETUP_STEPS.length}</small></div><button type="button" class="secondary" data-action="close-companion-setup">Закрыть</button></header><nav class="companion-setup-steps">${COMPANION_SETUP_STEPS.map((item, stepIndex) => `<button type="button" class="${item.id === getSetupStep() ? 'active' : ''} ${stepIndex < index ? 'complete' : ''}" data-action="companion-setup-step" data-step="${esc(item.id)}"><b>${stepIndex < index ? '✓' : String(stepIndex + 1).padStart(2, '0')}</b><span><strong>${esc(item.label)}</strong><small>${esc(item.hint)}</small></span></button>`).join('')}</nav><div class="companion-setup-stage"><div class="companion-setup-content">${companionSetupStatusHtml()}${companionSetupStepHtml(draft)}<footer class="companion-setup-footer">${back}<span></span>${footerPrimary}</footer></div>${showDock ? companionPersonalityPreviewHtml(draft) : ''}</div></form>`
  }

  return {
    companionSkillBlockers,
    companionBlockedSkills,
    companionSkillSelection,
    companionSetupStatusHtml,
    companionSetupWizardHtml,
  }
}
