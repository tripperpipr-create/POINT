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
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
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
		return "Нужно действие: порт приложения уже занят другим процессом; освободите его или смените порт в Compose и перезапустите квест."
	}
	port := match[1]
	lookupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	code, holders, err := runner.Run(lookupCtx, directory, `docker ps --filter publish=`+port+` --format "{{.Names}} {{.Labels}}"`)
	if err != nil || code != 0 || strings.TrimSpace(holders) == "" {
		return "Нужно действие: порт " + port + " занят программой вне Docker; освободите его или смените порт в Compose и перезапустите квест."
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
	return "Нужно действие: порт " + port + " занят — " + owner + ". Остановите его (docker stop " + name + ") или смените порт в Compose и перезапустите квест."
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
