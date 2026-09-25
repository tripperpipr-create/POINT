package gitlab

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Модели экранов. Поля — то, что показывает окно GitLab и карточка MR; всё
// остальное из ответа сервера отбрасывается. Текст из GitLab — недоверенный:
// он режется по потолкам здесь и экранируется или проходит разбор markdown
// в webview.
const (
	maxDescription = 64 << 10
	maxNoteBody    = 16 << 10
	maxDiff        = 512 << 10
	maxFileContent = 1 << 20
	maxJobLog      = 256 << 10
	maxTitle       = 500
)

type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

type MergeRequest struct {
	ProjectID      int       `json:"projectId"`
	ProjectPath    string    `json:"projectPath,omitempty"`
	IID            int       `json:"iid"`
	Title          string    `json:"title"`
	State          string    `json:"state"`
	Draft          bool      `json:"draft"`
	SourceBranch   string    `json:"sourceBranch"`
	TargetBranch   string    `json:"targetBranch"`
	Author         User      `json:"author"`
	Assignees      []User    `json:"assignees,omitempty"`
	Reviewers      []User    `json:"reviewers,omitempty"`
	MergeStatus    string    `json:"mergeStatus,omitempty"`
	SHA            string    `json:"sha,omitempty"`
	WebURL         string    `json:"webUrl,omitempty"`
	UpdatedAt      time.Time `json:"updatedAt"`
	CreatedAt      time.Time `json:"createdAt"`
	HasConflicts   bool      `json:"hasConflicts,omitempty"`
	BlockingThread bool      `json:"blockingThreads,omitempty"`
}

type DiffRefs struct {
	BaseSHA  string `json:"baseSha"`
	HeadSHA  string `json:"headSha"`
	StartSHA string `json:"startSha"`
}

type MergeRequestDetail struct {
	MergeRequest
	Description  string   `json:"description"`
	DescTrimmed  bool     `json:"descriptionTrimmed,omitempty"`
	DiffRefs     DiffRefs `json:"diffRefs"`
	ChangesCount string   `json:"changesCount,omitempty"`
	MergeError   string   `json:"mergeError,omitempty"`
	MergedBy     *User    `json:"mergedBy,omitempty"`
	RemoveSource bool     `json:"removeSourceBranch,omitempty"`
}

type ApprovalRule struct {
	Name       string `json:"name"`
	Required   int    `json:"required"`
	Approved   bool   `json:"approved"`
	ApprovedBy []User `json:"approvedBy,omitempty"`
}

type Approvals struct {
	Rules []ApprovalRule `json:"rules"`
	// ApprovedBy — все одобрившие по всем правилам, без повторов.
	ApprovedBy []User `json:"approvedBy,omitempty"`
}

type Position struct {
	OldPath string `json:"oldPath,omitempty"`
	NewPath string `json:"newPath,omitempty"`
	OldLine int    `json:"oldLine,omitempty"`
	NewLine int    `json:"newLine,omitempty"`
}

type Note struct {
	ID         int       `json:"id"`
	Author     User      `json:"author"`
	Body       string    `json:"body"`
	System     bool      `json:"system"`
	Resolvable bool      `json:"resolvable,omitempty"`
	Resolved   bool      `json:"resolved,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	Position   *Position `json:"position,omitempty"`
}

type Discussion struct {
	ID         string `json:"id"`
	Individual bool   `json:"individual"`
	Notes      []Note `json:"notes"`
	Resolvable bool   `json:"resolvable,omitempty"`
	Resolved   bool   `json:"resolved,omitempty"`
}

type ChangedFile struct {
	OldPath string `json:"oldPath"`
	NewPath string `json:"newPath"`
	New     bool   `json:"new,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
	Renamed bool   `json:"renamed,omitempty"`
}

type FileDiff struct {
	OldPath string `json:"oldPath"`
	NewPath string `json:"newPath"`
	Diff    string `json:"diff"`
	Trimmed bool   `json:"trimmed,omitempty"`
}

type FileContent struct {
	Path    string `json:"path"`
	Ref     string `json:"ref"`
	Content string `json:"content"`
	Binary  bool   `json:"binary,omitempty"`
	TooBig  bool   `json:"tooBig,omitempty"`
	Missing bool   `json:"missing,omitempty"`
}

