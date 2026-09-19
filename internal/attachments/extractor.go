package attachments

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/textutil"
	"local-agent-workbench/internal/workspace"
)

const (
	maxItems            = 16
	maxTextBytes        = 256 * 1024
	maxContextBytes     = 1024 * 1024
	maxSourceBytes      = 32 * 1024 * 1024
	maxFileBytes        = 16 * 1024 * 1024
	maxImageBytes       = 8 * 1024 * 1024
	maxTotalImageBytes  = 16 * 1024 * 1024
	maxZipEntryBytes    = 16 * 1024 * 1024
	maxZipExpandedBytes = 32 * 1024 * 1024
)

var (
	ErrUnsupported = errors.New("unsupported attachment format")
	ErrTooLarge    = errors.New("attachment exceeds its size limit")
)

// Resolve validates user-controlled references and returns an immutable,
// bounded snapshot. The caller should persist the returned items with the run.
func Resolve(fs *workspace.FS, inputs []domain.RunContextInput) (domain.ContextPreview, error) {
	preview := domain.ContextPreview{Items: []domain.RunContextItem{}, Warnings: []string{}}
	if len(inputs) > maxItems {
		return preview, fmt.Errorf("run context contains more than %d items", maxItems)
	}
	for index, input := range inputs {
		item, sourceBytes, warning, err := resolveOne(fs, input, index)
		if err != nil {
			return preview, err
		}
		preview.TotalSourceBytes += sourceBytes
		preview.TotalContextBytes += item.ExtractedSize
		if item.Kind == domain.ContextImage {
			preview.TotalImageBytes += item.SourceSize
		}
		if preview.TotalSourceBytes > maxSourceBytes {
			return preview, errors.New("run context source files exceed 32 MiB")
		}
		if preview.TotalContextBytes > maxContextBytes {
			return preview, errors.New("extracted run context exceeds 1 MiB")
		}
		if preview.TotalImageBytes > maxTotalImageBytes {
			return preview, errors.New("run context images exceed 16 MiB")
		}
		if warning != "" {
			preview.Warnings = append(preview.Warnings, warning)
		}
		preview.Items = append(preview.Items, item)
	}
	preview.EstimatedTokens = int((preview.TotalContextBytes + 3) / 4)
	if preview.TotalImageBytes > 0 {
		preview.Warnings = append(preview.Warnings, "Стоимость обработки изображений зависит от модели и не входит в текстовую оценку токенов.")
		preview.Warnings = append(preview.Warnings, "Выбранная модель должна поддерживать изображения; текстовые модели могут отклонить запуск.")
	}
	return preview, nil
}

// ValidateSnapshotLimits applies the same aggregate limits to an already
// resolved immutable snapshot. It is used when context is extended while a run
// is active, so existing and queued items cannot bypass the launch-time caps.
func ValidateSnapshotLimits(items []domain.RunContextItem) error {
	if len(items) > maxItems {
		return fmt.Errorf("run context contains more than %d items", maxItems)
	}
	var sourceBytes, contextBytes, imageBytes int64
	for _, item := range items {
		sourceSize := item.SourceSize
		if sourceSize == 0 {
			sourceSize = item.Size
		}
		extractedSize := item.ExtractedSize
		if extractedSize == 0 {
			extractedSize = int64(len(item.Content))
		}
		sourceBytes += sourceSize
		contextBytes += extractedSize
		if item.Kind == domain.ContextImage {
			imageBytes += sourceSize
		}
	}
	if sourceBytes > maxSourceBytes {
		return errors.New("run context source files exceed 32 MiB")
	}
	if contextBytes > maxContextBytes {
		return errors.New("extracted run context exceeds 1 MiB")
	}
	if imageBytes > maxTotalImageBytes {
		return errors.New("run context images exceed 16 MiB")
	}
	return nil
}

