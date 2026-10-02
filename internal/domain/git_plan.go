package domain

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

// GitPlan — решение git-агента о ветке, утверждаемое вместе с нарядом.
//
// Раньше наряд ветки не знал вовсе: квест коммитил в ту, что была открыта, —
// и однажды это оказалась ветка, уже влитая в main и удалённая на сервере.
// Теперь ветку выбирают до работы: защищённую (main, release/…) git-агент
// предлагает не трогать и завести новую по ТЗ, про незащищённую спрашивает.
// Переключение делает утверждение наряда, до первого снимка песочницы.
//
// Указатель с omitempty: наряд без плана сериализуется как прежде, и дайджест
// утверждённых ранее нарядов не меняется.
type GitPlan struct {
	Version string `json:"version"`
	// Choice: proposed — git-агент предложил, человеку достаточно утвердить;
	// required — без выбора человека наряд не утверждается; chosen — выбрал
	// человек.
	Choice string `json:"choice"`
	// Mode — current: работать в текущей ветке; new: новая ветка Branch от
	// BaseKind; none: ветку не трогать (грязное дерево, отключённый git).
	Mode     string `json:"mode,omitempty"`
	BaseKind string `json:"baseKind,omitempty"` // current | default
	Branch   string `json:"branch,omitempty"`
	// Recommended — что git-агент советует при Choice=required; карточка
	// показывает его выбранным, но утверждение всё равно ждёт человека.
	Recommended  string        `json:"recommended,omitempty"`
	Repositories []GitRepoPlan `json:"repositories"`
	Notes        []string      `json:"notes,omitempty"`
}

// GitRepoPlan — осмотр одного репозитория наряда. Path — корень репозитория
// относительно папки проекта через «/», "." — сама папка.
type GitRepoPlan struct {
	Path              string   `json:"path"`
	Current           string   `json:"current,omitempty"`
	HeadCommit        string   `json:"headCommit"`
	DefaultBranch     string   `json:"defaultBranch,omitempty"`
	CurrentBase       string   `json:"currentBase,omitempty"`
	CurrentBaseCommit string   `json:"currentBaseCommit,omitempty"`
	DefaultBase       string   `json:"defaultBase,omitempty"`
	DefaultBaseCommit string   `json:"defaultBaseCommit,omitempty"`
	Protected         bool     `json:"protected,omitempty"`
	ProtectedSource   string   `json:"protectedSource,omitempty"` // gitlab | name
	GitLab            bool     `json:"gitlab,omitempty"`
	Remote            string   `json:"remote,omitempty"`
	Dirty             int      `json:"dirty,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
}

const (
	GitChoiceProposed = "proposed"
	GitChoiceRequired = "required"
	GitChoiceChosen   = "chosen"
	GitModeCurrent    = "current"
	GitModeNew        = "new"
	GitModeNone       = "none"
)

var gitBranchNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,119}$`)

// ValidGitBranchName — то, что заведомо примет `git check-ref-format --branch`
// и что безопасно показывать: латиница, цифры, «._/-», без «..», «//», «.lock».
func ValidGitBranchName(name string) bool {
	if !gitBranchNamePattern.MatchString(name) || strings.Contains(name, "..") || strings.Contains(name, "//") ||
		strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") || strings.HasSuffix(name, ".lock") || strings.Contains(name, "/.") {
		return false
	}
	return true
}

// ValidateGitPlan — форма плана. Решение «можно ли утверждать» — отдельно, в
// GitPlanApprovalError: черновик с открытым выбором сохранять можно.
func ValidateGitPlan(plan *GitPlan) error {
	if plan == nil {
		return nil
	}
	switch plan.Choice {
	case GitChoiceProposed, GitChoiceRequired, GitChoiceChosen:
	default:
		return fmt.Errorf("git plan choice %q is invalid", plan.Choice)
	}
	switch plan.Mode {
	case "", GitModeCurrent, GitModeNone:
	case GitModeNew:
		if plan.BaseKind != "current" && plan.BaseKind != "default" {
			return errors.New("новая ветка требует базу: current или default")
		}
		if !ValidGitBranchName(plan.Branch) {
			return fmt.Errorf("имя ветки %q недопустимо", plan.Branch)
		}
	default:
		return fmt.Errorf("git plan mode %q is invalid", plan.Mode)
	}
	if len(plan.Repositories) == 0 || len(plan.Repositories) > 32 {
		return errors.New("git plan must name 1-32 repositories")
	}
	seen := map[string]bool{}
	for _, repo := range plan.Repositories {
		clean := path.Clean(repo.Path)
		if repo.Path == "" || clean != repo.Path || strings.HasPrefix(clean, "../") || clean == ".." || path.IsAbs(clean) || seen[clean] {
			return fmt.Errorf("git plan repository path %q is invalid", repo.Path)
		}
		seen[clean] = true
	}
	return nil
}