type Pipeline struct {
	ID        int       `json:"id"`
	Status    string    `json:"status"`
	Ref       string    `json:"ref"`
	SHA       string    `json:"sha"`
	Source    string    `json:"source,omitempty"`
	WebURL    string    `json:"webUrl,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Duration  float64   `json:"duration,omitempty"`
	User      *User     `json:"user,omitempty"`
}

type Job struct {
	ID            int       `json:"id"`
	Name          string    `json:"name"`
	Stage         string    `json:"stage"`
	Status        string    `json:"status"`
	Duration      float64   `json:"duration,omitempty"`
	FailureReason string    `json:"failureReason,omitempty"`
	AllowFailure  bool      `json:"allowFailure,omitempty"`
	WebURL        string    `json:"webUrl,omitempty"`
	StartedAt     time.Time `json:"startedAt,omitempty"`
}

type JobLog struct {
	JobID   int    `json:"jobId"`
	Text    string `json:"text"`
	Trimmed bool   `json:"trimmed,omitempty"`
}

// Сырые формы ответа сервера — поля REST API GitLab.
type rawUser struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

func (u rawUser) view() User {
	return User{ID: u.ID, Username: clip(u.Username, 100), Name: clip(u.Name, 200)}
}

func users(list []rawUser) []User {
	if len(list) == 0 {
		return nil
	}
	out := make([]User, 0, len(list))
	for _, item := range list {
		out = append(out, item.view())
	}
	return out
}

type rawMergeRequest struct {
	ID                          int       `json:"id"`
	IID                         int       `json:"iid"`
	ProjectID                   int       `json:"project_id"`
	Title                       string    `json:"title"`
	Description                 string    `json:"description"`
	State                       string    `json:"state"`
	Draft                       bool      `json:"draft"`
	WorkInProgress              bool      `json:"work_in_progress"`
	SourceBranch                string    `json:"source_branch"`
	TargetBranch                string    `json:"target_branch"`
	Author                      rawUser   `json:"author"`
	Assignees                   []rawUser `json:"assignees"`
	Reviewers                   []rawUser `json:"reviewers"`
	DetailedMergeStatus         string    `json:"detailed_merge_status"`
	MergeStatus                 string    `json:"merge_status"`
	HasConflicts                bool      `json:"has_conflicts"`
	BlockingDiscussionsResolved *bool     `json:"blocking_discussions_resolved"`
	SHA                         string    `json:"sha"`
	WebURL                      string    `json:"web_url"`
	CreatedAt                   time.Time `json:"created_at"`
	UpdatedAt                   time.Time `json:"updated_at"`
	DiffRefs                    *struct {
		BaseSHA  string `json:"base_sha"`
		HeadSHA  string `json:"head_sha"`
		StartSHA string `json:"start_sha"`
	} `json:"diff_refs"`
	ChangesCount       string   `json:"changes_count"`
	MergeError         string   `json:"merge_error"`
	MergedBy           *rawUser `json:"merged_by"`
	ShouldRemoveSource *bool    `json:"should_remove_source_branch"`
	ForceRemoveSource  *bool    `json:"force_remove_source_branch"`
	References         *struct {
		Full string `json:"full"`
	} `json:"references"`
}

func (c *Client) mergeRequest(raw rawMergeRequest) MergeRequest {
	status := raw.DetailedMergeStatus
	if status == "" {
		status = raw.MergeStatus
	}
	view := MergeRequest{
		ProjectID: raw.ProjectID, IID: raw.IID, Title: clip(raw.Title, maxTitle), State: raw.State,
		Draft: raw.Draft || raw.WorkInProgress, SourceBranch: clip(raw.SourceBranch, 255), TargetBranch: clip(raw.TargetBranch, 255),
		Author: raw.Author.view(), Assignees: users(raw.Assignees), Reviewers: users(raw.Reviewers), MergeStatus: status,
		SHA: raw.SHA, WebURL: c.sameOrigin(raw.WebURL), CreatedAt: raw.CreatedAt, UpdatedAt: raw.UpdatedAt,
		HasConflicts: raw.HasConflicts,
	}
	if raw.BlockingDiscussionsResolved != nil {
		view.BlockingThread = !*raw.BlockingDiscussionsResolved
	}
	if raw.References != nil {
		if path, _, ok := strings.Cut(raw.References.Full, "!"); ok {
			view.ProjectPath = clip(path, 255)
		}
	}
	return view
}

func (c *Client) mergeRequestDetail(raw rawMergeRequest) MergeRequestDetail {
	detail := MergeRequestDetail{MergeRequest: c.mergeRequest(raw), ChangesCount: raw.ChangesCount, MergeError: clip(raw.MergeError, 2000)}
	detail.Description, detail.DescTrimmed = clipFlag(raw.Description, maxDescription)
	if raw.DiffRefs != nil {
		detail.DiffRefs = DiffRefs{BaseSHA: raw.DiffRefs.BaseSHA, HeadSHA: raw.DiffRefs.HeadSHA, StartSHA: raw.DiffRefs.StartSHA}
	}
	if raw.MergedBy != nil {
		user := raw.MergedBy.view()
		detail.MergedBy = &user
	}
	if raw.ForceRemoveSource != nil && *raw.ForceRemoveSource || raw.ShouldRemoveSource != nil && *raw.ShouldRemoveSource {
		detail.RemoveSource = true
	}
	return detail
}

type rawNote struct {
	ID         int       `json:"id"`
	Body       string    `json:"body"`
	Author     rawUser   `json:"author"`
	System     bool      `json:"system"`
	Resolvable bool      `json:"resolvable"`
	Resolved   bool      `json:"resolved"`
	CreatedAt  time.Time `json:"created_at"`
	Position   *struct {
		OldPath string `json:"old_path"`
		NewPath string `json:"new_path"`
		OldLine *int   `json:"old_line"`
		NewLine *int   `json:"new_line"`
	} `json:"position"`
}

func noteView(raw rawNote) Note {
	note := Note{ID: raw.ID, Author: raw.Author.view(), Body: clip(raw.Body, maxNoteBody), System: raw.System,
		Resolvable: raw.Resolvable, Resolved: raw.Resolved, CreatedAt: raw.CreatedAt}
	if raw.Position != nil {
		position := Position{OldPath: clip(raw.Position.OldPath, 1000), NewPath: clip(raw.Position.NewPath, 1000)}
		if raw.Position.OldLine != nil {
			position.OldLine = *raw.Position.OldLine
		}
		if raw.Position.NewLine != nil {
			position.NewLine = *raw.Position.NewLine
		}
		note.Position = &position
	}
	return note
}

type rawPipeline struct {
	ID        int       `json:"id"`
	Status    string    `json:"status"`
	Ref       string    `json:"ref"`
	SHA       string    `json:"sha"`
	Source    string    `json:"source"`
	WebURL    string    `json:"web_url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Duration  *float64  `json:"duration"`
	User      *rawUser  `json:"user"`
}

