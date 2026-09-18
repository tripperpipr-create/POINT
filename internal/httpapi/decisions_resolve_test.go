package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/events"
)

// Очередь решений умеет то, чем сама себя объявила.
//
// Каждый элемент очереди несёт описание запроса, которым его решают: путь, имя
// поля решения, имя поля идентификатора и значения. Клиент собирает тело ровно
// по этому описанию — и оно обязано доходить до логики маршрута.
//
// Обязано, но не доходило. Тело собиралось по одному правилу на всех — «поле
// решения плюс id», — а ядро отвергает чужие поля целиком: каждое нажатие в
// очереди возвращало 400 «unknown field». Ни подтверждение шага, ни узел Flow,
// ни предложение квеста, ни действие компаньона нельзя было ни принять, ни
// отклонить; работал единственный вид, чей маршрут тело вовсе не читает.
//
// Проверка нарочно грубая: идентификаторы вымышленные, поэтому ответы будут
// «не найдено». Важно ровно одно — что это отказ логики, а не отказ разбора.
func TestDecisionQueueBodiesReachTheirRoutes(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	application, err := app.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if err = application.SetWorkspaceBoundary(root); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(application, events.NewHub(), nil, "", "").Handler())
	defer server.Close()

	// Описания взяты те же, что кладёт в очередь internal/app/decisions.go.
	for _, resolve := range []app.DecisionResolve{
		{Path: "/api/approvals/appr_0123abcd/resolve", Field: "allow", Accept: "approve", Reject: "deny", AcceptValue: true, RejectValue: false},
		{Path: "/api/flow-runs/fr_0123abcd/nodes/n1/resolve", Field: "approved", Accept: "approve", Reject: "reject", AcceptValue: true, RejectValue: false},
		{Path: "/api/quest-proposals/decide", Field: "action", Accept: "start", Reject: "ignore", IDField: "proposalId"},
		{Path: "/api/companion/actions/decide", Field: "action", Accept: "apply", Reject: "ignore", IDField: "proposalId"},
	} {
		for _, side := range []string{"accept", "reject"} {
			// Ровно то, что собирает расширение по описанию.
			body := map[string]any{}
			value := any(resolve.Accept)
			if side == "reject" {
				value = any(resolve.Reject)
			}
			if side == "accept" && resolve.AcceptValue != nil {
				value = resolve.AcceptValue
			}
			if side == "reject" && resolve.RejectValue != nil {
				value = resolve.RejectValue
			}
			if resolve.Field != "" {
				body[resolve.Field] = value
			}
			if resolve.IDField != "" {
				body[resolve.IDField] = "x_0123abcd"
			}
			payload, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.Post(server.URL+resolve.Path, "application/json", bytes.NewReader(payload))
			if err != nil {
				t.Fatal(err)
			}
			answer := make(map[string]any)
			_ = json.NewDecoder(response.Body).Decode(&answer)
			_ = response.Body.Close()

			problem, _ := answer["error"].(map[string]any)
			code, _ := problem["code"].(string)
			message, _ := problem["message"].(string)
			if code == "invalid_json" {
				t.Fatalf("%s (%s): маршрут не принял тело, собранное по его же описанию — %s", resolve.Path, side, message)
			}
			if strings.Contains(message, "unknown field") {
				t.Fatalf("%s (%s): в теле поле, которого маршрут не знает — %s", resolve.Path, side, message)
			}
		}
	}
}
