package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
	projectenv "local-agent-workbench/internal/environment"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

// Прогон машинных критериев наряда — один на приёмку и на проверку Point перед
// ней (stage_verification.go). Прежде он жил внутри tryDeterministicAccept, и
// второй вызывающий неизбежно разошёлся бы с первым: в сроке, в поправках, в
// образе. Результат проверки переиспользуется приёмкой только потому, что обе
// гоняют критерии одним кодом и в одинаковых условиях.

type criteriaBatchInput struct {
	QuestID  string
	RunID    string
	Criteria []domain.AcceptanceCriterion
	// Sandbox — запись песочницы, чьё дерево проверяется: её путь и образ.
	Sandbox domain.SandboxRecord
	// Root — где гонять команды. Пусто — в самой песочнице; проверка перед
	// приёмкой гоняет в чистой копии, как приёмка.
	Root          string
	NetworkPolicy string
	NetworkHosts  []string
	// WorkOrder — утверждённый наряд, если он есть: по нему узнаются критерии,
	// которые проверяются на хосте после доставки.
	WorkOrder *domain.WorkOrder
}

type criteriaBatchResult struct {
	Criteria    []agent.CriterionEvidence
	Summaries   []string
	AllOK       bool
	NeedsReview bool
	// Commands — команды, которые действительно запускались, в порядке
	// критериев: с поправками, сроком и подстановкой PHP. По ним считается
	// ключ переиспользования.
	Commands []executedCriterion
}

type executedCriterion struct {
	CriterionID      string          `json:"criterionId"`
	Tool             string          `json:"tool"`
	Arguments        json.RawMessage `json:"arguments"`
	ExpectedExitCode int             `json:"expectedExitCode"`
}

// criterionExecution готовит критерий к запуску: применяет поправку и решает,
// запускается ли он вообще. Тот же код считает ключ переиспользования — без
// запуска команд (plannedCriteriaCommands).
func criterionExecution(criterion domain.AcceptanceCriterion, amendments map[string]domain.CriterionAmendment, order *domain.WorkOrder, root string) (domain.AcceptanceCriterion, map[string]any, string) {
	if amendment, ok := amendments[criterion.ID]; ok && criterion.Kind != "manual" {
		criterion.Arguments = amendedCriterionArguments(criterion.Arguments, amendment)
	}
	if criterion.Kind == "manual" {
		return criterion, nil, "manual"
	}
	if order != nil && deferredHostCriterionV2(*order, criterion) {
		return criterion, nil, "deferred"
	}
	var args map[string]any
	_ = json.Unmarshal(criterion.Arguments, &args)
	if args == nil {
		args = map[string]any{}
	}
	if _, _, masked := maskedCriterionEvidence(criterion, args); masked {
		return criterion, args, "masked"
	}
	// Срок — предел run_command, а не пять минут: `npm ci && npm run verify`
	// у исполнителей cf-vue-apps шёл 344–402 с, и приёмка с 300 с упала бы
	// на сроке там, где работа была исправна.
	if _, ok := args["timeoutSeconds"]; !ok {
		args["timeoutSeconds"] = 600
	}
	if _, ok := args["reason"]; !ok {
		args["reason"] = "deterministic accept: " + criterion.ID
	}
	if cmd, _ := args["command"].(string); strings.TrimSpace(cmd) != "" {
		args["command"] = projectenv.ResolvePHPVerificationCommand(root, cmd)
	}
	return criterion, args, "run"
}

func expectedExitCode(criterion domain.AcceptanceCriterion) int {
	if criterion.ExpectedExitCode != nil {
		return *criterion.ExpectedExitCode
	}
	return 0
}

// plannedCriteriaCommands — что запустил бы runCriteriaBatch, без запуска.
func (a *App) plannedCriteriaCommands(input criteriaBatchInput) []executedCriterion {
	root := input.Root
	if root == "" {
		root = input.Sandbox.Path
	}
	amendments := a.criterionAmendments(context.Background(), input.QuestID)
	var planned []executedCriterion
	for _, criterion := range input.Criteria {
		prepared, args, mode := criterionExecution(criterion, amendments, input.WorkOrder, root)
		if mode != "run" {
			continue
		}
		raw, _ := json.Marshal(args)
		planned = append(planned, executedCriterion{CriterionID: prepared.ID, Tool: prepared.Tool, Arguments: raw, ExpectedExitCode: expectedExitCode(prepared)})
	}
	return planned
}

