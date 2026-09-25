// Интеграции со стороны хоста: MCP-серверы владельца (mcp-controller.js) и
// плагин GitLab (gitlab-controller.js). Composition root отдаёт сюда два типа
// сообщений вебвью — mcpAction и gitlabAction — и больше о предмете не знает.

const vscode = require('vscode')
const { createMcpController } = require('./mcp-controller')
const { createGitLabController } = require('./gitlab-controller')

function createIntegrationsController(provider) {
  const request = (route, init = {}) => provider.service.request(route, init)
  // Ключ процесса ядра: секреты MCP живут в его памяти и пропадают вместе с ним.
  const coreKey = () => {
    const base = provider.service.baseUrl || ''
    return base ? `${base}#${provider.service.process?.pid || ''}` : ''
  }
  const mcp = createMcpController({
    vscode, secrets: provider.context.secrets, request, coreKey, post: message => provider.post(message),
  })
  const gitlab = createGitLabController({
    vscode, provider, request, secrets: provider.context.secrets,
    unlock: () => mcp.ensureUnlocked(),
    publishServers: extra => mcp.publish(extra),
  })

  async function handle(message) {
    if (message?.type === 'mcpAction') {
      await mcp.handle(message)
      // Доверие, проверка и удаление меняют и то, что видит окно GitLab.
      if (['trust', 'probe', 'delete', 'secret', 'stop'].includes(String(message.action || ''))) gitlab.announceChange()
      return
    }
    if (message?.type === 'gitlabAction') await gitlab.handle(message)
  }

  function register(context) {
    gitlab.register(context)
    context.subscriptions.push(vscode.commands.registerCommand('localAgent.openIntegrations', () => provider.showWide('integrations')))
  }

  return { handle, register }
}

module.exports = { createIntegrationsController }
