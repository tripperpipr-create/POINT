import type { FileContent, WorkspaceView } from './types'

export interface EditorState {
  file?: FileContent
  draft: string
  saved: string
  busy: boolean
  saving: boolean
}
export const emptyEditorState: EditorState = { draft: '', saved: '', busy: false, saving: false }
interface FileAPI {
  readFile: (path: string) => Promise<FileContent>
  saveFile: (path: string, content: string) => Promise<FileContent>
}

// Serialize document/workspace changes while allowing typing during a save.
// Keeping the current draft here also avoids stale React closures on shortcuts.
export class EditorSession {
  state: EditorState = { ...emptyEditorState }
  constructor(private api: FileAPI, private changed: (state: EditorState) => void, private confirmDiscard: () => boolean) {}
  get dirty() { return Boolean(this.state.file && this.state.draft !== this.state.saved) }
  private update(patch: Partial<EditorState>) {
    this.state = { ...this.state, ...patch }
    this.changed(this.state)
  }
  setDraft(value: string) { if (!this.state.busy) this.update({ draft: value }) }
  private beginNavigation() {
    if (this.state.busy || this.state.saving) return false
    if (this.dirty && !this.confirmDiscard()) return false
    this.update({ busy: true })
    return true
  }
  async openFile(path: string) {
    if (!this.beginNavigation()) return
    try {
      const file = await this.api.readFile(path)
      this.update({ file, draft: file.content, saved: file.content })
    } finally { this.update({ busy: false }) }
  }
  async changeWorkspace(open: () => Promise<WorkspaceView>) {
    if (!this.beginNavigation()) return
    try {
      const view = await open()
      this.update({ file: undefined, draft: '', saved: '' })
      return view
    } finally { this.update({ busy: false }) }
  }
  async save() {
    if (!this.state.file || !this.dirty || this.state.busy || this.state.saving) return
    const { file, draft: submitted } = this.state
    this.update({ saving: true })
    try {
      const saved = await this.api.saveFile(file.path, submitted)
      this.update({ file: saved, saved: saved.content,
        draft: this.state.draft === submitted ? saved.content : this.state.draft })
    } finally { this.update({ saving: false }) }
  }
}
