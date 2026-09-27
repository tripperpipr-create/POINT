package app

// Живое состояние доставленного приложения.
//
// Запуск доставленного приложения шёл одним синхронным вызовом `docker compose
// up -d`: карточка квеста молчала, пока команда не кончится, а потом получала
// две тысячи знаков вывода. Сборка образов идёт минутами, и человек не видел,
// что происходит, — а клиент бросал ожидание через двадцать секунд.
//
// Здесь три вещи. Вывод команды отводится построчно в реестр, который читает
// GET /api/v2/master/quests/{id}/application, пока POST ещё выполняется. После
// `up -d` ядро ждёт ответа по адресу доставки (только loopback) и сообщает,
// страница это или API, — по этому клиент решает, открыть ли браузер. И вид
// приложения — веб, сервис, консольное, настольное — выводится из договора
// наряда и квитанции доставки, чтобы клиент знал, как его запускать.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/security"
)

const (
	deliveredAppLineLimit   = 160
	deliveredAppOutputLimit = 256 * 1024
	deliveredAppReadyWait   = 60 * time.Second
)

// deliveredAppInspector — то, что умеет только настоящий Docker-раннер:
// построчный вывод, счёт работающих контейнеров и проба адреса. Подставной
// раннер тестов его не реализует, и тогда ядро ведёт себя как прежде.
type deliveredAppInspector interface {
	RunStreaming(ctx context.Context, directory string, onLine func(string), arguments ...string) (string, error)
	RunningServices(ctx context.Context, directory, composePath string) (int, error)
	ProbeURL(ctx context.Context, address string) (int, string, error)
}

type deliveredAppProgress struct {
	action      string
	status      string
	lines       []string
	ready       bool
	httpStatus  int
	contentType string
	startedAt   time.Time
	updatedAt   time.Time
}

// deliveredAppLive — вывод идущих действий по квестам. Нулевое значение готово
// к работе: карта заводится при первом запуске.
type deliveredAppLive struct {
	mu    sync.Mutex
	items map[string]*deliveredAppProgress
}

// begin отказывает, если по квесту уже идёт действие: второй `compose up`
// поверх первого запустил бы ту же сборку дважды.
func (live *deliveredAppLive) begin(questID, action string) bool {
	live.mu.Lock()
	defer live.mu.Unlock()
	if live.items == nil {
		live.items = map[string]*deliveredAppProgress{}
	}
	if current := live.items[questID]; current != nil && current.status == "executing" {
		return false
	}
	now := time.Now().UTC()
	live.items[questID] = &deliveredAppProgress{action: action, status: "executing", startedAt: now, updatedAt: now}
	return true
}

