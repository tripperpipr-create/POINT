package app

import (
	"encoding/base64"
	"local-agent-workbench/internal/domain"
	"strings"
	"testing"
)

func TestMasterAttachmentModelBudgetAndVision(t *testing.T) {
	cfg := domain.OrchestratorConfig{Model: "custom", MaxOutputTokens: 1024}
	small := domain.ModelReference{ContextWindow: 6144}
	input := []domain.MasterAttachment{{Name: "main.go", Content: strings.Repeat("x", 1025)}}
	if _, _, _, err := masterAttachments(input, cfg, small); err == nil {
		t.Fatal("oversized context accepted")
	}
	input[0].Content = "package main"
	text, _, snapshots, err := masterAttachments(input, cfg, small)
	if err != nil || !strings.Contains(text, input[0].Content) || snapshots[0].SHA256 == "" {
		t.Fatalf("snapshot: %+v %v", snapshots, err)
	}
	png := base64.StdEncoding.EncodeToString(append([]byte{137, 80, 78, 71, 13, 10, 26, 10}, make([]byte, 16)...))
	image := []domain.MasterAttachment{{Name: "diagram.png", Kind: "image", MIME: "image/png", Content: png}}
	if _, _, _, err = masterAttachments(image, cfg, small); err == nil {
		t.Fatal("non-vision model accepted an image")
	}
	small.Capabilities = []string{"vision"}
	if _, images, _, err := masterAttachments(image, cfg, small); err != nil || len(images) != 1 {
		t.Fatalf("custom vision model: %v", err)
	}
	image[0].Content = base64.StdEncoding.EncodeToString([]byte("not an image"))
	if _, _, _, err = masterAttachments(image, cfg, small); err == nil {
		t.Fatal("invalid image MIME accepted")
	}
}
