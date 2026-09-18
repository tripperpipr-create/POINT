package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
)

// statisticsSkillVersionEvidence reports only observed outcomes for an exact
// Skill payload. Revision is human-readable metadata; Digest is the immutable
// identity used to keep rewritten or imported revisions separate.
type statisticsSkillVersionEvidence struct {
	SkillID               string    `json:"skillId"`
	Name                  string    `json:"name"`
	Revision              int       `json:"revision,omitempty"`
	Digest                string    `json:"digest"`
	PromotionStatus       string    `json:"promotionStatus,omitempty"`
	Runs                  int       `json:"runs"`
	Completed             int       `json:"completed"`
	Healthy               int       `json:"healthy"`
	Attention             int       `json:"attention"`
	Failed                int       `json:"failed"`
	ToolCalls             int       `json:"toolCalls"`
	ToolFailures          int       `json:"toolFailures"`
	ToolSuccessRate       *float64  `json:"toolSuccessRate,omitempty"`
	VerificationRequired  int       `json:"verificationRequired"`
	VerificationSatisfied int       `json:"verificationSatisfied"`
	VerificationRate      *float64  `json:"verificationRate,omitempty"`
	FeedbackRuns          int       `json:"feedbackRuns"`
	FeedbackCount         int       `json:"feedbackCount"`
	RevisionRuns          int       `json:"revisionRuns"`
	CompletionRevisions   int       `json:"completionRevisions"`
	ApprovalDenied        int       `json:"approvalDenied"`
	FirstSeenAt           time.Time `json:"firstSeenAt"`
	LastSeenAt            time.Time `json:"lastSeenAt"`
}

func buildSkillVersionStatistics(outcomes []domain.SkillOutcome) []statisticsSkillVersionEvidence {
	buckets := map[string]*statisticsSkillVersionEvidence{}
	seenRuns := map[string]map[string]bool{}
	for _, outcome := range outcomes {
		if strings.TrimSpace(outcome.SkillID) == "" || strings.TrimSpace(outcome.SkillDigest) == "" || strings.TrimSpace(outcome.RunID) == "" {
			continue
		}
		key := outcome.SkillID + "\x00" + outcome.SkillDigest
		if buckets[key] == nil {
			buckets[key] = &statisticsSkillVersionEvidence{
				SkillID: outcome.SkillID, Name: outcome.SkillName, Revision: outcome.SkillRevision,
				Digest: outcome.SkillDigest, PromotionStatus: outcome.PromotionStatus,
				FirstSeenAt: outcome.CreatedAt, LastSeenAt: outcome.CreatedAt,
			}
			seenRuns[key] = map[string]bool{}
		}
		if seenRuns[key][outcome.RunID] {
			continue
		}
		seenRuns[key][outcome.RunID] = true
		bucket := buckets[key]
		bucket.Runs++
		if outcome.RunStatus == domain.RunCompleted {
			bucket.Completed++
		}
		switch diagnostics.Health(outcome.Health) {
		case diagnostics.HealthHealthy:
			bucket.Healthy++
		case diagnostics.HealthAttention:
			bucket.Attention++
		case diagnostics.HealthFailed:
			bucket.Failed++
		}
		bucket.ToolCalls += outcome.ToolCalls
		bucket.ToolFailures += outcome.ToolFailures
		bucket.ApprovalDenied += outcome.ApprovalDenied
		if outcome.FeedbackCount > 0 {
			bucket.FeedbackRuns++
			bucket.FeedbackCount += outcome.FeedbackCount
		}
		if outcome.CompletionRevisions > 0 {
			bucket.RevisionRuns++
			bucket.CompletionRevisions += outcome.CompletionRevisions
		}
		if outcome.VerificationRequired {
			bucket.VerificationRequired++
			if outcome.VerificationRecorded {
				bucket.VerificationSatisfied++
			}
		}
		if bucket.FirstSeenAt.IsZero() || (!outcome.CreatedAt.IsZero() && outcome.CreatedAt.Before(bucket.FirstSeenAt)) {
			bucket.FirstSeenAt = outcome.CreatedAt
		}
		if outcome.CreatedAt.After(bucket.LastSeenAt) {
			bucket.LastSeenAt = outcome.CreatedAt
		}
	}
	result := make([]statisticsSkillVersionEvidence, 0, len(buckets))
	for _, bucket := range buckets {
		if bucket.ToolCalls > 0 {
			bucket.ToolSuccessRate = percentage(bucket.ToolCalls-bucket.ToolFailures, bucket.ToolCalls)
		}
		if bucket.VerificationRequired > 0 {
			bucket.VerificationRate = percentage(bucket.VerificationSatisfied, bucket.VerificationRequired)
		}
		result = append(result, *bucket)
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].LastSeenAt.Equal(result[j].LastSeenAt) {
			return result[i].LastSeenAt.After(result[j].LastSeenAt)
		}
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].Revision > result[j].Revision
	})
	return result
}

