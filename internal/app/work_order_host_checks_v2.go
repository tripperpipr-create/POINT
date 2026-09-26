package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

// managedHostArgsV2 is the structured form of a criterion Point verifies on
// the host after delivery. The command stays the human-readable contract; the
// fields say what must be observed. "status":"ok" alone let a service that
// answered db:"down" with a live database pass, so the brief can now name the
// fields that prove the result.
type managedHostArgsV2 struct {
	Command      string         `json:"command"`
	URL          string         `json:"url,omitempty"`
	ExpectStatus int            `json:"expectStatus,omitempty"`
	ExpectJSON   map[string]any `json:"expectJson,omitempty"`
	RecoverJSON  map[string]any `json:"recoverJson,omitempty"`
}

// managedHostCheckV2 is one approved host check resolved to what Point runs.
// Kind "http" probes a local URL; "outage" stops one Compose service, probes
// the degraded answer, starts the service again and probes recovery. Other
// kinds are the legacy exact command forms of managedComposeCriterionKindV2.
type managedHostCheckV2 struct {
	Kind        string
	Base        string
	URL         string
	Service     string
	Status      int
	JSON        map[string]any
	RecoverJSON map[string]any
}

var composeServiceNameV2 = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

func managedHostCheckFromArgsV2(order domain.WorkOrder, raw json.RawMessage) managedHostCheckV2 {
	var args managedHostArgsV2
	if json.Unmarshal(raw, &args) != nil {
		return managedHostCheckV2{}
	}
	command := strings.TrimSpace(args.Command)
	if service, ok := strings.CutPrefix(command, "docker compose stop "); ok && composeServiceNameV2.MatchString(service) {
		target := strings.TrimSpace(args.URL)
		if !managedLocalProbeURLV2(order, target) {
			return managedHostCheckV2{}
		}
		status := args.ExpectStatus
		if status == 0 {
			status = http.StatusOK
		}
		return managedHostCheckV2{Kind: "outage", Service: service, URL: target, Status: status, JSON: args.ExpectJSON, RecoverJSON: args.RecoverJSON}
	}
	if target, ok := strings.CutPrefix(command, "curl -sf "); ok && !strings.ContainsAny(target, " \t'\"|&;$`<>") {
		if !managedLocalProbeURLV2(order, target) {
			return managedHostCheckV2{}
		}
		expect := args.ExpectJSON
		if expect == nil && strings.HasSuffix(target, "/health") {
			expect = map[string]any{"status": "ok"}
		}
		return managedHostCheckV2{Kind: "http", URL: target, Status: http.StatusOK, JSON: expect}
	}
	kind, base := managedComposeCriterionKindV2(order, command)
	return managedHostCheckV2{Kind: kind, Base: base}
}

// managedLocalProbeURLV2 accepts only a loopback URL of the delivered
// application: Point never probes a host the brief did not name.
func managedLocalProbeURLV2(order domain.WorkOrder, target string) bool {
	parsed, err := url.Parse(target)
	if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return false
	}
	return managedComposeLocalURLV2(order, parsed.Scheme+"://"+parsed.Host)
}

func (a *App) runManagedHostCheckV2(ctx context.Context, directory string, check managedHostCheckV2, runner CompletionCheckRunner) (int, string, error) {
	switch check.Kind {
	case "http":
		probeCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		return managedHTTPExpectV2(probeCtx, check.URL, check.Status, check.JSON)
	case "outage":
		return runManagedOutageV2(ctx, directory, check, runner)
	case "stack-up":
		code, output, err := runner.Run(ctx, directory, "docker compose up -d")
		if err == nil && code != 0 {
			if hint := composePortConflictHintV2(ctx, directory, output, runner); hint != "" {
				output = strings.TrimSpace(output) + "\n" + hint
			}
		}
		return code, output, err
	default:
		return a.runManagedComposeCriterionV2(ctx, directory, check.Kind, check.Base, runner)
	}
}