func (c *Client) pipeline(raw rawPipeline) Pipeline {
	view := Pipeline{ID: raw.ID, Status: raw.Status, Ref: clip(raw.Ref, 255), SHA: raw.SHA, Source: raw.Source,
		WebURL: c.sameOrigin(raw.WebURL), CreatedAt: raw.CreatedAt, UpdatedAt: raw.UpdatedAt}
	if raw.Duration != nil {
		view.Duration = *raw.Duration
	}
	if raw.User != nil {
		user := raw.User.view()
		view.User = &user
	}
	return view
}

type rawJob struct {
	ID            int       `json:"id"`
	Name          string    `json:"name"`
	Stage         string    `json:"stage"`
	Status        string    `json:"status"`
	Duration      *float64  `json:"duration"`
	FailureReason string    `json:"failure_reason"`
	AllowFailure  bool      `json:"allow_failure"`
	WebURL        string    `json:"web_url"`
	StartedAt     time.Time `json:"started_at"`
}

func (c *Client) job(raw rawJob) Job {
	view := Job{ID: raw.ID, Name: clip(raw.Name, 255), Stage: clip(raw.Stage, 255), Status: raw.Status,
		FailureReason: clip(raw.FailureReason, 255), AllowFailure: raw.AllowFailure, WebURL: c.sameOrigin(raw.WebURL), StartedAt: raw.StartedAt}
	if raw.Duration != nil {
		view.Duration = *raw.Duration
	}
	return view
}

// ansi — управляющие последовательности терминала в логе джоба: цвета,
// перемещения курсора и секции GitLab (\x1b[0K section_start:…).
var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07]*\x07|\r`)

func cleanLog(text string) string {
	text = ansi.ReplaceAllString(text, "")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r >= 0x20 && r != 0x7f {
			return r
		}
		return -1
	}, text)
}

// tail — последние limit байт по границе строки: у упавшего джоба причина в
// конце лога.
func tail(text string, limit int) (string, bool) {
	if len(text) <= limit {
		return text, false
	}
	cut := text[len(text)-limit:]
	for !utf8.ValidString(cut[:min(len(cut), 4)]) && len(cut) > 0 {
		cut = cut[1:]
	}
	if index := strings.IndexByte(cut, '\n'); index >= 0 && index < len(cut)-1 {
		cut = cut[index+1:]
	}
	return cut, true
}

func clip(text string, limit int) string {
	out, _ := clipFlag(text, limit)
	return out
}

func clipFlag(text string, limit int) (string, bool) {
	if len(text) <= limit {
		return text, false
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit], true
}

// decodeText разбирает JSON из текста результата. Сервер отдаёт один объект
// или массив в первой текстовой части.
func decodeText(text string, target any) error {
	return json.Unmarshal([]byte(strings.TrimSpace(text)), target)
}
