package orchestrator

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

type reportModelStub struct{ answer string }

func (model reportModelStub) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: model.answer})
}

func reportFixture() ReportDocument {
	return ReportDocument{
		Title: "Риски релиза", Subtitle: "Решение для команды", ExecutiveSummary: "Сначала исправьте критический риск.",
		Sections: []ReportSection{{Heading: "Приоритеты", Paragraphs: []string{"Факты и следующий шаг."}, Bullets: []string{"Назначить владельца"}, Links: []ReportLink{{Label: "Документация", URL: "https://example.com/docs"}}, Tables: []ReportTable{{Title: "Матрица", Headers: []string{"Риск", "Приоритет"}, Rows: [][]string{{"Регрессия", "Высокий"}}}}}},
		Sources:  []ReportLink{{Label: "Источник", URL: "https://example.com/source"}},
	}
}

func TestRenderReportProducesReadableFormats(t *testing.T) {
	document := reportFixture()
	markdown, _, err := RenderReport(document, "md")
	if err != nil || !strings.Contains(string(markdown), "# Риски релиза") || !strings.Contains(string(markdown), "| Риск | Приоритет |") || !strings.Contains(string(markdown), "[Источник](https://example.com/source)") {
		t.Fatalf("markdown report mismatch: %v\n%s", err, markdown)
	}
	htmlReport, _, err := RenderReport(document, "html")
	if err != nil || !strings.Contains(string(htmlReport), "<!doctype html>") || !strings.Contains(string(htmlReport), "<table>") || !strings.Contains(string(htmlReport), `href="https://example.com/docs"`) {
		t.Fatalf("html report mismatch: %v", err)
	}
	xlsx, mediaType, err := RenderReport(document, "xlsx")
	if err != nil || mediaType != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("xlsx report mismatch: media=%q err=%v", mediaType, err)
	}
	reader, err := zip.NewReader(bytes.NewReader(xlsx), int64(len(xlsx)))
	if err != nil {
		t.Fatalf("xlsx is not a zip archive: %v", err)
	}
	required := map[string]bool{"[Content_Types].xml": false, "xl/workbook.xml": false, "xl/styles.xml": false, "xl/worksheets/sheet1.xml": false}
	for _, file := range reader.File {
		if _, ok := required[file.Name]; !ok {
			continue
		}
		required[file.Name] = true
		stream, openErr := file.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		data, readErr := io.ReadAll(stream)
		_ = stream.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		decoder := xml.NewDecoder(bytes.NewReader(data))
		for {
			if _, parseErr := decoder.Token(); parseErr == io.EOF {
				break
			} else if parseErr != nil {
				t.Fatalf("invalid OOXML in %s: %v", file.Name, parseErr)
			}
		}
	}
	for name, found := range required {
		if !found {
			t.Errorf("xlsx misses %s", name)
		}
	}
}

func TestGenerateReportUsesSeparateReporterOnMasterModel(t *testing.T) {
	answer := `{"title":"Обзор","subtitle":"Для команды","executiveSummary":"Главный вывод.","sections":[{"heading":"Детали","paragraphs":["Проверено."],"bullets":[],"links":[],"tables":[]}],"sources":[]}`
	artifact, err := GenerateReport(context.Background(), domain.OrchestratorConfig{Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", Model: "master-model", MaxOutputTokens: 8192}, ReportRequest{Prompt: "Собери обзор", Format: "md"}, func(providers.Config) (providers.Model, error) { return reportModelStub{answer: answer}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if artifact.AgentID != "reporter" || artifact.Model != "master-model" || artifact.SuggestedName != "обзор.md" || !strings.Contains(string(artifact.Content), "Главный вывод") {
		t.Fatalf("unexpected artifact: %#v", artifact)
	}
}

func TestNormalizeReportRejectsInventedOrUnsafeLinks(t *testing.T) {
	document := reportFixture()
	document.Sources = append(document.Sources, ReportLink{Label: "local", URL: "file:///secret"}, ReportLink{Label: "script", URL: "javascript:alert(1)"})
	if err := normalizeReport(&document); err != nil {
		t.Fatal(err)
	}
	if len(document.Sources) != 1 || document.Sources[0].URL != "https://example.com/source" {
		t.Fatalf("unsafe links survived: %#v", document.Sources)
	}
}
