package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"local-agent-workbench/internal/changesets"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/forge"
	"local-agent-workbench/internal/gitflow"
	"local-agent-workbench/internal/orchestrator"
	"local-agent-workbench/internal/security"
)

// Git-действия квеста после вердикта: коммит, отправка, MR, откат. Коммит
// делает сам Point при политике on_completion и вердикте «выполнен»; всё
// остальное — только по просьбе человека (кнопка карточки или чат).

// QuestGitActionResult — ответ действия. OpenURL — страница, которую
// расширение откроет само (новый MR, когда GitLab не создал его при push).
type QuestGitActionResult struct {
	Git     *domain.QuestGitView `json:"git,omitempty"`
	OpenURL string               `json:"openUrl,omitempty"`
	Message string               `json:"message"`
}

var (
	questGitLocks sync.Map // questID → *sync.Mutex
	questGitKeys  sync.Map // questID → ключ модели, пришедший с запуском квеста
)

// rememberQuestGitKey держит ключ модели, с которым запущен квест, чтобы
// git-агент мог написать сообщение коммита после вердикта. Только в памяти.
func (a *App) rememberQuestGitKey(questID, apiKey string) {
	if strings.TrimSpace(questID) != "" && strings.TrimSpace(apiKey) != "" {
		questGitKeys.Store(questID, apiKey)
	}
}

func questGitKey(questID string) string {
	if value, ok := questGitKeys.Load(questID); ok {
		return value.(string)
	}
	return ""
}

