// Правая панель разговора: вкладки «Команда» и «Контекст».
//
// Ошибаются они молча: один агент стоит в панели дважды (в отряде и в ростере),
// лист раскрывается не у того, кого нажали, недоверенный текст памяти или
// основания попадает в разметку как разметка, а неизвестная вкладка оставляет
// панель пустой. Проверка сторожит эти четыре вещи.

import assert from 'node:assert/strict'
import {
  inspectorTab, masterAgentSheetHtml, masterContextPanelHtml, masterInspectorTabsHtml,
  masterTeamGroups, masterTeamHtml,
} from '../vscode-extension/ui/client/master-inspector.js'
import { esc } from '../vscode-extension/ui/client/html-escape.js'

// Вкладка: неизвестное имя — запасная вкладка, а не пустая панель.
assert.equal(inspectorTab('team'), 'team')
assert.equal(inspectorTab('nonsense'), 'quest')
assert.equal(inspectorTab('', 'team'), 'team')
const tabs = masterInspectorTabsHtml('context', esc, { team: 2 })
assert.match(tabs, /role="tablist"/)
assert.match(tabs, /id="master-inspector-tab-context"[^>]*aria-selected="true"/, 'выбранная вкладка помечена')
assert.equal((tabs.match(/tabindex="0"/g) || []).length, 1, 'в обходе клавиатурой одна вкладка — выбранная')
assert.match(tabs, /<small>2<\/small>/, 'число работающих видно на вкладке команды')

// Команда: каждый агент в ближайшем своём круге и больше нигде.
const dev = { id: 'dev', name: 'Разработчик', role: 'Пишет код', status: 'active' }
const qa = { id: 'qa', name: 'Проверяющий', status: 'active' }
const groups = masterTeamGroups({
  working: [{ ...dev, working: true, note: 'Реализация' }],
  party: [dev, qa],
  roster: [dev, qa, { id: 'arch', name: 'Архитектор' }],
})
assert.deepEqual(groups.map(group => [group.id, group.members.map(member => member.id)]), [
  ['working', ['dev']], ['party', ['qa']], ['roster', ['arch']],
], 'агент не повторяется в трёх кругах')
assert.deepEqual(masterTeamGroups({}), [], 'пустые круги не рисуются')

// Лист раскрывается у того, кого нажали, и только у него.
const agents = {
  dev: { ...dev, primaryModel: 'qwen', allowedTools: ['read_file', 'propose_patch'], maxSteps: 20, maxDurationSeconds: 900, tasksCompleted: 3, successCount: 2, level: 2 },
}
const team = masterTeamHtml({ groups, openId: 'dev', agentById: id => agents[id], esc })
assert.equal((team.match(/hall-insp-sheet/g) || []).length, 1, 'раскрыт один лист')
assert.match(team, /data-action="master-inspector-agent" data-id="dev" aria-expanded="true"/)
assert.match(team, /3 квеста · 2 успешно/, 'послужной список взят из данных ядра')
assert.match(team, /read_file/, 'умения агента перечислены')
assert.match(team, /open-agent-constructor-edit/, 'правят агента в мастерской, а не здесь')
assert.match(masterTeamHtml({ groups: [], openId: '', agentById: () => null, esc }), /Команды пока нет/)
assert.match(masterAgentSheetHtml(null, esc), /создаст утверждение наряда/, 'агент, которого ещё нет, не выдаётся за существующего')

// Контекст: недоверенный текст остаётся текстом.
const context = masterContextPanelHtml({
  attachments: [{ id: 'a', name: '<b>x</b>.go', kind: 'file' }],
  memory: '<script>alert(1)</script>',
  usedMemory: ['<i>запомнено</i>'],
  facts: ['<img src=x>'],
  model: 'qwen',
  esc,
})
assert.doesNotMatch(context, /<script>|<img|<b>x<\/b>|<i>запомнено/)
assert.match(context, /К следующей реплике <small>1<\/small>/)
assert.doesNotMatch(context, /master-context-remove/, 'у вложения одна кнопка «убрать» — в поле ввода')
assert.match(masterContextPanelHtml({ esc }), /Вложений нет/)

console.log('правая панель разговора: PASS')
