// Отбор кусков кода в контекст модели.
package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"local-agent-workbench/internal/textutil"
)

func (f *FS) RelevantContext(ctx context.Context, query string, maxChunks, maxChars int) ([]RelevantChunk, error) {
	result, err := f.SearchContext(ctx, query, maxChunks, maxChars)
	return result.Chunks, err
}

func (f *FS) SearchContext(ctx context.Context, query string, maxChunks, maxChars int) (ContextSearchResult, error) {
	return f.SearchContextWithRelations(ctx, query, maxChunks, maxChars, false)
}

func (f *FS) SearchContextWithRelations(ctx context.Context, query string, maxChunks, maxChars int, includeRelated bool) (ContextSearchResult, error) {
	if len(query) > 4096 {
		return ContextSearchResult{}, errors.New("context query exceeds 4096 UTF-8 bytes")
	}
	queryTokens := expandedTokens(query)
	if len(queryTokens) == 0 {
		return ContextSearchResult{}, errors.New("context query is empty")
	}
	if len(queryTokens) > 64 {
		return ContextSearchResult{}, errors.New("context query has more than 64 meaningful terms; refine it to a symbol, path, or focused concept")
	}
	if maxChunks <= 0 || maxChunks > 20 {
		maxChunks = 6
	}
	if maxChars <= 0 || maxChars > 64*1024 {
		maxChars = 16 * 1024
	}
	// Attempt 0: search the ready snapshot without a full-tree drift walk.
	// Hits with current digests short-circuit the expensive freshen. Misses and
	// stale digests fall through to ensureFreshIndex / targeted UpdateIndex.
	for attempt := 0; attempt < 3; attempt++ {
		var index *projectIndex
		var err error
		freshened := false
		switch {
		case attempt == 0:
			index = f.peekReadyIndex()
			if index == nil {
				index, err = f.ensureFreshIndex(ctx)
				freshened = true
			}
		default:
			index, err = f.ensureFreshIndex(ctx)
			freshened = true
		}
		if err != nil {
			return ContextSearchResult{}, err
		}
		if index == nil {
			return ContextSearchResult{}, errors.New("project index is unavailable")
		}
		search := selectRelevantChunks(index, query, queryTokens, maxChunks, maxChars)
		if includeRelated {
			search.RelatedFiles, search.RelatedFilesTruncated = selectRelatedFiles(index, search.Chunks, 20)
		}
		if attempt == 0 && !freshened && search.CandidateChunks == 0 {
			// Likely a brand-new file/symbol — walk once, then retry.
			continue
		}
		current, err := f.relevantChunksCurrent(search.Chunks)
		if err != nil {
			return ContextSearchResult{}, err
		}
		if current && includeRelated {
			current, err = f.relatedFilesCurrent(search.RelatedFiles)
			if err != nil {
				return ContextSearchResult{}, err
			}
		}
		if current {
			if !freshened {
				if missing := f.missingIndexedPaths(index); len(missing) > 0 {
					if _, err = f.UpdateIndex(ctx, nil, missing); err != nil {
						return ContextSearchResult{}, err
					}
					continue
				}
			}
			return search, nil
		}
		stale := make([]string, 0, len(search.Chunks)+len(search.RelatedFiles))
		for _, chunk := range search.Chunks {
			stale = append(stale, chunk.Path)
		}
		for _, related := range search.RelatedFiles {
			stale = append(stale, related.Path)
		}
		if len(stale) == 0 {
			return search, nil
		}
		if _, err = f.UpdateIndex(ctx, stale, nil); err != nil {
			return ContextSearchResult{}, err
		}
	}
	return ContextSearchResult{}, errors.New("project changed while indexed context was being prepared; retry the search")
}

func (f *FS) relatedFilesCurrent(files []RelatedFile) (bool, error) {
	for _, related := range files {
		if !validIndexSHA256(related.FileSHA256) {
			return false, nil
		}
		content, err := f.Read(related.Path, false)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		if !strings.EqualFold(content.SHA256, related.FileSHA256) {
			return false, nil
		}
	}
	return true, nil
}

