// Главный цикл прогона: ход модели, разбор плана, вызовы инструментов.
//
// Здесь решается, что делать с ответом модели — принять финал, выполнить
// инструменты, попросить исправиться или остановиться.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/executors"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/policy"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/security"
	workbenchtools "local-agent-workbench/internal/tools"
)

func (e *Engine) executeWithCheckpoint(ctx context.Context, active *activeRun, profile domain.AgentProfile, customTools []domain.CustomTool, model providers.Model, registry *workbenchtools.Registry, patches *workbenchtools.PatchManager, restored *domain.RunCheckpoint) {
	defer func() {
		if rec := recover(); rec != nil {
			e.fail(active, fmt.Errorf("agent panic: %v", rec))
		}
		active.cancel()
		finalRun := e.snapshot(active)
		if active.onFinished != nil {
			active.onFinished(finalRun)
		}
		e.mu.Lock()
		delete(e.active, finalRun.ID)
		e.mu.Unlock()
		close(active.finalized)
	}()
	run := e.snapshot(active)
	toolDefinitions := withoutUnusableGitTools(ctx, registry.Definitions(policy.ProfileGrants(profile).ToolNames()), patches)
	history := newConversationHistory(BuildStableMessages(profile, run.ContextItems, run.Task, customTools))
	// Предел вывода считает не только ответ: размышление тратит тот же бюджет и
	// тратит его первым, так что ход, которому не хватило места, заканчивается
	// ничем — finish_reason=length и ноль content. Пол ставится здесь, а не
	// только при рождении исполнителя: профили, созданные раньше, живут с
	// прежним числом, и на живом квесте 21 сентября 2026 теряли ходы именно они.
	// Половина окна — граница и здесь: бюджет входа считается разницей окна и
	// вывода, и поднятый пол у узкого окна оставил бы разговору ноль. Профиль
	// с окном в пару тысяч токенов остаётся со своим числом: размышлять там
	// всё равно негде, а прогон обязан начаться.
	//
	// На бесплатном рантайме думающая модель сразу получает потолок роста
	// (domain.InitialOutputBudget): иначе первый же ход тратил минуты на
	// размышление до 8192 и начинался заново.
	if budget := domain.InitialOutputBudget(profile.MaxOutputTokens, profile.Model, profile.ReasoningEffort, profile.Provider, profile.ProviderPreset, effectiveContextWindowTokens(profile)); budget > profile.MaxOutputTokens {
		profile.MaxOutputTokens = budget
	}
	inputBudgetTokens := ModelInputBudgetTokens(profile)
	if active.steps == nil {
		active.steps = newStepBudget(profile.MaxSteps, active.taskBrief)
	}
	steps := active.steps
	toolOutputBytes := 0
	lastToolPlan := ""
	identicalToolPlanCount := 0
	toolPlanRecoveries := 0
	reasoningBudgetRecoveries := 0
	emptyResponseRecoveries := 0
	transientModelRetries := 0
	toolOutputBudgetNoticed := false
	forceDisableThinking := false
	// «Размышление» гасится полем сверх спецификации OpenAI, и официальный
	// endpoint отвечает на него 400. Аварийный повтор там не спасает, а вредит:
	// признак живёт до конца прогона, поэтому один ход, потративший вывод на
	// размышление, отравил бы ошибкой формата все последующие запросы. У чужого
	// рантайма остаётся честный путь — подсказка о бюджете и заявленный fallback.
	canDisableThinking := domain.ShouldSuppressThinking(profile.Provider, profile.ProviderPreset)
	completedToolCalls := make(map[string]struct{})
	workspaceRevision := 0
	completion := newCompletionTracker(profile, run.Task, customTools, active.taskBrief)
	if instruction := completion.ContractInstructions(); instruction != "" && len(history.stable) > 0 {
		history.stable[0].Content += "\n" + instruction
	}
	observations := newObservationTracker(run.ContextItems)
	completionPolicy := DescribeCompletionPolicy(profile, run.Task, customTools)
	completionRevisions := 0
	maxCompletionRevisions := completionPolicy.CorrectionEpisodes
	if maxCompletionRevisions <= 0 {
		maxCompletionRevisions = 2
	}
	currentModel := profile.Model
	remainingFallbacks := append([]string(nil), profile.FallbackModels...)
	modelSelector := connections.ClassifiedSelector{}
	startStep := 1
	if restored != nil {
		var restoreErr error
		history, restoreErr = unmarshalConversationHistory(restored.HistoryJSON)
		if restoreErr != nil {
			e.fail(active, fmt.Errorf("restore conversation checkpoint: %w", restoreErr))
			return
		}
		completion, restoreErr = unmarshalCompletionTracker(restored.CompletionJSON)
		if restoreErr != nil {
			e.fail(active, fmt.Errorf("restore completion checkpoint: %w", restoreErr))
			return
		}
		observations, restoreErr = unmarshalObservationTracker(restored.ObservationsJSON)
		if restoreErr != nil {
			e.fail(active, fmt.Errorf("restore observation checkpoint: %w", restoreErr))
			return
		}
		completedToolCalls = completedToolCallSet(restored.CompletedToolCalls)
		workspaceRevision = restored.WorkspaceRevision
		active.workspaceRevision = restored.WorkspaceRevision
		active.checkpointSeq = restored.Seq
		active.clock.restore(restored.ActiveElapsedMs, restored.ActiveTimeExtensions, restored.ActiveSecondsBudget)
		steps.restore(restored.StepLimit, restored.StepExtensions, restored.StepWrapUp)
		toolOutputBytes = restored.ToolOutputBytes
		if len(restored.TeamInboxSeen) > 0 {
			active.teamInboxSeen = make(map[string]bool, len(restored.TeamInboxSeen))
			for _, id := range restored.TeamInboxSeen {
				active.teamInboxSeen[id] = true
			}
		}
		lastToolPlan = restored.LastToolPlan
		identicalToolPlanCount = restored.IdenticalToolPlans
		toolPlanRecoveries = restored.ToolPlanRecoveries
		completionRevisions = restored.CompletionRevisions
		if restored.CurrentModel != "" {
			currentModel = restored.CurrentModel
		}
		remainingFallbacks = append([]string(nil), restored.RemainingFallbacks...)
		if restored.NextStep > 1 {
			startStep = restored.NextStep
		}
		e.update(active, func(r *domain.Run) {
			r.Step = restored.Step
			r.RequestCount = restored.RequestCount
			r.ChangedFiles = append([]string(nil), restored.ChangedFiles...)
			r.ToolsUsed = append([]string(nil), restored.ToolsUsed...)
			r.Model = currentModel
			r.Status = domain.RunRunning
			r.Controller.PauseReason = ""
			r.Controller.Resumable = true
		})
		if err := e.saveRun(e.snapshot(active)); err != nil {
			e.fail(active, fmt.Errorf("persist resumed run: %w", err))
			return
		}
	} else if err := e.persistRoundCheckpoint(active, history, completion, observations, completedToolCalls, currentModel, remainingFallbacks, 1, completionRevisions, identicalToolPlanCount, toolPlanRecoveries, toolOutputBytes, lastToolPlan, "", ""); err != nil {
		e.fail(active, fmt.Errorf("persist initial run checkpoint: %w", err))
		return
	}
	if executors.KindForProvider(profile.Provider) != executors.KindPoint {
		if restored != nil {
			e.fail(active, fmt.Errorf("%w: unknown_outcome: interrupted CLI process cannot be safely continued", errToolJournalIntegrity))
			return
		}
		e.executeHeadlessCLI(ctx, active, profile, registry, patches, history, completion, observations, completedToolCalls)
		return
	}
	active.clock.start()
	for step := startStep; ; step++ {
		if err := ctx.Err(); err != nil {
			e.finishContext(active, err)
			return
		}
		if active.clock.exhausted() {
			e.requestPause(active, domain.PauseReasonActiveTimeExhausted)
		}
		if err := e.waitAtCheckpoint(ctx, active); err != nil {
			e.finishContext(active, err)
			return
		}
		if active.clock.exhausted() {
			// Still exhausted after resume without extend — pause again.
			e.requestPause(active, domain.PauseReasonActiveTimeExhausted)
			if err := e.waitAtCheckpoint(ctx, active); err != nil {
				e.finishContext(active, err)
				return
			}
			if active.clock.exhausted() {
				e.fail(active, errors.New("active time budget exhausted"))
				return
			}
		}
		if step > steps.currentLimit() && !steps.inWrapUp() && !e.waitForStepBudget(ctx, active, step) {
			return
		}
		// Последний ход после лимита идёт без инструментов: модель обязана
		// ответить итогом, а не начать ещё одну проверку.
		wrapUp := steps.inWrapUp()
		requestTools := toolDefinitions
		if wrapUp {
			requestTools = nil
		}
		e.applyPendingAmendments(active, history, profile, customTools)
		e.update(active, func(r *domain.Run) { r.Step = step; r.RequestCount++ })
		run = e.snapshot(active)
		if err := e.saveRun(run); err != nil {
			e.fail(active, fmt.Errorf("persist run state: %w", err))
			return
		}
		messages, compaction, prepareErr := history.Prepare(requestTools, inputBudgetTokens)
		if prepareErr != nil {
			e.fail(active, prepareErr)
			return
		}
		for _, key := range compaction.ReleasedReplayableKeys {
			delete(completedToolCalls, key)
			observations.Release(key)
		}
		if compaction.Compacted() {
			e.publishOrLog(ctx, run, domain.EventContextCompacted, "agent", map[string]any{
				"beforeTokens": compaction.BeforeTokens, "afterTokens": compaction.AfterTokens, "budgetTokens": compaction.BudgetTokens,
				"releasedTokens": compaction.ReleasedTokens, "removedRounds": compaction.RemovedRounds,
				"reducedToolMessages": compaction.ReducedToolMessages, "memoryEntries": compaction.MemoryEntries,
			})
		}
		// Провайдер один раз уже отказал «не влезло в окно»: окно профиля
		// больше настоящего, и разговор сжимается под меньший бюджет.
		contextOverflowRetried := false
		var content strings.Builder
		var calls []providers.ToolCall
		// Блоки размышления живут ровно один ход: провайдер требует вернуть их
		// вместе с ответом на его же вызов инструмента и не принимает чужие.
		var reasoning []providers.ReasoningBlock
		disableThinking := forceDisableThinking
		triedDisableThinking := forceDisableThinking
		reasoningRecovered := false
		for {
			content.Reset()
			calls = nil
			reasoning = nil
			estimatedInputTokens := int64(compaction.AfterTokens)
			if estimatedInputTokens <= 0 {
				estimatedInputTokens = int64(EstimateModelInputTokens(messages, requestTools))
			}
			reservationID := ""
			if e.budgets != nil {
				reservationID = active.takeInitialBudgetReservation()
			}
			if e.budgets != nil && reservationID == "" {
				var reserveErr error
				reservationID, reserveErr = e.budgets.ReserveModelBudget(ctx, ModelBudgetRequest{
					WorkspaceID: run.WorkspaceID, QuestID: active.correlation.QuestID, ExecutionID: active.correlation.ExecutionID,
					RunID: run.ID, Provider: profile.Provider, ProviderPreset: profile.ProviderPreset, Model: currentModel,
					EstimatedInputTokens: estimatedInputTokens, MaxOutputTokens: int64(profile.MaxOutputTokens),
				})
				if reserveErr != nil {
					e.fail(active, fmt.Errorf("reserve model budget: %w", reserveErr))
					return
				}
			}
			e.publishOrLog(ctx, e.snapshot(active), domain.EventModelRequested, "agent", map[string]any{"model": currentModel, "messageCount": len(messages), "estimatedInputTokens": estimatedInputTokens, "inputBudgetTokens": inputBudgetTokens, "contextWindowTokens": profile.ContextWindowTokens, "budgetReservationId": reservationID})
			log := observability.From(ctx)
			log.Info("agent model request",
				"run_id", run.ID,
				"step", step,
				"model", currentModel,
				"message_count", len(messages),
				"tools", len(requestTools),
				"estimated_input_tokens", compaction.AfterTokens,
				"input_budget_tokens", inputBudgetTokens,
			)
			var usageInput, usageOutput int64
			usageReported := false
			// Дельты потока копятся и пишутся не чаще раза в streamFlushInterval
			// или по streamFlushBytes: прежде каждый токен был отдельной записью в
			// SQLite с одним соединением, общим для всех прогонов и интерфейса.
			var streamed strings.Builder
			lastStreamFlush := time.Now()
			flushStream := func() {
				if streamed.Len() == 0 {
					return
				}
				e.publishOrLog(ctx, e.snapshot(active), domain.EventModelStreamed, "model", map[string]any{"delta": streamed.String()})
				streamed.Reset()
				lastStreamFlush = time.Now()
			}
			effort := profile.ReasoningEffort
			if disableThinking {
				effort = ""
			}
			err := model.Stream(ctx, providers.ModelRequest{Model: currentModel, Messages: messages, Tools: requestTools, Temperature: profile.Temperature, MaxOutputTokens: profile.MaxOutputTokens, ContextWindowTokens: effectiveContextWindowTokens(profile), ReasoningEffort: effort, DisableThinking: disableThinking}, func(event providers.ModelEvent) error {
				switch event.Kind {
				case providers.EventTextDelta:
					if content.Len()+len(event.Delta) > maxModelResponseBytes {
						return errors.New("model response exceeds 2 MiB")
					}
					content.WriteString(event.Delta)
					// Поток ответа — показ для человека; итог хода пишет
					// model.responded. Сбой записи дельты прогон не роняет.
					streamed.WriteString(event.Delta)
					if streamed.Len() >= streamFlushBytes || time.Since(lastStreamFlush) >= streamFlushInterval {
						flushStream()
					}
				case providers.EventToolCall:
					if event.ToolCall != nil {
						calls = append(calls, *event.ToolCall)
					}
				case providers.EventReasoning:
					// Наружу не публикуем: это внутренний ход модели, а не ответ
					// человеку. Он нужен только следующему запросу к провайдеру.
					if event.Reasoning != nil {
						reasoning = append(reasoning, *event.Reasoning)
					}
				case providers.EventUsage:
					usageReported = true
					if int64(event.InputTokens) > usageInput {
						usageInput = int64(event.InputTokens)
					}
					if int64(event.OutputTokens) > usageOutput {
						usageOutput = int64(event.OutputTokens)
					}
					return e.publish(ctx, e.snapshot(active), domain.EventModelUsage, "model", map[string]any{"budgetReservationId": reservationID, "usage": map[string]int{"inputTokens": event.InputTokens, "outputTokens": event.OutputTokens}})
				case providers.EventRetry:
					// Провайдер начинает запрос заново: всё, что пришло в этой
					// попытке, выбрасывается. Прежде текст оборванной попытки
					// оставался в ответе, и повтор его задваивал.
					content.Reset()
					calls = nil
					reasoning = nil
					streamed.Reset()
					return e.publish(ctx, e.snapshot(active), domain.EventModelRetrying, "provider", map[string]any{"attempt": event.Attempt, "delayMs": event.DelayMs, "message": event.Message, "model": currentModel, "restart": true})
				}
				return nil
			})
			flushStream()
			if reservationID != "" {
				reconcileErr := e.budgets.ReconcileModelBudget(context.Background(), ModelBudgetSettlement{
					ReservationID: reservationID, WorkspaceID: run.WorkspaceID, Provider: profile.Provider, Model: currentModel,
					InputTokens: usageInput, OutputTokens: usageOutput, UsageReported: usageReported,
				})
				if reconcileErr != nil {
					e.fail(active, fmt.Errorf("reconcile model budget: %w", reconcileErr))
					return
				}
			}
			if err == nil {
				log.Info("agent model responded", "run_id", run.ID, "step", step, "model", currentModel, "tool_calls", len(calls), "content_bytes", content.Len(), "tools", toolCallNames(calls))
				// Счётчик обрывов — подряд, а не за весь прогон: три случайных
				// обрыва за два часа работы не повод её терять.
				transientModelRetries = 0
				break
			}
			log.Warn("agent model error", "run_id", run.ID, "step", step, "model", currentModel, "error", security.Redact(err.Error()))
			if ctx.Err() != nil {
				e.finishContext(active, ctx.Err())
				return
			}
			if providers.IsContextOverflowError(err) && !contextOverflowRetried {
				contextOverflowRetried = true
				reduced := inputBudgetTokens * 3 / 4
				compactedMessages, compactedInfo, compactErr := history.Prepare(requestTools, reduced)
				if compactErr == nil {
					inputBudgetTokens = reduced
					messages, compaction = compactedMessages, compactedInfo
					e.publishOrLog(ctx, e.snapshot(active), domain.EventContextCompacted, "agent", map[string]any{
						"reason": "provider_context_overflow", "beforeTokens": compaction.BeforeTokens, "afterTokens": compaction.AfterTokens,
						"budgetTokens": compaction.BudgetTokens, "releasedTokens": compaction.ReleasedTokens, "removedRounds": compaction.RemovedRounds,
					})
					for _, key := range compaction.ReleasedReplayableKeys {
						delete(completedToolCalls, key)
						observations.Release(key)
					}
					continue
				}
			}
			// Same-model retry: kill thinking before burning a declared fallback.
			if providers.IsTruncatedReasoningError(err) && canDisableThinking && !triedDisableThinking {
				triedDisableThinking = true
				disableThinking = true
				forceDisableThinking = true
				e.publishOrLog(ctx, e.snapshot(active), domain.EventModelRetrying, "provider", map[string]any{
					"disableThinking": true, "model": currentModel, "message": err.Error(),
				})
				continue
			}
			nextModel, useFallback := modelSelector.Select(currentModel, remainingFallbacks, err)
			if active.taskBrief != nil && active.taskBrief.Mode == domain.TaskModeProject && (useFallback || len(remainingFallbacks) > 0) {
				// Project freezes the model for the assignment, but still allows
				// a declared fallback after a transient empty stream or a turn
				// that spent the whole output budget on reasoning.
				if !providers.IsTransientProviderError(err) {
					e.fail(active, fmt.Errorf("project task freezes model %q for the assignment; provider error: %w", currentModel, err))
					return
				}
			}
			if !useFallback {
				if providers.IsTruncatedReasoningError(err) && reasoningBudgetRecoveries < maxReasoningBudgetRecoveries {
					reasoningBudgetRecoveries++
					// Сначала место для ответа, потом тишина — порядок тот же,
					// что у Мастера (streamMasterModel). Прежде здесь росла
					// только подсказка, а предел оставался прежним: на рантайме,
					// где размышление не гасят, повтор упирался в тот же потолок
					// и эпизоды восстановления сгорали впустую.
					//
					// Рост режется половиной окна, а не отменяется ею: вывод
					// больше половины провайдер отвергнет, но и отказ от роста
					// вернул бы прежний тупик. Остаток окна разговор ужимает сам.
					grown := domain.GrowThinkingOutputBudget(profile.MaxOutputTokens, currentModel)
					if half := effectiveContextWindowTokens(profile) / 2; half > 0 && grown > half {
						grown = half
					}
					if grown > profile.MaxOutputTokens {
						profile.MaxOutputTokens = grown
						inputBudgetTokens = ModelInputBudgetTokens(profile)
					}
					forceDisableThinking = canDisableThinking
					feedback := providers.Message{Role: "user", Content: reasoningBudgetRecoveryFeedback(reasoningBudgetRecoveries, maxReasoningBudgetRecoveries)}
					history.AppendRound(conversationRound{
						Step:      step,
						Assistant: providers.Message{Role: "assistant", Content: "I spent the output budget on reasoning without calling a tool."},
						Followup:  &feedback,
					})
					e.publishOrLog(ctx, e.snapshot(active), domain.EventAgentGuardrail, "agent", map[string]any{
						"code": "reasoning_budget_recovery", "recoveryEpisode": reasoningBudgetRecoveries,
						"maxRecoveryEpisodes": maxReasoningBudgetRecoveries, "model": currentModel,
					})
					reasoningRecovered = true
					break
				}
				// Обрыв потока посреди ответа — не отказ прогона. HTTP-слой
				// повторяет попытку установить обращение, но поток, умерший на
				// середине, он уже не спасает: ошибка приходит сюда, и прежде
				// первая же такая уносила всю работу, если у профиля не было
				// объявленной запасной модели. У большинства профилей её нет.
				if providers.IsTransientProviderError(err) {
					// Размышление, съевшее предел вывода, уже прошло свои
					// восстановления выше; долгое ожидание его не лечит.
					truncated := providers.IsTruncatedReasoningError(err)
					retryLimit := maxTransientModelRetries
					if truncated {
						retryLimit = maxTruncatedReasoningRetries
					}
					if transientModelRetries < retryLimit {
						transientModelRetries++
						delay := transientDelay(transientModelRetries)
						e.publishOrLog(ctx, e.snapshot(active), domain.EventModelRetrying, "provider", map[string]any{
							"transientRetry": transientModelRetries, "maxTransientRetries": maxTransientModelRetries,
							"attempt": transientModelRetries + 1, "delayMs": delay.Milliseconds(),
							"model": currentModel, "message": err.Error(),
						})
						select {
						case <-ctx.Done():
							e.finishContext(active, ctx.Err())
							return
						case <-time.After(delay):
						}
						continue
					}
					if truncated {
						e.fail(active, err)
						return
					}
					// Провайдер молчит несколько минут подряд. Прогон не падает:
					// работа до этого хода цела в контрольной точке, и после
					// паузы тот же ход повторяется — сам через несколько минут
					// или раньше, если человек нажмёт «Продолжить».
					transientModelRetries = 0
					e.publishOrLog(ctx, e.snapshot(active), domain.EventAgentGuardrail, "agent", map[string]any{
						"code": "provider_unavailable", "model": currentModel, "message": err.Error(),
						"autoResumeMs": providerUnavailableAutoResume.Milliseconds(),
					})
					e.requestPauseFor(active, domain.PauseReasonProviderUnavailable, providerUnavailableAutoResume)
					if waitErr := e.waitAtCheckpoint(ctx, active); waitErr != nil {
						e.finishContext(active, waitErr)
						return
					}
					continue
				}
				e.fail(active, err)
				return
			}
			remainingFallbacks = remainingFallbacks[1:]
			previousModel := currentModel
			currentModel = nextModel
			triedDisableThinking = forceDisableThinking
			disableThinking = forceDisableThinking
			e.update(active, func(r *domain.Run) {
				r.Model = currentModel
				r.ConfigurationSnapshot = r.ConfigurationSnapshot.WithEffectiveModel(currentModel)
			})
			run = e.snapshot(active)
			if saveErr := e.saveRun(run); saveErr != nil {
				e.fail(active, fmt.Errorf("persist fallback model: %w", saveErr))
				return
			}
			e.publishOrLog(ctx, run, domain.EventModelRetrying, "provider", map[string]any{
				"fallback": true, "fromModel": previousModel, "toModel": currentModel,
				"configurationDigest": run.ConfigurationSnapshot.ConfigurationDigest,
				"message":             err.Error(),
			})
		}
		if reasoningRecovered {
			continue
		}
		assistantText := content.String()
		if !wrapUp {
			calls, assistantText = e.recoverToolCalls(ctx, active, step, requestTools, calls, assistantText)
		}
		if wrapUp {
			// Инструментов в этом ходе нет; случайный вызов не исполняется, а
			// пустой ответ не повод для нового круга — ход последний.
			calls = nil
			if strings.TrimSpace(assistantText) == "" {
				assistantText = "Turn limit reached; the model returned no final summary."
			}
		}
		if strings.TrimSpace(assistantText) == "" && len(calls) == 0 {
			// Пустой ход — не отказ прогона, а несостоявшийся ход. Прежде он
			// уносил всю работу немедленно и без единого повтора: модель,
			// закончившая размышление ровно на границе бюджета, стоила
			// человеку всего, что агент успел сделать. Лечится он тем же
			// способом, что и два соседних затыка, — нуджем и новым ходом.
			if emptyResponseRecoveries < maxEmptyResponseRecoveries {
				emptyResponseRecoveries++
				feedback := providers.Message{Role: "user", Content: emptyResponseRecoveryFeedback(emptyResponseRecoveries, maxEmptyResponseRecoveries)}
				history.AppendRound(conversationRound{
					Step:      step,
					Assistant: providers.Message{Role: "assistant", Content: "I returned nothing: no answer and no tool call."},
					Followup:  &feedback,
				})
				e.publishOrLog(ctx, e.snapshot(active), domain.EventAgentGuardrail, "agent", map[string]any{
					"code": "empty_response_recovery", "recoveryEpisode": emptyResponseRecoveries,
					"maxRecoveryEpisodes": maxEmptyResponseRecoveries, "model": currentModel,
				})
				continue
			}
			e.fail(active, errors.New("model returned an empty response without tool calls"))
			return
		}
		e.publishOrLog(ctx, e.snapshot(active), domain.EventModelResponded, "model", map[string]any{"content": assistantText, "toolCalls": calls})
		if len(calls) == 0 {
			currentRun := e.snapshot(active)
			requirements := completion.Missing(workspaceRevision, currentRun.ChangedFiles)
			if len(requirements) > 0 {
				outOfSteps := wrapUp
				if !outOfSteps && step >= steps.currentLimit() {
					outOfSteps = !e.autoExtendSteps(ctx, active, step)
				}
				// Без брифа исчерпание остаётся провалом, как прежде; с брифом
				// прогон встаёт на паузу, и человек может дать ещё ходов.
				fatal := completionRevisions >= maxCompletionRevisions || (outOfSteps && active.taskBrief == nil)
				status := "revision_required"
				if fatal {
					status = "rejected"
				}
				e.publishOrLog(ctx, currentRun, domain.EventCompletionChecked, "agent", withCompletionCheckKind(active, map[string]any{
					"status": status, "requirements": requirements, "workspaceRevision": workspaceRevision, "evidence": completion.Evidence(workspaceRevision),
					"changedFiles": currentRun.ChangedFiles, "commandAttempts": completion.commandAttempts,
					"successfulVerificationRevision": completion.successfulVerificationRevision,
					"correctionEpisode":              completionRevisions + 1,
					"maxCorrectionEpisodes":          maxCompletionRevisions,
				}))
				if fatal {
					e.fail(active, completionRequirementError(requirements))
					return
				}
				feedback := providers.Message{Role: "user", Content: completion.Feedback(requirements, workspaceRevision, currentRun.ChangedFiles)}
				if outOfSteps {
					feedback.Content = joinFeedback(&feedback.Content, stepBudgetPausedFeedback())
					history.AppendRound(conversationRound{Step: step, Assistant: providers.Message{Role: "assistant", Content: assistantText, Reasoning: reasoning}, Followup: &feedback})
					steps.setWrapUp(false)
					if err := e.persistRoundCheckpoint(active, history, completion, observations, completedToolCalls, currentModel, remainingFallbacks, step+1, completionRevisions, identicalToolPlanCount, toolPlanRecoveries, toolOutputBytes, lastToolPlan, domain.PauseReasonStepBudgetExhausted, ""); err != nil {
						e.fail(active, fmt.Errorf("persist run checkpoint: %w", err))
						return
					}
					continue
				}
				history.AppendRound(conversationRound{Step: step, Assistant: providers.Message{Role: "assistant", Content: assistantText, Reasoning: reasoning}, Followup: &feedback})
				completionRevisions++
				continue
			}
			if completionRevisions > 0 || active.taskBrief != nil {
				if err := e.publish(ctx, currentRun, domain.EventCompletionChecked, "agent", withCompletionCheckKind(active, map[string]any{
					"status": "accepted_after_revision", "workspaceRevision": workspaceRevision, "evidence": completion.Evidence(workspaceRevision),
					"changedFiles": currentRun.ChangedFiles, "commandAttempts": completion.commandAttempts,
					"successfulVerificationRevision": completion.successfulVerificationRevision,
					"correctionEpisodesUsed":         completionRevisions,
				})); err != nil {
					e.fail(active, fmt.Errorf("persist completion evidence: %w", err))
					return
				}
			}
			e.complete(active, assistantText)
			return
		}
		toolPlan := toolPlanFingerprint(calls)
		if toolPlan == lastToolPlan {
			identicalToolPlanCount++
		} else {
			lastToolPlan = toolPlan
			identicalToolPlanCount = 1
		}
		canRetryPlan := planHasUncompletedCalls(calls, completedToolCalls, workspaceRevision)
		if identicalToolPlanCount >= maxIdenticalToolPlans {
			if !canRetryPlan && toolPlanRecoveries < maxToolPlanRecoveries {
				toolPlanRecoveries++
				e.publishOrLog(ctx, e.snapshot(active), domain.EventAgentGuardrail, "agent", map[string]any{
					"code": "tool_plan_recovery", "identicalPlans": identicalToolPlanCount, "recoveryEpisode": toolPlanRecoveries,
					"maxRecoveryEpisodes": maxToolPlanRecoveries, "tools": toolCallNames(calls),
				})
				recoveryContent := strings.TrimSpace(assistantText)
				if recoveryContent == "" {
					recoveryContent = "I was about to repeat an identical tool plan that already succeeded."
				}
				feedback := providers.Message{Role: "user", Content: toolPlanRecoveryFeedback(calls, toolPlanRecoveries, maxToolPlanRecoveries)}
				history.AppendRound(conversationRound{
					Step:      step,
					Assistant: providers.Message{Role: "assistant", Content: recoveryContent, Reasoning: reasoning},
					Followup:  &feedback,
				})
				identicalToolPlanCount = 0
				lastToolPlan = ""
				continue
			}
			e.publishOrLog(ctx, e.snapshot(active), domain.EventAgentGuardrail, "agent", map[string]any{"code": "agent_stalled", "identicalPlans": identicalToolPlanCount, "tools": toolCallNames(calls), "recoveryEpisodesUsed": toolPlanRecoveries})
			e.fail(active, fmt.Errorf("agent stalled after repeating an identical tool plan %d times", identicalToolPlanCount))
			return
		}
		if identicalToolPlanCount > 1 {
			e.publishOrLog(ctx, e.snapshot(active), domain.EventAgentGuardrail, "agent", map[string]any{"code": "duplicate_tool_plan", "identicalPlans": identicalToolPlanCount, "tools": toolCallNames(calls), "canRetry": canRetryPlan})
		}
		round := conversationRound{Step: step, Assistant: providers.Message{Role: "assistant", Content: assistantText, ToolCalls: calls, Reasoning: reasoning}, Tools: make([]conversationToolTurn, 0, len(calls))}
		executedNewSuccess := false
		for _, call := range calls {
			var result domain.ToolResult
			var toolErr error
			execCall := prepareToolCall(call, profile.AllowedTools)
			callKey := toolExecutionKey(execCall, workspaceRevision)
			_, alreadyCompleted := completedToolCalls[callKey]
			if alreadyCompleted {
				result = e.rejectDuplicateTool(ctx, active, execCall)
			} else {
				if persistErr := e.persistRoundCheckpoint(active, history, completion, observations, completedToolCalls, currentModel, remainingFallbacks, step+1, completionRevisions, identicalToolPlanCount, toolPlanRecoveries, toolOutputBytes, execCall.Name, "", execCall.ID); persistErr != nil {
					e.fail(active, fmt.Errorf("persist in-flight checkpoint: %w", persistErr))
					return
				}
				result, toolErr = e.executeTool(ctx, active, profile, registry, patches, observations, execCall)
			}
			if toolErr != nil {
				if ctx.Err() != nil {
					e.finishContext(active, ctx.Err())
					return
				}
				if errors.Is(toolErr, errWorkspaceAuditIntegrity) || errors.Is(toolErr, errToolJournalIntegrity) {
					e.fail(active, toolErr)
					return
				}
				result = workbenchtools.FailWithHint("tool_failed", toolErr.Error(), "change the tool arguments or choose a different allowed tool; do not repeat the identical failing call")
			}
			if result.Error != nil {
				switch result.Error.Code {
				case "inspection_stale":
					for _, key := range observations.ReleasePatchTarget(execCall.Arguments) {
						delete(completedToolCalls, key)
					}
				case "edit_anchor_missing", "edit_anchor_ambiguous":
					for _, key := range observations.PatchTargetKeys(execCall.Arguments) {
						delete(completedToolCalls, key)
					}
				}
			}
			ownsCompletion := toolCallCompletedSuccessfully(result) && !alreadyCompleted
			if ownsCompletion && progressfulToolResult(execCall.Name, result) {
				steps.markProgress(step)
			}
			if ownsCompletion {
				if isEffectfulTool(execCall.Name) {
					releaseCommandResults(completedToolCalls, callKey)
				}
				completedToolCalls[callKey] = struct{}{}
				executedNewSuccess = true
			}
			workspaceRevision = e.currentWorkspaceRevision(active)
			if ownsCompletion {
				observations.Observe(execCall, result, workspaceRevision, step, callKey)
			}
			completion.ObserveTool(execCall.Name, execCall.Arguments, result, workspaceRevision)
			payload := []byte(workbenchtools.EncodeResult(modelFacingResult(execCall.Name, result)))
			if toolOutputBytes+len(payload) > maxRunToolOutputBytes && len(payload) > overBudgetToolOutputBytes {
				// Суммарный вывод за прогон исчерпан. Прежде это роняло прогон
				// со всей сделанной работой; теперь дальнейшие выводы модели
				// сокращаются, а она получает подсказку сузить запрос.
				if !toolOutputBudgetNoticed {
					toolOutputBudgetNoticed = true
					e.publishOrLog(ctx, e.snapshot(active), domain.EventAgentGuardrail, "agent", map[string]any{
						"code": "tool_output_budget", "toolOutputBytes": toolOutputBytes, "limitBytes": maxRunToolOutputBytes,
					})
				}
				result = shrinkToolResult(result, overBudgetToolOutputBytes)
				payload = []byte(workbenchtools.EncodeResult(result))
			}
			toolOutputBytes += len(payload)
			if toolOutputBytes > hardRunToolOutputBytes {
				e.fail(active, fmt.Errorf("cumulative tool output exceeds %d MiB", hardRunToolOutputBytes/(1024*1024)))
				return
			}
			round.Tools = append(round.Tools, conversationToolTurn{Call: execCall, Result: result, Message: providers.Message{Role: "tool", ToolCallID: call.ID, Content: string(payload)}, ExecutionKey: callKey, Replayable: ownsCompletion && isReplayableReadTool(execCall.Name)})
		}
		if identicalToolPlanCount > 1 && !executedNewSuccess {
			nudge := toolPlanNudgeFeedback(calls, identicalToolPlanCount, canRetryPlan)
			round.Followup = &providers.Message{Role: "user", Content: nudge}
		}
		if notice := e.stepBudgetFollowup(ctx, active, step); notice != "" {
			var existing *string
			if round.Followup != nil {
				existing = &round.Followup.Content
			}
			round.Followup = &providers.Message{Role: "user", Content: joinFeedback(existing, notice)}
		}
		history.AppendRound(round)
		if err := e.persistRoundCheckpoint(active, history, completion, observations, completedToolCalls, currentModel, remainingFallbacks, step+1, completionRevisions, identicalToolPlanCount, toolPlanRecoveries, toolOutputBytes, lastToolPlan, "", ""); err != nil {
			e.fail(active, fmt.Errorf("persist run checkpoint: %w", err))
			return
		}
		active.clock.start()
	}
}

// WaitFinalized waits until application-level completion hooks have correlated
// the terminal run with its Execution, Quest and Change Set records.
func (e *Engine) WaitFinalized(runID string, timeout time.Duration) bool {
	e.mu.RLock()
	active := e.active[runID]
	e.mu.RUnlock()
	if active == nil {
		return true
	}
	if timeout <= 0 {
		<-active.finalized
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-active.finalized:
		return true
	case <-timer.C:
		return false
	}
}

// isReplayableReadTool — можно ли показать результат повторно на следующем
// круге. Признак объявлен в каталоге вместе с самим инструментом: список здесь
// разошёлся бы с ним при первом же добавлении.
func isReplayableReadTool(name string) bool {
	item, ok := domain.ToolCatalogEntry(name)
	return ok && item.Replayable
}
