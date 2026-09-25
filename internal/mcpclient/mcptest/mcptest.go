// Package mcptest — поддельный stdio MCP-сервер для тестов других пакетов.
//
// Тест запускает самого себя (os.Args[0]) с переменной окружения, TestMain
// видит её и вызывает Serve вместо тестов. Так проверяется настоящий путь:
// процесс, трубы, надзор — без node и без сети.
package mcptest

import (
	"bufio"
	"encoding/json"
	"io"
)

// Tool — инструмент, который отдаёт поддельный сервер.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations json.RawMessage `json:"annotations,omitempty"`
}

// Handler отвечает на tools/call: текст результата и признак isError.
type Handler func(name string, arguments json.RawMessage) (text string, isError bool)

// Serve говорит по протоколу до закрытия in.
func Serve(in io.Reader, out io.Writer, name string, tools []Tool, handle Handler) {
	writer := bufio.NewWriter(out)
	send := func(value any) {
		raw, _ := json.Marshal(value)
		_, _ = writer.Write(raw)
		_ = writer.WriteByte('\n')
		_ = writer.Flush()
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1<<20), 16<<20)
	for scanner.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &msg) != nil || msg.Method == "" || len(msg.ID) == 0 {
			continue
		}
		switch msg.Method {
		case "initialize":
			send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": name, "version": "test"},
			}})
		case "tools/list":
			send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"tools": tools}})
		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			text, isError := handle(params.Name, params.Arguments)
			send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
				"isError": isError,
			}})
		default:
			send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
}