func resolveOne(fs *workspace.FS, input domain.RunContextInput, index int) (domain.RunContextItem, int64, string, error) {
	label := strings.TrimSpace(input.Label)
	if len([]rune(label)) > 200 {
		return domain.RunContextItem{}, 0, "", fmt.Errorf("context item %d label exceeds 200 characters", index+1)
	}
	item := domain.RunContextItem{
		ID: domain.NewID("context"), Kind: input.Kind, Label: label,
		Category: input.Category, AddedBy: input.AddedBy, Reason: input.Reason,
		Source: input.Source, Relevance: input.Relevance, Pinned: input.Pinned,
	}
	if input.Kind == domain.ContextText {
		if strings.TrimSpace(input.Content) == "" {
			return item, 0, "", fmt.Errorf("context item %d is empty", index+1)
		}
		if !utf8.ValidString(input.Content) || strings.IndexByte(input.Content, 0) >= 0 {
			return item, 0, "", fmt.Errorf("context item %d is not valid UTF-8 text", index+1)
		}
		if len(input.Content) > maxTextBytes {
			return item, 0, "", fmt.Errorf("context item %d exceeds 256 KiB", index+1)
		}
		if item.Label == "" {
			item.Label = fmt.Sprintf("Текст %d", index+1)
		}
		item.Format, item.MediaType = "text", "text/plain; charset=utf-8"
		item.Content = security.Redact(input.Content)
		item.SourceSize, item.ExtractedSize, item.Size = int64(len(input.Content)), int64(len(item.Content)), int64(len(item.Content))
		item.Digest = digest([]byte(input.Content))
		return item, item.SourceSize, "", nil
	}
	if input.Kind != domain.ContextWorkspaceFile {
		return item, 0, "", fmt.Errorf("context item %d has unsupported kind %q", index+1, input.Kind)
	}
	path := filepath.ToSlash(strings.TrimSpace(input.Path))
	if path == "" {
		return item, 0, "", fmt.Errorf("context item %d file path is empty", index+1)
	}
	if workspace.IsSensitive(path) {
		return item, 0, "", fmt.Errorf("read context file %q: %w", path, workspace.ErrSensitive)
	}
	abs, err := fs.Resolve(path, false)
	if err != nil {
		return item, 0, "", fmt.Errorf("read context file %q: %w", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return item, 0, "", fmt.Errorf("read context file %q: %w", path, err)
	}
	if info.IsDir() {
		return item, 0, "", fmt.Errorf("read context file %q: cannot attach a directory", path)
	}
	if info.Size() > maxFileBytes {
		return item, 0, "", fmt.Errorf("read context file %q: %w (16 MiB)", path, ErrTooLarge)
	}
	item.Path, item.SourceSize = path, info.Size()
	if item.Label == "" {
		item.Label = path
	}
	ext := strings.ToLower(filepath.Ext(path))
	var content string
	var raw []byte
	var truncated bool
	var warning string
	switch ext {
	case ".pdf":
		item.Kind, item.Format, item.MediaType = domain.ContextDocument, "pdf", "application/pdf"
		content, truncated, err = extractPDF(abs)
	case ".docx":
		item.Kind, item.Format, item.MediaType = domain.ContextDocument, "docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		content, truncated, err = extractDOCX(abs)
	case ".xlsx":
		item.Kind, item.Format, item.MediaType = domain.ContextTable, "xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		content, truncated, err = extractXLSX(abs)
	case ".csv", ".tsv":
		item.Kind, item.Format, item.MediaType = domain.ContextTable, strings.TrimPrefix(ext, "."), "text/csv; charset=utf-8"
		raw, err = readFileBounded(abs, maxFileBytes)
		if err == nil {
			content, truncated, err = extractCSV(raw, ext == ".tsv")
		}
	case ".json", ".jsonl", ".ndjson":
		item.Kind, item.Format, item.MediaType = domain.ContextDocument, strings.TrimPrefix(ext, "."), "application/json"
		raw, err = readFileBounded(abs, maxFileBytes)
		if err == nil {
			content, truncated, err = extractJSON(raw, ext != ".json")
		}
	default:
		raw, err = readFileBounded(abs, maxFileBytes)
		if err == nil {
			mediaType := http.DetectContentType(raw)
			if isImageMediaType(mediaType) {
				if len(raw) > maxImageBytes {
					return item, 0, "", fmt.Errorf("read context file %q: image %w (8 MiB)", path, ErrTooLarge)
				}
				item.Kind, item.Format, item.MediaType = domain.ContextImage, strings.TrimPrefix(ext, "."), mediaType
				item.DataBase64 = base64.StdEncoding.EncodeToString(raw)
				item.Digest = digest(raw)
				item.Size = int64(len(raw))
				if config, _, configErr := image.DecodeConfig(bytes.NewReader(raw)); configErr == nil {
					item.Width, item.Height = config.Width, config.Height
				}
				return item, info.Size(), "", nil
			}
			content, truncated, err = extractText(raw)
			item.Kind, item.Format, item.MediaType = domain.ContextDocument, textFormat(ext), "text/plain; charset=utf-8"
		}
	}
	if err != nil {
		return item, 0, "", fmt.Errorf("read context file %q: %w", path, err)
	}
	content, wasTruncated := truncateUTF8(content, maxTextBytes)
	item.Truncated = truncated || wasTruncated
	item.Content = security.Redact(content)
	item.ExtractedSize, item.Size = int64(len(item.Content)), int64(len(item.Content))
	if raw == nil {
		raw, _ = readFileBounded(abs, maxFileBytes)
	}
	item.Digest = digest(raw)
	if item.Truncated {
		warning = fmt.Sprintf("%s: извлечённый текст сокращён до 256 КиБ.", item.Label)
	}
	if strings.TrimSpace(item.Content) == "" {
		warning = fmt.Sprintf("%s: извлекаемого текста не найдено; для сканированного PDF потребуется OCR.", item.Label)
	}
	return item, info.Size(), warning, nil
}

func readFileBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	return data, nil
}

