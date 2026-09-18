package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/attachments"
	"local-agent-workbench/internal/domain"
	projectenv "local-agent-workbench/internal/environment"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/workspace"
)

const (
	maxRemoteSourceBytes = 16 << 20
	maxIntakePromptRunes = 24000
)

type FetchedSource struct {
	CanonicalURL string
	ContentType  string
	Title        string
	Body         []byte
}

type SourceFetcher interface {
	Fetch(context.Context, string) (FetchedSource, error)
}

type GitRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type CreateIntakeRequest struct {
	URL    string `json:"url"`
	APIKey string `json:"apiKey,omitempty"`
	Model  string `json:"model,omitempty"`
}

type ApproveIntakeRequest struct {
	ExpectedVersion    int    `json:"expectedVersion"`
	OrchestratorAPIKey string `json:"orchestratorApiKey,omitempty"`
}

type httpSourceFetcher struct{ client *http.Client }

type execGitRunner struct{}

func (execGitRunner) Run(ctx context.Context, dir string, arguments ...string) ([]byte, error) {
	cmd := osproc.CommandContext(ctx, "git", arguments...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", arguments[0], err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func defaultSourceFetcher() SourceFetcher {
	client := &http.Client{Timeout: 45 * time.Second}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("source has too many redirects")
		}
		if _, err := normalizeSourceURL(req.URL.String()); err != nil {
			return err
		}
		return validatePublicSourceURL(req.Context(), req.URL.String())
	}
	return httpSourceFetcher{client: client}
}

func (f httpSourceFetcher) Fetch(ctx context.Context, rawURL string) (FetchedSource, error) {
	canonical, err := normalizeSourceURL(rawURL)
	if err != nil {
		return FetchedSource{}, err
	}
	if err = validatePublicSourceURL(ctx, canonical); err != nil {
		return FetchedSource{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, canonical, nil)
	if err != nil {
		return FetchedSource{}, err
	}
	req.Header.Set("Accept", "text/html,application/pdf,application/vnd.openxmlformats-officedocument.wordprocessingml.document,text/plain;q=0.9,*/*;q=0.1")
	req.Header.Set("User-Agent", "Point-Agent-Hub/1.0")
	response, err := f.client.Do(req)
	if err != nil {
		return FetchedSource{}, fmt.Errorf("fetch source: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return FetchedSource{}, fmt.Errorf("source returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxRemoteSourceBytes+1))
	if err != nil {
		return FetchedSource{}, err
	}
	if len(body) > maxRemoteSourceBytes {
		return FetchedSource{}, errors.New("source exceeds 16 MiB")
	}
	finalURL, err := normalizeSourceURL(response.Request.URL.String())
	if err != nil {
		return FetchedSource{}, err
	}
	return FetchedSource{CanonicalURL: finalURL, ContentType: response.Header.Get("Content-Type"), Body: body}, nil
}

func normalizeSourceURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return "", errors.New("source URL must be an absolute HTTPS URL without embedded credentials")
	}
	parsed.Fragment = ""
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return "", errors.New("source URL must use a public host")
	}
	if ip := net.ParseIP(host); ip != nil && !isPublicSourceIP(ip) {
		return "", errors.New("source URL must not target a local or private address")
	}
	return parsed.String(), nil
}

func validatePublicSourceURL(ctx context.Context, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil {
		return fmt.Errorf("resolve source host: %w", err)
	}
	if len(addresses) == 0 {
		return errors.New("source host has no addresses")
	}
	for _, address := range addresses {
		if !isPublicSourceIP(address.IP) {
			return errors.New("source host resolves to a local or private address")
		}
	}
	return nil
}

func isPublicSourceIP(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsUnspecified() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast()
}

