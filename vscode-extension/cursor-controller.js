// Исполнитель Cursor: вход, выход, запуск и отмена.
//
// У интерактивных исполнений Cursor нет headless RunID — работу ведёт IDE, а
// ядро только помечает начало и конец. Отсюда и отдельный путь, и отдельные
// ветки: они не похожи ни на прогон агента, ни на шаг workflow. Рядом уже
// лежит cursor-runtime.js с самим временем жизни процесса.
//
// Зависимостей нет: обработчик вызывается через .call(this, message).

const vscode = require('vscode')

async function handleCursorMessage(message) {
  switch (message.type) {
    case 'launchCursorAgent':
      await this.launchCursorAgent(message); break
    case 'cursorRefresh':
      await this.refreshCursorRuntime(); break
    case 'cursorLogin':
      this.cursorRuntimeState = await cursorRuntime.login({
        apiKeyName: 'Point IDE',
        openBrowser: async url => {
          await vscode.env.openExternal(vscode.Uri.parse(url))
        },
        onLoginUrl: url => {
          this.output.appendLine(`[Cursor] Login URL: ${url}`)
          void vscode.window.showInformationMessage('Откройте страницу входа Cursor в браузере, затем вернитесь в Point.', 'Открыть')
            .then(choice => {
              if (choice === 'Открыть') void vscode.env.openExternal(vscode.Uri.parse(url))
            })
        },
      })
      this.postCursorRuntime()
      this.postState(true)
      break
    case 'cursorLogout':
      this.cursorRuntimeState = await cursorRuntime.logout()
      this.postCursorRuntime()
      this.postState(true)
      break
    case 'startCursorRun':
      await this.startCursorRun(message); break
    case 'cancelCursorRun':
      await this.cursorRun?.cancel()
      break
    case 'cancelCursorExecution':
      if (!this.cursorRun || this.cursorHubExecutionId !== String(message.id || '')) {
        throw new Error('Это Cursor-исполнение сейчас не запущено в Point.')
      }
      await this.cursorRun.cancel()
      break
  }
}

module.exports = { handleCursorMessage }
