import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from 'react'
import { api } from './api'
import { EditorSession, emptyEditorState } from './editor-session'
import type { Bootstrap, FileNode, TerminalCommandResult, Workspace } from './types'

const CodeEditor = lazy(() => import('./components/MonacoEditors').then(module => ({ default: module.CodeEditor })))
const Onboarding = lazy(() => import('./components/Onboarding'))

const languageByExtension: Record<string,string> = {
  css:'css', go:'go', html:'html', htm:'html', js:'javascript', jsx:'javascript', json:'json',
  md:'markdown', py:'python', rs:'rust', sh:'shell', sql:'sql', ts:'typescript', tsx:'typescript',
  yaml:'yaml', yml:'yaml', xml:'xml', toml:'ini', env:'ini',
}

function language(path: string) {
  const name = path.split('/').pop() ?? path
  const extension = name.includes('.') ? name.split('.').pop()!.toLowerCase() : name.toLowerCase()
  return languageByExtension[extension] ?? 'plaintext'
}

function Tree({nodes,active,onOpen,depth=0}:{nodes:FileNode[];active:string;onOpen:(path:string)=>void;depth?:number}) {
  return <>{nodes.map(node => node.isDir ? (
    <div key={node.path}>
      <div className="tree-row" style={{paddingLeft:12+depth*13}}><span className="folder-icon">▾</span><span>{node.name}</span></div>
      <Tree nodes={node.children??[]} active={active} onOpen={onOpen} depth={depth+1}/>
    </div>
  ) : (
    <button key={node.path} className={`tree-row ${active===node.path?'selected':''}`} style={{paddingLeft:12+depth*13}} onClick={()=>onOpen(node.path)}>
      <span className="file-icon">◇</span><span>{node.name}</span>
    </button>
  ))}</>
}