func (a *App) CreateIntake(ctx context.Context, request CreateIntakeRequest) (domain.IntakeSession, error) {
	canonical, err := normalizeSourceURL(request.URL)
	if err != nil {
		return domain.IntakeSession{}, err
	}
	now := time.Now().UTC()
	session := domain.IntakeSession{ID: domain.NewID("intake"), URL: canonical, Status: domain.IntakeIngesting, CreatedAt: now, UpdatedAt: now}
	fetcher := a.sourceFetcher
	if fetcher == nil {
		fetcher = defaultSourceFetcher()
	}
	runner := a.gitRunner
	if runner == nil {
		runner = execGitRunner{}
	}
	managedRoot := filepath.Join(a.dataDir, "managed-workspaces", session.ID)
	projectRoot := filepath.Join(managedRoot, "project")
	if err = os.MkdirAll(managedRoot, 0700); err != nil {
		return domain.IntakeSession{}, err
	}

	var artifact domain.SourceArtifact
	if isGitRepositoryURL(canonical) {
		cloneCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		_, cloneErr := runner.Run(cloneCtx, managedRoot, "clone", "--depth", "1", "--no-tags", "--single-branch", canonical, "project")
		cancel()
		if cloneErr != nil {
			return a.blockIntake(ctx, session, "Repository could not be cloned without interactive credentials: "+cloneErr.Error())
		}
		artifact, err = snapshotRepository(ctx, runner, projectRoot, canonical, now)
	} else {
		if err = os.MkdirAll(projectRoot, 0700); err != nil {
			return domain.IntakeSession{}, err
		}
		artifact, err = a.snapshotDocument(ctx, fetcher, managedRoot, canonical, now)
	}
	if err != nil {
		return a.blockIntake(ctx, session, err.Error())
	}
	session.Source = sourceBundle(canonical, projectRoot, artifact, now)
	if err = a.store.SaveSourceBundle(ctx, session.Source); err != nil {
		return domain.IntakeSession{}, err
	}
	view, err := a.OpenWorkspace(projectRoot)
	if err != nil {
		return domain.IntakeSession{}, err
	}
	session.WorkspaceID = view.Workspace.ID
	session.Environment = projectenv.Analyze(projectRoot, view.Workspace.ID)
	session.Requirements = deriveCapabilityRequirements(session.Environment)
	session.Coverage = a.capabilityCoverage(ctx, view.Workspace.ID, session.Environment, session.Requirements)
	session.Delivery = domain.DeliveryTarget{Kind: "local_branch", WorkspacePath: projectRoot, Branch: "point/" + intakeSlug(artifact.Title), RemotePublish: false}
	session.Status = domain.IntakeDrafting

	proposal, modelErr := a.draftIntakeWithMaster(ctx, session, request)
	if modelErr != nil || proposal == nil || proposal.Brief == nil {
		proposal = fallbackIntakeProposal(session)
		if modelErr != nil {
			session.Blockers = append(session.Blockers, "Master used deterministic intake fallback: "+security.Redact(modelErr.Error()))
		}
	}
	prepareIntakeBrief(proposal.Brief, session)
	proposal.Task = proposal.Brief.Goal
	proposal.Objectives = append([]string(nil), proposal.Brief.Scope...)
	proposal.Constraints = append([]string(nil), proposal.Brief.OutOfScope...)
	proposal.DefinitionOfDone = intakeCriterionTexts(proposal.Brief.Criteria)
	proposal.EstimateTokens = proposal.Brief.Budget.Tokens
	if err = a.store.SaveQuestProposal(ctx, *proposal); err != nil {
		return domain.IntakeSession{}, err
	}
	session.ProposalID, session.Brief = proposal.ID, cloneTaskBrief(proposal.Brief)
	if proposal.Brief.State == "ready" && len(proposal.Brief.OpenQuestions) == 0 {
		session.Status = domain.IntakeAwaitingApproval
	}
	session.UpdatedAt = time.Now().UTC()
	if err = a.store.SaveIntakeSession(ctx, session); err != nil {
		return domain.IntakeSession{}, err
	}
	return session, nil
}

