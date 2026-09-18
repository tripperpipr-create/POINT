package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"net/http"
	"strings"
)

func (a *App) masterAttachmentReference(ctx context.Context, cfg domain.OrchestratorConfig) domain.ModelReference {
	ref, _ := domain.LookupModel(cfg.Model)
	connections, err := a.store.ListConnections(ctx)
	if err == nil {
		for _, connection := range connections {
			if connection.ID != cfg.ConnectionID {
				continue
			}
			for _, model := range connection.Models {
				if model.ID != cfg.Model {
					continue
				}
				if model.ContextWindow > 0 {
					ref.ContextWindow = model.ContextWindow
				}
				if len(model.Capabilities) > 0 {
					ref.Capabilities = model.Capabilities
				}
			}
		}
	}
	return ref
}

func masterAttachmentBudget(cfg domain.OrchestratorConfig, ref domain.ModelReference) int {
	window := 131072
	if ref.ContextWindow > 0 {
		window = ref.ContextWindow
	}
	output := cfg.MaxOutputTokens
	if output <= 0 {
		output = 8192
	}
	// Запас под системный промпт и историю; на узком окне — как раньше (4K).
	reserve := 4096
	if window >= 32*1024 {
		reserve = 8192
	}
	return max(0, min(48000, window-output-reserve))
}

func masterAttachments(input []domain.MasterAttachment, cfg domain.OrchestratorConfig, references ...domain.ModelReference) (string, []providers.ImageContent, []domain.MasterAttachment, error) {
	ref, _ := domain.LookupModel(cfg.Model)
	if len(references) > 0 {
		ref = references[0]
	}
	budget := masterAttachmentBudget(cfg, ref)
	if len(input) > 16 {
		return "", nil, nil, fmt.Errorf("не более 16 вложений в одном сообщении")
	}
	var text strings.Builder
	images := []providers.ImageContent{}
	out := make([]domain.MasterAttachment, 0, len(input))
	total := 0
	for _, v := range input {
		if v.StartLine < 1 {
			v.StartLine = 1
		}
		if v.Kind != "image" {
			v.EndLine = v.StartLine + strings.Count(strings.TrimSuffix(v.Content, "\n"), "\n")
		}
		if len(v.Name) > 500 {
			return "", nil, nil, fmt.Errorf("слишком длинное имя вложения")
		}
		if v.ID == "" {
			v.ID = domain.NewID("attachment")
		}
		v.SHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(v.Content)))
		if v.Kind == "image" {
			if v.MIME != "image/png" && v.MIME != "image/jpeg" && v.MIME != "image/webp" {
				return "", nil, nil, fmt.Errorf("изображения: PNG, JPEG или WebP")
			}
			data, err := base64.StdEncoding.DecodeString(v.Content)
			if err != nil || len(data) > 5*1024*1024 || http.DetectContentType(data) != v.MIME {
				return "", nil, nil, fmt.Errorf("изображение повреждено или больше 5 МБ")
			}
			images = append(images, providers.ImageContent{MediaType: v.MIME, DataBase64: v.Content})
			if len(images) > 4 {
				return "", nil, nil, fmt.Errorf("не более четырёх изображений")
			}
		} else {
			total += len([]rune(v.Content))
			if total > budget {
				return "", nil, nil, fmt.Errorf("контекст превышает бюджет модели %s: доступно %d символов вложений; сократите выбор или выберите фрагменты", cfg.Model, max(0, budget))
			}
			fmt.Fprintf(&text, "\n--- %s (строка %d) ---\n%s\n", v.Name, v.StartLine, v.Content)
		}
		out = append(out, v)
	}
	if len(images) > 0 {
		capable := false
		for _, capability := range ref.Capabilities {
			capable = capable || capability == "vision"
		}
		if !capable {
			return "", nil, nil, fmt.Errorf("для изображений выберите модель с поддержкой зрения; текущая модель: %s", cfg.Model)
		}
	}
	return text.String(), images, out, nil
}
