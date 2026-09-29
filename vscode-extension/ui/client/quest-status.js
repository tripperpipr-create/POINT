// Состояния квеста для всех поверхностей: подпись, знак и тон наряда.
//
// Полоса квестов, карточка наряда, список квестов проекта и счётчики читают
// статус отсюда. Пока у каждой поверхности был свой список, одна и та же
// работа могла быть «выполняется» во вкладке настроек и «готово» в чате.

// Провал — такое же состояние квеста, как остальные, и без него карточка
// рисовала «✓ failed · <текст ошибки>» зелёной галочкой успеха: ключа не было
// ни в подписях, ни в знаках, ни в тонах, и все три словаря отдавали запасное
// значение «готово».
const runtimeLabels = {
  preflight:'Проверяем окружение', running:'Квест выполняется', awaiting_user:'Нужны данные пользователя',
  verifying:'Проверяем результат', applying:'Переносим в проект', completed:'Готово', needs_review:'Нужна ручная приёмка',
  blocked:'Заблокирован', failed:'Провален', paused:'На паузе', cancelled:'Отменён',
}

// Знак и тон состояния. Строка утверждённого наряда всегда начиналась зелёной
// галочкой — и «✓ Заблокирован» получалось зелёным успехом, хотя квест стоит, а
// причина написана тут же. Галочка принадлежит только исходу «готово»:
// остановка помечается знаком внимания, отмена — крестом, пауза — паузой, а
// работа в ходу — точкой.
const runtimeMarks = {
  completed:'✓', needs_review:'!', blocked:'!', failed:'✕', awaiting_user:'?', cancelled:'✕', paused:'‖',
  preflight:'·', running:'·', verifying:'·', applying:'·',
}
// Провал — тоном отказа (--wound), а не тем же «вниманием», что у квеста,
// ждущего человека: из провала выход один — новая версия наряда.
const runtimeTones = {
  completed:'is-done', needs_review:'is-attention', blocked:'is-attention', failed:'is-failed', awaiting_user:'is-attention',
  cancelled:'is-quiet', paused:'is-quiet',
  preflight:'is-active', running:'is-active', verifying:'is-active', applying:'is-active',
}

export function runtimePresentation(runtime) {
  if (runtime?.status === 'completed' && runtime?.assurance === 'partial') {
    return { label: 'Готово с ограничениями', mark: '!', tone: 'is-attention' }
  }
  // Незнакомое состояние — не успех: галочка и тон «готово» принадлежат только
  // исходу `completed`.
  return {
    label: runtimeLabels[runtime?.status] || runtime?.status,
    mark: runtimeMarks[runtime?.status] || '·',
    tone: runtimeTones[runtime?.status] || 'is-quiet',
  }
}

// Где квест для человека: идёт, ждёт его решения или уже в истории. blocked,
// paused и needs_review — не завершение: по ним ещё решают, и из «Сейчас» они
// не уходят. Незнакомый статус тоже остаётся на виду, а не прячется в историю.
const QUEST_PHASES = {
  live: ['preflight', 'running', 'verifying', 'applying', 'awaiting_approval', 'awaiting_user', 'active'],
  attention: ['paused', 'blocked', 'needs_review', 'proposed', 'draft'],
  history: ['completed', 'failed', 'cancelled'],
}

export function questPhase(status) {
  const key = String(status || '')
  for (const [phase, statuses] of Object.entries(QUEST_PHASES)) {
    if (statuses.includes(key)) return phase
  }
  return 'attention'
}

// Корень — квест человека. Дочерние квесты — этапы его Flow (подготовка,
// интеграция, приёмка), и самостоятельной работой в списке проекта они не
// являются: одна задача в E1 выглядела шестью квестами.
export const isRootQuest = quest => !String(quest?.parentId || '').trim()

// Последний закрытый квест человека — по времени завершения. Текущим он не
// становится (currentHubQuest), но его итог «Обещано и получено» обзор
// показывает, пока нового квеста нет: иначе исход виден только в развёрнутой
// строке списка квестов.
export function latestFinishedRootQuest(quests) {
  const finishedAt = quest => Date.parse(quest?.finishedAt || quest?.updatedAt || '') || 0
  return (quests || []).filter(quest => isRootQuest(quest) && questPhase(quest.status) === 'history')
    .reduce((latest, quest) => (!latest || finishedAt(quest) > finishedAt(latest) ? quest : latest), null)
}