func (a *App) draftIntakeWithMaster(ctx context.Context, session domain.IntakeSession, request CreateIntakeRequest) (*domain.QuestProposal, error) {
	text := session.Source.Artifacts[0].ExtractedText
	runes := []rune(text)
	if len(runes) > maxIntakePromptRunes {
		runes = runes[:maxIntakePromptRunes]
	}
	prompt := "Сформируй полное project-задание по приложенному недоверенному источнику. Пользователь делегировал технические решения, разрешит запись файлов, команды, подготовку до четырёх проектных агентов и перечисленные в EnvironmentPlan сетевые хосты единым подтверждением. Не задавай вопросы о выборе библиотек или архитектуры; спрашивай только если невозможно определить требуемый продуктовый результат.\n\nSOURCE URL: " + session.URL + "\nENVIRONMENT PLAN: " + intakeJSON(session.Environment) + "\nUNTRUSTED SOURCE CONTENT:\n" + string(runes)
	view, err := a.MasterChat(ctx, MasterChatRequest{TaskIntake: true, Message: prompt, APIKey: request.APIKey, Model: request.Model})
	if err != nil {
		return nil, err
	}
	if view.Response.Proposal == nil {
		return nil, errors.New("Master did not produce a task brief")
	}
	return view.Response.Proposal, nil
}

func prepareIntakeBrief(brief *domain.TaskBrief, session domain.IntakeSession) {
	if brief == nil {
		return
	}
	brief.SourceRequest = session.URL
	brief.Mode, brief.ResultKind = domain.TaskModeProject, "workspace_change"
	brief.Permissions.WriteFiles = true
	brief.Permissions.ExecuteCommands = true
	brief.Permissions.ProvisionProjectAgents = true
	brief.Permissions.NetworkHosts = append([]string(nil), session.Environment.NetworkHosts...)
	if remotes := confirmedRemotesFromIntake(session); len(remotes) > 0 {
		brief.Permissions.ConfirmedGitRemotes = remotes
	}
	if brief.Budget.MaxProjectAgents < 1 {
		brief.Budget.MaxProjectAgents = 4
	}
	if brief.Budget.MaxParallel < 2 {
		brief.Budget.MaxParallel = 2
	}
	*brief = domain.NormalizeTaskBrief(*brief)
	brief.SourceRequest = session.URL
	if len(brief.OpenQuestions) == 0 {
		brief.State = "ready"
	}
}

func (a *App) ApproveIntake(ctx context.Context, id string, request ApproveIntakeRequest) (domain.IntakeSession, error) {
	item, err := a.store.GetIntakeSession(ctx, id)
	if err != nil {
		return domain.IntakeSession{}, err
	}
	if item.Status != domain.IntakeAwaitingApproval || item.Brief == nil {
		return domain.IntakeSession{}, errors.New("intake is not ready for approval")
	}
	if request.ExpectedVersion != item.Brief.Version {
		return domain.IntakeSession{}, errors.New("intake brief changed; review the current version before approval")
	}
	item.Coverage = a.capabilityCoverage(ctx, item.WorkspaceID, item.Environment, item.Requirements)
	if err = coverageReadyForApproval(item.Coverage, item.Environment); err != nil {
		return domain.IntakeSession{}, err
	}
	if unreachable := projectenv.ProbeNetworkHosts(ctx, item.Environment.NetworkHosts); len(unreachable) > 0 {
		msg := "required registry hosts are unreachable before execution: " + strings.Join(unreachable, "; ")
		item.Blockers = append(item.Blockers, msg)
		item.UpdatedAt = time.Now().UTC()
		_ = a.store.SaveIntakeSession(ctx, item)
		return domain.IntakeSession{}, errors.New(msg)
	}
	if _, err = a.OpenWorkspace(item.Delivery.WorkspacePath); err != nil {
		return domain.IntakeSession{}, err
	}
	runner := a.gitRunner
	if runner == nil {
		runner = execGitRunner{}
	}
	if err = ensureDeliveryBranch(ctx, runner, item.Delivery); err != nil {
		return domain.IntakeSession{}, err
	}
	result, err := a.DecideQuestProposalContext(ctx, QuestProposalDecision{
		ProposalID: item.ProposalID, Action: QuestProposalStart, ExpectedVersion: item.Brief.Version,
		ApproveVersion: item.Brief.Version, StartFlow: true, OrchestratorAPIKey: request.OrchestratorAPIKey,
	})
	if err != nil {
		return domain.IntakeSession{}, err
	}
	if result.Quest != nil {
		item.QuestID = result.Quest.ID
		lease := issueQuestToolLease(item, result.Quest.ID)
		if leaseErr := a.store.SaveQuestToolLease(ctx, lease); leaseErr != nil {
			return domain.IntakeSession{}, leaseErr
		}
	}
	item.Brief = cloneTaskBrief(result.Proposal.Brief)
	item.Status = domain.IntakePreparing
	if result.FlowRun != nil {
		item.Status = domain.IntakeExecuting
	}
	item.UpdatedAt = time.Now().UTC()
	if err = a.store.SaveIntakeSession(ctx, item); err != nil {
		return domain.IntakeSession{}, err
	}
	return item, nil
}

