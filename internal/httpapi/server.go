package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/changesets"
	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/events"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/orchestrator"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/servers"
)

type Server struct {
	app           *app.App
	eventHub      *events.Hub
	logger        *slog.Logger
	allowedOrigin string
	apiToken      string
	mux           *http.ServeMux
}

func New(application *app.App, eventHub *events.Hub, logger *slog.Logger, allowedOrigin, apiToken string) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if eventHub == nil {
		eventHub = events.NewHub()
	}
	s := &Server{app: application, eventHub: eventHub, logger: logger, allowedOrigin: allowedOrigin, apiToken: strings.TrimSpace(apiToken), mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.middleware(s.mux) }

func (s *Server) routes() {
	// Инструменты Point для исполнителей, принимающих их только по MCP.
	// Права здесь не решаются: сервер спрашивает исполнителя сущности,
	// открывшей сессию, — тот же, что отвечает на обычном пути.
	s.mux.HandleFunc("POST /mcp", s.app.MCPHandler())
	// Клиент MCP пробует открыть поток событий отдельным GET. Потока здесь нет:
	// вызовы идут запрос-ответом. Молчаливый 405 маршрутизатора это не
	// объясняет, поэтому отвечаем словами протокола.
	s.mux.HandleFunc("GET /mcp", s.app.MCPStreamRefusal())
	s.mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		s.write(w, http.StatusOK, map[string]any{"status": "ok", "version": app.Version})
	})
	s.mux.HandleFunc("GET /api/system/diagnostics", s.systemDiagnostics)
	s.mux.HandleFunc("GET /api/system/backups", s.listSystemBackups)
	s.mux.HandleFunc("POST /api/system/backups", s.createSystemBackup)
	s.mux.HandleFunc("GET /api/bootstrap", s.bootstrap)
	s.mux.HandleFunc("GET /api/state/runtime", s.runtimeState)
	s.mux.HandleFunc("GET /api/state/guild", s.guildState)
	s.mux.HandleFunc("POST /api/workspaces/open", s.openWorkspace)
	s.mux.HandleFunc("POST /api/intakes", s.createIntake)
	s.mux.HandleFunc("GET /api/intakes", s.listIntakes)
	s.mux.HandleFunc("GET /api/intakes/{id}", s.getIntake)
	s.mux.HandleFunc("POST /api/intakes/{id}/approve", s.approveIntake)
	s.mux.HandleFunc("GET /api/intakes/{id}/evidence", s.intakeEvidence)
	s.mux.HandleFunc("POST /api/intakes/{id}/expand", s.expandIntake)
	s.mux.HandleFunc("GET /api/workspaces/tree", s.workspaceTree)
	s.mux.HandleFunc("GET /api/workspace/model-routing", s.getWorkspaceModelRouting)
	s.mux.HandleFunc("PUT /api/workspace/model-routing", s.saveWorkspaceModelRouting)
	s.mux.HandleFunc("GET /api/files", s.readFile)
	s.mux.HandleFunc("PUT /api/files", s.saveFile)
	s.mux.HandleFunc("GET /api/search", s.search)
	s.mux.HandleFunc("POST /api/terminal", s.runTerminalCommand)
	s.mux.HandleFunc("POST /api/profiles", s.saveProfile)
	s.mux.HandleFunc("DELETE /api/profiles/{id}", s.deleteProfile)
	s.mux.HandleFunc("POST /api/custom-tools", s.saveCustomTool)
	s.mux.HandleFunc("POST /api/custom-tools/preview", s.previewCustomTool)
	s.mux.HandleFunc("POST /api/tools/preview", s.previewTool)
	s.mux.HandleFunc("POST /api/tools/execution-approvals", s.requestToolExecutionApproval)
	s.mux.HandleFunc("POST /api/tools/execution-approvals/{id}/resolve", s.resolveToolExecutionApproval)
	s.mux.HandleFunc("POST /api/tools/execute", s.executeTool)
	s.mux.HandleFunc("DELETE /api/custom-tools/{id}", s.deleteCustomTool)
	s.mux.HandleFunc("POST /api/providers/probe", s.probeProvider)
	s.mux.HandleFunc("POST /api/providers/capability-probe", s.probeModelCapability)
	s.mux.HandleFunc("GET /api/index/status", s.projectIndexStatus)
	s.mux.HandleFunc("GET /api/index/search", s.searchIndex)
	s.mux.HandleFunc("POST /api/index/update", s.updateProjectIndex)
	s.mux.HandleFunc("POST /api/index/invalidate", s.invalidateProjectIndex)
	s.mux.HandleFunc("POST /api/index/rebuild", s.rebuildProjectIndex)
	s.mux.HandleFunc("POST /api/context/preview", s.previewContext)
	s.mux.HandleFunc("POST /api/workflows", s.saveWorkflow)
	s.mux.HandleFunc("POST /api/workflows/validate", s.validateWorkflow)
	s.mux.HandleFunc("DELETE /api/workflows/{id}", s.deleteWorkflow)
	s.mux.HandleFunc("GET /api/workflow-runs", s.workflowRuns)
	s.mux.HandleFunc("POST /api/workflow-runs", s.startWorkflow)
	s.mux.HandleFunc("GET /api/workflow-runs/{id}", s.workflowRunDetails)
	s.mux.HandleFunc("POST /api/workflow-runs/{id}/cancel", s.cancelWorkflowRun)
	s.mux.HandleFunc("POST /api/workflow-runs/{id}/steps/{stepId}/claim", s.claimWorkflowStep)
	s.mux.HandleFunc("POST /api/workflow-runs/{id}/steps/{stepId}/heartbeat", s.heartbeatWorkflowStep)
	s.mux.HandleFunc("POST /api/workflow-runs/{id}/steps/{stepId}/complete", s.completeWorkflowStep)
	s.mux.HandleFunc("GET /api/decisions", s.decisions)
	s.mux.HandleFunc("POST /api/egress-asks/{id}/resolve", s.resolveEgressAsk)
	s.mux.HandleFunc("POST /api/master/sessions", s.masterSessionUpdate)
	s.mux.HandleFunc("GET /api/master/history", s.masterHistory)
	// Каталог чатов Чертога — единственный маршрут мастера, который смотрит за
	// пределы текущего мира. Только GET и только метаданные; см. комментарий у
	// App.MasterChatDirectory о границах этого послабления.
	s.mux.HandleFunc("GET /api/master/directory", s.masterDirectory)
	s.mux.HandleFunc("POST /api/master/turns", s.masterStartTurn)
	// Agent Hub v2 contracts. The Master owns creation/revision; approval is a
	// single idempotent transaction bound to the exact reviewed digest.
	s.mux.HandleFunc("POST /api/v2/master/turns", s.masterStartTurnV2)
	s.mux.HandleFunc("GET /api/v2/master/turns/{id}", s.masterGetTurn)
	s.mux.HandleFunc("GET /api/v2/master/turns/{id}/events", s.masterEvents)
	s.mux.HandleFunc("POST /api/v2/master/turns/{id}/cancel", s.masterCancelTurn)
	s.mux.HandleFunc("POST /api/v2/sources/preview", s.previewSourceV2)
	s.mux.HandleFunc("GET /api/v2/sources/{id}", s.getSourceSnapshotV2)
	s.mux.HandleFunc("POST /api/v2/sources/{id}/refresh", s.refreshSourceSnapshotV2)
	s.mux.HandleFunc("GET /api/v2/model-certifications", s.modelCertificationsV2)
	s.mux.HandleFunc("GET /api/v2/connections/{id}/capabilities", s.connectionCapabilitiesV2)
	s.mux.HandleFunc("POST /api/v2/work-orders", s.createWorkOrderV2)
	s.mux.HandleFunc("GET /api/v2/work-orders/{id}", s.getWorkOrderV2)
	s.mux.HandleFunc("DELETE /api/v2/work-orders/{id}", s.deleteWorkOrderV2)
	s.mux.HandleFunc("GET /api/v2/work-orders/{id}/diffs", s.listWorkOrderDiffsV2)
	s.mux.HandleFunc("POST /api/v2/work-orders/{id}/revise", s.reviseWorkOrderV2)
	s.mux.HandleFunc("POST /api/v2/work-orders/{id}/approve", s.approveWorkOrderV2)
	s.mux.HandleFunc("POST /api/v2/master/quests/{id}/{action}", s.controlWorkOrderQuestV2)
	s.mux.HandleFunc("GET /api/v2/master/quests/{id}", s.getWorkOrderQuestV2)
	s.mux.HandleFunc("GET /api/v2/master/quests/{id}/evidence", s.questEvidenceBundle)
	s.mux.HandleFunc("POST /api/v2/master/quests/{id}/application/{action}", s.controlDeliveredApplicationV2)
	s.mux.HandleFunc("GET /api/master/turns/{id}", s.masterGetTurn)
	s.mux.HandleFunc("GET /api/master/turns/{id}/events", s.masterEvents)
	s.mux.HandleFunc("POST /api/master/turns/{id}/cancel", s.masterCancelTurn)
	s.mux.HandleFunc("POST /api/master/conversations/{id}/fork", s.masterFork)
	s.mux.HandleFunc("GET /api/master/conversations/{id}/export", s.masterExport)
	s.mux.HandleFunc("GET /api/master/conversations/{id}/messages", s.masterPage)
	s.mux.HandleFunc("POST /api/master/chat", s.masterChat)
	s.mux.HandleFunc("POST /api/master/messages/{id}/feedback", s.masterMessageFeedback)
	s.mux.HandleFunc("GET /api/files/history", s.fileHistory)
	s.mux.HandleFunc("GET /api/runs", s.runs)
	s.mux.HandleFunc("POST /api/runs/preview", s.previewRun)
	s.mux.HandleFunc("POST /api/runs/fast-agent", s.startFastAgent)
	s.mux.HandleFunc("POST /api/runs", s.startRun)
	s.mux.HandleFunc("GET /api/runs/{id}", s.runDetails)
	s.mux.HandleFunc("POST /api/runs/{id}/cancel", s.cancelRun)
	s.mux.HandleFunc("POST /api/runs/{id}/pause", s.pauseRun)
	s.mux.HandleFunc("POST /api/runs/{id}/resume", s.resumeRun)
	s.mux.HandleFunc("POST /api/runs/{id}/undo", s.undoRunPatches)
	s.mux.HandleFunc("POST /api/runs/{id}/extend-active-time", s.extendActiveTime)
	s.mux.HandleFunc("POST /api/runs/{id}/message", s.injectRunMessage)
	s.mux.HandleFunc("POST /api/runs/{id}/forbid-file", s.forbidRunFile)
	s.mux.HandleFunc("POST /api/runs/{id}/context-amend", s.amendRunContext)
	s.mux.HandleFunc("POST /api/runs/{id}/context-add", s.addRunContext)
	s.mux.HandleFunc("GET /api/runs/{id}/context-inspector", s.runContextInspector)
	s.mux.HandleFunc("POST /api/approvals/{id}/resolve", s.resolveApproval)
	s.mux.HandleFunc("POST /api/patches/{id}/revert", s.revertPatch)
	s.mux.HandleFunc("GET /api/companion/live", s.companionLive)
	s.mux.HandleFunc("POST /api/companion/chat", s.companionChat)
	s.mux.HandleFunc("POST /api/companion/propose", s.companionPropose)
	s.mux.HandleFunc("GET /api/companion/history", s.companionHistory)
	s.mux.HandleFunc("DELETE /api/companion/history", s.clearCompanionHistory)
	s.mux.HandleFunc("POST /api/companion/interventions/{id}/dismiss", s.dismissCompanionIntervention)
	s.mux.HandleFunc("DELETE /api/companion/interventions/dismissed", s.restoreCompanionInterventions)
	s.mux.HandleFunc("POST /api/companion/actions/decide", s.decideCompanionAction)
	s.mux.HandleFunc("POST /api/ide/observations", s.recordIDEObservations)
	s.mux.HandleFunc("GET /api/model-candidates", s.modelCandidates)
	s.mux.HandleFunc("GET /api/model-capability-evidence", s.modelCapabilityEvidence)
	s.mux.HandleFunc("GET /api/agent-prep-chains", s.agentPrepChains)
	s.mux.HandleFunc("GET /api/flow-runs/{id}/team-events", s.teamEvents)
	s.mux.HandleFunc("POST /api/quests/{id}/reconcile-controller", s.reconcileProjectController)
	s.mux.HandleFunc("POST /api/executions/{id}/resume-runtime", s.resumeExternalExecution)
	s.mux.HandleFunc("POST /api/executions/{id}/stop-runtime", s.stopExternalExecution)
	s.mux.HandleFunc("POST /api/quest-proposals/decide", s.decideQuestProposal)
	s.mux.HandleFunc("GET /api/quest-proposals/{id}/brief-history", func(w http.ResponseWriter, r *http.Request) {
		value, err := s.app.TaskBriefHistory(r.Context(), r.PathValue("id"))
		s.result(w, value, err)
	})
	s.mux.HandleFunc("POST /api/companion/config", s.saveCompanionConfig)
	s.mux.HandleFunc("POST /api/orchestrator/config", s.saveOrchestratorConfig)
	s.mux.HandleFunc("POST /api/orchestrator/policy", s.orchestratorPolicy)
	s.mux.HandleFunc("POST /api/change-sets/{id}/apply", s.applyChangeSet)
	s.mux.HandleFunc("POST /api/change-sets/{id}/reject", s.rejectChangeSet)
	s.mux.HandleFunc("POST /api/change-sets/{id}/revert", s.revertChangeSet)
	s.mux.HandleFunc("POST /api/change-sets/{id}/resolve", s.resolveChangeSet)
	s.mux.HandleFunc("POST /api/connections", s.saveConnection)
	s.mux.HandleFunc("POST /api/blueprints", s.saveBlueprint)
	s.mux.HandleFunc("POST /api/project-agents/preview-prompt", s.previewCompiledPrompt)
	s.mux.HandleFunc("POST /api/project-agents/capability", s.agentCapability)
	s.mux.HandleFunc("POST /api/project-agents/capability-delta", s.agentCapabilityDelta)
	s.mux.HandleFunc("POST /api/project-agents", s.saveProjectAgent)
	s.mux.HandleFunc("DELETE /api/project-agents/{id}", s.deleteProjectAgent)
	s.mux.HandleFunc("DELETE /api/blueprints/{id}", s.deleteBlueprint)
	s.mux.HandleFunc("POST /api/project-agents/{id}/apply-blueprint", s.applyBlueprintToAgent)
	s.mux.HandleFunc("POST /api/project-agents/{id}/update-blueprint", s.updateBlueprintFromAgent)
	s.mux.HandleFunc("GET /api/project-agents/{id}/diff", s.diffProjectAgent)
	s.mux.HandleFunc("POST /api/skills", s.saveSkill)
	s.mux.HandleFunc("POST /api/project-skills/equip", s.equipSkill)
	s.mux.HandleFunc("POST /api/project-skills/preview", s.previewSkillEquip)
	s.mux.HandleFunc("POST /api/teams", s.saveTeam)
	s.mux.HandleFunc("DELETE /api/teams/{id}", s.deleteTeam)
	s.mux.HandleFunc("POST /api/quests", s.saveQuest)
	s.mux.HandleFunc("DELETE /api/quests/{id}", s.deleteQuest)
	s.mux.HandleFunc("POST /api/flows", s.saveFlow)
	s.mux.HandleFunc("GET /api/flows/{id}", s.getFlow)
	s.mux.HandleFunc("DELETE /api/flows/{id}", s.deleteFlow)
	s.mux.HandleFunc("POST /api/flows/compile-workflow", s.compileWorkflow)
	s.mux.HandleFunc("POST /api/flow-runs", s.startFlowRun)
	s.mux.HandleFunc("GET /api/flow-runs/{id}/handoffs", s.flowHandoffs)
	s.mux.HandleFunc("POST /api/flow-runs/{id}/tick", s.tickFlowRun)
	s.mux.HandleFunc("POST /api/flow-runs/{id}/nodes/{nodeId}/resolve", s.resolveFlowNode)
	s.mux.HandleFunc("POST /api/flow-runs/{id}/nodes/{nodeId}/merge/resolve", s.resolveFlowSandboxMerge)
	s.mux.HandleFunc("POST /api/executions/sandbox", s.startSandboxedExecution)
	s.mux.HandleFunc("POST /api/executions/{id}/launch", s.launchPendingExecution)
	s.mux.HandleFunc("POST /api/executions/{id}/cursor/start", s.beginCursorExecution)
	s.mux.HandleFunc("POST /api/executions/{id}/cursor/complete", s.completeCursorExecution)
	s.mux.HandleFunc("POST /api/executions/{id}/change-set", s.buildChangeSet)
	s.mux.HandleFunc("POST /api/executions/{id}/revert", s.revertExecution)
	s.mux.HandleFunc("GET /api/quests/{id}/outcome", s.questOutcome)
	s.mux.HandleFunc("GET /api/quests/{id}/evidence-bundle", s.questEvidenceBundle)
	s.mux.HandleFunc("GET /api/quests/{id}/replans", s.listQuestReplans)
	s.mux.HandleFunc("POST /api/quests/{id}/replan", s.replanQuest)
	s.mux.HandleFunc("POST /api/quests/{id}/revise-brief", s.reviseQuestBrief)
	s.mux.HandleFunc("POST /api/quests/{id}/revert", s.revertQuest)
	s.mux.HandleFunc("POST /api/flow-runs/{id}/nodes/{nodeId}/revert", s.revertFlowNode)
	s.mux.HandleFunc("POST /api/memories", s.saveMemory)
	s.mux.HandleFunc("DELETE /api/memories/{id}", s.deleteMemory)
	s.mux.HandleFunc("POST /api/usage", s.recordUsage)
	s.mux.HandleFunc("POST /api/budget", s.saveBudget)
	s.mux.HandleFunc("POST /api/budget/pricing", s.saveModelPricing)
	s.mux.HandleFunc("GET /api/statistics", s.statistics)
	s.mux.HandleFunc("POST /api/agent-improvements/{id}/rollback", s.rollbackAgentImprovement)
	s.mux.HandleFunc("POST /api/agent-improvements/{id}/promote", s.promoteAgentImprovement)
	s.mux.HandleFunc("GET /api/experience/search", s.searchExperience)
	s.mux.HandleFunc("POST /api/learning/manual/preview", s.previewManualLearning)
	s.mux.HandleFunc("POST /api/learning/manual/apply", s.applyManualLearning)
	s.mux.HandleFunc("GET /api/benchmarks", s.listAgentBenchmarks)
	s.mux.HandleFunc("POST /api/benchmarks", s.saveAgentBenchmark)
	s.mux.HandleFunc("POST /api/benchmarks/{id}/evaluate", s.evaluateAgentBenchmark)
	s.mux.HandleFunc("GET /api/benchmark-evaluations", s.listAgentBenchmarkEvaluations)
	s.mux.HandleFunc("POST /api/benchmark-comparisons", s.compareAgentBenchmarks)
	s.mux.HandleFunc("GET /api/docker", s.dockerOverview)
	s.mux.HandleFunc("GET /api/docker/logs", s.dockerLogs)
	s.mux.HandleFunc("POST /api/docker/containers/action", s.dockerContainerAction)
	s.mux.HandleFunc("POST /api/servers", s.saveServerProfile)
	s.mux.HandleFunc("GET /api/servers", s.listServerProfiles)
	s.mux.HandleFunc("DELETE /api/servers/{id}", s.deleteServerProfile)
	s.mux.HandleFunc("POST /api/servers/{id}/probe", s.probeServerProfile)
	s.mux.HandleFunc("POST /api/servers/{id}/list", s.listServerRemote)
	s.mux.HandleFunc("POST /api/servers/{id}/read", s.readServerRemote)
	s.mux.HandleFunc("GET /api/servers/{id}/terminal", s.serverTerminal)
	s.registerDBRoutes()
	s.registerConnectionRoutes()
	s.mux.HandleFunc("GET /api/events", s.events)
}

