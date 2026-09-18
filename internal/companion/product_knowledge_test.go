package companion

import (
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

// Блок знаний о продукте — единственное, из чего модель отвечает на вопрос «что
// ты умеешь». Он живёт отдельно от кода возможностей и поэтому расходится с ним
// молча: возможность добавили или сняли, а обещание осталось прежним. Здесь оба
// направления и проверяются.
func TestProductKnowledgeMatchesCompanionMemory(t *testing.T) {
	remembers := companionMemoryIntent("Запомни: релиз через make release") == "remember"
	lists := companionMemoryIntent("что ты помнишь") == "list"
	forgets := companionMemoryIntent("забудь про релиз") == "forget"
	promised := strings.Contains(pointIDECapabilities, "запомни") &&
		strings.Contains(pointIDECapabilities, "что ты помнишь") &&
		strings.Contains(pointIDECapabilities, "забудь")
	if remembers && lists && forgets && !promised {
		t.Fatal("помощник умеет помнить, но знание о продукте об этом молчит — на вопрос «умеешь ли» он ответит догадкой")
	}
	if promised && !(remembers && lists && forgets) {
		t.Fatal("знание о продукте обещает память, которой уже нет — обещание надо снять вместе с возможностью")
	}
}

// То же для diff по коммиту: инструмент объявляет аргумент, знание о продукте
// обязано называть возможность, иначе модель не предложит её человеку.
func TestProductKnowledgeMatchesGitDiffCommit(t *testing.T) {
	fs, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	definition := tools.GitDiff{FS: fs}.Definition()
	hasCommitArgument := strings.Contains(string(definition.InputSchema), `"commit"`)
	promised := strings.Contains(pointIDECapabilities, "named commit")
	if hasCommitArgument != promised {
		t.Fatalf("git_diff и знание о продукте разошлись: аргумент=%t, обещание=%t", hasCommitArgument, promised)
	}
	if definition.Name != "git_diff" {
		t.Fatalf("проверялся не тот инструмент: %q", definition.Name)
	}
	var _ domain.ToolDefinition = definition
}
