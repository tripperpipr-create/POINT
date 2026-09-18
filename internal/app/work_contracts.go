package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
)

func workContractFromNode(node domain.FlowNode) (domain.WorkContract, error) {
	if node.Config == nil || node.Config["workContract"] == nil {
		return domain.WorkContract{}, nil
	}
	raw, err := json.Marshal(node.Config["workContract"])
	if err != nil {
		return domain.WorkContract{}, err
	}
	var contract domain.WorkContract
	err = json.Unmarshal(raw, &contract)
	return contract, err
}

func workContractInstructions(contract domain.WorkContract) string {
	if len(contract.OwnedPaths) == 0 && len(contract.ForbiddenPaths) == 0 && len(contract.InterfaceContracts) == 0 {
		return ""
	}
	return fmt.Sprintf("\n\nWork contract (enforced after execution; forbidden paths also denied mid-run):\nOwned paths: %s\nForbidden paths: %s\nInterfaces: %s\nCriteria: %s\nMerge plan: %s",
		strings.Join(contract.OwnedPaths, ", "), strings.Join(contract.ForbiddenPaths, ", "),
		strings.Join(contract.InterfaceContracts, "; "), strings.Join(contract.CriterionIDs, ", "), contract.MergePlan)
}

func workContractForbiddenPaths(contract *domain.WorkContract) []string {
	if contract == nil || len(contract.ForbiddenPaths) == 0 {
		return nil
	}
	return append([]string(nil), contract.ForbiddenPaths...)
}

// workContractMidRunForbiddenPaths previously seeded SharedProjectPaths / bin
// into engine amendments. Any seeded forbid trips toolHasWorkspaceWideAccess
// (list_files, search_code, propose_patch) and deadlocks greenfield writers
// that must create src/ when those directories do not exist yet. Ownership is
// enforced on the ChangeSet after the run instead.
func workContractMidRunForbiddenPaths(contract *domain.WorkContract) []string {
	_ = contract
	return nil
}

func validateWorkContractChanges(contract domain.WorkContract, set domain.ChangeSet) error {
	for _, item := range set.Items {
		path := normalizeContractPath(item.Path)
		for _, forbidden := range contract.ForbiddenPaths {
			if contractPathContains(forbidden, path) {
				return fmt.Errorf("work contract forbids change to %s", item.Path)
			}
		}
		if len(contract.OwnedPaths) == 0 {
			continue
		}
		owned := false
		for _, scope := range contract.OwnedPaths {
			if contractPathContains(scope, path) {
				owned = true
				break
			}
		}
		if !owned {
			return fmt.Errorf("change to %s is outside stage ownership", item.Path)
		}
	}
	return nil
}

// Нормализация пути в договоре этапа.
//
// Здесь стоял strings.Trim(..., "./"), и это срезало не префикс «./», а любые
// точки и слэши по краям: «.github/workflows/ci.yml» превращался в
// «github/workflows/ci.yml», а «.env» — в «env». Скрытый каталог и обычный
// каталог с тем же именем становились одним путём, и запрет на «.env» ложился
// на каталог «env» (и наоборот). Срезается ровно ведущее «./» и обрамляющие
// слэши; точка в начале имени — часть имени.
func normalizeContractPath(value string) string {
	clean := strings.ReplaceAll(filepath.Clean(strings.TrimSpace(value)), "\\", "/")
	clean = strings.TrimPrefix(clean, "./")
	return strings.Trim(clean, "/")
}

func contractPathContains(scope, path string) bool {
	scope = normalizeContractPath(scope)
	return scope != "" && (path == scope || strings.HasPrefix(path, scope+"/"))
}

func (a *App) requireIsolatedProjectWriters(flow domain.FlowGraph) error {
	groups := orchestrator.ConcurrentWriterGroups(flow)
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		roots := map[string]bool{}
		byID := map[string]domain.FlowNode{}
		for _, node := range flow.Nodes {
			byID[node.ID] = node
		}
		for _, id := range group {
			node := byID[id]
			root := domain.FlowNodeExecutionRoot(node)
			if root == "" {
				return errors.New("parallel writers require a distinct executionRoot on each writer node")
			}
			if roots[root] {
				return fmt.Errorf("parallel writers share executionRoot %q", root)
			}
			roots[root] = true
		}
		if !a.sandboxCapabilities().LiveWorkspaceIsolation {
			return errors.New("parallel writers require isolated execution workspaces")
		}
	}
	return nil
}

func writerRootBusy(flow domain.FlowGraph, flowRun domain.FlowRun, node domain.FlowNode) bool {
	root := domain.FlowNodeExecutionRoot(node)
	parallelPeers := map[string]bool{}
	for _, group := range orchestrator.ConcurrentWriterGroups(flow) {
		member := false
		for _, id := range group {
			if id == node.ID {
				member = true
				break
			}
		}
		if !member {
			continue
		}
		for _, id := range group {
			parallelPeers[id] = true
		}
	}
	for _, other := range flow.Nodes {
		if other.ID == node.ID || !domain.FlowNodeWriteFiles(other) {
			continue
		}
		state := flowRun.NodeStates[other.ID]
		if state.Status != "waiting_agent" && state.Status != "running" {
			continue
		}
		executionID, _ := state.Output["executionId"].(string)
		if executionID == "" && state.Status != "running" {
			continue
		}
		otherRoot := domain.FlowNodeExecutionRoot(other)
		if root != "" && otherRoot != "" && root != otherRoot {
			continue
		}
		if root == "" && otherRoot == "" && parallelPeers[other.ID] {
			continue
		}
		if root == "" && otherRoot == "" && !parallelPeers[other.ID] {
			return true
		}
		if root != "" && root == otherRoot {
			return true
		}
	}
	return false
}

func (a *App) applyFlowNodeWritePolicy(profile domain.AgentProfile, questID, flowNodeID string) domain.AgentProfile {
	if flowNodeID == "" || questID == "" {
		return profile
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return profile
	}
	quests, err := a.store.ListQuests(context.Background(), ws.ID)
	if err != nil {
		return profile
	}
	for _, quest := range quests {
		if quest.ID != questID || quest.FlowID == "" {
			continue
		}
		flow, flowErr := a.store.GetFlow(context.Background(), quest.FlowID)
		if flowErr != nil {
			return profile
		}
		for _, node := range flow.Nodes {
			if node.ID != flowNodeID {
				continue
			}
			if domain.FlowNodeWriteFiles(node) {
				return profile
			}
			filtered := make([]string, 0, len(profile.AllowedTools))
			for _, tool := range profile.AllowedTools {
				switch tool {
				case "propose_patch", "write_file", "apply_patch":
					continue
				default:
					filtered = append(filtered, tool)
				}
			}
			profile.AllowedTools = filtered
			return profile
		}
	}
	return profile
}
