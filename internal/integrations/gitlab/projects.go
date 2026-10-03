package gitlab

// Проекты GitLab: список, карточка, коммиты, ветки и дерево файлов. Всё —
// чтение: клон делает git на машине владельца, а не сервер плагина.

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxCommitMessage = 16 << 10
	maxDescriptionLn = 2000
	projectsPerPage  = 50
	commitsPerPage   = 30
	branchesPerPage  = 100
	treePerPage      = 100
)

// ProjectScope — какие проекты показать.
type ProjectScope string

const (
	// ProjectsMember — проекты, где владелец участник: обычный список «мои».
	ProjectsMember ProjectScope = "member"
	// ProjectsOwned — только свои. Отмеченных звёздочкой схема list_projects
	// сервера 2.1.66 не знает: лишний аргумент zod молча отбросил бы.
	ProjectsOwned ProjectScope = "owned"
)

// Project — проект для списка и карточки. Адреса клона проверены: HTTPS — тот
// же GitLab, SSH — тот же узел или его поддомен и без опасных знаков.
type Project struct {
	ID             int       `json:"id"`
	Path           string    `json:"path"`
	Name           string    `json:"name"`
	Namespace      string    `json:"namespace,omitempty"`
	Description    string    `json:"description,omitempty"`
	Visibility     string    `json:"visibility,omitempty"`
	DefaultBranch  string    `json:"defaultBranch,omitempty"`
	WebURL         string    `json:"webUrl,omitempty"`
	HTTPURL        string    `json:"httpUrl,omitempty"`
	SSHURL         string    `json:"sshUrl,omitempty"`
	LastActivityAt time.Time `json:"lastActivityAt"`
	Stars          int       `json:"stars,omitempty"`
	Forks          int       `json:"forks,omitempty"`
	OpenIssues     int       `json:"openIssues,omitempty"`
	Topics         []string  `json:"topics,omitempty"`
	Archived       bool      `json:"archived,omitempty"`
	// AccessLevel — уровень доступа владельца: 10 гость … 50 владелец.
	AccessLevel int `json:"accessLevel,omitempty"`
}

type CommitStats struct {
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}

type Commit struct {
	ID          string       `json:"id"`
	ShortID     string       `json:"shortId"`
	Title       string       `json:"title"`
	Message     string       `json:"message,omitempty"`
	AuthorName  string       `json:"authorName"`
	AuthoredAt  time.Time    `json:"authoredAt"`
	CommittedAt time.Time    `json:"committedAt"`
	ParentIDs   []string     `json:"parentIds,omitempty"`
	WebURL      string       `json:"webUrl,omitempty"`
	Stats       *CommitStats `json:"stats,omitempty"`
}

// CommitFile — файл коммита со счётчиками строк. Сам diff окно не рисует:
// файл открывается сравнением IDE родителя и коммита.
type CommitFile struct {
	ChangedFile
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}

type CommitDetail struct {
	Commit
	Files []CommitFile `json:"files"`
	// FilesTrimmed — GitLab отдал только первую страницу файлов.
	FilesTrimmed bool `json:"filesTrimmed,omitempty"`
}

type Branch struct {
	Name      string `json:"name"`
	Default   bool   `json:"default,omitempty"`
	Protected bool   `json:"protected,omitempty"`
	Merged    bool   `json:"merged,omitempty"`
	CanPush   bool   `json:"canPush,omitempty"`
	Commit    Commit `json:"commit"`
	WebURL    string `json:"webUrl,omitempty"`
}

type TreeEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Type — tree (папка), blob (файл) или commit (подмодуль).
	Type string `json:"type"`
	Mode string `json:"mode,omitempty"`
}

type Tree struct {
	Path    string      `json:"path"`
	Ref     string      `json:"ref,omitempty"`
	Entries []TreeEntry `json:"entries"`
	// Trimmed — в папке больше записей, чем показано.
	Trimmed bool `json:"trimmed,omitempty"`
}

type rawProject struct {
	ID                jsonInt  `json:"id"`
	Name              string   `json:"name"`
	PathWithNamespace string   `json:"path_with_namespace"`
	Description       *string  `json:"description"`
	Visibility        string   `json:"visibility"`
	DefaultBranch     *string  `json:"default_branch"`
	WebURL            string   `json:"web_url"`
	HTTPURL           string   `json:"http_url_to_repo"`
	SSHURL            string   `json:"ssh_url_to_repo"`
	LastActivityAt    string   `json:"last_activity_at"`
	StarCount         jsonInt  `json:"star_count"`
	ForksCount        jsonInt  `json:"forks_count"`
	OpenIssuesCount   jsonInt  `json:"open_issues_count"`
	Topics            []string `json:"topics"`
	TagList           []string `json:"tag_list"`
	Archived          bool     `json:"archived"`
	Namespace         *struct {
		Name     string `json:"name"`
		FullPath string `json:"full_path"`
	} `json:"namespace"`
	Permissions *struct {
		ProjectAccess *struct {
			AccessLevel jsonInt `json:"access_level"`
		} `json:"project_access"`
		GroupAccess *struct {
			AccessLevel jsonInt `json:"access_level"`
		} `json:"group_access"`
	} `json:"permissions"`
}

