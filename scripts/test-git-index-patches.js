const assert=require('node:assert/strict')
const fs=require('node:fs'),os=require('node:os'),path=require('node:path'),cp=require('node:child_process')
const {parsePatch,reversePatch,selectedPatch}=require('../vscode-extension/git-index-patches')
const dir=fs.mkdtempSync(path.join(os.tmpdir(),'point-index-'))
const git=(...args)=>cp.execFileSync('git',['-C',dir,...args],{encoding:'utf8'})
const apply=text=>{fs.writeFileSync(path.join(dir,'patch.tmp'),text);git('apply','--cached','--check','patch.tmp');git('apply','--cached','patch.tmp')}
try {
  git('init');git('config','core.autocrlf','false');git('config','user.email','test@point.local');git('config','user.name','Point')
  const original='one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n'
  fs.writeFileSync(path.join(dir,'строки.txt'),original);git('add','строки.txt');git('commit','-m','base')
  fs.writeFileSync(path.join(dir,'строки.txt'),original.replace('two','TWO').replace('nine','NINE'))
  let parsed=parsePatch(git('diff','--','строки.txt'))
  const keys=parsed.hunks.flatMap((h,hi)=>h.lines.map((l,li)=>l==='+TWO'||l==='-two'?hi+':'+li:'').filter(Boolean))
  apply(selectedPatch(parsed,new Set(keys)))
  assert.equal(git('show',':строки.txt'),original.replace('two','TWO'))
  assert.match(git('diff','--','строки.txt'),/\+NINE/)
  parsed=reversePatch(parsePatch(git('diff','--cached','--','строки.txt')))
  apply(selectedPatch(parsed,new Set(parsed.hunks.flatMap((h,hi)=>h.lines.map((l,li)=>/^[+-]/.test(l)?hi+':'+li:'').filter(Boolean)))))
  assert.equal(git('show',':строки.txt'),original)
  // Select insertions and deletions independently, including zero-line hunk boundaries.
  for(const [from,to] of [['','add\n'],['delete\n',''],['no newline','changed'],['a\nb\n','a\nnew\nb\n']]) {
    fs.writeFileSync(path.join(dir,'edge.txt'),from);git('add','edge.txt');git('commit','-m','edge')
    fs.writeFileSync(path.join(dir,'edge.txt'),to)
    parsed=parsePatch(git('diff','--','edge.txt'))
    const selection=new Set(parsed.hunks.flatMap((h,hi)=>h.lines.map((l,li)=>/^[+-]/.test(l)?hi+':'+li:'').filter(Boolean)))
    apply(selectedPatch(parsed,selection))
    assert.equal(git('show',':edge.txt'),to)
    git('commit','-m','selected')
  }
  fs.writeFileSync(path.join(dir,'new.txt'),'first\nsecond\n')
  git('add','new.txt')
  parsed=reversePatch(parsePatch(git('diff','--cached','--','new.txt')))
  const second=new Set(parsed.hunks.flatMap((h,hi)=>h.lines.map((l,li)=>l==='-second'?hi+':'+li:'').filter(Boolean)))
  apply(selectedPatch(parsed,second))
  assert.equal(git('show',':new.txt'),'first\n','partially unstage a new file')
  console.log(JSON.stringify({gitIndexPatches:'ok',unicode:true,partial:true,reverse:true,zeroLines:true,noNewline:true}))
}finally{fs.rmSync(dir,{recursive:true,force:true})}
