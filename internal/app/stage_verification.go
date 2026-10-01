package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/verification"
)

// Сервис проверок: Point сам гоняет критерии приёмки, когда последний пишущий
// этап собрался завершиться, и запоминает исход по отпечатку дерева.
//
// Две цели. Качество: агент узнаёт о проваленной проверке, пока он в работе,
// а не после приёмки, когда чинить уже некому. Скорость: приёмка не гоняет
// заново то, что прошло на том же дереве, в том же образе и теми же командами.
//
// Режим задаёт POINT_VERIFY_SERVICE:
//   - off    — ничего не делается, как до сервиса;
//   - shadow — проверка перед приёмкой идёт, приёмка гоняет всё сама и
//     записывает, совпал бы переиспользованный исход (умолчание);
//   - on     — приёмка переиспользует целиком прошедший исход при совпавшем ключе.

const (
	verifyServiceOff    = "off"
	verifyServiceShadow = "shadow"
	verifyServiceOn     = "on"
)

func verifyServiceMode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("POINT_VERIFY_SERVICE"))) {
	case verifyServiceOff:
		return verifyServiceOff
	case verifyServiceOn:
		return verifyServiceOn
	}
	return verifyServiceShadow
}

const verificationBatchKeyVersion = "point-verification-batch-v1"