type statusRecorder struct {
	http.ResponseWriter
	status  int
	errCode string
	errMsg  string
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) finalStatus() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestID := strings.TrimSpace(r.Header.Get("X-Request-Id"))
		if requestID == "" {
			requestID = observability.NewRequestID()
		}
		w.Header().Set("X-Request-Id", requestID)
		r = r.WithContext(observability.WithRequestID(r.Context(), requestID))
		log := observability.From(r.Context())
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		if s.allowedOrigin != "" {
			origin := r.Header.Get("Origin")
			if origin == s.allowedOrigin {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-Id")
				w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// MCP живёт под своим ключом: он выдан одной сущности на время одного
		// разговора и уходит в чужой процесс. Общий ключ ядра туда отдавать
		// нельзя — им открывается всё API, а исполнителю нужен один маршрут.
		if s.apiToken != "" && r.URL.Path != "/api/health" && r.URL.Path != "/mcp" {
			provided := extractBearerToken(r.Header.Get("Authorization"), r.URL.Query().Get("access_token"))
			if !tokenMatches(s.apiToken, provided) {
				s.problem(w, http.StatusUnauthorized, "unauthorized", "valid API token required")
				log.Warn("http unauthorized", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodGet && r.Method != http.MethodDelete && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			s.problem(w, http.StatusUnsupportedMediaType, "content_type", "Content-Type must be application/json")
			log.Warn("http bad content-type", "method", r.Method, "path", r.URL.Path, "content_type", r.Header.Get("Content-Type"))
			return
		}
		bodyLimit := int64(2 << 20)
		// A PNG/JPEG/WebP source may contain up to 16 MiB before base64
		// expansion. Only the immutable source-intake route gets the larger
		// envelope; every other API request keeps the narrow default.
		if r.Method == http.MethodPost && r.URL.Path == "/api/v2/sources/preview" {
			bodyLimit = 24 << 20
		}
		r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		status := recorder.finalStatus()
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration_ms", time.Since(started).Milliseconds(),
			"remote", r.RemoteAddr,
		}
		if recorder.errCode != "" {
			attrs = append(attrs, "error_code", recorder.errCode, "error", observability.Snippet(security.Redact(recorder.errMsg), 400))
		}
		switch {
		case status >= 500:
			log.Error("http request", attrs...)
		case status >= 400:
			log.Warn("http request", attrs...)
		case observability.QuietHTTP(r.Method, r.URL.Path):
			log.Debug("http request", attrs...)
		default:
			log.Info("http request", attrs...)
		}
	})
}

