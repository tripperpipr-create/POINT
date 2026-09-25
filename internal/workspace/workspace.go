package workspace

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"local-agent-workbench/internal/domain"
)

var (
	ErrOutsideWorkspace = errors.New("path is outside the workspace")
	ErrExcluded         = errors.New("path is excluded")
	ErrSensitive        = errors.New("sensitive file requires explicit permission")
	ErrBinary           = errors.New("binary files are not readable")
)

var excludedDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true,
	"node_modules": true, "vendor": true, ".point": true,
}

// knownTextExts skips an 8KiB open/read during drift walks and directory listings.
// Unknown extensions still fall through to isText content sniffing.
var knownTextExts = map[string]struct{}{
	".go": {}, ".mod": {}, ".sum": {}, ".rs": {}, ".py": {}, ".pyi": {}, ".rb": {}, ".php": {},
	".js": {}, ".mjs": {}, ".cjs": {}, ".jsx": {}, ".ts": {}, ".tsx": {}, ".cts": {}, ".mts": {},
	".java": {}, ".kt": {}, ".kts": {}, ".scala": {}, ".cs": {}, ".fs": {}, ".fsx": {},
	".c": {}, ".h": {}, ".cc": {}, ".cpp": {}, ".cxx": {}, ".hpp": {}, ".hh": {}, ".m": {}, ".mm": {},
	".swift": {}, ".dart": {}, ".lua": {}, ".r": {}, ".pl": {}, ".pm": {}, ".sh": {}, ".bash": {},
	".zsh": {}, ".ps1": {}, ".psm1": {}, ".bat": {}, ".cmd": {}, ".sql": {}, ".graphql": {}, ".gql": {},
	".md": {}, ".mdx": {}, ".rst": {}, ".txt": {}, ".toml": {}, ".yaml": {}, ".yml": {}, ".json": {},
	".jsonc": {}, ".json5": {}, ".xml": {}, ".html": {}, ".htm": {}, ".css": {}, ".scss": {}, ".sass": {},
	".less": {}, ".vue": {}, ".svelte": {}, ".astro": {}, ".ini": {}, ".cfg": {}, ".conf": {},
	".env": {}, ".gitignore": {}, ".dockerignore": {}, ".editorconfig": {}, ".csv": {}, ".tsv": {},
	".proto": {}, ".thrift": {}, ".avsc": {}, ".tf": {}, ".hcl": {}, ".nix": {}, ".zig": {},
	".ex": {}, ".exs": {}, ".erl": {}, ".hrl": {}, ".clj": {}, ".cljs": {}, ".edn": {},
	".gradle": {}, ".properties": {}, ".pom": {}, ".makefile": {},
}

var knownBinaryExts = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {}, ".ico": {}, ".bmp": {}, ".svgz": {},
	".mp3": {}, ".mp4": {}, ".wav": {}, ".ogg": {}, ".webm": {}, ".mov": {}, ".avi": {},
	".zip": {}, ".gz": {}, ".tgz": {}, ".bz2": {}, ".xz": {}, ".7z": {}, ".rar": {}, ".tar": {},
	".exe": {}, ".dll": {}, ".so": {}, ".dylib": {}, ".o": {}, ".a": {}, ".lib": {}, ".class": {},
	".jar": {}, ".war": {}, ".ear": {}, ".apk": {}, ".dmg": {}, ".iso": {},
	".pdf": {}, ".doc": {}, ".docx": {}, ".xls": {}, ".xlsx": {}, ".ppt": {}, ".pptx": {},
	".woff": {}, ".woff2": {}, ".ttf": {}, ".otf": {}, ".eot": {},
	".pyc": {}, ".pyo": {}, ".wasm": {}, ".bin": {}, ".dat": {}, ".db": {}, ".sqlite": {}, ".sqlite3": {},
}

// indexExcludedDirs are skipped only while building/freshening the project
// index. Resolve/List still allow reading build artifacts when the agent asks.
var indexExcludedDirs = map[string]bool{
	"dist": true, "build": true, "out": true, ".cache": true, ".gocache": true, ".tmp": true, ".idea": true,
	"target": true, ".venv": true, "venv": true, "__pycache__": true,
	".next": true, ".turbo": true, "coverage": true,
	".pnpm": true, ".pnpm-store": true, ".gradle": true, ".dart_tool": true, "pods": true,
	".yarn": true, "bower_components": true, ".tox": true, ".mypy_cache": true,
	".pytest_cache": true, ".ruff_cache": true, ".parcel-cache": true, ".svelte-kit": true,
}

