package companion

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/textutil"

	"golang.org/x/sync/errgroup"
)

type companionHubSnapshot struct {
	Agents       []domain.ProjectAgent
	Quests       []domain.Quest
	Flows        []domain.FlowGraph
	Executions   []domain.ExecutionInstance
	Observations []domain.IDEObservation
	ChangeSets   []domain.ChangeSet
	Connections  []domain.Connection
	Memories     []domain.MemoryRecord
	// Declined — предложения, от которых человек уже отказался. Без них
	// помощник предлагал то же самое в следующем разговоре: отказ жил в
	// базе, а в контекст не попадал.
	DeclinedQuests  []domain.QuestProposal
	DeclinedActions []domain.CompanionActionProposal
	Usage           []domain.UsageRecord
	Runs            []domain.Run
}

func (s Service) loadHubSnapshot(ctx context.Context, workspaceID string, lean bool) (companionHubSnapshot, error) {
	var snap companionHubSnapshot
	group, groupCtx := errgroup.WithContext(ctx)

	group.Go(func() error {
		var err error
		snap.Agents, err = s.Store.ListProjectAgents(groupCtx, workspaceID)
		return err
	})
	group.Go(func() error {
		var err error
		snap.Quests, err = s.Store.ListQuests(groupCtx, workspaceID)
		return err
	})
	group.Go(func() error {
		var err error
		snap.Executions, err = s.Store.ListExecutions(groupCtx, workspaceID, 30)
		return err
	})
	group.Go(func() error {
		var err error
		snap.Observations, err = s.Store.ListIDEObservations(groupCtx, workspaceID, 100)
		return err
	})
	group.Go(func() error {
		var err error
		snap.Memories, err = s.Store.ListMemories(groupCtx, workspaceID)
		return err
	})
	group.Go(func() error {
		proposals, err := s.Store.ListQuestProposals(groupCtx, workspaceID)
		if err != nil {
			return err
		}
		snap.DeclinedQuests = declinedQuestProposals(proposals)
		return nil
	})
	group.Go(func() error {
		proposals, err := s.Store.ListCompanionActionProposals(groupCtx, workspaceID)
		if err != nil {
			return err
		}
		snap.DeclinedActions = declinedCompanionActions(proposals)
		return nil
	})
	if !lean {
		group.Go(func() error {
			var err error
			snap.Flows, err = s.Store.ListFlows(groupCtx, workspaceID)
			return err
		})
		group.Go(func() error {
			var err error
			snap.ChangeSets, err = s.Store.ListChangeSets(groupCtx, workspaceID)
			return err
		})
		group.Go(func() error {
			var err error
			snap.Connections, err = s.Store.ListConnections(groupCtx)
			return err
		})
		group.Go(func() error {
			var err error
			snap.Usage, err = s.Store.ListUsageRecords(groupCtx, workspaceID, companionUsageRecordLimit)
			return err
		})
		if store, ok := s.Store.(runStore); ok {
			group.Go(func() error {
				var err error
				snap.Runs, err = store.ListRunsForWorkspace(groupCtx, workspaceID, 8)
				return err
			})
		}
	} else {
		group.Go(func() error {
			var err error
			snap.ChangeSets, err = s.Store.ListChangeSets(groupCtx, workspaceID)
			return err
		})
	}
	if err := group.Wait(); err != nil {
		return companionHubSnapshot{}, err
	}
	return snap, nil
}

func appendFocusSection(facts, sections []string, focus ChatFocus) ([]string, []string) {
	if focus.Empty() {
		return facts, sections
	}
	if label := focus.Label(); label != "" {
		facts = append(facts, "ideFocus="+label)
	}
	focusLines := make([]string, 0, 6)
	if label := focus.Label(); label != "" {
		line := "file=" + label
		if focus.Language != "" {
			line += " language=" + focus.Language
		}
		if focus.Dirty {
			line += " dirty=true"
		}
		if focus.Selection {
			line += " selection=true"
			facts = append(facts, "ideSelection=true")
		}
		if focus.Diagnostics > 0 {
			line += fmt.Sprintf(" diagnostics=%d", focus.Diagnostics)
		}
		focusLines = append(focusLines, line)
	}
	if focus.Run != "" {
		focusLines = append(focusLines, "run="+focus.Run)
		facts = append(facts, "ideRun="+focus.Run)
	}
	if focus.Debug != "" {
		focusLines = append(focusLines, "debug="+focus.Debug)
		facts = append(facts, "ideDebug="+focus.Debug)
	}
	if focus.Failure != "" {
		focusLines = append(focusLines, "failure="+focus.Failure)
		facts = append(facts, "ideFailure="+focus.Failure)
	}
	if focus.Snippet != "" {
		focusLines = append(focusLines, "snippet=\n"+focus.Snippet)
	}
	sections = appendContextSection(sections, "CURRENT IDE FOCUS (UNTRUSTED)", focusLines)
	return facts, sections
}

