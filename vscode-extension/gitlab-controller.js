// Плагин GitLab со стороны хоста: окно инструмента, карточка MR вкладкой
// редактора, diff и логи джобов документами IDE, подтверждение merge.
//
// Данные дают маршруты ядра /api/integrations/gitlab/*; хост их не толкует,
// а доставляет ответ той поверхности, что спросила: окну, карточке MR или
// вкладке «Интеграции». Модели здесь нет.
//
// Diff свой не рисуется: файл на базе и на голове MR открываются документами
// схемы point-gitlab: только для чтения, и сравнивает их штатный vscode.diff —
// с подсветкой, поиском и навигацией IDE.

const SCHEME = 'point-gitlab'
const GITLAB_TIMEOUT_MS = 130_000
const PROJECT_PATTERN = /^[A-Za-z0-9_.][A-Za-z0-9_./-]{0,254}$/

function escapeAttribute(value) {
  return String(value ?? '').replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char])
}

function query(params) {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== null && value !== '' && value !== 0) search.set(key, String(value))
  }
  const text = search.toString()
  return text ? `?${text}` : ''
}

function mrKey(project, iid) {
  const path = String(project || '')
  const number = Number(iid)
  if (!PROJECT_PATTERN.test(path) || path.includes('..') || !Number.isInteger(number) || number <= 0) {
    throw new Error('Неверный адрес merge request.')
  }
  return `${path}!${number}`
}

const shortSha = sha => String(sha || '').slice(0, 8)

