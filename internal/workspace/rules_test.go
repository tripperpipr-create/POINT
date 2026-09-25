package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectRulesReadsRootRuleFilesWithinBudget(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# Разработка\nОтвечать по-русски."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(strings.Repeat("правило ", MaxProjectRulesBytes)), 0o644); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	rules := fs.ProjectRules()
	if len(rules.Sources) != 2 || rules.Sources[0] != "AGENTS.md" {
		t.Fatalf("источники правил: %v", rules.Sources)
	}
	if !strings.Contains(rules.Text, "Отвечать по-русски.") || !rules.Truncated {
		t.Fatal("правила потеряны или предел не отмечен")
	}
	if len(rules.Text) > MaxProjectRulesBytes {
		t.Fatalf("правила вышли за предел: %d", len(rules.Text))
	}
	if !utf8Valid(rules.Text) {
		t.Fatal("обрезка разрезала символ")
	}
}

func TestProjectRulesWithoutFilesIsEmpty(t *testing.T) {
	fs, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if rules := fs.ProjectRules(); rules.Text != "" || len(rules.Sources) != 0 {
		t.Fatalf("правила из ниоткуда: %#v", rules)
	}
}

func utf8Valid(value string) bool {
	return strings.ToValidUTF8(value, "�") == value
}
