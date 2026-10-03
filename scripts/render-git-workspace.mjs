import fs from 'node:fs'
import path from 'node:path'
import http from 'node:http'
import {createRequire} from 'node:module'
import assert from 'node:assert/strict'
const require=createRequire(import.meta.url)
const dependencyRoot=process.env.POINT_NODE_PACKAGES
const {chromium}=require(dependencyRoot?path.join(dependencyRoot,'playwright'): 'playwright')
const root=path.resolve(import.meta.dirname,'..')
const output=path.join(root,'build','preview','git-workspace')
fs.mkdirSync(output,{recursive:true})
const fixture=`
window.errors=[]
window.addEventListener('error',e=>window.errors.push(e.message))
window.acquireVsCodeApi=()=>({
 getState:()=>undefined,setState:s=>{window.saved=s},postMessage:m=>{
  const git={root:'C:/fixture',head:'abcdef123',branch:'feature/payment',revision:'local-view',remote:'origin/feature/payment',ahead:2,behind:1,operation:'',
   branches:['main','feature/payment','v1.0'],
   changes:[{path:'src/payment.go',area:'staged',code:'M'},{path:'src/payment.go',area:'working',code:'M'},
    {path:'src/new.go',area:'untracked',code:'?'},{path:'src/remove.go',area:'working',code:'D'}],remotes:[{name:'origin',url:'https://gitlab.test/group/project.git'}]}
  const connection={id:'work',name:'GitLab · Анна',provider:'gitlab',url:'https://gitlab.test',enabled:true}
  const review={iid:17,projectPath:'group/project',projectId:1,title:'Исправить расчёт платежа',description:'Сохраняем подготовленные изменения пользователя.\\nПроверки: тесты Git и API.',sourceBranch:'feature/payment',targetBranch:'main',sha:'head-seen',diffRefs:{baseSha:'base',headSha:'head-seen',startSha:'start'},mergeStatus:'mergeable',author:{name:'Анна'},reviewers:[{id:2,name:'Илья'}]}
  let value
  if(m.type==='ready') return
  if(m.type!=='gitWorkspaceAction')return
  if(m.op==='inventory')value={workspaceId:'test-world',repositories:[{root:git.root,name:'payment-api',branch:git.branch,changes:4},{root:'C:/fixture/ui',name:'payment-ui',branch:'main',changes:1}]}
  else if(m.op==='connections')value=[connection]
  else if(m.op==='status')value={git}
  else if(m.op==='bindings')value={candidates:[{connectionId:'work',remote:'origin',project:'group/project'}]}
  else if(m.op==='read')value=[
    {hash:'abcdef123',message:'Исправить расчёт платежа',author:'Анна',date:'2026-10-03',parents:['left','right'],refs:'HEAD → feature/payment'},
    {hash:'left',message:'Добавить проверку index',author:'Илья',date:'2026-10-02',parents:['base'],refs:''},
    {hash:'right',message:'Обновить контракт MR',author:'Анна',date:'2026-10-02',parents:['base'],refs:''},
    {hash:'base',message:'Первый коммит',author:'Анна',date:'2026-10-01',parents:[],refs:'main'},
   ]
  else if(m.op==='forge'){
    const q=m.input
    if(q.action==='reviews')value={data:[review]}
    if(q.action==='review')value={data:review}
    if(q.action==='status')value={capabilities:{merge:{available:true},approve:{available:true}}}
    if(q.action==='changes')value={data:[{oldPath:'src/payment.go',newPath:'src/payment.go',diff:'@@ -1 +1 @@\\n-old\\n+new'}]}
    if(q.action==='discussions')value={data:[{id:'thread',resolved:false,resolvable:true,notes:[{author:{name:'Илья'},body:'Стоит сохранить пользовательский index.',position:{newPath:'src/payment.go',newLine:1}}]}]}
    if(q.action==='pipelines')value={data:[{id:42,status:'failed',sha:'head-seen',ref:'feature/payment'}]}
    if(q.action==='jobs')value={data:[{id:81,name:'go test',stage:'test',status:'failed',failureReason:'script_failure'},{id:82,name:'UI contracts',stage:'test',status:'success'}]}
    if(q.action==='comment'||q.action==='reply'||q.action==='resolve')value={data:{done:true}}
  } else if(m.op==='suggest')value={revision:'local-view',purpose:m.input.purpose,text:'fix: сохранить index при коммите задачи'}
  else value={opened:true}
  queueMicrotask(()=>window.dispatchEvent(new MessageEvent('message',{data:{type:'gitWorkspaceResult',id:m.id,ok:true,data:value}})))
 }
})
`
const html=`<!doctype html><html lang="ru"><head><meta charset="utf-8"><link rel="stylesheet" href="/style.css"></head><body data-layout="tool-git-workspace" data-git-screen="changes"><div id="root"></div><script>${fixture}</script><script src="/main.js"></script></body></html>`
const server=http.createServer((request,response)=>{
 const route=request.url.split('?')[0]
 const files={'/style.css':'vscode-extension/media/style.css','/main.js':'vscode-extension/media/main.js'}
 if(route==='/'){response.setHeader('Content-Type','text/html;charset=utf-8');response.end(html);return}
 if(files[route]){response.setHeader('Content-Type',route.endsWith('.css')?'text/css':'text/javascript');response.end(fs.readFileSync(path.join(root,files[route])));return}
 response.writeHead(404);response.end()
})
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve))
let browser
try {
 const exists=fs.existsSync(chromium.executablePath())
 browser=await chromium.launch({headless:true,...(exists?{}:{channel:'msedge'})})
 const page=await browser.newPage({viewport:{width:1280,height:860}})
 const errors=[];page.on('pageerror',e=>errors.push(e.message))
 await page.goto('http://127.0.0.1:'+server.address().port)
 await page.getByText('Подготовленные',{exact:true}).waitFor()
 await page.locator('#gw-commit').fill('Черновик переживает обновление')
 await page.getByRole('button',{name:'Обновить',exact:true}).click()
 await assert.equal(await page.locator('#gw-commit').inputValue(),'Черновик переживает обновление')
 await page.locator('#gw-commit').focus();await page.keyboard.press('End')
 // A background state message is an actual production re-render, not fixture HTML replacement.
 await page.evaluate(()=>window.dispatchEvent(new MessageEvent('message',{data:{type:'gitWorkspaceChanged'}})))
 await page.waitForTimeout(150)
 assert.equal(await page.evaluate(()=>document.activeElement.id),'gw-commit')
 await page.screenshot({path:path.join(output,'changes-wide.png'),fullPage:true})
 await page.setViewportSize({width:340,height:860})
 await page.screenshot({path:path.join(output,'changes-narrow.png'),fullPage:true})
 assert.ok(await page.evaluate(()=>document.querySelector('.git-workspace').scrollWidth<=document.querySelector('.git-workspace').clientWidth),'narrow workspace horizontal overflow')
 await page.setViewportSize({width:1280,height:860})
 await page.getByRole('button',{name:'История',exact:true}).click()
 await page.locator('.gw-history svg').first().waitFor()
 await page.screenshot({path:path.join(output,'history.png'),fullPage:true})
 await page.getByRole('button',{name:'Ревью',exact:true}).click()
 await page.getByRole('button',{name:'!17 Исправить расчёт платежа',exact:true}).click()
 await page.getByRole('button',{name:'src/payment.go',exact:true}).waitFor()
 await page.screenshot({path:path.join(output,'review.png'),fullPage:true})
 await page.getByRole('button',{name:'Обсуждения',exact:true}).click()
 await page.locator('#gw-comment').fill('Комментарий')
 await page.getByRole('button',{name:'Обновить',exact:true}).click()
 assert.equal(await page.locator('#gw-comment').inputValue(),'Комментарий')
 await page.screenshot({path:path.join(output,'discussion.png'),fullPage:true})
 await page.getByRole('button',{name:'Проверки',exact:true}).click()
 await page.getByRole('button',{name:'Pipeline #42',exact:true}).click()
 await page.getByRole('button',{name:'go test',exact:true}).waitFor()
 await page.screenshot({path:path.join(output,'checks.png'),fullPage:true})
 assert.deepEqual(errors,[])
 console.log(JSON.stringify({gitWorkspaceBrowser:'ok',widths:[340,1280],draft:true,focus:true,history:true,review:true,discussions:true,checks:true,output}))
}finally{await browser?.close();await new Promise(resolve=>server.close(resolve))}