func filterObservationsForFocus(items []domain.IDEObservation, focus ChatFocus) []domain.IDEObservation {
	if focus.File == "" && focus.Failure == "" && focus.Run == "" {
		return items
	}
	focusPath := filepath.ToSlash(strings.TrimSpace(focus.File))
	base := filepath.Base(focusPath)
	out := make([]domain.IDEObservation, 0, min(24, len(items)))
	for _, item := range items {
		path := filepath.ToSlash(strings.TrimSpace(item.Path))
		matchFile := focusPath != "" && (path == focusPath || strings.HasSuffix(path, "/"+base) || filepath.Base(path) == base)
		matchFail := focus.Failure != "" && (strings.Contains(item.Summary, focus.Failure) || strings.Contains(item.Command, focus.Failure) || strings.Contains(item.Detail, focus.Failure))
		matchRun := focus.Run != "" && (strings.Contains(item.Summary, focus.Run) || strings.Contains(item.Command, focus.Run))
		if matchFile || matchFail || matchRun || (item.Kind == "diagnostic" && item.Level == "error" && matchFile) {
			out = append(out, item)
		}
		if len(out) >= 20 {
			break
		}
	}
	if len(out) == 0 {
		return items
	}
	return out
}

func (s Service) gatherContext(ctx context.Context, workspaceID, query string, focus ChatFocus, onProgress ProgressFn) (gatheredContext, error) {
	var facts, sections []string
	focus = sanitizeChatFocus(focus)
	lean := PreferLeanGather(query, focus)
	if lean {
		facts = append(facts, "gatherMode=lean")
	} else {
		facts = append(facts, "gatherMode=full")
	}

	if !focus.Empty() {
		emitProgress(onProgress, "focus", "running")
		facts, sections = appendFocusSection(facts, sections, focus)
		emitProgress(onProgress, "focus", "done")
	}

	emitProgress(onProgress, "roster", "running")
	snap, err := s.loadHubSnapshot(ctx, workspaceID, lean)
	if err != nil {
		return gatheredContext{}, err
	}
	facts = append(facts, fmt.Sprintf("projectAgents=%d", len(snap.Agents)))
	if lean {
		sections = appendContextSection(sections, "PROJECT AGENTS", []string{
			fmt.Sprintf("count=%d (lean: roster details omitted)", len(snap.Agents)),
		})
	} else {
		agentLines := make([]string, 0, min(8, len(snap.Agents)))
		for _, agent := range snap.Agents {
			if len(agentLines) >= 8 {
				break
			}
			success := "no completed-task history"
			if agent.TasksCompleted > 0 {
				success = fmt.Sprintf("success=%d/%d", agent.SuccessCount, agent.TasksCompleted)
			}
			agentLines = append(agentLines, fmt.Sprintf("%s | role=%s | model=%s | %s | tools=%s",
				trim(agent.Name, 100), trim(agent.RoleDescription, 180), trim(agent.PrimaryModel, 80), success,
				trim(strings.Join(agent.AllowedTools, ","), 240)))
		}
		sections = appendContextSection(sections, "PROJECT AGENTS", agentLines)
	}
	emitProgress(onProgress, "roster", "done")

	emitProgress(onProgress, "quests", "running")
	active := 0
	questLines := make([]string, 0, min(8, len(snap.Quests)))
	for _, quest := range snap.Quests {
		if quest.Status == domain.QuestActive || quest.Status == domain.QuestPaused {
			active++
		}
		if !lean && len(questLines) < 8 {
			questLines = append(questLines, fmt.Sprintf("%s | status=%s | importance=%s | objectives=%d | team=%s | flow=%s",
				trim(quest.Title, 160), quest.Status, quest.Importance, len(quest.Objectives), quest.TeamID, quest.FlowID))
		}
	}
	facts = append(facts, fmt.Sprintf("activeQuests=%d", active))
	if lean {
		sections = appendContextSection(sections, "RECENT QUESTS", []string{fmt.Sprintf("active=%d total=%d", active, len(snap.Quests))})
	} else {
		sections = appendContextSection(sections, "RECENT QUESTS", questLines)
	}
	emitProgress(onProgress, "quests", "done")

	if !lean {
		flowLines := make([]string, 0, min(8, len(snap.Flows)))
		for _, flow := range snap.Flows {
			if len(flowLines) >= 8 {
				break
			}
			flowLines = append(flowLines, fmt.Sprintf("%s | id=%s | nodes=%d | %s", trim(flow.Name, 140), flow.ID, len(flow.Nodes), trim(flow.Description, 220)))
		}
		facts = append(facts, fmt.Sprintf("availableFlows=%d", len(snap.Flows)))
		sections = appendContextSection(sections, "AVAILABLE FLOWS", flowLines)
	} else {
		facts = append(facts, "availableFlows=skipped")
	}

	activeExecutions := 0
	executionLines := make([]string, 0, min(10, len(snap.Executions)))
	for _, execution := range snap.Executions {
		if execution.Status == domain.RunRunning || execution.Status == domain.RunPaused || execution.Status == domain.RunWaiting || execution.Status == domain.RunPending {
			activeExecutions++
			if lean && len(executionLines) < 4 {
				executionLines = append(executionLines, fmt.Sprintf("%s | status=%s | agent=%s | error=%s",
					trim(execution.Task, 180), execution.Status, execution.ProjectAgentID, trim(execution.Error, 120)))
			}
		}
		if !lean && len(executionLines) < 10 {
			executionLines = append(executionLines, fmt.Sprintf("%s | status=%s | agent=%s | quest=%s | durationMs=%d | error=%s",
				trim(execution.Task, 220), execution.Status, execution.ProjectAgentID, execution.QuestID, execution.DurationMs, trim(execution.Error, 180)))
		}
	}
	facts = append(facts, fmt.Sprintf("activeExecutions=%d", activeExecutions))
	sections = appendContextSection(sections, "RECENT EXECUTIONS", executionLines)

	ideObservations := snap.Observations
	if lean {
		ideObservations = filterObservationsForFocus(snap.Observations, focus)
	}
	ideLines := make([]string, 0, min(20, len(ideObservations)))
	diagnosticErrors, diagnosticWarnings, failedCommands := 0, 0, 0
	for _, item := range snap.Observations {
		if item.Kind == "diagnostic" {
			if item.Level == "error" {
				diagnosticErrors++
			} else if item.Level == "warning" {
				diagnosticWarnings++
			}
		}
		if (item.Kind == "terminal" || item.Kind == "task") && item.ExitCode != nil && *item.ExitCode != 0 {
			failedCommands++
		}
	}
	for _, item := range ideObservations {
		if len(ideLines) >= 20 {
			break
		}
		exit := ""
		if item.ExitCode != nil {
			exit = fmt.Sprintf(" | exitCode=%d", *item.ExitCode)
		}
		ideLines = append(ideLines, fmt.Sprintf("kind=%s | level=%s | source=%s | path=%s:%d | summary=%s | command=%s%s | detail=%s",
			item.Kind, item.Level, trim(item.Source, 120), trim(item.Path, 240), item.Line, trim(item.Summary, 500),
			trim(item.Command, 500), exit, trim(item.Detail, 1400)))
	}
	facts = append(facts,
		fmt.Sprintf("ideDiagnosticErrors=%d", diagnosticErrors),
		fmt.Sprintf("ideDiagnosticWarnings=%d", diagnosticWarnings),
		fmt.Sprintf("ideFailedCommands=%d", failedCommands),
	)
	sections = appendContextSection(sections, "IDE PROBLEMS AND TERMINAL (UNTRUSTED)", ideLines)

	if !lean && len(snap.Runs) > 0 {
		runLines := make([]string, 0, 8)
		for _, run := range snap.Runs {
			if len(runLines) >= 8 {
				continue
			}
			contextTokens := 0
			for _, item := range run.ContextItems {
				if item.TokenEstimate > 0 {
					contextTokens += item.TokenEstimate
				} else {
					contextTokens += int(item.Size / 4)
				}
			}
			files := append([]string(nil), run.ChangedFiles...)
			if len(files) > 12 {
				files = append(files[:12], fmt.Sprintf("…+%d", len(run.ChangedFiles)-12))
			}
			runLines = append(runLines, fmt.Sprintf("run=%s | status=%s | provider=%s | model=%s | requests=%d | tools=%s | changedFiles=%s | contextTokens~%d",
				run.ID, run.Status, run.Provider, run.Model, run.RequestCount, strings.Join(run.ToolsUsed, ","), strings.Join(files, ","), contextTokens))
			facts = append(facts, fmt.Sprintf("runEvidence=%s status=%s changedFiles=%d tools=%s contextTokens~%d", run.ID, run.Status, len(run.ChangedFiles), strings.Join(run.ToolsUsed, ","), contextTokens))
		}
		sections = appendContextSection(sections, "RUN EVIDENCE", runLines)
	}

	pendingChanges, changedFiles := 0, 0
	changeLines := make([]string, 0, min(6, len(snap.ChangeSets)))
	for _, set := range snap.ChangeSets {
		if set.Status == domain.ChangeSetPending || set.Status == domain.ChangeSetApproved || set.Status == domain.ChangeSetConflict {
			pendingChanges++
			changedFiles += len(set.Items)
		}
		if !lean && len(changeLines) < 6 {
			paths := make([]string, 0, min(6, len(set.Items)))
			for _, item := range set.Items {
				if len(paths) >= 6 {
					break
				}
				paths = append(paths, item.Path)
			}
			changeLines = append(changeLines, fmt.Sprintf("%s | status=%s | files=%d | paths=%s", trim(set.Title, 140), set.Status, len(set.Items), strings.Join(paths, ",")))
		}
	}
	facts = append(facts, fmt.Sprintf("pendingChangeSets=%d", pendingChanges), fmt.Sprintf("pendingChangedFiles=%d", changedFiles))
	if lean {
		sections = appendContextSection(sections, "CHANGE SETS", []string{fmt.Sprintf("pending=%d files=%d", pendingChanges, changedFiles)})
	} else {
		sections = appendContextSection(sections, "CHANGE SETS", changeLines)
	}

	if !lean {
		connected := 0
		connectionLines := make([]string, 0, min(8, len(snap.Connections)))
		for _, conn := range snap.Connections {
			if conn.Status == domain.ConnectionConnected {
				connected++
			}
			if len(connectionLines) < 8 {
				connectionLines = append(connectionLines, fmt.Sprintf("%s | provider=%s | preset=%s | status=%s", trim(conn.DisplayName, 120), conn.Provider, conn.PresetID, conn.Status))
			}
		}
		facts = append(facts, fmt.Sprintf("connectedProviders=%d", connected))
		sections = appendContextSection(sections, "PROVIDER CONNECTIONS", connectionLines)
	}

	emitProgress(onProgress, "memory", "running")
	memoryLimit := companionMemoryContextLimit
	if lean {
		memoryLimit = companionMemoryContextLimit / 2
	}
	selectedMemories := relevantMemories(snap.Memories, query, memoryLimit)
	memoryLines := make([]string, 0, len(selectedMemories))
	for _, memory := range selectedMemories {
		memoryLines = append(memoryLines, fmt.Sprintf("kind=%s | pinned=%t | confidence=%.2f | source=%s | content=%s",
			memory.Kind, memory.Pinned, memory.Confidence, trim(memory.Source, 140), trim(memory.Content, 700)))
	}
	facts = append(facts, fmt.Sprintf("memories=%d selected=%d", len(snap.Memories), len(selectedMemories)))
	// Отказ — тоже факт о проекте, и притом дорогой: карточку сначала прочитали,
	// потом отклонили. Не показать его значит предложить то же самое снова.
	if declined := declinedProposalLines(snap); len(declined) > 0 {
		facts = append(facts, fmt.Sprintf("declinedProposals=%d", len(declined)))
		sections = appendContextSection(sections, "DECLINED EARLIER (do not offer the same again unless the person asks)", declined)
	}
	sections = appendContextSection(sections, "RELEVANT MEMORY", memoryLines)
	emitProgress(onProgress, "memory", "done")

	var usageSummary usageAnalysis
	if !lean {
		usageSummary = buildUsageAnalysis(snap.Usage, snap.Agents, time.Now().UTC())
		usageLines := []string{
			fmt.Sprintf("records=%d | bounded=%t", usageSummary.Records, usageSummary.Bounded),
			fmt.Sprintf("currentMonth: records=%d | tokens=%d | knownCostCents=%d | unknownCostRecords=%d | failedOutcomes=%d",
				usageSummary.CurrentMonth.Records, usageSummary.CurrentMonth.Tokens, usageSummary.CurrentMonth.KnownCostCents,
				usageSummary.CurrentMonth.UnknownCostRecords, usageSummary.CurrentMonth.FailedOutcomes),
			fmt.Sprintf("previousMonth: records=%d | tokens=%d | knownCostCents=%d | unknownCostRecords=%d | failedOutcomes=%d",
				usageSummary.PreviousMonth.Records, usageSummary.PreviousMonth.Tokens, usageSummary.PreviousMonth.KnownCostCents,
				usageSummary.PreviousMonth.UnknownCostRecords, usageSummary.PreviousMonth.FailedOutcomes),
		}
		for _, item := range usageSummary.TopModels {
			usageLines = append(usageLines, fmt.Sprintf("topModel=%s | records=%d | tokens=%d", item.Name, item.Records, item.Tokens))
		}
		for _, item := range usageSummary.TopAgents {
			usageLines = append(usageLines, fmt.Sprintf("topAgent=%s | records=%d | tokens=%d", item.Name, item.Records, item.Tokens))
		}
		facts = append(facts,
			fmt.Sprintf("usageRecords=%d bounded=%t", usageSummary.Records, usageSummary.Bounded),
			fmt.Sprintf("usageCurrentMonthRecords=%d tokens=%d knownCostCents=%d unknownCostRecords=%d", usageSummary.CurrentMonth.Records, usageSummary.CurrentMonth.Tokens, usageSummary.CurrentMonth.KnownCostCents, usageSummary.CurrentMonth.UnknownCostRecords),
			fmt.Sprintf("usagePreviousMonthRecords=%d tokens=%d knownCostCents=%d unknownCostRecords=%d", usageSummary.PreviousMonth.Records, usageSummary.PreviousMonth.Tokens, usageSummary.PreviousMonth.KnownCostCents, usageSummary.PreviousMonth.UnknownCostRecords),
		)
		if len(usageSummary.TopModels) > 0 {
			facts = append(facts, fmt.Sprintf("usageTopModel=%s tokens=%d", usageSummary.TopModels[0].Name, usageSummary.TopModels[0].Tokens))
		}
		if len(usageSummary.TopAgents) > 0 {
			facts = append(facts, fmt.Sprintf("usageTopAgent=%s tokens=%d", usageSummary.TopAgents[0].Name, usageSummary.TopAgents[0].Tokens))
		}
		sections = appendContextSection(sections, "RECENT USAGE", usageLines)
	}

	if s.ProjectContext != nil {
		emitProgress(onProgress, "index", "running")
		timeout := 12 * time.Second
		if lean {
			timeout = 4 * time.Second
		}
		indexCtx, cancel := context.WithTimeout(ctx, timeout)
		skipMap := lean && strings.TrimSpace(focus.Snippet) != ""
		if !skipMap {
			projectMap, mapErr := s.ProjectContext.ProjectMap(indexCtx, 80)
			if mapErr == nil {
				languages := make([]string, 0, len(projectMap.FilesByLanguage))
				for language, count := range projectMap.FilesByLanguage {
					languages = append(languages, fmt.Sprintf("%s=%d", language, count))
				}
				sort.Strings(languages)
				facts = append(facts, fmt.Sprintf("projectIndexFiles=%d symbols=%d", projectMap.Status.Files, projectMap.Status.Symbols))
				// Индекс упирается в лимиты на большом репозитории и остаётся
				// частичным. Без этого признака отсутствие файла в индексе
				// читается как отсутствие файла в проекте — и помощник уверенно
				// отвечает «такого нет».
				// Индекс — снимок, а не зеркало: между сборкой и вопросом человек
				// правит файлы. Возраст снимка решает, можно ли по нему говорить
				// о том, что в файле «сейчас».
				if built := projectMap.Status.BuiltAt; !built.IsZero() {
					facts = append(facts, fmt.Sprintf("projectIndexAgeMinutes=%d", int(time.Since(built).Minutes())))
				}
				if projectMap.Status.Partial {
					reason := projectMap.Status.LimitReason
					if reason == "" {
						reason = "unknown"
					}
					facts = append(facts, "projectIndexPartial="+reason)
				}
				if !lean {
					sections = appendContextSection(sections, "PROJECT MAP", []string{
						"languages=" + strings.Join(languages, ", "),
						"topDirectories=" + strings.Join(projectMap.TopDirectories, ", "),
						"symbols=" + strings.Join(projectMap.Symbols, ", "),
					})
				}
				emitProgress(onProgress, "index", "done")
			} else {
				facts = append(facts, "projectIndex=unavailable:"+trim(mapErr.Error(), 160))
				emitProgress(onProgress, "index", "error")
				observability.From(ctx).Warn("companion index unavailable", "error", security.Redact(mapErr.Error()))
			}
		} else {
			facts = append(facts, "projectIndex=skipped:focusSnippet")
			emitProgress(onProgress, "index", "done")
		}
		searchQuery := focusSearchQuery(query, focus)
		if len(textutil.Tokens(searchQuery)) > 0 {
			emitProgress(onProgress, "search", "running")
			chunks, chars := 4, 6000
			if lean {
				chunks, chars = 3, 3500
			}
			search, searchErr := s.ProjectContext.SearchContextWithRelations(indexCtx, searchQuery, chunks, chars, !lean)
			if searchErr == nil {
				kept, dropped := relevantCodeChunks(search.Chunks, len(textutil.Tokens(searchQuery)))
				if dropped > 0 {
					facts = append(facts, fmt.Sprintf("codeContextDropped=%d", dropped))
				}
				codeLines := make([]string, 0, len(kept)+1)
				paths := make([]string, 0, len(kept))
				for _, chunk := range kept {
					paths = append(paths, fmt.Sprintf("%s:%d-%d", chunk.Path, chunk.StartLine, chunk.EndLine))
					codeLines = append(codeLines, fmt.Sprintf("SOURCE %s:%d-%d (score=%d)\n%s", chunk.Path, chunk.StartLine, chunk.EndLine, chunk.Score, trim(chunk.Content, 2200)))
				}
				if !lean && len(search.RelatedFiles) > 0 {
					related := make([]string, 0, min(12, len(search.RelatedFiles)))
					for _, file := range search.RelatedFiles {
						if len(related) >= 12 {
							break
						}
						related = append(related, file.Path+" ("+file.Relation+")")
					}
					codeLines = append(codeLines, "related="+strings.Join(related, ", "))
				}
				if len(paths) > 0 {
					facts = append(facts, "codeContext="+strings.Join(paths, ","))
					sections = appendContextSection(sections, "RELEVANT CODE (UNTRUSTED)", codeLines)
				}
				emitProgress(onProgress, "search", "done")
			} else {
				emitProgress(onProgress, "search", "error")
				observability.From(ctx).Warn("companion index search failed", "query", observability.Snippet(searchQuery, 120), "error", security.Redact(searchErr.Error()))
			}
		}
		cancel()
	}

	for index := range facts {
		facts[index] = security.Redact(facts[index])
	}
	promptLimit := 24 * 1024
	if lean {
		promptLimit = 12 * 1024
	}
	prompt := security.Redact(trim(strings.Join(sections, "\n\n"), promptLimit))
	return gatheredContext{Facts: facts, Prompt: prompt, Usage: usageSummary, IDE: snap.Observations, Focus: focus}, nil
}

