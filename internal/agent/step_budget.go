// Бюджет ходов прогона: предупреждение, продление, последний ход и пауза.
//
// До 29.09.2026 цикл просто кончался на MaxSteps и вызывал fail. Живой квест
// в тот день сделал на 30-м ходу успешный `npm run verify` — и провалился, не
// получив хода, чтобы сказать «готово»; изменения остались в pending Change
// Set, а человек увидел «Этап сорвался». Утверждённый в наряде потолок
// (maxSteps 64) при этом не применялся нигде.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"local-agent-workbench/internal/domain"
	workbenchtools "local-agent-workbench/internal/tools"
)

const (
	// stepNoticeRemaining — за сколько ходов до лимита модель узнаёт остаток.
	stepNoticeRemaining = 3
	// stepProgressWindow — сколько последних ходов смотрит автопродление.
	stepProgressWindow = 5
	// maxStepCeiling совпадает с проверкой профиля в storage/validate.go.
	maxStepCeiling = 100
)

// stepBudget — текущий лимит ходов и его потолок. Лимит начинается с
// MaxSteps профиля; потолок задаёт только утверждённый бюджет брифа, модель
// на него не влияет. Живёт в activeRun: ручное продление приходит из другой
// горутины.
type stepBudget struct {
	mu           sync.Mutex
	base         int
	limit        int
	ceiling      int
	extensions   int
	lastProgress int
	wrapUp       bool
}

func newStepBudget(profileMaxSteps int, brief *domain.TaskBrief) *stepBudget {
	base := profileMaxSteps
	if base <= 0 {
		base = 20
	}
	ceiling := base
	if brief != nil && brief.Budget.MaxSteps > ceiling {
		ceiling = min(brief.Budget.MaxSteps, maxStepCeiling)
		ceiling = max(ceiling, base)
	}
	return &stepBudget{base: base, limit: base, ceiling: ceiling}
}

// chunk — на сколько ходов растёт лимит за одно продление. base не меняется
// после создания, поэтому замок не нужен.
func (b *stepBudget) chunk() int {
	return max(5, b.base/3)
}

func (b *stepBudget) restore(limit, extensions int, wrapUp bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if limit > 0 {
		b.limit = limit
	}
	b.extensions = extensions
	b.wrapUp = wrapUp
}

func (b *stepBudget) snapshot() (limit, ceiling, extensions int, wrapUp bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.limit, b.ceiling, b.extensions, b.wrapUp
}

func (b *stepBudget) currentLimit() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.limit
}

func (b *stepBudget) inWrapUp() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.wrapUp
}

func (b *stepBudget) setWrapUp(value bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.wrapUp = value
}

func (b *stepBudget) markProgress(step int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if step > b.lastProgress {
		b.lastProgress = step
	}
}

// autoExtend растит лимит, если агент продвигается: за последние ходы был
// применён патч или команда завершилась с кодом 0. Застрявший агент лимита
// не получает — его ждёт последний ход без инструментов.
func (b *stepBudget) autoExtend(step int) (from, to int, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit >= b.ceiling || b.lastProgress == 0 || step-b.lastProgress >= stepProgressWindow {
		return b.limit, b.limit, false
	}
	from = b.limit
	b.limit = min(b.ceiling, b.limit+b.chunk())
	return from, b.limit, b.limit > from
}

// extendByHuman — одно ручное продление сверх потолка, как у активного
// времени: второе требует нового утверждения задания.
func (b *stepBudget) extendByHuman() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.extensions >= 1 {
		return fmt.Errorf("step budget may be extended only once without a new task approval")
	}
	b.extensions++
	b.limit += b.chunk()
	b.wrapUp = false
	return nil
}

// progressfulToolResult — ход, который сдвинул работу: применённый патч или
// команда с кодом 0. Чтения и упавшие команды прогрессом не считаются.
func progressfulToolResult(name string, result domain.ToolResult) bool {
	if !result.OK {
		return false
	}
	switch name {
	case "propose_patch":
		return true
	case "run_command":
		var output struct {
			ExitCode *int `json:"exitCode"`
		}
		return json.Unmarshal(result.Output, &output) == nil && output.ExitCode != nil && *output.ExitCode == 0
	}
	return false
}