type statisticsImprovementRecommendation struct {
	Code        string `json:"code"`
	Severity    string `json:"severity"`
	Title       string `json:"title"`
	Detail      string `json:"detail"`
	Evidence    string `json:"evidence"`
	Step        string `json:"step"`
	ActionLabel string `json:"actionLabel"`
}

// statisticsQualityEvidence keeps quality claims tied to replayable run facts.
// There is deliberately no synthetic "agent score": a weighted number would
// hide whether the evidence came from verification, tool reliability or merely
// a technically completed run.
type statisticsQualityEvidence struct {
	AssessedExecutions    int                                   `json:"assessedExecutions"`
	HealthyExecutions     int                                   `json:"healthyExecutions"`
	AttentionExecutions   int                                   `json:"attentionExecutions"`
	FailedExecutions      int                                   `json:"failedExecutions"`
	HealthyRate           *float64                              `json:"healthyRate,omitempty"`
	VerificationRequired  int                                   `json:"verificationRequired"`
	VerificationSatisfied int                                   `json:"verificationSatisfied"`
	VerificationRate      *float64                              `json:"verificationRate,omitempty"`
	ToolCalls             int                                   `json:"toolCalls"`
	ToolSucceeded         int                                   `json:"toolSucceeded"`
	ToolFailed            int                                   `json:"toolFailed"`
	ToolSuccessRate       *float64                              `json:"toolSuccessRate,omitempty"`
	Recommendations       []statisticsImprovementRecommendation `json:"recommendations,omitempty"`
}

type statisticsBucket struct {
	ID               string                    `json:"id"`
	Name             string                    `json:"name"`
	Executions       int                       `json:"executions"`
	Completed        int                       `json:"completed"`
	Failed           int                       `json:"failed"`
	SuccessRate      float64                   `json:"successRate"`
	TotalTokens      int64                     `json:"totalTokens"`
	DiscussionTokens int64                     `json:"discussionTokens,omitempty"`
	PlanTokens       int64                     `json:"planTokens,omitempty"`
	ExecutionTokens  int64                     `json:"executionTokens,omitempty"`
	LearningTokens   int64                     `json:"learningTokens,omitempty"`
	BudgetCoverage   string                    `json:"budgetCoverage,omitempty"`
	KnownCostCents   int64                     `json:"knownCostCents"`
	AverageTokens    int64                     `json:"averageTokens"`
	AverageTimeMs    int64                     `json:"averageTimeMs"`
	Quality          statisticsQualityEvidence `json:"quality"`
	durationMs       int64
	seenExecutions   map[string]bool
	seenQualityRuns  map[string]bool
	toolFailures     map[string]int
	revisionRuns     int
	revisionCount    int
	deniedRuns       int
	deniedApprovals  int
}