func (c *Client) project(raw rawProject) Project {
	view := Project{ID: int(raw.ID), Path: clip(raw.PathWithNamespace, 255), Name: clip(raw.Name, 255),
		Visibility: clip(raw.Visibility, 20), WebURL: c.sameOrigin(raw.WebURL), HTTPURL: c.sameOrigin(raw.HTTPURL),
		SSHURL: c.sshURL(raw.SSHURL), LastActivityAt: parseTime(raw.LastActivityAt), Stars: int(raw.StarCount),
		Forks: int(raw.ForksCount), OpenIssues: int(raw.OpenIssuesCount), Archived: raw.Archived}
	if raw.Description != nil {
		view.Description = clip(strings.TrimSpace(*raw.Description), maxDescriptionLn)
	}
	if raw.DefaultBranch != nil {
		view.DefaultBranch = clip(*raw.DefaultBranch, 255)
	}
	if raw.Namespace != nil {
		view.Namespace = clip(raw.Namespace.FullPath, 255)
	}
	if view.Namespace == "" {
		if index := strings.LastIndex(view.Path, "/"); index > 0 {
			view.Namespace = view.Path[:index]
		}
	}
	topics := raw.Topics
	if len(topics) == 0 {
		topics = raw.TagList
	}
	for _, topic := range topics {
		if len(view.Topics) == 10 {
			break
		}
		view.Topics = append(view.Topics, clip(topic, 60))
	}
	if raw.Permissions != nil {
		if raw.Permissions.ProjectAccess != nil {
			view.AccessLevel = int(raw.Permissions.ProjectAccess.AccessLevel)
		}
		if raw.Permissions.GroupAccess != nil && int(raw.Permissions.GroupAccess.AccessLevel) > view.AccessLevel {
			view.AccessLevel = int(raw.Permissions.GroupAccess.AccessLevel)
		}
	}
	return view
}

// sshPattern — git@host:group/project.git или ssh://git@host[:port]/group/project.git.
// Адрес уходит в git clone, поэтому ни пробелов, ни ведущего дефиса.
var (
	scpPattern     = regexp.MustCompile(`^[A-Za-z0-9_.-]+@([A-Za-z0-9.-]+):[A-Za-z0-9_.][A-Za-z0-9_./-]*$`)
	sshHostPattern = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
	pathPattern    = regexp.MustCompile(`^/[A-Za-z0-9_.][A-Za-z0-9_./-]*$`)
)

// sshURL оставляет адрес SSH, только если он ведёт на узел этого GitLab или
// его поддомен (ssh.gitlab.company.local): чужой адрес клона — подмена.
func (c *Client) sshURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || c.origin == "" || len(raw) > 500 {
		return ""
	}
	base, err := url.Parse(c.origin)
	if err != nil {
		return ""
	}
	gitlabHost := strings.ToLower(base.Hostname())
	host := ""
	if match := scpPattern.FindStringSubmatch(raw); match != nil {
		host = match[1]
	} else if parsed, parseErr := url.Parse(raw); parseErr == nil && parsed.Scheme == "ssh" && sshHostPattern.MatchString(parsed.Hostname()) &&
		pathPattern.MatchString(parsed.Path) && parsed.RawQuery == "" && parsed.Fragment == "" {
		host = parsed.Hostname()
	}
	host = strings.ToLower(host)
	if host == "" || (host != gitlabHost && !strings.HasSuffix(host, "."+gitlabHost) && !sameParentDomain(host, gitlabHost)) {
		return ""
	}
	return raw
}

// sameParentDomain: ssh.company.local и gitlab.company.local — один GitLab
// за разными именами. Домен второго уровня (company.local) обязан совпасть
// целиком и не быть общим суффиксом вроде .com.
func sameParentDomain(a, b string) bool {
	parent := func(host string) string {
		if index := strings.IndexByte(host, '.'); index > 0 {
			return host[index+1:]
		}
		return ""
	}
	pa, pb := parent(a), parent(b)
	return pa != "" && pa == pb && strings.Contains(pa, ".")
}

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// Projects — проекты владельца, свежие сверху. search ищет по имени и пути.
func (c *Client) Projects(ctx context.Context, search string, scope ProjectScope) ([]Project, error) {
	tool, err := c.tool(FeatureProjects, 0)
	if err != nil {
		return nil, err
	}
	args := map[string]any{"order_by": "last_activity_at", "sort": "desc", "per_page": projectsPerPage}
	switch scope {
	case ProjectsOwned:
		args["owned"] = true
	default:
		args["membership"] = true
	}
	if search = strings.TrimSpace(search); search != "" {
		args["search"] = clip(search, 100)
		if strings.Contains(search, "/") {
			args["search_namespaces"] = true
		}
	}
	var raw []rawProject
	if _, err = c.invoke(ctx, tool, args, &raw); err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(raw))
	for _, item := range raw {
		out = append(out, c.project(item))
	}
	return out, nil
}

