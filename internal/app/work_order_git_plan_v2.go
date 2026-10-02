package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/gitflow"
	"local-agent-workbench/internal/integrations/gitlab"
	"local-agent-workbench/internal/orchestrator"
)

// Git-агент в наряде: ветка решается до работы, переключается при
// утверждении, коммит — после вердикта «выполнен» (quest_git_actions_v2.go).

const questGitPolicySettingKey = "quest-git-policy-v1"

// QuestGitPolicy — что квест делает с git после успеха. on_completion — сам
// коммитит в свою ветку и в конце спрашивает про отправку и MR; on_request —
// ничего, пока человек не попросит кнопкой или в чате.
type QuestGitPolicy struct {
	CommitPolicy string `json:"commitPolicy"`
}

func (a *App) QuestGitPolicy(ctx context.Context) (QuestGitPolicy, error) {
	policy := QuestGitPolicy{CommitPolicy: domain.CommitOnCompletion}
	raw, err := a.store.Setting(ctx, questGitPolicySettingKey)
	if errors.Is(err, sql.ErrNoRows) {
		return policy, nil
	}
	if err != nil {
		return policy, err
	}
	if err = json.Unmarshal([]byte(raw), &policy); err != nil || (policy.CommitPolicy != domain.CommitOnCompletion && policy.CommitPolicy != domain.CommitOnRequest) {
		return QuestGitPolicy{CommitPolicy: domain.CommitOnCompletion}, nil
	}
	return policy, nil
}

func (a *App) SaveQuestGitPolicy(ctx context.Context, policy QuestGitPolicy) (QuestGitPolicy, error) {
	switch strings.TrimSpace(policy.CommitPolicy) {
	case "auto", domain.CommitOnCompletion:
		policy.CommitPolicy = domain.CommitOnCompletion
	case "onRequest", domain.CommitOnRequest:
		policy.CommitPolicy = domain.CommitOnRequest
	default:
		return policy, fmt.Errorf("неизвестная политика git %q", policy.CommitPolicy)
	}
	raw, _ := json.Marshal(policy)
	return policy, a.store.SaveSetting(ctx, questGitPolicySettingKey, string(raw))
}

// gitInspectRunner — git без всплывающих окон входа: осмотр идёт посреди хода
// Мастера, и credential manager не должен открывать диалог.
func (a *App) gitInspectRunner() gitflow.Runner {
	if a.gitRunner != nil {
		return a.gitRunner
	}
	return gitflow.ExecRunner{Env: []string{"GCM_INTERACTIVE=never"}}
}

// gitActionRunner — git для действий человека (отправка): окно входа
// credential manager здесь допустимо, человек сам нажал кнопку.
func (a *App) gitActionRunner() gitflow.Runner {
	if a.gitRunner != nil {
		return a.gitRunner
	}
	return gitflow.ExecRunner{}
}

// masterWorkOrderGitV2 — план ветки и режим коммита наряда. Осмотр (fetch,
// GitLab, модель) делается, когда наряд готов к утверждению, и повторяется
// только если репозитории сдвинулись: иначе каждый ход Мастера давал бы новую
// версию карточки, а выбор человека пропадал.
func (a *App) masterWorkOrderGitV2(ctx context.Context, order domain.WorkOrder, current *domain.WorkOrder, cfg domain.OrchestratorConfig, apiKey string) (*domain.GitPlan, string) {
	if order.Workspace.Mode != "existing" || strings.TrimSpace(order.Workspace.Path) == "" {
		return nil, "none"
	}
	policy, _ := a.QuestGitPolicy(ctx)
	runner := a.gitInspectRunner()
	repos := gitflow.Repositories(ctx, runner, order.Workspace.Path)
	if len(repos) == 0 {
		return nil, "none"
	}
	if current != nil && current.Git != nil && gitPlanStillCurrent(ctx, runner, current.Git, repos) {
		plan := *current.Git
		return &plan, gitCommitModeFor(&plan, policy.CommitPolicy)
	}
	if order.State != "staffing" && order.State != "ready" {
		return nil, "none"
	}
	reports := make([]gitflow.RepoReport, 0, len(repos))
	for _, repo := range repos {
		reports = append(reports, gitflow.InspectRepo(ctx, runner, repo, gitflow.InspectOptions{Fetch: true}))
	}
	a.markGitLabProtection(ctx, reports)
	plan := gitflow.BuildPlan(reports, a.gitAgentBranchName(ctx, cfg, apiKey, order, reports))
	return plan, gitCommitModeFor(plan, policy.CommitPolicy)
}