func extractText(data []byte) (string, bool, error) {
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return "", false, ErrUnsupported
	}
	text, truncated := truncateUTF8(string(data), maxTextBytes)
	return text, truncated, nil
}

func extractJSON(data []byte, lines bool) (string, bool, error) {
	if !utf8.Valid(data) {
		return "", false, errors.New("JSON is not valid UTF-8")
	}
	if lines {
		for lineNumber, line := range bytes.Split(data, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) > 0 && !json.Valid(line) {
				return "", false, fmt.Errorf("invalid JSON on line %d", lineNumber+1)
			}
		}
		text, truncated := truncateUTF8(string(data), maxTextBytes)
		return text, truncated, nil
	}
	if !json.Valid(data) {
		return "", false, errors.New("invalid JSON")
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, data, "", "  "); err != nil {
		return "", false, err
	}
	text, truncated := truncateUTF8(pretty.String(), maxTextBytes)
	return text, truncated, nil
}

func extractCSV(data []byte, tabSeparated bool) (string, bool, error) {
	if !utf8.Valid(data) {
		return "", false, errors.New("table is not valid UTF-8")
	}
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = true
	if tabSeparated {
		reader.Comma = '\t'
	}
	var output strings.Builder
	truncated := false
	for row := 0; ; row++ {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", false, fmt.Errorf("parse table row %d: %w", row+1, err)
		}
		if row >= 1000 {
			truncated = true
			break
		}
		if len(record) > 100 {
			record, truncated = record[:100], true
		}
		for column, value := range record {
			if column > 0 {
				output.WriteByte('\t')
			}
			output.WriteString(strings.ReplaceAll(value, "\t", " "))
		}
		output.WriteByte('\n')
		if output.Len() > maxTextBytes {
			truncated = true
			break
		}
	}
	text, sizeTruncated := truncateUTF8(output.String(), maxTextBytes)
	return text, truncated || sizeTruncated, nil
}