// verificationBatchKey связывает всё, от чего зависит исход: дерево, образ,
// исполненные команды с ожидаемым кодом и сетевую политику. Порядок команд
// входит в ключ — критерии идут в одной песочнице подряд, и поздний может
// опираться на след раннего (`npm ci`, затем тест).
func verificationBatchKey(treeDigest, imageDigest, networkPolicy string, hosts []string, commands []executedCriterion) string {
	sortedHosts := append([]string(nil), hosts...)
	sort.Strings(sortedHosts)
	hash := sha256.New()
	write := func(parts ...string) {
		for _, part := range parts {
			hash.Write([]byte(part))
			hash.Write([]byte{0})
		}
	}
	write(verificationBatchKeyVersion, treeDigest, imageDigest, strings.ToUpper(strings.TrimSpace(networkPolicy)), strings.Join(sortedHosts, ","))
	for _, command := range commands {
		write(command.CriterionID, verification.CheckIdentity(command.Tool, command.Arguments), fmt.Sprint(command.ExpectedExitCode))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

// preAcceptPlan — что приёмка этого Flow будет гонять и в каких условиях.
type preAcceptPlan struct {
	flowRun    domain.FlowRun
	acceptNode domain.FlowNode
	criteria   []domain.AcceptanceCriterion
	workOrder  *domain.WorkOrder
	policy     string
	hosts      []string
}

// lastWriterBeforeAccept находит узел приёмки, если nodeID — последний пишущий
// этап перед ней: ниже по графу до приёмки нет другого пишущего узла. Ранние
// писатели делают часть работы и честно не проходят критерии целого наряда.
func lastWriterBeforeAccept(flow domain.FlowGraph, nodeID string) (domain.FlowNode, bool) {
	nodes := make(map[string]domain.FlowNode, len(flow.Nodes))
	for _, node := range flow.Nodes {
		nodes[node.ID] = node
	}
	self, ok := nodes[nodeID]
	if !ok || !domain.FlowNodeWriteFiles(self) {
		return domain.FlowNode{}, false
	}
	switch domain.FlowNodeStageRole(self) {
	case domain.StageRoleImplement, domain.StageRoleIntegrate:
	default:
		return domain.FlowNode{}, false
	}
	var accept domain.FlowNode
	found := false
	seen := map[string]bool{nodeID: true}
	queue := []string{nodeID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range flow.Edges {
			if edge.From != current || seen[edge.To] {
				continue
			}
			seen[edge.To] = true
			next := nodes[edge.To]
			if domain.FlowNodeWriteFiles(next) {
				return domain.FlowNode{}, false
			}
			if next.Kind == domain.FlowNodeAgent && domain.FlowNodeStageRole(next) == domain.StageRoleAccept {
				if found && accept.ID != next.ID {
					return domain.FlowNode{}, false
				}
				accept, found = next, true
				continue
			}
			queue = append(queue, edge.To)
		}
	}
	return accept, found
}

func (a *App) preAcceptPlanFor(ctx context.Context, flowRunID, nodeID string) (preAcceptPlan, bool) {
	flowRun, err := a.store.GetFlowRun(ctx, flowRunID)
	if err != nil {
		return preAcceptPlan{}, false
	}
	flow, ok, err := flowruntime.FlowFromSnapshot(flowRun)
	if err != nil {
		return preAcceptPlan{}, false
	}
	if !ok {
		if flow, err = a.store.GetFlow(ctx, flowRun.FlowID); err != nil {
			return preAcceptPlan{}, false
		}
	}
	acceptNode, ok := lastWriterBeforeAccept(flow, nodeID)
	if !ok {
		return preAcceptPlan{}, false
	}
	quest, err := a.store.GetQuest(ctx, flowRun.QuestID)
	if err != nil || quest.Brief == nil {
		return preAcceptPlan{}, false
	}
	plan := preAcceptPlan{flowRun: flowRun, acceptNode: acceptNode, criteria: quest.Brief.Criteria}
	approval, approvalErr := a.store.WorkOrderApprovalByQuestV2(ctx, flowRun.QuestID)
	// Те же условия, что у детерминированной приёмки: иначе приёмку ведёт
	// модель, и переиспользовать исход будет некому.
	if !criteriaSupportDeterministicAccept(quest.Brief) && !(approvalErr == nil && criteriaSupportWorkOrderAcceptV2(quest.Brief)) {
		return preAcceptPlan{}, false
	}
	if approvalErr == nil {
		plan.workOrder = &approval.WorkOrder
	}
	agentID := acceptNode.AgentID
	if state, ok := flowRun.NodeStates[acceptNode.ID]; ok {
		if candidate, _ := state.Output["effectiveAgentId"].(string); strings.TrimSpace(candidate) != "" {
			agentID = strings.TrimSpace(candidate)
		}
	}
	projectAgent, err := a.store.GetProjectAgent(ctx, agentID)
	if err != nil {
		return preAcceptPlan{}, false
	}
	plan.hosts = bootstrapNetworkHosts(a, flowRun, projectAgent)
	plan.policy = "DENY"
	if len(plan.hosts) > 0 {
		plan.policy = "ALLOWLIST"
	}
	return plan, true
}

// VerifyBeforeCompletion гоняет критерии приёмки в чистой копии песочницы
// этапа — в том же виде, в каком дерево получит приёмка. Любая собственная
// неудача сервиса (нет копии, нет записи) — это «проверки не было»: этап
// завершается как раньше, и судит приёмка.
func (a *App) VerifyBeforeCompletion(ctx context.Context, request agent.StageVerifyRequest) agent.StageVerifyOutcome {
	if verifyServiceMode() == verifyServiceOff || request.FlowRunID == "" || request.FlowNodeID == "" {
		return agent.StageVerifyOutcome{}
	}
	plan, ok := a.preAcceptPlanFor(ctx, request.FlowRunID, request.FlowNodeID)
	if !ok {
		return agent.StageVerifyOutcome{}
	}
	exec, err := a.findExecution(request.ExecutionID)
	if err != nil {
		return agent.StageVerifyOutcome{}
	}
	record, err := a.store.GetSandbox(ctx, exec.SandboxID)
	if err != nil {
		return agent.StageVerifyOutcome{}
	}
	skipped := func(stage string, err error) agent.StageVerifyOutcome {
		slog.Warn("pre-accept verification skipped", "stage", stage, "flow_run_id", request.FlowRunID, "node_id", request.FlowNodeID, "error", security.Redact(err.Error()))
		return agent.StageVerifyOutcome{}
	}
	clean, err := os.MkdirTemp(filepath.Dir(record.Path), "verify-")
	if err != nil {
		return skipped("copy", err)
	}
	defer os.RemoveAll(clean)
	if err = sandbox.CopyCarried(record.Path, clean); err != nil {
		return skipped("copy", err)
	}
	tree, err := sandbox.TreeDigest(clean)
	if err != nil {
		return skipped("digest", err)
	}
	input := criteriaBatchInput{
		QuestID: plan.flowRun.QuestID, RunID: request.RunID, Criteria: plan.criteria, Sandbox: record, Root: clean,
		NetworkPolicy: plan.policy, NetworkHosts: plan.hosts, WorkOrder: plan.workOrder,
	}
	planned := a.plannedCriteriaCommands(input)
	if len(planned) == 0 {
		return agent.StageVerifyOutcome{}
	}
	image := sandbox.ExecutionImageForRecord(record)
	// Ключ считается по командам, какими их увидит приёмка в своей песочнице,
	// а не по временной копии: подстановка PHP зависит только от содержимого.
	key := verificationBatchKey(tree, image, plan.policy, plan.hosts, planned)
	if stored, found, lookupErr := a.store.PassedVerificationResultV2(ctx, plan.flowRun.QuestID, key); lookupErr == nil && found {
		return agent.StageVerifyOutcome{Ran: true, Passed: true, Reused: true, ResultID: stored.ID, TreeDigest: tree, Summaries: []string{"проверки уже прошли на этой ревизии"}}
	}
	batch, err := a.runCriteriaBatch(input)
	if err != nil {
		return skipped("run", err)
	}
	summaries := append([]string(nil), batch.Summaries...)
	passed := batch.AllOK
	for _, drift := range a.candidateLockfileDrift(plan.flowRun, clean) {
		passed = false
		summaries = append(summaries, lockfileSyncCriterionID+": "+drift.summary())
	}
	evidence, _ := json.Marshal(map[string]any{"criteria": batch.Criteria, "summaries": summaries, "commands": batch.Commands})
	result := domain.VerificationResult{
		ID: domain.NewID("verification"), WorkspaceID: plan.flowRun.WorkspaceID, QuestID: plan.flowRun.QuestID,
		FlowRunID: plan.flowRun.ID, ExecutionID: exec.ID, RunID: request.RunID, BatchKey: key, TreeDigest: tree,
		ImageDigest: image, Source: "pre_accept", AllPassed: passed, Evidence: evidence, CreatedAt: time.Now().UTC(),
	}
	if err = a.store.SaveVerificationResultV2(ctx, result); err != nil {
		slog.Warn("pre-accept verification result not saved", "quest_id", result.QuestID, "error", security.Redact(err.Error()))
		result.ID = ""
	}
	outcome := agent.StageVerifyOutcome{Ran: true, Passed: passed, ResultID: result.ID, TreeDigest: tree, Summaries: summaries}
	if !passed {
		outcome.Feedback = preAcceptFeedback(batch, summaries)
	}
	return outcome
}

// preAcceptFeedback говорит агенту, что не прошло и почему. Команды и причины
// — те же, что увидит приёмка; хвост вывода ограничен, чтобы не съесть окно.
func preAcceptFeedback(batch criteriaBatchResult, summaries []string) string {
	lines := []string{
		"Point ran the acceptance checks of this work order on a clean copy of your result (no node_modules, no build output — exactly what acceptance will see). They did not pass, so the stage is not finished:",
	}
	for _, summary := range summaries {
		if !strings.HasSuffix(summary, ": ok") {
			lines = append(lines, "- "+summary)
		}
	}
	for _, criterion := range batch.Criteria {
		if criterion.Status != "failed" || criterion.Check == nil {
			continue
		}
		var args struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(criterion.Check.Arguments, &args)
		lines = append(lines, fmt.Sprintf("\nCheck %s: `%s`\n%s", criterion.CriterionID, args.Command, tailRunes(criterion.Check.Detail, 2000)))
	}
	lines = append(lines,
		"\nFix the cause in the files and finish again; Point will rerun the checks itself. Do not rerun the full acceptance commands yourself unless you need their output to debug.",
		"If a check cannot pass because the check itself is wrong (for example it writes to a directory it never creates), do not work around it in the code: say so in your final answer, naming the check and the reason.",
	)
	return strings.Join(lines, "\n")
}

func tailRunes(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	return "…" + string(runes[len(runes)-limit:])
}

// acceptCriteriaBatch — прогон критериев приёмкой с учётом сервиса проверок.
// В режиме on целиком прошедший исход с тем же ключом не гоняется заново; в
// shadow приёмка гоняет всё сама и записывает, совпал бы взятый исход.
// Провал не переиспользуется никогда: он всегда перепроверяется.
func (a *App) acceptCriteriaBatch(ctx context.Context, input criteriaBatchInput, flowRun domain.FlowRun, executionID string) (criteriaBatchResult, map[string]any, error) {
	mode := verifyServiceMode()
	if mode == verifyServiceOff {
		batch, err := a.runCriteriaBatch(input)
		return batch, nil, err
	}
	note := map[string]any{"mode": mode}
	tree, digestErr := sandbox.TreeDigest(input.Sandbox.Path)
	planned := a.plannedCriteriaCommands(input)
	if digestErr != nil || len(planned) == 0 {
		batch, err := a.runCriteriaBatch(input)
		return batch, note, err
	}
	image := sandbox.ExecutionImageForRecord(input.Sandbox)
	key := verificationBatchKey(tree, image, input.NetworkPolicy, input.NetworkHosts, planned)
	note["treeDigest"] = tree
	stored, found, lookupErr := a.store.PassedVerificationResultV2(ctx, flowRun.QuestID, key)
	found = found && lookupErr == nil
	note["found"] = found
	if found && mode == verifyServiceOn {
		if batch, ok := reusedCriteriaBatch(stored, tree); ok {
			note["reusedFrom"] = stored.ID
			return batch, note, nil
		}
	}
	batch, err := a.runCriteriaBatch(input)
	if err != nil {
		return batch, note, err
	}
	for index := range batch.Criteria {
		if batch.Criteria[index].Check != nil {
			batch.Criteria[index].Check.TreeDigest = tree
		}
	}
	if found {
		// Тень: исход, который приёмка взяла бы, против того, что получила сама.
		note["wouldReuse"] = stored.ID
		note["agrees"] = batch.AllOK
	}
	evidence, _ := json.Marshal(map[string]any{"criteria": batch.Criteria, "summaries": batch.Summaries, "commands": batch.Commands})
	if saveErr := a.store.SaveVerificationResultV2(ctx, domain.VerificationResult{
		ID: domain.NewID("verification"), WorkspaceID: flowRun.WorkspaceID, QuestID: flowRun.QuestID, FlowRunID: flowRun.ID,
		ExecutionID: executionID, RunID: input.RunID, BatchKey: key, TreeDigest: tree, ImageDigest: image,
		Source: "accept", AllPassed: batch.AllOK, Evidence: evidence, CreatedAt: time.Now().UTC(),
	}); saveErr != nil {
		slog.Warn("accept verification result not saved", "quest_id", flowRun.QuestID, "error", security.Redact(saveErr.Error()))
	}
	return batch, note, nil
}

// reusedCriteriaBatch строит исход приёмки из сохранённого прогона. Каждая
// проверка несёт, откуда она взята: доказательство не выдаёт её за свежую.
func reusedCriteriaBatch(stored domain.VerificationResult, tree string) (criteriaBatchResult, bool) {
	var payload struct {
		Criteria  []agent.CriterionEvidence `json:"criteria"`
		Summaries []string                  `json:"summaries"`
		Commands  []executedCriterion       `json:"commands"`
	}
	if !stored.AllPassed || stored.TreeDigest != tree || json.Unmarshal(stored.Evidence, &payload) != nil || len(payload.Criteria) == 0 {
		return criteriaBatchResult{}, false
	}
	batch := criteriaBatchResult{AllOK: true, Commands: payload.Commands}
	for _, criterion := range payload.Criteria {
		switch criterion.Status {
		case "failed":
			return criteriaBatchResult{}, false
		case "needs_review", "unavailable":
			batch.NeedsReview = true
		}
		if criterion.Check != nil && criterion.Status == "satisfied" {
			check := *criterion.Check
			check.TreeDigest = tree
			check.ReusedFrom = &agent.CheckReuse{ResultID: stored.ID, RanAt: stored.CreatedAt, TreeDigest: stored.TreeDigest, ImageDigest: stored.ImageDigest, RunID: stored.RunID}
			check.Detail = fmt.Sprintf("Переиспользовано из проверки %s от %s на ревизии %s: дерево, образ и команды те же. %s",
				stored.ID, stored.CreatedAt.UTC().Format(time.RFC3339), shortDigest(stored.TreeDigest), check.Detail)
			criterion.Check = &check
		}
		batch.Criteria = append(batch.Criteria, criterion)
	}
	for _, summary := range payload.Summaries {
		if strings.HasSuffix(summary, ": ok") {
			summary += " (переиспользовано)"
		}
		batch.Summaries = append(batch.Summaries, summary)
	}
	return batch, true
}

func shortDigest(digest string) string {
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}

// reusedCheckProblem объясняет, почему взятому исходу нельзя верить. Пусто —
// можно: запись цела, целиком прошла, принадлежит этому миру и сделана на том
// же дереве, которое приёмка посчитала у себя.
func (a *App) reusedCheckProblem(ctx context.Context, workspaceID, acceptTree string, reuse agent.CheckReuse) string {
	if strings.TrimSpace(acceptTree) == "" || reuse.TreeDigest != acceptTree {
		return "отпечаток дерева приёмки не совпадает с деревом проверки"
	}
	stored, found, err := a.store.VerificationResultV2(ctx, reuse.ResultID)
	switch {
	case err != nil:
		return "запись проверки не читается"
	case !found:
		return "записи проверки нет"
	case !stored.AllPassed:
		return "проверка, на которую ссылается приёмка, не прошла"
	case stored.TreeDigest != acceptTree:
		return "запись проверки сделана на другом дереве"
	case stored.WorkspaceID != "" && workspaceID != "" && stored.WorkspaceID != workspaceID:
		return "запись проверки принадлежит другому проекту"
	}
	return ""
}

// preAcceptStageNote говорит последнему писателю, что приёмочные проверки
// Point прогонит сам. Без этого агент гоняет их по много раз «на всякий
// случай»: в квесте 30.09 `npm ci` и `npm run verify` шли по 3–7 минут
// двенадцать раз, и приёмка потом запускала их снова.
func preAcceptStageNote(quest domain.Quest, flow domain.FlowGraph, node domain.FlowNode) string {
	if verifyServiceMode() == verifyServiceOff || quest.Brief == nil {
		return ""
	}
	if _, ok := lastWriterBeforeAccept(flow, node.ID); !ok {
		return ""
	}
	if !criteriaSupportDeterministicAccept(quest.Brief) && !criteriaSupportWorkOrderAcceptV2(quest.Brief) {
		return ""
	}
	return "\n\nAcceptance checks: when you finish, Point itself runs the acceptance commands of this work order on a clean copy of your result and tells you what failed and why. Do not run the full acceptance commands yourself just to be sure — use narrow, fast checks while you work, and finish when the change is ready."
}
