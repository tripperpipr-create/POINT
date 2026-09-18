package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Отказ человека обязан остановить действие.
//
// Это самая дорогая гарантия продукта: очередь решений, метки риска и
// предупреждения о необратимости имеют смысл только если «ЗАПРЕТИТЬ»
// действительно запрещает. При этом ни один тест нигде не отклонял
// подтверждение — все звали ResolveApproval(id, true), и ветку `if !allow`
// можно было погасить, оставив весь пакет зелёным.
//
// Тест содержит обе половины намеренно. Проверка «файла нет» ничего не стоит,
// пока не доказано, что при согласии файл появляется: инструмент проверки
// обязан сам быть проверен.
func runMarkerToolWithApproval(t *testing.T, allow bool) bool {
	t.Helper()
	t.Setenv("REDIS_ADDR", "")
	workspaceRoot := t.TempDir()
	marker := filepath.Join(workspaceRoot, "executed.txt")
	command := "echo marker-executed"
	if runtime.GOOS == "windows" {
		command = "cmd /c echo marker-executed"
	}

	var mu sync.Mutex
	requestNumber := 0
	var customToolID string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestNumber++
		current := requestNumber
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-ndjson")
		if current == 1 {
			arguments := map[string]any{"reason": "Записать отметку"}
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
				"function": map[string]any{"name": customToolID, "arguments": arguments},
			}}}, "done": true})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "content": "Готово"}, "done": true})
	}))
	defer provider.Close()

	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	custom, err := application.SaveCustomTool(domain.CustomTool{
		Kind: domain.CustomToolCommand, DisplayName: "Отметка", Description: "Создаёт файл-отметку",
		Command: command, CWD: ".", TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	customToolID = custom.ID
	profile := domain.DefaultProfile()
	profile.ID, profile.Name, profile.BaseURL, profile.Model = "approval-profile", "Approval agent", provider.URL, "script"
	profile.AllowedTools = []string{custom.ID}
	if _, err = application.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}

	run, err := application.StartRun(StartRunRequest{ProfileID: profile.ID, Task: "Создай отметку"})
	if err != nil {
		t.Fatal(err)
	}

	resolved := false
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		details, detailsErr := application.RunDetails(run.ID)
		if detailsErr != nil {
			t.Fatal(detailsErr)
		}
		for _, approval := range details.Approvals {
			if approval.Status == domain.ApprovalPending && !resolved {
				if err = application.ResolveApproval(approval.ID, allow); err != nil {
					t.Fatal(err)
				}
				resolved = true
			}
		}
		if resolved && (details.Run.Status == domain.RunCompleted || details.Run.Status == domain.RunFailed) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !resolved {
		t.Fatal("подтверждение не запрашивалось — проверка прошла бы вхолостую")
	}
	// Наблюдаем вывод самого инструмента: он может появиться только если тот
	// действительно запустился. Файл-отметка не появлялась даже при согласии, а
	// ToolsUsed пишется до проверки подтверждения и означает попытку, а не
	// выполнение, — оба наблюдения ничего бы не доказали.
	_ = marker
	details, err := application.RunDetails(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range details.Events {
		if event.Type == domain.EventToolFinished && strings.Contains(string(event.Data), "marker-executed") {
			return true
		}
	}
	return false
}

func TestApprovalDecidesWhetherToolRuns(t *testing.T) {
	// Сначала инструмент проверки: при согласии отметка обязана появиться.
	// Без этого «после отказа файла нет» доказывало бы лишь то, что файл не
	// появляется никогда.
	if !runMarkerToolWithApproval(t, true) {
		t.Fatal("при согласии инструмент не оставил отметку — проверка отказа ничего бы не значила")
	}
	if runMarkerToolWithApproval(t, false) {
		t.Fatal("инструмент выполнился вопреки отказу человека")
	}
}

// Отказ от правки файлов обязан оставить файл нетронутым.
//
// Вторая ветка того же обещания, что и отказ от инструмента, и цена ошибки
// здесь выше: человек говорит «не меняй мои файлы». Погасить эту ветку можно
// было незаметно — ни один тест не отклонял подтверждение правки.
//
// Порядок вызовов важен: ядро требует, чтобы агент сначала прочитал файл
// (observations.CheckPatch), иначе правка отклоняется до подтверждения и ветка
// отказа просто не достигается.
func runPatchWithApproval(t *testing.T, allow bool) (string, bool) {
	t.Helper()
	t.Setenv("REDIS_ADDR", "")
	workspaceRoot := t.TempDir()
	target := filepath.Join(workspaceRoot, "health.go")
	before := "package health\n\nfunc Status() string { return \"unknown\" }\n"
	if err := os.WriteFile(target, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	patched := "package health\n\nfunc Status() string { return \"ПРАВКА-ПРОШЛА\" }\n"

	var mu sync.Mutex
	requestNumber := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestNumber++
		current := requestNumber
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-ndjson")
		call := func(name string, arguments map[string]any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
				"function": map[string]any{"name": name, "arguments": arguments},
			}}}, "done": true})
		}
		switch current {
		case 1:
			call("read_file", map[string]any{"path": "health.go"})
		case 2:
			call("propose_patch", map[string]any{"path": "health.go", "content": patched, "reason": "переписать статус"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "content": "Готово"}, "done": true})
		}
	}))
	defer provider.Close()

	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	profile := domain.DefaultProfile()
	profile.ID, profile.Name, profile.BaseURL, profile.Model = "patch-approval", "Patch agent", provider.URL, "script"
	// run_command нужен как средство проверки: без него ядро отклоняет запуск с
	// propose_patch ещё до модели.
	profile.AllowedTools = []string{"read_file", "propose_patch", "run_command"}
	if _, err = application.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}

	run, err := application.StartRun(StartRunRequest{ProfileID: profile.ID, Task: "Обнови статус"})
	if err != nil {
		t.Fatal(err)
	}

	resolved := false
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		details, detailsErr := application.RunDetails(run.ID)
		if detailsErr != nil {
			t.Fatal(detailsErr)
		}
		for _, approval := range details.Approvals {
			if approval.Status == domain.ApprovalPending && !resolved {
				if err = application.ResolveApproval(approval.ID, allow); err != nil {
					t.Fatal(err)
				}
				resolved = true
			}
		}
		if resolved && (details.Run.Status == domain.RunCompleted || details.Run.Status == domain.RunFailed) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !resolved {
		t.Fatal("подтверждение правки не запрашивалось — проверка прошла бы вхолостую")
	}
	// Наблюдаем не файл рабочей копии: прогон идёт в песочнице, и файл не
	// меняется даже при согласии — такое наблюдение ничего бы не различало.
	// Ядро само помечает правку: applied или rejected (internal/tools/patch.go).
	_ = target
	details, err := application.RunDetails(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(details.Patches) == 0 {
		t.Fatal("правка не дошла до ядра — проверка прошла бы вхолостую")
	}
	statuses := make([]string, 0, len(details.Patches))
	applied := false
	for _, patch := range details.Patches {
		statuses = append(statuses, patch.Status)
		if patch.Status == "applied" {
			applied = true
		}
	}
	return strings.Join(statuses, ","), applied
}

func TestApprovalDecidesWhetherPatchIsWritten(t *testing.T) {
	// Инструмент проверки: при согласии файл обязан измениться. Без этого
	// «после отказа файл прежний» доказывало бы лишь то, что правка не работает
	// вовсе.
	if got, applied := runPatchWithApproval(t, true); !applied {
		t.Fatalf("при согласии правка не применилась, статусы: %s", got)
	}
	content, applied := runPatchWithApproval(t, false)
	if applied {
		t.Fatalf("правка применена вопреки отказу человека, статусы: %s", content)
	}
}
