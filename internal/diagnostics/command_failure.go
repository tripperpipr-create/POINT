package diagnostics

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Причина провала команды — то, что человек и Мастер читают вместо сводки.
//
// 30.09.2026 приёмка cf-vue-apps сказала «x Build failed in 17.43s»: первая
// строка со словом failed. Настоящая причина лежала выше в stderr — npm 12
// заблокировал скрипты установки, vue-demi не переключилась на Vue 2.7, и
// rollup не нашёл hasInjectionContext. Разбор идёт от частных сигнатур к
// общим; класс говорит, чем лечится повтор, а не насколько плохо.

// Классы причин — чем лечится повтор этапа.
const (
	// FailureRuntime — среда песочницы: другой образ или версия инструмента.
	FailureRuntime = "runtime"
	// FailureCriterion — сама команда проверки сломана и требует правки.
	FailureCriterion = "criterion"
	// FailureHuman — решение за человеком: сеть, права, договор.
	FailureHuman = "human"
	// FailureTransient — сбой сети или среды Point; повтор без изменений.
	FailureTransient = "transient"
	// FailureCode — ошибка в работе или проекте; повтором не лечится.
	FailureCode = "code"
)

// CommandFailure — разобранная причина провала одной команды.
type CommandFailure struct {
	Signature string   `json:"signature"`
	Class     string   `json:"class"`
	Cause     string   `json:"cause"`
	Hint      string   `json:"hint,omitempty"`
	Evidence  []string `json:"evidence,omitempty"`
}

// CommandRun — всё, что известно о завершившейся команде.
type CommandRun struct {
	Command     string
	ExitCode    int
	TimedOut    bool
	Timeout     string
	Stdout      string
	Stderr      string
	DeniedHosts []string
}

var (
	blockedScriptsLine   = regexp.MustCompile(`(?i)install scripts blocked`)
	blockedScriptPackage = regexp.MustCompile(`(?i)install-scripts\s+(@?[a-z0-9][\w.\-]*(?:/[\w.\-]+)?)@[\w.\-]+\s*\(`)
	npmVersionNotice     = regexp.MustCompile(`(?i)version of npm available!\s*([0-9]+)\.[0-9.]+\s*->`)
	notExportedLine      = regexp.MustCompile(`"[^"]+" is not exported by "[^"]+"`)
	notFoundProgram      = regexp.MustCompile(`(?:^|[\s:])([\w.\-/]+): (?:command )?not found`)
	npmErrorCode         = regexp.MustCompile(`(?i)^npm (?:error|ERR!) code (\S+)`)
	npmErrorMessage      = regexp.MustCompile(`(?i)^npm (?:error|ERR!) (.+)`)
	enoentPath           = regexp.MustCompile(`ENOENT[^'"]*['"]([^'"]+)['"]`)
	packDestination      = regexp.MustCompile(`--pack-destination[= ]+(\S+)`)
	transientLine        = regexp.MustCompile(`(?i)wsarecv|econnreset|connection reset by peer|socket hang up|eai_again|etimedout|cannot connect to the docker daemon|tls handshake timeout|502 bad gateway|503 service unavailable`)
)

