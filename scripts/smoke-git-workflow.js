const Module = require('module')
const { extensionHostSource } = require('./lib/extension-host-source')
const { overlaySource: readOverlaySource } = require('./lib/overlay-source')
const path = require('path')
const assert = require('assert')
const fs = require('fs')
const vm = require('vm')

const originalLoad = Module._load
Module._load = function load(request, parent, isMain) {
  if (request === 'vscode') {
    return {
      workspace: {
        isTrusted: true,
        workspaceFolders: [{ name: 'smoke', uri: { scheme: 'file', fsPath: process.cwd() } }],
        getConfiguration: () => ({ get: (_key, fallback) => fallback }),
        asRelativePath: uri => path.basename(String(uri?.fsPath || '')),
        onDidChangeWorkspaceFolders: () => ({ dispose() {} }),
        onDidChangeConfiguration: () => ({ dispose() {} }),
      },
      Uri: {
        file: fsPath => ({ fsPath: String(fsPath), scheme: 'file', toString: () => String(fsPath) }),
        parse: value => ({ fsPath: String(value), scheme: 'file', toString: () => String(value) }),
        joinPath: (...parts) => ({ fsPath: parts.join('/'), scheme: 'file', toString: () => parts.join('/') }),
      },
      window: {
        createOutputChannel: () => ({ append() {}, appendLine() {}, dispose() {} }),
        showInformationMessage: async () => undefined,
        showWarningMessage: async () => undefined,
        showErrorMessage: async () => undefined,
      },
      commands: { registerCommand: () => ({ dispose() {} }), executeCommand: async () => undefined },
      extensions: { getExtension: () => undefined },
      EventEmitter: class {
        constructor() { this.event = () => ({ dispose() {} }) }
        fire() {}
        dispose() {}
      },
      ViewColumn: { One: 1 },
      TreeItemCollapsibleState: { None: 0, Collapsed: 1 },
      StatusBarAlignment: { Left: 1, Right: 2 },
      ThemeIcon: class { constructor(id) { this.id = id } },
      Disposable: { from: (...items) => ({ dispose() { for (const item of items) item?.dispose?.() } }) },
    }
  }
  return originalLoad(request, parent, isMain)
}

const {
  pathIsUnder,
  pickGitRepository,
  pathRelativeToRoot,
  formatVcsError,
  gitListsState,
} = require(path.resolve(__dirname, '..', 'vscode-extension', 'extension.js')).__test

assert.equal(pathIsUnder('C:\\foo\\bar\\file.go', 'C:\\foo\\bar'), true)
assert.equal(pathIsUnder('C:\\foo\\barfile\\x.go', 'C:\\foo\\bar'), false, 'prefix sibling must not match')
assert.equal(pathIsUnder('C:\\Foo\\Bar\\x.go', 'c:\\foo\\bar'), true, 'Windows path compare is case-insensitive')

const nested = pickGitRepository([
  { rootUri: { fsPath: 'C:\\repo' } },
  { rootUri: { fsPath: 'C:\\repo\\packages\\app' } },
], { fsPath: 'C:\\repo\\packages\\app\\main.go' })
assert.equal(nested.rootUri.fsPath, 'C:\\repo\\packages\\app')

assert.equal(pathRelativeToRoot({ fsPath: 'C:\\repo\\src\\a.go' }, 'C:\\repo'), 'src/a.go')
assert.match(formatVcsError('Push недоступен', new Error('remote rejected')), /remote rejected/)

// Папки изменений. Назначение живёт ровно столько, сколько живёт изменение:
// иначе список рос бы вечно и возвращал файл в чужую папку после коммита.
{
  const lists = gitListsState(
    { lists: [{ id: 'default', name: 'Изменения' }, { id: 'l1', name: 'Рефакторинг' }], active: 'l1', assign: { 'a.js': 'l1', 'gone.js': 'l1' } },
    [{ path: 'a.js', area: 'working' }, { path: 'b.js', area: 'working' }, { path: 'n.js', area: 'untracked' }],
  )
  assert.equal(lists.assign['a.js'], 'l1', 'existing assignment must survive a refresh')
  assert.equal(lists.assign['b.js'], 'l1', 'a new change belongs to the active folder')
  assert.ok(!('gone.js' in lists.assign), 'a committed file must not keep its folder')
  assert.ok(!('n.js' in lists.assign), 'untracked files live in their own group')
  const broken = gitListsState({ lists: [{ id: 'l1', name: 'Рефакторинг' }], active: 'zzz', assign: { 'a.js': 'zzz' } }, [{ path: 'a.js', area: 'working' }])
  assert.equal(broken.lists[0].id, 'default', 'the default folder always exists')
  assert.equal(broken.active, 'default', 'an unknown active folder falls back to the default one')
  assert.equal(broken.assign['a.js'], 'default', 'an assignment to a deleted folder falls back too')
}


