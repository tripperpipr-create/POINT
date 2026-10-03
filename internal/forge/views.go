package forge

import "time"

type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

type ReviewSummary struct {
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

type Review struct {
	ReviewSummary
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

type LinePosition struct {
	OldPath string `json:"oldPath,omitempty"`
	NewPath string `json:"newPath,omitempty"`
	OldLine int    `json:"oldLine,omitempty"`
	NewLine int    `json:"newLine,omitempty"`
}

type Note struct {
	ID         int           `json:"id"`
	Author     User          `json:"author"`
	Body       string        `json:"body"`
	System     bool          `json:"system"`
	Resolvable bool          `json:"resolvable,omitempty"`
	Resolved   bool          `json:"resolved,omitempty"`
	CreatedAt  time.Time     `json:"createdAt"`
	Position   *LinePosition `json:"position,omitempty"`
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