// DiagnoseCommand разбирает провал. Для успешной команды ничего не говорит.
func DiagnoseCommand(run CommandRun) (CommandFailure, bool) {
	if run.ExitCode == 0 && !run.TimedOut {
		return CommandFailure{}, false
	}
	combined := run.Stderr + "\n" + run.Stdout
	lines := meaningfulLines(combined)
	if len(run.DeniedHosts) > 0 {
		hosts := append([]string(nil), run.DeniedHosts...)
		sort.Strings(hosts)
		return CommandFailure{
			Signature: "network_denied", Class: FailureHuman,
			Cause: "сеть: не разрешён " + strings.Join(hosts, ", "),
			Hint:  "хост добавляется только новой версией наряда — это решение человека",
		}, true
	}
	if run.TimedOut {
		limit := strings.TrimSpace(run.Timeout)
		cause := "команда не уложилась в отведённое время"
		if limit != "" {
			cause = "команда не уложилась в " + limit
		}
		return CommandFailure{Signature: "timeout", Class: FailureCode, Cause: cause,
			Hint: "долгая сборка: нужен больший срок или более узкая команда", Evidence: lastLines(lines, 2)}, true
	}
	if packages := blockedInstallScripts(combined); len(packages) > 0 {
		npm := "npm"
		if match := npmVersionNotice.FindStringSubmatch(combined); match != nil {
			npm = "npm " + match[1]
		}
		cause := fmt.Sprintf("%s заблокировал скрипты установки: %s", npm, strings.Join(packages, ", "))
		evidence := []string{}
		if line := firstMatch(lines, notExportedLine); line != "" {
			cause += "; из-за этого сборка: " + line
			evidence = append(evidence, line)
		}
		return CommandFailure{
			Signature: "npm_install_scripts_blocked", Class: FailureRuntime, Cause: cause,
			Hint:     "повторить в образе Node 20 или 22 (npm 10 выполняет скрипты установки) либо разрешить эти пакеты в allowScripts проекта",
			Evidence: evidence,
		}, true
	}
	if line := firstMatchFold(lines, "ENOENT"); line != "" {
		if match := packDestination.FindStringSubmatch(run.Command); match != nil {
			dir := strings.Trim(match[1], `'"`)
			return CommandFailure{
				Signature: "pack_destination_missing", Class: FailureCriterion,
				Cause:    "каталог назначения " + dir + " не существует: npm pack его не создаёт",
				Hint:     "добавить `mkdir -p " + dir + "` перед npm pack",
				Evidence: []string{line},
			}, true
		}
		path := ""
		if match := enoentPath.FindStringSubmatch(line); match != nil {
			path = match[1]
		}
		cause := "файл или каталог не найден"
		if path != "" {
			cause += ": " + path
		}
		return CommandFailure{Signature: "enoent", Class: FailureCode, Cause: cause, Evidence: []string{line}}, true
	}
	if run.ExitCode == 127 {
		program := ""
		for _, line := range lines {
			if match := notFoundProgram.FindStringSubmatch(line); match != nil {
				program = match[1]
				break
			}
		}
		cause := "в образе нет нужной программы"
		if program != "" {
			cause = "в образе нет программы " + program
		}
		return CommandFailure{Signature: "program_missing", Class: FailureRuntime, Cause: cause,
			Hint: "повторить в образе, где она есть, или установить её командой подготовки", Evidence: lastLines(lines, 1)}, true
	}
	if line := firstMatch(lines, transientLine); line != "" {
		return CommandFailure{Signature: "transient", Class: FailureTransient, Cause: "сбой сети или среды: " + truncate(line, 160),
			Hint: "повтор без изменений", Evidence: []string{line}}, true
	}
	if line := firstMatch(lines, notExportedLine); line != "" {
		return CommandFailure{Signature: "build_missing_export", Class: FailureCode, Cause: "сборка: " + line, Evidence: []string{line}}, true
	}
	if after := lineAfter(lines, "error during build:"); after != "" {
		return CommandFailure{Signature: "build_error", Class: FailureCode, Cause: "сборка: " + truncate(after, 200), Evidence: []string{after}}, true
	}
	for i, line := range lines {
		match := npmErrorCode.FindStringSubmatch(line)
		if match == nil || strings.EqualFold(match[1], "1") {
			continue
		}
		message := ""
		for _, next := range lines[i+1:] {
			if msg := npmErrorMessage.FindStringSubmatch(next); msg != nil && !npmErrorCode.MatchString(next) && !npmNoise(next) {
				message = msg[1]
				break
			}
		}
		cause := "npm " + match[1]
		if message != "" {
			cause += ": " + truncate(message, 180)
		}
		return CommandFailure{Signature: "npm_error", Class: FailureCode, Cause: cause, Evidence: []string{line}}, true
	}
	if line := firstErrorLine(lines); line != "" {
		return CommandFailure{Signature: "first_error", Class: FailureCode, Cause: truncate(line, 200), Evidence: []string{line}}, true
	}
	return CommandFailure{Signature: "exit_code", Class: FailureCode, Cause: fmt.Sprintf("команда завершилась с кодом %d", run.ExitCode)}, true
}