func gitCommitModeFor(plan *domain.GitPlan, policy string) string {
	if plan == nil || plan.Mode == domain.GitModeNone {
		return "none"
	}
	if policy == domain.CommitOnRequest {
		return domain.CommitOnRequest
	}
	return domain.CommitOnCompletion
}

func gitPlanStillCurrent(ctx context.Context, runner gitflow.Runner, plan *domain.GitPlan, repos []gitflow.RepoReport) bool {
	if len(plan.Repositories) != len(repos) {
		return false
	}
	for index, repo := range repos {
		planned := plan.Repositories[index]
		if planned.Path != repo.Path {
			return false
		}
		head, _ := runner.Run(ctx, repo.Dir, "rev-parse", "--verify", "HEAD")
		branch, _ := runner.Run(ctx, repo.Dir, "branch", "--show-current")
		if strings.TrimSpace(string(head)) != planned.HeadCommit || strings.TrimSpace(string(branch)) != planned.Current {
			return false
		}
	}
	return true
}

// markGitLabProtection — защита ветки и признак GitLab по каждому
// репозиторию. Подключённый GitLab знает защиту точно; без него остаётся
// правило по имени. Сбой GitLab осмотр не останавливает.
func (a *App) markGitLabProtection(ctx context.Context, reports []gitflow.RepoReport) {
	connected := ""
	var session *gitlabSession
	if server, err := a.store.GetMCPServer(ctx, gitlabServerID); err == nil {
		connected = server.Settings["url"]
		sessionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if live, sessionErr := a.gitlabSession(sessionCtx); sessionErr == nil && live.client != nil {
			session = &live
		}
		cancel()
	}
	for index := range reports {
		report := &reports[index]
		report.GitLab = gitflow.LooksLikeGitLab(report.Remote, connected)
		if session == nil || report.Current == "" || report.Remote == "" {
			continue
		}
		remote, err := gitlab.ParseRemote(report.Remote)
		if err != nil {
			continue
		}
		project, ok := gitlab.ProjectFor(remote, connected)
		if !ok {
			continue
		}
		report.GitLab = true
		lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		branches, err := session.client.Branches(lookupCtx, project, report.Current)
		cancel()
		if err != nil {
			continue
		}
		for _, branch := range branches {
			if branch.Name == report.Current {
				report.Protected = branch.Protected || branch.Default
				report.ProtectedSource = ""
				if report.Protected {
					report.ProtectedSource = "gitlab"
				}
			}
		}
	}
}

// gitAgentConfig — модель git-агента: Архивариуса, иначе Мастера (как у
// отчётов). Ключ — тот, что пришёл с действием человека.
func (a *App) gitAgentConfig(ctx context.Context, workspaceID string) (domain.OrchestratorConfig, error) {
	cfg, err := a.masterConfig(ctx, workspaceID)
	if err != nil {
		return cfg, err
	}
	if defaults, defaultsErr := a.GlobalModelDefaults(ctx); defaultsErr == nil && defaults.Archivist.ConnectionID != "" {
		archivist := cfg
		archivist.ConnectionID, archivist.Model = defaults.Archivist.ConnectionID, defaults.Archivist.Model
		if resolved, resolveErr := a.resolveOrchestratorConnection(archivist); resolveErr == nil {
			cfg = resolved
		}
	}
	return cfg, nil
}

func (a *App) gitAgentFactory(workspaceID string) orchestrator.ModelFactory {
	return a.budgetedModelFactory(modelBudgetScope{WorkspaceID: workspaceID, ProjectAgentID: orchestrator.GitAgentID, Outcome: "git_agent_model"})
}