// runManagedOutageV2 proves the degraded answer and restores the service even
// when the probe fails: a verification must never leave the user's stack
// half-stopped.
func runManagedOutageV2(ctx context.Context, directory string, check managedHostCheckV2, runner CompletionCheckRunner) (int, string, error) {
	code, output, err := runner.Run(ctx, directory, "docker compose stop "+check.Service)
	if err != nil || code != 0 {
		return code, output, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	outageCode, outageOutput, outageErr := managedHTTPExpectV2(probeCtx, check.URL, check.Status, check.JSON)
	cancel()
	restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer restoreCancel()
	restoreCode, restoreOutput, restoreErr := runner.Run(restoreCtx, directory, "docker compose start "+check.Service)
	if restoreErr != nil || restoreCode != 0 {
		restoreCode = 1
		restoreOutput = strings.TrimSpace(restoreOutput + "\nСервис " + check.Service + " не удалось поднять обратно")
	} else {
		recoveredCode, recoveredOutput, recoveredErr := managedHTTPExpectV2(restoreCtx, check.URL, http.StatusOK, check.RecoverJSON)
		restoreCode, restoreErr = recoveredCode, recoveredErr
		restoreOutput = "После восстановления " + check.Service + ": " + recoveredOutput
	}
	return outageCode | restoreCode,
		strings.Join([]string{"Остановлен " + check.Service + ": " + outageOutput, restoreOutput}, "\n"),
		errors.Join(outageErr, restoreErr)
}

// managedHTTPExpectV2 polls target until it answers the expected status and
// JSON fields. The summary always carries the last body: "last 200 OK" alone
// hid both a db:"down" answer and a foreign container holding the port.
func managedHTTPExpectV2(ctx context.Context, target string, status int, fields map[string]any) (int, string, error) {
	// Один запрос ждёт до 10 с: приложение законно держит ответ, пока его
	// собственный таймаут к БД (обычно 5 с) не истечёт. При 3 с проверка
	// деградации обрывала честный ответ «db=down» и винила код.
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	last := "нет ответа"
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return 1, "invalid probe URL", err
		}
		response, err := client.Do(request)
		if err == nil {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			last = strings.TrimSpace(response.Status + " " + managedBodySnippetV2(body))
			if response.StatusCode == status {
				mismatch := managedJSONMismatchV2(body, fields)
				if mismatch == "" {
					return 0, "GET " + target + ": " + last, nil
				}
				last += " — " + mismatch
			}
		} else if ctx.Err() == nil {
			last = security.Redact(err.Error())
		}
		select {
		case <-ctx.Done():
			return 1, fmt.Sprintf("GET %s: ожидали %d%s; последний ответ: %s", target, status, managedExpectTextV2(fields), last), nil
		case <-ticker.C:
		}
	}
}

func managedBodySnippetV2(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if runes := []rune(text); len(runes) > 200 {
		text = string(runes[:200]) + "…"
	}
	return security.Redact(text)
}

func managedExpectTextV2(fields map[string]any) string {
	if len(fields) == 0 {
		return ""
	}
	return " и " + strings.Join(managedFieldPairsV2(fields), ", ")
}

func managedFieldPairsV2(fields map[string]any) []string {
	pairs := make([]string, 0, len(fields))
	for key, value := range fields {
		pairs = append(pairs, key+"="+fmt.Sprint(value))
	}
	sort.Strings(pairs)
	return pairs
}

