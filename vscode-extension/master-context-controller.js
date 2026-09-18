const vscode = require('vscode')
const {execFile} = require('node:child_process')
const {promisify} = require('node:util')
const run = promisify(execFile)

async function readAttachment(uri) {
  if (!vscode.workspace.getWorkspaceFolder(uri)) throw new Error('Выберите файл текущего проекта.')
  const stat=await vscode.workspace.fs.stat(uri)
  if(stat.size>5*1024*1024)throw new Error('Файл больше 5 МБ: '+uri.fsPath)
  const name=vscode.workspace.asRelativePath(uri), ext=name.split('.').pop().toLowerCase()
  const mime={png:'image/png',jpg:'image/jpeg',jpeg:'image/jpeg',webp:'image/webp'}[ext]
  if(!mime && stat.size>48000)throw new Error('Выделите нужный фрагмент большого файла: '+name)
  const data=Buffer.from(await vscode.workspace.fs.readFile(uri))
  if(!mime && data.includes(0))throw new Error('Бинарный файл не поддерживается: '+name)
  return {name,path:name,kind:mime?'image':'file',mime,content:mime?data.toString('base64'):data.toString('utf8'),startLine:1}
}
// Поиск файлов для списка под кареткой.
//
// «@» в поле открывало нативное окно выбора источника, а в нём — второй диалог
// файлов: от «хочу приложить файл» до вложения четыре действия и два окна
// поверх редактора. У эталона это список прямо под кареткой, и искать файлы
// умеет только расширение — вебвью о дереве проекта не знает ничего.
//
// Открытые вкладки идут первыми: чаще всего прикладывают то, что и так перед
// глазами. Из запроса вырезаны служебные знаки шаблона: в findFiles он уезжает
// как есть, и «**» из строки человека превратились бы в обход всего дерева.
const MENTION_LIMIT = 40
const MENTION_EXCLUDE = '**/{.git,node_modules,.cache,build,dist,out,vendor}/**'

async function searchMasterContext(host, message) {
  const query = String(message.query || '').replace(/[*?{}[\]!]/g, '').trim().slice(0, 80)
  const open = []
  for (const group of vscode.window.tabGroups?.all || []) {
    for (const tab of group.tabs || []) {
      const uri = tab.input?.uri
      if (!uri || !vscode.workspace.getWorkspaceFolder(uri)) continue
      const path = vscode.workspace.asRelativePath(uri)
      if (query && !path.toLowerCase().includes(query.toLowerCase())) continue
      if (!open.includes(path)) open.push(path)
    }
  }
  let found = []
  if (query) {
    const uris = await vscode.workspace.findFiles(`**/*${query}*`, MENTION_EXCLUDE, MENTION_LIMIT)
    found = uris.filter(uri => vscode.workspace.getWorkspaceFolder(uri)).map(uri => vscode.workspace.asRelativePath(uri))
  }
  const items = [...open, ...found.filter(path => !open.includes(path))].slice(0, MENTION_LIMIT)
    .map(path => ({ path, open: open.includes(path) }))
  host.post({ type: 'masterContextSuggestions', viewId: message.viewId, conversationId: message.conversationId, query: String(message.query || ''), items })
}

// Приложить файл, выбранный в списке. Читается тем же readAttachment, что и
// выбранный диалогом: предел размера, отказ по бинарному содержимому и разбор
// картинок у обоих путей обязаны быть одни.
async function attachMasterContextPath(host, message) {
  const relative = String(message.path || '')
  if (!relative) throw new Error('Файл не назван.')
  const folders = vscode.workspace.workspaceFolders || []
  if (!folders.length) throw new Error('Откройте проект.')
  // При нескольких корнях asRelativePath дописывает имя папки первым сегментом.
  // Склеив путь с первым корнем вслепую, мы попали бы в чужую папку — или мимо.
  const segments = relative.split('/')
  const named = folders.length > 1 && folders.find(item => item.name === segments[0])
  const folder = named || folders[0]
  const uri = vscode.Uri.joinPath(folder.uri, ...(named ? segments.slice(1) : segments))
  host.post({ type: 'masterContext', viewId: message.viewId, conversationId: message.conversationId, contexts: [await readAttachment(uri)] })
}

