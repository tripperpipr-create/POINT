package workspace

import (
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxImportTargets  = 8
	maxImportsPerFile = 256
)

func buildRelationshipGraph(index *projectIndex) {
	paths := make([]string, 0, len(index.files))
	for path := range index.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	resolver := newImportResolver(paths)
	for _, source := range paths {
		for _, spec := range index.imports[source] {
			for _, target := range resolver.resolve(source, spec) {
				addRelatedFile(index, source, target, "imports", spec)
				relation := "imported_by"
				if isTestFilePath(source) {
					relation = "test"
				}
				addRelatedFile(index, target, source, relation, spec)
			}
		}
	}
	for _, testPath := range paths {
		if !isTestFilePath(testPath) {
			continue
		}
		for _, target := range resolver.filenameTestTargets(testPath) {
			addRelatedFile(index, target, testPath, "test", "filename")
			addRelatedFile(index, testPath, target, "tests", "filename")
		}
	}
	for path := range index.related {
		sort.Slice(index.related[path], func(left, right int) bool {
			first, second := index.related[path][left], index.related[path][right]
			if first.Relation != second.Relation {
				return first.Relation < second.Relation
			}
			if first.Path != second.Path {
				return first.Path < second.Path
			}
			return first.Via < second.Via
		})
	}
	index.relatedKeys = nil
}

func addRelatedFile(index *projectIndex, source, target, relation, via string) {
	if source == target || source == "" || target == "" {
		return
	}
	metadata, exists := index.files[target]
	if !exists || !validIndexSHA256(metadata.SHA256) {
		return
	}
	if index.relatedKeys == nil {
		index.relatedKeys = map[string]map[string]bool{}
	}
	if index.relatedKeys[source] == nil {
		index.relatedKeys[source] = map[string]bool{}
	}
	key := relation + "\x00" + target
	if index.relatedKeys[source][key] {
		return
	}
	index.relatedKeys[source][key] = true
	index.related[source] = append(index.related[source], RelatedFile{Path: target, Relation: relation, Via: via, FileSHA256: metadata.SHA256})
}

func selectRelatedFiles(index *projectIndex, chunks []RelevantChunk, limit int) ([]RelatedFile, bool) {
	if limit <= 0 || len(chunks) == 0 {
		return nil, false
	}
	selectedPaths := make(map[string]bool, len(chunks))
	for _, chunk := range chunks {
		selectedPaths[chunk.Path] = true
	}
	seen := make(map[string]bool)
	result := make([]RelatedFile, 0, min(limit, len(index.related)))
	truncated := false
	for _, chunk := range chunks {
		for _, related := range index.related[chunk.Path] {
			if selectedPaths[related.Path] {
				continue
			}
			key := related.Relation + "\x00" + related.Path
			if seen[key] {
				continue
			}
			seen[key] = true
			if len(result) >= limit {
				truncated = true
				continue
			}
			result = append(result, related)
		}
	}
	return result, truncated
}

func extractImportSpecs(path, content string) []string {
	extension := strings.ToLower(filepath.Ext(path))
	lines := scanLines(content)
	result := make([]string, 0)
	seen := map[string]bool{}
	add := func(spec string) {
		spec = strings.TrimSpace(strings.TrimSuffix(spec, ";"))
		if spec == "" || seen[spec] || len(result) >= maxImportsPerFile {
			return
		}
		seen[spec] = true
		result = append(result, spec)
	}
	switch extension {
	case ".go":
		inBlock := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "import (") || trimmed == "import(" || trimmed == "import (" {
				inBlock = true
				continue
			}
			if inBlock && strings.HasPrefix(trimmed, ")") {
				inBlock = false
				continue
			}
			if inBlock || strings.HasPrefix(trimmed, "import ") {
				for _, value := range quotedValues(trimmed) {
					add(value)
				}
			}
		}
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "export ") || strings.Contains(trimmed, " from ") || strings.Contains(trimmed, "require(") || strings.Contains(trimmed, "import(") {
				values := quotedValues(trimmed)
				if len(values) > 0 {
					add(values[len(values)-1])
				}
			}
		}
	case ".py":
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "from ") {
				fields := strings.Fields(trimmed)
				if len(fields) >= 3 && fields[2] == "import" {
					add(fields[1])
				}
				continue
			}
			if strings.HasPrefix(trimmed, "import ") {
				for _, value := range strings.Split(strings.TrimPrefix(trimmed, "import "), ",") {
					fields := strings.Fields(strings.TrimSpace(value))
					if len(fields) > 0 {
						add(fields[0])
					}
				}
			}
		}
	case ".rs":
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "use ") {
				continue
			}
			value := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "use "), ";"))
			if cut := strings.IndexAny(value, "{*"); cut >= 0 {
				value = strings.TrimSuffix(value[:cut], "::")
			}
			add(strings.ReplaceAll(value, "::", "/"))
		}
	case ".java", ".kt", ".kts", ".cs":
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			prefix := "import "
			if extension == ".cs" {
				prefix = "using "
			}
			if strings.HasPrefix(trimmed, prefix) {
				value := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, prefix), ";"))
				add(strings.ReplaceAll(value, ".", "/"))
			}
		}
	}
	return result
}

