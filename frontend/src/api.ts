import type { AgentProfile, AgentRunPreview, AgentWorkflow, Bootstrap, ContextPreview, CustomTool, CustomToolPreview, Event, FileContent, FileNode, Match, ProviderKind, ProviderProbeResult, Run, RunContextInput, RunDetails, TerminalCommandResult, WorkflowRun, WorkspaceView } from './types'

declare global {
  interface Window {
    go?: { app?: { App?: Record<string, (...args: unknown[]) => Promise<unknown>> } }
    runtime?: { EventsOn?: (name: string, callback: (...args: unknown[]) => void) => () => void }
  }
}

function desktop() { return window.go?.app?.App }
async function invoke<T>(method: string, ...args: unknown[]): Promise<T> {
  const fn = desktop()?.[method]
  if (!fn) throw new Error(`Связь с desktop-приложением недоступна (${method})`)
  return fn(...args) as Promise<T>
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers)
  const token = apiToken()
  if (token && !headers.has('Authorization')) headers.set('Authorization', `Bearer ${token}`)
  const response = await fetch(path, { ...init, headers })
  if (response.status === 204) return undefined as T
  const payload = await response.json().catch(() => ({})) as { error?: { message?: string } }
  if (!response.ok) throw new Error(payload.error?.message ?? `Ошибка HTTP ${response.status}`)
  return payload as T
}
function json(method: string, body?: unknown): RequestInit { return { method, headers: { 'Content-Type':'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) } }
function either<T>(method: string, desktopArgs: unknown[], web: () => Promise<T>) { return desktop() ? invoke<T>(method, ...desktopArgs) : web() }
function apiToken(): string {
  const fromWindow = (window as Window & { __POINT_API_TOKEN__?: string }).__POINT_API_TOKEN__
  const fromEnv = import.meta.env.VITE_POINT_API_TOKEN
  return String(fromWindow || fromEnv || localStorage.getItem('pointApiToken') || '').trim()
}

export const api = {
  canSelectWorkspace: () => Boolean(desktop()?.SelectWorkspace),
  bootstrap: () => either<Bootstrap>('Bootstrap', [], () => request('/api/bootstrap')),
  selectWorkspace: () => invoke<WorkspaceView>('SelectWorkspace'),
  openWorkspace: (path: string) => either<WorkspaceView>('OpenWorkspace', [path], () => request('/api/workspaces/open', json('POST',{path}))),
  workspaceTree: () => either<FileNode[]>('WorkspaceTree', [], () => request('/api/workspaces/tree')),
  readFile: (path: string) => either<FileContent>('ReadFile', [path], () => request(`/api/files?path=${encodeURIComponent(path)}`)),
  saveFile: (path: string, content: string) => either<FileContent>('SaveFile', [path,content], () => request('/api/files',json('PUT',{path,content}))),
  searchText: (query: string) => either<Match[]>('SearchText', [query], () => request(`/api/search?q=${encodeURIComponent(query)}`)),
  runTerminalCommand: (command: string, cwd = '', timeoutSeconds = 120) => either<TerminalCommandResult>('RunTerminalCommand', [{command,cwd,timeoutSeconds}], () => request('/api/terminal',json('POST',{command,cwd,timeoutSeconds}))),
  saveProfile: (profile: AgentProfile) => either<AgentProfile>('SaveProfile', [profile], () => request('/api/profiles',json('POST',profile))),
  deleteProfile: (id: string) => either<void>('DeleteProfile', [id], () => request(`/api/profiles/${encodeURIComponent(id)}`,{method:'DELETE'})),
  saveCustomTool: (tool: CustomTool) => either<CustomTool>('SaveCustomTool', [tool], () => request('/api/custom-tools',json('POST',tool))),
  previewCustomTool: (tool: CustomTool, args: Record<string,unknown>) => either<CustomToolPreview>('PreviewCustomTool', [{tool,arguments:args}], () => request('/api/custom-tools/preview',json('POST',{tool,arguments:args}))),
  deleteCustomTool: (id: string) => either<void>('DeleteCustomTool', [id], () => request(`/api/custom-tools/${encodeURIComponent(id)}`,{method:'DELETE'})),
  probeProvider: (provider: ProviderKind, baseUrl: string, apiKey = '') => either<ProviderProbeResult>('ProbeProvider', [{provider,baseUrl,apiKey}], () => request('/api/providers/probe',json('POST',{provider,baseUrl,apiKey}))),
  previewContext: (contextItems: RunContextInput[]) => either<ContextPreview>('PreviewContext', [contextItems], () => request('/api/context/preview',json('POST',{contextItems}))),
  saveWorkflow: (workflow: AgentWorkflow) => either<AgentWorkflow>('SaveWorkflow', [workflow], () => request('/api/workflows',json('POST',workflow))),
  deleteWorkflow: (id: string) => either<void>('DeleteWorkflow', [id], () => request(`/api/workflows/${encodeURIComponent(id)}`,{method:'DELETE'})),
  startWorkflow: (workflowId: string, task: string, apiKeys: Record<string,string>, contextItems: RunContextInput[] = []) => either<WorkflowRun>('StartWorkflow', [{workflowId,task,apiKeys,contextItems}], () => request('/api/workflow-runs',json('POST',{workflowId,task,apiKeys,contextItems}))),
  cancelWorkflow: (id: string) => either<void>('CancelWorkflowRun', [id], () => request(`/api/workflow-runs/${encodeURIComponent(id)}/cancel`,json('POST',{}))),
  workflowRun: (id: string) => either<WorkflowRun>('WorkflowRunDetails', [id], () => request(`/api/workflow-runs/${encodeURIComponent(id)}`)),
  previewRun: (profileId: string, task: string, contextItems: RunContextInput[] = []) => either<AgentRunPreview>('PreviewAgentRun', [{profileId,task,contextItems}], () => request('/api/runs/preview',json('POST',{profileId,task,contextItems}))),
  startRun: (profileId: string, task: string, apiKey: string, contextItems: RunContextInput[] = [], preflightFingerprint = '') => either<Run>('StartRun', [{profileId,task,apiKey,contextItems,preflightFingerprint}], () => request('/api/runs',json('POST',{profileId,task,apiKey,contextItems,preflightFingerprint}))),
  cancelRun: (runId: string) => either<void>('CancelRun', [runId], () => request(`/api/runs/${encodeURIComponent(runId)}/cancel`,json('POST',{}))),
  resolveApproval: (approvalId: string, allow: boolean) => either<void>('ResolveApproval', [approvalId,allow], () => request(`/api/approvals/${encodeURIComponent(approvalId)}/resolve`,json('POST',{allow}))),
  runDetails: (runId: string) => either<RunDetails>('RunDetails', [runId], () => request(`/api/runs/${encodeURIComponent(runId)}`)),
  runs: () => either<Run[]>('Runs', [], () => request('/api/runs')),
  onEvent(callback: (event: Event) => void) {
    if (desktop()) return window.runtime?.EventsOn?.('workbench:event', event => callback(event as Event)) ?? (() => {})
    const token = apiToken()
    const source = new EventSource(token ? `/api/events?access_token=${encodeURIComponent(token)}` : '/api/events')
    const listener = (message: MessageEvent) => { try { callback(JSON.parse(message.data) as Event) } catch { /* malformed events are ignored */ } }
    source.addEventListener('workbench', listener as EventListener)
    return () => source.close()
  },
}