func (a *App) runCriteriaBatch(input criteriaBatchInput) (criteriaBatchResult, error) {
	root := input.Root
	if root == "" {
		root = input.Sandbox.Path
	}
	fs, err := workspace.Open(root)
	if err != nil {
		return criteriaBatchResult{}, err
	}
	// Образ — тот же, что у исполнителей этапа. Без него проверки уходили в
	// образ по умолчанию: 30.09 квест на Node 20 принимался под npm 12,
	// который заблокировал postinstall vue-demi, и сборка упала на коде,
	// который в своём образе собирался.
	tool := tools.RunCommand{
		FS: fs, NetworkPolicy: input.NetworkPolicy, AllowedNetworkHosts: input.NetworkHosts,
		Executor: a.sandboxProcessExecutor(), SandboxImage: sandbox.ExecutionImageForRecord(input.Sandbox),
		RunID: input.RunID, QuestID: input.QuestID, Authoritative: true,
		DefaultTimeout: 10 * time.Minute, MaxOutput: 256 * 1024,
	}
	result := criteriaBatchResult{AllOK: true, Criteria: make([]agent.CriterionEvidence, 0, len(input.Criteria))}
	amendments := a.criterionAmendments(context.Background(), input.QuestID)
	for _, original := range input.Criteria {
		// Разрешённая человеком поправка команды: проверка идёт ею, и
		// доказательство несёт ту команду, что действительно запускалась.
		criterion, args, mode := criterionExecution(original, amendments, input.WorkOrder, root)
		switch mode {
		case "manual":
			result.Criteria = append(result.Criteria, agent.CriterionEvidence{
				CriterionID: criterion.ID, Text: criterion.Text, Kind: criterion.Kind, Status: "needs_review",
			})
			result.NeedsReview = true
			result.Summaries = append(result.Summaries, criterion.ID+": manual acceptance pending")
			continue
		case "deferred":
			result.Criteria = append(result.Criteria, agent.CriterionEvidence{
				CriterionID: criterion.ID, Text: criterion.Text, Kind: criterion.Kind,
				Status: "unavailable", ExpectedExitCode: criterion.ExpectedExitCode,
			})
			result.NeedsReview = true
			result.Summaries = append(result.Summaries, criterion.ID+": awaiting delivered workspace")
			continue
		case "masked":
			masked, fragment, _ := maskedCriterionEvidence(criterion, args)
			result.Criteria = append(result.Criteria, masked)
			result.NeedsReview = true
			result.Summaries = append(result.Summaries, criterion.ID+": command masks its exit code ("+fragment+")")
			continue
		}
		raw, _ := json.Marshal(args)
		expected := expectedExitCode(criterion)
		result.Commands = append(result.Commands, executedCriterion{CriterionID: criterion.ID, Tool: criterion.Tool, Arguments: raw, ExpectedExitCode: expected})
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
		outcome := tool.Execute(ctx, raw)
		cancel()
		exit := toolResultExitCode(outcome)
		ce := agent.CriterionEvidence{
			CriterionID: criterion.ID, Text: criterion.Text, Kind: criterion.Kind,
			ExpectedExitCode: criterion.ExpectedExitCode,
			Check: &agent.CheckEvidence{
				Tool: criterion.Tool, Arguments: criterion.Arguments, ExitCode: &exit,
				Detail: acceptCheckDetail(outcome.Output, input.Sandbox), Status: "passed",
			},
		}
		if !outcome.OK || exit != expected {
			result.AllOK = false
			ce.Status = "failed"
			ce.Check.Status = "unresolved"
			if outcome.Error != nil {
				ce.Check.Detail = outcome.Error.Message
			}
			// Сводка называет причину, а команда и полный вывод остаются в
			// доказательстве: человек читает «почему», а не «что запускали».
			failure := fmt.Sprintf("%s: код %d вместо %d", criterion.ID, exit, expected)
			if detail := deterministicAcceptFailureDetail(outcome); detail != "" {
				failure = fmt.Sprintf("%s: %s (код %d)", criterion.ID, detail, exit)
			}
			result.Summaries = append(result.Summaries, failure)
		} else {
			ce.Status = "satisfied"
			result.Summaries = append(result.Summaries, criterion.ID+": ok")
		}
		result.Criteria = append(result.Criteria, ce)
	}
	return result, nil
}
