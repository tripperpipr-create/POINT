// Задание созрело — вебвью об этом узнаёт.
//
// Предложение живёт весь разговор: id и статус у него прежние, а содержание
// меняется каждым ходом Мастера, и в какой-то из них обсуждение становится
// готовым к запуску. Подпись состояния читала у предложения только id и статус,
// postState считал сообщение повторным и не отправлял его — вебвью оставался с
// прежней, обсуждаемой версией задания. Человек читал «задание готово к
// запуску» там, где карточки с кнопкой в ленте нет, и решал, что квест не
// создался вовсе.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const source = fs.readFileSync(path.join(__dirname, '..', 'vscode-extension', 'extension.js'), 'utf8')
  .replace(/\r\n/g, '\n')
const start = source.indexOf('function cheapStateSignature(message)')
const end = source.indexOf('\n\n// Ошибки ядра', start)
if (start < 0 || end < 0) throw new Error('cheapStateSignature source was not found')

const context = { JSON }
vm.runInNewContext(`${source.slice(start, end)}\nthis.signature = cheapStateSignature`, context)
const signature = context.signature

const proposal = (brief, status = 'pending') => ({
  service: { state: 'running' }, workspaceTrusted: true, workspace: 'fixture', selectedTab: 'master',
  onboarding: { complete: true },
  boot: { questProposals: [{ id: 'qp-1', status, brief }] },
})

const discussed = signature(proposal({ version: 4, state: 'discussion', openQuestions: ['Где развернуть?'] }))
const ready = signature(proposal({ version: 5, state: 'ready', openQuestions: [] }))
if (discussed === ready) {
  throw new Error('Созревшее задание невидимо подписи postState: вебвью остаётся с обсуждаемой версией')
}

// Статус решает судьбу карточки в ленте, и его подпись обязана видеть и дальше.
if (signature(proposal({ version: 5, state: 'ready' }, 'started')) === ready) {
  throw new Error('Запуск предложения невидим подписи postState')
}

console.log('quest brief state signature: PASS')