// managedJSONMismatchV2 compares the named fields of a JSON body. A dotted key
// reaches into nested objects ("checks.db"). Values compare by their printed
// form, so "up" matches "up" and 200 matches 200 regardless of JSON number type.
func managedJSONMismatchV2(body []byte, fields map[string]any) string {
	if len(fields) == 0 {
		return ""
	}
	var document map[string]any
	if json.Unmarshal(body, &document) != nil {
		return "ответ не JSON-объект"
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	mismatches := []string{}
	for _, key := range keys {
		var current any = document
		for _, part := range strings.Split(key, ".") {
			object, ok := current.(map[string]any)
			if !ok {
				current = nil
				break
			}
			current = object[part]
		}
		want := fmt.Sprint(fields[key])
		if current == nil {
			mismatches = append(mismatches, "нет поля "+key)
		} else if got := fmt.Sprint(current); got != want {
			mismatches = append(mismatches, key+"="+got+", ждали "+want)
		}
	}
	return strings.Join(mismatches, "; ")
}

var composePortFailureV2 = regexp.MustCompile(`(?i)(?:bind for|listen tcp|exposing port tcp)\s+\S*?:(\d{2,5})\b`)

// composePortConflictHintV2 names who holds a published port when
// `docker compose up` fails on it. Without the name the user saw only
// "устраните указанную ошибку" and spent the next twelve minutes guessing.
func composePortConflictHintV2(ctx context.Context, directory, output string, runner CompletionCheckRunner) string {
	lower := strings.ToLower(output)
	if !strings.Contains(lower, "already allocated") && !strings.Contains(lower, "address already in use") &&
		!strings.Contains(lower, "ports are not available") && !strings.Contains(lower, "only one usage of each socket") {
		return ""
	}
	match := composePortFailureV2.FindStringSubmatch(output)
	if match == nil {
		return "Нужно действие: порт приложения уже занят другим процессом; освободите его или смените порт в Compose, затем попросите Мастера повторить наряд."
	}
	port := match[1]
	lookupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	code, holders, err := runner.Run(lookupCtx, directory, `docker ps --filter publish=`+port+` --format "{{.Names}} {{.Labels}}"`)
	if err != nil || code != 0 || strings.TrimSpace(holders) == "" {
		return "Нужно действие: порт " + port + " занят программой вне Docker; освободите его или смените порт в Compose, затем попросите Мастера повторить наряд."
	}
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(holders), "\n", 2)[0])
	name, labels, _ := strings.Cut(line, " ")
	project, folder := "", ""
	for _, label := range strings.Split(labels, ",") {
		key, value, _ := strings.Cut(label, "=")
		switch key {
		case "com.docker.compose.project":
			project = value
		case "com.docker.compose.project.working_dir":
			folder = value
		}
	}
	owner := "контейнер " + name
	if project != "" {
		owner += " (Compose-проект " + project
		if folder != "" {
			owner += ", папка " + folder
		}
		owner += ")"
	}
	return "Нужно действие: порт " + port + " занят — " + owner + ". Остановите его (docker stop " + name + ") или смените порт в Compose, затем попросите Мастера повторить наряд."
}

// failedHostCriteriaV2 names failed host checks by their criterion and last
// output line, not by the shared kind "acceptance".
func failedHostCriteriaV2(checks []domain.VerificationCheck, order domain.WorkOrder) []string {
	texts := map[string]string{}
	for _, criterion := range order.Criteria {
		texts[criterion.ID] = strings.TrimSpace(criterion.Text)
	}
	failed := []string{}
	for _, check := range checks {
		if check.Satisfied {
			continue
		}
		label := texts[check.ID]
		if label == "" {
			label = check.ID
		}
		if reason := lastSummaryLineV2(check.Summary); reason != "" {
			label += " — " + reason
		}
		failed = append(failed, label)
	}
	return failed
}

// hostCheckActionV2 lifts a concrete "Нужно действие" line out of a failed
// check, so the card ends with what to do rather than a generic retry.
func hostCheckActionV2(summary string) string {
	for _, line := range strings.Split(summary, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "Нужно действие:") {
			return line
		}
	}
	return ""
}

func lastSummaryLineV2(summary string) string {
	lines := strings.Split(strings.TrimSpace(summary), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" || strings.HasPrefix(line, "Нужно действие:") {
			continue
		}
		if runes := []rune(line); len(runes) > 300 {
			line = string(runes[:300]) + "…"
		}
		return line
	}
	return ""
}

// composeProjectRunnerV2 runs every `docker compose` command of a host check
// in the quest's own Compose project. The default project is the folder
// name, so a stack left by an earlier quest in the same folder was recreated
// in place and kept its database volume: postgres started on foreign data
// (`role "postgres" does not exist`) and the check blamed correct code. The
// approved command stays the contract; only the project it acts on is scoped.
type composeProjectRunnerV2 struct {
	inner   CompletionCheckRunner
	project string
}

func (r composeProjectRunnerV2) Run(ctx context.Context, directory, command string) (int, string, error) {
	if rest, ok := strings.CutPrefix(strings.TrimSpace(command), "docker compose "); ok {
		command = "docker compose -p " + r.project + " " + rest
	}
	return r.inner.Run(ctx, directory, command)
}