func selectRelevantChunks(index *projectIndex, query string, queryTokens []string, maxChunks, maxChars int) ContextSearchResult {
	scores := map[int]int{}
	matches := map[int]map[string]bool{}
	const (
		partialExactHitFloor = 8  // skip vocab partial scan when exact hits are plentiful
		partialChunkCap      = 64 // cap partial matches per query token
	)
	for _, token := range queryTokens {
		exactChunkIDs := index.tokens[token]
		rarity := 0
		if len(exactChunkIDs) > 0 {
			rarity = min(24, (len(index.chunks)*8)/(len(exactChunkIDs)+1))
		}
		for _, chunkID := range exactChunkIDs {
			scores[chunkID] += 8 + rarity
			if matches[chunkID] == nil {
				matches[chunkID] = map[string]bool{}
			}
			matches[chunkID][token] = true
		}
		// Partial token scan is O(vocab). Skip when the exact posting list is
		// already rich enough, or the token is too short to be discriminative.
		if len(token) < 4 || len(exactChunkIDs) >= partialExactHitFloor {
			continue
		}
		partialChunkIDs := map[int]bool{}
		for indexedToken, chunkIDs := range index.tokens {
			if len(partialChunkIDs) >= partialChunkCap {
				break
			}
			if indexedToken != token && strings.Contains(indexedToken, token) {
				for _, chunkID := range chunkIDs {
					partialChunkIDs[chunkID] = true
					if len(partialChunkIDs) >= partialChunkCap {
						break
					}
				}
			}
		}
		for chunkID := range partialChunkIDs {
			scores[chunkID] += 2
			if matches[chunkID] == nil {
				matches[chunkID] = map[string]bool{}
			}
			matches[chunkID][token] = true
		}
	}
	queryPhrase := strings.ToLower(strings.TrimSpace(query))
	compactQuery := compactToken(query)
	for chunkID := range scores {
		chunk := index.chunks[chunkID]
		if len([]rune(queryPhrase)) >= 3 && strings.Contains(strings.ToLower(chunk.Content), queryPhrase) {
			scores[chunkID] += 24
		}
		path := strings.ToLower(chunk.Path)
		if strings.Contains(path, queryPhrase) || (compactQuery != "" && strings.Contains(compactToken(filepath.Base(path)), compactQuery)) {
			scores[chunkID] += 28
		}
		for _, symbol := range chunk.Symbols {
			compactSymbol := compactToken(symbol)
			switch {
			case compactQuery != "" && compactSymbol == compactQuery:
				scores[chunkID] += 64
			case len([]rune(compactQuery)) >= 4 && strings.Contains(compactSymbol, compactQuery):
				scores[chunkID] += 20
			}
		}
	}
	result := make([]RelevantChunk, 0, len(scores))
	for chunkID, score := range scores {
		if chunkID >= 0 && chunkID < len(index.chunks) {
			matched := make([]string, 0, len(matches[chunkID]))
			for _, token := range queryTokens {
				if matches[chunkID][token] {
					matched = append(matched, token)
				}
			}
			result = append(result, RelevantChunk{IndexedChunk: index.chunks[chunkID], Score: score, MatchedTokens: matched})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		if result[i].Path != result[j].Path {
			return result[i].Path < result[j].Path
		}
		return result[i].StartLine < result[j].StartLine
	})
	search := ContextSearchResult{Chunks: make([]RelevantChunk, 0, min(maxChunks, len(result))), CandidateChunks: len(result), MaxChars: maxChars, MaxChunks: maxChunks}
	type lineRange struct{ start, end int }
	selectedRanges := make(map[string][]lineRange)
	selectedPerPath := make(map[string]int)
	remainingChunks := append([]RelevantChunk(nil), result...)
	for len(remainingChunks) > 0 {
		if len(search.Chunks) >= maxChunks || search.UsedChars >= maxChars {
			search.Truncated = true
			break
		}
		bestIndex, bestAdjustedScore := -1, -1
		for index, candidate := range remainingChunks {
			overlaps := false
			for _, selected := range selectedRanges[candidate.Path] {
				if candidate.StartLine <= selected.end && selected.start <= candidate.EndLine {
					overlaps = true
					break
				}
			}
			if overlaps {
				continue
			}
			adjustedScore := candidate.Score
			if count := selectedPerPath[candidate.Path]; count > 0 {
				adjustedScore = candidate.Score * 3 / (3 + count)
			}
			if adjustedScore > bestAdjustedScore {
				bestIndex, bestAdjustedScore = index, adjustedScore
			}
		}
		if bestIndex < 0 {
			search.Truncated = len(remainingChunks) > 0
			break
		}
		chunk := remainingChunks[bestIndex]
		remainingChunks = append(remainingChunks[:bestIndex], remainingChunks[bestIndex+1:]...)
		remaining := maxChars - search.UsedChars
		if len(chunk.Content) > remaining {
			chunk.Content = textutil.BoundedBytes(chunk.Content, remaining)
			chunk.EndLine = chunk.StartLine + strings.Count(chunk.Content, "\n")
			search.Truncated = true
		}
		if chunk.Content == "" {
			search.Truncated = true
			break
		}
		search.UsedChars += len(chunk.Content)
		search.Chunks = append(search.Chunks, chunk)
		selectedPerPath[chunk.Path]++
		selectedRanges[chunk.Path] = append(selectedRanges[chunk.Path], lineRange{start: chunk.StartLine, end: chunk.EndLine})
		for index := len(remainingChunks) - 1; index >= 0; index-- {
			candidate := remainingChunks[index]
			if candidate.Path != chunk.Path {
				continue
			}
			if candidate.StartLine <= chunk.EndLine && chunk.StartLine <= candidate.EndLine {
				remainingChunks = append(remainingChunks[:index], remainingChunks[index+1:]...)
				search.Truncated = true
			}
		}
	}
	search.ReturnedChunks = len(search.Chunks)
	if search.ReturnedChunks < search.CandidateChunks {
		search.Truncated = true
	}
	return search
}

// relevantChunksCurrent сверяет выбранные куски с диском по отпечатку того,
// что индексировалось. Сверять по FileSHA256 нельзя: у файла длиннее лимита
// чтения он пуст с обеих сторон, такой кусок навсегда считался бы устаревшим,
// и поиск контекста упирался бы в потолок попыток вместо ответа.
func (f *FS) relevantChunksCurrent(chunks []RelevantChunk) (bool, error) {
	checked := make(map[string]string)
	for _, chunk := range chunks {
		if !validIndexSHA256(chunk.IndexedSHA256) {
			return false, nil
		}
		if digest, exists := checked[chunk.Path]; exists {
			if !strings.EqualFold(digest, chunk.IndexedSHA256) {
				return false, nil
			}
			continue
		}
		content, err := f.Read(chunk.Path, false)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		current := indexedContentDigest(content)
		if !strings.EqualFold(current, chunk.IndexedSHA256) {
			return false, nil
		}
		checked[chunk.Path] = current
	}
	return true, nil
}
