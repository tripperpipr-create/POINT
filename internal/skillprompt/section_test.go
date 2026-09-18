package skillprompt

import (
	"strings"
	"testing"
	"unicode/utf8"

	"local-agent-workbench/internal/domain"
)

func TestEquippedSkillsSectionInlinesShortAndGatesLong(t *testing.T) {
	long := strings.Repeat("Практика. ", 400)
	if utf8.RuneCountInString(long) <= MaxInlinedRunes {
		t.Fatal("fixture is not long enough")
	}
	message := Section([]domain.SkillRuntime{
		{ID: "skill-short", Name: "Short", Instructions: "Do the small thing.", RequiredTools: []string{"read_file"}},
		{ID: "skill-long", Name: "Long", Description: "A large playbook", Instructions: long, Scripts: []string{"scripts/check.sh"}},
	})
	if !strings.Contains(message, "Do the small thing.") || strings.Contains(message, long[:40]) {
		t.Fatalf("short skill should inline, long should not: %s", message)
	}
	if !strings.Contains(message, "Load with read_skill") || !strings.Contains(message, "Scripts: 1") {
		t.Fatalf("long skill should ask for read_skill: %s", message)
	}
}