// GitPlanApprovalError — почему наряд с этим планом утверждать нельзя.
func GitPlanApprovalError(plan *GitPlan) error {
	if plan == nil {
		return nil
	}
	if plan.Choice == GitChoiceRequired || plan.Mode == "" {
		return errors.New("выберите ветку в разделе «Ветка» карточки наряда")
	}
	if plan.Mode != GitModeNew {
		return nil
	}
	for _, repo := range plan.Repositories {
		base := repo.CurrentBaseCommit
		if plan.BaseKind == "default" {
			base = repo.DefaultBaseCommit
		}
		if strings.TrimSpace(base) == "" {
			return fmt.Errorf("%s: нет основы для новой ветки (%s)", repo.Path, plan.BaseKind)
		}
	}
	return nil
}

// GitRepoBase — ссылка и коммит, от которых в этом репозитории пойдёт новая
// ветка по выбранной базе.
func GitRepoBase(plan GitPlan, repo GitRepoPlan) (string, string) {
	if plan.BaseKind == "default" {
		return repo.DefaultBase, repo.DefaultBaseCommit
	}
	return repo.CurrentBase, repo.CurrentBaseCommit
}

// GitPlanTargetBranch — ветка, куда ляжет коммит квеста в репозитории.
func GitPlanTargetBranch(plan *GitPlan, repoPath string) string {
	if plan == nil {
		return ""
	}
	if plan.Mode == GitModeNew {
		return plan.Branch
	}
	for _, repo := range plan.Repositories {
		if repo.Path == repoPath {
			return repo.Current
		}
	}
	return ""
}

// Режимы коммита нарядов с планом ветки. Старые squash/none остаются за
// нарядами, утверждёнными до git-агента: шлюз доказательств требует у squash
// коммит в квитанции, а новые режимы коммитят уже после вердикта «выполнен»,
// отдельным действием, и шлюз не трогают.
const (
	CommitOnCompletion = "on_completion"
	CommitOnRequest    = "on_request"
)

// ValidCommitMode — все режимы, которые встречались в нарядах.
func ValidCommitMode(mode string) bool {
	switch mode {
	case "", "squash", "staged", "none", CommitOnCompletion, CommitOnRequest:
		return true
	}
	return false
}

// QuestGitAction — запись журнала git-действий квеста. Журнал только
// дописывается: started без пары означает оборванное действие.
type QuestGitAction struct {
	ID          string    `json:"id"`
	OpID        string    `json:"opId"`
	QuestID     string    `json:"questId"`
	WorkOrderID string    `json:"workOrderId,omitempty"`
	Repo        string    `json:"repo"`
	Action      string    `json:"action"` // checkout | commit | push | merge_request | revert | cleanup
	Phase       string    `json:"phase"`  // started | succeeded | failed
	Branch      string    `json:"branch,omitempty"`
	Base        string    `json:"base,omitempty"`
	CommitID    string    `json:"commitId,omitempty"`
	Remote      string    `json:"remote,omitempty"`
	MRURL       string    `json:"mrUrl,omitempty"`
	Message     string    `json:"message,omitempty"`
	Error       string    `json:"error,omitempty"`
	Trigger     string    `json:"trigger,omitempty"` // auto | button | chat
	CreatedAt   time.Time `json:"createdAt"`
}

// QuestGitView — производное состояние git квеста для карточки: что сделано
// и какие кнопки сейчас имеют смысл. В дайджест наряда не входит.
type QuestGitView struct {
	CommitMode   string             `json:"commitMode"`
	Repositories []QuestGitRepoView `json:"repositories"`
	Notes        []string           `json:"notes,omitempty"`
}

type QuestGitRepoView struct {
	Path          string   `json:"path"`
	Branch        string   `json:"branch,omitempty"`
	Target        string   `json:"target,omitempty"` // база для MR
	Protected     bool     `json:"protected,omitempty"`
	GitLab        bool     `json:"gitlab,omitempty"`
	CommitID      string   `json:"commitId,omitempty"`
	CommitSubject string   `json:"commitSubject,omitempty"`
	Pushed        bool     `json:"pushed,omitempty"`
	MRURL         string   `json:"mrUrl,omitempty"`
	Reverted      bool     `json:"reverted,omitempty"`
	LastError     string   `json:"lastError,omitempty"`
	Actions       []string `json:"actions,omitempty"`
}
