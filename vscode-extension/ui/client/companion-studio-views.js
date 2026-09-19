import { fillAttribute } from './format-units.js'
import { COMPANION_EXAMPLES, companionSceneById } from './companion-compose.js'

// Студия характера компаньона: роль, стиль, шесть черт и живой образец ответа.
// Всё это лежало в main.js вперемешку с мастером настройки, хотя наружу отсюда
// смотрят только тринадцать отрисовщиков — остальное считает образец реплики по
// слайдерам и за пределами студии не нужно.
export const COMPANION_PRESETS = [
  { id: 'technical-lead', icon: '⌘', label: 'Техлид', hint: 'Баланс архитектуры, поставки и качества', opener: 'Сначала зафиксируем поставку и границы изменения.', does: ['Квесты', 'Логи IDE', 'Агенты'], values: { criticality: 65, creativity: 45, verbosity: 55, initiative: 65, questionStrictness: 60, riskTolerance: 25 } },
  { id: 'critical-architect', icon: '◇', label: 'Критичный архитектор', hint: 'Ищет слабые места и требует доказательств', opener: 'Не стартую, пока не ясны границы текущей auth-системы.', does: ['Ревью кода', 'Проблемы', 'Архитектура'], values: { criticality: 90, creativity: 40, verbosity: 65, initiative: 55, questionStrictness: 80, riskTolerance: 10 } },
  { id: 'mentor', icon: '✦', label: 'Наставник', hint: 'Объясняет решения и развивает проект', opener: 'Разберём задачу так, чтобы решение было понятно и проверяемо.', does: ['Объясняет код', 'Учит на логах', 'Проверяемый план'], values: { criticality: 55, creativity: 55, verbosity: 80, initiative: 60, questionStrictness: 55, riskTolerance: 20 } },
  { id: 'product-engineer', icon: '↗', label: 'Продакт-инженер', hint: 'Быстро превращает цель в рабочий квест', opener: 'Превращу цель в короткий проверяемый квест.', does: ['Квесты', 'Код', 'Готово, когда'], values: { criticality: 55, creativity: 75, verbosity: 50, initiative: 85, questionStrictness: 40, riskTolerance: 40 } },
  { id: 'custom', icon: '◌', label: 'Свой характер', hint: 'Полный ручной контроль шести параметров', opener: 'Соберу план по вашим шести чертам.', does: ['Вся IDE', 'Ваш тон'], values: { criticality: 50, creativity: 50, verbosity: 50, initiative: 50, questionStrictness: 70, riskTolerance: 30 } },
  { id: 'balanced', icon: '◎', label: 'Сбалансированный', hint: 'Универсальный компаньон для любого проекта', opener: 'Соберу план по фактам текущего проекта.', does: ['Логи и код', 'Квесты и агенты'], values: { criticality: 50, creativity: 50, verbosity: 50, initiative: 50, questionStrictness: 70, riskTolerance: 30 } },
  { id: 'cautious', icon: '▣', label: 'Осторожный', hint: 'Больше вопросов и минимальный допуск риска', opener: 'Иду осторожно: сначала неизвестные, потом план.', does: ['Проблемы', 'Минимум риска'], values: { criticality: 80, creativity: 30, verbosity: 60, initiative: 35, questionStrictness: 85, riskTolerance: 15 } },
  { id: 'proactive', icon: '⚡', label: 'Инициативный', hint: 'Чаще предлагает квесты и улучшения', opener: 'Уже вижу состав отряда и подходящий Flow.', does: ['Сам ставит квесты', 'Ловит падения сборок'], values: { criticality: 60, creativity: 70, verbosity: 60, initiative: 85, questionStrictness: 45, riskTolerance: 40 } },
  { id: 'minimal', icon: '—', label: 'Минималист', hint: 'Краткие ответы и меньше подсказок', opener: 'Короткий план.', does: ['Коротко по логам', 'Мало вмешательств'], values: { criticality: 45, creativity: 20, verbosity: 20, initiative: 20, questionStrictness: 30, riskTolerance: 30 } },
]
const COMPANION_CANONICAL_PRESET_IDS = ['technical-lead', 'critical-architect', 'mentor', 'product-engineer', 'custom']
const COMPANION_TRAIT_FIELDS = [
  { id: 'criticality', label: 'Критичность', hint: 'Искать проблемы и оспаривать слабые решения', low: 'мягче', high: 'жёстче' },
  { id: 'creativity', label: 'Креативность', hint: 'Сколько альтернатив и нестандартных путей предлагать', low: 'прямо', high: 'варианты' },
  { id: 'verbosity', label: 'Подробность', hint: 'Насколько подробно объяснять причины', low: 'кратко', high: 'подробно' },
  { id: 'initiative', label: 'Инициатива', hint: 'Как часто сам вмешивается подсказками', low: 'по запросу', high: 'сам' },
  { id: 'questionStrictness', label: 'Строгость вопросов', hint: 'Уточнять только архитектуру, безопасность и критичный результат', low: 'допускает', high: 'уточняет' },
  { id: 'riskTolerance', label: 'Допуск риска', hint: 'Насколько смело предлагать эксперименты', low: 'осторожно', high: 'смелее' },
]

