package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"local-agent-workbench/internal/domain"
)

// Сколько вывода команды видит модель в одном потоке. Полный вывод (до
// предела run_command) остаётся в журнале и доказательствах; модели хватает
// начала и хвоста, где стоит ошибка сборки. Прежде каждая многословная
// команда (npm ci, сборка) отдавала модели до 64 КиБ на поток, и они жили в
// каждом следующем запросе.
const (
	modelCommandHeadBytes = 4 * 1024
	modelCommandTailBytes = 12 * 1024
)

// modelFacingResult сокращает для модели вывод команд; остальные результаты
// не трогает.
func modelFacingResult(tool string, result domain.ToolResult) domain.ToolResult {
	if tool != "run_command" && !strings.HasPrefix(tool, "customtool_") || len(result.Output) <= modelCommandHeadBytes+modelCommandTailBytes {
		return result
	}
	var output map[string]any
	if json.Unmarshal(result.Output, &output) != nil {
		return result
	}
	changed := false
	for _, stream := range []string{"stdout", "stderr"} {
		text, ok := output[stream].(string)
		if !ok || len(text) <= modelCommandHeadBytes+modelCommandTailBytes {
			continue
		}
		output[stream] = headAndTail(text, modelCommandHeadBytes, modelCommandTailBytes)
		changed = true
	}
	if !changed {
		return result
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(output) != nil {
		return result
	}
	shortened := result
	shortened.Output = bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
	shortened.Truncated = true
	return shortened
}

func headAndTail(text string, head, tail int) string {
	headEnd := head
	for headEnd > 0 && !utf8.RuneStart(text[headEnd]) {
		headEnd--
	}
	tailStart := len(text) - tail
	for tailStart < len(text) && !utf8.RuneStart(text[tailStart]) {
		tailStart++
	}
	omitted := tailStart - headEnd
	return text[:headEnd] + fmt.Sprintf("\n…[%d bytes omitted for the model; the full output is in the run journal]…\n", omitted) + text[tailStart:]
}
