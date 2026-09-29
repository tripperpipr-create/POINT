// Задание созрело — вебвью об этом узнаёт.
//
// Предложение живёт весь разговор: id и статус у него прежние, а содержание
// меняется каждым ходом Мастера, и в какой-то из них обсуждение становится
// готовым к запуску. Подпись состояния читала у предложения только id и статус,
// postState считал сообщение повторным и не отправлял его — вебвью оставался с
// прежней, обсуждаемой версией задания. Человек читал «задание готово к
// запуску» там, где карточки с кнопкой в ленте нет, и решал, что квест не
// создался вовсе.

const path = require('path')

const { cheapStateSignature: signature } = require(path.join(__dirname, '..', 'vscode-extension', 'hub-state-signature.js'))

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

// Квест дошёл до исхода: вкладки обязаны получить новое состояние. Без статуса
// квеста в подписи настройки проекта навсегда показывали его активным.
const withQuest = status => signature({ ...proposal({ version: 5, state: 'ready' }), boot: { quests: [{ id: 'quest-1', status, updatedAt: '1' }] } })
if (withQuest('verifying') === withQuest('completed')) {
  throw new Error('Исход квеста невидим подписи postState: вкладки остаются с «проверяется»')
}
const withRuntime = status => signature({ ...proposal({ version: 5, state: 'ready' }), boot: { workOrders: [{ id: 'wo-1', runtime: { status, updatedAt: '1' } }] } })
if (withRuntime('running') === withRuntime('failed')) {
  throw new Error('Исход наряда невидим подписи postState')
}

console.log('quest brief state signature: PASS')