// Отклонённые предложения: последние отказы человека по этому проекту.
//
// Показываются только заголовки и вид: карточку человек уже видел, а модели
// нужно ровно одно — не предлагать то же снова. Три штуки, потому что контекст
// не резиновый, а старый отказ значит меньше свежего.
func declinedQuestProposals(proposals []domain.QuestProposal) []domain.QuestProposal {
	declined := make([]domain.QuestProposal, 0, 3)
	for index := len(proposals) - 1; index >= 0 && len(declined) < 3; index-- {
		if proposals[index].Status == "ignored" {
			declined = append(declined, proposals[index])
		}
	}
	return declined
}

func declinedCompanionActions(proposals []domain.CompanionActionProposal) []domain.CompanionActionProposal {
	declined := make([]domain.CompanionActionProposal, 0, 3)
	for index := len(proposals) - 1; index >= 0 && len(declined) < 3; index-- {
		if proposals[index].Status == "ignored" {
			declined = append(declined, proposals[index])
		}
	}
	return declined
}

func declinedProposalLines(snap companionHubSnapshot) []string {
	lines := make([]string, 0, len(snap.DeclinedQuests)+len(snap.DeclinedActions))
	for _, proposal := range snap.DeclinedQuests {
		lines = append(lines, "quest: "+trim(strings.Join(strings.Fields(proposal.Title), " "), 160))
	}
	for _, proposal := range snap.DeclinedActions {
		lines = append(lines, string(proposal.Kind)+": "+trim(strings.Join(strings.Fields(proposal.Title), " "), 160))
	}
	return lines
}
