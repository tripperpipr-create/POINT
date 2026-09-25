package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

// Правила репозитория доезжают и до исполнителя: Мастер ставит задачу по
// AGENTS.md, и агент, который её делает, обязан знать те же договорённости.
// Секрет из правил в контекст не попадает.
func TestRunContextCarriesProjectRules(t *testing.T) {
	application := newTestApp(t)
	world := openTestWorld(t, application)
	rules := "# Правила\nОтвечать по-русски.\nOPENAI_API_KEY=sk-abcdefghijklmnopqrstuvwxyz0123456789"
	if err := os.WriteFile(filepath.Join(world.Path, "AGENTS.md"), []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := domain.AgentProfile{}
	var inputs []domain.RunContextInput
	if err := application.enrichProjectAgentForRun(world.ID, domain.ProjectAgent{ID: "agent"}, &profile, &inputs); err != nil {
		t.Fatal(err)
	}
	var found *domain.RunContextInput
	for index := range inputs {
		if inputs[index].Category == "rules" {
			found = &inputs[index]
		}
	}
	if found == nil || !strings.Contains(found.Content, "Отвечать по-русски.") || found.Source != "AGENTS.md" || !found.Pinned {
		t.Fatalf("правила не доехали до исполнителя: %#v", inputs)
	}
	if strings.Contains(found.Content, "sk-abcdefghijklmnopqrstuvwxyz0123456789") {
		t.Fatal("секрет из правил попал в контекст")
	}
}
