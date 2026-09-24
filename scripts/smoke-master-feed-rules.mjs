// Правила ленты Мастера, которые считаются без отрисовки.
//
// Подпись дня и память ответа ошибаются молча: дата прописными выглядит как
// «так задумано», а схлопнувшаяся на пересборке раскрывашка — как случайный
// щелчок мимо. Проверяются они здесь, прямо по источникам вебвью, без сборки.

import assert from 'node:assert/strict'
import { masterDayLabel } from '../vscode-extension/ui/client/master-feed.js'
import { masterUsedMemoryHtml } from '../vscode-extension/ui/client/master-memory-ui.js'
import { esc as escapeHtml } from '../vscode-extension/ui/client/html-escape.js'

const now = new Date(2026, 8, 24, 12, 0)
assert.equal(masterDayLabel(new Date(2026, 8, 24, 9, 0), now), 'Сегодня')
assert.equal(masterDayLabel(new Date(2026, 8, 23, 23, 0), now), 'Вчера')
assert.equal(masterDayLabel(new Date(2026, 8, 21, 9, 0), now), '21 сентября',
  'дата набрана обычным регистром, как «Сегодня» рядом с ней')
assert.equal(masterDayLabel(new Date(2025, 0, 3, 9, 0), now), '3 января 2025',
  'год называется только у прошлых лет')

const entries = [{ id: 'm1', content: 'Тесты — через go test ./...' }]
const closed = masterUsedMemoryHtml(['m1'], entries, escapeHtml, 'ma-1', false)
assert.match(closed, /data-master-open="memory" data-id="memory:ma-1"/,
  'раскрытие памяти помнится по реплике, иначе пересборка ленты его схлопнет')
assert.doesNotMatch(closed, /\sopen[\s>]/, 'по умолчанию память свёрнута')
assert.match(masterUsedMemoryHtml(['m1'], entries, escapeHtml, 'ma-1', true), /\sopen>/,
  'раскрытая память остаётся раскрытой после пересборки')
assert.equal(masterUsedMemoryHtml([], entries, escapeHtml, 'ma-1', true), '', 'без памяти блока нет')

console.log('правила ленты Мастера: PASS')
