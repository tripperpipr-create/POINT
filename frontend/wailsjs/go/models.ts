export namespace agent {
	
	export class CompletionPolicy {
	    explicitVerification: boolean;
	    fileChangesRequireVerification: boolean;
	    verificationToolAvailable: boolean;
	    blockingConfigurationIssue: boolean;
	    correctionEpisodes: number;
	    acceptedEvidence: string[];
	
	    static createFrom(source: any = {}) {
	        return new CompletionPolicy(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.explicitVerification = source["explicitVerification"];
	        this.fileChangesRequireVerification = source["fileChangesRequireVerification"];
	        this.verificationToolAvailable = source["verificationToolAvailable"];
	        this.blockingConfigurationIssue = source["blockingConfigurationIssue"];
	        this.correctionEpisodes = source["correctionEpisodes"];
	        this.acceptedEvidence = source["acceptedEvidence"];
	    }
	}

}

export namespace app {
	
	export class AgentTokenEstimate {
	    systemPrompt: number;
	    task: number;
	    toolSchemas: number;
	    context: number;
	    total: number;
	    contextWindow: number;
	    availableInput: number;
	    reservedOutput: number;
	
	    static createFrom(source: any = {}) {
	        return new AgentTokenEstimate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.systemPrompt = source["systemPrompt"];
	        this.task = source["task"];
	        this.toolSchemas = source["toolSchemas"];
	        this.context = source["context"];
	        this.total = source["total"];
	        this.contextWindow = source["contextWindow"];
	        this.availableInput = source["availableInput"];
	        this.reservedOutput = source["reservedOutput"];
	    }
	}
	export class AgentToolPreview {
	    definition: domain.ToolDefinition;
	    displayName: string;
	    category: string;
	    risk: string;
	    requiresApproval: boolean;
	    approvalReason?: string;
	    providesVerification?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AgentToolPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.definition = this.convertValues(source["definition"], domain.ToolDefinition);
	        this.displayName = source["displayName"];
	        this.category = source["category"];
	        this.risk = source["risk"];
	        this.requiresApproval = source["requiresApproval"];
	        this.approvalReason = source["approvalReason"];
	        this.providesVerification = source["providesVerification"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AgentRunPreview {
	    fingerprint: string;
	    version: string;
	    workspace: domain.Workspace;
	    profile: domain.AgentProfile;
	    systemMessage: string;
	    task: string;
	    tools: AgentToolPreview[];
	    context: domain.ContextPreview;
	    tokens: AgentTokenEstimate;
	    completion: agent.CompletionPolicy;
	    warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new AgentRunPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fingerprint = source["fingerprint"];
	        this.version = source["version"];
	        this.workspace = this.convertValues(source["workspace"], domain.Workspace);
	        this.profile = this.convertValues(source["profile"], domain.AgentProfile);
	        this.systemMessage = source["systemMessage"];
	        this.task = source["task"];
	        this.tools = this.convertValues(source["tools"], AgentToolPreview);
	        this.context = this.convertValues(source["context"], domain.ContextPreview);
	        this.tokens = this.convertValues(source["tokens"], AgentTokenEstimate);
	        this.completion = this.convertValues(source["completion"], agent.CompletionPolicy);
	        this.warnings = source["warnings"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AgentRunPreviewRequest {
	    profileId: string;
	    task: string;
	    goal?: string;
	    acceptanceCriteria?: string[];
	    constraints?: string[];
	    contextItems?: domain.RunContextInput[];
	
	    static createFrom(source: any = {}) {
	        return new AgentRunPreviewRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profileId = source["profileId"];
	        this.task = source["task"];
	        this.goal = source["goal"];
	        this.acceptanceCriteria = source["acceptanceCriteria"];
	        this.constraints = source["constraints"];
	        this.contextItems = this.convertValues(source["contextItems"], domain.RunContextInput);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class Bootstrap {
	    version: string;
	    profiles: domain.AgentProfile[];
	    profileTemplates: domain.AgentProfileTemplate[];
	    toolCatalog: domain.ToolCatalogItem[];
	    customTools: domain.CustomTool[];
	    customToolTemplates: domain.CustomToolTemplate[];
	    workspaces: domain.Workspace[];
	    runs: domain.Run[];
	    runDiagnostics: diagnostics.RunDiagnostics[];
	    workflows: domain.AgentWorkflow[];
	    workflowRuns: domain.WorkflowRun[];
	    currentWorkspace?: domain.Workspace;
	    providerCatalog: domain.ProviderPreset[];
	    indexStatus: workspace.IndexStatus;
	    changes: domain.PatchProposal[];
	
	    static createFrom(source: any = {}) {
	        return new Bootstrap(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.profiles = this.convertValues(source["profiles"], domain.AgentProfile);
	        this.profileTemplates = this.convertValues(source["profileTemplates"], domain.AgentProfileTemplate);
	        this.toolCatalog = this.convertValues(source["toolCatalog"], domain.ToolCatalogItem);
	        this.customTools = this.convertValues(source["customTools"], domain.CustomTool);
	        this.customToolTemplates = this.convertValues(source["customToolTemplates"], domain.CustomToolTemplate);
	        this.workspaces = this.convertValues(source["workspaces"], domain.Workspace);
	        this.runs = this.convertValues(source["runs"], domain.Run);
	        this.runDiagnostics = this.convertValues(source["runDiagnostics"], diagnostics.RunDiagnostics);
	        this.workflows = this.convertValues(source["workflows"], domain.AgentWorkflow);
	        this.workflowRuns = this.convertValues(source["workflowRuns"], domain.WorkflowRun);
	        this.currentWorkspace = this.convertValues(source["currentWorkspace"], domain.Workspace);
	        this.providerCatalog = this.convertValues(source["providerCatalog"], domain.ProviderPreset);
	        this.indexStatus = this.convertValues(source["indexStatus"], workspace.IndexStatus);
	        this.changes = this.convertValues(source["changes"], domain.PatchProposal);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CustomToolPreviewRequest {
	    tool: domain.CustomTool;
	    arguments: number[];
	
	    static createFrom(source: any = {}) {
	        return new CustomToolPreviewRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tool = this.convertValues(source["tool"], domain.CustomTool);
	        this.arguments = source["arguments"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ProviderProbeRequest {
	    provider: string;
	    baseUrl: string;
	    apiKey?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProviderProbeRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.provider = source["provider"];
	        this.baseUrl = source["baseUrl"];
	        this.apiKey = source["apiKey"];
	    }
	}
	export class ProviderProbeResult {
	    connected: boolean;
	    provider: string;
	    baseUrl: string;
	    models: providers.ModelInfo[];
	    latencyMs: number;
	
	    static createFrom(source: any = {}) {
	        return new ProviderProbeResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connected = source["connected"];
	        this.provider = source["provider"];
	        this.baseUrl = source["baseUrl"];
	        this.models = this.convertValues(source["models"], providers.ModelInfo);
	        this.latencyMs = source["latencyMs"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RunDetails {
	    run: domain.Run;
	    events: domain.Event[];
	    approvals: domain.Approval[];
	    patches: domain.PatchProposal[];
	    diagnostics: diagnostics.RunDiagnostics;
	
	    static createFrom(source: any = {}) {
	        return new RunDetails(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.run = this.convertValues(source["run"], domain.Run);
	        this.events = this.convertValues(source["events"], domain.Event);
	        this.approvals = this.convertValues(source["approvals"], domain.Approval);
	        this.patches = this.convertValues(source["patches"], domain.PatchProposal);
	        this.diagnostics = this.convertValues(source["diagnostics"], diagnostics.RunDiagnostics);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class StartRunRequest {
	    profileId: string;
	    task: string;
	    goal?: string;
	    acceptanceCriteria?: string[];
	    constraints?: string[];
	    apiKey: string;
	    contextItems?: domain.RunContextInput[];
	    preflightFingerprint?: string;
	
	    static createFrom(source: any = {}) {
	        return new StartRunRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.profileId = source["profileId"];
	        this.task = source["task"];
	        this.goal = source["goal"];
	        this.acceptanceCriteria = source["acceptanceCriteria"];
	        this.constraints = source["constraints"];
	        this.apiKey = source["apiKey"];
	        this.contextItems = this.convertValues(source["contextItems"], domain.RunContextInput);
	        this.preflightFingerprint = source["preflightFingerprint"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class StartWorkflowRequest {
	    workflowId: string;
	    task: string;
	    apiKeys?: Record<string, string>;
	    contextItems?: domain.RunContextInput[];
	
	    static createFrom(source: any = {}) {
	        return new StartWorkflowRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workflowId = source["workflowId"];
	        this.task = source["task"];
	        this.apiKeys = source["apiKeys"];
	        this.contextItems = this.convertValues(source["contextItems"], domain.RunContextInput);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TerminalCommandRequest {
	    command: string;
	    cwd: string;
	    timeoutSeconds: number;
	
	    static createFrom(source: any = {}) {
	        return new TerminalCommandRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.command = source["command"];
	        this.cwd = source["cwd"];
	        this.timeoutSeconds = source["timeoutSeconds"];
	    }
	}
	export class TerminalCommandResult {
	    stdout: string;
	    stderr: string;
	    exitCode: number;
	    durationMs: number;
	    timedOut: boolean;
	    truncated: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TerminalCommandResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stdout = source["stdout"];
	        this.stderr = source["stderr"];
	        this.exitCode = source["exitCode"];
	        this.durationMs = source["durationMs"];
	        this.timedOut = source["timedOut"];
	        this.truncated = source["truncated"];
	    }
	}
	export class WorkspaceView {
	    workspace: domain.Workspace;
	    tree: domain.FileNode[];
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workspace = this.convertValues(source["workspace"], domain.Workspace);
	        this.tree = this.convertValues(source["tree"], domain.FileNode);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace diagnostics {
	
	export class ApprovalMetrics {
	    requested: number;
	    allowed: number;
	    denied: number;
	    pending: number;
	    waitMs: number;
	    averageWaitMs: number;
	    maxWaitMs: number;
	
	    static createFrom(source: any = {}) {
	        return new ApprovalMetrics(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.requested = source["requested"];
	        this.allowed = source["allowed"];
	        this.denied = source["denied"];
	        this.pending = source["pending"];
	        this.waitMs = source["waitMs"];
	        this.averageWaitMs = source["averageWaitMs"];
	        this.maxWaitMs = source["maxWaitMs"];
	    }
	}
	export class CompletionMetrics {
	    checks: number;
	    revisionRequests: number;
	    acceptedAfterRevision: boolean;
	    rejected: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CompletionMetrics(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.checks = source["checks"];
	        this.revisionRequests = source["revisionRequests"];
	        this.acceptedAfterRevision = source["acceptedAfterRevision"];
	        this.rejected = source["rejected"];
	    }
	}
	export class ContextMetrics {
	    compactions: number;
	    removedRounds: number;
	    reducedToolMessages: number;
	    releasedTokens: number;
	    peakInputTokens: number;
	    latestInputTokens: number;
	    inputBudgetTokens: number;
	
	    static createFrom(source: any = {}) {
	        return new ContextMetrics(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.compactions = source["compactions"];
	        this.removedRounds = source["removedRounds"];
	        this.reducedToolMessages = source["reducedToolMessages"];
	        this.releasedTokens = source["releasedTokens"];
	        this.peakInputTokens = source["peakInputTokens"];
	        this.latestInputTokens = source["latestInputTokens"];
	        this.inputBudgetTokens = source["inputBudgetTokens"];
	    }
	}
	export class GuardrailMetrics {
	    duplicatePlans: number;
	    inspectionRequired: number;
	    inspectionScope: number;
	    inspectionStale: number;
	
	    static createFrom(source: any = {}) {
	        return new GuardrailMetrics(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.duplicatePlans = source["duplicatePlans"];
	        this.inspectionRequired = source["inspectionRequired"];
	        this.inspectionScope = source["inspectionScope"];
	        this.inspectionStale = source["inspectionStale"];
	    }
	}
	export class ModelMetrics {
	    requests: number;
	    responses: number;
	    retries: number;
	    pending: number;
	    inputTokens: number;
	    outputTokens: number;
	    totalTokens: number;
	    usageReported: boolean;
	    latencyMs: number;
	    averageLatencyMs: number;
	    firstResponseMs: number;
	
	    static createFrom(source: any = {}) {
	        return new ModelMetrics(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.requests = source["requests"];
	        this.responses = source["responses"];
	        this.retries = source["retries"];
	        this.pending = source["pending"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.totalTokens = source["totalTokens"];
	        this.usageReported = source["usageReported"];
	        this.latencyMs = source["latencyMs"];
	        this.averageLatencyMs = source["averageLatencyMs"];
	        this.firstResponseMs = source["firstResponseMs"];
	    }
	}
	export class PatchMetrics {
	    proposed: number;
	    applied: number;
	    rejected: number;
	
	    static createFrom(source: any = {}) {
	        return new PatchMetrics(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.proposed = source["proposed"];
	        this.applied = source["applied"];
	        this.rejected = source["rejected"];
	    }
	}
	export class RetrievalMetrics {
	    searches: number;
	    truncatedSearches: number;
	    candidateChunks: number;
	    returnedChunks: number;
	    relatedFiles: number;
	    usedChars: number;
	
	    static createFrom(source: any = {}) {
	        return new RetrievalMetrics(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.searches = source["searches"];
	        this.truncatedSearches = source["truncatedSearches"];
	        this.candidateChunks = source["candidateChunks"];
	        this.returnedChunks = source["returnedChunks"];
	        this.relatedFiles = source["relatedFiles"];
	        this.usedChars = source["usedChars"];
	    }
	}
	export class Signal {
	    code: string;
	    severity: string;
	    value?: number;
	
	    static createFrom(source: any = {}) {
	        return new Signal(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.severity = source["severity"];
	        this.value = source["value"];
	    }
	}
	export class WorkspaceMetrics {
	    audits: number;
	    changedFiles: number;
	    revertibleChanges: number;
	    recordedChanges: number;
	    nonRevertibleChanges: number;
	    omittedChanges: number;
	    incompleteAudits: number;
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceMetrics(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.audits = source["audits"];
	        this.changedFiles = source["changedFiles"];
	        this.revertibleChanges = source["revertibleChanges"];
	        this.recordedChanges = source["recordedChanges"];
	        this.nonRevertibleChanges = source["nonRevertibleChanges"];
	        this.omittedChanges = source["omittedChanges"];
	        this.incompleteAudits = source["incompleteAudits"];
	    }
	}
	export class VerificationMetrics {
	    required: boolean;
	    recorded: boolean;
	    successfulCommands: number;
	
	    static createFrom(source: any = {}) {
	        return new VerificationMetrics(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.required = source["required"];
	        this.recorded = source["recorded"];
	        this.successfulCommands = source["successfulCommands"];
	    }
	}
	export class ToolMetric {
	    name: string;
	    calls: number;
	    succeeded: number;
	    failed: number;
	    pending: number;
	    durationMs: number;
	
	    static createFrom(source: any = {}) {
	        return new ToolMetric(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.calls = source["calls"];
	        this.succeeded = source["succeeded"];
	        this.failed = source["failed"];
	        this.pending = source["pending"];
	        this.durationMs = source["durationMs"];
	    }
	}
	export class ToolMetrics {
	    calls: number;
	    succeeded: number;
	    failed: number;
	    pending: number;
	    durationMs: number;
	    items: ToolMetric[];
	
	    static createFrom(source: any = {}) {
	        return new ToolMetrics(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.calls = source["calls"];
	        this.succeeded = source["succeeded"];
	        this.failed = source["failed"];
	        this.pending = source["pending"];
	        this.durationMs = source["durationMs"];
	        this.items = this.convertValues(source["items"], ToolMetric);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RunDiagnostics {
	    schemaVersion: number;
	    runId: string;
	    health: string;
	    stopReason: string;
	    durationMs: number;
	    model: ModelMetrics;
	    context: ContextMetrics;
	    retrieval: RetrievalMetrics;
	    completion: CompletionMetrics;
	    tools: ToolMetrics;
	    approvals: ApprovalMetrics;
	    patches: PatchMetrics;
	    verification: VerificationMetrics;
	    workspace: WorkspaceMetrics;
	    guardrails: GuardrailMetrics;
	    signals: Signal[];
	
	    static createFrom(source: any = {}) {
	        return new RunDiagnostics(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.schemaVersion = source["schemaVersion"];
	        this.runId = source["runId"];
	        this.health = source["health"];
	        this.stopReason = source["stopReason"];
	        this.durationMs = source["durationMs"];
	        this.model = this.convertValues(source["model"], ModelMetrics);
	        this.context = this.convertValues(source["context"], ContextMetrics);
	        this.retrieval = this.convertValues(source["retrieval"], RetrievalMetrics);
	        this.completion = this.convertValues(source["completion"], CompletionMetrics);
	        this.tools = this.convertValues(source["tools"], ToolMetrics);
	        this.approvals = this.convertValues(source["approvals"], ApprovalMetrics);
	        this.patches = this.convertValues(source["patches"], PatchMetrics);
	        this.verification = this.convertValues(source["verification"], VerificationMetrics);
	        this.workspace = this.convertValues(source["workspace"], WorkspaceMetrics);
	        this.guardrails = this.convertValues(source["guardrails"], GuardrailMetrics);
	        this.signals = this.convertValues(source["signals"], Signal);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	

}

export namespace domain {
	
	export class AgentProfile {
	    id: string;
	    name: string;
	    roleDescription: string;
	    systemPrompt: string;
	    goals: string[];
	    rules: string[];
	    provider: string;
	    providerPreset: string;
	    baseUrl: string;
	    model: string;
	    temperature: number;
	    maxOutputTokens: number;
	    contextWindowTokens: number;
	    reasoningEffort: string;
	    allowedTools: string[];
	    maxSteps: number;
	    maxDurationSeconds: number;
	    approvalMode: string;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new AgentProfile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.roleDescription = source["roleDescription"];
	        this.systemPrompt = source["systemPrompt"];
	        this.goals = source["goals"];
	        this.rules = source["rules"];
	        this.provider = source["provider"];
	        this.providerPreset = source["providerPreset"];
	        this.baseUrl = source["baseUrl"];
	        this.model = source["model"];
	        this.temperature = source["temperature"];
	        this.maxOutputTokens = source["maxOutputTokens"];
	        this.contextWindowTokens = source["contextWindowTokens"];
	        this.reasoningEffort = source["reasoningEffort"];
	        this.allowedTools = source["allowedTools"];
	        this.maxSteps = source["maxSteps"];
	        this.maxDurationSeconds = source["maxDurationSeconds"];
	        this.approvalMode = source["approvalMode"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AgentProfileTemplate {
	    id: string;
	    name: string;
	    description: string;
	    roleDescription: string;
	    systemPrompt: string;
	    goals: string[];
	    rules: string[];
	    allowedTools: string[];
	    maxSteps: number;
	    maxDurationSeconds: number;
	    approvalMode: string;
	
	    static createFrom(source: any = {}) {
	        return new AgentProfileTemplate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.roleDescription = source["roleDescription"];
	        this.systemPrompt = source["systemPrompt"];
	        this.goals = source["goals"];
	        this.rules = source["rules"];
	        this.allowedTools = source["allowedTools"];
	        this.maxSteps = source["maxSteps"];
	        this.maxDurationSeconds = source["maxDurationSeconds"];
	        this.approvalMode = source["approvalMode"];
	    }
	}
	export class WorkflowStep {
	    id: string;
	    name: string;
	    profileId: string;
	    instruction: string;
	    includeOriginalContext: boolean;
	    includePreviousResult: boolean;
	
	    static createFrom(source: any = {}) {
	        return new WorkflowStep(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.profileId = source["profileId"];
	        this.instruction = source["instruction"];
	        this.includeOriginalContext = source["includeOriginalContext"];
	        this.includePreviousResult = source["includePreviousResult"];
	    }
	}
	export class AgentWorkflow {
	    id: string;
	    name: string;
	    description: string;
	    steps: WorkflowStep[];
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new AgentWorkflow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.steps = this.convertValues(source["steps"], WorkflowStep);
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Approval {
	    id: string;
	    runId: string;
	    agentId: string;
	    toolName: string;
	    reason: string;
	    arguments: number[];
	    status: string;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    resolvedAt?: any;
	
	    static createFrom(source: any = {}) {
	        return new Approval(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.runId = source["runId"];
	        this.agentId = source["agentId"];
	        this.toolName = source["toolName"];
	        this.reason = source["reason"];
	        this.arguments = source["arguments"];
	        this.status = source["status"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.resolvedAt = this.convertValues(source["resolvedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RunContextItem {
	    id: string;
	    kind: string;
	    label: string;
	    path?: string;
	    format?: string;
	    mediaType?: string;
	    content: string;
	    dataBase64?: string;
	    digest?: string;
	    size: number;
	    sourceSize?: number;
	    extractedSize?: number;
	    width?: number;
	    height?: number;
	    truncated?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RunContextItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.label = source["label"];
	        this.path = source["path"];
	        this.format = source["format"];
	        this.mediaType = source["mediaType"];
	        this.content = source["content"];
	        this.dataBase64 = source["dataBase64"];
	        this.digest = source["digest"];
	        this.size = source["size"];
	        this.sourceSize = source["sourceSize"];
	        this.extractedSize = source["extractedSize"];
	        this.width = source["width"];
	        this.height = source["height"];
	        this.truncated = source["truncated"];
	    }
	}
	export class ContextPreview {
	    items: RunContextItem[];
	    totalSourceBytes: number;
	    totalContextBytes: number;
	    totalImageBytes: number;
	    estimatedTokens: number;
	    warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new ContextPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.items = this.convertValues(source["items"], RunContextItem);
	        this.totalSourceBytes = source["totalSourceBytes"];
	        this.totalContextBytes = source["totalContextBytes"];
	        this.totalImageBytes = source["totalImageBytes"];
	        this.estimatedTokens = source["estimatedTokens"];
	        this.warnings = source["warnings"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CustomToolParameter {
	    name: string;
	    displayName: string;
	    description: string;
	    type: string;
	    required: boolean;
	    enumValues?: string[];
	    maxLength?: number;
	
	    static createFrom(source: any = {}) {
	        return new CustomToolParameter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.displayName = source["displayName"];
	        this.description = source["description"];
	        this.type = source["type"];
	        this.required = source["required"];
	        this.enumValues = source["enumValues"];
	        this.maxLength = source["maxLength"];
	    }
	}
	export class CustomTool {
	    id: string;
	    kind: string;
	    displayName: string;
	    description: string;
	    command: string;
	    program?: string;
	    arguments?: string[];
	    parameters?: CustomToolParameter[];
	    providesVerification?: boolean;
	    cwd: string;
	    timeoutSeconds: number;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new CustomTool(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.displayName = source["displayName"];
	        this.description = source["description"];
	        this.command = source["command"];
	        this.program = source["program"];
	        this.arguments = source["arguments"];
	        this.parameters = this.convertValues(source["parameters"], CustomToolParameter);
	        this.providesVerification = source["providesVerification"];
	        this.cwd = source["cwd"];
	        this.timeoutSeconds = source["timeoutSeconds"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class CustomToolTemplate {
	    id: string;
	    name: string;
	    description: string;
	    tool: CustomTool;
	
	    static createFrom(source: any = {}) {
	        return new CustomToolTemplate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.tool = this.convertValues(source["tool"], CustomTool);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Event {
	    id: string;
	    runId: string;
	    agentId: string;
	    type: string;
	    step: number;
	    actor: string;
	    data: number[];
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Event(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.runId = source["runId"];
	        this.agentId = source["agentId"];
	        this.type = source["type"];
	        this.step = source["step"];
	        this.actor = source["actor"];
	        this.data = source["data"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class FileNode {
	    name: string;
	    path: string;
	    isDir: boolean;
	    size?: number;
	    children?: FileNode[];
	
	    static createFrom(source: any = {}) {
	        return new FileNode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.isDir = source["isDir"];
	        this.size = source["size"];
	        this.children = this.convertValues(source["children"], FileNode);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PatchProposal {
	    id: string;
	    runId: string;
	    approvalId: string;
	    sourceTool?: string;
	    path: string;
	    originalHash: string;
	    originalExisted: boolean;
	    original: string;
	    proposed: string;
	    diff: string;
	    status: string;
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new PatchProposal(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.runId = source["runId"];
	        this.approvalId = source["approvalId"];
	        this.sourceTool = source["sourceTool"];
	        this.path = source["path"];
	        this.originalHash = source["originalHash"];
	        this.originalExisted = source["originalExisted"];
	        this.original = source["original"];
	        this.proposed = source["proposed"];
	        this.diff = source["diff"];
	        this.status = source["status"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ProviderPreset {
	    id: string;
	    name: string;
	    description: string;
	    kind: string;
	    baseUrl: string;
	    defaultModel: string;
	    local: boolean;
	    requiresApiKey: boolean;
	    external: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProviderPreset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.kind = source["kind"];
	        this.baseUrl = source["baseUrl"];
	        this.defaultModel = source["defaultModel"];
	        this.local = source["local"];
	        this.requiresApiKey = source["requiresApiKey"];
	        this.external = source["external"];
	    }
	}
	export class RunConfigurationSnapshot {
	    schemaVersion: number;
	    applicationVersion: string;
	    // Go type: time
	    capturedAt: any;
	    profile: AgentProfile;
	    customTools: CustomTool[];
	
	    static createFrom(source: any = {}) {
	        return new RunConfigurationSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.schemaVersion = source["schemaVersion"];
	        this.applicationVersion = source["applicationVersion"];
	        this.capturedAt = this.convertValues(source["capturedAt"], null);
	        this.profile = this.convertValues(source["profile"], AgentProfile);
	        this.customTools = this.convertValues(source["customTools"], CustomTool);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Run {
	    id: string;
	    agentId: string;
	    profileId: string;
	    workspaceId: string;
	    task: string;
	    contextItems: RunContextItem[];
	    configurationSnapshot: RunConfigurationSnapshot;
	    provider: string;
	    model: string;
	    status: string;
	    step: number;
	    requestCount: number;
	    toolsUsed: string[];
	    changedFiles: string[];
	    error?: string;
	    result?: string;
	    // Go type: time
	    startedAt: any;
	    // Go type: time
	    finishedAt?: any;
	    durationMs: number;
	
	    static createFrom(source: any = {}) {
	        return new Run(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.agentId = source["agentId"];
	        this.profileId = source["profileId"];
	        this.workspaceId = source["workspaceId"];
	        this.task = source["task"];
	        this.contextItems = this.convertValues(source["contextItems"], RunContextItem);
	        this.configurationSnapshot = this.convertValues(source["configurationSnapshot"], RunConfigurationSnapshot);
	        this.provider = source["provider"];
	        this.model = source["model"];
	        this.status = source["status"];
	        this.step = source["step"];
	        this.requestCount = source["requestCount"];
	        this.toolsUsed = source["toolsUsed"];
	        this.changedFiles = source["changedFiles"];
	        this.error = source["error"];
	        this.result = source["result"];
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.finishedAt = this.convertValues(source["finishedAt"], null);
	        this.durationMs = source["durationMs"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class RunContextInput {
	    kind: string;
	    label?: string;
	    path?: string;
	    content?: string;
	
	    static createFrom(source: any = {}) {
	        return new RunContextInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.label = source["label"];
	        this.path = source["path"];
	        this.content = source["content"];
	    }
	}
	
	export class ToolCatalogItem {
	    name: string;
	    displayName: string;
	    description: string;
	    category: string;
	    risk: string;
	    requiresApproval: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ToolCatalogItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.displayName = source["displayName"];
	        this.description = source["description"];
	        this.category = source["category"];
	        this.risk = source["risk"];
	        this.requiresApproval = source["requiresApproval"];
	    }
	}
	export class ToolDefinition {
	    name: string;
	    description: string;
	    inputSchema: number[];
	
	    static createFrom(source: any = {}) {
	        return new ToolDefinition(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.inputSchema = source["inputSchema"];
	    }
	}
	export class WorkflowStepRun {
	    stepId: string;
	    stepName: string;
	    profileId: string;
	    profileName: string;
	    runId?: string;
	    status: string;
	    result?: string;
	    resultTruncated?: boolean;
	    error?: string;
	    // Go type: time
	    startedAt?: any;
	    // Go type: time
	    finishedAt?: any;
	
	    static createFrom(source: any = {}) {
	        return new WorkflowStepRun(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stepId = source["stepId"];
	        this.stepName = source["stepName"];
	        this.profileId = source["profileId"];
	        this.profileName = source["profileName"];
	        this.runId = source["runId"];
	        this.status = source["status"];
	        this.result = source["result"];
	        this.resultTruncated = source["resultTruncated"];
	        this.error = source["error"];
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.finishedAt = this.convertValues(source["finishedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class WorkflowSnapshot {
	    schemaVersion: number;
	    applicationVersion: string;
	    // Go type: time
	    capturedAt: any;
	    workflow: AgentWorkflow;
	
	    static createFrom(source: any = {}) {
	        return new WorkflowSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.schemaVersion = source["schemaVersion"];
	        this.applicationVersion = source["applicationVersion"];
	        this.capturedAt = this.convertValues(source["capturedAt"], null);
	        this.workflow = this.convertValues(source["workflow"], AgentWorkflow);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class WorkflowRun {
	    id: string;
	    workflowId: string;
	    workspaceId: string;
	    task: string;
	    contextItems: RunContextItem[];
	    snapshot: WorkflowSnapshot;
	    status: string;
	    currentStep: number;
	    stepRuns: WorkflowStepRun[];
	    error?: string;
	    result?: string;
	    // Go type: time
	    startedAt: any;
	    // Go type: time
	    finishedAt?: any;
	    durationMs: number;
	
	    static createFrom(source: any = {}) {
	        return new WorkflowRun(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.workflowId = source["workflowId"];
	        this.workspaceId = source["workspaceId"];
	        this.task = source["task"];
	        this.contextItems = this.convertValues(source["contextItems"], RunContextItem);
	        this.snapshot = this.convertValues(source["snapshot"], WorkflowSnapshot);
	        this.status = source["status"];
	        this.currentStep = source["currentStep"];
	        this.stepRuns = this.convertValues(source["stepRuns"], WorkflowStepRun);
	        this.error = source["error"];
	        this.result = source["result"];
	        this.startedAt = this.convertValues(source["startedAt"], null);
	        this.finishedAt = this.convertValues(source["finishedAt"], null);
	        this.durationMs = source["durationMs"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	export class Workspace {
	    id: string;
	    path: string;
	    name: string;
	    // Go type: time
	    openedAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Workspace(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.path = source["path"];
	        this.name = source["name"];
	        this.openedAt = this.convertValues(source["openedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace providers {
	
	export class ModelInfo {
	    id: string;
	    displayName: string;
	    ownedBy?: string;
	    size?: number;
	    // Go type: time
	    modifiedAt?: any;
	
	    static createFrom(source: any = {}) {
	        return new ModelInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.displayName = source["displayName"];
	        this.ownedBy = source["ownedBy"];
	        this.size = source["size"];
	        this.modifiedAt = this.convertValues(source["modifiedAt"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace tools {
	
	export class CustomProcessPreview {
	    definition: domain.ToolDefinition;
	    program: string;
	    arguments: string[];
	    command: string;
	    parameters: Record<string, any>;
	    cwd: string;
	    resolvedCwd: string;
	    reason: string;
	    timeoutSeconds: number;
	
	    static createFrom(source: any = {}) {
	        return new CustomProcessPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.definition = this.convertValues(source["definition"], domain.ToolDefinition);
	        this.program = source["program"];
	        this.arguments = source["arguments"];
	        this.command = source["command"];
	        this.parameters = source["parameters"];
	        this.cwd = source["cwd"];
	        this.resolvedCwd = source["resolvedCwd"];
	        this.reason = source["reason"];
	        this.timeoutSeconds = source["timeoutSeconds"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace workspace {
	
	export class FileContent {
	    path: string;
	    content: string;
	    numbered: string;
	    sha256?: string;
	    size: number;
	    truncated: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FileContent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.content = source["content"];
	        this.numbered = source["numbered"];
	        this.sha256 = source["sha256"];
	        this.size = source["size"];
	        this.truncated = source["truncated"];
	    }
	}
	export class IndexStatus {
	    state: string;
	    files: number;
	    chunks: number;
	    symbols: number;
	    // Go type: time
	    builtAt?: any;
	    durationMs: number;
	    approxBytes: number;
	
	    static createFrom(source: any = {}) {
	        return new IndexStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.files = source["files"];
	        this.chunks = source["chunks"];
	        this.symbols = source["symbols"];
	        this.builtAt = this.convertValues(source["builtAt"], null);
	        this.durationMs = source["durationMs"];
	        this.approxBytes = source["approxBytes"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Match {
	    path: string;
	    line: number;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new Match(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.line = source["line"];
	        this.text = source["text"];
	    }
	}

}