func lockQuestGit(questID string) func() {
	value, _ := questGitLocks.LoadOrStore(questID, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

type questGitContext struct {
	approval domain.WorkOrderApproval
	order    domain.WorkOrder
	status   domain.QuestStatus
	bundle   domain.EvidenceBundle
	view     *domain.QuestGitView
	dirs     map[string]string
}

func (a *App) questGitContextV2(ctx context.Context, questID string) (questGitContext, error) {
	var out questGitContext
	approval, err := a.store.WorkOrderApprovalByQuestV2(ctx, questID)
	if err != nil {
		return out, fmt.Errorf("квест не найден среди нарядов: %w", err)
	}
	out.approval, out.order = approval, approval.WorkOrder
	if out.order.Git == nil {
		return out, errors.New("у наряда нет плана ветки — git-действия доступны квестам, запущенным с git-агентом")
	}
	order, err := a.store.GetWorkOrderV2(ctx, out.order.ID)
	if err == nil && order.Runtime != nil {
		out.status = order.Runtime.Status
		if order.Runtime.Evidence != nil {
			out.bundle = *order.Runtime.Evidence
		}
		out.view = order.Runtime.Git
	}
	if out.view == nil {
		actions, _ := a.store.ListQuestGitActions(ctx, questID)
		out.view = domain.BuildQuestGitView(out.order, out.status, out.bundle.Assurance, out.bundle.ChangedFiles, actions)
	}
	out.dirs = map[string]string{}
	for _, repo := range gitflow.Repositories(ctx, a.gitInspectRunner(), out.order.Workspace.Path) {
		// Пути файлов квеста — от папки проекта; для её собственного
		// репозитория git запускается в ней, а не в корне репозитория.
		dir := repo.Dir
		if repo.Path == "." {
			dir = out.order.Workspace.Path
		}
		out.dirs[repo.Path] = dir
	}
	return out, nil
}

// RunQuestGitAction — действие человека: commit, push, merge_request, revert.
// repo пуст — все репозитории, где действие сейчас доступно.
func (a *App) RunQuestGitAction(ctx context.Context, questID, action, repo, trigger, apiKey string) (QuestGitActionResult, error) {
	questID, action, repo = strings.TrimSpace(questID), strings.TrimSpace(action), strings.TrimSpace(repo)
	switch action {
	case "commit", "push", "merge_request", "revert":
	default:
		return QuestGitActionResult{}, fmt.Errorf("неизвестное git-действие %q", action)
	}
	if trigger == "" {
		trigger = "button"
	}
	unlock := lockQuestGit(questID)
	defer unlock()
	state, err := a.questGitContextV2(ctx, questID)
	if err != nil {
		return QuestGitActionResult{}, err
	}
	targets := []domain.QuestGitRepoView{}
	for _, item := range state.view.Repositories {
		if (repo == "" || item.Path == repo) && containsString(item.Actions, action) {
			targets = append(targets, item)
		}
	}
	if len(targets) == 0 {
		return QuestGitActionResult{Git: state.view}, fmt.Errorf("действие «%s» сейчас недоступно для этого квеста", questGitActionTitle(action))
	}
	if apiKey == "" {
		apiKey = questGitKey(questID)
	}
	result := QuestGitActionResult{}
	var messages []string
	var failures []string
	switch action {
	case "revert":
		message, revertErr := a.revertQuestDeliveryV2(ctx, state, trigger)
		if revertErr != nil {
			failures = append(failures, revertErr.Error())
		} else {
			messages = append(messages, message)
		}
	default:
		if action == "commit" {
			if branchErr := a.verifyWorkOrderBranchV2(ctx, state.order); branchErr != nil {
				return QuestGitActionResult{Git: state.view}, branchErr
			}
		}
		for _, item := range targets {
			var text, openURL string
			var actionErr error
			switch action {
			case "commit":
				text, actionErr = a.commitQuestRepoV2(ctx, state, item, trigger, apiKey)
			case "push":
				text, actionErr = a.pushQuestRepoV2(ctx, state, item, trigger, false)
			case "merge_request":
				text, actionErr = a.pushQuestRepoV2(ctx, state, item, trigger, true)
				if actionErr == nil && strings.HasPrefix(text, "http") {
					openURL, text = text, "GitLab не создал MR при отправке — открываю страницу нового MR"
				}
			}
			if actionErr != nil {
				failures = append(failures, item.Path+": "+actionErr.Error())
				continue
			}
			if openURL != "" {
				result.OpenURL = openURL
			}
			messages = append(messages, text)
		}
	}
	if refreshed, refreshErr := a.questGitContextV2(ctx, questID); refreshErr == nil {
		result.Git = refreshed.view
	}
	result.Message = strings.Join(messages, "\n")
	if len(failures) > 0 {
		return result, errors.New(strings.Join(failures, "\n"))
	}
	return result, nil
}

func (a *App) recordQuestGitV2(ctx context.Context, state questGitContext, action domain.QuestGitAction) {
	action.QuestID, action.WorkOrderID = state.approval.QuestID, state.order.ID
	if _, err := a.store.AppendQuestGitAction(ctx, action); err != nil {
		slog.Warn("quest git action not recorded", "quest_id", action.QuestID, "action", action.Action, "error", err)
	}
}

func questFilesForRepoV2(files []string, repo string) []string {
	var out []string
	for _, file := range files {
		file = filepath.ToSlash(file)
		switch {
		case repo == ".":
			out = append(out, file)
		case strings.HasPrefix(file, repo+"/"):
			out = append(out, strings.TrimPrefix(file, repo+"/"))
		}
	}
	return out
}

func (a *App) commitQuestRepoV2(ctx context.Context, state questGitContext, item domain.QuestGitRepoView, trigger, apiKey string) (string, error) {
	dir := state.dirs[item.Path]
	files := questFilesForRepoV2(state.bundle.ChangedFiles, item.Path)
	if dir == "" || len(files) == 0 {
		return "", errors.New("нет файлов квеста для коммита")
	}
	message := a.composeQuestCommitV2(ctx, state, item, dir, files, apiKey)
	opID := domain.NewID("gitop")
	a.recordQuestGitV2(ctx, state, domain.QuestGitAction{OpID: opID, Repo: item.Path, Action: "commit", Phase: "started", Branch: item.Branch, Trigger: trigger})
	commitID, err := gitflow.Commit(ctx, a.gitActionRunner(), dir, files, message)
	if err != nil {
		if errors.Is(err, gitflow.ErrNothingToCommit) {
			err = errors.New("файлы квеста уже совпадают с последним коммитом — коммитить нечего")
		}
		a.recordQuestGitV2(ctx, state, domain.QuestGitAction{OpID: opID, Repo: item.Path, Action: "commit", Phase: "failed", Branch: item.Branch, Error: security.Redact(err.Error()), Trigger: trigger})
		return "", err
	}
	a.recordQuestGitV2(ctx, state, domain.QuestGitAction{OpID: opID, Repo: item.Path, Action: "commit", Phase: "succeeded", Branch: item.Branch, CommitID: commitID, Message: message, Trigger: trigger})
	subject := strings.SplitN(message, "\n", 2)[0]
	return fmt.Sprintf("%s: коммит %s в %s — %s", item.Path, shortCommit(commitID), item.Branch, subject), nil
}

// composeQuestCommitV2 — сообщение пишет git-агент по фактам: цель наряда,
// файлы, diffstat, история репозитория. Без модели — шаблон по стилю истории.
func (a *App) composeQuestCommitV2(ctx context.Context, state questGitContext, item domain.QuestGitRepoView, dir string, files []string, apiKey string) string {
	runner := a.gitInspectRunner()
	subjectsRaw, _ := runner.Run(ctx, dir, "log", "-15", "--no-merges", "--format=%s")
	var subjects []string
	for _, line := range strings.Split(strings.TrimSpace(string(subjectsRaw)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			subjects = append(subjects, line)
		}
	}
	diffStat, _ := runner.Run(ctx, dir, append([]string{"diff", "HEAD", "--stat", "--"}, files...)...)
	diff, _ := runner.Run(ctx, dir, append([]string{"diff", "HEAD", "--"}, files...)...)
	var checks []string
	for _, criterion := range state.order.Criteria {
		checks = append(checks, criterion.Text)
	}
	trailers := map[string]string{"Point-Quest": state.approval.QuestID, "Point-Evidence": state.bundle.ID}
	if cfg, err := a.gitAgentConfig(ctx, state.order.WorkspaceID); err == nil {
		text, composeErr := orchestrator.ComposeCommit(ctx, cfg, orchestrator.GitAgentRequest{
			APIKey: apiKey, Goal: state.order.Goal, Scope: state.order.Scope, Files: files, DiffStat: string(diffStat),
			Diff: string(diff), RecentSubjects: subjects, Branch: item.Branch, Checks: checks,
		}, a.gitAgentFactory(state.order.WorkspaceID))
		if composeErr == nil {
			return gitflow.ComposeMessage(text.Subject, text.Body, trailers)
		}
		slog.Info("git agent commit message fell back to template", "quest_id", state.approval.QuestID, "error", composeErr)
	}
	return gitflow.ComposeMessage(gitflow.FallbackCommitSubject(state.order.Goal, gitflow.DetectCommitStyle(subjects)), "", trailers)
}

// pushQuestRepoV2 отправляет ветку квеста; с withMR просит GitLab создать MR.
// Возвращает либо текст для человека, либо ссылку «новый MR», если GitLab
// его не создал (ветка уже была на сервере).
func (a *App) pushQuestRepoV2(ctx context.Context, state questGitContext, item domain.QuestGitRepoView, trigger string, withMR bool) (string, error) {
	dir := state.dirs[item.Path]
	if dir == "" || item.Branch == "" || item.CommitID == "" {
		return "", errors.New("нечего отправлять: у квеста нет коммита")
	}
	action := "push"
	var request *gitflow.MergeRequest
	if withMR {
		action = "merge_request"
		subject, body := item.CommitSubject, ""
		for _, entry := range a.questGitActionsFor(ctx, state.approval.QuestID, item.Path, "commit") {
			if entry.CommitID == item.CommitID {
				parts := strings.SplitN(entry.Message, "\n\n", 2)
				subject = parts[0]
				if len(parts) > 1 {
					body = stripPointTrailers(parts[1])
				}
			}
		}
		request = &gitflow.MergeRequest{Target: item.Target, Title: subject, Description: body}
	}
	remote := ""
	for _, repo := range state.order.Git.Repositories {
		if repo.Path == item.Path {
			remote = repo.Remote
		}
	}
	opID := domain.NewID("gitop")
	a.recordQuestGitV2(ctx, state, domain.QuestGitAction{OpID: opID, Repo: item.Path, Action: action, Phase: "started", Branch: item.Branch, CommitID: item.CommitID, Remote: remote, Trigger: trigger})
	pushCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	var forgeBinding *forge.Binding
	if withMR {
		links, linkErr := a.ForgeBindings(ctx, GitTarget{WorkspaceID: state.order.WorkspaceID, RepoRoot: dir})
		if linkErr != nil {
			return "", linkErr
		}
		if len(links.Candidates) != 1 {
			return "", errors.New("выберите связь репозитория с аккаунтом GitLab в пространстве Git")
		}
		forgeBinding = &links.Candidates[0]
	}
	remoteName := "origin"
	if forgeBinding != nil {
		remoteName = forgeBinding.Remote
	}
	a.recordQuestGitV2(ctx, state, domain.QuestGitAction{OpID: opID, Repo: item.Path, Action: action, Phase: "started", Branch: item.Branch, CommitID: item.CommitID, Remote: remote, Trigger: trigger})
	result, err := gitflow.Push(pushCtx, a.gitActionRunner(), dir, remoteName, item.Branch, nil)
	if err != nil {
		a.recordQuestGitV2(ctx, state, domain.QuestGitAction{OpID: opID, Repo: item.Path, Action: action, Phase: "failed", Branch: item.Branch, CommitID: item.CommitID, Remote: remote, Error: security.Redact(err.Error()), Trigger: trigger})
		return "", err
	}
	mrURL := result.MRURL
	if withMR {
		response, createErr := a.RunForgeRequest(ctx, forge.Request{ConnectionID: forgeBinding.ConnectionID,
			Project: forgeBinding.Project, Action: "create", Title: request.Title, Description: request.Description,
			SourceBranch: item.Branch, TargetBranch: request.Target})
		if createErr != nil {
			a.recordQuestGitV2(ctx, state, domain.QuestGitAction{OpID: opID, Repo: item.Path, Action: action, Phase: "failed", Branch: item.Branch, CommitID: item.CommitID, Remote: remote, Error: security.Redact(createErr.Error()), Trigger: trigger})
			return "", fmt.Errorf("ветка отправлена; создание MR: %w", createErr)
		}
		if review, ok := response.Data.(forge.Review); ok {
			mrURL = review.WebURL
		}
	}
	a.recordQuestGitV2(ctx, state, domain.QuestGitAction{OpID: opID, Repo: item.Path, Action: action, Phase: "succeeded", Branch: item.Branch, CommitID: item.CommitID, Remote: remote, MRURL: mrURL, Message: clipText(result.Output, 2000), Trigger: trigger})
	if !withMR {
		return fmt.Sprintf("%s: ветка %s отправлена в origin", item.Path, item.Branch), nil
	}
	if mrURL != "" {
		return fmt.Sprintf("%s: MR создан — %s", item.Path, mrURL), nil
	}
	if result.NewMRURL != "" {
		return result.NewMRURL, nil
	}
	return gitflow.NewMergeRequestURL(remote, item.Branch, item.Target), nil
}

func (a *App) questGitActionsFor(ctx context.Context, questID, repo, action string) []domain.QuestGitAction {
	all, _ := a.store.ListQuestGitActions(ctx, questID)
	var out []domain.QuestGitAction
	for _, entry := range all {
		if entry.Repo == repo && entry.Action == action && entry.Phase == "succeeded" {
			out = append(out, entry)
		}
	}
	return out
}

func stripPointTrailers(body string) string {
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "Point-Quest:") || strings.HasPrefix(line, "Point-Evidence:") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// revertQuestDeliveryV2 возвращает файлы проекта к состоянию до квеста:
// применённые наборы квеста откатываются в обратном порядке. Сначала
// проверка всех наборов — откат не должен остановиться на середине.
func (a *App) revertQuestDeliveryV2(ctx context.Context, state questGitContext, trigger string) (string, error) {
	sets, err := a.questAppliedChangeSetsV2(ctx, state.order, state.approval.QuestID)
	if err != nil {
		return "", err
	}
	if len(sets) == 0 {
		return "", errors.New("у квеста нет применённых файлов")
	}
	applier := changesets.Applier{Store: a.store}
	blockers, err := applier.RevertBlockers(ctx, state.order.Workspace.Path, sets)
	if err != nil {
		return "", err
	}
	if len(blockers) > 0 {
		return "", fmt.Errorf("откат остановлен до начала — файлы изменены после квеста: %s", strings.Join(blockers, ", "))
	}
	opID := domain.NewID("gitop")
	for index := len(sets) - 1; index >= 0; index-- {
		if _, revertErr := applier.Revert(ctx, state.order.Workspace.Path, sets[index]); revertErr != nil {
			a.recordQuestGitV2(ctx, state, domain.QuestGitAction{OpID: opID, Repo: ".", Action: "revert", Phase: "failed", Error: revertErr.Error(), Trigger: trigger})
			return "", revertErr
		}
	}
	for _, repo := range state.order.Git.Repositories {
		a.recordQuestGitV2(ctx, state, domain.QuestGitAction{OpID: opID, Repo: repo.Path, Action: "revert", Phase: "succeeded", Message: fmt.Sprintf("откатано наборов: %d", len(sets)), Trigger: trigger})
	}
	return fmt.Sprintf("файлы квеста откатаны (наборов изменений: %d)", len(sets)), nil
}

func (a *App) questAppliedChangeSetsV2(ctx context.Context, order domain.WorkOrder, rootID string) ([]string, error) {
	quests, err := a.store.ListQuests(ctx, order.WorkspaceID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*domain.Quest, len(quests))
	for index := range quests {
		byID[quests[index].ID] = &quests[index]
	}
	executions, err := a.store.ListExecutions(ctx, order.WorkspaceID, 500)
	if err != nil {
		return nil, err
	}
	owned := map[string]bool{}
	for _, execution := range executions {
		if questDescendsFrom(execution.QuestID, rootID, byID) {
			owned[execution.ID] = true
		}
	}
	sets, err := a.store.ListChangeSets(ctx, order.WorkspaceID)
	if err != nil {
		return nil, err
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].CreatedAt.Before(sets[j].CreatedAt) })
	var ids []string
	for _, set := range sets {
		if set.Status == domain.ChangeSetApplied && (questDescendsFrom(set.QuestID, rootID, byID) || owned[set.ExecutionID]) {
			ids = append(ids, set.ID)
		}
	}
	return ids, nil
}

