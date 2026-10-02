package gitflow

import (
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
)

// Рекомендации при открытом выборе.
const (
	RecommendCurrent    = "current"
	RecommendNewCurrent = "new-current"
	RecommendNewDefault = "new-default"
)

// BuildPlan — правило владельца Point:
//   - текущая ветка защищена (main, release/…, защищена в GitLab) → git-агент
//     предлагает новую ветку от неё, человеку достаточно утвердить;
//   - не защищена → спросить: работать в ней, новая от неё или от основной;
//   - незакоммиченные правки → ветку не трогаем и не коммитим, объясняем.
//
// suggested — имя новой ветки, уже придуманное по ТЗ.
func BuildPlan(reports []RepoReport, suggested string) *domain.GitPlan {
	if len(reports) == 0 {
		return nil
	}
	plan := &domain.GitPlan{Version: "1", Repositories: make([]domain.GitRepoPlan, 0, len(reports))}
	allProtected, stale, dirty := true, false, false
	for _, report := range reports {
		repo := domain.GitRepoPlan{
			Path: report.Path, Current: report.Current, HeadCommit: report.HeadCommit,
			DefaultBranch: report.DefaultBranch, CurrentBase: report.CurrentBase, CurrentBaseCommit: report.CurrentBaseCommit,
			DefaultBase: report.DefaultRef, DefaultBaseCommit: report.DefaultCommit,
			Protected: report.Protected, ProtectedSource: report.ProtectedSource, GitLab: report.GitLab,
			Remote: report.Remote, Dirty: report.DirtyCount, Warnings: Warnings(report),
		}
		plan.Repositories = append(plan.Repositories, repo)
		if !report.Protected {
			allProtected = false
		}
		if report.UpstreamGone || (report.MergedIntoDefault && report.Upstream != "") || (report.BehindDefault > 0 && report.Current != report.DefaultBranch) {
			stale = true
		}
		if report.DirtyCount > 0 {
			dirty = true
			plan.Notes = append(plan.Notes, fmt.Sprintf("%s: незакоммиченные изменения (%d) — ветку не переключаем, коммита квеста не будет. Закоммитьте или спрячьте их и обновите карточку.", report.Path, report.DirtyCount))
		}
		if report.FetchError != "" {
			plan.Notes = append(plan.Notes, fmt.Sprintf("%s: сведения с сервера не обновились (%s) — ветки показаны на момент прошлого fetch.", report.Path, clip(report.FetchError, 160)))
		}
		if report.IdentityName == "" || report.IdentityEmail == "" {
			plan.Notes = append(plan.Notes, fmt.Sprintf("%s: Git не знает автора — задайте `git config user.name` и `user.email`, иначе коммит не будет создан.", report.Path))
		}
		if report.GPGSign {
			plan.Notes = append(plan.Notes, fmt.Sprintf("%s: коммиты подписываются GPG — сам Point не коммитит, в конце будет кнопка «Закоммитить».", report.Path))
		}
	}
	plan.Branch = UniqueBranchName(suggested, reports)
	switch {
	case dirty:
		plan.Choice, plan.Mode, plan.Branch = domain.GitChoiceProposed, domain.GitModeNone, ""
	case allProtected:
		plan.Choice, plan.Mode, plan.BaseKind = domain.GitChoiceProposed, domain.GitModeNew, "current"
		for _, report := range reports {
			// От отсоединённого HEAD ответвляются от основной ветки.
			if report.Detached {
				plan.BaseKind = "default"
			}
		}
	default:
		plan.Choice = domain.GitChoiceRequired
		plan.Recommended = RecommendCurrent
		if stale {
			plan.Recommended = RecommendNewDefault
		}
	}
	return plan
}