func stepBudgetNotice(remaining, limit int) string {
	return fmt.Sprintf("<point_step_budget>\nYou have %d model turns left out of %d. Finish the verification you need now, then answer without a tool call: what changed, which checks passed with their exit codes, what remains. If you cannot finish, say exactly what is left instead of starting new exploration.\n</point_step_budget>", remaining, limit)
}

func stepBudgetWrapUpFeedback(limit int) string {
	return fmt.Sprintf("<point_step_budget>\nThe turn limit (%d) is reached. Tools are unavailable for this turn. Answer now without a tool call: what you changed, which checks passed (command and exit code), and what remains unfinished.\n</point_step_budget>", limit)
}

func stepBudgetPausedFeedback() string {
	return "<point_step_budget>\nPoint paused the run because the turn limit was reached before the task was complete. If the person grants more turns, tools will be available again: continue from where you stopped, do not repeat finished work.\n</point_step_budget>"
}

func joinFeedback(existing *string, addition string) string {
	if existing == nil || *existing == "" {
		return addition
	}
	return *existing + "\n" + addition
}

// withoutUnusableGitTools убирает git-инструменты из того, что видит модель,
// когда у корня нет рабочего дерева Git. Реестр их сохраняет: критерии,
// названные по имени, проверяются по нему. Живые квесты 28–29.09 тратили на
// git_diff без .git по ходу.
func withoutUnusableGitTools(ctx context.Context, definitions []domain.ToolDefinition, patches *workbenchtools.PatchManager) []domain.ToolDefinition {
	if patches == nil || patches.FS == nil {
		return definitions
	}
	hasGit := false
	for _, definition := range definitions {
		if workbenchtools.IsGitReadTool(definition.Name) {
			hasGit = true
			break
		}
	}
	if !hasGit || workbenchtools.GitWorkTreeAvailable(ctx, patches.FS.Root()) {
		return definitions
	}
	filtered := make([]domain.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if !workbenchtools.IsGitReadTool(definition.Name) {
			filtered = append(filtered, definition)
		}
	}
	return filtered
}

// autoExtendSteps пробует автопродление и сообщает о нём событием guardrail.
func (e *Engine) autoExtendSteps(ctx context.Context, active *activeRun, step int) bool {
	from, to, ok := active.steps.autoExtend(step)
	if !ok {
		return false
	}
	_, ceiling, _, _ := active.steps.snapshot()
	e.publishOrLog(ctx, e.snapshot(active), domain.EventAgentGuardrail, "agent", map[string]any{
		"code": "step_budget_extended", "from": from, "to": to, "ceiling": ceiling, "step": step,
	})
	return true
}

// stepBudgetFollowup — что добавить к раунду после его инструментов: остаток
// ходов за три до лимита или, на самом лимите без права продления, требование
// дать итог следующим ходом без инструментов.
func (e *Engine) stepBudgetFollowup(ctx context.Context, active *activeRun, step int) string {
	limit := active.steps.currentLimit()
	if step >= limit {
		if !e.autoExtendSteps(ctx, active, step) {
			active.steps.setWrapUp(true)
			return stepBudgetWrapUpFeedback(limit)
		}
		limit = active.steps.currentLimit()
	}
	if remaining := limit - step; remaining == stepNoticeRemaining {
		return stepBudgetNotice(remaining, limit)
	}
	return ""
}

// waitForStepBudget держит прогон на паузе, пока человек не добавит ходов.
// Продолжение без продления ставит паузу ещё раз, как у активного времени;
// после второго — провал с прежним текстом, по нему diagnostics узнаёт
// step_limit.
func (e *Engine) waitForStepBudget(ctx context.Context, active *activeRun, step int) bool {
	if active.taskBrief != nil {
		for attempt := 0; attempt < 2; attempt++ {
			e.requestPause(active, domain.PauseReasonStepBudgetExhausted)
			if err := e.waitAtCheckpoint(ctx, active); err != nil {
				e.finishContext(active, err)
				return false
			}
			if step <= active.steps.currentLimit() {
				return true
			}
		}
	}
	e.fail(active, fmt.Errorf("maximum step count (%d) reached", active.steps.currentLimit()))
	return false
}
