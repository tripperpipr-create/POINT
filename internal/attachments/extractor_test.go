package attachments

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/workspace"
)

func TestResolveStructuredDocumentsAndImage(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "data.json"), []byte(`{"name":"workbench","enabled":true}`))
	mustWrite(t, filepath.Join(root, "table.csv"), []byte("name,value\nalpha,42\n"))
	writeZip(t, filepath.Join(root, "note.docx"), map[string]string{
		"word/document.xml": `<w:document xmlns:w="urn:w"><w:body><w:p><w:r><w:t>Первая строка</w:t></w:r></w:p><w:p><w:r><w:t>Вторая строка</w:t></w:r></w:p></w:body></w:document>`,
	})
	writeZip(t, filepath.Join(root, "book.xlsx"), map[string]string{
		"xl/sharedStrings.xml":     `<sst><si><t>Название</t></si><si><t>Значение</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row><row r="2"><c r="A2" t="inlineStr"><is><t>alpha</t></is></c><c r="B2"><v>42</v></c></row></sheetData></worksheet>`,
	})
	var imageData bytes.Buffer
	bitmap := image.NewRGBA(image.Rect(0, 0, 2, 3))
	bitmap.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&imageData, bitmap); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "diagram.png"), imageData.Bytes())

	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := Resolve(fs, []domain.RunContextInput{
		{Kind: domain.ContextWorkspaceFile, Path: "data.json"},
		{Kind: domain.ContextWorkspaceFile, Path: "table.csv"},
		{Kind: domain.ContextWorkspaceFile, Path: "note.docx"},
		{Kind: domain.ContextWorkspaceFile, Path: "book.xlsx"},
		{Kind: domain.ContextWorkspaceFile, Path: "diagram.png"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 5 || preview.EstimatedTokens <= 0 || preview.TotalImageBytes <= 0 {
		t.Fatalf("preview=%#v", preview)
	}
	if preview.Items[0].Kind != domain.ContextDocument || !strings.Contains(preview.Items[0].Content, `"name": "workbench"`) {
		t.Fatalf("JSON snapshot=%#v", preview.Items[0])
	}
	if preview.Items[1].Kind != domain.ContextTable || !strings.Contains(preview.Items[1].Content, "alpha\t42") {
		t.Fatalf("CSV snapshot=%#v", preview.Items[1])
	}
	if !strings.Contains(preview.Items[2].Content, "Первая строка\nВторая строка") {
		t.Fatalf("DOCX snapshot=%#v", preview.Items[2])
	}
	if preview.Items[3].Kind != domain.ContextTable || !strings.Contains(preview.Items[3].Content, "Название\tЗначение") || !strings.Contains(preview.Items[3].Content, "alpha\t42") {
		t.Fatalf("XLSX snapshot=%#v", preview.Items[3])
	}
	imageItem := preview.Items[4]
	if imageItem.Kind != domain.ContextImage || imageItem.MediaType != "image/png" || imageItem.Width != 2 || imageItem.Height != 3 || imageItem.DataBase64 == "" || !strings.HasPrefix(imageItem.Digest, "sha256:") {
		t.Fatalf("image snapshot=%#v", imageItem)
	}
}

func TestResolveExtractsTextPDF(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "brief.pdf"), minimalPDF("Hello PDF context"))
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := Resolve(fs, []domain.RunContextInput{{Kind: domain.ContextWorkspaceFile, Path: "brief.pdf"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 1 || preview.Items[0].Kind != domain.ContextDocument || preview.Items[0].Format != "pdf" || !strings.Contains(preview.Items[0].Content, "Hello PDF context") {
		t.Fatalf("PDF snapshot=%#v", preview.Items)
	}
}

func TestResolveRejectsSensitiveUnsupportedAndOversizedFiles(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".env"), []byte("TOKEN=secret"))
	mustWrite(t, filepath.Join(root, "binary.bin"), []byte{0, 1, 2, 3})
	large := filepath.Join(root, "large.pdf")
	file, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(maxFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path string
		want string
	}{
		{path: ".env", want: "sensitive"},
		{path: "binary.bin", want: "unsupported"},
		{path: "large.pdf", want: "16 MiB"},
	} {
		_, resolveErr := Resolve(fs, []domain.RunContextInput{{Kind: domain.ContextWorkspaceFile, Path: test.path}})
		if resolveErr == nil || !strings.Contains(resolveErr.Error(), test.want) {
			t.Fatalf("path=%s error=%v, want %q", test.path, resolveErr, test.want)
		}
	}
}

func TestResolveTruncatesExtractedTablesAndReportsWarning(t *testing.T) {
	root := t.TempDir()
	data := bytes.Repeat([]byte("value,value,value\n"), 20_000)
	mustWrite(t, filepath.Join(root, "large.csv"), data)
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := Resolve(fs, []domain.RunContextInput{{Kind: domain.ContextWorkspaceFile, Path: "large.csv"}})
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Items[0].Truncated || len(preview.Items[0].Content) > maxTextBytes || len(preview.Warnings) == 0 {
		t.Fatalf("preview=%#v", preview)
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func writeZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, content := range entries {
		entry, createErr := archive.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := entry.Write([]byte(content)); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
}

func minimalPDF(text string) []byte {
	objects := []string{
		`<< /Type /Catalog /Pages 2 0 R >>`,
		`<< /Type /Pages /Kids [3 0 R] /Count 1 >>`,
		`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>`,
		fmt.Sprintf("<< /Length %d >>\nstream\nBT /F1 12 Tf 72 720 Td (%s) Tj ET\nendstream", len("BT /F1 12 Tf 72 720 Td () Tj ET")+len(text), text),
		`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`,
	}
	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = output.Len()
		fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&output, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return output.Bytes()
}
