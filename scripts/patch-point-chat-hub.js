const fs = require('fs')
const path = require('path')

const root = path.resolve(__dirname, '..')
const files = [
  path.join(root, '.cache/VSCode-win32-x64/resources/app/out/vs/workbench/workbench.desktop.main.js'),
  path.join(root, '.cache/VSCode-win32-x64/resources/app/out/vs/sessions/sessions.desktop.main.js'),
]

const descRe = /openCommandActionDescriptor: \{\s*id: ChatViewContainerId,\s*title: chatViewContainer\.title,\s*mnemonicTitle: localize\((\d+), null\),\s*keybindings: \{\s*primary: 2048 \/\* CtrlCmd \*\/ \| 512 \/\* Alt \*\/ \| 39 \/\* KeyI \*\/,\s*mac: \{\s*primary: 2048 \/\* CtrlCmd \*\/ \| 256 \/\* WinCtrl \*\/ \| 39 \/\* KeyI \*\/\s*\}\s*\},\s*order: 1\s*\},\s*ctorDescriptor: new SyncDescriptor\(ChatViewPane\),\s*when: ContextKeyExpr\.and\(\s*ChatContextKeys\.accountPolicyGateActive\.negate\(\),\s*ContextKeyExpr\.or\(\s*ContextKeyExpr\.and\(\s*ChatContextKeys\.Setup\.hidden\.negate\(\),\s*ChatContextKeys\.Setup\.disabledInWorkspace\.negate\(\)\s*\),\s*ChatContextKeys\.panelParticipantRegistered,\s*ChatContextKeys\.extensionInvalid\s*\)\s*\)\s*\}/

function inspectPointChatContract(text) {
  const chatStart = text.indexOf('chat-view-icon')
  const chatRegistration = chatStart >= 0 ? text.slice(chatStart, chatStart + 4200) : ''
  const applicationName = '[A-Za-z_$][\\w$]*\\.applicationName'
  const point = "[\"']point[\"']"
  const commandPrefix = applicationName + '\\s*===\\s*' + point + '[\\s\\S]{0,420}?executeCommand\\s*\\(\\s*'

  return {
    chatRegistrationFound: chatStart >= 0,
    notDefault: new RegExp('isDefault\\s*:\\s*' + applicationName + '\\s*!==\\s*' + point).test(chatRegistration),
    openCommandDisabled: new RegExp('openCommandActionDescriptor\\s*:\\s*' + applicationName + '\\s*===\\s*' + point + '\\s*\\?\\s*(?:void\\s+0|undefined)\\s*:').test(chatRegistration),
    viewHidden: new RegExp('when\\s*:\\s*' + applicationName + '\\s*===\\s*' + point + '\\s*\\?\\s*[A-Za-z_$][\\w$]*\\.false\\(\\)\\s*:').test(chatRegistration),
    quickRedirects: (text.match(new RegExp(commandPrefix + "[\"']localAgent\\.quickChat[\"']", 'g')) || []).length,
    openRedirects: (text.match(new RegExp(commandPrefix + "[\"']localAgent\\.open[\"']", 'g')) || []).length,
  }
}

function missingPointChatContract(contract) {
  const missing = []
  if (!contract.chatRegistrationFound) missing.push('chat registration')
  if (!contract.notDefault) missing.push('non-default Chat container')
  if (!contract.openCommandDisabled) missing.push('disabled Chat open command')
  if (!contract.viewHidden) missing.push('hidden Chat view')
  if (contract.quickRedirects < 2) missing.push('both Quick Chat redirects')
  if (contract.openRedirects < 1) missing.push('Chat-to-Hub redirect')
  return missing
}

function assertPointChatContract(text, label = 'bundle') {
  const contract = inspectPointChatContract(text)
  const missing = missingPointChatContract(contract)
  if (missing.length > 0) {
    throw new Error('Point Chat-to-Hub contract is incomplete in ' + label + ': ' + missing.join(', '))
  }
  return contract
}