async function workbenchSmoke() {
  const {createGitWorkbenchTools}=require('../vscode-extension/git-workbench-controller')
  const calls=[]
  const root='C:\\fixture',workspaceId='world-1'
  const state={root,revision:'seen-revision',head:'abc',branch:'main',remote:'origin/main',remotes:[{name:'origin'}],
    branches:['main'],changes:[{path:'shared.txt',area:'staged'},{path:'shared.txt',area:'working'}]}
  const repo={rootUri:{fsPath:root}}
  const tools=createGitWorkbenchTools({path,vscode:{window:{
    showWarningMessage:async()=> 'Выполнить',
  }}})
  const provider={
    gitContext:async()=>({repo}),gitWorkbenchState:{...state,workspaceId},
    service:{request:async(route,opts)=>{
      const body=JSON.parse(opts.body);calls.push({route,body})
      return {snapshot:state,message:'done',commit:'abc'}
    }},
  }
  await tools.runGitWorkbenchAction(provider,{action:'commit',message:'Prepared only',paths:['unselected.txt'],revision:'seen-revision',type:'gitAction'})
  assert.equal(calls[0].route,'/api/v2/git/actions')
  assert.equal(calls[0].body.revision,'seen-revision')
  assert.equal(calls[0].body.workspaceId,workspaceId)
  assert.equal(calls[0].body.repoRoot,root)
  assert.equal(calls[0].body.type,undefined,'transport fields must not leak to core')
  await tools.runGitWorkbenchAction(provider,{action:'stage',path:'shared.txt',revision:'old-view'})
  assert.equal(calls[1].body.revision,'old-view','never silently replace the revision the user saw')

  const listeners={},posted=[]
  const source=fs.readFileSync(path.resolve(__dirname,'../vscode-extension/ui/client/git-workspace-ui.js'),'utf8').replace(/export function/g,'function').replace(/^import[^\n]+\n/,'')
  const context={
    document:{body:{dataset:{}},hidden:false},window:{addEventListener(){}},
    setTimeout(){return 1},clearTimeout(){},setInterval(){return 1},clearInterval(){},
    console,Map,Set,Promise,JSON,countOf:(count,one,few,many)=>count+' '+(count===1?one:count<5?few:many),
  }
  vm.createContext(context);vm.runInContext(source,context)
  const fixture={workspaces:[{root,name:'fixture',branch:'main',changes:2}]}
  let ui
  const transport={postMessage(m){
    posted.push(m)
    const result=m.op==='inventory'?{workspaceId,repositories:fixture.workspaces}:m.op==='connections'?[]:
      m.op==='bindings'?{candidates:[]}:m.op==='status'?{git:state}:[]
    queueMicrotask(()=>ui.receive({type:'gitWorkspaceResult',id:m.id,ok:true,data:result}))
  }}
  ui=context.createGitWorkspaceUi({root:{},vscode:transport,render(){},persist(){},persisted:{screen:'changes'}})
  ui.start()
  await new Promise(resolve=>setImmediate(resolve))
  assert.match(ui.view(),/Подготовленные/)
  assert.match(ui.view(),/Неподготовленные/)
  assert.match(ui.view(),/data-path="shared.txt" data-area="staged"/);assert.match(ui.view(),/data-path="shared.txt" data-area="working"/,'same path has independent staged and working actions')
  ui.input({target:{id:'gw-commit',dataset:{gwField:'commit'},value:'keep me',type:'textarea'}})
  const saved=ui.snapshot()
  assert.equal(saved.drafts[workspaceId+':'+root].commit,'keep me')
  await ui.click('gw-refresh',{dataset:{}})
  assert.match(ui.view(),/keep me/,'draft survives refresh')

  const actionsSource=fs.readFileSync(path.resolve(__dirname,'../vscode-extension/ui/client/git-actions.js'),'utf8').replace(/export function/g,'function')
  vm.runInContext(actionsSource,context)
  const panel={gitCommitDraft:'legacy',gitChecked:new Set(),gitKnown:new Set(),gitCollapsed:new Set()}
  const memory=context.createGitPanelMemory(panel)
  memory.select('world:a');panel.gitCommitDraft='A';panel.gitChecked.add('a.txt')
  memory.select('world:b');assert.equal(panel.gitCommitDraft,'');panel.gitCommitDraft='B'
  memory.select('world:a');assert.equal(panel.gitCommitDraft,'A');assert.ok(panel.gitChecked.has('a.txt'))
  const restored={gitChecked:new Set(),gitKnown:new Set(),gitCollapsed:new Set()}
  const reopened=context.createGitPanelMemory(restored,memory.snapshot())
  reopened.select('world:b');assert.equal(restored.gitCommitDraft,'B')
  const graph=context.graphRows([{hash:'merge',parents:['left','right']},{hash:'left',parents:['base']},{hash:'right',parents:['base']},{hash:'base',parents:[]}])
  assert.match(graph[0].graph,/родителей: 2/)
  assert.ok(graph[0].graph.match(/<path/g).length===2,'merge emits two parent connections')
  assert.match(ui.view(),/Коммит включает только содержимое index/)

  const commands=new Map(),opened=[],reviewWrites=[]
  const makeUri=spec=>({...spec,toString:()=>JSON.stringify(spec)})
  const review={sha:'head',diffRefs:{baseSha:'base',headSha:'head',startSha:'start'}}
  const native={Uri:{from:makeUri,parse:raw=>makeUri(JSON.parse(raw))},
    Range:class {constructor(line){this.start={line};this.end={line}}},CommentMode:{Preview:1},CommentThreadState:{},
    workspace:{registerTextDocumentContentProvider(){return {dispose(){}}}},
    window:{showErrorMessage(message){throw new Error(message)}},
    comments:{createCommentController(){return {options:{},dispose(){}}}},
    commands:{registerCommand(name,fn){commands.set(name,fn);return {dispose(){}}},
      async executeCommand(name,...args){opened.push(args)}},
  }
  const editorModule={exports:{}}
  vm.runInNewContext(fs.readFileSync(path.resolve(__dirname,'../vscode-extension/git-review-editors.js'),'utf8'),
    {require:()=>native,module:editorModule,Map,Set,JSON,Date,console})
  const editors=editorModule.exports.createReviewEditors({context:{subscriptions:[]}},async input=>{
    if(input.action==='review')return {data:review}
    if(input.action==='discussions')return {data:[]}
    reviewWrites.push(input);return {data:{done:true}}
  },async()=>{})
  await editors.openDiff({review,file:{oldPath:'a.txt',newPath:'a.txt',
    diff:'--- a/a.txt\n+++ b/a.txt\n@@ -10,3 +10,3 @@\n keep\n-old\n+new\n next\n'},
    connectionId:'account',project:'group/repo',iid:7})
  const [left,right]=opened[0]
  for(const [uri,line] of [[right,10],[left,10],[right,11]]) {
    await commands.get('localAgent.reviewReply')({text:'Review',thread:{uri,range:new native.Range(line),dispose(){}}})
  }
  assert.equal(reviewWrites[0].position.newLine,11);assert.equal(reviewWrites[0].position.oldLine,undefined)
  assert.equal(reviewWrites[1].position.oldLine,11);assert.equal(reviewWrites[1].position.newLine,undefined)
  assert.equal(reviewWrites[2].position.oldLine,12);assert.equal(reviewWrites[2].position.newLine,12)
  assert.ok(reviewWrites.every(write=>write.expectedSha==='head'&&write.position.headSha==='head'))
  console.log('smoke-git-workflow: ok (core routing, revision, separate index, graph, draft)')
}
workbenchSmoke().catch(error=>{console.error(error);process.exitCode=1})