// afterQuestVerdictGitV2 — шаг после сохранённого вердикта. «Выполнен» с
// проверенным итогом и политикой on_completion — коммит в ветку квеста и
// вопрос про отправку и MR. Ветки, в которые квест ничего не положил,
// убираются. Сбой коммита квест не меняет: он остаётся выполненным, а в
// карточке появляется кнопка повторить.
func (a *App) afterQuestVerdictGitV2(ctx context.Context, approval domain.WorkOrderApproval, quest domain.Quest, status domain.QuestStatus, bundle domain.EvidenceBundle) {
	order := approval.WorkOrder
	if order.Git == nil || approval.QuestID != quest.ID {
		return
	}
	defer questGitKeys.Delete(quest.ID)
	a.cleanupUnusedQuestBranchesV2(ctx, approval, bundle)
	if status != domain.QuestCompleted || order.Delivery.CommitMode != domain.CommitOnCompletion || bundle.Assurance != domain.WorkOrderAssuranceVerified {
		return
	}
	for _, repo := range order.Git.Repositories {
		state, err := a.questGitContextV2(ctx, quest.ID)
		if err != nil {
			return
		}
		if gpg := gitConfigBool(ctx, a.gitInspectRunner(), state.dirs[repo.Path], "commit.gpgsign"); gpg {
			continue
		}
		if _, err = a.RunQuestGitAction(ctx, quest.ID, "commit", repo.Path, "auto", questGitKey(quest.ID)); err != nil {
			slog.Info("quest auto commit skipped", "quest_id", quest.ID, "repo", repo.Path, "error", err)
		}
	}
	a.offerQuestPublishV2(ctx, approval, quest.ID)
}

