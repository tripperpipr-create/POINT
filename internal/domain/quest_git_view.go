package domain

import "strings"

// BuildQuestGitView — состояние git квеста для карточки по плану ветки,
// итогу квеста и журналу действий. Кнопки появляются только там, где действие
// сейчас имеет смысл: закоммитить — есть файлы квеста и нет коммита;
// отправить — есть коммит; MR — коммит в незащищённой ветке GitLab-проекта;
// откатить — файлы квеста лежат без коммита.
func BuildQuestGitView(order WorkOrder, status QuestStatus, assurance string, changedFiles []string, actions []QuestGitAction) *QuestGitView {
	plan := order.Git
	if plan == nil {
		return nil
	}
	view := &QuestGitView{CommitMode: order.Delivery.CommitMode, Notes: append([]string(nil), plan.Notes...)}
	terminal := status == QuestCompleted || status == QuestBlocked || status == QuestCancelled || status == QuestNeedsReview
	for _, repo := range plan.Repositories {
		item := QuestGitRepoView{Path: repo.Path, Branch: GitPlanTargetBranch(plan, repo.Path), GitLab: repo.GitLab}
		switch plan.Mode {
		case GitModeNew:
			item.Target = repo.Current
			if plan.BaseKind == "default" || item.Target == "" {
				item.Target = repo.DefaultBranch
			}
		case GitModeCurrent:
			item.Protected = repo.Protected
			if repo.Current != repo.DefaultBranch {
				item.Target = repo.DefaultBranch
			}
		}
		hasFiles := false
		for _, file := range changedFiles {
			if repo.Path == "." || file == repo.Path || strings.HasPrefix(file, repo.Path+"/") {
				hasFiles = true
				break
			}
		}
		pushedCommit := ""
		for _, action := range actions {
			if action.Repo != repo.Path {
				continue
			}
			if action.Phase == "failed" {
				item.LastError = action.Error
				continue
			}
			if action.Phase != "succeeded" {
				continue
			}
			item.LastError = ""
			switch action.Action {
			case "checkout":
				if action.Branch != "" {
					item.Branch = action.Branch
				}
			case "commit":
				item.CommitID = action.CommitID
				item.CommitSubject = strings.TrimSpace(strings.SplitN(action.Message, "\n", 2)[0])
				item.Reverted = false
			case "push":
				pushedCommit = action.CommitID
			case "merge_request":
				pushedCommit = action.CommitID
				if action.MRURL != "" {
					item.MRURL = action.MRURL
				}
			case "revert":
				item.Reverted = true
			}
		}
		item.Pushed = item.CommitID != "" && pushedCommit == item.CommitID
		if terminal && plan.Mode != GitModeNone && plan.Mode != "" {
			uncommitted := hasFiles && item.CommitID == "" && !item.Reverted
			if uncommitted && status == QuestCompleted {
				item.Actions = append(item.Actions, "commit")
			}
			if item.CommitID != "" && !item.Pushed {
				item.Actions = append(item.Actions, "push")
			}
			if item.CommitID != "" && item.GitLab && !item.Protected && item.Target != "" && item.Target != item.Branch && item.MRURL == "" {
				item.Actions = append(item.Actions, "merge_request")
			}
			if uncommitted {
				item.Actions = append(item.Actions, "revert")
			}
		}
		if assurance != "" && assurance != WorkOrderAssuranceVerified && status == QuestCompleted && item.CommitID == "" && hasFiles && order.Delivery.CommitMode == CommitOnCompletion {
			view.Notes = append(view.Notes, repo.Path+": итог с ограничениями — сам Point его не коммитил, решите кнопкой «Закоммитить».")
		}
		view.Repositories = append(view.Repositories, item)
	}
	return view
}