func (live *deliveredAppLive) line(questID, text string) {
	text = strings.TrimSpace(security.Redact(text))
	if text == "" {
		return
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	item := live.items[questID]
	if item == nil {
		return
	}
	item.lines = append(item.lines, text)
	if len(item.lines) > deliveredAppLineLimit {
		item.lines = item.lines[len(item.lines)-deliveredAppLineLimit:]
	}
	item.updatedAt = time.Now().UTC()
}

func (live *deliveredAppLive) finish(questID string, control domain.DeliveredApplicationControl) {
	live.mu.Lock()
	defer live.mu.Unlock()
	item := live.items[questID]
	if item == nil {
		return
	}
	item.status = control.Status
	item.ready, item.httpStatus, item.contentType = control.Ready, control.HTTPStatus, control.ContentType
	item.updatedAt = time.Now().UTC()
}

func (live *deliveredAppLive) snapshot(questID string) (deliveredAppProgress, bool) {
	live.mu.Lock()
	defer live.mu.Unlock()
	item := live.items[questID]
	if item == nil {
		return deliveredAppProgress{}, false
	}
	copied := *item
	copied.lines = append([]string(nil), item.lines...)
	return copied, true
}

// lineTap копит вывод процесса и отдаёт его по строкам. Docker compose
// перерисовывает прогресс через \r — такие строки тоже считаются строками.
type lineTap struct {
	mu      sync.Mutex
	all     bytes.Buffer
	pending []byte
	onLine  func(string)
}

func (tap *lineTap) Write(chunk []byte) (int, error) {
	tap.mu.Lock()
	defer tap.mu.Unlock()
	if tap.all.Len() < deliveredAppOutputLimit {
		tap.all.Write(chunk)
	}
	tap.pending = append(tap.pending, chunk...)
	for {
		index := bytes.IndexAny(tap.pending, "\r\n")
		if index < 0 {
			break
		}
		line := string(tap.pending[:index])
		tap.pending = tap.pending[index+1:]
		if strings.TrimSpace(line) != "" && tap.onLine != nil {
			tap.onLine(line)
		}
	}
	return len(chunk), nil
}

func (tap *lineTap) flush() string {
	tap.mu.Lock()
	defer tap.mu.Unlock()
	if strings.TrimSpace(string(tap.pending)) != "" && tap.onLine != nil {
		tap.onLine(string(tap.pending))
	}
	tap.pending = nil
	return tap.all.String()
}

func (dockerDeliveredAppRunner) RunStreaming(ctx context.Context, directory string, onLine func(string), arguments ...string) (string, error) {
	command := osproc.CommandContext(ctx, "docker", arguments...)
	command.Dir = directory
	tap := &lineTap{onLine: onLine}
	command.Stdout, command.Stderr = tap, tap
	err := command.Run()
	return tap.flush(), err
}

func (runner dockerDeliveredAppRunner) RunningServices(ctx context.Context, directory, composePath string) (int, error) {
	output, err := runner.Run(ctx, directory, "compose", "-f", composePath, "ps", "--status", "running", "--quiet")
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count, nil
}

func (dockerDeliveredAppRunner) ProbeURL(ctx context.Context, address string) (int, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return 0, "", err
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	return response.StatusCode, response.Header.Get("Content-Type"), nil
}

// loopbackURL: ядро ждёт ответа только от адреса на этой машине — адрес пишет
// договор наряда, и ходить по нему в сеть ядро не должно.
func loopbackURL(address string) bool {
	parsed, err := url.Parse(strings.TrimSpace(address))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// waitDeliveredAppReady ждёт первого ответа по адресу доставки. Ответ 5xx —
// ещё не готово: сервис поднят, но за ним ещё стартует приложение.
func waitDeliveredAppReady(ctx context.Context, inspector deliveredAppInspector, address string, say func(string)) (bool, int, string) {
	say("Ждём ответа " + address + " …")
	deadline := time.Now().Add(deliveredAppReadyWait)
	lastStatus, lastType, lastErr := 0, "", error(nil)
	for time.Now().Before(deadline) {
		status, contentType, err := inspector.ProbeURL(ctx, address)
		lastStatus, lastType, lastErr = status, contentType, err
		if err == nil && status < 500 {
			say(fmt.Sprintf("Отвечает: %d · %s", status, firstNonEmptyString(strings.TrimSpace(strings.Split(contentType, ";")[0]), "без типа содержимого")))
			return true, status, contentType
		}
		select {
		case <-ctx.Done():
			return false, lastStatus, lastType
		case <-time.After(time.Second):
		}
	}
	if lastErr != nil {
		say("Не ответил за 60 с: " + security.Redact(lastErr.Error()))
	} else {
		say(fmt.Sprintf("Не ответил за 60 с: последний ответ %d", lastStatus))
	}
	return false, lastStatus, lastType
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type deliveredAppTargetV2 struct {
	approval    domain.WorkOrderApproval
	bundle      domain.EvidenceBundle
	receipt     domain.DeliveryReceipt
	composeFile string
	composePath string
}

// resolveDeliveredAppTargetV2 проверяет, что квест доставлен и квитанция
// принадлежит утверждённой папке, и находит compose-файл. Без compose-файла
// цель остаётся валидной: такое приложение запускают из терминала.
func (a *App) resolveDeliveredAppTargetV2(ctx context.Context, questID string) (deliveredAppTargetV2, error) {
	approval, err := a.store.WorkOrderApprovalByQuestV2(ctx, questID)
	if err != nil {
		return deliveredAppTargetV2{}, err
	}
	bundle, err := a.store.GetEvidenceBundle(ctx, questID)
	if err != nil {
		return deliveredAppTargetV2{}, err
	}
	if bundle.DeliveryReceipt == nil {
		return deliveredAppTargetV2{}, errors.New("quest has no delivery receipt")
	}
	receipt := *bundle.DeliveryReceipt
	if filepath.Clean(receipt.Target) != filepath.Clean(approval.WorkOrder.Workspace.Path) {
		return deliveredAppTargetV2{}, errors.New("delivery receipt does not match the approved target")
	}
	target := deliveredAppTargetV2{approval: approval, bundle: bundle, receipt: receipt}
	composeFile := strings.TrimSpace(receipt.ComposeFile)
	if composeFile == "" {
		composeFile, _ = discoverComposeFileV2(receipt.Target)
	}
	if composeFile != "" {
		if filepath.Base(composeFile) != composeFile {
			return deliveredAppTargetV2{}, errors.New("delivery receipt compose path is not a root file")
		}
		path := filepath.Join(receipt.Target, composeFile)
		if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
			target.composeFile, target.composePath = composeFile, path
		}
	}
	return target, nil
}

var deliveredAppLoopbackURL = regexp.MustCompile(`https?://(?:localhost|127\.0\.0\.1|\[::1\])(?::\d+)?(?:/[^\s"'\\<>]*)?`)

// deliveredAppURLV2 — адрес приложения. Договор пишет его не всегда: наряд
// Go-сервиса с /health доставлялся без applicationUrl, и карточка называла его
// «сервисом» без адреса, хотя проверки уже ходили на http://localhost:8080.
// Тогда адрес берётся из самих проверок — проверки завершения, потом
// доказательства условий, потом аргументы условий, — и только на этой машине.
func deliveredAppURLV2(target deliveredAppTargetV2) string {
	if value := strings.TrimSpace(target.receipt.URL); value != "" {
		return value
	}
	order := target.approval.WorkOrder
	candidates := []string{order.Delivery.ApplicationURL}
	for _, check := range order.Completion.Checks {
		if check.Kind == "health" || check.Kind == "http_smoke" {
			candidates = append(candidates, check.URL, check.Command)
		}
	}
	for _, check := range order.Completion.Checks {
		candidates = append(candidates, check.URL, check.Command)
	}
	for _, proof := range target.bundle.Criteria {
		candidates = append(candidates, proof.Command)
	}
	for _, criterion := range order.Criteria {
		candidates = append(candidates, string(criterion.Arguments))
	}
	for _, candidate := range candidates {
		if found := deliveredAppLoopbackURL.FindString(candidate); found != "" {
			return strings.TrimRight(found, ".,;:)")
		}
	}
	return ""
}

// deliveredAppKindV2 — как запускать доставленное: compose поднимает веб или
// сервис; консольное и настольное приложение запускают командой в терминале,
// которую договор уже проверял (проверка завершения cli_smoke или live_smoke).
func deliveredAppKindV2(target deliveredAppTargetV2) (kind, launch, command string) {
	if target.composePath != "" {
		if deliveredAppURLV2(target) != "" {
			return "web", "compose", ""
		}
		return "service", "compose", ""
	}
	for _, check := range target.approval.WorkOrder.Completion.Checks {
		if (check.Kind == "cli_smoke" || check.Kind == "live_smoke") && strings.TrimSpace(check.Command) != "" {
			command = strings.TrimSpace(check.Command)
			break
		}
	}
	switch strings.TrimSpace(target.approval.WorkOrder.Stack.Category) {
	case "cli":
		kind = "cli"
	case "desktop-mobile":
		kind = "desktop"
	case "data":
		kind = "data"
	default:
		kind = "web"
	}
	if command != "" {
		return kind, "terminal", command
	}
	return kind, "folder", ""
}

// DeliveredApplicationStateV2 — снимок приложения для карточки квеста: вид,
// способ запуска, идущее действие с его выводом или последнее из журнала.
// probe дополнительно спрашивает Docker, сколько контейнеров работает сейчас.
func (a *App) DeliveredApplicationStateV2(ctx context.Context, questID string, probe bool) (domain.DeliveredApplicationState, error) {
	questID = strings.TrimSpace(questID)
	if questID == "" {
		return domain.DeliveredApplicationState{}, errors.New("quest is required")
	}
	target, err := a.resolveDeliveredAppTargetV2(ctx, questID)
	if err != nil {
		return domain.DeliveredApplicationState{}, err
	}
	kind, launch, command := deliveredAppKindV2(target)
	state := domain.DeliveredApplicationState{
		QuestID: questID, Kind: kind, Launch: launch, URL: deliveredAppURLV2(target),
		Target: target.receipt.Target, ComposeFile: target.composeFile, Command: command, Status: "idle",
	}
	if live, ok := a.deliveredApps.snapshot(questID); ok {
		state.Action, state.Status, state.Lines = live.action, live.status, live.lines
		state.InFlight = live.status == "executing"
		state.Ready, state.HTTPStatus, state.ContentType = live.ready, live.httpStatus, live.contentType
		state.StartedAt, state.UpdatedAt = live.startedAt, live.updatedAt
	} else if last, found, lastErr := a.store.LatestDeliveredAppControlV2(ctx, questID); lastErr != nil {
		return domain.DeliveredApplicationState{}, lastErr
	} else if found {
		state.Action, state.Status, state.UpdatedAt = last.Action, last.Status, last.UpdatedAt
		state.Ready, state.HTTPStatus, state.ContentType = last.Ready, last.HTTPStatus, last.ContentType
		for _, line := range strings.Split(last.Summary, "\n") {
			if strings.TrimSpace(line) != "" {
				state.Lines = append(state.Lines, strings.TrimSpace(line))
			}
		}
	}
	// Журнал помнит, что было сделано, а не что есть сейчас: Docker мог
	// перезапуститься, контейнер — упасть. Проба сверяет это с живыми
	// контейнерами, пока никакое действие не идёт.
	inspector, inspectable := a.deliveredAppRunnerOrDefault().(deliveredAppInspector)
	if probe && inspectable && !state.InFlight && state.Launch == "compose" {
		probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		services, probeErr := inspector.RunningServices(probeCtx, target.receipt.Target, target.composePath)
		cancel()
		if probeErr == nil {
			state.Services, state.Probed = services, true
			if services > 0 && state.Status != "running" {
				state.Status = "running"
			} else if services == 0 && state.Status == "running" {
				state.Status = "stopped"
			}
		}
	}
	return state, nil
}

func (a *App) deliveredAppRunnerOrDefault() DeliveredAppRunner {
	if a.deliveredAppRunner != nil {
		return a.deliveredAppRunner
	}
	return dockerDeliveredAppRunner{}
}