func quotedValues(value string) []string {
	result := make([]string, 0, 2)
	for index := 0; index < len(value); index++ {
		quote := value[index]
		if quote != '\'' && quote != '"' && quote != '`' {
			continue
		}
		start := index + 1
		index++
		for index < len(value) {
			if value[index] == '\\' {
				index += 2
				continue
			}
			if value[index] == quote {
				result = append(result, value[start:index])
				break
			}
			index++
		}
	}
	return result
}

func resolveImportTargets(source, spec string, paths []string) []string {
	return newImportResolver(paths).resolve(source, spec)
}

type importResolver struct {
	pathsByExact     map[string][]string
	pathsByNoExt     map[string][]string
	pathsByIndexBase map[string][]string
	pathsByDirectory map[string][]string
	pathsByBase      map[string][]string
}

func newImportResolver(paths []string) *importResolver {
	resolver := &importResolver{
		pathsByExact: map[string][]string{}, pathsByNoExt: map[string][]string{}, pathsByIndexBase: map[string][]string{},
		pathsByDirectory: map[string][]string{}, pathsByBase: map[string][]string{},
	}
	for _, path := range paths {
		path = filepath.ToSlash(path)
		pathWithoutExtension := strings.TrimSuffix(path, filepath.Ext(path))
		directory := filepath.ToSlash(filepath.Dir(path))
		if directory == "." {
			directory = ""
		}
		resolver.pathsByExact[path] = append(resolver.pathsByExact[path], path)
		resolver.pathsByNoExt[pathWithoutExtension] = append(resolver.pathsByNoExt[pathWithoutExtension], path)
		resolver.pathsByIndexBase[strings.TrimSuffix(pathWithoutExtension, "/index")] = append(resolver.pathsByIndexBase[strings.TrimSuffix(pathWithoutExtension, "/index")], path)
		if directory != "" {
			resolver.pathsByDirectory[directory] = append(resolver.pathsByDirectory[directory], path)
		}
		resolver.pathsByBase[filepath.Base(path)] = append(resolver.pathsByBase[filepath.Base(path)], path)
	}
	return resolver
}

func (resolver *importResolver) resolve(source, spec string) []string {
	spec = strings.TrimSpace(strings.Trim(spec, "'\"`"))
	if spec == "" || strings.Contains(spec, "://") || strings.HasPrefix(spec, "node:") {
		return nil
	}
	spec = strings.ReplaceAll(spec, "\\", "/")
	if strings.HasPrefix(spec, "/") || filepath.IsAbs(filepath.FromSlash(spec)) || filepath.VolumeName(filepath.FromSlash(spec)) != "" {
		return nil
	}
	target, relative, safe := normalizedImportTarget(source, spec)
	if !safe || target == "" {
		return nil
	}
	target = strings.TrimSuffix(filepath.ToSlash(filepath.Clean(filepath.FromSlash(target))), "/")
	targetWithoutExtension := stripKnownImportExtension(target)
	scores := make(map[string]int)
	add := func(items []string, score int) {
		for _, path := range items {
			if path == source || (isTestFilePath(path) && !isTestFilePath(source)) {
				continue
			}
			if score > scores[path] {
				scores[path] = score
			}
		}
	}
	add(resolver.pathsByExact[target], 100)
	add(resolver.pathsByNoExt[targetWithoutExtension], 100)
	add(resolver.pathsByIndexBase[targetWithoutExtension], 95)
	add(resolver.pathsByDirectory[targetWithoutExtension], 85)
	if !relative && strings.Contains(targetWithoutExtension, "/") {
		segments := strings.Split(targetWithoutExtension, "/")
		for start := 1; start < len(segments); start++ {
			suffix := strings.Join(segments[start:], "/")
			add(resolver.pathsByNoExt[suffix], 75)
			add(resolver.pathsByDirectory[suffix], 65)
		}
	}
	type candidate struct {
		path  string
		score int
	}
	candidates := make([]candidate, 0, len(scores))
	for path, score := range scores {
		candidates = append(candidates, candidate{path: path, score: score})
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].score != candidates[right].score {
			return candidates[left].score > candidates[right].score
		}
		return candidates[left].path < candidates[right].path
	})
	result := make([]string, 0, min(maxImportTargets, len(candidates)))
	for _, item := range candidates[:min(maxImportTargets, len(candidates))] {
		result = append(result, item.path)
	}
	return result
}