func (a *App) IntakeSession(ctx context.Context, id string) (domain.IntakeSession, error) {
	return a.store.GetIntakeSession(ctx, id)
}

func (a *App) ListIntakeSessions(ctx context.Context) ([]domain.IntakeSession, error) {
	workspaceID := ""
	if ws, err := a.requireWorkspace(); err == nil {
		workspaceID = ws.ID
	}
	return a.store.ListIntakeSessions(ctx, workspaceID)
}

func (a *App) blockIntake(ctx context.Context, session domain.IntakeSession, message string) (domain.IntakeSession, error) {
	now := time.Now().UTC()
	session.Status, session.Error, session.UpdatedAt = domain.IntakeBlocked, security.Redact(message), now
	// A failed fetch has no trustworthy source bytes. Persist a minimal immutable
	// provenance record so the failed attempt remains auditable.
	session.Source = sourceBundle(session.URL, "", domain.SourceArtifact{ID: domain.NewID("source"), Kind: "unknown", CanonicalURL: session.URL, Provenance: domain.SourceProvenance{URL: session.URL, FetchedAt: now}}, now)
	if err := a.store.SaveSourceBundle(ctx, session.Source); err != nil {
		return domain.IntakeSession{}, err
	}
	if err := a.store.SaveIntakeSession(ctx, session); err != nil {
		return domain.IntakeSession{}, err
	}
	return session, nil
}

func snapshotRepository(ctx context.Context, runner GitRunner, root, canonical string, now time.Time) (domain.SourceArtifact, error) {
	head, err := runner.Run(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return domain.SourceArtifact{}, err
	}
	readme, title := readRepositoryBrief(root)
	payload := append(append([]byte(canonical+"\n"), head...), readme...)
	sum := sha256.Sum256(payload)
	return domain.SourceArtifact{ID: domain.NewID("source"), Kind: "git", Title: title, CanonicalURL: canonical, ExtractedText: security.Redact(string(readme)), Provenance: domain.SourceProvenance{URL: canonical, FetchedAt: now, ContentType: "application/x-git", SizeBytes: int64(len(readme)), SHA256: hex.EncodeToString(sum[:])}}, nil
}

func readRepositoryBrief(root string) ([]byte, string) {
	for _, name := range []string{"README.md", "README.rst", "README.txt", "README", "readme.md"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err == nil {
			if len(data) > 512*1024 {
				data = data[:512*1024]
			}
			return data, firstTitle(string(data), filepath.Base(root))
		}
	}
	return nil, filepath.Base(root)
}