func extractPDF(path string) (content string, truncated bool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("PDF parser rejected the document: %v", recovered)
		}
	}()
	file, reader, err := pdf.Open(path)
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	plain, err := reader.GetPlainText()
	if err != nil {
		return "", false, err
	}
	data, err := io.ReadAll(io.LimitReader(plain, maxTextBytes+1))
	if err != nil {
		return "", false, err
	}
	if !utf8.Valid(data) {
		data = bytes.ToValidUTF8(data, []byte("�"))
	}
	content, truncated = truncateUTF8(string(data), maxTextBytes)
	return content, truncated, nil
}

func extractDOCX(path string) (string, bool, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return "", false, err
	}
	defer reader.Close()
	data, err := readZipEntry(reader.File, "word/document.xml", maxZipEntryBytes)
	if err != nil {
		return "", false, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var output strings.Builder
	inText := false
	for {
		token, tokenErr := decoder.Token()
		if errors.Is(tokenErr, io.EOF) {
			break
		}
		if tokenErr != nil {
			return "", false, tokenErr
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "t":
				inText = true
			case "tab":
				output.WriteByte('\t')
			case "br", "cr":
				output.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				output.Write(value)
			}
		case xml.EndElement:
			if value.Name.Local == "t" {
				inText = false
			} else if value.Name.Local == "p" {
				output.WriteByte('\n')
			}
		}
		if output.Len() > maxTextBytes {
			text, _ := truncateUTF8(output.String(), maxTextBytes)
			return text, true, nil
		}
	}
	return output.String(), false, nil
}

func extractXLSX(path string) (string, bool, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return "", false, err
	}
	defer reader.Close()
	shared := []string{}
	if data, sharedErr := readZipEntryOptional(reader.File, "xl/sharedStrings.xml", 8*1024*1024); sharedErr != nil {
		return "", false, sharedErr
	} else if data != nil {
		shared, err = parseSharedStrings(data)
		if err != nil {
			return "", false, err
		}
	}
	var sheets []*zip.File
	for _, entry := range reader.File {
		name := filepath.ToSlash(entry.Name)
		if strings.HasPrefix(name, "xl/worksheets/sheet") && strings.HasSuffix(name, ".xml") {
			sheets = append(sheets, entry)
		}
	}
	sort.Slice(sheets, func(i, j int) bool { return sheets[i].Name < sheets[j].Name })
	if len(sheets) == 0 {
		return "", false, errors.New("XLSX contains no worksheets")
	}
	var output strings.Builder
	expanded := int64(0)
	truncated := false
	for index, sheet := range sheets {
		if index >= 32 {
			truncated = true
			break
		}
		if sheet.UncompressedSize64 > maxZipEntryBytes || expanded+int64(sheet.UncompressedSize64) > maxZipExpandedBytes {
			return "", false, ErrTooLarge
		}
		data, readErr := readSingleZipEntry(sheet, maxZipEntryBytes)
		if readErr != nil {
			return "", false, readErr
		}
		expanded += int64(len(data))
		output.WriteString("# Лист ")
		output.WriteString(strings.TrimSuffix(filepath.Base(sheet.Name), ".xml"))
		output.WriteByte('\n')
		text, sheetTruncated, parseErr := parseWorksheet(data, shared, maxTextBytes-output.Len())
		if parseErr != nil {
			return "", false, parseErr
		}
		output.WriteString(text)
		truncated = truncated || sheetTruncated
		if output.Len() >= maxTextBytes {
			truncated = true
			break
		}
	}
	text, sizeTruncated := truncateUTF8(output.String(), maxTextBytes)
	return text, truncated || sizeTruncated, nil
}

func parseSharedStrings(data []byte) ([]string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	values := []string{}
	var current strings.Builder
	inItem, inText := false, false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return values, nil
		}
		if err != nil {
			return nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			if value.Name.Local == "si" {
				inItem = true
				current.Reset()
			} else if inItem && value.Name.Local == "t" {
				inText = true
			}
		case xml.CharData:
			if inText {
				current.Write(value)
			}
		case xml.EndElement:
			if value.Name.Local == "t" {
				inText = false
			} else if value.Name.Local == "si" {
				values = append(values, current.String())
				inItem = false
			}
		}
		if len(values) > 1_000_000 {
			return nil, ErrTooLarge
		}
	}
}

