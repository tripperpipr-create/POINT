// point-perf-report — куда уходит время квестов, по журналу событий ядра.
//
// Ускорение без замера — догадка. Разбор 30.09 делался руками запросами к
// hub-v2.db: 64% времени ждали модель, 33% шли команды, оркестровка — 2%. Этот
// отчёт повторяет тот разбор одной командой, чтобы каждое изменение скорости
// проверялось числами «до» и «после», а не ощущением.
//
//	go run ./cmd/point-perf-report -flows 10
//	go run ./cmd/point-perf-report -db путь/hub-v2.db -json > after.json
//
// База открывается только для чтения: рядом может работать живое ядро.
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type event struct {
	RunID, FlowRunID, Type string
	Data                   map[string]any
	At                     time.Time
}

type command struct {
	Command string `json:"command"`
	Calls   int    `json:"calls"`
	TotalMs int64  `json:"totalMs"`
}

type stage struct {
	RunID          string `json:"runId"`
	Role           string `json:"role"`
	WallMs         int64  `json:"wallMs"`
	ModelMs        int64  `json:"modelMs"`
	ModelCalls     int    `json:"modelCalls"`
	InputTokens    int64  `json:"inputTokens"`
	CachedTokens   int64  `json:"cachedTokens"`
	ToolMs         int64  `json:"toolMs"`
	CommandMs      int64  `json:"commandMs"`
	AuditMs        int64  `json:"auditMs"`
	Commands       int    `json:"commands"`
	ReasoningRetry int    `json:"reasoningRecoveries"`
	// Контрольные точки: сколько раз прогон сохранял весь разговор и во что
	// это обошлось (нарастающий итог из последнего model.requested).
	Checkpoints     int   `json:"checkpoints,omitempty"`
	CheckpointMs    int64 `json:"checkpointMs,omitempty"`
	CheckpointBytes int64 `json:"checkpointBytes,omitempty"`
	// Сервис проверок: сколько раз Point проверил этап перед завершением,
	// сколько из них провалилось, и взяла ли приёмка готовый исход.
	PreAcceptChecks int    `json:"preAcceptChecks,omitempty"`
	PreAcceptFailed int    `json:"preAcceptFailed,omitempty"`
	AcceptReused    bool   `json:"acceptReused,omitempty"`
	ShadowAgrees    string `json:"shadowAgrees,omitempty"`
}

type flowReport struct {
	FlowRunID string    `json:"flowRunId"`
	StartedAt time.Time `json:"startedAt"`
	WallMs    int64     `json:"wallMs"`
	Stages    []stage   `json:"stages"`
}

type summary struct {
	Flows        int       `json:"flows"`
	WallMs       int64     `json:"wallMs"`
	ModelMs      int64     `json:"modelMs"`
	ToolMs       int64     `json:"toolMs"`
	CommandMs    int64     `json:"commandMs"`
	AuditMs      int64     `json:"auditMs"`
	CheckpointMs int64     `json:"checkpointMs"`
	ModelCalls   int       `json:"modelCalls"`
	InputTokens  int64     `json:"inputTokens"`
	CachedTokens int64     `json:"cachedTokens"`
	TopCommands  []command `json:"topCommands"`
}

type report struct {
	Summary summary      `json:"summary"`
	Flows   []flowReport `json:"flows"`
}

func defaultDatabase() string {
	base := os.Getenv("APPDATA")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "Point", "User", "globalStorage", "local-agent.local-agent-workbench", "hub-v2.db")
}

func main() {
	database := flag.String("db", defaultDatabase(), "путь к hub-v2.db")
	flows := flag.Int("flows", 10, "сколько последних прогонов Flow разобрать")
	asJSON := flag.Bool("json", false, "вывести JSON вместо таблицы")
	flag.Parse()
	result, err := build(*database, *flows)
	if err != nil {
		fmt.Fprintln(os.Stderr, "point-perf-report:", err)
		os.Exit(1)
	}
	if *asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(result)
		return
	}
	printReport(os.Stdout, result)
}