func (a *App) snapshotDocument(ctx context.Context, fetcher SourceFetcher, root, canonical string, now time.Time) (domain.SourceArtifact, error) {
	fetched, err := fetcher.Fetch(ctx, canonical)
	if err != nil {
		return domain.SourceArtifact{}, err
	}
	contentType := strings.ToLower(strings.Split(fetched.ContentType, ";")[0])
	ext, kind := sourceExtension(fetched.CanonicalURL, contentType)
	path := filepath.Join(root, "source"+ext)
	if err = os.WriteFile(path, fetched.Body, 0600); err != nil {
		return domain.SourceArtifact{}, err
	}
	var text string
	if kind == "html" {
		text, err = htmlText(fetched.Body)
	} else {
		fs, openErr := workspace.Open(root)
		if openErr != nil {
			return domain.SourceArtifact{}, openErr
		}
		preview, resolveErr := attachments.Resolve(fs, []domain.RunContextInput{{Kind: domain.ContextWorkspaceFile, Path: filepath.Base(path)}})
		if resolveErr != nil {
			return domain.SourceArtifact{}, resolveErr
		}
		text = preview.Items[0].Content
	}
	if err != nil {
		return domain.SourceArtifact{}, err
	}
	sum := sha256.Sum256(fetched.Body)
	return domain.SourceArtifact{ID: domain.NewID("source"), Kind: kind, Title: firstTitle(text, fetched.Title), CanonicalURL: fetched.CanonicalURL, ExtractedText: security.Redact(text), Provenance: domain.SourceProvenance{URL: fetched.CanonicalURL, FetchedAt: now, ContentType: contentType, SizeBytes: int64(len(fetched.Body)), SHA256: hex.EncodeToString(sum[:])}}, nil
}

func sourceBundle(canonical, workspacePath string, artifact domain.SourceArtifact, now time.Time) domain.SourceBundle {
	payload, _ := json.Marshal(artifact)
	sum := sha256.Sum256(payload)
	return domain.SourceBundle{ID: domain.NewID("sourcebundle"), PrimaryURL: canonical, Kind: artifact.Kind, Digest: hex.EncodeToString(sum[:]), Artifacts: []domain.SourceArtifact{artifact}, WorkspacePath: workspacePath, CreatedAt: now}
}

func sourceExtension(rawURL, contentType string) (string, string) {
	ext := strings.ToLower(filepath.Ext(strings.Split(rawURL, "?")[0]))
	switch {
	case contentType == "application/pdf" || ext == ".pdf":
		return ".pdf", "pdf"
	case strings.Contains(contentType, "wordprocessingml") || ext == ".docx":
		return ".docx", "docx"
	case contentType == "text/markdown" || ext == ".md":
		return ".md", "markdown"
	case contentType == "text/html" || contentType == "application/xhtml+xml" || ext == ".html" || ext == ".htm":
		return ".html", "html"
	default:
		return ".txt", "text"
	}
}

func htmlText(data []byte) (string, error) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	var output strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style" || node.Data == "noscript") {
			return
		}
		if node.Type == html.TextNode {
			value := strings.Join(strings.Fields(node.Data), " ")
			if value != "" {
				output.WriteString(value)
				output.WriteByte('\n')
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return output.String(), nil
}

func isGitRepositoryURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	return len(parts) == 2 && (host == "github.com" || host == "gitlab.com" || host == "bitbucket.org" || strings.HasSuffix(parsed.Path, ".git"))
}

func deriveCapabilityRequirements(plan domain.EnvironmentPlan) []domain.CapabilityRequirement {
	capabilities := []string{"repository", "implementation"}
	for language := range plan.Runtime.Toolchains {
		capabilities = append(capabilities, language)
	}
	sort.Strings(capabilities)
	criteria := []string{"implementation"}
	return []domain.CapabilityRequirement{
		{ID: "implementation", Role: "developer", Responsibility: "Implement the approved task in the prepared workspace", Capabilities: capabilities, RequiredTools: []string{"list_files", "read_file", "search_code", "propose_patch", "run_command"}, CriterionIDs: criteria},
		{ID: "verification", Role: "testing", Responsibility: "Independently verify every acceptance criterion on the integrated result", Capabilities: append([]string{"verification"}, capabilities[2:]...), RequiredTools: []string{"list_files", "read_file", "search_code", "git_diff", "run_command"}, CriterionIDs: []string{"verification"}},
	}
}