// ProjectDetail — карточка проекта по пути group/name.
func (c *Client) ProjectDetail(ctx context.Context, path string) (Project, error) {
	tool, err := c.tool(FeatureProject, 0)
	if err != nil {
		return Project{}, err
	}
	var raw rawProject
	if _, err = c.invoke(ctx, tool, map[string]any{"project_id": path}, &raw); err != nil {
		return Project{}, err
	}
	project := c.project(raw)
	if project.Path == "" {
		project.Path = path
	}
	return project, nil
}

type rawCommit struct {
	ID            string   `json:"id"`
	ShortID       string   `json:"short_id"`
	Title         string   `json:"title"`
	Message       string   `json:"message"`
	AuthorName    string   `json:"author_name"`
	AuthoredDate  string   `json:"authored_date"`
	CommittedDate string   `json:"committed_date"`
	ParentIDs     []string `json:"parent_ids"`
	WebURL        string   `json:"web_url"`
	Stats         *struct {
		Additions *jsonInt `json:"additions"`
		Deletions *jsonInt `json:"deletions"`
	} `json:"stats"`
}

var commitIDPattern = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

func (c *Client) commit(raw rawCommit) Commit {
	view := Commit{ID: raw.ID, ShortID: raw.ShortID, Title: clip(raw.Title, maxTitle), AuthorName: clip(raw.AuthorName, 200),
		AuthoredAt: parseTime(raw.AuthoredDate), CommittedAt: parseTime(raw.CommittedDate), WebURL: c.sameOrigin(raw.WebURL)}
	if !commitIDPattern.MatchString(view.ID) {
		view.ID = ""
	}
	if !commitIDPattern.MatchString(view.ShortID) {
		view.ShortID = shortID(view.ID)
	}
	if message := strings.TrimSpace(raw.Message); message != "" && message != strings.TrimSpace(raw.Title) {
		view.Message = clip(message, maxCommitMessage)
	}
	for _, parent := range raw.ParentIDs {
		if commitIDPattern.MatchString(parent) && len(view.ParentIDs) < 8 {
			view.ParentIDs = append(view.ParentIDs, parent)
		}
	}
	if raw.Stats != nil {
		stats := CommitStats{}
		if raw.Stats.Additions != nil {
			stats.Additions = int(*raw.Stats.Additions)
		}
		if raw.Stats.Deletions != nil {
			stats.Deletions = int(*raw.Stats.Deletions)
		}
		view.Stats = &stats
	}
	return view
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// Commits — история ветки (пусто — ветка по умолчанию), страница по 30.
func (c *Client) Commits(ctx context.Context, project, ref string, page int) ([]Commit, error) {
	tool, err := c.tool(FeatureCommits, 0)
	if err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}
	args := map[string]any{"project_id": project, "per_page": commitsPerPage, "page": page, "with_stats": true}
	if ref != "" {
		args["ref_name"] = ref
	}
	var raw []rawCommit
	if _, err = c.invoke(ctx, tool, args, &raw); err != nil {
		return nil, err
	}
	out := make([]Commit, 0, len(raw))
	for _, item := range raw {
		out = append(out, c.commit(item))
	}
	return out, nil
}

// Commit — коммит и его файлы со счётчиками строк.
func (c *Client) Commit(ctx context.Context, project, sha string) (CommitDetail, error) {
	commitTool, err := c.tool(FeatureCommit, 0)
	if err != nil {
		return CommitDetail{}, err
	}
	diffTool, err := c.tool(FeatureCommit, 1)
	if err != nil {
		return CommitDetail{}, err
	}
	var raw rawCommit
	if _, err = c.invoke(ctx, commitTool, map[string]any{"project_id": project, "sha": sha, "stats": true}, &raw); err != nil {
		return CommitDetail{}, err
	}
	var diffs []struct {
		rawDiff
		New     bool `json:"new_file"`
		Deleted bool `json:"deleted_file"`
		Renamed bool `json:"renamed_file"`
	}
	if _, err = c.invoke(ctx, diffTool, map[string]any{"project_id": project, "sha": sha}, &diffs); err != nil {
		return CommitDetail{}, err
	}
	detail := CommitDetail{Commit: c.commit(raw), Files: make([]CommitFile, 0, len(diffs))}
	for _, item := range diffs {
		file := CommitFile{ChangedFile: ChangedFile{OldPath: clip(item.OldPath, 1000), NewPath: clip(item.NewPath, 1000),
			New: item.New, Deleted: item.Deleted, Renamed: item.Renamed}}
		file.Additions, file.Deletions = countDiffLines(item.Diff)
		detail.Files = append(detail.Files, file)
	}
	// Без full_diff GitLab отдаёт первую страницу — 20 файлов по умолчанию.
	detail.FilesTrimmed = len(diffs) >= 20 && detail.Stats != nil && sumLines(detail.Files) < detail.Stats.Additions+detail.Stats.Deletions
	sort.SliceStable(detail.Files, func(i, j int) bool { return detail.Files[i].NewPath < detail.Files[j].NewPath })
	return detail, nil
}