// ApplyChoice — выбор человека поверх плана git-агента: режим, база и имя.
// Сервер проверяет имя и наличие основы; осмотр (коммиты, защита) остаётся
// серверным.
func ApplyChoice(plan *domain.GitPlan, choice string, branch string) error {
	if plan == nil {
		return fmt.Errorf("у наряда нет плана ветки")
	}
	branch = strings.TrimSpace(branch)
	next := *plan
	switch choice {
	case RecommendCurrent:
		for _, repo := range plan.Repositories {
			if repo.Current == "" {
				return fmt.Errorf("%s: HEAD отсоединён — работать «в текущей ветке» нельзя", repo.Path)
			}
		}
		next.Mode, next.BaseKind = domain.GitModeCurrent, ""
	case RecommendNewCurrent, RecommendNewDefault:
		next.Mode, next.BaseKind = domain.GitModeNew, "current"
		if choice == RecommendNewDefault {
			next.BaseKind = "default"
		}
		if branch == "" {
			branch = plan.Branch
		}
		if !domain.ValidGitBranchName(branch) {
			return fmt.Errorf("имя ветки %q недопустимо: латиница, цифры и «._/-»", branch)
		}
		next.Branch = branch
	case domain.GitModeNone:
		next.Mode, next.BaseKind = domain.GitModeNone, ""
	default:
		return fmt.Errorf("неизвестный выбор ветки %q", choice)
	}
	if next.Mode != domain.GitModeNew {
		next.Branch = plan.Branch
	}
	next.Choice = domain.GitChoiceChosen
	if err := domain.GitPlanApprovalError(&next); err != nil {
		return err
	}
	*plan = next
	return nil
}

// Warnings — что человеку стоит знать о текущей ветке, словами.
func Warnings(report RepoReport) []string {
	var out []string
	current := report.Current
	switch {
	case report.Detached:
		out = append(out, "HEAD отсоединён: работать можно только в новой ветке")
	case report.UpstreamGone:
		out = append(out, fmt.Sprintf("ветка %s удалена на сервере (%s)", current, report.Upstream))
	}
	if !report.Detached && report.MergedIntoDefault && report.Upstream != "" && report.DefaultBranch != "" {
		out = append(out, fmt.Sprintf("ветка %s уже влита в %s", current, report.DefaultBranch))
	}
	if !report.Detached && current != report.DefaultBranch && report.BehindDefault > 0 && report.DefaultRef != "" {
		out = append(out, fmt.Sprintf("отстаёт от %s на %s", report.DefaultRef, commits(report.BehindDefault)))
	}
	if !report.Detached && report.Behind > 0 && !report.UpstreamGone {
		out = append(out, fmt.Sprintf("локальная %s отстаёт от %s на %s", current, report.Upstream, commits(report.Behind)))
	}
	if report.Remote == "" {
		out = append(out, "нет remote origin — отправить и создать MR будет некуда")
	}
	if report.Protected {
		switch report.ProtectedSource {
		case "gitlab":
			out = append(out, fmt.Sprintf("ветка %s защищена в GitLab", current))
		default:
			if !report.Detached {
				out = append(out, fmt.Sprintf("ветка %s основная — работать в ней напрямую не стоит", current))
			}
		}
	}
	return out
}

// UniqueBranchName — имя, которого ещё нет ни локально, ни на сервере ни в
// одном репозитории наряда.
func UniqueBranchName(name string, reports []RepoReport) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	taken := map[string]bool{}
	for _, report := range reports {
		for _, branch := range report.Branches {
			taken[branch] = true
		}
	}
	candidate := name
	for index := 2; taken[candidate] && index < 100; index++ {
		candidate = fmt.Sprintf("%s-%d", name, index)
	}
	return candidate
}

func commits(count int) string {
	mod10, mod100 := count%10, count%100
	switch {
	case mod10 == 1 && mod100 != 11:
		return fmt.Sprintf("%d коммит", count)
	case mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14):
		return fmt.Sprintf("%d коммита", count)
	default:
		return fmt.Sprintf("%d коммитов", count)
	}
}