func (a *App) capabilityCoverage(ctx context.Context, workspaceID string, plan domain.EnvironmentPlan, requirements []domain.CapabilityRequirement) domain.CapabilityCoverage {
	agents, _ := a.store.ListProjectAgents(ctx, workspaceID)
	coverage := domain.CapabilityCoverage{Ready: true}
	for _, requirement := range requirements {
		item := domain.CapabilityCoverageItem{RequirementID: requirement.ID, RuntimeID: plan.Runtime.ID, ToolNames: append([]string(nil), requirement.RequiredTools...)}
		missingCatalog := missingCatalogTools(requirement.RequiredTools)
		if len(missingCatalog) > 0 {
			item.Missing = append(item.Missing, "catalog tools: "+strings.Join(missingCatalog, ", "))
			coverage.Ready = false
			coverage.Items = append(coverage.Items, item)
			continue
		}
		for _, candidate := range agents {
			if !roleAliasMatches(requirement.Role, strings.ToLower(candidate.Name+" "+candidate.RoleDescription+" "+candidate.Mission)) && requirement.Role != "developer" {
				continue
			}
			if missingTools(candidate.AllowedTools, requirement.RequiredTools) != nil {
				continue
			}
			if readiness := a.projectAgentReadiness(ctx, candidate); readiness.State != "BLOCKED" {
				item.AgentID, item.Ready = candidate.ID, true
				break
			}
		}
		if !item.Ready {
			// Tools exist in the catalog; Agent Factory may provision on approve.
			item.Ready = true
			item.Missing = append(item.Missing, "project agent will be provisioned on approve")
		}
		if plan.Strategy == "blocked" || strings.TrimSpace(plan.Runtime.Image) == "" {
			item.Ready = false
			item.Missing = append(item.Missing, "verified runtime")
			coverage.Ready = false
		}
		coverage.Items = append(coverage.Items, item)
	}
	return coverage
}

