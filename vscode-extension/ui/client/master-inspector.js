// Инспектор разговора: правая панель с тремя вкладками.
//
// Панель задания отвечала на один вопрос — что обсуждаем или делаем, — и
// существовала только при предложении. Два других вопроса, которые возникают
// за работой постоянно, ответа не имели нигде рядом с разговором: кто в
// команде и чем каждый вооружён, и что именно Мастер видит в этом разговоре.
// За ответом приходилось уходить в Гильдию или угадывать.
//
// Модуль рисует только вкладки «Команда» и «Контекст» и полосу вкладок;
// вкладку «Квест» по-прежнему собирает master-brief-panel.js, где живут
// задание и его редактор. Всё здесь — чтение: правят агента в мастерской,
// вложения — в поле ввода, память — в настройках разговора.

import { agentStats, monogram } from './master-agent-sheet.js'
import { countOf, fillAttribute, list } from './format-units.js'
import { icon } from './ui-icons.js'

export const INSPECTOR_TABS = [
  ['quest', 'Квест', 'quest'],
  ['team', 'Команда', 'team'],
  ['context', 'Контекст', 'memory'],
]

export function inspectorTab(value, fallback = 'quest') {
  return INSPECTOR_TABS.some(([id]) => id === value) ? value : fallback
}

export function masterInspectorTabsHtml(active, esc, counts = {}) {
  const tabs = INSPECTOR_TABS.map(([id, label, glyph]) => {
    const on = id === active
    const count = Number(counts[id] || 0)
    return `<button type="button" role="tab" id="master-inspector-tab-${id}" class="hall-insp-tab${on ? ' is-on' : ''}" aria-selected="${on}" aria-controls="master-inspector-body" tabindex="${on ? 0 : -1}" data-action="master-inspector-tab" data-tab="${id}">${icon(glyph)}<span>${esc(label)}</span>${count ? `<small>${count}</small>` : ''}</button>`
  }).join('')
  return `<div class="hall-insp-tabs" role="tablist" aria-label="Разделы панели" data-keynav="row">${tabs}</div>`
}

// —— Команда ——
//
// Три круга людей, от ближнего к дальнему: кто работает над квестом сейчас,
// кого Мастер подобрал в отряд задания, и весь ростер проекта. Один агент
// стоит в ближайшем своём круге и больше не повторяется.
export function masterTeamGroups({ working = [], party = [], roster = [] }) {
  const seen = new Set()
  const take = items => list(items).filter(item => {
    const id = String(item?.id || '')
    if (!id || seen.has(id)) return false
    seen.add(id)
    return true
  })
  return [
    { id: 'working', title: 'Работают сейчас', members: take(working) },
    { id: 'party', title: 'Отряд задания', members: take(party) },
    { id: 'roster', title: 'Ростер проекта', members: take(roster) },
  ].filter(group => group.members.length)
}

const STATUS_LABEL = { active: 'готов', draft: 'черновик', disabled: 'выключен', archived: 'в архиве' }

function agentRowHtml(member, open, esc) {
  const name = String(member.name || member.id)
  const role = String(member.role || member.roleDescription || '')
  const model = String(member.model || member.primaryModel || '')
  const state = member.note || STATUS_LABEL[member.status] || ''
  const tone = member.working ? ' is-working' : member.status && member.status !== 'active' ? ' is-idle' : ''
  return `<button type="button" class="hall-insp-agent${open ? ' is-open' : ''}${tone}" data-action="master-inspector-agent" data-id="${esc(member.id)}" aria-expanded="${open}">
      <span class="hall-insp-mark" aria-hidden="true">${esc(monogram(name) || '?')}</span>
      <span class="hall-insp-who"><b title="${esc(name)}">${esc(name)}</b>${role ? `<small title="${esc(role)}">${esc(role)}</small>` : ''}</span>
      <span class="hall-insp-meta">${model ? `<small title="Модель">${esc(model)}</small>` : ''}${state ? `<small class="hall-insp-state">${esc(state)}</small>` : ''}</span>
      <span class="hall-insp-chevron">${icon('chevron-down')}</span>
    </button>`
}

