// Git-действия квеста из карточки: закоммитить, отправить, создать MR,
// откатить. Ядро делает их через git-агента; здесь — подтверждение человека
// для всего, что уходит на сервер или меняет его файлы, и ключ модели для
// сообщения коммита (он живёт только в SecretStorage редактора).
//
// Политика «коммит сам / только по просьбе» — настройка Point; ядро читает её
// при сборке наряда, поэтому она отправляется в ядро при старте и при смене.

const vscode = require('vscode')
const { masterWorkspaceId, masterPath } = require('./master-scope')
const { projectScope } = require('./master-work-order-watch')

const TITLES = { commit: 'Закоммитить', push: 'Отправить', merge_request: 'Создать MR', revert: 'Откатить' }

function repoLine(view, repo) {
  const items = (view?.repositories || []).filter(item => !repo || item.path === repo)
  return items.map(item => `${item.path === '.' ? 'проект' : item.path}: ${item.branch || '—'}${item.target ? ` → ${item.target}` : ''}`).join('; ')
}

// Подтверждение — модальное, с тем, что именно уйдёт и куда: отправка
// необратима для сервера, откат — для файлов.
async function confirmQuestGitAction(action, view, repo) {
  const where = repoLine(view, repo)
  const protectedBranch = (view?.repositories || []).some(item => (!repo || item.path === repo) && item.protected)
  const text = {
    push: `Отправить ветку в origin? ${where}.${protectedBranch ? ' Ветка защищена: сервер примет её только при ваших правах.' : ''}`,
    merge_request: `Отправить ветку и создать merge request в GitLab? ${where}.`,
    revert: 'Откатить файлы квеста? Проект вернётся к состоянию до квеста; изменения после квеста откат не тронет — при них он остановится.',
  }[action]
  if (!text) return true
  const choice = await vscode.window.showWarningMessage(text, { modal: true }, TITLES[action])
  return choice === TITLES[action]
}

async function handleQuestGitMessage(message) {
  const scope = projectScope(this)
  const workspaceId = masterWorkspaceId(this, message.workspaceId)
  const request = (route, options) => this.service.request(masterPath(route, workspaceId), options)
  const post = reply => scope.post(reply)
  switch (message.type) {
    case 'questGitAction': {
      const questId = String(message.questId || '')
      const workOrderId = String(message.workOrderId || '')
      const action = String(message.gitAction || '')
      const repo = String(message.repo || '')
      const done = async (tone, text) => {
        const workOrder = workOrderId ? await request('/api/v2/work-orders/' + encodeURIComponent(workOrderId)).catch(() => null) : null
        post({ type: 'masterWorkOrderControlled', workOrder, viewId: message.viewId })
        if (text) post({ type: 'masterBranchNotice', tone, message: text })
      }
      if (!questId || !TITLES[action]) { await done('error', 'Неизвестное git-действие'); break }
      const order = workOrderId ? await request('/api/v2/work-orders/' + encodeURIComponent(workOrderId)).catch(() => null) : null
      if (!(await confirmQuestGitAction(action, order?.runtime?.git, repo))) { await done('', ''); break }
      // Ключ нужен только git-агенту для сообщения коммита; без него — шаблон.
      const apiKey = action === 'commit' ? await this.credentialForOrchestrator().catch(() => '') : ''
      try {
        const result = await request('/api/v2/master/quests/' + encodeURIComponent(questId) + '/git/' + encodeURIComponent(action), {
          method: 'POST', timeoutMs: 5 * 60_000, body: JSON.stringify({ repo, apiKey }),
        })
        if (result?.openUrl && /^https:\/\//.test(result.openUrl)) await vscode.env.openExternal(vscode.Uri.parse(result.openUrl, true))
        await done('ok', String(result?.message || `${TITLES[action]}: готово`))
      } catch (error) {
        await done('error', `${TITLES[action]}: ${String(error?.message || error)}`)
      }
      break
    }
    case 'openQuestGitUrl': {
      const url = String(message.url || '')
      if (/^https:\/\//.test(url)) await vscode.env.openExternal(vscode.Uri.parse(url, true))
      break
    }
  }
}

// Политика коммита — в ядро перед каждым ходом Мастера: только он собирает
// наряд, и смена настройки действует со следующего наряда без перезапуска.
async function syncQuestGitPolicy(service, config) {
  const value = String(config.get('questGit', 'auto') || 'auto')
  try {
    await service.request('/api/settings/quest-git', { method: 'PUT', body: JSON.stringify({ commitPolicy: value === 'onRequest' ? 'on_request' : 'on_completion' }) })
  } catch {}
}

module.exports = { handleQuestGitMessage, syncQuestGitPolicy }