function createGitLabController({ vscode, provider, request, secrets, unlock, publishServers }) {
  const panels = new Map()
  // Откуда разрешено открывать ссылки «в браузере»: только свой GitLab.
  let origin = ''

  async function call(route, init = {}) {
    await unlock()
    return request(route, { timeoutMs: GITLAB_TIMEOUT_MS, ...init })
  }

  function remember(response) {
    const url = response?.data?.url
    if (!url) return
    try { origin = new URL(url).origin } catch { /* неразборчивый адрес не запоминаем */ }
  }

  // deliver отвечает спросившей поверхности. Имя поверхности пришло из
  // вебвью, поэтому ищется только среди своих: окно GitLab, открытые
  // карточки MR, остальное — Гильдия.
  function deliver(surface, message) {
    const target = String(surface || '')
    if (target === 'tool') {
      void provider.toolWindows.get('gitlab')?.webview.postMessage(message)
      return
    }
    if (target.startsWith('mr:')) {
      void panels.get(target.slice(3))?.webview.postMessage(message)
      return
    }
    provider.post(message)
  }

  // Любое изменение подключения — повод окну GitLab перечитать состояние.
  function announceChange() {
    void provider.toolWindows.get('gitlab')?.webview.postMessage({ type: 'gitlabChanged' })
    for (const panel of panels.values()) void panel.webview.postMessage({ type: 'gitlabChanged' })
  }

  function openMergeRequest(message) {
    const project = String(message.project || '')
    const iid = Number(message.iid)
    const key = mrKey(project, iid)
    const existing = panels.get(key)
    if (existing) {
      existing.reveal(vscode.ViewColumn.Active)
      return
    }
    const title = `!${iid} ${String(message.title || '')}`.trim()
    const panel = vscode.window.createWebviewPanel('point.gitlabMr', title.length > 48 ? `${title.slice(0, 47)}…` : title, vscode.ViewColumn.Active, {
      enableScripts: true,
      retainContextWhenHidden: true,
      localResourceRoots: [vscode.Uri.joinPath(provider.context.extensionUri, 'media')],
    })
    panel.webview.html = provider.html(panel.webview, 'gitlab-mr')
      .replace('data-layout="gitlab-mr"', `data-layout="gitlab-mr" data-gitlab-project="${escapeAttribute(project)}" data-gitlab-iid="${iid}"`)
    panel.webview.onDidReceiveMessage(incoming => provider.handleMessage(incoming), undefined, provider.context.subscriptions)
    // Карточка стоит в ряду окон инструментов: так ей приходит то же
    // состояние ядра, что остальным поверхностям, без своего канала.
    const surfaceKey = `gitlab-mr:${key}`
    provider.toolWindows.set(surfaceKey, panel)
    panels.set(key, panel)
    panel.onDidDispose(() => {
      panels.delete(key)
      if (provider.toolWindows.get(surfaceKey) === panel) provider.toolWindows.delete(surfaceKey)
      provider.toolWindowStateSignatures?.delete(surfaceKey)
    }, undefined, provider.context.subscriptions)
  }

  function documentUri(kind, label, spec) {
    const name = String(label || kind).split('/').pop() || kind
    return vscode.Uri.from({ scheme: SCHEME, path: `/${kind}/${name}`, query: JSON.stringify(spec) })
  }

  async function documentContent(uri) {
    let spec
    try { spec = JSON.parse(uri.query) } catch { return '[адрес документа GitLab не разобрался]' }
    const failed = response => `[${response?.problem || 'GitLab не ответил'}]\n${response?.fix || ''}\n`
    try {
      if (uri.path.startsWith('/job/')) {
        const response = await call(`/api/integrations/gitlab/job-log${query({ project: spec.project, job: spec.job })}`)
        if (response?.state !== 'ok') return failed(response)
        const head = response.data?.trimmed ? '… начало лога не показано: Point держит последние 256 КБ\n\n' : ''
        return head + String(response.data?.text || '')
      }
      if (!spec.ref) return ''
      const response = await call(`/api/integrations/gitlab/file${query({ project: spec.project, path: spec.path, ref: spec.ref })}`)
      if (response?.state !== 'ok') return failed(response)
      const file = response.data || {}
      if (file.missing) return ''
      if (file.binary) return '[двоичный файл — содержимое не показывается]\n'
      if (file.tooBig) return '[файл больше 1 МБ — откройте его в GitLab]\n'
      return String(file.content || '')
    } catch (error) {
      return `[${error instanceof Error ? error.message : String(error)}]\n`
    }
  }

  async function openDiff(message) {
    const key = mrKey(message.project, message.iid)
    const path = String(message.path || '')
    const oldPath = String(message.oldPath || path)
    if (!path) throw new Error('Не выбран файл.')
    const base = message.newFile ? '' : String(message.base || '')
    const head = message.deleted ? '' : String(message.head || '')
    const left = documentUri('file', oldPath, { project: message.project, path: oldPath, ref: base })
    const right = documentUri('file', path, { project: message.project, path, ref: head })
    const name = path.split('/').pop()
    await vscode.commands.executeCommand('vscode.diff', left, right,
      `${name} · ${key.slice(key.lastIndexOf('!'))} (${shortSha(message.base)} ↔ ${shortSha(message.head)})`, { preview: true })
  }

  async function openJobLog(message) {
    const job = Number(message.job)
    if (!Number.isInteger(job) || job <= 0) throw new Error('Неверный номер джоба.')
    const uri = documentUri('job', `${String(message.name || 'job').replace(/[^\w.-]+/g, '-')}-${job}.log`, { project: String(message.project || ''), job })
    const document = await vscode.workspace.openTextDocument(uri)
    await vscode.window.showTextDocument(document, { preview: true })
  }

  async function confirmMerge(message) {
    const remove = message.removeSourceBranch === true
    const detail = [
      `${String(message.sourceBranch || '')} → ${String(message.targetBranch || '')}`,
      `Голова: ${shortSha(message.sha)}. Если за это время в ветку пришли коммиты, GitLab откажет.`,
      remove ? 'Исходная ветка будет удалена.' : 'Исходная ветка останется.',
    ].join('\n')
    const answer = await vscode.window.showWarningMessage(
      `Слить !${Number(message.iid)} «${String(message.title || '')}»?`, { modal: true, detail }, 'Merge')
    return answer === 'Merge'
  }

  async function reloadMergeRequest(surface, project, iid) {
    const response = await call(`/api/integrations/gitlab/merge-request${query({ project, iid })}`)
    deliver(surface, { type: 'gitlabMr', response })
  }

  async function savePlugin(message) {
    const token = String(message.token || '')
    const view = await call('/api/integrations/gitlab/plugin', {
      method: 'POST',
      body: JSON.stringify({ url: String(message.url || ''), caPath: String(message.caPath || ''), token }),
    })
    if (token) {
      const ref = view?.secretRefs?.['env:GITLAB_PERSONAL_ACCESS_TOKEN']
      if (ref) await secrets.store(ref, token)
    }
    await publishServers({ saved: view?.id || '' })
    announceChange()
  }

  async function handle(message) {
    const action = String(message?.action || '')
    const surface = String(message?.surface || 'hub')
    const project = String(message?.project || '')
    const iid = Number(message?.iid || 0)
    try {
      switch (action) {
        case 'status': {
          const response = await call('/api/integrations/gitlab/status')
          remember(response)
          deliver(surface, { type: 'gitlabStatus', response })
          return
        }
        case 'savePlugin':
          await savePlugin(message)
          return
        case 'changed':
          announceChange()
          return
        case 'binding': {
          const response = await call('/api/integrations/gitlab/binding', {
            method: 'PUT',
            body: JSON.stringify({ mode: String(message.mode || ''), project, username: String(message.username || '') }),
          })
          deliver(surface, { type: 'gitlabBinding', response })
          return
        }
        case 'mergeRequests': {
          const scope = String(message.scope || 'mine')
          const response = await call(`/api/integrations/gitlab/merge-requests${query({ scope })}`)
          deliver(surface, { type: 'gitlabMergeRequests', scope, response })
          return
        }
        case 'pipelines': {
          const response = await call(`/api/integrations/gitlab/pipelines${query({ project, ref: message.ref, mr: iid })}`)
          deliver(surface, { type: 'gitlabPipelines', response })
          return
        }
        case 'jobs': {
          const pipeline = Number(message.pipeline || 0)
          const response = await call(`/api/integrations/gitlab/jobs${query({ project, pipeline })}`)
          deliver(surface, { type: 'gitlabJobs', pipeline, response })
          return
        }
        case 'openMr':
          openMergeRequest(message)
          return
        case 'openWindow':
          await vscode.commands.executeCommand('workbench.view.extension.pointGitLab')
          return
        case 'mr':
          await reloadMergeRequest(surface, project, iid)
          return
        case 'discussions': {
          const response = await call(`/api/integrations/gitlab/merge-request/discussions${query({ project, iid })}`)
          deliver(surface, { type: 'gitlabDiscussions', response })
          return
        }
        case 'changes': {
          const response = await call(`/api/integrations/gitlab/merge-request/changes${query({ project, iid })}`)
          deliver(surface, { type: 'gitlabChanges', response })
          return
        }
        case 'openDiff':
          await openDiff(message)
          return
        case 'openJobLog':
          await openJobLog(message)
          return
        case 'comment': {
          const response = await call('/api/integrations/gitlab/merge-request/notes', {
            method: 'POST',
            body: JSON.stringify({ project, iid, discussionId: String(message.discussionId || ''), body: String(message.body || '') }),
          })
          deliver(surface, { type: 'gitlabDiscussions', response, posted: response?.state === 'ok' })
          return
        }
        case 'approval': {
          const response = await call('/api/integrations/gitlab/merge-request/approval', {
            method: 'POST',
            body: JSON.stringify({ project, iid, approve: message.approve === true, sha: String(message.sha || '') }),
          })
          deliver(surface, { type: 'gitlabActionResult', action, response })
          if (response?.state === 'ok') await reloadMergeRequest(surface, project, iid)
          return
        }
        case 'merge': {
          if (!(await confirmMerge(message))) {
            deliver(surface, { type: 'gitlabActionResult', action, cancelled: true })
            return
          }
          const response = await call('/api/integrations/gitlab/merge-request/merge', {
            method: 'POST',
            body: JSON.stringify({ project, iid, expectedSha: String(message.sha || ''), confirmed: true, removeSourceBranch: message.removeSourceBranch === true }),
          })
          deliver(surface, { type: 'gitlabActionResult', action, response })
          if (response?.state === 'ok') await reloadMergeRequest(surface, project, iid)
          return
        }
        case 'retry': {
          const response = await call('/api/integrations/gitlab/jobs/retry', {
            method: 'POST',
            body: JSON.stringify({ project, jobId: Number(message.job || 0) }),
          })
          deliver(surface, { type: 'gitlabActionResult', action, response, project, pipeline: Number(message.pipeline || 0) })
          return
        }
        case 'openBrowser': {
          const url = String(message.url || '')
          let parsed
          try { parsed = new URL(url) } catch { throw new Error('Ссылка не разобралась.') }
          if (!origin || parsed.origin !== origin) throw new Error('Ссылка ведёт не на подключённый GitLab — Point её не откроет.')
          await vscode.env.openExternal(vscode.Uri.parse(parsed.toString()))
          return
        }
        case 'actions': {
          const payload = await call('/api/integrations/actions?limit=50')
          deliver(surface, { type: 'integrationActions', actions: Array.isArray(payload?.actions) ? payload.actions : [] })
          return
        }
        default:
          throw new Error('Неизвестное действие GitLab')
      }
    } catch (error) {
      deliver(surface, { type: 'gitlabActionResult', action, error: error instanceof Error ? error.message : String(error) })
    }
  }

  function register(context) {
    context.subscriptions.push(
      vscode.workspace.registerTextDocumentContentProvider(SCHEME, { provideTextDocumentContent: uri => documentContent(uri) }),
    )
  }

  return { handle, register, announceChange }
}

module.exports = { createGitLabController, mrKey, SCHEME }
