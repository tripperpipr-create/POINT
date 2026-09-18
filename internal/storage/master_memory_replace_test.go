package storage

import (
	"context"
	"local-agent-workbench/internal/domain"
	"path/filepath"
	"testing"
)

func TestMasterMemoryReplacementIsScopedAndAtomic(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for _, w := range []string{"project-a", "project-b"} {
		for _, entry := range []domain.MasterMemoryEntry{{ID: "old", Content: "Old preference", Status: "accepted"}, {ID: "new", Content: "New preference", Status: "proposed", SourceID: "turn-2"}} {
			if err = s.SaveMasterMemory(ctx, w, entry); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = s.ReplaceMasterMemory(ctx, "project-a", "new", "missing"); err == nil {
		t.Fatal("missing target accepted")
	}
	entries, _ := s.MasterMemory(ctx, "project-a")
	if len(entries) != 2 {
		t.Fatal("failed replacement lost proposal")
	}
	if err = s.ReplaceMasterMemory(ctx, "project-a", "new", "old"); err != nil {
		t.Fatal(err)
	}
	entries, _ = s.MasterMemory(ctx, "project-a")
	if len(entries) != 1 || entries[0].Content != "New preference" || entries[0].SourceID != "turn-2" {
		t.Fatalf("replacement: %+v", entries)
	}
	other, _ := s.MasterMemory(ctx, "project-b")
	if len(other) != 2 {
		t.Fatal("modified another project")
	}
}