func build(path string, limit int) (report, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return report{}, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT flow_run_id FROM events WHERE flow_run_id <> '' GROUP BY flow_run_id ORDER BY MAX(sequence) DESC LIMIT ?`, limit)
	if err != nil {
		return report{}, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return report{}, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var result report
	commands := map[string]*command{}
	for i := len(ids) - 1; i >= 0; i-- {
		events, loadErr := loadEvents(db, ids[i])
		if loadErr != nil {
			return report{}, loadErr
		}
		flow := analyze(ids[i], events, commands)
		result.Flows = append(result.Flows, flow)
		result.Summary.Flows++
		result.Summary.WallMs += flow.WallMs
		for _, s := range flow.Stages {
			result.Summary.ModelMs += s.ModelMs
			result.Summary.ToolMs += s.ToolMs
			result.Summary.CommandMs += s.CommandMs
			result.Summary.AuditMs += s.AuditMs
			result.Summary.CheckpointMs += s.CheckpointMs
			result.Summary.ModelCalls += s.ModelCalls
			result.Summary.InputTokens += s.InputTokens
			result.Summary.CachedTokens += s.CachedTokens
		}
	}
	for _, c := range commands {
		result.Summary.TopCommands = append(result.Summary.TopCommands, *c)
	}
	sort.Slice(result.Summary.TopCommands, func(i, j int) bool {
		return result.Summary.TopCommands[i].TotalMs > result.Summary.TopCommands[j].TotalMs
	})
	if len(result.Summary.TopCommands) > 12 {
		result.Summary.TopCommands = result.Summary.TopCommands[:12]
	}
	return result, nil
}

func loadEvents(db *sql.DB, flowRunID string) ([]event, error) {
	rows, err := db.Query(`SELECT run_id, flow_run_id, type, data, created_at FROM events WHERE flow_run_id = ? ORDER BY sequence`, flowRunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []event
	for rows.Next() {
		var item event
		var data, created string
		if err = rows.Scan(&item.RunID, &item.FlowRunID, &item.Type, &data, &created); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(data), &item.Data)
		item.At, _ = time.Parse(time.RFC3339Nano, created)
		events = append(events, item)
	}
	return events, rows.Err()
}

// analyze раскладывает события одного прогона Flow по этапам. Вызов модели —
// от model.requested до model.responded (или до следующего события, если
// ответа нет: оборванный поток тоже время ожидания).
func analyze(flowRunID string, events []event, commands map[string]*command) flowReport {
	flow := flowReport{FlowRunID: flowRunID}
	if len(events) == 0 {
		return flow
	}
	flow.StartedAt = events[0].At
	flow.WallMs = events[len(events)-1].At.Sub(events[0].At).Milliseconds()
	stages := map[string]*stage{}
	var order []string
	first, last := map[string]time.Time{}, map[string]time.Time{}
	requested := map[string]time.Time{}
	callCommand := map[string]string{}
	for _, e := range events {
		if e.RunID == "" {
			continue
		}
		s, ok := stages[e.RunID]
		if !ok {
			s = &stage{RunID: e.RunID}
			stages[e.RunID] = s
			order = append(order, e.RunID)
			first[e.RunID] = e.At
		}
		last[e.RunID] = e.At
		// Запрос без ответа (оборванный или зависший поток) закрывается первым
		// же событием прогона не из потока модели: до него прогон ждал модель.
		if at, pending := requested[e.RunID]; pending && !strings.HasPrefix(e.Type, "model.") {
			s.ModelMs += e.At.Sub(at).Milliseconds()
			delete(requested, e.RunID)
		}
		switch e.Type {
		case "run.started":
			if role, _ := e.Data["stageRole"].(string); role != "" {
				s.Role = role
			}
		case "model.requested":
			requested[e.RunID] = e.At
			s.ModelCalls++
			if cost, ok := e.Data["checkpoints"].(map[string]any); ok {
				s.Checkpoints, s.CheckpointMs, s.CheckpointBytes = int(number(cost["count"])), int64(number(cost["ms"])), int64(number(cost["bytes"]))
			}
		case "model.responded":
			if at, ok := requested[e.RunID]; ok {
				s.ModelMs += e.At.Sub(at).Milliseconds()
				delete(requested, e.RunID)
			}
		case "model.usage":
			if usage, ok := e.Data["usage"].(map[string]any); ok {
				s.InputTokens += int64(number(usage["inputTokens"]))
				s.CachedTokens += int64(number(usage["cachedInputTokens"]))
			}
		case "completion.checked":
			switch kind, _ := e.Data["checkKind"].(string); kind {
			case "pre_accept":
				s.PreAcceptChecks++
				if status, _ := e.Data["status"].(string); status != "accepted" {
					s.PreAcceptFailed++
				}
			case "accept":
				if s.Role == "" {
					s.Role = "accept"
				}
				if note, ok := e.Data["verificationService"].(map[string]any); ok {
					if id, _ := note["reusedFrom"].(string); id != "" {
						s.AcceptReused = true
					}
					if agrees, ok := note["agrees"].(bool); ok {
						s.ShadowAgrees = fmt.Sprint(agrees)
					}
				}
			}
		case "agent.guardrail":
			if kind, _ := e.Data["kind"].(string); strings.Contains(kind, "reasoning_budget") {
				s.ReasoningRetry++
			}
		case "tool.requested":
			if args, ok := e.Data["arguments"].(map[string]any); ok {
				if cmd, _ := args["command"].(string); cmd != "" {
					callID, _ := e.Data["callId"].(string)
					callCommand[callID] = cmd
				}
			}
		case "tool.finished":
			duration := int64(number(e.Data["durationMs"]))
			s.ToolMs += duration
			if timing, ok := e.Data["timing"].(map[string]any); ok {
				s.AuditMs += int64(number(timing["auditTotalMs"]))
			}
			if tool, _ := e.Data["tool"].(string); tool == "run_command" {
				s.Commands++
				s.CommandMs += duration
				callID, _ := e.Data["callId"].(string)
				key := commandKey(callCommand[callID])
				c := commands[key]
				if c == nil {
					c = &command{Command: key}
					commands[key] = c
				}
				c.Calls++
				c.TotalMs += duration
			}
		}
	}
	for _, id := range order {
		s := stages[id]
		if at, pending := requested[id]; pending {
			s.ModelMs += last[id].Sub(at).Milliseconds()
		}
		s.WallMs = last[id].Sub(first[id]).Milliseconds()
		flow.Stages = append(flow.Stages, *s)
	}
	return flow
}

// commandKey сводит команды к семейству: первые три слова без путей, чтобы
// «npm run verify» в разных папках считался одной строкой.
func commandKey(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "(неизвестно)"
	}
	// «cd папка && …» — место, а не команда: семейство определяет то, что после.
	for strings.HasPrefix(cmd, "cd ") {
		_, rest, ok := strings.Cut(cmd, "&&")
		if !ok {
			break
		}
		cmd = strings.TrimSpace(rest)
	}
	fields := strings.Fields(cmd)
	if len(fields) > 4 {
		fields = fields[:4]
	}
	key := strings.Join(fields, " ")
	if len(key) > 60 {
		key = key[:60]
	}
	return key
}

func number(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case json.Number:
		f, _ := v.Float64()
		return f
	}
	return 0
}

func seconds(ms int64) string { return fmt.Sprintf("%.0f с", float64(ms)/1000) }

func share(part, whole int64) string {
	if whole <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", float64(part)*100/float64(whole))
}

func printReport(out io.Writer, r report) {
	for _, flow := range r.Flows {
		fmt.Fprintf(out, "Flow %s  %s  всего %s\n", flow.FlowRunID, flow.StartedAt.Local().Format("02.01 15:04"), seconds(flow.WallMs))
		for _, s := range flow.Stages {
			role := s.Role
			if role == "" {
				role = "—"
			}
			fmt.Fprintf(out, "  %-12s %7s  модель %7s (%d выз.)  команды %7s (%d)  аудит %6s  кэш %s\n",
				role, seconds(s.WallMs), seconds(s.ModelMs), s.ModelCalls, seconds(s.CommandMs), s.Commands, seconds(s.AuditMs), share(s.CachedTokens, s.InputTokens))
			if s.PreAcceptChecks > 0 {
				fmt.Fprintf(out, "               проверки Point перед приёмкой: %d, из них не прошло %d\n", s.PreAcceptChecks, s.PreAcceptFailed)
			}
			if s.AcceptReused {
				fmt.Fprintln(out, "               приёмка взяла готовый исход проверок")
			} else if s.ShadowAgrees != "" {
				fmt.Fprintf(out, "               тень: готовый исход совпал бы с прогоном приёмки — %s\n", s.ShadowAgrees)
			}
		}
	}
	sum := r.Summary
	fmt.Fprintf(out, "\nИтого по %d Flow: %s. Модель %s (%s), команды %s (%s), аудит папки %s (%s), контрольные точки %s (%s). Вызовов модели %d, кэш промпта %s.\n",
		sum.Flows, seconds(sum.WallMs), seconds(sum.ModelMs), share(sum.ModelMs, sum.WallMs), seconds(sum.CommandMs), share(sum.CommandMs, sum.WallMs),
		seconds(sum.AuditMs), share(sum.AuditMs, sum.WallMs), seconds(sum.CheckpointMs), share(sum.CheckpointMs, sum.WallMs), sum.ModelCalls, share(sum.CachedTokens, sum.InputTokens))
	if len(sum.TopCommands) > 0 {
		fmt.Fprintln(out, "\nСамые дорогие команды:")
		for _, c := range sum.TopCommands {
			fmt.Fprintf(out, "  %7s  %3d×  %s\n", seconds(c.TotalMs), c.Calls, c.Command)
		}
	}
}
