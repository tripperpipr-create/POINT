import Editor, { DiffEditor, loader, type Monaco, type OnMount } from '@monaco-editor/react'
import * as monaco from 'monaco-editor/esm/vs/editor/editor.api'
import EditorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker'
import 'monaco-editor/esm/vs/basic-languages/go/go.contribution'
import 'monaco-editor/esm/vs/basic-languages/markdown/markdown.contribution'
import 'monaco-editor/esm/vs/basic-languages/python/python.contribution'
import 'monaco-editor/esm/vs/basic-languages/rust/rust.contribution'
import 'monaco-editor/esm/vs/basic-languages/shell/shell.contribution'
import 'monaco-editor/esm/vs/basic-languages/sql/sql.contribution'
import 'monaco-editor/esm/vs/basic-languages/yaml/yaml.contribution'
import 'monaco-editor/esm/vs/basic-languages/typescript/typescript.contribution'
import 'monaco-editor/esm/vs/basic-languages/css/css.contribution'
import 'monaco-editor/esm/vs/basic-languages/html/html.contribution'
import { useEffect, useRef } from 'react'

self.MonacoEnvironment = { getWorker: () => new EditorWorker() }
loader.config({ monaco })

function theme(instance: Monaco) {
  instance.editor.defineTheme('workbench-dark', {base:'vs-dark',inherit:true,rules:[],colors:{'editor.background':'#0d111b','diffEditor.insertedTextBackground':'#123f3480','diffEditor.removedTextBackground':'#52263380','editorLineNumber.foreground':'#475569'}})
}

export function CodeViewer({path,language,value}:{path:string,language:string,value:string}) {
  return <Editor height="100%" path={path} language={language} value={value} theme="workbench-dark" beforeMount={theme} options={{readOnly:true,minimap:{enabled:false},fontSize:13,lineHeight:21,scrollBeyondLastLine:false,automaticLayout:true}}/>
}

export function CodeEditor({path,language,value,onChange,onSave,readOnly=false}:{path:string,language:string,value:string,onChange:(value:string)=>void,onSave:()=>void,readOnly?:boolean}) {
  const saveRef = useRef(onSave)
  useEffect(()=>{ saveRef.current = onSave },[onSave])
  const mount: OnMount = (editor, instance) => {
    editor.addCommand(instance.KeyMod.CtrlCmd | instance.KeyCode.KeyS, () => saveRef.current())
    editor.focus()
  }
  return <Editor height="100%" path={`ide:${path}`} language={language} value={value} theme="workbench-dark" beforeMount={theme} onMount={mount} onChange={next=>onChange(next??'')} options={{readOnly,minimap:{enabled:true,scale:1},fontSize:14,lineHeight:22,fontLigatures:true,smoothScrolling:true,cursorSmoothCaretAnimation:'on',scrollBeyondLastLine:false,automaticLayout:true,wordWrap:'off',tabSize:2,insertSpaces:true,padding:{top:10,bottom:10},renderWhitespace:'selection',bracketPairColorization:{enabled:true}}}/>
}

export function DiffViewer({language,original,modified}:{language:string,original:string,modified:string}) {
  return <DiffEditor height="310px" language={language} original={original} modified={modified} theme="workbench-dark" beforeMount={theme} options={{readOnly:true,minimap:{enabled:false},fontSize:12,lineNumbers:'on',renderSideBySide:true,scrollBeyondLastLine:false,automaticLayout:true,wordWrap:'on'}}/>
}