var composeProjectUnsafeV2 = regexp.MustCompile(`[^a-z0-9]+`)

// composeProjectNameV2 names the verification stack of one quest. It is stable
// across attempts of the same quest, so each attempt starts by removing what
// the previous one left.
func composeProjectNameV2(questID string) string {
	id := composeProjectUnsafeV2.ReplaceAllString(strings.ToLower(strings.TrimPrefix(questID, "quest_")), "")
	if len(id) > 16 {
		id = id[:16]
	}
	if id == "" {
		id = "verify"
	}
	return "point-" + id
}

// resetComposeProjectV2 removes the quest's verification stack together with
// its volumes. Failure is reported, not fatal: the next command will say what
// is really wrong with Docker.
func resetComposeProjectV2(ctx context.Context, directory string, runner CompletionCheckRunner) string {
	resetCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	code, output, err := runner.Run(resetCtx, directory, "docker compose down -v --remove-orphans")
	if err != nil {
		return security.Redact(err.Error())
	}
	if code != 0 {
		return lastSummaryLineV2(output)
	}
	return ""
}

// hostDiagnosticsLimit keeps the log tail readable in the card and small
// enough to hand to the planner of a repair attempt.
const hostDiagnosticsLimit = 6000

// collectComposeDiagnosticsV2 captures what the containers themselves said
// when a host check failed.
func collectComposeDiagnosticsV2(ctx context.Context, directory string, runner CompletionCheckRunner) string {
	diagCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	parts := []string{}
	if code, output, err := runner.Run(diagCtx, directory, `docker compose ps -a --format "{{.Service}}: {{.State}} {{.Status}}"`); err == nil && code == 0 && strings.TrimSpace(output) != "" {
		parts = append(parts, "Контейнеры:\n"+strings.TrimSpace(output))
	}
	if code, output, err := runner.Run(diagCtx, directory, "docker compose logs --no-color --tail=40"); err == nil && code == 0 && strings.TrimSpace(output) != "" {
		parts = append(parts, "Журнал контейнеров (хвост):\n"+strings.TrimSpace(output))
	}
	text := security.Redact(strings.Join(parts, "\n\n"))
	if runes := []rune(text); len(runes) > hostDiagnosticsLimit {
		text = "…" + string(runes[len(runes)-hostDiagnosticsLimit:])
	}
	return text
}

var diagnosticSignalV2 = regexp.MustCompile(`(?i)\b(fatal|panic|error|exception|refused|denied|does not exist|no such|cannot|failed)\b`)

// hostDiagnosticHighlightsV2 picks the few log lines that explain a failure,
// for the card: the full tail stays in the evidence bundle.
func hostDiagnosticHighlightsV2(diagnostics string, limit int) []string {
	seen := map[string]bool{}
	lines := []string{}
	for _, line := range strings.Split(diagnostics, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !diagnosticSignalV2.MatchString(line) {
			continue
		}
		// Одна и та же ошибка раз в секунду — это одна строка для человека.
		key := line
		if _, rest, ok := strings.Cut(line, "|"); ok {
			key = strings.TrimSpace(rest)
		}
		key = logTimestampV2.ReplaceAllString(key, "")
		if seen[key] {
			continue
		}
		seen[key] = true
		if runes := []rune(line); len(runes) > 240 {
			line = string(runes[:240]) + "…"
		}
		lines = append(lines, line)
		if len(lines) >= limit {
			break
		}
	}
	return lines
}

var logTimestampV2 = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(\.\d+)?( UTC)?|\[\d+\]`)

// repairableHostFailuresV2 lists host checks that failed on an observed
// answer the code can change. A check that could not run (no exit code) or
// that ends with a concrete "Нужно действие" for the human — a port held by
// another stack — is the environment's problem, and another attempt by the
// same executor would only repeat it.
func repairableHostFailuresV2(checks []domain.VerificationCheck) []domain.VerificationCheck {
	failed := []domain.VerificationCheck{}
	for _, check := range checks {
		if check.Satisfied || check.ExitCode == nil || hostCheckActionV2(check.Summary) != "" {
			continue
		}
		failed = append(failed, check)
	}
	return failed
}
