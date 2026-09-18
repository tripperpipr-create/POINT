// Поиск по индексу и ранжирование попаданий.
package workspace

import (
	"context"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type ProjectMap struct {
	FilesByLanguage map[string]int `json:"filesByLanguage"`
	TopDirectories  []string       `json:"topDirectories"`
	Symbols         []string       `json:"symbols"`
	Status          IndexStatus    `json:"status"`
}

func (f *FS) ProjectMap(ctx context.Context, maxSymbols int) (ProjectMap, error) {
	// Prefer the ready snapshot: companion and Hub call this often and a full-tree
	// drift walk is unnecessary when the extension already keeps the index fresh.
	index := f.peekReadyIndex()
	var err error
	if index == nil {
		index, err = f.ensureIndex(ctx)
		if err != nil {
			return ProjectMap{}, err
		}
	}
	if maxSymbols <= 0 || maxSymbols > 200 {
		maxSymbols = 80
	}
	type counted struct {
		value string
		count int
	}
	directories := make([]counted, 0, len(index.dirs))
	for value, count := range index.dirs {
		directories = append(directories, counted{value, count})
	}
	sort.Slice(directories, func(i, j int) bool { return directories[i].count > directories[j].count })
	topDirectories := make([]string, 0, min(20, len(directories)))
	for _, item := range directories[:min(20, len(directories))] {
		topDirectories = append(topDirectories, item.value)
	}
	symbols := uniqueStrings(index.symbols)
	if len(symbols) > maxSymbols {
		symbols = symbols[:maxSymbols]
	}
	return ProjectMap{FilesByLanguage: cloneLanguageCounts(index.language), TopDirectories: topDirectories, Symbols: symbols, Status: index.status}, nil
}

// LookupIndex ranks the already-built index for IDE navigation. It never starts a rebuild.
func (f *FS) LookupIndex(query string, limit int) IndexSearchResult {
	query = strings.TrimSpace(query)
	result := IndexSearchResult{Query: query, Hits: []IndexHit{}}
	if limit <= 0 || limit > 80 {
		limit = 40
	}
	slot := f.indexSlot()
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.index == nil {
		result.Status = IndexStatus{State: "not_built"}
		return result
	}
	result.Status = slot.index.status
	if query == "" {
		return result
	}
	result.Hits = rankIndexHits(slot.index, query, limit)
	return result
}

func rankIndexHits(index *projectIndex, query string, limit int) []IndexHit {
	compactQuery := compactToken(query)
	phrase := strings.ToLower(query)
	seen := map[string]bool{}
	hits := make([]IndexHit, 0, limit)
	for _, chunkID := range collectIndexCandidateIDs(index, query, compactQuery) {
		if chunkID < 0 || chunkID >= len(index.chunks) {
			continue
		}
		chunk := index.chunks[chunkID]
		snippet := indexSnippet(chunk.Content)
		pathScore := 0
		if compactQuery != "" && compactToken(filepath.Base(chunk.Path)) == compactQuery {
			pathScore = 80
		} else if compactQuery != "" && strings.Contains(compactToken(filepath.Base(chunk.Path)), compactQuery) {
			pathScore = 36
		} else if len([]rune(phrase)) >= 3 && strings.Contains(strings.ToLower(chunk.Path), phrase) {
			pathScore = 24
		}
		matchedSymbol := false
		for _, symbol := range chunk.Symbols {
			score := 0
			compactSymbol := compactToken(symbol)
			switch {
			case compactQuery != "" && compactSymbol == compactQuery:
				score = 100
			case compactQuery != "" && strings.HasPrefix(compactSymbol, compactQuery):
				score = 72
			case len([]rune(compactQuery)) >= 3 && strings.Contains(compactSymbol, compactQuery):
				score = 40
			}
			if score == 0 {
				continue
			}
			matchedSymbol = true
			addIndexHit(&hits, seen, IndexHit{
				Kind: "symbol", Name: symbol, Path: chunk.Path, Line: chunk.StartLine,
				Language: chunk.Language, Score: score + pathScore, Snippet: snippet,
			})
		}
		contentScore := 0
		if len([]rune(phrase)) >= 3 && strings.Contains(strings.ToLower(chunk.Content), phrase) {
			contentScore = 18
		}
		if !matchedSymbol && (pathScore > 0 || contentScore > 0) {
			name := filepath.Base(chunk.Path)
			if len(chunk.Symbols) > 0 {
				name = chunk.Symbols[0]
			}
			addIndexHit(&hits, seen, IndexHit{
				Kind: "chunk", Name: name, Path: chunk.Path, Line: chunk.StartLine,
				Language: chunk.Language, Score: pathScore + contentScore, Snippet: snippet,
			})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Path != hits[j].Path {
			return hits[i].Path < hits[j].Path
		}
		return hits[i].Line < hits[j].Line
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

func collectIndexCandidateIDs(index *projectIndex, query, compactQuery string) []int {
	// An exact compact token is emitted for complete identifiers such as
	// RotateRefreshToken. Prefer its posting list over broad component tokens
	// (rotate/refresh/token), which otherwise turn a precise lookup into a
	// full-index scan on large projects.
	if compactQuery != "" {
		if exact := index.tokens[compactQuery]; len(exact) > 0 {
			ids := append([]int(nil), exact...)
			sort.Ints(ids)
			return ids
		}
	}
	seen := map[int]bool{}
	add := func(ids []int) {
		for _, id := range ids {
			if id >= 0 && id < len(index.chunks) {
				seen[id] = true
			}
		}
	}
	for _, token := range expandedTokens(query) {
		add(index.tokens[token])
	}
	if compactQuery != "" {
		add(index.tokens[compactQuery])
	}
	if len(seen) < 24 && len(compactQuery) >= 3 {
		for token, ids := range index.tokens {
			if strings.HasPrefix(token, compactQuery) || (len(compactQuery) >= 4 && strings.Contains(token, compactQuery)) {
				add(ids)
				if len(seen) >= 400 {
					break
				}
			}
		}
	}
	ids := make([]int, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids
}

func addIndexHit(hits *[]IndexHit, seen map[string]bool, hit IndexHit) {
	key := hit.Kind + "\n" + hit.Path + "\n" + hit.Name + "\n" + strconv.Itoa(hit.Line)
	if seen[key] {
		return
	}
	seen[key] = true
	*hits = append(*hits, hit)
}

func indexSnippet(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		runes := []rune(line)
		if len(runes) > 160 {
			return string(runes[:160]) + "…"
		}
		return line
	}
	return ""
}

func cloneLanguageCounts(source map[string]int) map[string]int {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string]int, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}
