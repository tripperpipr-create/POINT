export type ProviderKind = 'ollama' | 'openai-compatible'
export type RunStatus = 'pending' | 'running' | 'waiting_approval' | 'completed' | 'failed' | 'cancelled' | 'interrupted'

export interface AgentProfile {
  id: string; name: string; roleDescription: string; systemPrompt: string; provider: ProviderKind
  baseUrl: string; model: string; allowedTools: string[]; maxSteps: number; maxDurationSeconds: number
  approvalMode: 'safe' | 'always'; createdAt: string; updatedAt: string
}
export interface AgentProfileTemplate {
  id: string; name: string; description: string; roleDescription: string; systemPrompt: string
  allowedTools: string[]; maxSteps: number; maxDurationSeconds: number; approvalMode: 'safe'|'always'
}
export interface ToolCatalogItem {
  name: string; displayName: string; description: string; category: 'read'|'write'|'execute'; risk: string; requiresApproval: boolean
}
export interface ToolDefinition { name: string; description: string; inputSchema: Record<string,unknown> }
export type CustomToolParameterType = 'string'|'integer'|'enum'|'workspace_path'
export interface CustomToolParameter { name: string; displayName: string; description: string; type: CustomToolParameterType; required: boolean; enumValues?: string[]; maxLength?: number }
export interface CustomTool { id: string; kind: 'command'|'process'; displayName: string; description: string; command: string; program?: string; arguments?: string[]; parameters?: CustomToolParameter[]; providesVerification?: boolean; cwd: string; timeoutSeconds: number; createdAt: string; updatedAt: string }
export interface CustomToolTemplate { id: string; name: string; description: string; tool: CustomTool }
export interface CustomToolPreview { definition: ToolDefinition; program: string; arguments: string[]; command: string; parameters: Record<string,unknown>; cwd: string; resolvedCwd: string; reason: string; timeoutSeconds: number }
export interface RunConfigurationSnapshot { schemaVersion: number; applicationVersion: string; capturedAt: string; profile: AgentProfile; customTools: CustomTool[] }
export interface ProviderModel { id: string; displayName: string; ownedBy?: string; size?: number; modifiedAt?: string }
export interface ProviderProbeResult { connected: boolean; provider: ProviderKind; baseUrl: string; models: ProviderModel[]; latencyMs: number }
export interface WorkflowStep { id: string; name: string; profileId: string; instruction: string; includeOriginalContext: boolean; includePreviousResult: boolean }
export interface AgentWorkflow { id: string; name: string; description: string; steps: WorkflowStep[]; createdAt: string; updatedAt: string }
export interface WorkflowSnapshot { schemaVersion: number; applicationVersion: string; capturedAt: string; workflow: AgentWorkflow }
export interface WorkflowStepRun { stepId: string; stepName: string; profileId: string; profileName: string; runId?: string; status: RunStatus; result?: string; error?: string; startedAt?: string; finishedAt?: string }
export interface WorkflowRun { id: string; workflowId: string; workspaceId: string; task: string; contextItems: RunContextItem[]; snapshot: WorkflowSnapshot; status: RunStatus; currentStep: number; stepRuns: WorkflowStepRun[]; error?: string; result?: string; startedAt: string; finishedAt?: string; durationMs: number }
export interface RunContextInput { kind: 'text'|'workspace_file'; label?: string; path?: string; content?: string }
export interface RunContextItem {
  id: string; kind: 'text'|'workspace_file'|'document'|'table'|'image'; label: string; path?: string
  format?: string; mediaType?: string; content: string; digest?: string; size: number; sourceSize?: number
  extractedSize?: number; width?: number; height?: number; truncated?: boolean
}
export interface ContextPreview {
  items: RunContextItem[]; totalSourceBytes: number; totalContextBytes: number; totalImageBytes: number
  estimatedTokens: number; warnings: string[]
}
export interface Workspace { id: string; path: string; name: string; openedAt: string }
export interface AgentToolPreview { definition: ToolDefinition; displayName: string; category: string; risk: string; requiresApproval: boolean; approvalReason?: string }
export interface AgentTokenEstimate { systemPrompt: number; task: number; toolSchemas: number; context: number; total: number }
export interface AgentRunPreview { fingerprint: string; version: string; workspace: Workspace; profile: AgentProfile; systemMessage: string; task: string; tools: AgentToolPreview[]; context: ContextPreview; tokens: AgentTokenEstimate; warnings: string[] }
export interface FileNode { name: string; path: string; isDir: boolean; size?: number; children?: FileNode[] }
export interface FileContent { path: string; content: string; numbered: string; sha256?: string; size: number; truncated: boolean }
export interface Match { path: string; line: number; text: string }
export interface Run {
  id: string; agentId: string; profileId: string; workspaceId: string; task: string; contextItems: RunContextItem[]; configurationSnapshot: RunConfigurationSnapshot; provider: string; model: string
  status: RunStatus; step: number; requestCount: number; toolsUsed: string[]; changedFiles: string[]; error?: string
  result?: string; startedAt: string; finishedAt?: string; durationMs: number
}
export interface Event { id: string; runId: string; agentId: string; type: string; step: number; actor: string; data: unknown; createdAt: string }
export interface Approval { id: string; runId: string; agentId: string; toolName: string; reason: string; arguments: Record<string, unknown>; status: 'pending'|'allowed'|'denied'; createdAt: string; resolvedAt?: string }
export interface PatchProposal { id: string; runId: string; approvalId: string; path: string; originalHash: string; original: string; proposed: string; diff: string; status: string; createdAt: string }
export interface RunDiagnosticSignal { code: string; severity: 'success'|'info'|'warning'|'error'; value?: number }
export interface RunDiagnostics {
  schemaVersion: number; runId: string; health: 'active'|'healthy'|'attention'|'failed'; stopReason: string; durationMs: number
  model: { requests: number; responses: number; pending: number; inputTokens: number; outputTokens: number; totalTokens: number; usageReported: boolean; latencyMs: number; averageLatencyMs: number; firstResponseMs: number }
	retrieval: { searches: number; truncatedSearches: number; candidateChunks: number; returnedChunks: number; relatedFiles: number; usedChars: number }
  tools: { calls: number; succeeded: number; failed: number; pending: number; durationMs: number; items: Array<{ name: string; calls: number; succeeded: number; failed: number; pending: number; durationMs: number }> }
  approvals: { requested: number; allowed: number; denied: number; pending: number; waitMs: number; averageWaitMs: number; maxWaitMs: number }
  patches: { proposed: number; applied: number; rejected: number }
	verification: { required: boolean; recorded: boolean; successfulCommands: number }
	guardrails: { duplicatePlans: number; inspectionRequired: number; inspectionScope: number; inspectionStale: number }
	signals: RunDiagnosticSignal[]
}
export interface Bootstrap { version: string; profiles: AgentProfile[]; profileTemplates: AgentProfileTemplate[]; toolCatalog: ToolCatalogItem[]; customTools: CustomTool[]; customToolTemplates: CustomToolTemplate[]; workspaces: Workspace[]; runs: Run[]; runDiagnostics: RunDiagnostics[]; workflows: AgentWorkflow[]; workflowRuns: WorkflowRun[]; currentWorkspace?: Workspace }
export interface WorkspaceView { workspace: Workspace; tree: FileNode[] }
export interface RunDetails { run: Run; events: Event[]; approvals: Approval[]; patches: PatchProposal[]; diagnostics: RunDiagnostics }
export interface TerminalCommandResult { stdout: string; stderr: string; exitCode: number; durationMs: number; timedOut: boolean; truncated: boolean }