// Лист персонажа. Показатели не выдумываются: характеристики считает
// agentStats из настроек агента, опыт и уровень ставит ядро.
export function masterAgentSheetHtml(agent, esc) {
  if (!agent) return '<p class="hall-insp-empty">Агента нет в ростере проекта: его создаст утверждение наряда.</p>'
  const stats = agentStats(agent).map(stat => `<div class="hall-insp-stat" title="${esc(stat.hint)}">
      <span>${esc(stat.label)}</span>
      <span class="hall-quest-bar is-sm"><span ${fillAttribute(stat.score * 20)}></span></span>
      <small>${esc(stat.hint)}</small>
    </div>`).join('')
  const tools = list(agent.allowedTools)
  const gear = tools.length
    ? `<div class="hall-insp-chips">${tools.slice(0, 12).map(tool => `<span>${esc(tool)}</span>`).join('')}${tools.length > 12 ? `<span>+${tools.length - 12}</span>` : ''}</div>`
    : '<p class="hall-insp-empty">Умений нет: агент только читает то, что ему дали.</p>'
  const done = Number(agent.tasksCompleted) || 0
  const record = [
    Number(agent.level) ? `уровень ${Number(agent.level)}` : '',
    done ? countOf(done, 'квест', 'квеста', 'квестов') : 'квестов ещё не было',
    done ? `${Number(agent.successCount) || 0} успешно` : '',
  ].filter(Boolean).join(' · ')
  const model = [agent.model || agent.primaryModel, agent.provider].filter(Boolean).join(' · ')
  return `<div class="hall-insp-sheet">
      ${agent.mission || agent.roleDescription ? `<p>${esc(agent.mission || agent.roleDescription)}</p>` : ''}
      <dl class="hall-insp-facts">
        ${model ? `<dt>Модель</dt><dd>${esc(model)}</dd>` : ''}
        <dt>Послужной список</dt><dd>${esc(record)}</dd>
      </dl>
      <div class="hall-insp-rubric">Характеристики</div>
      <div class="hall-insp-stats">${stats}</div>
      <div class="hall-insp-rubric">Умения · ${tools.length}</div>
      ${gear}
      <div class="hall-insp-actions"><button type="button" class="hall-btn is-sm" data-action="open-agent-constructor-edit" data-id="${esc(agent.id)}">Открыть мастерскую</button></div>
    </div>`
}

export function masterTeamHtml({ groups, openId, agentById, esc }) {
  if (!groups.length) {
    return `<div class="hall-insp-blank">${icon('team')}<b>Команды пока нет</b><span>Отряд появится вместе с заданием: исполнители из ростера проекта и те, кого предложат нанять.</span></div>`
  }
  return groups.map(group => `<section class="hall-insp-group">
      <h3>${esc(group.title)} <small>${group.members.length}</small></h3>
      ${group.members.map(member => {
        const open = member.id === openId
        return `${agentRowHtml(member, open, esc)}${open ? masterAgentSheetHtml(agentById(member.id), esc) : ''}`
      }).join('')}
    </section>`).join('')
}

// —— Контекст ——
//
// Что Мастер получит вместе со следующей репликой и на что опирался последний
// ответ. Вложения здесь только перечислены: снимают их в поле ввода, где их и
// добавляли, иначе у одного вложения стало бы две кнопки «убрать».
export function masterContextPanelHtml({ attachments = [], memory = '', usedMemory = [], facts = [], model = '', esc }) {
  const files = list(attachments)
  const sections = []
  sections.push(`<section class="hall-insp-group">
      <h3>К следующей реплике <small>${files.length}</small></h3>
      ${files.length
        ? `<ul class="hall-insp-list">${files.map(item => `<li>${icon(item.kind === 'image' ? 'file' : item.kind === 'selection' ? 'file-edit' : 'file')}<span title="${esc(item.path || item.name || '')}">${esc(item.name || item.path || 'вложение')}</span></li>`).join('')}</ul>`
        : '<p class="hall-insp-empty">Вложений нет. Кнопка «Контекст» или «@» в поле ввода добавит файл, выделение или вывод терминала.</p>'}
    </section>`)
  const used = list(usedMemory)
  sections.push(`<section class="hall-insp-group">
      <h3>Память разговора</h3>
      ${String(memory || '').trim() ? `<p class="hall-insp-note">${esc(memory)}</p>` : '<p class="hall-insp-empty">Постоянных указаний нет. Их задают в действиях с разговором (⋯ в шапке).</p>'}
      ${used.length ? `<div class="hall-insp-rubric">Использовано в последнем ответе · ${used.length}</div><ul class="hall-insp-list">${used.map(text => `<li>${icon('memory')}<span>${esc(text)}</span></li>`).join('')}</ul>` : ''}
    </section>`)
  const shown = list(facts)
  if (shown.length) {
    sections.push(`<section class="hall-insp-group">
      <h3>Основания последнего ответа</h3>
      <ul class="hall-insp-list">${shown.map(fact => `<li>${icon('check')}<span>${esc(fact)}</span></li>`).join('')}</ul>
    </section>`)
  }
  if (model) {
    sections.push(`<section class="hall-insp-group"><h3>Модель Мастера</h3><p class="hall-insp-note">${esc(model)}</p></section>`)
  }
  return sections.join('')
}