func parseWorksheet(data []byte, shared []string, budget int) (string, bool, error) {
	if budget <= 0 {
		return "", true, nil
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var output strings.Builder
	var cell strings.Builder
	cellType := ""
	inValue, inInlineText := false, false
	column, lastColumn, rows := 0, -1, 0
	truncated := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", false, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "row":
				lastColumn = -1
			case "c":
				cell.Reset()
				cellType, column = "", lastColumn+1
				for _, attribute := range value.Attr {
					if attribute.Name.Local == "t" {
						cellType = attribute.Value
					} else if attribute.Name.Local == "r" {
						column = columnIndex(attribute.Value)
					}
				}
			case "v":
				inValue = true
			case "t":
				if cellType == "inlineStr" {
					inInlineText = true
				}
			}
		case xml.CharData:
			if inValue || inInlineText {
				cell.Write(value)
			}
		case xml.EndElement:
			switch value.Name.Local {
			case "v":
				inValue = false
			case "t":
				inInlineText = false
			case "c":
				if column >= 100 {
					truncated = true
					continue
				}
				for lastColumn+1 < column {
					if lastColumn >= 0 {
						output.WriteByte('\t')
					}
					lastColumn++
				}
				if lastColumn >= 0 {
					output.WriteByte('\t')
				}
				value := cell.String()
				if cellType == "s" {
					if sharedIndex, parseErr := strconv.Atoi(strings.TrimSpace(value)); parseErr == nil && sharedIndex >= 0 && sharedIndex < len(shared) {
						value = shared[sharedIndex]
					}
				}
				output.WriteString(strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(value))
				lastColumn = column
			case "row":
				output.WriteByte('\n')
				rows++
				if rows >= 1000 {
					truncated = true
					return output.String(), truncated, nil
				}
			}
		}
		if output.Len() > budget {
			text, _ := truncateUTF8(output.String(), budget)
			return text, true, nil
		}
	}
	return output.String(), truncated, nil
}

func columnIndex(reference string) int {
	result := 0
	for _, char := range reference {
		if char < 'A' || char > 'Z' {
			break
		}
		result = result*26 + int(char-'A'+1)
	}
	if result == 0 {
		return 0
	}
	return result - 1
}

func readZipEntry(entries []*zip.File, name string, limit int64) ([]byte, error) {
	data, err := readZipEntryOptional(entries, name, limit)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, fmt.Errorf("archive entry %s is missing", name)
	}
	return data, nil
}

func readZipEntryOptional(entries []*zip.File, name string, limit int64) ([]byte, error) {
	for _, entry := range entries {
		if filepath.ToSlash(entry.Name) == name {
			return readSingleZipEntry(entry, limit)
		}
	}
	return nil, nil
}

func readSingleZipEntry(entry *zip.File, limit int64) ([]byte, error) {
	if entry.UncompressedSize64 > uint64(limit) {
		return nil, ErrTooLarge
	}
	stream, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(stream, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	return data, nil
}

func isImageMediaType(mediaType string) bool {
	switch strings.TrimSpace(strings.Split(mediaType, ";")[0]) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func textFormat(ext string) string {
	ext = strings.TrimPrefix(ext, ".")
	if ext == "" {
		return "text"
	}
	return ext
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// truncateUTF8 — тонкая обёртка над textutil.BoundedBytes: здесь нужен ещё
// признак «обрезали» — девять вызывающих кладут его в метаданные
// вложения. Само правило резки по границе руны живёт в одном месте:
// три собственные реализации уже расходились в обработке limit <= 0.
func truncateUTF8(value string, limit int) (string, bool) {
	bounded := textutil.BoundedBytes(value, limit)
	return bounded, len(bounded) < len(value)
}