func (a *App) gitAgentBranchName(ctx context.Context, cfg domain.OrchestratorConfig, apiKey string, order domain.WorkOrder, reports []gitflow.RepoReport) string {
	prefixes := map[string]int{}
	var subjects []string
	for _, report := range reports {
		for prefix, count := range report.BranchPrefixes {
			prefixes[prefix] += count
		}
		subjects = append(subjects, report.RecentSubjects...)
	}
	fallback := gitflow.FallbackBranchName(order.Goal, prefixes)
	agentCfg, err := a.gitAgentConfig(ctx, order.WorkspaceID)
	if err != nil {
		agentCfg = cfg
	}
	name, err := orchestrator.ProposeBranchName(ctx, agentCfg, orchestrator.GitAgentRequest{
		APIKey: apiKey, Goal: order.Goal, Scope: order.Scope, RecentSubjects: subjects, BranchPrefixes: prefixes,
	}, a.gitAgentFactory(order.WorkspaceID))
	if name = gitflow.SanitizeBranchName(name); err != nil || !domain.ValidGitBranchName(name) || !strings.Contains(name, "/") {
		return fallback
	}
	return name
}

// gitChoiceFromOrder — какой выбор человек сделал в карточке.
func gitChoiceFromOrder(plan *domain.GitPlan) string {
	if plan == nil {
		return ""
	}
	switch plan.Mode {
	case domain.GitModeCurrent:
		return gitflow.RecommendCurrent
	case domain.GitModeNone:
		return domain.GitModeNone
	case domain.GitModeNew:
		if plan.BaseKind == "default" {
			return gitflow.RecommendNewDefault
		}
		return gitflow.RecommendNewCurrent
	}
	return ""
}

// reviseWorkOrderGitV2 — выбор человека поверх серверного плана: осмотр
// (коммиты, защита, предупреждения) из карточки не принимается.
func reviseWorkOrderGitV2(current, requested *domain.GitPlan) (*domain.GitPlan, error) {
	if current == nil {
		return nil, nil
	}
	plan := *current
	plan.Repositories = append([]domain.GitRepoPlan(nil), current.Repositories...)
	if requested == nil {
		return &plan, nil
	}
	choice := gitChoiceFromOrder(requested)
	if choice == "" || (choice == gitChoiceFromOrder(current) && strings.TrimSpace(requested.Branch) == current.Branch) {
		return &plan, nil
	}
	if err := gitflow.ApplyChoice(&plan, choice, requested.Branch); err != nil {
		return nil, err
	}
	return &plan, nil
}

// revisedGitCommitModeV2 — режим коммита после выбора ветки: «не трогать
// ветку» значит и не коммитить, иначе — политика Point.
func (a *App) revisedGitCommitModeV2(ctx context.Context, current domain.WorkOrder, plan *domain.GitPlan) string {
	if plan == nil || plan.Mode == domain.GitModeNone {
		return "none"
	}
	if current.Delivery.CommitMode == domain.CommitOnCompletion || current.Delivery.CommitMode == domain.CommitOnRequest {
		return current.Delivery.CommitMode
	}
	policy, _ := a.QuestGitPolicy(ctx)
	return gitCommitModeFor(plan, policy.CommitPolicy)
}

// gitCheckoutResult — что сделало утверждение в репозитории.
type gitCheckoutResult struct {
	Repo, Dir, Previous, Branch, Base, BaseCommit string
	Created                                       bool
}

// checkoutWorkOrderGitV2 переключает репозитории наряда на его ветку до
// первого снимка песочницы. Сбой откатывает уже сделанное.
func (a *App) checkoutWorkOrderGitV2(ctx context.Context, order domain.WorkOrder) ([]gitCheckoutResult, error) {
	plan := order.Git
	if plan == nil {
		return nil, nil
	}
	if err := domain.GitPlanApprovalError(plan); err != nil {
		return nil, err
	}
	if plan.Mode == domain.GitModeNone {
		return nil, nil
	}
	runner := a.gitInspectRunner()
	repos := gitflow.Repositories(ctx, runner, order.Workspace.Path)
	dirs := map[string]string{}
	for _, repo := range repos {
		dirs[repo.Path] = repo.Dir
	}
	var done []gitCheckoutResult
	undo := func() {
		for index := len(done) - 1; index >= 0; index-- {
			item := done[index]
			if item.Created {
				gitflow.UndoCheckout(context.Background(), runner, item.Dir, item.Previous, item.Branch, item.BaseCommit)
			}
		}
	}
	for _, repo := range plan.Repositories {
		dir := dirs[repo.Path]
		if dir == "" {
			undo()
			return nil, fmt.Errorf("%s: репозиторий не найден — обновите карточку наряда", repo.Path)
		}
		currentRaw, _ := runner.Run(ctx, dir, "branch", "--show-current")
		current := strings.TrimSpace(string(currentRaw))
		if plan.Mode == domain.GitModeCurrent {
			if current != repo.Current {
				undo()
				return nil, fmt.Errorf("%s: открыта ветка %q, а наряд утверждался для %q — обновите карточку", repo.Path, current, repo.Current)
			}
			continue
		}
		base, baseCommit := domain.GitRepoBase(*plan, repo)
		if status, _ := runner.Run(ctx, dir, "status", "--porcelain=v1", "--untracked-files=no"); strings.TrimSpace(string(status)) != "" && current != plan.Branch {
			undo()
			return nil, fmt.Errorf("%s: в отслеживаемых файлах есть незакоммиченные правки — закоммитьте или спрячьте их, затем утвердите снова", repo.Path)
		}
		created, err := gitflow.Checkout(ctx, runner, dir, plan.Branch, baseCommit)
		if err != nil {
			undo()
			return nil, fmt.Errorf("%s: ветка %s не создана: %w", repo.Path, plan.Branch, err)
		}
		done = append(done, gitCheckoutResult{Repo: repo.Path, Dir: dir, Previous: current, Branch: plan.Branch, Base: base, BaseCommit: baseCommit, Created: created})
	}
	return done, nil
}