func (s *Server) bootstrap(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.Bootstrap()
	s.result(w, value, err)
}
func (s *Server) systemDiagnostics(w http.ResponseWriter, r *http.Request) {
	s.write(w, http.StatusOK, s.app.SystemDiagnostics(r.Context()))
}
func (s *Server) createSystemBackup(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Reason string `json:"reason"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	reason := strings.TrimSpace(input.Reason)
	switch reason {
	case "":
		reason = "manual"
	case "manual", "recovery-point", "before-update":
	default:
		s.problem(w, http.StatusBadRequest, "invalid_backup_reason", "reason must be manual, recovery-point, or before-update")
		return
	}
	snapshot, err := s.app.CreateBackup(r.Context(), reason)
	if err != nil {
		s.result(w, nil, err)
		return
	}
	s.write(w, http.StatusOK, map[string]any{
		"id": snapshot.ID, "reason": snapshot.Reason, "createdAt": snapshot.CreatedAt,
		"sizeBytes": snapshot.Database.SizeBytes, "sha256": snapshot.Database.SHA256,
		"integrity": snapshot.Database.Integrity,
	})
}
func (s *Server) listSystemBackups(w http.ResponseWriter, r *http.Request) {
	snapshots, err := s.app.ListBackups(r.Context())
	if err != nil {
		s.result(w, nil, err)
		return
	}
	result := make([]map[string]any, 0, len(snapshots))
	for _, snapshot := range snapshots {
		result = append(result, map[string]any{
			"id": snapshot.ID, "reason": snapshot.Reason, "createdAt": snapshot.CreatedAt,
			"sizeBytes": snapshot.Database.SizeBytes, "sha256": snapshot.Database.SHA256,
			"integrity": snapshot.Database.Integrity,
		})
	}
	s.write(w, http.StatusOK, result)
}
func (s *Server) runtimeState(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.RuntimeState()
	s.result(w, value, err)
}
func (s *Server) guildState(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.GuildState()
	s.result(w, value, err)
}
func (s *Server) openWorkspace(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.OpenWorkspace(input.Path)
	s.result(w, value, err)
}

func (s *Server) createIntake(w http.ResponseWriter, r *http.Request) {
	var input app.CreateIntakeRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.CreateIntake(r.Context(), input)
	s.result(w, value, err)
}

func (s *Server) listIntakes(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ListIntakeSessions(r.Context())
	s.result(w, value, err)
}

func (s *Server) getIntake(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.IntakeSession(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) approveIntake(w http.ResponseWriter, r *http.Request) {
	var input app.ApproveIntakeRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ApproveIntake(r.Context(), r.PathValue("id"), input)
	s.result(w, value, err)
}

func (s *Server) intakeEvidence(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.IntakeEvidence(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) expandIntake(w http.ResponseWriter, r *http.Request) {
	var input app.ExpandIntakeRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ExpandIntake(r.Context(), r.PathValue("id"), input)
	s.result(w, value, err)
}

func (s *Server) questEvidenceBundle(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.EvidenceBundle(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}
func (s *Server) workspaceTree(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.WorkspaceTree()
	s.result(w, value, err)
}
func (s *Server) readFile(w http.ResponseWriter, r *http.Request) {
	path, err := url.QueryUnescape(r.URL.Query().Get("path"))
	if err != nil {
		s.problem(w, 400, "invalid_path", err.Error())
		return
	}
	value, err := s.app.ReadFile(path)
	s.result(w, value, err)
}
func (s *Server) saveFile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveFile(input.Path, input.Content)
	s.result(w, value, err)
}
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.SearchText(r.URL.Query().Get("q"))
	s.result(w, value, err)
}
func (s *Server) runTerminalCommand(w http.ResponseWriter, r *http.Request) {
	var input app.TerminalCommandRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.RunTerminalCommand(input)
	s.result(w, value, err)
}
func (s *Server) saveProfile(w http.ResponseWriter, r *http.Request) {
	var input appProfile
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveProfile(input.AgentProfile)
	s.result(w, value, err)
}
func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	err := s.app.DeleteProfile(r.PathValue("id"))
	if err != nil {
		s.problem(w, 400, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) deleteBlueprint(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteBlueprint(r.PathValue("id")); err != nil {
		s.problem(w, 400, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteProjectAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteProjectAgent(r.PathValue("id")); err != nil {
		s.problem(w, 400, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) saveCustomTool(w http.ResponseWriter, r *http.Request) {
	var input domain.CustomTool
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveCustomTool(input)
	s.result(w, value, err)
}
func (s *Server) previewCustomTool(w http.ResponseWriter, r *http.Request) {
	var input app.CustomToolPreviewRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.PreviewCustomTool(input)
	s.result(w, value, err)
}
func (s *Server) previewTool(w http.ResponseWriter, r *http.Request) {
	var input app.ToolExecutionRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.PreviewTool(input)
	s.result(w, value, err)
}
func (s *Server) requestToolExecutionApproval(w http.ResponseWriter, r *http.Request) {
	var input app.ToolExecutionApprovalRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.RequestToolExecutionApproval(input)
	s.result(w, value, err)
}
func (s *Server) resolveToolExecutionApproval(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Allow bool `json:"allow"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ResolveToolExecutionApproval(r.PathValue("id"), input.Allow)
	s.result(w, value, err)
}
func (s *Server) executeTool(w http.ResponseWriter, r *http.Request) {
	var input app.ToolExecutionRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ExecuteTool(input)
	s.result(w, value, err)
}
func (s *Server) deleteCustomTool(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteCustomTool(r.PathValue("id")); err != nil {
		s.problem(w, http.StatusConflict, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) probeProvider(w http.ResponseWriter, r *http.Request) {
	var input app.ProviderProbeRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ProbeProvider(input)
	s.result(w, value, err)
}
func (s *Server) probeModelCapability(w http.ResponseWriter, r *http.Request) {
	var input app.ModelCapabilityProbeRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ProbeModelCapability(input)
	s.result(w, value, err)
}
func (s *Server) projectIndexStatus(w http.ResponseWriter, _ *http.Request) {
	s.write(w, http.StatusOK, s.app.ProjectIndexStatus())
}
func (s *Server) searchIndex(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	s.write(w, http.StatusOK, s.app.SearchIndex(r.URL.Query().Get("q"), limit))
}
func (s *Server) updateProjectIndex(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Changed []string `json:"changed"`
		Deleted []string `json:"deleted"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.UpdateProjectIndex(input.Changed, input.Deleted)
	s.result(w, value, err)
}
func (s *Server) invalidateProjectIndex(w http.ResponseWriter, _ *http.Request) {
	s.write(w, http.StatusOK, s.app.InvalidateProjectIndex())
}
func (s *Server) rebuildProjectIndex(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.RebuildProjectIndex()
	s.result(w, value, err)
}
func (s *Server) revertPatch(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RevertPatch(r.PathValue("id"))
	s.result(w, value, err)
}
func (s *Server) previewContext(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ContextItems []domain.RunContextInput `json:"contextItems"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.PreviewContext(input.ContextItems)
	s.result(w, value, err)
}
func (s *Server) saveWorkflow(w http.ResponseWriter, r *http.Request) {
	var input domain.AgentWorkflow
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveWorkflow(input)
	s.result(w, value, err)
}
func (s *Server) validateWorkflow(w http.ResponseWriter, r *http.Request) {
	var input domain.AgentWorkflow
	if !s.decode(w, r, &input) {
		return
	}
	err := s.app.ValidateWorkflow(input)
	if err != nil {
		s.problem(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	s.write(w, http.StatusOK, map[string]bool{"valid": true})
}
func (s *Server) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteWorkflow(r.PathValue("id")); err != nil {
		s.problem(w, http.StatusConflict, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) workflowRuns(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.WorkflowRuns()
	s.result(w, value, err)
}
func (s *Server) startWorkflow(w http.ResponseWriter, r *http.Request) {
	var input app.StartWorkflowRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.StartWorkflow(input)
	s.result(w, value, err)
}
func (s *Server) workflowRunDetails(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.WorkflowRunDetails(r.PathValue("id"))
	s.result(w, value, err)
}
func (s *Server) cancelWorkflowRun(w http.ResponseWriter, r *http.Request) {
	if err := s.app.CancelWorkflowRun(r.PathValue("id")); err != nil {
		s.problem(w, http.StatusConflict, "cancel_failed", err.Error())
		return
	}
	s.write(w, http.StatusOK, map[string]bool{"cancelled": true})
}
func (s *Server) claimWorkflowStep(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ClaimWorkflowStep(r.PathValue("id"), r.PathValue("stepId"))
	s.result(w, value, err)
}
func (s *Server) heartbeatWorkflowStep(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ClaimToken string `json:"claimToken"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if err := s.app.HeartbeatWorkflowStep(r.PathValue("id"), r.PathValue("stepId"), input.ClaimToken); err != nil {
		s.problem(w, http.StatusConflict, "heartbeat_failed", err.Error())
		return
	}
	s.write(w, http.StatusOK, map[string]bool{"ok": true})
}
func (s *Server) completeWorkflowStep(w http.ResponseWriter, r *http.Request) {
	var input app.WorkflowStepCompletion
	if !s.decode(w, r, &input) {
		return
	}
	if err := s.app.CompleteWorkflowStep(r.PathValue("id"), r.PathValue("stepId"), input); err != nil {
		s.problem(w, http.StatusConflict, "completion_failed", err.Error())
		return
	}
	s.write(w, http.StatusOK, map[string]bool{"accepted": true})
}
func (s *Server) runs(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.Runs()
	s.result(w, value, err)
}

// Очередь решений. Узкий ответ вместо перезапроса bootstrap на 42 поля:
// экран «Решения» опрашивается часто, и таскать ради него всю историю прогонов
// значит платить историей за каждое обновление счётчика.
func (s *Server) decisions(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.Decisions(r.Context())
	s.result(w, value, err)
}

func (s *Server) resolveEgressAsk(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action string `json:"action"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	action := strings.ToLower(strings.TrimSpace(input.Action))
	switch action {
	case "continue", "stop":
		err := s.app.ResolveSupervisionContinue(r.Context(), id, action == "continue")
		if err != nil {
			s.result(w, nil, err)
			return
		}
		ask, getErr := s.app.GetEgressAsk(r.Context(), id)
		s.result(w, ask, getErr)
	default:
		value, err := s.app.ResolveEgressAsk(r.Context(), id, app.ResolveEgressAskRequest{Action: input.Action})
		s.result(w, value, err)
	}
}

// История одного файла. Путь приходит query-параметром и проверяется границей
// рабочей папки внутри use-case — транспорт его не разбирает и не чистит.
// Разговор с Мастером. Отдельная поверхность от чата компаньона: у них разные
// собеседники, разные конфигурации и разные результаты хода.
// Политика Мастера по черновику настройки. Считает тот же код, который её
// исполняет: превью не пересказывает правила, а показывает их.
// Что агент сможет и чего не сможет — по черновику настройки. Считает тот же
// код, который это разрешает: форма показывает результат, а не ввод.
func (s *Server) agentCapability(w http.ResponseWriter, r *http.Request) {
	var input domain.AgentProfile
	if !s.decode(w, r, &input) {
		return
	}
	s.result(w, s.app.AgentCapabilityFor(r.Context(), input), nil)
}

// Что изменится, если выдать умение или экипировать навык. Считает тот же код,
// что и саму годность: обещание карточки и поведение движка совпадают.
// Цепочка передач между агентами. Эстафета работала и раньше, но уезжала в
// модель текстом: когда второй агент делал не то, оставалось гадать, не понял
// он задачу или ему не то передали.
// Сверка обещания с результатом. Квест закрывался, а определение готовности
// оставалось словами: человек видел «завершён» и не мог сказать, выполнено ли
// то, ради чего квест ставили.
func (s *Server) questOutcome(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.QuestOutcome(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) listQuestReplans(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ListQuestReplans(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) replanQuest(w http.ResponseWriter, r *http.Request) {
	var input app.ReplanQuestRequest
	if !s.decode(w, r, &input) {
		return
	}
	input.QuestID = r.PathValue("id")
	value, err := s.app.ReplanQuest(r.Context(), input)
	s.result(w, value, err)
}

func (s *Server) reviseQuestBrief(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Brief           domain.TaskBrief `json:"brief"`
		ExpectedVersion int              `json:"expectedVersion"`
		ApproveVersion  int              `json:"approveVersion"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ReviseActiveQuestBrief(r.Context(), r.PathValue("id"), input.Brief, input.ExpectedVersion, input.ApproveVersion)
	s.result(w, value, err)
}

func (s *Server) flowHandoffs(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.Handoffs(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) agentCapabilityDelta(w http.ResponseWriter, r *http.Request) {
	var input app.CapabilityDeltaRequest
	if !s.decode(w, r, &input) {
		return
	}
	s.result(w, s.app.CapabilityDeltaFor(r.Context(), input), nil)
}

func (s *Server) orchestratorPolicy(w http.ResponseWriter, r *http.Request) {
	var input domain.OrchestratorConfig
	if !s.decode(w, r, &input) {
		return
	}
	s.result(w, orchestrator.DescribePolicy(input), nil)
}

func (s *Server) masterHistory(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.MasterSessionHistory(r.Context(), r.URL.Query().Get("conversationId"), r.URL.Query().Get("full") == "1")
	s.result(w, value, err)
}

func (s *Server) masterDirectory(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.MasterChatDirectory(r.Context())
	s.result(w, value, err)
}

func (s *Server) masterChat(w http.ResponseWriter, r *http.Request) {
	var input app.MasterChatRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.MasterChat(r.Context(), input)
	s.result(w, value, err)
}

// Оценка реплики Мастера. Отдельный маршрут, а не поле хода: оценивают уже
// сказанное, иногда через день, и связывать это с отправкой новой реплики
// значило бы требовать разговора ради отметки.
func (s *Server) masterMessageFeedback(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Value string `json:"value"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	err := s.app.MasterMessageFeedback(r.Context(), r.PathValue("id"), input.Value)
	s.result(w, map[string]any{"ok": err == nil}, err)
}

func (s *Server) fileHistory(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.FileHistory(r.Context(), r.URL.Query().Get("path"))
	s.result(w, value, err)
}
func (s *Server) previewRun(w http.ResponseWriter, r *http.Request) {
	var input app.AgentRunPreviewRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.PreviewAgentRun(input)
	s.result(w, value, err)
}
func (s *Server) startRun(w http.ResponseWriter, r *http.Request) {
	var input app.StartRunRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.StartRun(input)
	s.result(w, value, err)
}
func (s *Server) startFastAgent(w http.ResponseWriter, r *http.Request) {
	var input app.FastAgentRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.StartFastAgent(input)
	s.result(w, value, err)
}
func (s *Server) undoRunPatches(w http.ResponseWriter, r *http.Request) {
	var input app.UndoRunRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.UndoRunPatches(r.PathValue("id"), input)
	s.result(w, value, err)
}
func (s *Server) runDetails(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RunDetails(r.PathValue("id"))
	s.result(w, value, err)
}
func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	err := s.app.CancelRun(r.PathValue("id"))
	if err != nil {
		s.problem(w, 409, "cancel_failed", err.Error())
		return
	}
	s.write(w, 200, map[string]bool{"cancelled": true})
}

func (s *Server) pauseRun(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.PauseRun(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) resumeRun(w http.ResponseWriter, r *http.Request) {
	var input app.ResumeRunRequest
	if r.ContentLength != 0 {
		if !s.decode(w, r, &input) {
			return
		}
	}
	value, err := s.app.ResumeRun(r.PathValue("id"), input)
	s.result(w, value, err)
}

func (s *Server) extendActiveTime(w http.ResponseWriter, r *http.Request) {
	var input struct {
		APIKey string `json:"apiKey,omitempty"`
	}
	if r.ContentLength != 0 {
		if !s.decode(w, r, &input) {
			return
		}
	}
	value, err := s.app.ExtendActiveTime(r.PathValue("id"), input.APIKey)
	s.result(w, value, err)
}

func (s *Server) injectRunMessage(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Message        string `json:"message"`
		LearningIntent string `json:"learningIntent,omitempty"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if err := s.app.InjectRunMessage(r.PathValue("id"), input.Message, input.LearningIntent); err != nil {
		s.problem(w, http.StatusConflict, "message_failed", err.Error())
		return
	}
	s.write(w, http.StatusOK, map[string]bool{"queued": true})
}

func (s *Server) forbidRunFile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if err := s.app.ForbidRunFile(r.PathValue("id"), input.Path); err != nil {
		s.problem(w, http.StatusConflict, "forbid_failed", err.Error())
		return
	}
	s.write(w, http.StatusOK, map[string]bool{"forbidden": true})
}

func (s *Server) amendRunContext(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action string `json:"action"`
		ItemID string `json:"itemId"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if err := s.app.AmendRunContext(r.PathValue("id"), domain.ContextAmendAction(input.Action), input.ItemID); err != nil {
		s.problem(w, http.StatusConflict, "context_amend_failed", err.Error())
		return
	}
	s.write(w, http.StatusOK, map[string]bool{"queued": true})
}

func (s *Server) addRunContext(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ContextItems []domain.RunContextInput `json:"contextItems"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	preview, err := s.app.AddRunContext(r.PathValue("id"), input.ContextItems)
	if err != nil {
		s.problem(w, http.StatusConflict, "context_add_failed", err.Error())
		return
	}
	s.write(w, http.StatusAccepted, preview)
}

func (s *Server) runContextInspector(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RunContextInspector(r.PathValue("id"))
	s.result(w, value, err)
}
func (s *Server) resolveApproval(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Allow bool `json:"allow"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	if err := s.app.ResolveApproval(r.PathValue("id"), input.Allow); err != nil {
		s.problem(w, 409, "approval_failed", err.Error())
		return
	}
	s.write(w, 200, map[string]bool{"resolved": true})
}

func (s *Server) companionLive(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.CompanionLive(r.URL.Query().Get("focusPath"))
	s.result(w, value, err)
}

func (s *Server) companionChat(w http.ResponseWriter, r *http.Request) {
	var input companion.ChatRequest
	if !s.decode(w, r, &input) {
		return
	}
	stream := r.URL.Query().Get("stream") == "1" || strings.Contains(r.Header.Get("Accept"), "application/x-ndjson")
	if !stream {
		value, err := s.app.CompanionChat(r.Context(), input)
		s.result(w, value, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	flusher, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	writeLine := func(payload any) {
		_ = enc.Encode(payload)
		if flusher != nil {
			flusher.Flush()
		}
	}
	input.OnProgress = func(step, status string) {
		writeLine(map[string]any{"type": "progress", "step": step, "status": status})
	}
	input.OnDelta = func(reply string) {
		writeLine(map[string]any{"type": "delta", "reply": reply})
	}
	value, err := s.app.CompanionChat(r.Context(), input)
	if err != nil {
		writeLine(map[string]any{"type": "error", "error": map[string]any{"message": err.Error()}})
		return
	}
	writeLine(map[string]any{"type": "result", "response": value})
}

func (s *Server) companionPropose(w http.ResponseWriter, r *http.Request) {
	var input companion.RecommendRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.CompanionPropose(input)
	s.result(w, value, err)
}

func (s *Server) clearCompanionHistory(w http.ResponseWriter, _ *http.Request) {
	err := s.app.ClearCompanionHistory()
	s.result(w, map[string]bool{"cleared": err == nil}, err)
}

func (s *Server) companionHistory(w http.ResponseWriter, r *http.Request) {
	limit := 80
	if value, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && value > 0 && value <= 200 {
		limit = value
	}
	value, err := s.app.CompanionHistory(r.Context(), r.URL.Query().Get("speaker"), limit)
	s.result(w, value, err)
}

func (s *Server) dismissCompanionIntervention(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OccurrenceKey string `json:"occurrenceKey"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	err := s.app.DismissCompanionIntervention(r.PathValue("id"), input.OccurrenceKey)
	s.result(w, map[string]bool{"dismissed": err == nil}, err)
}

func (s *Server) restoreCompanionInterventions(w http.ResponseWriter, _ *http.Request) {
	err := s.app.RestoreCompanionInterventions()
	s.result(w, map[string]bool{"restored": err == nil}, err)
}

func (s *Server) decideCompanionAction(w http.ResponseWriter, r *http.Request) {
	var input app.CompanionActionDecision
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.DecideCompanionAction(input)
	s.result(w, value, err)
}

func (s *Server) recordIDEObservations(w http.ResponseWriter, r *http.Request) {
	var input app.IDEObservationBatch
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.RecordIDEObservations(input)
	s.result(w, value, err)
}

func (s *Server) decideQuestProposal(w http.ResponseWriter, r *http.Request) {
	var input app.QuestProposalDecision
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.DecideQuestProposalContext(r.Context(), input)
	s.result(w, value, err)
}

func (s *Server) beginCursorExecution(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.BeginCursorExecution(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) completeCursorExecution(w http.ResponseWriter, r *http.Request) {
	var input app.CursorExecutionCompletion
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.CompleteCursorExecution(r.Context(), r.PathValue("id"), input)
	s.result(w, value, err)
}

func (s *Server) saveCompanionConfig(w http.ResponseWriter, r *http.Request) {
	var input domain.CompanionConfig
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveCompanionConfig(input)
	s.result(w, value, err)
}

func (s *Server) saveOrchestratorConfig(w http.ResponseWriter, r *http.Request) {
	var input domain.OrchestratorConfig
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveOrchestratorConfig(input)
	s.result(w, value, err)
}

func (s *Server) applyChangeSet(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ApplyChangeSet(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) rejectChangeSet(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RejectChangeSet(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) revertChangeSet(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RevertChangeSet(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) resolveChangeSet(w http.ResponseWriter, r *http.Request) {
	var input changesets.ResolveRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ResolveChangeSetConflict(r.PathValue("id"), input)
	s.result(w, value, err)
}

func (s *Server) saveConnection(w http.ResponseWriter, r *http.Request) {
	var input connections.UpsertRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveConnection(input)
	s.result(w, value, err)
}

func (s *Server) saveBlueprint(w http.ResponseWriter, r *http.Request) {
	var input domain.AgentBlueprint
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveBlueprint(input)
	s.result(w, value, err)
}

func (s *Server) previewCompiledPrompt(w http.ResponseWriter, r *http.Request) {
	var input domain.ProjectAgent
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.PreviewCompiledPrompt(input)
	s.result(w, value, err)
}

func (s *Server) saveProjectAgent(w http.ResponseWriter, r *http.Request) {
	var input domain.ProjectAgent
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveProjectAgent(input)
	s.result(w, value, err)
}

func (s *Server) applyBlueprintToAgent(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ApplyBlueprintToProjectAgent(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) updateBlueprintFromAgent(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.UpdateBlueprintFromProjectAgent(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) diffProjectAgent(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.DiffProjectAgentBlueprint(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) saveSkill(w http.ResponseWriter, r *http.Request) {
	var input domain.SkillDefinition
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveSkill(input)
	s.result(w, value, err)
}

func (s *Server) equipSkill(w http.ResponseWriter, r *http.Request) {
	var input struct {
		WorkspaceID   string         `json:"workspaceId"`
		SkillID       string         `json:"skillId"`
		Configuration map[string]any `json:"configuration"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.EquipSkill(input.WorkspaceID, input.SkillID, input.Configuration)
	s.result(w, value, err)
}

func (s *Server) previewSkillEquip(w http.ResponseWriter, r *http.Request) {
	var input struct {
		WorkspaceID string `json:"workspaceId"`
		SkillID     string `json:"skillId"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.PreviewSkillEquip(input.WorkspaceID, input.SkillID)
	s.result(w, value, err)
}

func (s *Server) saveTeam(w http.ResponseWriter, r *http.Request) {
	var input domain.Team
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveTeam(input)
	s.result(w, value, err)
}

func (s *Server) saveQuest(w http.ResponseWriter, r *http.Request) {
	var input domain.Quest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveQuest(input)
	s.result(w, value, err)
}

func (s *Server) deleteQuest(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteQuest(r.PathValue("id")); err != nil {
		s.problem(w, 400, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteTeam(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteTeam(r.PathValue("id")); err != nil {
		s.problem(w, 400, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteWorkOrderV2(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteWorkOrderV2(r.Context(), r.PathValue("id")); err != nil {
		s.problem(w, 400, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteFlow(w http.ResponseWriter, r *http.Request) {
	if err := s.app.DeleteFlow(r.PathValue("id")); err != nil {
		s.problem(w, 400, "delete_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) saveFlow(w http.ResponseWriter, r *http.Request) {
	var input domain.FlowGraph
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveFlow(input)
	s.result(w, value, err)
}

func (s *Server) getFlow(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.GetFlow(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) compileWorkflow(w http.ResponseWriter, r *http.Request) {
	var input struct {
		WorkflowID string `json:"workflowId"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.CompileWorkflowToFlow(input.WorkflowID)
	s.result(w, value, err)
}

func (s *Server) startFlowRun(w http.ResponseWriter, r *http.Request) {
	var input struct {
		FlowID  string         `json:"flowId"`
		QuestID string         `json:"questId"`
		Input   map[string]any `json:"input"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.StartFlowRun(input.FlowID, input.QuestID, input.Input)
	s.result(w, value, err)
}

func (s *Server) tickFlowRun(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.TickFlowRun(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) resolveFlowNode(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Approved bool `json:"approved"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ResumeFlowApproval(r.PathValue("id"), r.PathValue("nodeId"), input.Approved)
	s.result(w, value, err)
}

func (s *Server) resolveFlowSandboxMerge(w http.ResponseWriter, r *http.Request) {
	var input sandbox.MergeResolution
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ResolveFlowSandboxMerge(r.PathValue("id"), r.PathValue("nodeId"), input)
	s.result(w, value, err)
}

func (s *Server) startSandboxedExecution(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ProjectAgentID string `json:"projectAgentId"`
		Task           string `json:"task"`
		QuestID        string `json:"questId"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.StartSandboxedExecution(input.ProjectAgentID, input.Task, input.QuestID)
	s.result(w, value, err)
}

func (s *Server) launchPendingExecution(w http.ResponseWriter, r *http.Request) {
	var input struct {
		APIKey string `json:"apiKey"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.LaunchPendingExecution(r.PathValue("id"), input.APIKey)
	s.result(w, value, err)
}

func (s *Server) buildChangeSet(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.BuildChangeSet(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) revertExecution(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RevertExecution(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) revertQuest(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RevertQuest(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) revertFlowNode(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RevertFlowNode(r.PathValue("id"), r.PathValue("nodeId"))
	s.result(w, value, err)
}

func (s *Server) saveMemory(w http.ResponseWriter, r *http.Request) {
	var input domain.MemoryRecord
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveMemory(input)
	s.result(w, value, err)
}

func (s *Server) deleteMemory(w http.ResponseWriter, r *http.Request) {
	err := s.app.DeleteMemory(r.PathValue("id"))
	s.result(w, map[string]bool{"deleted": err == nil}, err)
}

func (s *Server) recordUsage(w http.ResponseWriter, r *http.Request) {
	var input domain.UsageRecord
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.RecordUsage(input)
	s.result(w, value, err)
}

func (s *Server) saveBudget(w http.ResponseWriter, r *http.Request) {
	var input app.HubBudgetSettings
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveHubBudget(input)
	s.result(w, value, err)
}

func (s *Server) saveModelPricing(w http.ResponseWriter, r *http.Request) {
	var input domain.ModelPricingProfile
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveModelPricingProfile(input)
	s.result(w, value, err)
}

func (s *Server) statistics(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.Statistics(r.URL.Query().Get("workspaceId"))
	s.result(w, value, err)
}

func (s *Server) rollbackAgentImprovement(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RollbackAgentImprovement(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) promoteAgentImprovement(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.PromoteAgentImprovement(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) searchExperience(w http.ResponseWriter, r *http.Request) {
	limit := 30
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	value, err := s.app.SearchExperience(r.URL.Query().Get("q"), limit)
	s.result(w, value, err)
}

func (s *Server) listAgentBenchmarks(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.ListAgentBenchmarkSets()
	s.result(w, value, err)
}

func (s *Server) saveAgentBenchmark(w http.ResponseWriter, r *http.Request) {
	var input domain.AgentBenchmarkSet
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveAgentBenchmarkSet(input)
	s.result(w, value, err)
}

func (s *Server) evaluateAgentBenchmark(w http.ResponseWriter, r *http.Request) {
	var input app.AgentBenchmarkEvaluationRequest
	if !s.decode(w, r, &input) {
		return
	}
	input.BenchmarkSetID = r.PathValue("id")
	value, err := s.app.EvaluateAgentBenchmark(input)
	s.result(w, value, err)
}

func (s *Server) listAgentBenchmarkEvaluations(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	value, err := s.app.ListAgentBenchmarkEvaluations(limit)
	s.result(w, value, err)
}

func (s *Server) compareAgentBenchmarks(w http.ResponseWriter, r *http.Request) {
	var input app.AgentBenchmarkComparisonRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.CompareAgentBenchmarks(input)
	s.result(w, value, err)
}

func (s *Server) previewManualLearning(w http.ResponseWriter, r *http.Request) {
	var input app.ManualLearningRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.PreviewManualLearning(input)
	s.result(w, value, err)
}

func (s *Server) applyManualLearning(w http.ResponseWriter, r *http.Request) {
	var input app.ManualLearningRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ApplyManualLearning(input)
	s.result(w, value, err)
}

func (s *Server) dockerOverview(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.DockerOverview()
	s.result(w, value, err)
}

func (s *Server) dockerLogs(w http.ResponseWriter, r *http.Request) {
	tail := 100
	if raw := r.URL.Query().Get("tail"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			tail = parsed
		}
	}
	value, err := s.app.DockerContainerLogs(r.URL.Query().Get("container"), tail)
	s.result(w, value, err)
}

func (s *Server) dockerContainerAction(w http.ResponseWriter, r *http.Request) {
	var input app.DockerContainerActionRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.DockerContainerAction(input)
	s.result(w, value, err)
}

func (s *Server) saveServerProfile(w http.ResponseWriter, r *http.Request) {
	var input servers.UpsertRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveServerProfile(input)
	s.result(w, value, err)
}

func (s *Server) listServerProfiles(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.ListServerProfiles()
	s.result(w, value, err)
}

func (s *Server) deleteServerProfile(w http.ResponseWriter, r *http.Request) {
	err := s.app.DeleteServerProfile(r.PathValue("id"))
	s.result(w, map[string]any{"ok": true}, err)
}

func (s *Server) probeServerProfile(w http.ResponseWriter, r *http.Request) {
	var input app.ServerProbeRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ProbeServerProfile(r.PathValue("id"), input)
	s.result(w, value, err)
}

func (s *Server) listServerRemote(w http.ResponseWriter, r *http.Request) {
	var input app.ServerListRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ListServerRemotePath(r.PathValue("id"), input)
	s.result(w, value, err)
}

func (s *Server) readServerRemote(w http.ResponseWriter, r *http.Request) {
	var input app.ServerReadRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ReadServerRemoteFile(r.PathValue("id"), input)
	s.result(w, value, err)
}

func (s *Server) serverTerminal(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ServerTerminalArgv(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	workspaceID := s.app.CurrentWorkspaceID()
	if workspaceID == "" {
		s.problem(w, 409, "workspace_required", "open a workspace before subscribing to events")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.problem(w, 500, "stream_unsupported", "streaming is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	eventStream, cancel := s.eventHub.SubscribeWorkspace(workspaceID)
	defer cancel()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	fmt.Fprint(w, "event: ready\ndata: {}\n\n")
	flusher.Flush()
	for {
		select {
		case event, open := <-eventStream:
			if !open {
				return
			}
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: workbench\ndata: %s\n\n", data)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

type appProfile struct{ domain.AgentProfile }

func (s *Server) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		s.problem(w, 400, "invalid_json", err.Error())
		return false
	}
	return true
}
func (s *Server) result(w http.ResponseWriter, value any, err error) {
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, context.Canceled) {
			status = 499
		}
		s.problem(w, status, "request_failed", err.Error())
		return
	}
	s.write(w, http.StatusOK, value)
}
func (s *Server) write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (s *Server) problem(w http.ResponseWriter, status int, code, message string) {
	if rec, ok := w.(*statusRecorder); ok {
		rec.errCode = code
		rec.errMsg = message
	}
	s.write(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
