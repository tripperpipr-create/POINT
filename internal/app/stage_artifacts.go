package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

func stageArtifactsFromQuest(quest domain.Quest) []domain.StageArtifact {
	if quest.Controller == nil {
		return nil
	}
	raw, err := json.Marshal(quest.Controller["stageArtifacts"])
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var items []domain.StageArtifact
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	return items
}

func saveStageArtifacts(quest *domain.Quest, items []domain.StageArtifact) {
	if quest.Controller == nil {
		quest.Controller = map[string]any{}
	}
	quest.Controller["stageArtifacts"] = items
}

func appendStageArtifact(quest *domain.Quest, artifact domain.StageArtifact) {
	items := stageArtifactsFromQuest(*quest)
	if artifact.ID == "" {
		artifact.ID = domain.NewID("artifact")
	}
	if artifact.SchemaVersion == 0 {
		artifact.SchemaVersion = domain.StageArtifactSchemaV1
	}
	if artifact.Status == "" {
		artifact.Status = domain.ArtifactValid
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now().UTC()
	}
	items = append(items, artifact)
	saveStageArtifacts(quest, items)
}

func invalidateDownstreamArtifacts(quest *domain.Quest, fromStageID string) {
	if quest == nil {
		return
	}
	items := stageArtifactsFromQuest(*quest)
	invalidate := fromStageID == ""
	for i := range items {
		if items[i].StageID == fromStageID {
			invalidate = true
			items[i].Status = domain.ArtifactInvalidated
			continue
		}
		if invalidate && (items[i].Kind == "impl_review" || items[i].Kind == "acceptance" || items[i].Kind == domain.StageRoleImplReview || items[i].Kind == domain.StageRoleAccept) {
			items[i].Status = domain.ArtifactInvalidated
			continue
		}
		if invalidate {
			items[i].Status = domain.ArtifactInvalidated
		}
	}
	saveStageArtifacts(quest, items)
}

func (a *App) recordFlowNodeArtifact(flowRun domain.FlowRun, node domain.FlowNode, success bool, executionID string) {
	if flowRun.QuestID == "" || node.Kind != domain.FlowNodeAgent && node.Kind != domain.FlowNodeVerifier {
		return
	}
	quests, err := a.store.ListQuests(context.Background(), flowRun.WorkspaceID)
	if err != nil {
		return
	}
	for i := range quests {
		if quests[i].ID != flowRun.QuestID {
			continue
		}
		role := domain.FlowNodeStageRole(node)
		if role == "" {
			role = node.Name
		}
		status := domain.ArtifactValid
		if !success {
			status = domain.ArtifactInvalidated
		}
		appendStageArtifact(&quests[i], domain.StageArtifact{
			Kind: role, StageID: node.ID, ContentRef: "execution:" + executionID,
			ProducerID: node.AgentID, AttemptID: executionID, Status: status,
			InputRevision: flowRun.ID,
		})
		_ = a.store.SaveQuest(context.Background(), quests[i])
		return
	}
}

func reviewReadsIntegratedRevision(quest domain.Quest, node domain.FlowNode) bool {
	role := domain.FlowNodeStageRole(node)
	if node.Kind == domain.FlowNodeVerifier {
		role = domain.StageRoleAccept
	}
	if role != domain.StageRoleImplReview && role != domain.StageRoleAccept {
		return true
	}
	items := stageArtifactsFromQuest(quest)
	if len(items) == 0 {
		return true
	}
	hasImplement, hasValidIntegrate := false, false
	for _, item := range items {
		if item.Kind == domain.StageRoleImplement && item.Status == domain.ArtifactValid {
			hasImplement = true
		}
		if item.Kind == domain.StageRoleIntegrate && item.Status == domain.ArtifactValid {
			hasValidIntegrate = true
		}
	}
	if hasImplement && !hasValidIntegrate {
		return false
	}
	return true
}

func validAcceptanceArtifact(quest domain.Quest) bool {
	for _, item := range stageArtifactsFromQuest(quest) {
		if (item.Kind == "acceptance" || item.Kind == domain.StageRoleAccept) && item.Status == domain.ArtifactValid {
			return true
		}
	}
	return false
}

func bumpQuestCounter(quest *domain.Quest, key string) int {
	if quest.Controller == nil {
		quest.Controller = map[string]any{}
	}
	current := 0
	switch v := quest.Controller[key].(type) {
	case float64:
		current = int(v)
	case int:
		current = v
	}
	current++
	quest.Controller[key] = current
	return current
}

func questCounter(quest domain.Quest, key string) int {
	if quest.Controller == nil {
		return 0
	}
	switch v := quest.Controller[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

func (a *App) QuestSupervisionCount(ctx context.Context, workspaceID, questID string) int {
	if ctx == nil {
		ctx = context.Background()
	}
	if workspaceID == "" || questID == "" {
		return 0
	}
	quests, err := a.store.ListQuests(ctx, workspaceID)
	if err != nil {
		return 0
	}
	for _, quest := range quests {
		if quest.ID == questID {
			return questCounter(quest, "supervisionContinues")
		}
	}
	return 0
}

func deniedEgressReplay(quest domain.Quest, host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || quest.Controller == nil {
		return false
	}
	raw, _ := json.Marshal(quest.Controller["deniedEgressHosts"])
	var hosts []string
	_ = json.Unmarshal(raw, &hosts)
	for _, item := range hosts {
		if strings.ToLower(item) == host {
			return true
		}
	}
	return false
}

func recordDeniedEgress(quest *domain.Quest, host string) {
	host = strings.ToLower(strings.TrimSpace(host))
	if quest == nil || host == "" {
		return
	}
	if quest.Controller == nil {
		quest.Controller = map[string]any{}
	}
	raw, _ := json.Marshal(quest.Controller["deniedEgressHosts"])
	var hosts []string
	_ = json.Unmarshal(raw, &hosts)
	for _, item := range hosts {
		if strings.ToLower(item) == host {
			return
		}
	}
	quest.Controller["deniedEgressHosts"] = append(hosts, host)
}