func normalizedImportTarget(source, spec string) (target string, relative bool, safe bool) {
	sourceDir := filepath.ToSlash(filepath.Dir(source))
	if sourceDir == "." {
		sourceDir = ""
	}
	if strings.HasPrefix(spec, "@/") {
		return strings.TrimPrefix(spec, "@/"), true, true
	}
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") {
		target = filepath.ToSlash(filepath.Clean(filepath.Join(filepath.FromSlash(sourceDir), filepath.FromSlash(spec))))
		return target, true, target != ".." && !strings.HasPrefix(target, "../")
	}
	if strings.HasPrefix(spec, ".") && !strings.Contains(spec, "/") {
		leading := len(spec) - len(strings.TrimLeft(spec, "."))
		segments := splitPathSegments(sourceDir)
		parentHops := leading - 1
		if parentHops > len(segments) {
			return "", true, false
		}
		base := filepath.Join(segments[:len(segments)-parentHops]...)
		remainder := strings.TrimLeft(spec, ".")
		target = filepath.ToSlash(filepath.Clean(filepath.Join(base, filepath.FromSlash(strings.ReplaceAll(remainder, ".", "/")))))
		return target, true, target != ".." && !strings.HasPrefix(target, "../")
	}
	target = strings.ReplaceAll(spec, "::", "/")
	target = strings.TrimPrefix(target, "crate/")
	target = strings.TrimPrefix(target, "self/")
	if strings.HasPrefix(target, "super/") {
		target = filepath.ToSlash(filepath.Clean(filepath.Join(filepath.FromSlash(sourceDir), filepath.FromSlash(strings.TrimPrefix(target, "super/")))))
		return target, true, target != ".." && !strings.HasPrefix(target, "../")
	}
	if !strings.Contains(target, "/") && strings.Contains(target, ".") {
		target = strings.ReplaceAll(target, ".", "/")
	}
	return filepath.ToSlash(target), false, true
}

func splitPathSegments(value string) []string {
	parts := strings.Split(filepath.ToSlash(value), "/")
	result := parts[:0]
	for _, part := range parts {
		if part != "" && part != "." {
			result = append(result, part)
		}
	}
	return result
}

func stripKnownImportExtension(value string) string {
	for _, extension := range []string{".tsx", ".jsx", ".mjs", ".cjs", ".ts", ".js", ".py", ".rs", ".go", ".java", ".kt", ".cs"} {
		if strings.HasSuffix(strings.ToLower(value), extension) {
			return value[:len(value)-len(extension)]
		}
	}
	return value
}

func isTestFilePath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := filepath.Base(lower)
	return strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") || strings.Contains(lower, "/__tests__/") ||
		strings.HasPrefix(base, "test_") || strings.Contains(base, "_test.") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.")
}

func filenameTestTargets(testPath string, paths []string) []string {
	return newImportResolver(paths).filenameTestTargets(testPath)
}

func (resolver *importResolver) filenameTestTargets(testPath string) []string {
	base := filepath.Base(testPath)
	extension := filepath.Ext(base)
	stem := strings.TrimSuffix(base, extension)
	targetStem := ""
	switch {
	case strings.HasSuffix(stem, "_test"):
		targetStem = strings.TrimSuffix(stem, "_test")
	case strings.HasSuffix(stem, ".test"):
		targetStem = strings.TrimSuffix(stem, ".test")
	case strings.HasSuffix(stem, ".spec"):
		targetStem = strings.TrimSuffix(stem, ".spec")
	case strings.HasPrefix(stem, "test_"):
		targetStem = strings.TrimPrefix(stem, "test_")
	}
	if targetStem == "" {
		return nil
	}
	targetBase := targetStem + extension
	sameDirectory := filepath.ToSlash(filepath.Join(filepath.Dir(testPath), targetBase))
	for _, path := range resolver.pathsByExact[sameDirectory] {
		if !isTestFilePath(path) {
			return []string{path}
		}
	}
	result := make([]string, 0, 1)
	for _, path := range resolver.pathsByBase[targetBase] {
		if !isTestFilePath(path) {
			result = append(result, path)
		}
	}
	if len(result) == 1 {
		return result
	}
	return nil
}