export default function App() {
  const [boot,setBoot] = useState<Bootstrap>()
  const [workspace,setWorkspace] = useState<Workspace>()
  const [tree,setTree] = useState<FileNode[]>([])
  const [editorState, setEditorState] = useState(emptyEditorState)
  const [editor] = useState(() => new EditorSession(api, setEditorState,
    () => window.confirm('Есть несохранённые изменения. Продолжить без сохранения?')))
  const { file, draft } = editorState
  const canSelectWorkspace = api.canSelectWorkspace()
  const navigationDisabled = editorState.busy || editorState.saving
  const [busy,setBusy] = useState(true)
  const [error,setError] = useState('')
  const [command,setCommand] = useState('go version')
  const [terminal,setTerminal] = useState<TerminalCommandResult>()
  const [terminalBusy,setTerminalBusy] = useState(false)
  const [selectedProfileId,setSelectedProfileId] = useState('')
  const [apiKey,setApiKey] = useState('')
  const [showOnboarding,setShowOnboarding] = useState(()=>localStorage.getItem('point.onboarding.v1')!=='done')

  const dirty = Boolean(file && draft !== editorState.saved)

  const refreshTree = useCallback(async () => {
    const nodes = await api.workspaceTree()
    setTree(nodes)
  },[])

  useEffect(()=>{
    void (async()=>{
      try {
        const initial = await api.bootstrap()
        setBoot(initial)
        setWorkspace(initial.currentWorkspace)
        setSelectedProfileId(initial.profiles[0]?.id??'')
        if (initial.currentWorkspace) await refreshTree()
      } catch (cause) { setError(cause instanceof Error?cause.message:String(cause)) }
      finally { setBusy(false) }
    })()
  },[refreshTree])

  const changeWorkspace = async (open: () => ReturnType<typeof api.selectWorkspace>) => {
    try {
      setError('')
      const view = await editor.changeWorkspace(open)
      if (view) { setWorkspace(view.workspace); setTree(view.tree) }
    } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)) }
  }
  const chooseWorkspace = async () => {
    if (canSelectWorkspace) await changeWorkspace(() => api.selectWorkspace())
  }
  const openRecent = async (path: string) => {
    if (canSelectWorkspace) await changeWorkspace(() => api.openWorkspace(path))
  }
  const openFile = async (path: string) => {
    try { setError(''); await editor.openFile(path) }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)) }
  }
  const save = async () => {
    try { setError(''); await editor.save() }
    catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)) }
  }
  useEffect(() => {
    const beforeUnload = (event: BeforeUnloadEvent) => {
      if (editor.dirty || editor.state.saving) { event.preventDefault(); event.returnValue = '' }
    }
    window.addEventListener('beforeunload', beforeUnload)
    return () => window.removeEventListener('beforeunload', beforeUnload)
  }, [editor])

  const runCommand = async () => {
    if (!command.trim()) return
    try {
      setTerminalBusy(true); setError(''); setTerminal(undefined)
      setTerminal(await api.runTerminalCommand(command.trim()))
    } catch (cause) { setError(cause instanceof Error?cause.message:String(cause)) }
    finally { setTerminalBusy(false) }
  }

  const recent = useMemo(()=>boot?.workspaces.filter(item=>item.path!==workspace?.path)??[],[boot,workspace])

  useEffect(()=>{
    const onKeyDown = (event:KeyboardEvent) => {
      if (canSelectWorkspace && event.ctrlKey && event.altKey && event.key.toLowerCase()==='p') {
        event.preventDefault()
        void chooseWorkspace()
      }
    }
    window.addEventListener('keydown',onKeyDown)
    return ()=>window.removeEventListener('keydown',onKeyDown)
  })

  return <div className="app-shell">
    <header className="topbar">
      <div className="brand"><div className="brand-mark"><img src="/point-icon.png" alt=""/></div><div><strong>Point</strong><small>ЛЁГКАЯ СРЕДА РАЗРАБОТКИ</small></div></div>
      <div className="workspace-switcher">
        <button onClick={chooseWorkspace} disabled={!canSelectWorkspace || navigationDisabled}><span className="project-glyph" style={{gridRow:'1 / 3',alignSelf:'center'}}>P.</span><span>{workspace?.name??'Открыть проект'}</span><small>{workspace?.path??'Выберите локальную папку'}</small><kbd>{canSelectWorkspace ? 'Ctrl Alt P' : 'Смонтированный проект'}</kbd></button>
        {canSelectWorkspace&&recent.length>0&&<select disabled={navigationDisabled} aria-label="Недавние проекты" defaultValue="" onChange={event=>{if(event.target.value)void openRecent(event.target.value)}}><option value="">Недавние</option>{recent.map(item=><option key={item.id} value={item.path}>{item.name}</option>)}</select>}
      </div>
      <div className="top-actions"><button className="top-action" onClick={()=>setShowOnboarding(true)}>Начало работы</button><span className="local-badge" title="Диагностический клиент; полный Agent Hub доступен в Code-OSS"><i/>ДИАГНОСТИКА</span></div>
    </header>

    <div className="workbench-grid">
      <nav className="tool-rail" aria-label="Окна инструментов"><button className="active" title="Проект">P</button><button title="Поиск">⌕</button><button title="Git">⑂</button><span/><button title="Агент">AI</button><button title="Терминал" onClick={()=>document.querySelector<HTMLInputElement>('.terminal-form input')?.focus()}>›_</button></nav>
      <aside className="left-panel">
        <div className="panel-heading"><span>ПРОЕКТ</span><button className="icon-button" onClick={()=>void refreshTree()} title="Обновить">↻</button></div>
        {tree.length?<div className="file-tree"><Tree nodes={tree} active={file?.path??''} onOpen={path=>void openFile(path)}/></div>:<div className="empty-state compact"><strong>{busy?'Загрузка…':'Нет открытого проекта'}</strong><p>Выберите папку проекта в верхней панели.</p></div>}
      </aside>

      <main className="center-panel">
        <div className="tabs"><button className="active">{file?.path.split('/').pop()??'Point'}{dirty&&<span>●</span>}</button></div>
        <div className="main-scroll">
          {file?<div className="editor-wrap"><div className="file-bar"><span>{file.path}</span><button className="button primary" disabled={!dirty || navigationDisabled} onClick={()=>void save()}>{dirty?'Сохранить':'Сохранено'}</button></div><Suspense fallback={<div className="empty">Загрузка редактора…</div>}><CodeEditor path={file.path} language={language(file.path)} value={draft} onChange={value => editor.setDraft(value)} readOnly={editorState.busy} onSave={()=>void save()}/></Suspense></div>:
          <div className="point-home"><div className="home-copy"><span className="eyebrow">КОД · ТЕРМИНАЛ · АГЕНТ</span><h1>Сосредоточьтесь<br/>на <em>главной точке.</em></h1><p>Быстрая IDE с профессиональными инструментами и локальным агентом, который запускается только когда нужен.</p><div className="home-actions"><button className="button primary" disabled={!canSelectWorkspace || navigationDisabled} onClick={chooseWorkspace}>{canSelectWorkspace ? (workspace ? 'Сменить проект' : 'Открыть проект') : 'Проект задан при запуске'} <span>→</span></button>{workspace&&<button className="button" onClick={()=>document.querySelector<HTMLInputElement>('.terminal-form input')?.focus()}>Открыть терминал</button>}</div></div><div className="home-project"><span className="eyebrow">ТЕКУЩИЙ КОНТЕКСТ</span><strong>{workspace?.name??'Проект не открыт'}</strong><p>{workspace?.path??'Выберите папку, чтобы открыть дерево файлов и редактор.'}</p><div><span><i/>Локальное ядро</span><span>Ctrl Alt P · проекты</span></div></div></div>}
        </div>
      </main>

      <aside className="right-panel">
        <div className="panel-heading"><span>POINT AGENT</span><span className="agent-state"><i/>готов</span></div>
        <div className="companion-panel">
          <section className="agent-intro"><div className="point-agent-mark"><img src="/point-icon.png" alt=""/></div><span className="eyebrow">ЛОКАЛЬНЫЙ АГЕНТ</span><h3>Что сделаем?</h3><p>Исследуйте код, готовьте изменения и запускайте проверки. Команды и патчи остаются под вашим контролем.</p><div className="agent-prompts"><button>Объяснить архитектуру</button><button>Найти потенциальную ошибку</button><button>Добавить тесты</button></div></section>
          <section><span className="eyebrow">БЫСТРАЯ КОМАНДА</span><div className="terminal-form"><input value={command} onChange={event=>setCommand(event.target.value)} onKeyDown={event=>{if(event.key==='Enter')void runCommand()}}/><button className="button primary" disabled={terminalBusy||!workspace} onClick={()=>void runCommand()}>{terminalBusy?'…':'Запустить'}</button></div>{terminal&&<pre className={terminal.exitCode===0?'terminal-output':'terminal-output failed'}>{terminal.stdout}{terminal.stderr}{`\n[exit ${terminal.exitCode} · ${terminal.durationMs} ms]`}</pre>}</section>
        </div>
      </aside>
    </div>

    <footer className="statusbar"><div><span>Point <b>v{boot?.version??'1.1'}</b></span><span>{workspace?'Граница проекта включена':'Проект не выбран'}</span></div><div><span>Локальный режим</span><span>UTF-8</span></div></footer>
    {showOnboarding&&boot&&boot.profiles.length>0&&<Suspense fallback={<div className="loading-screen">Загрузка настройки…</div>}><Onboarding profiles={boot.profiles} selectedId={selectedProfileId||boot.profiles[0].id} workspace={workspace} apiKey={apiKey} onSelectProfile={setSelectedProfileId} onApiKeyChange={setApiKey} onSaveProfile={async profile=>{const savedProfile=await api.saveProfile(profile);setBoot(value=>value?{...value,profiles:value.profiles.map(item=>item.id===savedProfile.id?savedProfile:item)}:value);return savedProfile}} canSelectWorkspace={canSelectWorkspace} onOpenWorkspace={chooseWorkspace} onFinish={()=>{localStorage.setItem('point.onboarding.v1','done');setShowOnboarding(false)}}/></Suspense>}
    {error&&<div className="toast"><span>!</span><p>{error}</p><button onClick={()=>setError('')}>×</button></div>}
  </div>
}
