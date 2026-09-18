const { formatDateTime } = require('./extension-utils')
// Две самостоятельные панели Point: домашний экран и хроника ядра.
//
// С AgentViewProvider их не связывает ничего, кроме одной кнопки «показать»:
// у каждой своя разметка, свой CSP и свой обмен сообщениями. В extension.js
// они лежали посреди жизненного цикла ядра и маршрутизации вебвью, где их
// никто не искал.
//
// Зависимости переданы явно: экранирование и запуск git живут в extension.js,
// потому что нужны и другим местам.

const vscode = require('vscode')

function createPointPanels({ escapeHtml, runGit, resolveGitRoot, openWorkspaceFile, chronicleWorkspaceFolder }) {
  class PointHome {
    constructor(context, provider) {
      this.context = context
      this.provider = provider
      this.panel = undefined
    }

    show() {
      if (this.panel) {
        this.panel.reveal(vscode.ViewColumn.One)
        this.refresh()
        return
      }
      this.panel = vscode.window.createWebviewPanel(
        'point.home',
        'Point',
        vscode.ViewColumn.One,
        {
          enableScripts: true,
          retainContextWhenHidden: false,
          localResourceRoots: [vscode.Uri.joinPath(this.context.extensionUri, 'media')],
        },
      )
      this.panel.iconPath = vscode.Uri.joinPath(this.context.extensionUri, 'media', 'agent.svg')
      this.panel.onDidDispose(() => { this.panel = undefined }, undefined, this.context.subscriptions)
      this.panel.webview.onDidReceiveMessage(async message => {
        if (!message || typeof message.type !== 'string') return
        if (message.type === 'openProject') await vscode.commands.executeCommand('workbench.action.files.openFolder')
        if (message.type === 'switchProject') await vscode.commands.executeCommand('localAgent.switchProject')
        if (message.type === 'openAgent') { this.panel?.dispose(); await this.provider.show('quests') }
        if (message.type === 'configureAgent') { this.panel?.dispose(); await this.provider.show('settings') }
        if (message.type === 'languageSupport') await vscode.commands.executeCommand('localAgent.languageSupport')
        if (message.type === 'terminal') await vscode.commands.executeCommand('workbench.action.terminal.toggleTerminal')
      }, undefined, this.context.subscriptions)
      this.refresh()
    }

    refresh() {
      if (!this.panel) return
      const webview = this.panel.webview
      const tokens = webview.asWebviewUri(vscode.Uri.joinPath(this.context.extensionUri, 'media', 'rpg-tokens.css'))
      const style = webview.asWebviewUri(vscode.Uri.joinPath(this.context.extensionUri, 'media', 'home.css'))
      const icon = webview.asWebviewUri(vscode.Uri.joinPath(this.context.extensionUri, 'media', 'point-icon.png'))
      const nonce = Math.random().toString(36).slice(2)
      const folder = this.provider?.service?.workspaceFolder?.() || vscode.workspace.workspaceFolders?.[0]
      const projectName = folder?.name || 'Проект не открыт'
      const projectPath = folder?.uri.scheme === 'file' ? folder.uri.fsPath : 'Выберите локальную папку — это ваш мир для кода и Гильдии'
      const primaryAction = folder ? 'openAgent' : 'openProject'
      const primaryLabel = folder ? 'Новый квест' : 'Открыть проект'
      const secondary = folder
        ? `<button data-action="configureAgent">Нанять агента</button>
            <button data-action="languageSupport">Поддержка языков</button>
            <button data-action="switchProject">Переключить проект <kbd>Ctrl Alt P</kbd></button>`
        : `<button data-action="switchProject">Недавние проекты <kbd>Ctrl Alt P</kbd></button>`
      webview.html = `<!doctype html>
  <html lang="ru">
  <head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width,initial-scale=1">
    <meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src ${webview.cspSource} data:; style-src ${webview.cspSource}; font-src ${webview.cspSource}; script-src 'nonce-${nonce}';">
    <link rel="stylesheet" href="${tokens}">
    <link rel="stylesheet" href="${style}">
    <title>Point</title>
  </head>
  <body>
    <main class="home">
      <header class="masthead">
        <div class="point-logo" aria-hidden="true"><img src="${icon}" alt=""></div>
        <div><strong>POINT</strong><small>AI-FIRST RPG CODE EDITOR</small></div>
      </header>
      <section class="hero">
        <div class="hero-copy">
          <p class="eyebrow">${folder ? 'МИР ОТКРЫТ' : 'ПЕРВЫЙ ШАГ'}</p>
          <h1>${folder ? 'Куйте код.<br><em>Меняйте мир.</em>' : 'Откройте проект.<br><em>Соберите отряд.</em>'}</h1>
          <p class="lead">${folder
            ? 'Задача — квест, локальные агенты — отряд. Следующий шаг: брифинг квеста или найм персонажа.'
            : 'Один клик — открыть папку. Дальше Point подскажет: ядро, агент и language server по необходимости.'}</p>
          <div class="actions">
            <button class="primary" data-action="${primaryAction}">${primaryLabel}<span>→</span></button>
            ${secondary}
          </div>
        </div>
        <aside class="project-card">
          <div class="project-heading"><span class="folder-icon"></span><small>АКТИВНЫЙ МИР</small></div>
          <strong>${escapeHtml(projectName)}</strong>
          <p>${escapeHtml(projectPath)}</p>
          <div class="project-actions">
            <button data-action="${folder ? 'switchProject' : 'openProject'}">${folder ? 'Проекты' : 'Открыть проект'}</button>
            <button data-action="terminal" ${folder ? '' : 'disabled'}>Консоль</button>
          </div>
        </aside>
      </section>
      <section class="features">
        <article><b>01</b><div><strong>Инвентарь</strong><p>Файлы текущего мира; переход между проектами — <kbd>Ctrl Alt P</kbd>.</p></div></article>
        <article><b>02</b><div><strong>Гильдия</strong><p>Наём персонажа и квесты — после открытия папки и пробуждения ядра.</p></div></article>
        <article><b>03</b><div><strong>Языки</strong><p>Language server ставится по требованию из «Поддержка языков» — без лишнего шума при старте.</p></div></article>
      </section>
      <footer>
        <span><i></i> ${folder ? 'МИР ДОСТУПЕН' : 'ЖДЁМ ПРОЕКТ'}</span>
        <button data-action="${folder ? 'configureAgent' : 'openProject'}">${folder ? 'Карточка персонажа' : 'Открыть проект'}</button>
      </footer>
    </main>
    <script nonce="${nonce}">
      const vscode = acquireVsCodeApi();
      document.addEventListener('click', event => {
        const button = event.target.closest('[data-action]');
        if (button && !button.disabled) vscode.postMessage({ type: button.dataset.action });
      });
    </script>
  </body>
  </html>`
    }
  }

  class PointChronicle {
    constructor(context, output) {
      this.context = context
      this.output = output
      this.panel = undefined
      this.selectedCommit = ''
      this.refreshVersion = 0
      this.hydrated = false
    }

    async show(commit = '') {
      if (commit) this.selectedCommit = commit
      if (!this.panel) {
        this.panel = vscode.window.createWebviewPanel(
          'point.chronicle',
          'Летопись Point',
          vscode.ViewColumn.One,
          { enableScripts: true, retainContextWhenHidden: false, localResourceRoots: [vscode.Uri.joinPath(this.context.extensionUri, 'media')] },
        )
        this.panel.iconPath = vscode.Uri.joinPath(this.context.extensionUri, 'media', 'agent.svg')
        this.hydrated = false
        this.panel.webview.onDidReceiveMessage(async message => {
          try {
            if (message.type === 'selectCommit') {
              this.selectedCommit = String(message.hash || '')
              await this.refresh({ mode: 'detail' })
            }
            if (message.type === 'refresh') await this.refresh({ mode: 'full' })
            if (message.type === 'openFile') await openWorkspaceFile(message.path)
            if (message.type === 'openWorkingTree') await vscode.commands.executeCommand('workbench.view.scm')
          } catch (error) {
            this.output.appendLine(`[chronicle] ${error instanceof Error ? error.stack || error.message : String(error)}`)
            void vscode.window.showErrorMessage(`Point: ${error instanceof Error ? error.message : String(error)}`)
          }
        }, undefined, this.context.subscriptions)
        this.panel.onDidDispose(() => { this.panel = undefined; this.hydrated = false }, undefined, this.context.subscriptions)
        this.panel.webview.html = this.loadingHtml(this.panel.webview)
      } else {
        this.panel.reveal(vscode.ViewColumn.One)
      }
      await this.refresh({ mode: 'full' })
    }

    async loadCommitDetail(cwd, selected) {
      if (!selected) return { files: [], diff: '' }
      const [filesText, diffText] = await Promise.all([
        runGit(cwd, ['show', '--format=', '--name-status', '--find-renames', selected.hash], 512 * 1024),
        runGit(cwd, ['show', '--format=', '--no-ext-diff', '--unified=3', selected.hash], 768 * 1024),
      ])
      const files = filesText.split(/\r?\n/).map(line => line.trim()).filter(Boolean).map(line => {
        const parts = line.split('\t')
        return { status: parts[0], path: parts.at(-1) || '' }
      })
      return { files, diff: diffText }
    }

    async refresh(options = {}) {
      if (!this.panel) return
      const mode = options.mode || 'full'
      const folder = chronicleWorkspaceFolder()
      if (!folder || folder.uri.scheme !== 'file') {
        this.panel.webview.html = this.errorHtml(this.panel.webview, 'Откройте локальный Git-проект, чтобы читать его летопись.')
        this.hydrated = false
        return
      }
      const version = ++this.refreshVersion
      try {
        const cwd = await resolveGitRoot(folder.uri.fsPath) || folder.uri.fsPath
        if (mode === 'detail' && this.hydrated && this.selectedCommit) {
          const selected = { hash: this.selectedCommit }
          const meta = await runGit(cwd, ['show', '-s', '--date=iso-strict', '--pretty=format:%H%x1f%h%x1f%an%x1f%ad%x1f%s', this.selectedCommit])
          const [hash, shortHash, author, date, ...subject] = meta.split('\x1f')
          const commit = { hash, shortHash, author, date, subject: subject.join('\x1f') }
          const { files, diff } = await this.loadCommitDetail(cwd, commit)
          if (!this.panel || version !== this.refreshVersion) return
          void this.panel.webview.postMessage({
            type: 'detail',
            selectedHash: commit.hash,
            detailHtml: this.detailMarkup(commit, files, diff),
          })
          return
        }

        const [branch, statusText, logText] = await Promise.all([
          runGit(cwd, ['rev-parse', '--abbrev-ref', 'HEAD']),
          runGit(cwd, ['status', '--porcelain=v1', '--branch']),
          runGit(cwd, ['log', '--max-count=80', '--date=iso-strict', '--pretty=format:%H%x1f%h%x1f%an%x1f%ad%x1f%s%x1e']),
        ])
        const commits = logText.split('\x1e').map(row => row.trim()).filter(Boolean).map(row => {
          const [hash, shortHash, author, date, ...subject] = row.split('\x1f')
          return { hash, shortHash, author, date, subject: subject.join('\x1f') }
        })
        if (!this.selectedCommit || !commits.some(commit => commit.hash === this.selectedCommit)) this.selectedCommit = commits[0]?.hash || ''
        const selected = commits.find(commit => commit.hash === this.selectedCommit)
        const { files, diff } = await this.loadCommitDetail(cwd, selected)
        if (!this.panel || version !== this.refreshVersion) return
        const statusLines = statusText.split(/\r?\n/).filter(Boolean)
        const changed = statusLines.filter(line => !line.startsWith('##'))
        const staged = changed.filter(line => line[0] && line[0] !== ' ' && line[0] !== '?').length
        const untracked = changed.filter(line => line.startsWith('??')).length
        const working = changed.length - staged - untracked
        const data = {
          project: folder.name,
          branch: branch.trim(),
          commits,
          selected,
          files,
          diff,
          workingTree: { changed: changed.length, staged, untracked, working },
        }
        if (!this.hydrated) {
          this.panel.webview.html = this.renderHtml(this.panel.webview, data)
          this.hydrated = true
          return
        }
        void this.panel.webview.postMessage({
          type: 'snapshot',
          selectedHash: selected?.hash || '',
          headerHtml: this.headerMarkup(data),
          workingTreeHtml: this.workingTreeMarkup(data),
          commitListHtml: this.commitListMarkup(data.commits, selected?.hash || ''),
          detailHtml: selected ? this.detailMarkup(selected, files, diff) : '<div class="empty-chronicle">Выберите главу слева.</div>',
        })
      } catch (error) {
        if (!this.panel || version !== this.refreshVersion) return
        const message = error instanceof Error ? error.message : String(error)
        this.panel.webview.html = this.errorHtml(this.panel.webview, /not a git repository/i.test(message) ? 'В открытом мире ещё нет Git-летописи. Создайте репозиторий в разделе «Летопись».' : message)
        this.hydrated = false
      }
    }

    document(webview, content, title = 'Летопись Point') {
      const nonce = Math.random().toString(36).slice(2)
      const tokens = webview.asWebviewUri(vscode.Uri.joinPath(this.context.extensionUri, 'media', 'rpg-tokens.css'))
      const style = webview.asWebviewUri(vscode.Uri.joinPath(this.context.extensionUri, 'media', 'chronicle.css'))
      const script = webview.asWebviewUri(vscode.Uri.joinPath(this.context.extensionUri, 'media', 'chronicle.js'))
      return `<!doctype html><html lang="ru"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src ${webview.cspSource}; script-src 'nonce-${nonce}';"><link rel="stylesheet" href="${tokens}"><link rel="stylesheet" href="${style}"><title>${escapeHtml(title)}</title></head><body>${content}<script nonce="${nonce}" src="${script}"></script></body></html>`
    }

    loadingHtml(webview) {
      return this.document(webview, '<main class="chronicle-state"><span>✦</span><strong>ЧИТАЕМ ЛЕТОПИСЬ МИРА</strong><small>Только локальная история Git</small></main>')
    }

    errorHtml(webview, message) {
      return this.document(webview, `<main class="chronicle-state"><span>!</span><strong>ЛЕТОПИСЬ НЕДОСТУПНА</strong><p>${escapeHtml(message)}</p><div><button data-action="openWorkingTree">Открыть рабочее дерево</button><button data-action="refresh">Повторить</button></div></main>`)
    }

    headerMarkup(data) {
      return `<header><div><span>GIT / ЛЕТОПИСЬ</span><h1>${escapeHtml(data.project)}</h1></div><label>ВЕТВЬ <strong>⑂ ${escapeHtml(data.branch || '—')}</strong></label><button data-action="refresh">↻ ОБНОВИТЬ</button></header>`
    }

    workingTreeMarkup(data) {
      return `<section class="working-tree"><div><small>РАБОЧЕЕ ДЕРЕВО</small><strong>${data.workingTree.changed ? `${data.workingTree.changed} изменений` : 'чисто'}</strong></div><span><b>${data.workingTree.working}</b> изменено</span><span><b>${data.workingTree.staged}</b> подготовлено</span><span><b>${data.workingTree.untracked}</b> новых</span><button data-action="openWorkingTree">ОТКРЫТЬ ИЗМЕНЕНИЯ →</button></section>`
    }

    commitListMarkup(commits, selectedHash) {
      return commits.map((commit, index) => `<button class="commit-row ${commit.hash === selectedHash ? 'selected' : ''}" data-action="selectCommit" data-hash="${escapeHtml(commit.hash)}"><span class="graph-node tone-${index % 4}"><i></i></span><strong>${escapeHtml(commit.subject)}</strong><code>${escapeHtml(commit.shortHash)}</code><small>${escapeHtml(commit.author)}</small><time>${escapeHtml(formatChronicleDate(commit.date))}</time></button>`).join('') || '<p>В летописи пока нет коммитов.</p>'
    }

    detailMarkup(selected, files, diff) {
      const fileRows = files.map(file => `<button data-action="openFile" data-path="${escapeHtml(file.path)}"><span class="file-status status-${escapeHtml(file.status[0] || 'M')}">${escapeHtml(file.status)}</span><strong>${escapeHtml(file.path)}</strong></button>`).join('')
      return `<header><span>ВЫБРАННАЯ ГЛАВА</span><h2>${escapeHtml(selected.subject)}</h2><div><small>АВТОР</small><strong>${escapeHtml(selected.author)}</strong><small>ХЕШ</small><code>${escapeHtml(selected.shortHash)}</code><small>ДАТА</small><time>${escapeHtml(formatDateTime(selected.date))}</time></div></header><section class="changed-files"><header><strong>ИЗМЕНЁННЫЕ АРТЕФАКТЫ</strong><span>${files.length}</span></header><div>${fileRows || '<p>Список файлов пуст.</p>'}</div></section><section class="diff-scroll"><header><strong>СВИТОК ИЗМЕНЕНИЙ</strong><span>READ ONLY</span></header><pre>${renderDiff(diff)}</pre></section>`
    }

    renderHtml(webview, data) {
      const selected = data.selected
      return this.document(webview, `<main class="chronicle">${this.headerMarkup(data)}${this.workingTreeMarkup(data)}<div class="chronicle-layout"><section class="commit-list"><header><strong>ГЛАВЫ МИРА</strong><span>${data.commits.length}</span></header><div>${this.commitListMarkup(data.commits, selected?.hash || '')}</div></section><section class="commit-detail">${selected ? this.detailMarkup(selected, data.files, data.diff) : '<div class="empty-chronicle">Выберите главу слева.</div>'}</section></div><footer><span>Летопись читается напрямую из локального Git.</span><span>Никаких аккаунтов и внешней синхронизации</span></footer></main>`)
    }
  }

  function formatChronicleDate(value) {
    const date = new Date(value)
    const difference = Date.now() - date.getTime()
    const hours = Math.max(0, Math.floor(difference / 3_600_000))
    if (hours < 1) return 'только что'
    if (hours < 24) return `${hours} ч назад`
    const days = Math.floor(hours / 24)
    return days < 30 ? `${days} дн назад` : date.toLocaleDateString('ru-RU')
  }

  function renderDiff(value) {
    const source = String(value || '').slice(0, 700 * 1024)
    return source.split(/\r?\n/).map(line => {
      const className = line.startsWith('+') && !line.startsWith('+++') ? 'add' : line.startsWith('-') && !line.startsWith('---') ? 'remove' : line.startsWith('@@') ? 'hunk' : line.startsWith('diff --git') ? 'file' : ''
      return `<span${className ? ` class="${className}"` : ''}>${escapeHtml(line)}</span>`
    }).join('\n')
  }

  return { PointHome, PointChronicle }
}

module.exports = { createPointPanels }