function main() {
for (const file of files) {
  if (!fs.existsSync(file)) throw new Error(`missing ${file}`)
  let text = fs.readFileSync(file, 'utf8')
  const before = text
  const existingContract = inspectPointChatContract(text)
  if (missingPointChatContract(existingContract).length === 0) {
    console.log(JSON.stringify({
      file: path.basename(path.dirname(file)) + '/' + path.basename(file),
      changed: false,
      ...existingContract,
    }))
    continue
  }

  text = text.replace(
    '}, 2 /* AuxiliaryBar */, { isDefault: true, doNotRegisterOpenCommand: true });',
    '}, 2 /* AuxiliaryBar */, { isDefault: product_default.applicationName !== "point", doNotRegisterOpenCommand: true });',
  )

  if (!descRe.test(text) && !text.includes('when: product_default.applicationName === "point" ? ContextKeyExpr.false()')) {
    const idx = text.indexOf('var chatViewDescriptor = {')
    throw new Error(`chat view descriptor not found in ${file}\n${text.slice(idx, idx + 900)}`)
  }

  text = text.replace(descRe, (_m, locId) => `openCommandActionDescriptor: product_default.applicationName === "point" ? void 0 : {
    id: ChatViewContainerId,
    title: chatViewContainer.title,
    mnemonicTitle: localize(${locId}, null),
    keybindings: {
      primary: 2048 /* CtrlCmd */ | 512 /* Alt */ | 39 /* KeyI */,
      mac: {
        primary: 2048 /* CtrlCmd */ | 256 /* WinCtrl */ | 39 /* KeyI */
      }
    },
    order: 1
  },
  ctorDescriptor: new SyncDescriptor(ChatViewPane),
  when: product_default.applicationName === "point" ? ContextKeyExpr.false() : ContextKeyExpr.and(
    ChatContextKeys.accountPolicyGateActive.negate(),
    ContextKeyExpr.or(
      ContextKeyExpr.and(
        ChatContextKeys.Setup.hidden.negate(),
        ChatContextKeys.Setup.disabledInWorkspace.negate()
      ),
      ChatContextKeys.panelParticipantRegistered,
      ChatContextKeys.extensionInvalid
    )
  )
}`)

  const q1 = `  run(accessor, query) {
    const quickChatService = accessor.get(IQuickChatService);
    let options3;
    switch (typeof query) {
      case "string":
        options3 = { query };
        break;
      case "object":
        options3 = query;
        break;
    }
    if (options3?.query) {
      options3.selection = new Selection(1, options3.query.length + 1, 1, options3.query.length + 1);
    }
    quickChatService.toggle(options3);
  }
};`
  const q1n = `  run(accessor, query) {
    if (product_default.applicationName === "point") {
      const pointQuery = typeof query === "string" ? query : query?.query;
      void accessor.get(ICommandService).executeCommand("localAgent.quickChat", pointQuery);
      return;
    }
    const quickChatService = accessor.get(IQuickChatService);
    let options3;
    switch (typeof query) {
      case "string":
        options3 = { query };
        break;
      case "object":
        options3 = query;
        break;
    }
    if (options3?.query) {
      options3.selection = new Selection(1, options3.query.length + 1, 1, options3.query.length + 1);
    }
    quickChatService.toggle(options3);
  }
};`
  if (text.includes(q1)) text = text.split(q1).join(q1n)

  const q2 = `  run(accessor, query) {
    const quickChatService = accessor.get(IQuickChatService);
    quickChatService.toggle(query ? {
      query,
      selection: new Selection(1, query.length + 1, 1, query.length + 1)
    } : void 0);
  }
};`
  const q2n = `  run(accessor, query) {
    if (product_default.applicationName === "point") {
      void accessor.get(ICommandService).executeCommand("localAgent.quickChat", query);
      return;
    }
    const quickChatService = accessor.get(IQuickChatService);
    quickChatService.toggle(query ? {
      query,
      selection: new Selection(1, query.length + 1, 1, query.length + 1)
    } : void 0);
  }
};`
  if (text.includes(q2)) text = text.split(q2).join(q2n)

  const contract = assertPointChatContract(text, file)
  if (text === before) {
    console.log(JSON.stringify({
      file: path.basename(path.dirname(file)) + '/' + path.basename(file),
      changed: false,
      ...contract,
    }))
    continue
  }
  fs.writeFileSync(file, text)
  console.log(JSON.stringify({
    file: path.basename(path.dirname(file)) + '/' + path.basename(file),
    changed: true,
    ...contract,
  }))
}
}

if (require.main === module) main()

module.exports = {
  assertPointChatContract,
  inspectPointChatContract,
  missingPointChatContract,
}
