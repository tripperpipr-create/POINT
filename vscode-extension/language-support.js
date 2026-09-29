// Поддержка языков по требованию: language server ставится только для
// открытого языка. Команда «Поддержка языков» показывает текущий язык и
// популярные, ставит расширение из Marketplace (снимая обязательную подпись,
// которую Code-OSS без магазина Microsoft не проверит) и просит перезагрузить
// окно, чтобы Ctrl+Click и usages заработали.
//
// Таблицы языков живут в ide-action-controller.js и приходят сюда доводами:
// ими же пользуется навигация, и второй копии у них быть не должно.

function createLanguageSupport({ vscode, LANGUAGE_SUPPORT, BUILTIN_LANGUAGE_SUPPORT, LANGUAGE_LABELS }) {
  async function ensureMarketplaceInstallAllowed() {
    const config = vscode.workspace.getConfiguration('extensions')
    if (config.get('verifySignature') === false) return
    try {
      await config.update('verifySignature', false, vscode.ConfigurationTarget.Application)
    } catch {
      // Application settings can be locked; Marketplace install may still prompt.
    }
  }

  async function ensureGoVulncheckCompatible() {
    // golang.Go defaults go.diagnostic.vulncheck to "Prompt", but gopls <0.21 rejects it.
    const config = vscode.workspace.getConfiguration('go')
    const inspect = config.inspect('diagnostic.vulncheck')
    const values = [
      inspect?.globalValue,
      inspect?.workspaceValue,
      inspect?.workspaceFolderValue,
      inspect?.defaultValue,
    ]
    if (!values.includes('Prompt')) return
    const current = config.get('diagnostic.vulncheck')
    if (current && current !== 'Prompt') return
    try {
      await config.update('diagnostic.vulncheck', 'Off', vscode.ConfigurationTarget.Global)
    } catch {
      // Best-effort override; configurationDefaults in package.json covers new installs.
    }
  }

  async function installLanguageExtension(extensionId, label) {
    await ensureMarketplaceInstallAllowed()
    if (vscode.extensions.getExtension(extensionId)) {
      await vscode.window.showInformationMessage(`${label} уже установлено (${extensionId}).`)
      return
    }
    await vscode.window.withProgress(
      { location: vscode.ProgressLocation.Notification, title: `Установка ${label}…`, cancellable: false },
      async () => {
        await vscode.commands.executeCommand('workbench.extensions.installExtension', extensionId)
      },
    )
    const installed = vscode.extensions.getExtension(extensionId)
    if (installed) {
      if (/^golang\.go$/i.test(extensionId)) await ensureGoVulncheckCompatible()
      const reload = await vscode.window.showInformationMessage(
        `${label} установлено. Перезагрузите окно, чтобы language server начал Ctrl+Click и usages.`,
        'Перезагрузить',
      )
      if (reload === 'Перезагрузить') await vscode.commands.executeCommand('workbench.action.reloadWindow')
      return
    }
    await vscode.commands.executeCommand('workbench.extensions.search', `@id:${extensionId}`)
    await vscode.window.showWarningMessage(
      `Не удалось установить ${label} автоматически. Откройте карточку расширения и нажмите «Установить» (при запросе подписи — «Все равно установить»).`,
    )
  }

  async function openLanguageSupport() {
    const languageId = vscode.window.activeTextEditor?.document.languageId || ''
    const current = LANGUAGE_SUPPORT.find(item => item.ids.includes(languageId))
    const builtin = BUILTIN_LANGUAGE_SUPPORT.has(languageId)
    const items = []
    if (languageId) {
      const languageLabel = current?.label || LANGUAGE_LABELS[languageId] || languageId
      items.push({
        label: `$(symbol-keyword) Текущий язык: ${languageLabel}`,
        description: builtin ? 'Расширенная поддержка уже встроена' : current ? 'Установить / открыть расширение' : 'Найти language server',
        detail: current?.extension || '',
        profile: current,
        builtin,
        languageId,
      })
      items.push({ label: 'Популярные языки', kind: vscode.QuickPickItemKind.Separator })
    }
    items.push(...LANGUAGE_SUPPORT.map(profile => ({
      label: `$(extensions) ${profile.label}`,
      description: vscode.extensions.getExtension(profile.extension) ? 'Установлено' : 'Установить по требованию',
      detail: profile.extension,
      profile,
    })))
    items.push({ label: 'Другой язык', kind: vscode.QuickPickItemKind.Separator })
    items.push({
      label: '$(search) Найти поддержку другого языка',
      description: 'Каталог расширений',
      languageId,
    })
    const selected = await vscode.window.showQuickPick(items, {
      title: 'Point — поддержка языков',
      placeHolder: 'Language server запускается только для открытого языка',
      matchOnDescription: true,
      matchOnDetail: true,
    })
    if (!selected) return
    if (selected.builtin) {
      await vscode.window.showInformationMessage(`${selected.label.replace(/^\$\([^)]*\)\s*/, '')}: навигация и поиск использований уже встроены в Point.`)
      return
    }
    if (selected.profile) {
      await installLanguageExtension(selected.profile.extension, selected.profile.label)
      return
    }
    await vscode.commands.executeCommand('workbench.extensions.search', `@category:"Programming Languages" ${selected.languageId || ''}`.trim())
  }

  return { openLanguageSupport }
}

module.exports = { createLanguageSupport }