func buildStatisticsBreakdowns(
	agents []domain.ProjectAgent,
	teams []domain.Team,
	quests []domain.Quest,
	flows []domain.FlowGraph,
	executions []domain.ExecutionInstance,
	usage []domain.UsageRecord,
	qualityByRun map[string]diagnostics.RunDiagnostics,
) map[string]any {
	agentBuckets := makeBucketsFromAgents(agents)
	questBuckets := makeBucketsFromQuests(quests)
	teamBuckets := makeBucketsFromTeams(teams)
	flowBuckets := makeBucketsFromFlows(flows)
	modelBuckets := map[string]*statisticsBucket{}
	providerBuckets := map[string]*statisticsBucket{}
	executionByID := make(map[string]domain.ExecutionInstance, len(executions))
	questByID := make(map[string]domain.Quest, len(quests))
	for _, quest := range quests {
		questByID[quest.ID] = quest
	}
	for _, execution := range executions {
		executionByID[execution.ID] = execution
		addExecution(agentBuckets[execution.ProjectAgentID], execution)
		addExecution(questBuckets[execution.QuestID], execution)
		quest := questByID[execution.QuestID]
		addExecution(teamBuckets[quest.TeamID], execution)
		addExecution(flowBuckets[quest.FlowID], execution)
		quality := qualityByRun[execution.RunID]
		addQualityEvidence(agentBuckets[execution.ProjectAgentID], quality)
		addQualityEvidence(questBuckets[execution.QuestID], quality)
		addQualityEvidence(teamBuckets[quest.TeamID], quality)
		addQualityEvidence(flowBuckets[quest.FlowID], quality)
	}
	for _, record := range usage {
		execution := executionByID[record.ExecutionID]
		agentID := execution.ProjectAgentID
		if agentID == "" {
			agentID = record.ProjectAgentID
		}
		questID := record.QuestID
		if questID == "" {
			questID = execution.QuestID
		}
		quest := questByID[questID]
		addUsage(agentBuckets[agentID], record)
		addUsage(questBuckets[questID], record)
		addUsage(teamBuckets[quest.TeamID], record)
		addUsage(flowBuckets[quest.FlowID], record)
		if record.Model != "" {
			bucket := ensureBucket(modelBuckets, record.Model, record.Model)
			addUsage(bucket, record)
			addUsageExecution(bucket, execution)
		}
		if record.Provider != "" {
			bucket := ensureBucket(providerBuckets, record.Provider, record.Provider)
			addUsage(bucket, record)
			addUsageExecution(bucket, execution)
		}
	}
	return map[string]any{
		"agentStats":    finishAgentBuckets(agentBuckets),
		"teamStats":     finishBuckets(teamBuckets),
		"questStats":    finishQuestBuckets(questBuckets),
		"flowStats":     finishBuckets(flowBuckets),
		"modelStats":    finishBuckets(modelBuckets),
		"providerStats": finishBuckets(providerBuckets),
	}
}

// classifyUsagePhase maps ledger outcomes onto budget lifecycle phases.
// Discussion before a Quest stays workspace-scoped (partial coverage).
func classifyUsagePhase(record domain.UsageRecord) string {
	outcome := strings.TrimSpace(record.Outcome)
	switch outcome {
	case "master_model":
		return "discussion"
	case "companion_model":
		if strings.TrimSpace(record.QuestID) == "" {
			return "discussion"
		}
		return "execution"
	case "orchestrator_plan", "master_planner_model":
		return "plan"
	case "agent_self_improvement":
		return "learning"
	default:
		if strings.TrimSpace(record.QuestID) != "" {
			return "execution"
		}
		return ""
	}
}

func buildBudgetPhaseTotals(usage []domain.UsageRecord) map[string]any {
	var discussion, plan, execution, learning int64
	for _, record := range usage {
		switch classifyUsagePhase(record) {
		case "discussion":
			discussion += record.TotalTokens
		case "plan":
			plan += record.TotalTokens
		case "execution":
			execution += record.TotalTokens
		case "learning":
			learning += record.TotalTokens
		}
	}
	return map[string]any{
		"discussionTokens": discussion,
		"planTokens":       plan,
		"executionTokens":  execution,
		"learningTokens":   learning,
		// Discussion is intentionally workspace-only until a Quest exists.
		"budgetCoverage": "partial",
		"phaseCoverage": map[string]string{
			"discussion": "partial",
			"plan":       "full",
			"execution":  "full",
			"learning":   "full",
		},
	}
}

func makeBucketsFromAgents(items []domain.ProjectAgent) map[string]*statisticsBucket {
	result := make(map[string]*statisticsBucket, len(items))
	for _, item := range items {
		result[item.ID] = newStatisticsBucket(item.ID, item.Name)
	}
	return result
}

func makeBucketsFromTeams(items []domain.Team) map[string]*statisticsBucket {
	result := make(map[string]*statisticsBucket, len(items))
	for _, item := range items {
		result[item.ID] = newStatisticsBucket(item.ID, item.Name)
	}
	return result
}

func makeBucketsFromQuests(items []domain.Quest) map[string]*statisticsBucket {
	result := make(map[string]*statisticsBucket, len(items))
	for _, item := range items {
		result[item.ID] = newStatisticsBucket(item.ID, item.Title)
	}
	return result
}

func makeBucketsFromFlows(items []domain.FlowGraph) map[string]*statisticsBucket {
	result := make(map[string]*statisticsBucket, len(items))
	for _, item := range items {
		result[item.ID] = newStatisticsBucket(item.ID, item.Name)
	}
	return result
}