export function createCompanionStudioViews({
  root,
  esc,
  sanitizeCompanionSetupDraft,
  companionSelectedScene,
  getProviderProbe,
  getSetupTestResult,
}) {
  function companionBehaviorLines(value) {
    const draft = sanitizeCompanionSetupDraft(value)
    return [
      draft.criticality >= 70 ? 'Ищет скрытые риски и оспаривает слабые решения.' : draft.criticality <= 30 ? 'Фокусируется на помощи, вмешивается только при явных проблемах.' : 'Проверяет ключевые решения без избыточной критики.',
      draft.creativity >= 70 ? 'Предлагает несколько альтернатив и нестандартные декомпозиции.' : 'Предпочитает прямой и проверяемый путь.',
      draft.verbosity >= 70 ? 'Объясняет причины и контекст подробно.' : draft.verbosity <= 30 ? 'Отвечает коротко, только по существу.' : 'Даёт компактное объяснение и следующий шаг.',
      draft.initiative >= 70 ? 'Сам замечает ошибки, расходы и слабые места Flow.' : draft.initiative <= 30 ? 'Почти не отвлекает от работы без прямого вопроса.' : 'Подключается, когда рекомендация заметно улучшит результат.',
      draft.questionStrictness >= 70 ? 'Уточняет архитектуру и безопасность до предложения.' : 'Чаще принимает безопасные допущения из контекста проекта.',
      draft.riskTolerance <= 25 ? 'Требует тестов, ревью и плана отката для рискованных изменений.' : 'Допускает быстрые эксперименты, сохраняя подтверждение мутаций.',
    ]
  }
  function companionLiveTone(value) {
    const draft = sanitizeCompanionSetupDraft(value)
    return {
      draft,
      preset: COMPANION_PRESETS.find(item => item.id === draft.preset),
      ask: draft.questionStrictness >= 70,
      critical: draft.criticality >= 70,
      creative: draft.creativity >= 70,
      verbose: draft.verbosity >= 70,
      terse: draft.verbosity <= 30,
      proactive: draft.initiative >= 70,
      quiet: draft.initiative <= 30,
      cautious: draft.riskTolerance <= 25,
    }
  }
  function companionTraitLiveLine(id, raw) {
    const value = Number(raw)
    const lines = {
      criticality: value >= 70 ? 'Будет оспаривать слабые решения и искать скрытые риски.' : value <= 30 ? 'Вмешается только при явной проблеме.' : 'Проверит ключевые решения без лишней критики.',
      creativity: value >= 70 ? 'Предложит несколько путей, а не один очевидный.' : 'Пойдёт прямым проверяемым маршрутом.',
      verbosity: value >= 70 ? 'Объяснит почему, а не только что делать.' : value <= 30 ? 'Ответит коротко, без лекции.' : 'Даст компактное объяснение и следующий шаг.',
      initiative: value >= 70 ? 'Сам заметит ошибки, расходы и слабые места Flow.' : value <= 30 ? 'Почти не отвлечёт без прямого вопроса.' : 'Подключится, когда совет заметно улучшит результат.',
      questionStrictness: value >= 70 ? 'Спросит только то, что меняет архитектуру или безопасность.' : 'Примет безопасные допущения из проекта.',
      riskTolerance: value <= 25 ? 'Потребует тесты, ревью и план отката.' : 'Допустит быстрый эксперимент, мутации всё равно на ревью.',
    }
    return lines[id] || ''
  }
  function companionSampleReplyText(value, sceneId) {
    const tone = companionLiveTone(value)
    const scene = companionSceneById(sceneId || companionSelectedScene(tone.draft).id)
    const opener = tone.preset?.opener || 'Соберу план по фактам текущего проекта.'
    const action = tone.draft.autoAct ? 'По команде подготовлю карточку на ревью — скрытого запуска не будет.' : 'По умолчанию только совет: запустить, изменить или отклонить.'
    if (scene.id === 'logs') {
      return [opener, tone.critical ? 'Сначала падения сборки и ошибки в «Проблемах» — не косметические предупреждения.' : 'Соберу активные «Проблемы» и последние неуспешные команды терминала.', tone.verbose ? 'Для каждой ошибки укажу файл, вероятную причину и проверяемый фикс.' : 'Дам короткий список: где упало и что чинить первым.', tone.cautious ? 'Квест на правку подготовлю, но без вашего «запустить» не начну.' : 'Могу сразу предложить квест на устранение.', action].filter(Boolean).join(' ')
    }
    if (scene.id === 'code') {
      return [opener, tone.ask ? 'OAuth должен использовать существующую систему сессий или полностью заменить её?' : 'Опираюсь на текущий стек авторизации, без лишних вопросов.', tone.critical ? 'Риск: вторая прослойка авторизации и обход текущих сессий.' : '', tone.creative ? 'Альтернатива: расширить текущие сессии через OIDC, не поднимая второй вход.' : 'Пойду прямым путём в существующую архитектуру.', tone.cautious ? 'Код только через песочницу и тесты до применения.' : 'После осмотра плана агент сможет писать код в песочнице.', action].filter(Boolean).join(' ')
    }
    if (scene.id === 'agent') {
      return [opener, tone.proactive ? 'Соберу разработчика бэкенда под языки и соглашения этого репозитория.' : 'Предложу карточку агента на осмотр — без скрытой записи в ростер.', tone.ask ? 'Нужен ли запуск команд или хватит чтения файлов и поиска по коду?' : 'Умения возьму из роли, права скрытно не расширю.', 'Дальше код пишет он в прогоне, а не компаньон напрямую.', action].filter(Boolean).join(' ')
    }
    if (scene.id === 'quest') {
      return [opener, tone.proactive ? 'Отряд: архитектор → разработчик → ревьюер. Флоу: доработка возможности.' : 'Могу предложить отряд и флоу после подтверждения.', tone.cautious ? 'Важность: важный. Нужны тесты, критерии готовности и план отката до запуска.' : 'После короткого осмотра плана квест можно запускать.', 'Прогоны запустит Мастер, компаньон останется советником.', action].filter(Boolean).join(' ')
    }
    if (scene.id === 'flow') {
      return [opener, tone.creative ? 'Сначала ищу близкий флоу и копирую его, а не строю схему с нуля.' : 'Предложу безопасный флоу релиза.', 'Узлы: вход → агент → тесты → подтверждение. Запуск делает движок Point, не этот разговор.', action].filter(Boolean).join(' ')
    }
    return [opener, action].filter(Boolean).join(' ')
  }
  function companionScenePickerHtml(value) {
    const selected = companionSelectedScene(value)
    return `<div class="companion-scene-picker" data-companion-scenes><span>Где он помогает</span>${COMPANION_EXAMPLES.map(item => `<button type="button" class="${item.id === selected.id ? 'selected' : ''}" data-action="companion-select-scene" data-scene="${esc(item.id)}"><b>${esc(item.icon)}</b><strong>${esc(item.title)}</strong></button>`).join('')}</div>`
  }
  function companionSampleDialogueHtml(value, compact = false) {
    const draft = sanitizeCompanionSetupDraft(value)
    const preset = COMPANION_PRESETS.find(item => item.id === draft.preset)
    const scene = companionSelectedScene(draft)
    const reply = companionSampleReplyText(draft, scene.id)
    return `<div class="companion-sample-dialogue ${compact ? 'compact' : ''}"><div class="you"><small>ВЫ · ${esc(scene.surface)}</small><p>${esc(scene.user)}</p></div><div class="them"><small>КОМПАНЬОН · ${esc(preset?.label || 'Свой')}</small><p>${esc(compact ? reply.slice(0, 220) + (reply.length > 220 ? '…' : '') : reply)}</p></div><div class="act">${draft.autoAct ? 'Подготовит карточку на ревью' : 'Запустить · Изменить · Пропустить'}</div></div>`
  }
  function companionRoleShowcaseHtml(value) {
    const draft = sanitizeCompanionSetupDraft(value)
    const preset = COMPANION_PRESETS.find(item => item.id === draft.preset)
    const does = (preset?.does || []).map(item => `<em>${esc(item)}</em>`).join('')
    return `<article class="companion-role-showcase" data-companion-role-showcase><header><span>${esc(preset?.icon || '◌')}</span><div><strong>${esc(preset?.label || 'Свой характер')}</strong><small>Работает во всей IDE: логи, код, агенты, квесты</small></div></header><div class="companion-does">${does}</div>${companionScenePickerHtml(draft)}</article>`
  }
  function companionActCompareHtml(value) {
    const draft = sanitizeCompanionSetupDraft(value)
    return `<div class="companion-act-compare" data-companion-act-preview><button type="button" class="${draft.autoAct ? '' : 'on'}" data-action="companion-toggle-auto-act" data-auto-act="false"><b>Советует</b><p>Карточка плана. Вы решаете, запускать ли её.</p><footer><span>Start</span><span>Modify</span><span>Ignore</span></footer></button><button type="button" class="${draft.autoAct ? 'on' : ''}" data-action="companion-toggle-auto-act" data-auto-act="true"><b>Готовит действие</b><p>По явной команде сам собирает Quest, Agent или Flow. Подготовит карточку на ревью.</p><footer><span>карточка на ревью</span></footer></button></div>`
  }
  function companionPredictedReplyHtml(value, example) {
    const scene = example || COMPANION_EXAMPLES[0]
    return `<div class="companion-predicted" data-companion-sample-reply><strong>Как ответит с текущим характером</strong><p>${esc(companionSampleReplyText(value, scene.id))}</p><small>Это прогноз по слайдерам, не живой ответ модели.</small></div>`
  }
  function companionSetupTestAnswerHtml() {
    const result = getSetupTestResult()
    if (!result?.reply) return ''
    const fallback = String(result.fallbackReason || '').trim()
    const mode = String(result.mode || '').toLowerCase()
    const viaModel = mode === 'model' && !fallback
    const badge = viaModel
      ? (result.model ? `модель · ${result.model}` : 'модель')
      : (fallback ? 'локальный fallback' : 'локальный Point Core')
    const warn = fallback
      ? `<div class="companion-test-fallback" role="status"><strong>Модель не ответила</strong><p>${esc(fallback)}</p><small>Ниже — локальный ответ Point Core по фактам IDE, не ответ шлюза.</small></div>`
      : ''
    return `${warn}<div class="companion-test-answer${fallback ? ' fallback' : ''}"><strong>Ответ компаньона · ${esc(badge)}</strong><p>${esc(result.reply)}</p></div>`
  }
  function refreshCompanionLiveSurfaces(value, scope) {
    const draft = sanitizeCompanionSetupDraft(value)
    const rootEl = scope || root
    const preview = rootEl.querySelector('[data-companion-preview]')
    if (preview) preview.outerHTML = companionPersonalityPreviewHtml(draft, preview.classList.contains('compact'))
    const showcase = rootEl.querySelector('[data-companion-role-showcase]')
    if (showcase) showcase.outerHTML = companionRoleShowcaseHtml(draft)
    const act = rootEl.querySelector('[data-companion-act-preview]')
    if (act) act.outerHTML = companionActCompareHtml(draft)
    const scenes = rootEl.querySelector('[data-companion-scenes]')
    if (scenes) scenes.outerHTML = companionScenePickerHtml(draft)
    const sample = rootEl.querySelector('[data-companion-sample-reply]')
    if (sample) {
      const example = companionSelectedScene(draft)
      sample.outerHTML = companionPredictedReplyHtml(draft, example)
    }
    const live = rootEl.querySelector('[data-companion-trait-live]')
    if (live) {
      const focused = rootEl.querySelector('[data-companion-personality]:focus')
      const trait = focused?.dataset?.trait || 'initiative'
      live.textContent = companionTraitLiveLine(trait, draft[trait])
    }
  }
  function companionTraitKeyToId(id, prefix = 'companion') {
    return `${prefix}-${String(id).replace(/[A-Z]/g, letter => `-${letter.toLowerCase()}`)}`
  }
  function companionPresetChoiceHtml(item, selectedId, action) {
    const does = (item.does || []).slice(0, 2).map(entry => `<em>${esc(entry)}</em>`).join('')
    return `<button type="button" class="companion-preset-choice ${item.id === selectedId ? 'selected' : ''}" data-action="${esc(action)}" data-preset="${esc(item.id)}"><span>${esc(item.icon)}</span><strong>${esc(item.label)}</strong><small>${esc(item.hint)}</small>${does ? `<div class="companion-does mini">${does}</div>` : ''}</button>`
  }
  // Роль — это кто он. Шаг «Роль» спрашивает только об этом.
  function companionPresetStudioHtml(selectedId, action = 'companion-setup-preset') {
    const canonical = COMPANION_PRESETS.filter(item => COMPANION_CANONICAL_PRESET_IDS.includes(item.id))
    return `<div class="companion-preset-studio">${canonical.map(item => companionPresetChoiceHtml(item, selectedId, action)).join('')}</div>`
  }

  // Стиль поведения — это как он себя ведёт, то же самое, что и черты характера,
  // только крупным шагом. Раньше эти четыре карточки лежали на шаге «Роль» под
  // заголовком «Ещё роли», хотя ролями не были и дублировали следующий шаг:
  // человек выбирал характер до того, как доходил до экрана характера.
  function companionStyleStudioHtml(selectedId, action = 'companion-setup-preset') {
    const styles = COMPANION_PRESETS.filter(item => !COMPANION_CANONICAL_PRESET_IDS.includes(item.id))
    if (!styles.length) return ''
    return `<div class="companion-style-presets">
      <span class="companion-style-label">Готовый стиль — или настройте черты ниже</span>
      <div class="companion-preset-studio extra">${styles.map(item => companionPresetChoiceHtml(item, selectedId, action)).join('')}</div>
    </div>`
  }
  function companionPersonalityControlsHtml(value, idPrefix = 'companion') {
    const draft = sanitizeCompanionSetupDraft(value)
    return `<div class="companion-personality-controls">${COMPANION_TRAIT_FIELDS.map(item => {
      const inputId = companionTraitKeyToId(item.id, idPrefix)
      return `<label class="companion-range"><span><strong>${esc(item.label)}</strong><small>${esc(item.hint)}</small></span><div class="companion-range-track"><em>${esc(item.low)}</em><input id="${esc(inputId)}" data-companion-personality data-trait="${esc(item.id)}" type="range" min="0" max="100" value="${Number(draft[item.id])}"><em>${esc(item.high)}</em></div><output>${Number(draft[item.id])}%</output></label>`
    }).join('')}<p class="companion-trait-live" data-companion-trait-live>${esc(companionTraitLiveLine('initiative', draft.initiative))}</p></div>`
  }
  function companionTraitBarsHtml(value) {
    const draft = sanitizeCompanionSetupDraft(value)
    return `<div class="companion-trait-bars">${COMPANION_TRAIT_FIELDS.map(item => `<span><small>${esc(item.label)}</small><i><b ${fillAttribute(draft[item.id])}></b></i></span>`).join('')}</div>`
  }
  function companionPersonalityPreviewHtml(value, compact = false) {
    const draft = sanitizeCompanionSetupDraft(value)
    const preset = COMPANION_PRESETS.find(item => item.id === draft.preset)
    const lines = companionBehaviorLines(draft)
    return `<aside class="companion-persona-dock companion-live-preview ${compact ? 'compact' : ''}" data-companion-preview data-companion-dock><header><span>${esc(preset?.icon || '◌')}</span><div><strong>${esc(preset?.label || 'Свой характер')}</strong><small>Так компаньон будет вести себя в проекте</small></div></header>${companionTraitBarsHtml(draft)}${companionSampleDialogueHtml(draft, true)}<ul>${lines.slice(0, compact ? 3 : 6).map(item => `<li>${esc(item)}</li>`).join('')}</ul></aside>`
  }
  function companionProbeHtml() {
    const probe = getProviderProbe()
    if (!probe) return '<p class="companion-probe-note">Проверка обращается прямо к выбранному адресу сервиса и получает реальный список моделей.</p>'
    if (probe.loading) return '<div class="companion-probe-result loading"><i></i><div><strong>Проверяем связь…</strong><small>Endpoint, авторизация и каталог моделей</small></div></div>'
    if (probe.connected) return `<div class="companion-probe-result success"><b>✓</b><div><strong>Подключение работает · ${Number(probe.latencyMs || 0).toLocaleString('ru-RU')} мс</strong><small>Найдено моделей: ${(probe.models || []).length}. Выберите одну ниже.</small></div></div>${(probe.models || []).length ? `<div class="companion-model-pills">${probe.models.slice(0, 18).map(item => `<button type="button" data-action="companion-select-model" data-model="${esc(item.id)}">${esc(item.id)}</button>`).join('')}</div>` : ''}`
    // Ядро называет причину и следующий шаг. Общая формулировка остаётся только
    // там, где ядро ничего не сказало.
    const title = probe.phase === 'save'
      ? 'Не удалось сохранить подключение'
      : (probe.problem || 'Не удалось подключиться к шлюзу')
    const next = probe.fix || probe.error || probe.message || 'Проверьте адрес, ключ и доступность сервиса.'
    return `<div class="companion-probe-result error"><b>!</b><div><strong>${esc(title)}</strong><small>${esc(next)}</small></div></div>`
  }
  function companionProbeCtaLabel(kind) {
    if (getProviderProbe()?.loading) return 'Подключаемся…'
    return kind === 'existing' ? 'Проверить связь' : 'Проверить и сохранить'
  }

  return {
    companionScenePickerHtml,
    companionRoleShowcaseHtml,
    companionActCompareHtml,
    companionPredictedReplyHtml,
    companionSetupTestAnswerHtml,
    refreshCompanionLiveSurfaces,
    companionTraitKeyToId,
    companionPresetStudioHtml,
    companionStyleStudioHtml,
    companionPersonalityControlsHtml,
    companionPersonalityPreviewHtml,
    companionProbeHtml,
    companionProbeCtaLabel,
  }
}
