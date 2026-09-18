const assert = require('assert')

const {
  assertPointChatContract,
  inspectPointChatContract,
} = require('./patch-point-chat-hub')

const compiledFixture = [
  'registerIcon("chat-view-icon")',
  'register({isDefault:Tt.applicationName!=="point"})',
  'const view={openCommandActionDescriptor:Tt.applicationName==="point"?void 0:{id:"chat"},when:Tt.applicationName==="point"?x.false():x.and(enabled)}',
  'if(Tt.applicationName==="point"){service.executeCommand("localAgent.quickChat",query);return}',
  'if(Tt.applicationName==="point"){service.executeCommand("localAgent.quickChat",query);return}',
  'if(Tt.applicationName==="point"){await service.executeCommand("localAgent.open");return}',
].join(';')

const contract = assertPointChatContract(compiledFixture, 'minified fixture')
assert.strictEqual(contract.notDefault, true)
assert.strictEqual(contract.openCommandDisabled, true)
assert.strictEqual(contract.viewHidden, true)
assert.strictEqual(contract.quickRedirects, 2)
assert.strictEqual(contract.openRedirects, 1)

const broken = compiledFixture.replace('localAgent.quickChat', 'workbench.action.quickchat.toggle')
assert.strictEqual(inspectPointChatContract(broken).quickRedirects, 1)
assert.throws(
  () => assertPointChatContract(broken, 'broken fixture'),
  /both Quick Chat redirects/,
)

console.log('patch-point-chat-hub contract tests passed')