func newStatisticsBucket(id, name string) *statisticsBucket {
	return &statisticsBucket{
		ID: id, Name: name,
		seenExecutions: map[string]bool{}, seenQualityRuns: map[string]bool{}, toolFailures: map[string]int{},
	}
}

func ensureBucket(buckets map[string]*statisticsBucket, id, name string) *statisticsBucket {
	if buckets[id] == nil {
		buckets[id] = newStatisticsBucket(id, name)
	}
	return buckets[id]
}

func addExecution(bucket *statisticsBucket, execution domain.ExecutionInstance) {
	if bucket == nil || execution.ID == "" || bucket.seenExecutions[execution.ID] {
		return
	}
	bucket.seenExecutions[execution.ID] = true
	bucket.Executions++
	bucket.durationMs += execution.DurationMs
	switch execution.Status {
	case domain.RunCompleted:
		bucket.Completed++
	case domain.RunFailed, domain.RunCancelled, domain.RunInterrupted:
		bucket.Failed++
	}
}

func addUsageExecution(bucket *statisticsBucket, execution domain.ExecutionInstance) {
	if execution.ID != "" {
		addExecution(bucket, execution)
	}
}

func addUsage(bucket *statisticsBucket, record domain.UsageRecord) {
	if bucket == nil {
		return
	}
	bucket.TotalTokens += record.TotalTokens
	if record.CostCents != nil {
		bucket.KnownCostCents += *record.CostCents
	}
	switch classifyUsagePhase(record) {
	case "discussion":
		bucket.DiscussionTokens += record.TotalTokens
	case "plan":
		bucket.PlanTokens += record.TotalTokens
	case "execution":
		bucket.ExecutionTokens += record.TotalTokens
	case "learning":
		bucket.LearningTokens += record.TotalTokens
	}
}

func addQualityEvidence(bucket *statisticsBucket, report diagnostics.RunDiagnostics) {
	if bucket == nil || report.RunID == "" || bucket.seenQualityRuns[report.RunID] {
		return
	}
	bucket.seenQualityRuns[report.RunID] = true
	quality := &bucket.Quality
	quality.AssessedExecutions++
	switch report.Health {
	case diagnostics.HealthHealthy:
		quality.HealthyExecutions++
	case diagnostics.HealthAttention:
		quality.AttentionExecutions++
	case diagnostics.HealthFailed:
		quality.FailedExecutions++
	}
	if report.Verification.Required {
		quality.VerificationRequired++
		if report.Verification.Recorded {
			quality.VerificationSatisfied++
		}
	}
	quality.ToolCalls += report.Tools.Calls
	quality.ToolSucceeded += report.Tools.Succeeded
	quality.ToolFailed += report.Tools.Failed
	for _, tool := range report.Tools.Items {
		if tool.Failed > 0 && strings.TrimSpace(tool.Name) != "" {
			bucket.toolFailures[tool.Name] += tool.Failed
		}
	}
	if report.Completion.RevisionRequests > 0 {
		bucket.revisionRuns++
		bucket.revisionCount += report.Completion.RevisionRequests
	}
	if report.Approvals.Denied > 0 {
		bucket.deniedRuns++
		bucket.deniedApprovals += report.Approvals.Denied
	}
}

func finishAgentBuckets(buckets map[string]*statisticsBucket) []statisticsBucket {
	result := finishBuckets(buckets)
	for index := range result {
		result[index].Quality.Recommendations = improvementRecommendations(result[index])
	}
	return result
}

func finishQuestBuckets(buckets map[string]*statisticsBucket) []statisticsBucket {
	result := finishBuckets(buckets)
	for index := range result {
		// Quest totals cover plan/execution/learning when present. Discussion
		// remains a separate workspace line, so quest coverage stays partial.
		result[index].BudgetCoverage = "partial"
	}
	return result
}

