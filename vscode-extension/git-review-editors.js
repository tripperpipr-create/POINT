const vscode=require('vscode')
const SCHEME='point-review'
function createReviewEditors(provider,forge,request) {
  const documents=new Map(),contexts=new Map(),threads=new Map()
  const controller=vscode.comments.createCommentController('point.reviews','Point — ревью')
  provider.context.subscriptions.push(controller)
  const uri=(name,spec)=>vscode.Uri.from({scheme:SCHEME,path:'/'+name,query:JSON.stringify(spec)})
  provider.context.subscriptions.push(vscode.workspace.registerTextDocumentContentProvider(SCHEME,{
    provideTextDocumentContent:async u=>{
      if(documents.has(u.toString())) return documents.get(u.toString())
      const spec=JSON.parse(u.query)
      if(spec.empty) return ''
      const file=(await forge({...spec,action:'file'})).data
      if(file.binary||file.tooBig) throw new Error('Содержимое файла недоступно: двоичный файл или размер больше 1 МБ')
      return file.content||''
    },
  }))
  controller.commentingRangeProvider={provideCommentingRanges:doc=>{
    const ctx=contexts.get(doc.uri.toString())
    return {ranges:ctx?ctx.ranges:[],enableFileComments:false}
  }}
  const contextOf=thread=>{
    const ctx=contexts.get(thread.uri.toString())
    if(!ctx) throw new Error('Обновите diff MR перед комментарием')
    return ctx
  }
  provider.context.subscriptions.push(vscode.commands.registerCommand('localAgent.reviewReply',async value=>{
    if(!value?.thread)return
    const thread=value.thread
    try {
      const ctx=contextOf(thread)
      const existing=threads.get(thread)
      const input={...ctx.request,expectedSha:ctx.review.sha,body:value.text}
      if(existing) {input.action='reply';input.discussionId=existing.id}
      else {
        const line=thread.range.start.line+1
        const valid=ctx.lines.get(line)
        if(!valid) throw new Error('Комментарий доступен только в строках diff')
        input.action='comment'
        input.position={...ctx.review.diffRefs,oldPath:ctx.file.oldPath,newPath:ctx.file.newPath,
          ...valid}
      }
      await forge(input)
      if(!existing)thread.dispose()
      await reloadThreads(ctx)
    } catch(e) {void vscode.window.showErrorMessage(e.message);throw e}
  }))
  provider.context.subscriptions.push(vscode.commands.registerCommand('localAgent.reviewResolve',async thread=>{
    if(!thread)return
    const entry=threads.get(thread)
    if(!entry)return
    try {
      const ctx=contextOf(thread)
      await forge({...ctx.request,action:'resolve',discussionId:entry.id,resolved:!entry.resolved,expectedSha:ctx.review.sha})
      await reloadThreads(ctx)
    }catch(e){void vscode.window.showErrorMessage(e.message)}
  }))
  controller.options={prompt:'Ответить в GitLab',placeHolder:'Комментарий будет опубликован после отправки'}
  async function reloadThreads(ctx) {
    for(const [thread] of threads) {
      const previous=contexts.get(thread.uri.toString())
      if(previous?.key===ctx.key){threads.delete(thread);thread.dispose()}
    }
    let page=1
    do {
      const response=await forge({...ctx.request,action:'discussions',page})
      for(const entry of response.data||[]) {
        const position=entry.notes?.find(n=>n.position)?.position
        if(!position) continue
        const side=position.newLine?'new':'old',line=position.newLine||position.oldLine
        const target=[...contexts.entries()].find(([,c])=>c.key===ctx.key&&c.side===side&&
          (side==='new'?c.file.newPath:c.file.oldPath)===(side==='new'?position.newPath:position.oldPath))
        if(!target) continue
        const thread=controller.createCommentThread(vscode.Uri.parse(target[0]),
          new vscode.Range(Math.max(0,line-1),0,Math.max(0,line-1),0),(entry.notes||[]).map(n=>({
            body:new vscode.MarkdownString(n.body),mode:vscode.CommentMode.Preview,author:{name:n.author?.name||n.author?.username||'GitLab'},
            contextValue:'pointReviewComment',
          })))
        thread.contextValue=entry.resolvable?'pointReviewResolvable':'pointReviewThread'
        thread.label=entry.resolved?'Нить разрешена':'Обсуждение GitLab'
        thread.state=entry.resolved?vscode.CommentThreadState.Resolved:vscode.CommentThreadState.Unresolved
        threads.set(thread,entry)
      }
      page=response.nextPage||0
    } while(page)
  }
  function diffLines(diff,side) {
    let old=0,next=0,started=false;const lines=new Map()
    for(const row of String(diff||'').split('\n')) {
      const m=/^@@ -(\d+)(?:,\d+)? \+(\d+)/.exec(row)
      if(m){old=+m[1];next=+m[2];started=true;continue}
      if(!started)continue
      if(row.startsWith('+')){if(side==='new')lines.set(next,{newLine:next});next++}
      else if(row.startsWith('-')){if(side==='old')lines.set(old,{oldLine:old});old++}
      else if(row.startsWith(' ')){lines.set(side==='new'?next:old,{oldLine:old,newLine:next});old++;next++}
    }
    return lines
  }
  async function openDiff(input) {
    const {review,file,...spec}=input
    const current=(await forge({...spec,action:'review'})).data
    if(!review?.sha||review.sha!==current.sha)throw new Error('MR изменился: обновите карточку перед открытием diff')
    const old=uri(file.oldPath,{...spec,path:file.oldPath,ref:review.diffRefs.baseSha,empty:!!file.new})
    const next=uri(file.newPath,{...spec,path:file.newPath,ref:review.diffRefs.headSha,empty:!!file.deleted})
    const key=[spec.connectionId,spec.project,spec.iid,review.sha].join(':')
    for(const [u,side] of [[old,'old'],[next,'new']]) {
      const lines=file.trimmed?new Map():diffLines(file.diff,side)
      const ranges=[...lines.keys()].map(n=>new vscode.Range(n-1,0,n-1,0))
      contexts.set(u.toString(),{key,request:spec,review,file,side,lines,ranges})
    }
    await reloadThreads(contexts.get(next.toString()))
    await vscode.commands.executeCommand('vscode.diff',old,next,(review.term||'MR')+' !'+spec.iid+' · '+file.newPath,{preview:true})
    return {opened:true}
  }
  async function localDocument(input) {
    if(input.kind==='compare') {
      const paths=await request('/api/v2/git/read',{...input,kind:'files'})
      const selected=input.path || await vscode.window.showQuickPick(paths.filter(Boolean),{title:'Git — файлы сравнения'})
      if(!selected)return {opened:false}
      const pair=[]
      for(const head of [input.base,input.head]) {
        let content
        try {content=await request('/api/v2/git/read',{...input,kind:'content',path:selected,head})}
        catch(error){throw new Error('Не удалось прочитать '+selected+' на '+head+': '+error.message)}
        const u=uri(selected,{repoRoot:input.repoRoot,workspaceId:input.workspaceId,head,nonce:Date.now()})
        documents.set(u.toString(),content);pair.push(u)
      }
      await vscode.commands.executeCommand('vscode.diff',pair[0],pair[1],selected+' · '+input.base+' ↔ '+input.head,{preview:true})
      return {opened:true}
    }
    const text=await request('/api/v2/git/read',input)
    const u=uri(input.kind+'.diff',{...input,nonce:Date.now()})
    documents.set(u.toString(),typeof text==='string'?text:JSON.stringify(text,null,2))
    await vscode.window.showTextDocument(await vscode.workspace.openTextDocument(u),{preview:true})
    return {opened:true}
  }
  async function logs(input) {
    const log=(await forge({...input,action:'logs'})).data
    const u=uri('job-'+input.jobId+'.log',{...input,nonce:Date.now()})
    documents.set(u.toString(),(log.trimmed?'… начало лога сокращено\n':'')+log.text)
    await vscode.window.showTextDocument(await vscode.workspace.openTextDocument(u),{preview:true})
    return {opened:true}
  }
  return {openDiff,localDocument,logs}
}
module.exports={createReviewEditors}
