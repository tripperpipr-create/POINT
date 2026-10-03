import { countOf } from './format-units.js'
const escape=value=>String(value??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))
const button=(action,label,attrs='')=>`<button id="gw-button-${escape(action+'-'+label+'-'+attrs)}" type="button" data-action="gw-${action}" ${attrs}>${escape(label)}</button>`
const data=(key,value)=>`data-${key}="${escape(value)}"`
export function graphRows(commits) {
  const lanes=[]
  return commits.map(commit=>{
    let lane=lanes.indexOf(commit.hash)
    if(lane<0){lane=lanes.length;lanes.push(commit.hash)}
    const before=[...lanes],parents=commit.parents||[]
    lanes.splice(lane,1)
    if(parents.length)lanes.splice(lane,0,...parents.filter(p=>!lanes.includes(p)))
    const edges=[]
    before.forEach((hash,i)=>{
      if(i!==lane&&lanes.includes(hash))edges.push([i,lanes.indexOf(hash)])
    })
    parents.forEach(hash=>edges.push([lane,lanes.indexOf(hash)]))
    const width=Math.max(before.length,lanes.length,1)*14+10
    const paths=edges.map(([from,to])=>`<path d="M${from*14+10} 10 L${to*14+10} 28"/>`).join('')
    return {...commit,graph:`<svg width="${width}" height="28" role="img" aria-label="${parents.length>1?'Слияние, родителей: '+parents.length:'Коммит'}">${paths}<circle cx="${lane*14+10}" cy="10" r="3"/></svg>`}
  })
}
export function createGitWorkspaceUi({root,vscode,render,persist,persisted={}}) {
  let screen=document.body.dataset.gitScreen||persisted.screen||'history'
  let repository=persisted.repository||'',workspaceId='',inventory=[],snapshot,binding
  let connections=[],history=[],reviews=[],detail,changes=[],discussions=[],pipelines=[],jobs=[],capabilities={}
  let workspacePath=''
  let reviewTab='changes',scope='project',notice='',busy=0,visible=true,sequence=0,generation=0,nextPage=0,stale=false
  const pending=new Map(),drafts={...(persisted.drafts||{})}
  const active=()=>[workspaceId,repository].join(':')
  const reviewDrafts=['title','description','target','reviewers','reviewerPeople','draft','comment']
  const fieldKey=name=>reviewDrafts.includes(name)||name.startsWith('reply-')?reviewKey()+':'+name:name
  const draft=()=>Object.fromEntries(Object.entries(drafts[active()]||{}).map(([key,value])=>[key.startsWith(reviewKey()+':')?key.slice(reviewKey().length+1):key,value]))
  const target=()=>({workspaceId,repoRoot:repository})
  let reviewTerm='MR'
  const reviewKey=()=>detail?[detail.connectionId,detail.projectPath,detail.iid].join(':'):'new'
  const text=(name,value='')=>`<input id="gw-${name}" data-gw-field="${name}" value="${escape(draft()[name]??value)}">`
  const textarea=(name,value='')=>`<textarea id="gw-${name}" data-gw-field="${name}" rows="3">${escape(draft()[name]??value)}</textarea>`
  const issue=e=>{notice=e.uncertain?'Результат записи неизвестен. Обновите состояние сервиса перед повтором. '+e.message:e.message;render()}
  const call=(op,input={},t=target())=>new Promise((resolve,reject)=>{
    const id='gw-'+(++sequence)
    const timer=setTimeout(()=>{pending.delete(id);const e=new Error('Ответ не получен. Обновите состояние перед повтором записи.');e.uncertain=['forge','git'].includes(op);reject(e)},130000)
    pending.set(id,{resolve,reject,timer})
    vscode.postMessage({type:'gitWorkspaceAction',op,id,input,target:t})
  })
  const api=(action,input={})=>call('forge',{connectionId:binding?.connectionId,project:binding?.project,
    ...(detail?{connectionId:detail.connectionId,project:detail.projectPath,iid:detail.iid,expectedSha:detail.sha}:{}),
    action,...input})
  async function connectionLoad(){connections=await call('connections')}
  async function selectRepo(value) {
    drafts[active()]={...drafts[active()],screen,scope,reviewTab,selectedReview:detail,scroll:root.querySelector?.('.git-workspace')?.scrollTop||0}
    generation++;repository=value;detail=undefined;binding=undefined;history=[];reviews=[];notice='';stale=false
    screen=draft().screen||'changes';scope=draft().scope||'project';reviewTab=draft().reviewTab||'changes';detail=draft().selectedReview
    persist();await load();render()
  }
  async function load() {
    const epoch=generation
    if(!workspaceId){
      const v=await call('inventory');inventory=v.repositories;workspaceId=v.workspaceId
      if(!inventory.some(r=>r.root===repository))repository=inventory[0]?.root||''
      const saved=draft();screen=document.body.dataset.gitScreen||saved.screen||screen;detail=saved.selectedReview;scope=saved.scope||scope;reviewTab=saved.reviewTab||'changes'
      await connectionLoad()
    }
    if(!repository){render();return}
    const [status,links]=await Promise.all([call('status'),call('bindings')])
    if(epoch!==generation)return
    snapshot=status.git
    if(links.candidates.length===1)binding=links.candidates[0]
    else binding=links.candidates.find(b=>b.connectionId===draft().connectionId&&b.remote===draft().remote)
    if(screen==='history'){
      history=await call('read',{kind:'history',query:{search:draft().search||'',author:draft().author||'',path:draft().path||'',skip:0}})
    } else if(screen==='reviews'&&!detail)await loadReviews()
    else if(screen==='reviews'&&detail)await loadReview(true)
    render()
  }
  async function loadReviews(append=false) {
    const epoch=generation
    let result=[]
    if(scope==='project'){
      if(!binding){reviews=[];return}
      const v=await api('reviews',{iid:0,scope:'all',page:append?nextPage:1})
      nextPage=v.nextPage||0
      result=(v.data||[]).map(r=>({...r,connectionId:binding.connectionId,projectPath:binding.project}))
    }else {
      const outcomes=await Promise.allSettled(connections.filter(c=>c.enabled).map(async c=>{
        let page=1,rows=[]
        do {
          const v=await call('forge',{connectionId:c.id,action:'reviews',scope,page})
          rows.push(...(v.data||[]).map(r=>({...r,connectionId:c.id})));page=v.nextPage||0
        }while(page)
        return rows
      }))
      for(let i=0;i<outcomes.length;i++){
        const v=outcomes[i];if(v.status==='fulfilled')result.push(...v.value)
        else notice=String(v.reason.message||v.reason)
      }
      nextPage=0
    }
    if(epoch!==generation)return
    reviews=append?[...reviews,...result]:result
  }
  async function loadReview(poll=false) {
    const epoch=generation,key=reviewKey(),previous=detail
    const v=await api('review')
    if(epoch!==generation||key!==reviewKey())return
    if(poll&&previous.sha!==v.data.sha){stale=true;notice='Появилась новая ревизия '+reviewTerm+'. Обновите изменения перед действием.';return}
    detail={...v.data,connectionId:previous.connectionId,projectPath:previous.projectPath}
    const rights=await api('status');capabilities=rights.capabilities||{};reviewTerm=rights.data?.term||'MR';detail.term=reviewTerm
    const rows=[];let page=1
    do {
      const response=await api(reviewTab==='checks'?'pipelines':reviewTab==='discussions'?'discussions':'changes',{page})
      rows.push(...(response.data||[]));page=response.nextPage||0
    }while(page)
    if(epoch!==generation||key!==reviewKey())return
    if(reviewTab==='changes')changes=rows
    if(reviewTab==='discussions')discussions=rows
    if(reviewTab==='checks')pipelines=rows
    render()
  }
  const allowed=action=>!stale&&capabilities[action]?.available!==false
  function operationButton(action,label,attrs=''){
    const c=capabilities[action]
    return button('write',label,`${data('write',action)} ${attrs} ${!allowed(action)?'disabled':''} title="${escape(stale?'Обновите ревизию '+reviewTerm:c?.reason||label)}"`)
  }
  function header(){
    return `<header class="gw-header"><strong>Git</strong><select id="gw-repository" aria-label="Репозиторий">${inventory.map(r=>`<option value="${escape(r.root)}" ${r.root===repository?'selected':''}>${escape(r.name)} · ${escape(r.branch||'без ветки')} · ${r.changes} изм.</option>`).join('')}</select><span>${escape(snapshot?.branch||'detached HEAD')}</span><span>↓ ${snapshot?.behind||0} ↑ ${snapshot?.ahead||0}</span>${button('bind',binding?(connections.find(c=>c.id===binding.connectionId)?.name||'GitLab')+' · '+binding.project:'Связать сервис')}${button('refresh','Обновить')}</header>
    <nav class="gw-nav">${[['changes','Изменения'],['history','История'],['reviews','Ревью'],['connections','Подключения']].map(([s,n])=>button('screen',n,`${data('screen',s)} aria-pressed="${s===screen}"`)).join('')}</nav>`
  }
  function localChanges(){
    if(!snapshot)return '<p>Открытый проект не содержит репозиториев. Создайте или клонируйте репозиторий.</p>'+button('setup','Clone',data('setup','clone'))+button('setup','Init',data('setup','init'))
    return `${snapshot.operation?`<p>Незавершённая операция: ${escape(snapshot.operation)} ${button('git','Продолжить',data('git','continue'))} ${button('git','Отменить',data('git','abort'))}</p>`:''}
    <div class="gw-local">${['conflict','staged','working','untracked'].map(area=>`<section><h3>${({conflict:'Конфликты',staged:'Подготовленные',working:'Неподготовленные',untracked:'Новые файлы'})[area]}</h3>${snapshot.changes.filter(c=>c.area===area).map(c=>`<div class="gw-row"><span>${escape(c.code)}</span>${button('localDiff',c.path,`${data('path',c.path)} ${data('area',area)}`)}${area==='conflict'?button('git','Merge-редактор',`${data('git','openMerge')} ${data('path',c.path)}`):button('git',area==='staged'?'− Снять':'+ Подготовить',`${data('git',area==='staged'?'unstage':'stage')} ${data('path',c.path)}`)}${['staged','working'].includes(area)?button('git','Строки…',`${data('git',area==='staged'?'unstageLines':'stageLines')} ${data('path',c.path)}`):''}</div>`).join('')||'<p class="muted">Нет файлов</p>'}</section>`).join('')}</div>
    <section class="gw-compose"><label>Сообщение коммита ${textarea('commit')}</label><label><input id="gw-amend" type="checkbox" ${draft().amend?'checked':''}> Amend</label><p>Коммит включает только содержимое index: ${countOf(snapshot.changes.filter(c=>c.area==='staged').length,'файл','файла','файлов')}.</p>${button('commit','Создать коммит')}${button('git','Push',data('git','push'))}${button('git','Pull',data('git','pull'))}${button('git','Fetch',data('git','fetch'))}${button('assist','Объяснить diff',data('purpose','diff'))}${snapshot.operation?button('assist','Предложить решение конфликтов',data('purpose','conflict')):''}${button('assist','Помочь с сообщением',data('purpose','commit'))}</section>`
  }
  function historyView(){
    return `<div class="gw-filters"><label>Сообщение ${text('search')}</label><label>Автор ${text('author')}</label><label>Путь ${text('path')}</label>${button('refresh','Найти')}<label>База ${text('base','HEAD~1')}</label><label>Ревизия ${text('head','HEAD')}</label>${button('compare','Сравнить')}</div><div class="gw-history" data-keynav aria-label="История коммитов">${graphRows(history).map(c=>`<div class="gw-row">${c.graph}${button('commitDetail',c.hash.slice(0,8),data('hash',c.hash))}<span class="gw-subject">${escape(c.message||c.subject)}</span><span class="muted">${escape(c.author)} ${escape(c.date)}</span><span>${escape(c.refs||'')}</span></div>`).join('')}</div>${history.length>=100?button('moreHistory','Ещё коммиты'):''}<details><summary>Ветки, теги и stash</summary><div class="gw-refs">${(snapshot?.localBranches||snapshot?.branches||[]).map(ref=>button('git',ref,`${data('git','switchBranch')} ${data('ref',ref)}`)).join('')}${(snapshot?.remoteBranches||[]).map(ref=>button('git',ref,data('git','switchBranch')+' '+data('ref',ref))).join('')}${(snapshot?.tags||[]).map(ref=>button('git','Тег '+ref,data('git','switchBranch')+' '+data('ref','refs/tags/'+ref))).join('')}</div>${['createBranch','renameBranch','deleteBranch','createTag','deleteTag','merge','rebase','cherry-pick','revert','stashPush','stashApply','stashDrop','addRemote','removeRemote','setUpstream'].map(a=>button('git',({createBranch:'Новая ветка',renameBranch:'Переименовать ветку',deleteBranch:'Удалить ветку',createTag:'Создать тег',deleteTag:'Удалить тег',stashPush:'Сохранить stash',stashApply:'Применить stash',stashDrop:'Удалить stash',addRemote:'Добавить remote',removeRemote:'Удалить remote',setUpstream:'Upstream'})[a]||a,data('git',a))).join('')}${button('stashView','Посмотреть stash')}${button('blame','Blame пути')}</details>`
  }
  function reviewList(){
    return `<div class="gw-filters">${[['project','Этот репозиторий'],['mine','Мои'],['review','На моём ревью']].map(([s,n])=>button('scope',n,`${data('scope',s)} aria-pressed="${scope===s}"`)).join('')}${binding?button('newReview','Создать '+reviewTerm):''}</div>${!binding&&scope==='project'?'<p>Выберите связь репозитория с подключением сервиса. При нескольких аккаунтах выбор требуется явно.</p>':''}${reviews.map((r,i)=>`<div class="gw-row">${button('review','!'+r.iid+' '+r.title,data('index',i))}<span>${escape(connections.find(c=>c.id===r.connectionId)?.name||r.connectionId)}</span><span>${escape(r.sourceBranch)} → ${escape(r.targetBranch)}</span><span>${r.draft?'Черновик':escape(r.mergeStatus)}</span></div>`).join('')||'<p class="muted">'+escape(reviewTerm)+' не найдены</p>'}${nextPage?button('moreReviews','Ещё '+reviewTerm):''}`
  }
  function mrCompose(){
    return `<section class="gw-compose"><label>Заголовок ${text('title',detail?.title||snapshot?.branch)}</label><label>Описание ${textarea('description',detail?.description)}</label><label>Целевая ветка ${text('target',detail?.targetBranch||'main')}</label><p>Ревьюеры: ${escape((draft().reviewerPeople||detail?.reviewers||[]).map(u=>u.name).join(', ')||'не назначены')} ${button('reviewers','Выбрать…')}</p><label><input id="gw-draft" type="checkbox" ${(draft().draft??detail?.draft)?'checked':''}> Черновик</label>${button(detail?'update':'create',(detail?'Сохранить ':'Создать ')+reviewTerm)}${button('cancelCompose','Отмена')}${button('assist','Помочь с описанием',data('purpose','description'))}</section>`
  }
  let proposal
  let composing=false
  function reviewDetail(){
    if(composing)return mrCompose()
    const blockers=[detail.draft?'Черновик':'',detail.hasConflicts?'Есть конфликты':'',detail.blockingThreads?'Остались обсуждения':'',detail.mergeError||'',detail.mergeStatus&&detail.mergeStatus!=='mergeable'?'Статус: '+detail.mergeStatus:''].filter(Boolean)
    return `<section><div class="gw-filters">${button('back','← Очередь')}<strong>!${detail.iid} ${escape(detail.title)}</strong>${button('edit','Редактировать')}${operationButton('approve','Одобрить')}${operationButton('unapprove','Снять одобрение')}${operationButton('merge','Слить '+reviewTerm)}</div><p>${escape(detail.sourceBranch)} → ${escape(detail.targetBranch)} · ${escape(detail.sha?.slice(0,8))} · Автор: ${escape(detail.author?.name)} · Ревьюеры: ${escape(detail.reviewers?.map(u=>u.name).join(', ')||'не назначены')}</p><p>${escape(blockers.join(' · ')||'Готов к слиянию')}</p><p class="gw-description">${escape(detail.description)}</p><nav class="gw-nav">${[['changes','Изменения'],['discussions','Обсуждения'],['checks','Проверки']].map(([t,n])=>button('reviewTab',n,`${data('tab',t)} aria-pressed="${t===reviewTab}"`)).join('')}</nav>${reviewTab==='changes'?changes.map((f,i)=>`<div class="gw-row">${button('diff',f.newPath,data('index',i))}<span>${f.renamed?'Переименован':f.new?'Добавлен':f.deleted?'Удалён':'Изменён'}</span>${f.trimmed?'<span>Diff сокращён</span>':''}</div>`).join(''):reviewTab==='discussions'?discussionView():checksView()}</section>`
  }
  function discussionView(){
    return `${discussions.map((d,i)=>`<article class="gw-discussion"><p>${d.resolved?'✓ Разрешено':'Обсуждение'} ${escape(d.notes?.find(n=>n.position)?.position?.newPath||'')}</p>${d.notes?.map(n=>`<p><strong>${escape(n.author?.name)}</strong> ${escape(n.body)}</p>`).join('')}<label>Ответ ${textarea('reply-'+d.id)}</label>${operationButton('reply','Ответить',data('index',i))}${d.resolvable?operationButton('resolve',d.resolved?'Открыть нить':'Разрешить нить',data('index',i)):''}</article>`).join('')}<label>Общий комментарий ${textarea('comment')}</label>${operationButton('comment','Опубликовать')}`
  }
  function checksView(){
    return `${pipelines.map((p,i)=>`<div class="gw-row">${button('jobs','Pipeline #'+p.id,data('index',i))}<span>${escape(p.status)} · ${escape(p.sha?.slice(0,8))}</span>${operationButton('retryPipeline','Повторить',data('pipeline',p.id))}</div>`).join('')}${jobs.map(j=>`<div class="gw-row"><span>${escape(j.stage)}</span>${button('logs',j.name,data('job',j.id))}<span>${escape(j.status)} ${escape(j.failureReason||'')}</span>${operationButton('retryJob','Повторить',data('job',j.id))}</div>`).join('')}${button('assist','Объяснить CI',data('purpose','ci'))}`
  }
  function view(){
    const content=!repository?localChanges():screen==='changes'?localChanges():screen==='history'?historyView():screen==='connections'?`${button('connection','Добавить GitLab')}${connections.map((c,i)=>`<div class="gw-row"><strong>${escape(c.name)}</strong><span>${escape(c.url)}</span><span>${c.enabled?'Включено':'Отключено'}</span>${button('connection','Изменить',data('index',i))}${button('toggle','Вкл./выкл.',data('index',i))}</div>`).join('')}`:detail?reviewDetail():composing?mrCompose():reviewList()
    return `<main class="git-workspace" data-gw-repo="${escape(active())}" data-gw-scroll="${draft().scroll||0}">${header()}${notice?`<p role="status" class="gw-notice">${escape(notice)}</p>`:''}${busy?'<p role="status">Выполняется…</p>':''}${proposal?`<section class="gw-compose"><h3>Предложение Point · ${escape(proposal.revision.slice(0,8))}</h3><pre>${escape(proposal.text)}</pre>${proposal.patch?`<pre>${escape(proposal.patch)}</pre>`:''}${['commit','description','conflict'].includes(proposal.purpose)?button('applyProposal','Применить предложение'):''}${button('dismissProposal','Закрыть')}</section>`:''}${content}</main>`
  }
  async function click(action,node) {
    if(!action.startsWith('gw-'))return false
    if(busy)return true
    busy++;notice='';render()
    try {
      const op=action.slice(3),d=node.dataset
      if(op==='screen'){screen=d.screen;composing=false;persist();await load()}
      else if(op==='refresh'){stale=false;if(detail)await loadReview(false);else await load()}
      else if(op==='bind'){await call('bind');await load()}
      else if(op==='connection'){await call('connection',connections[+d.index]||{});await connectionLoad()}
      else if(op==='toggle'){await call('toggleConnection',connections[+d.index]);await connectionLoad()}
      else if(op==='scope'){scope=d.scope;detail=undefined;await loadReviews()}
      else if(op==='review'){detail=reviews[+d.index];reviewTab='changes';await loadReview()}
      else if(op==='back'){detail=undefined;await loadReviews()}
      else if(op==='reviewTab'){reviewTab=d.tab;jobs=[];await loadReview()}
      else if(op==='diff'){await call('diff',{connectionId:detail.connectionId,project:detail.projectPath,iid:detail.iid,review:detail,file:changes[+d.index]})}
      else if(op==='localDiff'){await call('git',{action:d.area==='conflict'?'openMerge':'openChange',path:d.path,area:d.area,revision:snapshot.revision})}
      else if(op==='git'){await call('git',{action:d.git,path:d.path,ref:d.ref,revision:snapshot.revision});await load()}
      else if(op==='commit'){await call('git',{action:'commit',message:draft().commit||'',amend:!!draft().amend,revision:snapshot.revision});if(drafts[active()])delete drafts[active()].commit;await load()}
      else if(op==='compare'){await call('localDocument',{kind:'compare',base:draft().base||'HEAD~1',head:draft().head||'HEAD',path:draft().path||''})}
      else if(op==='commitDetail'){await call('localDocument',{kind:'compare',base:history.find(c=>c.hash===d.hash)?.parents?.[0]||'4b825dc642cb6eb9a060e54bf8d69288fbee4904',head:d.hash})}
      else if(op==='blame'){await call('localDocument',{kind:'blame',path:draft().path||''})}
      else if(op==='stashView'){await call('localDocument',{kind:'stash',head:draft().head||'stash@{0}'})}
      else if(op==='moreHistory'){history.push(...await call('read',{kind:'history',query:{search:draft().search||'',author:draft().author||'',path:draft().path||'',skip:history.length}}))}
      else if(op==='moreReviews'){await loadReviews(true)}
      else if(op==='newReview'||op==='edit'){composing=true}
      else if(op==='cancelCompose'){composing=false}
      else if(op==='reviewers'){
        const people=await call('reviewers',{connectionId:detail?.connectionId||binding.connectionId,project:detail?.projectPath||binding.project})
        drafts[active()]={...drafts[active()],[fieldKey('reviewerPeople')]:people,[fieldKey('reviewers')]:people.map(u=>u.id).join(',')}
      }else if(op==='create'||op==='update'){
        if(op==='update'&&stale)throw new Error('Обновите ревизию '+reviewTerm)
        const v=await api(op,{title:draft().title??detail?.title??snapshot.branch,description:draft().description??detail?.description??'',
          sourceBranch:snapshot.branch,targetBranch:draft().target??detail?.targetBranch??'main',draft:!!(draft().draft??detail?.draft),
          reviewerIds:String(draft().reviewers??(detail?.reviewers||[]).map(u=>u.id).join(',')).split(',').filter(Boolean).map(Number)})
        detail={...v.data,connectionId:detail?.connectionId||binding.connectionId,projectPath:detail?.projectPath||binding.project}
        composing=false;stale=false;await loadReview()
      }else if(op==='write'){
        if(!allowed(d.write))throw new Error('Действие недоступно: обновите '+reviewTerm+' или проверьте права')
        const input={jobId:+d.job||0,pipelineId:+d.pipeline||0}
        if(d.write==='reply'||d.write==='resolve'){
          const row=discussions[+d.index];input.discussionId=row.id;input.resolved=!row.resolved;input.body=draft()['reply-'+row.id]||''
        }else if(d.write==='comment')input.body=draft().comment||''
        await api(d.write,input)
        if(d.write==='comment')delete drafts[active()][fieldKey('comment')]
        if(d.write==='reply')delete drafts[active()][fieldKey('reply-'+input.discussionId)]
        await loadReview()
      }else if(op==='jobs'){jobs=(await api('jobs',{pipelineId:pipelines[+d.index].id})).data||[]}
      else if(op==='logs'){await call('logs',{connectionId:detail.connectionId,project:detail.projectPath,jobId:+d.job})}
      else if(op==='dismissProposal'){proposal=undefined}
      else if(op==='applyProposal'){
        if(!proposal)throw new Error('Нет предложения')
        const revision=detail?.sha||snapshot?.revision
        if(revision!==proposal.revision||stale)throw new Error('Исходная ревизия изменилась. Запросите новое предложение.')
        if(proposal.purpose==='conflict'){
          if(!proposal.patch)throw new Error('Point предложил только объяснение')
          await call('git',{action:'applyPatch',patch:proposal.patch,revision:proposal.revision})
          await load()
        }else {
          drafts[active()]={...drafts[active()],[fieldKey(proposal.purpose==='commit'?'commit':'description')]:proposal.text}
        }
        proposal=undefined
      }
      else if(op==='assist'){
        let ciLogs=''
        if(d.purpose==='ci'&&detail){
          if(!jobs.length&&pipelines.length)jobs=(await api('jobs',{pipelineId:pipelines[0].id})).data||[]
          const failed=jobs.find(job=>job.status==='failed')
          if(failed)ciLogs=(await api('logs',{jobId:failed.id})).data?.text?.slice(-24000)||''
        }
        const source=d.purpose==='commit'?await call('read',{kind:'diff',area:'staged'}):d.purpose==='ci'?JSON.stringify({pipelines,jobs,logs:ciLogs}):detail?JSON.stringify({detail,changes}):''
        proposal=await call('suggest',{purpose:d.purpose,forge:detail?{connectionId:detail.connectionId,project:detail.projectPath,iid:detail.iid}:undefined,revision:detail?.sha||snapshot?.revision,text:source,prompt:({
          commit:'Предложи сообщение коммита по index. Покажи предложение; ничего не применяй и не публикуй.',
          description:'Предложи описание '+reviewTerm+'. Покажи предложение перед применением.',
          ci:'Объясни результат CI и предложи действия. Не запускай повтор без пользователя.',
        })[d.purpose]})
      }else if(op==='setup'){const v=await call('setup',{action:d.setup});inventory=v.repositories;repository=inventory[0]?.root||'';await load()}
      else if(op==='native'){d.command==='git.init'?await call('native',{command:d.command}):vscode.postMessage({type:'toolCommand',command:d.command})}
    }catch(e){issue(e)}
    finally{busy--;persist();render()}
    return true
  }
  function input(event){
    const el=event.target
    if(el.id==='gw-repository'){void selectRepo(el.value).catch(issue);return true}
    const name=el.dataset.gwField||({'gw-amend':'amend','gw-draft':'draft'})[el.id]
    if(!name)return false
    drafts[active()]={...draft(),[fieldKey(name)]:el.type==='checkbox'?el.checked:el.value};persist();return true
  }
  function receive(message) {
    if(message.type==='state' && message.workspacePath){
      if(workspacePath && workspacePath!==message.workspacePath){workspaceId='';repository='';snapshot=undefined;detail=undefined;generation++;void load().catch(issue)}
      workspacePath=message.workspacePath
    }
    if(message.type==='gitWorkspaceResult'){
      const p=pending.get(message.id);if(!p)return true
      clearTimeout(p.timer);pending.delete(message.id)
      if(message.ok)p.resolve(message.data)
      else {const e=new Error(message.error);e.uncertain=message.uncertain;p.reject(e)}
      return true
    }
    if(message.type==='gitWorkspaceChanged'){if(!busy)void load().catch(issue);return true}
    if(message.type==='gitWorkspaceScreen'){screen=message.screen;void load().catch(issue);return true}
    if(message.type==='gitWorkspaceVisibility'){visible=message.visible;return true}
    return false
  }
  let elapsed=0
  const timer=setInterval(()=>{
    elapsed+=15
    if(!visible||document.hidden||busy||screen!=='reviews')return
    if(detail&&elapsed%30===0)void loadReview(true).catch(issue)
    else if(!detail&&elapsed%60===0)void loadReviews().then(render).catch(issue)
  },15000)
  window.addEventListener('unload',()=>clearInterval(timer))
  return {view,click,input,receive,start:()=>void load().catch(issue),
    snapshot:()=>{drafts[active()]={...drafts[active()],screen,scope,reviewTab,selectedReview:detail,scroll:root.querySelector?.('.git-workspace')?.dataset.gwRepo===active()?root.querySelector('.git-workspace').scrollTop:(draft().scroll||0)};return {screen,repository,drafts}}}
}