func improvementRecommendations(bucket statisticsBucket) []statisticsImprovementRecommendation {
	quality := bucket.Quality
	result := make([]statisticsImprovementRecommendation, 0, 4)
	if quality.VerificationRequired >= 2 && quality.VerificationSatisfied < quality.VerificationRequired {
		result = append(result, statisticsImprovementRecommendation{
			Code: "verification_gap", Severity: "warning", Step: "skills",
			Title:       "Усилить навык верификации",
			Detail:      "Проверьте testing/build Skill и его required tools. Hub ничего не подключит без вашего подтверждения.",
			Evidence:    fmt.Sprintf("Верификация подтверждена в %d из %d обязательных запусков.", quality.VerificationSatisfied, quality.VerificationRequired),
			ActionLabel: "Открыть навыки агента",
		})
	}
	terminalTools := quality.ToolSucceeded + quality.ToolFailed
	if terminalTools >= 5 && quality.ToolFailed >= 2 && quality.ToolFailed*100 >= terminalTools*20 {
		failedTools := topFailedTools(bucket.toolFailures, 2)
		evidence := fmt.Sprintf("%d сбоев в %d завершённых вызовах tools.", quality.ToolFailed, terminalTools)
		if len(failedTools) > 0 {
			evidence = fmt.Sprintf("Повторные сбои: %s; всего %d из %d вызовов.", strings.Join(failedTools, ", "), quality.ToolFailed, terminalTools)
		}
		result = append(result, statisticsImprovementRecommendation{
			Code: "tool_instability", Severity: "warning", Step: "tools",
			Title:       "Стабилизировать инструменты",
			Detail:      "Проверьте конфигурацию и policy проблемных tools; не расширяйте доступ без необходимости.",
			Evidence:    evidence,
			ActionLabel: "Открыть tools агента",
		})
	}
	if bucket.deniedRuns >= 2 && bucket.deniedApprovals >= 2 {
		result = append(result, statisticsImprovementRecommendation{
			Code: "permission_friction", Severity: "attention", Step: "permissions",
			Title:       "Проверить политики доступа",
			Detail:      "Сверьте задачу агента с его tool policy. Решение может быть в сужении сценария, а не в выдаче дополнительных прав.",
			Evidence:    fmt.Sprintf("Отклонено %d approval-запросов в %d разных запусках.", bucket.deniedApprovals, bucket.deniedRuns),
			ActionLabel: "Открыть разрешения",
		})
	}
	if bucket.revisionRuns >= 2 && bucket.revisionCount >= 2 {
		result = append(result, statisticsImprovementRecommendation{
			Code: "completion_revisions", Severity: "attention", Step: "rules",
			Title:       "Уточнить критерии готовности",
			Detail:      "Добавьте в постоянные правила агента только повторяющиеся требования к результату и доказательствам.",
			Evidence:    fmt.Sprintf("Проверка вернула результат на доработку %d раз в %d разных запусках.", bucket.revisionCount, bucket.revisionRuns),
			ActionLabel: "Открыть правила агента",
		})
	}
	return result
}

func topFailedTools(failures map[string]int, limit int) []string {
	type toolFailure struct {
		name  string
		count int
	}
	items := make([]toolFailure, 0, len(failures))
	for name, count := range failures {
		if count > 0 {
			items = append(items, toolFailure{name: name, count: count})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].count != items[j].count {
			return items[i].count > items[j].count
		}
		return items[i].name < items[j].name
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, fmt.Sprintf("%s ×%d", item.name, item.count))
	}
	return result
}

func finishBuckets(buckets map[string]*statisticsBucket) []statisticsBucket {
	result := make([]statisticsBucket, 0, len(buckets))
	for _, bucket := range buckets {
		terminal := bucket.Completed + bucket.Failed
		if terminal > 0 {
			bucket.SuccessRate = float64(bucket.Completed) / float64(terminal) * 100
		}
		if bucket.Executions > 0 {
			bucket.AverageTokens = bucket.TotalTokens / int64(bucket.Executions)
			bucket.AverageTimeMs = bucket.durationMs / int64(bucket.Executions)
		}
		if bucket.Quality.AssessedExecutions > 0 {
			bucket.Quality.HealthyRate = percentage(bucket.Quality.HealthyExecutions, bucket.Quality.AssessedExecutions)
		}
		if bucket.Quality.VerificationRequired > 0 {
			bucket.Quality.VerificationRate = percentage(bucket.Quality.VerificationSatisfied, bucket.Quality.VerificationRequired)
		}
		terminalTools := bucket.Quality.ToolSucceeded + bucket.Quality.ToolFailed
		if terminalTools > 0 {
			bucket.Quality.ToolSuccessRate = percentage(bucket.Quality.ToolSucceeded, terminalTools)
		}
		result = append(result, *bucket)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Executions != result[j].Executions {
			return result[i].Executions > result[j].Executions
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func percentage(value, total int) *float64 {
	if total <= 0 {
		return nil
	}
	result := float64(value) / float64(total) * 100
	return &result
}