func countDiffLines(diff string) (int, int) {
	additions, deletions := 0, 0
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+"):
			additions++
		case strings.HasPrefix(line, "-"):
			deletions++
		}
	}
	return additions, deletions
}

func sumLines(files []CommitFile) int {
	total := 0
	for _, file := range files {
		total += file.Additions + file.Deletions
	}
	return total
}

// Branches — ветки проекта: по умолчанию первой, затем по свежести коммита.
func (c *Client) Branches(ctx context.Context, project, search string) ([]Branch, error) {
	tool, err := c.tool(FeatureBranches, 0)
	if err != nil {
		return nil, err
	}
	args := map[string]any{"project_id": project, "per_page": branchesPerPage}
	if search = strings.TrimSpace(search); search != "" {
		args["search"] = clip(search, 100)
	}
	var raw []struct {
		Name      string    `json:"name"`
		Commit    rawCommit `json:"commit"`
		Merged    bool      `json:"merged"`
		Protected bool      `json:"protected"`
		CanPush   bool      `json:"can_push"`
		Default   bool      `json:"default"`
		WebURL    string    `json:"web_url"`
	}
	if _, err = c.invoke(ctx, tool, args, &raw); err != nil {
		return nil, err
	}
	out := make([]Branch, 0, len(raw))
	for _, item := range raw {
		out = append(out, Branch{Name: clip(item.Name, 255), Default: item.Default, Protected: item.Protected, Merged: item.Merged,
			CanPush: item.CanPush, Commit: c.commit(item.Commit), WebURL: c.sameOrigin(item.WebURL)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Default != out[j].Default {
			return out[i].Default
		}
		return out[i].Commit.CommittedAt.After(out[j].Commit.CommittedAt)
	})
	return out, nil
}

// Tree — одна папка репозитория: сначала папки, потом файлы, по имени.
func (c *Client) Tree(ctx context.Context, project, path, ref string) (Tree, error) {
	tool, err := c.tool(FeatureTree, 0)
	if err != nil {
		return Tree{}, err
	}
	args := map[string]any{"project_id": project, "per_page": treePerPage}
	if path != "" {
		args["path"] = path
	}
	if ref != "" {
		args["ref"] = ref
	}
	var raw json.RawMessage
	if _, err = c.invoke(ctx, tool, args, &raw); err != nil {
		if ReasonOf(err) == ReasonNotFound {
			// Пустой репозиторий GitLab тоже отвечает 404 на корень.
			return Tree{Path: path, Ref: ref, Entries: []TreeEntry{}}, nil
		}
		return Tree{}, err
	}
	type rawEntry struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Type string `json:"type"`
		Mode string `json:"mode"`
	}
	var items []rawEntry
	trimmed := false
	if err := json.Unmarshal(raw, &items); err != nil {
		var page struct {
			Items         []rawEntry `json:"items"`
			NextPageToken string     `json:"next_page_token"`
		}
		if pageErr := json.Unmarshal(raw, &page); pageErr != nil {
			return Tree{}, &Error{Reason: ReasonFormat, Tool: tool, Detail: "tree did not parse: " + pageErr.Error()}
		}
		items, trimmed = page.Items, page.NextPageToken != ""
	}
	tree := Tree{Path: path, Ref: ref, Entries: make([]TreeEntry, 0, len(items)), Trimmed: trimmed || len(items) >= treePerPage}
	for _, item := range items {
		if item.Type != "tree" && item.Type != "blob" && item.Type != "commit" {
			continue
		}
		tree.Entries = append(tree.Entries, TreeEntry{Name: clip(item.Name, 255), Path: clip(item.Path, 1000), Type: item.Type, Mode: clip(item.Mode, 10)})
	}
	rank := map[string]int{"tree": 0, "commit": 1, "blob": 2}
	sort.SliceStable(tree.Entries, func(i, j int) bool {
		a, b := tree.Entries[i], tree.Entries[j]
		if rank[a.Type] != rank[b.Type] {
			return rank[a.Type] < rank[b.Type]
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return tree, nil
}