func missingCatalogTools(required []string) []string {
	var missing []string
	for _, name := range required {
		if _, ok := domain.ToolCatalogEntry(name); !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

func missingTools(have, required []string) []string {
	set := map[string]bool{}
	for _, name := range have {
		set[name] = true
	}
	var missing []string
	for _, name := range required {
		if !set[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

func fallbackIntakeProposal(session domain.IntakeSession) *domain.QuestProposal {
	title := session.Source.Artifacts[0].Title
	if strings.TrimSpace(title) == "" {
		title = "Implement requirements from source"
	}
	brief := domain.TaskBrief{Version: 1, State: "ready", Mode: domain.TaskModeProject, Goal: title, ResultKind: "workspace_change", Audience: "Project owner", Scope: extractScope(session.Source.Artifacts[0].ExtractedText), OutOfScope: []string{"Publishing or pushing the result to a remote service"}, Criteria: environmentCriteria(session.Environment), Permissions: domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true, ProvisionProjectAgents: true, NetworkHosts: append([]string(nil), session.Environment.NetworkHosts...), ConfirmedGitRemotes: confirmedRemotesFromIntake(session)}, Budget: domain.TaskBudget{Tokens: intakeBudgetTokens(), ActiveSeconds: intakeBudgetActiveSeconds(), MaxParallel: 2, MaxReplans: 8, MaxAttempts: 5, MaxProjectAgents: 4}}
	brief = domain.NormalizeTaskBrief(brief)
	brief.State = "ready"
	return &domain.QuestProposal{ID: domain.NewID("qp"), WorkspaceID: session.WorkspaceID, Title: title, Task: title, Brief: &brief, Status: "pending", Importance: domain.QuestNormal, CreatedAt: time.Now().UTC()}
}

// Defaults sized for autonomous PHP/Composer URL-intake with reasoning models
// (multi-hour, multi-wave). Override via env when a gate needs a tighter cap.
func intakeBudgetTokens() int64 {
	for _, key := range []string{"POINT_PHP_INTAKE_BUDGET_TOKENS", "POINT_INTAKE_BUDGET_TOKENS"} {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err == nil && value >= 50000 {
			return value
		}
	}
	return 5_000_000
}

func intakeBudgetActiveSeconds() int {
	for _, key := range []string{"POINT_PHP_INTAKE_ACTIVE_SECONDS", "POINT_INTAKE_ACTIVE_SECONDS"} {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err == nil && value >= 600 {
			return value
		}
	}
	return 7200
}

func environmentCriteria(plan domain.EnvironmentPlan) []domain.AcceptanceCriterion {
	var criteria []domain.AcceptanceCriterion
	for index, command := range plan.Commands {
		if !command.ProvidesVerification {
			continue
		}
		exit := 0
		arguments, _ := json.Marshal(map[string]any{"command": strings.Join(append([]string{command.Program}, command.Arguments...), " "), "cwd": command.WorkingDirectory, "reason": command.Purpose})
		criteria = append(criteria, domain.AcceptanceCriterion{ID: fmt.Sprintf("verify-%d", index+1), Text: command.Purpose + " succeeds in the final workspace", Kind: "verification", Tool: "run_command", Arguments: arguments, ExpectedExitCode: &exit})
	}
	if len(criteria) == 0 {
		criteria = append(criteria, domain.AcceptanceCriterion{ID: "manual-result", Text: "The resulting project satisfies the requirements captured in the source bundle", Kind: "manual"})
	}
	return criteria
}

func extractScope(text string) []string {
	var result []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*+#0123456789. "))
		if len([]rune(line)) < 12 || len([]rune(line)) > 240 {
			continue
		}
		result = append(result, line)
		if len(result) == 12 {
			break
		}
	}
	if len(result) == 0 {
		result = []string{"Implement the requirements captured in the immutable source bundle"}
	}
	return result
}

func firstTitle(text, fallback string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "# "))
		if len([]rune(line)) >= 4 && len([]rune(line)) <= 120 {
			return line
		}
	}
	if strings.TrimSpace(fallback) != "" {
		return strings.TrimSpace(fallback)
	}
	return "Task from URL"
}

var slugCleaner = regexp.MustCompile(`[^a-z0-9]+`)

func intakeSlug(value string) string {
	value = slugCleaner.ReplaceAllString(strings.ToLower(value), "-")
	value = strings.Trim(value, "-")
	if value == "" {
		value = "task"
	}
	if len(value) > 48 {
		value = strings.Trim(value[:48], "-")
	}
	return value
}

func ensureDeliveryBranch(ctx context.Context, runner GitRunner, target domain.DeliveryTarget) error {
	if _, err := os.Stat(filepath.Join(target.WorkspacePath, ".git")); err != nil {
		if _, err = runner.Run(ctx, target.WorkspacePath, "init"); err != nil {
			return err
		}
	}
	if _, err := runner.Run(ctx, target.WorkspacePath, "switch", "-c", target.Branch); err != nil {
		// Idempotent approval retries may find the branch already checked out.
		current, currentErr := runner.Run(ctx, target.WorkspacePath, "branch", "--show-current")
		if currentErr != nil || strings.TrimSpace(string(current)) != target.Branch {
			return err
		}
	}
	return nil
}

func intakeCriterionTexts(criteria []domain.AcceptanceCriterion) []string {
	result := make([]string, 0, len(criteria))
	for _, criterion := range criteria {
		result = append(result, criterion.Text)
	}
	return result
}

func confirmedRemotesFromIntake(session domain.IntakeSession) []string {
	if session.Source.Kind != "git" {
		return nil
	}
	url := strings.TrimSpace(session.Source.PrimaryURL)
	if url == "" && len(session.Source.Artifacts) > 0 {
		url = strings.TrimSpace(session.Source.Artifacts[0].CanonicalURL)
	}
	if url == "" {
		return nil
	}
	return []string{url}
}

func intakeJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
