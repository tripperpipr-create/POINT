package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/events"
)

func TestV2SourceWorkOrderApprovalContract(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("USERPROFILE", t.TempDir())
	application, err := app.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	server := httptest.NewServer(New(application, events.NewHub(), nil, "", "").Handler())
	t.Cleanup(server.Close)

	sourceBody := doV2JSON(t, server.URL+"/api/v2/sources/preview", map[string]any{"kind": "text", "label": "ТЗ", "content": "Создать health endpoint"})
	if strings.Contains(string(sourceBody), "storagePath") {
		t.Fatalf("internal source storage path leaked: %s", sourceBody)
	}
	var source domain.SourceSnapshot
	if err = json.Unmarshal(sourceBody, &source); err != nil || source.ID == "" || source.Digest == "" {
		t.Fatalf("source response=%s err=%v", sourceBody, err)
	}

	order := domain.WorkOrder{
		State: "ready", Goal: "Создать API", Scope: []string{"health endpoint"},
		Sources:   []domain.SourceSnapshotRef{{ID: source.ID, Kind: source.Kind, Label: source.Label, Digest: source.Digest}},
		Criteria:  []domain.AcceptanceCriterion{{ID: "health", Kind: "manual", Text: "GET /health returns 200"}},
		Workspace: domain.WorkspacePlan{Mode: "managed", Isolation: "snapshot"},
		Stack:     domain.StackPresetRef{ID: "api", Version: "1", Category: "api", Source: "benchmark"},
		Routing:   domain.ModelRoutingPolicy{Mode: "fixed", FixedConnectionID: "connection", FixedModel: "model", FallbackMode: "auto"},
		Budget:    domain.BudgetEnvelope{Preset: "small", Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxAttempts: 1},
		Delivery:  domain.DeliveryPolicy{ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30},
	}
	createdBody := doV2JSON(t, server.URL+"/api/v2/work-orders", order)
	var created domain.WorkOrder
	if err = json.Unmarshal(createdBody, &created); err != nil {
		t.Fatalf("work order response=%s err=%v", createdBody, err)
	}
	revision := created
	revision.Goal = "Создать проверенный API"
	revisionBody := doV2JSON(t, server.URL+"/api/v2/work-orders/"+created.ID+"/revise", map[string]any{
		"expectedVersion": created.Version, "expectedDigest": domain.WorkOrderDigest(created), "idempotencyKey": "http-revision-once", "workOrder": revision,
	})
	var revised domain.WorkOrder
	if err = json.Unmarshal(revisionBody, &revised); err != nil || revised.Version != created.Version+1 || revised.Goal != revision.Goal {
		t.Fatalf("revision response=%s err=%v", revisionBody, err)
	}
	revisionReplayBody := doV2JSON(t, server.URL+"/api/v2/work-orders/"+created.ID+"/revise", map[string]any{
		"expectedVersion": created.Version, "expectedDigest": domain.WorkOrderDigest(created), "idempotencyKey": "http-revision-once", "workOrder": revision,
	})
	var revisionReplay domain.WorkOrder
	if err = json.Unmarshal(revisionReplayBody, &revisionReplay); err != nil || revisionReplay.Version != revised.Version || domain.WorkOrderDigest(revisionReplay) != domain.WorkOrderDigest(revised) {
		t.Fatalf("revision replay=%s err=%v", revisionReplayBody, err)
	}
	// A missing agent is now a staffing proposal. It must be settled before
	// approval can create a Quest or provision the managed workspace.
	requestBody, err := json.Marshal(map[string]any{
		"version": revised.Version, "digest": domain.WorkOrderDigest(revised), "idempotencyKey": "http-contract-once",
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(server.URL+"/api/v2/work-orders/"+revised.ID+"/approve", "application/json", bytes.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "staffing") {
		t.Fatalf("unsettled roster approval returned %d: %s", response.StatusCode, body)
	}
}

// awaitWorkOrderRuntimeV2 ждёт, пока фоновый запуск наряда доведёт квест до
// состояния, которое видно человеку. Без ожидания проверка читала бы
// «Готовим план выполнения» и считала это исходом.
func awaitWorkOrderRuntimeV2(t *testing.T, base, workOrderID string) domain.WorkOrderRuntime {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last domain.WorkOrderRuntime
	for time.Now().Before(deadline) {
		response, err := http.Get(base + "/api/v2/work-orders/" + workOrderID)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET work order returned %d: %s", response.StatusCode, body)
		}
		var order domain.WorkOrder
		if err = json.Unmarshal(body, &order); err != nil {
			t.Fatalf("work order response=%s err=%v", body, err)
		}
		if order.Runtime != nil {
			last = *order.Runtime
			if last.Status != domain.QuestPreflight {
				return last
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("work order %s never left preflight: %#v", workOrderID, last)
	return last
}

func doV2JSON(t *testing.T, target string, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(target, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("POST %s returned %d: %s", target, response.StatusCode, body)
	}
	return body
}