func (a *App) recordWorkOrderCheckoutsV2(ctx context.Context, approval domain.WorkOrderApproval, results []gitCheckoutResult) {
	for _, item := range results {
		message := ""
		if item.Previous != "" {
			message = "от " + item.Previous
		}
		_, _ = a.store.AppendQuestGitAction(ctx, domain.QuestGitAction{
			QuestID: approval.QuestID, WorkOrderID: approval.WorkOrder.ID, Repo: item.Repo, Action: "checkout", Phase: "succeeded",
			Branch: item.Branch, Base: item.Base, CommitID: item.BaseCommit, Message: message, Trigger: "auto",
		})
	}
}

func undoWorkOrderCheckoutsV2(runner gitflow.Runner, results []gitCheckoutResult) {
	for index := len(results) - 1; index >= 0; index-- {
		item := results[index]
		if item.Created {
			gitflow.UndoCheckout(context.Background(), runner, item.Dir, item.Previous, item.Branch, item.BaseCommit)
		}
	}
}

// verifyWorkOrderBranchV2 — перед каждым запуском этапа репозитории должны
// стоять на ветке наряда: песочница копирует живое дерево, и чужая ветка дала
// бы работу не от той основы и коммит не туда.
func (a *App) verifyWorkOrderBranchV2(ctx context.Context, order domain.WorkOrder) error {
	plan := order.Git
	if plan == nil || plan.Mode == "" || plan.Mode == domain.GitModeNone {
		return nil
	}
	runner := a.gitInspectRunner()
	for _, repo := range gitflow.Repositories(ctx, runner, order.Workspace.Path) {
		want := domain.GitPlanTargetBranch(plan, repo.Path)
		if want == "" {
			continue
		}
		raw, _ := runner.Run(ctx, repo.Dir, "branch", "--show-current")
		if current := strings.TrimSpace(string(raw)); current != want {
			return fmt.Errorf("%s: открыта ветка %q, а квест работает в %q — переключитесь обратно (git switch %s) и продолжите квест", repo.Path, current, want, want)
		}
	}
	return nil
}

// GitInspection — осмотр папки git-агентом для ручной ветки чата и карточек.
type GitInspection struct {
	Repositories []gitflow.RepoReport `json:"repositories"`
	Warnings     map[string][]string  `json:"warnings,omitempty"`
}

// InspectGitPath — тот же осмотр, что перед нарядом: fetch, основная ветка,
// защита, предупреждения. Рабочее дерево не меняет.
func (a *App) InspectGitPath(ctx context.Context, path string) (GitInspection, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return GitInspection{}, errors.New("папка не указана")
	}
	runner := a.gitInspectRunner()
	reports := gitflow.Inspect(ctx, runner, path, gitflow.InspectOptions{Fetch: true})
	a.markGitLabProtection(ctx, reports)
	out := GitInspection{Repositories: reports, Warnings: map[string][]string{}}
	for _, report := range reports {
		out.Warnings[report.Path] = gitflow.Warnings(report)
	}
	return out, nil
}
