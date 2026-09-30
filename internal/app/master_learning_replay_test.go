package app

import (
	"encoding/json"
	"testing"

	"local-agent-workbench/internal/masterskills"
	"local-agent-workbench/internal/orchestrator"
)

// Кандидат, сохранивший встроенную методику слово в слово, обязан пройти
// проверку переносимости. Иначе обучение отвергает всякого кандидата,
// который держит контракт — например, проверку http://localhost:ПОРТ/health.
func TestEveryBuiltinMasterSkillPassesItsOwnCandidateGate(t *testing.T) {
	for _, skill := range masterskills.Builtins() {
		raw, _ := json.Marshal(map[string]string{"instructions": skill.Instructions})
		if _, err := masterCandidateInstructions(string(raw)); err != nil {
			t.Errorf("%s: builtin text fails the candidate gate: %v", skill.ID, err)
		}
	}
}

func TestPortabilityStillRejectsHostPathsAndRemoteURLs(t *testing.T) {
	for _, content := range []string{
		`Открой C:\Users\dev\repo\main.go перед правкой.`,
		"Скачай схему с https://internal.example.com/schema.json.",
		"Смотри /home/dev/project/config.yaml.",
	} {
		if safePortableMemory(content) {
			t.Errorf("non-portable content accepted: %q", content)
		}
	}
	if !safePortableMemory("Проверь ответ http://localhost:8080/health и http://127.0.0.1:ПОРТ/ready после запуска.") {
		t.Error("loopback check address was rejected as non-portable")
	}
}

// Реплей приёма задания идёт с инструментами разговора — только ими.
func TestIntakeReplayKeepsOnlyConversationTools(t *testing.T) {
	skill := masterskills.Builtins()[0]
	for _, candidate := range masterskills.Builtins() {
		if candidate.ID == "master-intake" {
			skill = candidate
		}
	}
	request := replaceMasterSkill(orchestrator.MasterSkillFixtures("intake")[1], skill.ID, skill)
	if len(request.Tools) == 0 {
		t.Fatal("intake replay has no conversation tools: propose_brief cannot be checked")
	}
	for _, tool := range request.Tools {
		if !orchestrator.IsMasterActionTool(tool.Name) {
			t.Fatalf("replay exposes a project tool %q", tool.Name)
		}
	}
}