func isIndexExcludedDir(name string) bool {
	key := strings.ToLower(name)
	return excludedDirs[key] || indexExcludedDirs[key]
}

type FS struct {
	root         string
	maxReadBytes int64
}

type FileContent struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Numbered  string `json:"numbered"`
	SHA256    string `json:"sha256,omitempty"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated"`
}

type Match struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

func Open(root string) (*FS, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace is not a directory: %s", abs)
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	return &FS{root: filepath.Clean(canonical), maxReadBytes: 1024 * 1024}, nil
}

func (f *FS) Root() string { return f.root }

// normalizeIncomingPath accepts the messy paths models actually send:
// quoted strings, file:// URLs, Windows backslashes, leading ./, Unix-style
// /foo on Windows, and absolute paths that still live inside this workspace.
func (f *FS) normalizeIncomingPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, `"'`)
	if path == "" || path == "." {
		return path
	}
	if strings.HasPrefix(strings.ToLower(path), "file:") {
		if parsed, err := url.Parse(path); err == nil && parsed.Path != "" {
			path = parsed.Path
			if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
				path = path[1:]
			}
		}
	}
	path = strings.TrimPrefix(path, "./")
	converted := filepath.FromSlash(path)
	if !filepath.IsAbs(converted) && filepath.VolumeName(converted) == "" {
		return converted
	}
	clean := filepath.Clean(converted)
	if rel, err := filepath.Rel(f.root, clean); err == nil && isRelativeInside(rel) {
		return rel
	}
	if runtime.GOOS == "windows" && filepath.VolumeName(converted) == "" {
		return strings.TrimLeft(converted, `\/`)
	}
	return converted
}

func isRelativeInside(rel string) bool {
	if rel == "" || rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (f *FS) Resolve(path string, allowMissing bool) (string, error) {
	path = f.normalizeIncomingPath(path)
	if path == "" {
		path = "."
	}
	if filepath.IsAbs(path) {
		return "", ErrOutsideWorkspace
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrOutsideWorkspace
	}
	// Двоеточие в звене — альтернативный поток NTFS (`.git::$INDEX_ALLOCATION`,
	// `file.txt:stream`): имени файла в нём на Windows быть не может.
	if runtime.GOOS == "windows" {
		for _, part := range strings.Split(clean, string(filepath.Separator)) {
			if strings.Contains(part, ":") {
				return "", ErrOutsideWorkspace
			}
		}
	}
	if hasExcludedComponent(clean) {
		return "", ErrExcluded
	}
	candidate := filepath.Join(f.root, clean)
	if !isWithin(f.root, candidate) {
		return "", ErrOutsideWorkspace
	}
	if hasEscapingReparsePoint(f.root, candidate) {
		return "", ErrOutsideWorkspace
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err == nil {
		if !isWithin(f.root, resolved) {
			return "", ErrOutsideWorkspace
		}
		// Разрешённый путь несёт длинные имена: `GIT~1` здесь уже `.git`.
		if rel, relErr := filepath.Rel(f.root, resolved); relErr != nil || hasExcludedComponent(rel) {
			return "", ErrExcluded
		}
		return resolved, nil
	}
	if !allowMissing || !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(candidate)
	for {
		resolvedParent, parentErr := filepath.EvalSymlinks(parent)
		if parentErr == nil {
			if !isWithin(f.root, resolvedParent) {
				return "", ErrOutsideWorkspace
			}
			if rel, relErr := filepath.Rel(f.root, resolvedParent); relErr != nil || hasExcludedComponent(rel) {
				return "", ErrExcluded
			}
			break
		}
		if !os.IsNotExist(parentErr) || samePath(parent, f.root) {
			return "", parentErr
		}
		parent = filepath.Dir(parent)
	}
	return candidate, nil
}

// hasExcludedComponent — есть ли в пути служебный каталог, закрытый для
// агента. Сравнение идёт так, как его видит Windows: без регистра и без
// хвостовых точек и пробелов, которые Win32 молча отбрасывает, — иначе
// `.git.` открывал настоящий `.git`, и запись хука в него исполнялась бы
// следующим `git commit` на машине человека. Короткое имя `GIT~1` ловит
// повторная проверка уже разрешённого пути.
func hasExcludedComponent(rel string) bool {
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		key := strings.ToLower(part)
		if runtime.GOOS == "windows" {
			key = strings.TrimRight(key, ". ")
		}
		if !excludedDirs[key] {
			continue
		}
		// Dependency trees are omitted from root listings, but agents may open an
		// explicit path under vendor/ or node_modules/ to inspect package APIs.
		if key == "vendor" || key == "node_modules" {
			continue
		}
		return true
	}
	return false
}

// hasEscapingReparsePoint проверяет каждое звено пути на точку повторного
// разбора, ведущую наружу.
//
// filepath.EvalSymlinks на Windows не разворачивает junction: он возвращает сам
// путь ссылки, поэтому проверка вложенности видела путь «внутри проекта», а
// запись уходила по ссылке наружу — файл создавался за пределами рабочей копии.
// Junction, в отличие от симлинка, создаётся без прав администратора, то есть
// такой обход доступен любому.
//
// os.Lstat помечает junction как ModeIrregular, а не ModeSymlink, поэтому
// смотрим оба признака; цель разворачиваем через os.Readlink, который junction
// разбирает верно. Нерасшифруемое звено считаем выходом наружу: неизвестная
// ссылка внутри границы — это не «внутри».
//
// Оговорка о проверках: ветка «Readlink не смог» тестом не покрыта — ссылку,
// которую os.Lstat признаёт точкой разбора, а os.Readlink прочитать не может,
// переносимо не собрать. Выбор в пользу отказа сделан из осторожности, а не по
// результату измерения.
func hasEscapingReparsePoint(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return true
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			// Звена ещё нет — дальше по нему никто не пройдёт.
			return false
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0 {
			continue
		}
		target, linkErr := os.Readlink(current)
		if linkErr != nil {
			return true
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(current), target)
		}
		if !isWithin(root, filepath.Clean(target)) {
			return true
		}
		// Ссылка внутри проекта, ведущая в служебный каталог (`link` → `.git`),
		// открывает то же, что `.git` напрямую.
		if rel, relErr := filepath.Rel(root, filepath.Clean(target)); relErr != nil || hasExcludedComponent(rel) {
			return true
		}
	}
	return false
}

func isWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// HasParentDirSegment reports a ".." path element. Filenames like notes..md stay allowed.
func HasParentDirSegment(value string) bool {
	for _, part := range strings.Split(filepath.ToSlash(value), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func IsSensitive(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if runtime.GOOS == "windows" {
		base = strings.TrimRight(base, ". ")
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") || base == ".npmrc" || base == ".pypirc" || base == "credentials" {
		return true
	}
	exts := []string{".pem", ".key", ".p12", ".pfx", ".kdbx"}
	for _, ext := range exts {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return strings.Contains(base, "id_rsa") || strings.Contains(base, "id_ed25519")
}

func (f *FS) List(ctx context.Context, maxDepth int) ([]domain.FileNode, error) {
	return f.ListPath(ctx, "", maxDepth)
}

func (f *FS) ListPath(ctx context.Context, rel string, maxDepth int) ([]domain.FileNode, error) {
	if maxDepth <= 0 {
		maxDepth = 8
	}
	start, relative := f.root, ""
	if trimmed := strings.TrimSpace(rel); trimmed != "" && trimmed != "." {
		abs, err := f.Resolve(trimmed, true)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(abs)
		if err != nil {
			if os.IsNotExist(err) {
				// Missing dirs are a normal greenfield state — return empty so agents
				// can treat list_files as inspection without inventing paths.
				return []domain.FileNode{}, nil
			}
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("path is not a directory")
		}
		start = abs
		relative, err = filepath.Rel(f.root, abs)
		if err != nil {
			return nil, err
		}
		relative = filepath.ToSlash(relative)
	}
	count := 0
	return f.listDir(ctx, start, relative, 0, maxDepth, &count)
}

func (f *FS) listDir(ctx context.Context, absolute, relative string, depth, maxDepth int, count *int) ([]domain.FileNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	result := make([]domain.FileNode, 0, len(entries))
	for _, entry := range entries {
		if *count >= 5000 {
			return result, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		lower := strings.ToLower(name)
		if IsSensitive(name) {
			continue
		}
		// Skip dependency roots when listing a parent, but allow listing when the
		// caller already asked for vendor/ or node_modules/ explicitly.
		if excludedDirs[lower] {
			relLower := strings.ToLower(filepath.ToSlash(relative))
			underDeps := relLower == "vendor" || strings.HasPrefix(relLower, "vendor/") ||
				relLower == "node_modules" || strings.HasPrefix(relLower, "node_modules/")
			if !underDeps {
				continue
			}
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if !info.IsDir() && !likelyTextPath(pathJoin(absolute, name)) {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(relative, name))
		node := domain.FileNode{Name: name, Path: rel, IsDir: info.IsDir(), Size: info.Size()}
		*count++
		if info.IsDir() && depth < maxDepth {
			node.Children, _ = f.listDir(ctx, filepath.Join(absolute, name), rel, depth+1, maxDepth, count)
		}
		result = append(result, node)
	}
	return result, nil
}

func (f *FS) Read(path string, allowSensitive bool) (FileContent, error) {
	return f.readContent(path, allowSensitive, true)
}

func (f *FS) readContent(path string, allowSensitive bool, numbered bool) (FileContent, error) {
	if IsSensitive(path) && !allowSensitive {
		return FileContent{}, ErrSensitive
	}
	abs, err := f.Resolve(path, false)
	if err != nil {
		return FileContent{}, err
	}
	// Короткое имя (`ENV~1`) или хвостовая точка проходят проверку по
	// присланному имени, а открывают настоящий `.env`.
	if IsSensitive(abs) && !allowSensitive {
		return FileContent{}, ErrSensitive
	}
	info, err := os.Stat(abs)
	if err != nil {
		return FileContent{}, err
	}
	if info.IsDir() {
		return FileContent{}, fmt.Errorf("cannot read a directory")
	}
	file, err := os.Open(abs)
	if err != nil {
		return FileContent{}, err
	}
	defer file.Close()
	displayPath := path
	if rel, relErr := filepath.Rel(f.root, abs); relErr == nil {
		displayPath = filepath.ToSlash(rel)
	}
	return readOpenedContent(file, info, displayPath, f.maxReadBytes, numbered)
}

// readIndexCandidateFile validates and opens a regular file discovered by
// WalkDir without requiring the walk to stat every file serially. The Lstat
// identity is compared with the opened handle, so a replacement or link race
// cannot redirect indexing outside the workspace.
func (f *FS) readIndexCandidateFile(abs, relative string) (FileContent, os.FileInfo, error) {
	if IsSensitive(relative) || !isWithin(f.root, abs) {
		return FileContent{}, nil, ErrOutsideWorkspace
	}
	walked, err := os.Lstat(abs)
	if err != nil {
		return FileContent{}, nil, err
	}
	if !walked.Mode().IsRegular() || walked.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return FileContent{}, nil, ErrOutsideWorkspace
	}
	file, err := os.Open(abs)
	if err != nil {
		return FileContent{}, nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return FileContent{}, nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(walked, opened) {
		return FileContent{}, nil, ErrOutsideWorkspace
	}
	content, err := readOpenedContent(file, opened, filepath.ToSlash(relative), f.maxReadBytes, false)
	if err != nil {
		return FileContent{}, nil, err
	}
	return content, opened, nil
}

func readOpenedContent(file *os.File, info os.FileInfo, displayPath string, maxReadBytes int64, numbered bool) (FileContent, error) {
	limited := io.LimitReader(file, maxReadBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return FileContent{}, err
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return FileContent{}, ErrBinary
	}
	truncated := int64(len(data)) > maxReadBytes
	if truncated {
		data = data[:maxReadBytes]
	}
	content := string(data)
	digest := ""
	if !truncated {
		digest = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	result := FileContent{Path: displayPath, Content: content, SHA256: digest, Size: info.Size(), Truncated: truncated}
	if !numbered {
		return result, nil
	}
	var builder strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(content))
	line := 1
	for scanner.Scan() {
		fmt.Fprintf(&builder, "%6d | %s\n", line, scanner.Text())
		line++
	}
	result.Numbered = builder.String()
	return result, nil
}

// Write atomically replaces one UTF-8 text file inside the workspace. It is
// intended for explicit user edits in the IDE; agent writes still go through
// the patch approval flow.
func (f *FS) Write(path, content string) (FileContent, error) {
	if IsSensitive(path) {
		return FileContent{}, ErrSensitive
	}
	if len(content) > 2*1024*1024 {
		return FileContent{}, errors.New("file content exceeds 2 MiB")
	}
	if !utf8.ValidString(content) || strings.IndexByte(content, 0) >= 0 {
		return FileContent{}, ErrBinary
	}
	abs, err := f.Resolve(path, true)
	if err != nil {
		return FileContent{}, err
	}
	mode := os.FileMode(0600)
	if info, statErr := os.Stat(abs); statErr == nil {
		if info.IsDir() {
			return FileContent{}, errors.New("cannot write a directory")
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(statErr) {
		return FileContent{}, statErr
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".workbench-edit-*")
	if err != nil {
		return FileContent{}, err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err = tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		cleanup()
		return FileContent{}, err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return FileContent{}, err
	}
	if err = tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return FileContent{}, err
	}
	if err = tmp.Close(); err != nil {
		cleanup()
		return FileContent{}, err
	}
	if err = os.Rename(tmpName, abs); err != nil {
		// Windows cannot replace an existing destination atomically. Keep a
		// rollback copy so a failed replacement never loses the user's file.
		backup := tmpName + ".original"
		if backupErr := os.Rename(abs, backup); backupErr != nil {
			cleanup()
			return FileContent{}, err
		}
		if err = os.Rename(tmpName, abs); err != nil {
			_ = os.Rename(backup, abs)
			cleanup()
			return FileContent{}, err
		}
		_ = os.Remove(backup)
	}
	f.InvalidateIndex()
	return f.Read(path, false)
}

// RestoreAgentChange performs an optimistic rollback. It refuses to touch a
// file when its current contents differ from the exact agent-produced state,
// so a rollback can never overwrite a later user edit silently.
func (f *FS) RestoreAgentChange(path, expected, original string, originalExisted bool) error {
	if IsSensitive(path) {
		return ErrSensitive
	}
	// The agent may have deleted a file that existed before the change, so the
	// final path is allowed to be missing. Resolve still enforces the workspace
	// boundary and rejects unsafe traversal/symlink targets.
	abs, err := f.Resolve(path, true)
	if err != nil {
		return err
	}
	current := ""
	if data, readErr := os.ReadFile(abs); readErr == nil {
		if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
			return ErrBinary
		}
		current = string(data)
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	if sha256.Sum256([]byte(current)) != sha256.Sum256([]byte(expected)) {
		return errors.New("file changed after the agent edit; rollback refused")
	}
	if !originalExisted {
		if err = os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return err
		}
		f.InvalidateIndex()
		return nil
	}
	_, err = f.Write(path, original)
	return err
}

func (f *FS) Search(ctx context.Context, query string, maxResults int) ([]Match, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("search query is empty")
	}
	if maxResults <= 0 || maxResults > 500 {
		maxResults = 200
	}
	matches := make([]Match, 0)
	err := filepath.WalkDir(f.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != f.root && excludedDirs[strings.ToLower(entry.Name())] {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || IsSensitive(entry.Name()) {
			return nil
		}
		rel, _ := filepath.Rel(f.root, path)
		content, err := f.Read(filepath.ToSlash(rel), false)
		if err != nil {
			return nil
		}
		needle := strings.ToLower(query)
		scanner := bufio.NewScanner(strings.NewReader(content.Content))
		line := 1
		for scanner.Scan() {
			if strings.Contains(strings.ToLower(scanner.Text()), needle) {
				matches = append(matches, Match{Path: filepath.ToSlash(rel), Line: line, Text: scanner.Text()})
				if len(matches) >= maxResults {
					return errLimitReached
				}
			}
			line++
		}
		return nil
	})
	if errors.Is(err, errLimitReached) {
		err = nil
	}
	return matches, err
}

var errLimitReached = errors.New("result limit reached")

func pathJoin(directory, name string) string { return filepath.Join(directory, name) }

// likelyTextPath decides whether a path is worth reading as text. Known source
// extensions short-circuit without opening the file; known binaries are rejected;
// everything else falls back to an 8KiB content sniff.
func likelyTextPath(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		switch base {
		case "makefile", "dockerfile", "containerfile", "jenkinsfile", "gemfile", "rakefile", "procfile", "license", "readme", "changelog":
			return true
		}
	}
	if _, ok := knownBinaryExts[ext]; ok {
		return false
	}
	if _, ok := knownTextExts[ext]; ok {
		return true
	}
	// Dotfiles like ".env.local" often have compound suffixes; treat ".env*" as text.
	if strings.HasPrefix(base, ".env") {
		return true
	}
	return isText(path)
}

func isText(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	buffer := make([]byte, 8192)
	count, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		return false
	}
	sample := buffer[:count]
	return bytes.IndexByte(sample, 0) < 0 && utf8.Valid(sample)
}