async function pickMasterContext(host,message,editor) {
  const selected=await vscode.window.showQuickPick([
    {label:'Выделение или открытый файл',kind:'editor'}, {label:'Файлы проекта',kind:'files'},
    {label:'Папка проекта',kind:'folder'}, {label:'Ошибки и предупреждения',kind:'problems'},
    {label:'Git diff',kind:'diff'}, {label:'Вывод терминала из буфера',kind:'terminal'},
  ],{title:'Добавить контекст мастеру',placeHolder:'Выберите источник'})
  if(!selected)return
  let contexts=[]
  if(selected.kind==='editor'){
    if(!editor)throw new Error('Сначала откройте файл в редакторе.')
    if(!vscode.workspace.getWorkspaceFolder(editor.document.uri))throw new Error('Открытый файл должен относиться к текущему проекту.')
    contexts=[{name:vscode.workspace.asRelativePath(editor.document.uri),path:vscode.workspace.asRelativePath(editor.document.uri),kind:'selection',startLine:editor.selection.isEmpty?1:editor.selection.start.line+1,content:editor.selection.isEmpty?editor.document.getText():editor.document.getText(editor.selection)}]
  }else if(selected.kind==='files'){
    const uris=await vscode.window.showOpenDialog({canSelectMany:true,canSelectFiles:true,canSelectFolders:false,defaultUri:vscode.workspace.workspaceFolders?.[0]?.uri,openLabel:'Добавить в чат'})
    if(!uris)return;for(const uri of uris)contexts.push(await readAttachment(uri))
  }else if(selected.kind==='folder'){
    const uris=await vscode.window.showOpenDialog({canSelectFolders:true,canSelectFiles:false,defaultUri:vscode.workspace.workspaceFolders?.[0]?.uri,openLabel:'Выбрать папку'})
    if(!uris?.length)return
    if(!vscode.workspace.getWorkspaceFolder(uris[0]))throw new Error('Папка должна находиться в текущем проекте.')
    const files=await vscode.workspace.findFiles(new vscode.RelativePattern(uris[0],'**/*'),'**/{.git,node_modules,.cache,build,dist}/**',17)
    if(files.length>16)throw new Error('В папке больше 16 файлов. Выберите отдельные файлы или меньшую папку.')
    for(const uri of files)contexts.push(await readAttachment(uri))
  }else if(selected.kind==='problems'){
    const lines=[]
    for(const [uri,items] of vscode.languages.getDiagnostics())if(vscode.workspace.getWorkspaceFolder(uri))for(const d of items)lines.push(`${vscode.workspace.asRelativePath(uri)}:${d.range.start.line+1}: ${d.message}`)
    contexts=[{name:'Ошибки и предупреждения',kind:'diagnostics',content:lines.join('\n') || 'Диагностик нет.'}]
  }else if(selected.kind==='diff'){
    const cwd=vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;if(!cwd)throw new Error('Откройте проект.')
    const {stdout}=await run('git',['diff','HEAD','--no-ext-diff','--no-textconv'],{cwd,windowsHide:true,maxBuffer:48000})
    contexts=[{name:'Изменения Git относительно HEAD',kind:'diff',content:stdout || 'Изменений отслеживаемых файлов нет.'}]
  }else contexts=[{name:'Вывод терминала из буфера',kind:'terminal',content:await vscode.env.clipboard.readText()}]
  const total=contexts.filter(v=>v.kind!=='image').reduce((n,v)=>n+[...v.content].length,0)
  if(total>12000)throw new Error('Контекст слишком большой. Выберите меньше файлов или выделите нужный код.')
  host.post({type:'masterContext',viewId:message.viewId,conversationId:message.conversationId,contexts})
}
async function previewMasterContext(value) {
  if(value.kind==='image') {
    const panel=vscode.window.createWebviewPanel('point.context','Контекст: '+value.name,vscode.ViewColumn.Beside,{})
    if(!/^image\/(png|jpeg|webp)$/.test(value.mime)||!/^[A-Za-z0-9+/=]+$/.test(value.content))throw new Error('Некорректное изображение')
    panel.webview.html=`<!doctype html><meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:;"><img src="data:${value.mime};base64,${value.content}" alt="Контекст" />`
  }else await vscode.window.showTextDocument(await vscode.workspace.openTextDocument({content:String(value.content || ''),language:value.kind==='diff'?'diff':'plaintext'}),{preview:true,viewColumn:vscode.ViewColumn.Beside})
}
module.exports={pickMasterContext,previewMasterContext,searchMasterContext,attachMasterContextPath}