func gitConfigBool(ctx context.Context, runner gitflow.Runner, dir, key string) bool {
	if dir == "" {
		return false
	}
	out, err := runner.Run(ctx, dir, "config", "--bool", key)
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// cleanupUnusedQuestBranchesV2 — новая ветка, в которую квест не положил ни
// одного файла, возвращается назад: человек не должен остаться на пустой ветке.
func (a *App) cleanupUnusedQuestBranchesV2(ctx context.Context, approval domain.WorkOrderApproval, bundle domain.EvidenceBundle) {
	plan := approval.WorkOrder.Git
	if plan == nil || plan.Mode != domain.GitModeNew {
		return
	}
	runner := a.gitInspectRunner()
	actions, _ := a.store.ListQuestGitActions(ctx, approval.QuestID)
	repos := map[string]string{}
	for _, repo := range gitflow.Repositories(ctx, runner, approval.WorkOrder.Workspace.Path) {
		repos[repo.Path] = repo.Dir
	}
	for _, action := range actions {
		if action.Action != "checkout" || action.Phase != "succeeded" || len(questFilesForRepoV2(bundle.ChangedFiles, action.Repo)) > 0 {
			continue
		}
		dir := repos[action.Repo]
		previous := strings.TrimPrefix(action.Message, "от ")
		if dir == "" || previous == "" {
			continue
		}
		head, _ := runner.Run(ctx, dir, "rev-parse", "HEAD")
		current, _ := runner.Run(ctx, dir, "branch", "--show-current")
		status, _ := runner.Run(ctx, dir, "status", "--porcelain=v1", "--untracked-files=no")
		if strings.TrimSpace(string(head)) != action.CommitID || strings.TrimSpace(string(current)) != action.Branch || strings.TrimSpace(string(status)) != "" {
			continue
		}
		gitflow.UndoCheckout(ctx, runner, dir, previous, action.Branch, action.CommitID)
		_, _ = a.store.AppendQuestGitAction(ctx, domain.QuestGitAction{
			QuestID: approval.QuestID, WorkOrderID: approval.WorkOrder.ID, Repo: action.Repo, Action: "cleanup", Phase: "succeeded",
			Branch: action.Branch, Message: "квест не менял этот репозиторий — возвращена ветка " + previous, Trigger: "auto",
		})
	}
}

// offerQuestPublishV2 — карточка в чате «Отправить ветку и создать MR?».
func (a *App) offerQuestPublishV2(ctx context.Context, approval domain.WorkOrderApproval, questID string) {
	state, err := a.questGitContextV2(ctx, questID)
	if err != nil || strings.TrimSpace(approval.WorkOrder.ConversationID) == "" {
		return
	}
	action, title := "", ""
	for _, item := range state.view.Repositories {
		switch {
		case containsString(item.Actions, "merge_request"):
			action, title = "merge_request", fmt.Sprintf("Отправить ветку %s и создать MR в %s?", item.Branch, item.Target)
		case action == "" && containsString(item.Actions, "push"):
			action, title = "push", fmt.Sprintf("Отправить ветку %s в origin?", item.Branch)
		}
	}
	if action == "" {
		return
	}
	a.proposeQuestGitActionV2(ctx, approval.WorkOrder, questID, action, title, "Квест выполнен и закоммичен. Отправка на сервер — только по вашему решению.")
}

func (a *App) proposeQuestGitActionV2(ctx context.Context, order domain.WorkOrder, questID, action, title, rationale string) {
	now := time.Now().UTC()
	proposal := domain.CompanionActionProposal{
		ID: domain.NewID("gitpropose"), WorkspaceID: order.WorkspaceID, Kind: domain.CompanionActionGit,
		Title: title, Rationale: rationale, Status: "pending", CreatedAt: now, UpdatedAt: now,
		Git: &domain.GitActionRequest{QuestID: questID, Action: action},
	}
	if err := a.store.SaveCompanionActionProposal(ctx, proposal); err != nil {
		slog.Warn("git action proposal not saved", "quest_id", questID, "error", err)
		return
	}
	_, _ = a.store.SaveCompanionMessageOnce(ctx, domain.CompanionMessage{
		ID: "master-git-" + proposal.ID, WorkspaceID: order.WorkspaceID, ConversationID: order.ConversationID,
		Role: "assistant", Speaker: "Git-агент", Content: title, Level: "info", ActionProposalID: proposal.ID, CreatedAt: now,
	})
}

// masterGitRequestV2 — просьба из чата («закоммить и запушь»). Мастер её
// только передал; git-агент проверяет, что действие сейчас возможно, и
// показывает карточку подтверждения. Невозможное объясняет словами.
func (a *App) masterGitRequestV2(ctx context.Context, workspaceID, conversationID string, request orchestrator.MasterGitRequest) {
	say := func(text string) {
		_ = a.store.SaveCompanionMessage(ctx, domain.CompanionMessage{ID: domain.NewID("msg"), WorkspaceID: workspaceID, ConversationID: conversationID,
			Speaker: "Git-агент", Role: "assistant", Content: text, CreatedAt: time.Now().UTC()})
	}
	questID := strings.TrimSpace(request.QuestID)
	var order domain.WorkOrder
	if questID == "" {
		orders, err := a.store.ListWorkOrdersForConversationV2(ctx, workspaceID, conversationID)
		if err != nil {
			say("Git-агент не нашёл квест разговора: " + security.Redact(err.Error()))
			return
		}
		for _, candidate := range orders {
			if candidate.Runtime != nil && candidate.Runtime.QuestID != "" &&
				(order.ID == "" || candidate.UpdatedAt.After(order.UpdatedAt)) {
				order = candidate
			}
		}
		if order.ID == "" {
			say("В этом разговоре ещё нет квеста, результат которого можно закоммитить или отправить.")
			return
		}
		questID = order.Runtime.QuestID
	}
	state, err := a.questGitContextV2(ctx, questID)
	if err != nil {
		say(err.Error())
		return
	}
	var repos []string
	for _, item := range state.view.Repositories {
		if containsString(item.Actions, request.Action) {
			repos = append(repos, item.Path+" ("+item.Branch+")")
		}
	}
	if len(repos) == 0 {
		say(fmt.Sprintf("Сейчас «%s» сделать нельзя: %s", questGitActionTitle(request.Action), questGitUnavailableReason(state.view, request.Action)))
		return
	}
	a.proposeQuestGitActionV2(ctx, state.order, questID, request.Action,
		fmt.Sprintf("%s: %s?", questGitActionTitle(request.Action), strings.Join(repos, ", ")),
		"Вы попросили в чате. Git-агент выполнит действие только после подтверждения.")
}

func questGitUnavailableReason(view *domain.QuestGitView, action string) string {
	if view == nil || len(view.Repositories) == 0 {
		return "у квеста нет плана ветки"
	}
	item := view.Repositories[0]
	switch action {
	case "commit":
		if item.CommitID != "" {
			return "результат уже закоммичен (" + shortCommit(item.CommitID) + ")"
		}
		return "у квеста нет незакоммиченных файлов или он ещё не выполнен"
	case "push":
		if item.Pushed {
			return "ветка " + item.Branch + " уже отправлена"
		}
		return "сначала нужен коммит результата"
	case "merge_request":
		switch {
		case item.MRURL != "":
			return "MR уже создан: " + item.MRURL
		case !item.GitLab:
			return "remote не похож на GitLab"
		case item.Protected:
			return "коммит лежит в защищённой ветке " + item.Branch
		}
		return "сначала нужен коммит результата в отдельной ветке"
	}
	return "действие недоступно"
}

func questGitActionTitle(action string) string {
	switch action {
	case "commit":
		return "Закоммитить"
	case "push":
		return "Отправить"
	case "merge_request":
		return "Создать MR"
	case "revert":
		return "Откатить"
	}
	return action
}

func shortCommit(commit string) string {
	if len(commit) > 10 {
		return commit[:10]
	}
	return commit
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