// pointEnvironmentFailure — сбои самого Point, а не работы: снимок рабочей
// области не успел, песочница или Docker не ответили.
var pointEnvironmentFailure = regexp.MustCompile(`(?i)workspace mutation audit|snapshot .*(?:timed out|deadline)|create execution sandbox|cannot connect to the docker daemon|docker desktop is not running`)

var providerTransient = regexp.MustCompile(`(?i)stream stalled|stream (?:closed|ended) unexpectedly|unexpected eof|status 5\d\d|\b429\b|rate limit|too many requests|context deadline exceeded|provider (?:timeout|unavailable)|reserve model budget: context canceled`)

// DiagnoseMessage — причина по тексту ошибки этапа, когда команды нет: сбой
// модели или среды, остановка по сроку. Сбой провайдера лечится повтором.
func DiagnoseMessage(text string) CommandFailure {
	text = strings.TrimSpace(text)
	if text == "" {
		return CommandFailure{Signature: "unknown", Class: FailureCode, Cause: "этап завершился без описания причины"}
	}
	if pointEnvironmentFailure.MatchString(text) {
		return CommandFailure{Signature: "point_environment", Class: FailureTransient,
			Cause: "сбой среды Point: " + truncate(text, 200), Hint: "повтор без изменений"}
	}
	if line := firstMatch([]string{text}, providerTransient); line != "" {
		return CommandFailure{Signature: "provider_transient", Class: FailureTransient,
			Cause: "сбой модели или соединения: " + truncate(text, 200), Hint: "повтор без изменений"}
	}
	failure, _ := DiagnoseCommand(CommandRun{ExitCode: 1, Stderr: text})
	if failure.Signature == "first_error" || failure.Signature == "exit_code" {
		failure.Cause = truncate(text, 300)
	}
	return failure
}

// CauseLine — строка для вывода команды: модель видит причину сразу, а не
// перезапускает сборку, чтобы найти её в обрезанном хвосте.
func (f CommandFailure) CauseLine() string {
	if f.Cause == "" {
		return ""
	}
	if f.Hint == "" {
		return f.Cause
	}
	return f.Cause + " — " + f.Hint
}

func blockedInstallScripts(text string) []string {
	if !blockedScriptsLine.MatchString(text) {
		return nil
	}
	seen := map[string]bool{}
	names := []string{}
	for _, match := range blockedScriptPackage.FindAllStringSubmatch(text, -1) {
		if !seen[match[1]] {
			seen[match[1]] = true
			names = append(names, match[1])
		}
	}
	if len(names) == 0 {
		return []string{"пакетов проекта"}
	}
	return names
}

func meaningfulLines(text string) []string {
	out := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func firstMatch(lines []string, pattern *regexp.Regexp) string {
	for _, line := range lines {
		if match := pattern.FindString(line); match != "" {
			return match
		}
	}
	return ""
}

func firstMatchFold(lines []string, needle string) string {
	for _, line := range lines {
		if strings.Contains(line, needle) {
			return truncate(line, 200)
		}
	}
	return ""
}

func lineAfter(lines []string, marker string) string {
	for i, line := range lines {
		if strings.EqualFold(line, marker) && i+1 < len(lines) {
			return lines[i+1]
		}
	}
	return ""
}

// npmNoise — служебные строки npm, которые не называют причину.
func npmNoise(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "complete log") || strings.Contains(lower, "command failed") ||
		strings.Contains(lower, "error path") || strings.Contains(lower, "error command") || strings.Contains(lower, "error code")
}

// firstErrorLine — первая строка с ошибкой, кроме сводок вида «x Build failed
// in 17s» и служебных строк npm: они говорят, что упало, но не почему.
func firstErrorLine(lines []string) string {
	fallback := ""
	for _, line := range lines {
		lower := strings.ToLower(line)
		if npmNoise(line) || strings.Contains(lower, "build failed in") {
			continue
		}
		if fallback == "" {
			fallback = line
		}
		if strings.Contains(lower, "error") || strings.Contains(lower, "failed") || strings.Contains(lower, "cannot") {
			return line
		}
	}
	return fallback
}

func lastLines(lines []string, n int) []string {
	if len(lines) <= n {
		return append([]string(nil), lines...)
	}
	return append([]string(nil), lines[len(lines)-n:]...)
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
