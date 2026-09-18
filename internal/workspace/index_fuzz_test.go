package workspace

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzCodeIndexSelectionIsDeterministicAndBounded(f *testing.F) {
	f.Add("ResolveSessionToken", "func ResolveSessionToken() {}\n", "resolve session token docs\n", uint16(1000), uint8(2))
	f.Add("quest marker", strings.Repeat("quest marker\n", 80), "func QuestMarker() {}\n", uint16(4096), uint8(4))
	f.Fuzz(func(t *testing.T, query, first, second string, rawMaxChars uint16, rawMaxChunks uint8) {
		query = strings.ToValidUTF8(query, "")
		first = strings.ToValidUTF8(first, "")
		second = strings.ToValidUTF8(second, "")
		if len(query) > 4096 {
			return
		}
		if len(first) > 8192 {
			first = utf8Prefix(first, 8192)
		}
		if len(second) > 8192 {
			second = utf8Prefix(second, 8192)
		}
		queryTokens := expandedTokens(query)
		if len(queryTokens) == 0 || len(queryTokens) > 64 {
			return
		}
		maxChars := 1 + int(rawMaxChars)%4096
		maxChunks := 1 + int(rawMaxChunks)%4
		contents := []string{first, first + second, second, second + first}
		chunks := []IndexedChunk{
			{Path: "a.go", StartLine: 1, EndLine: 64, Content: contents[0]},
			{Path: "a.go", StartLine: 49, EndLine: 112, Content: contents[1]},
			{Path: "a.go", StartLine: 97, EndLine: 160, Content: contents[2]},
			{Path: "b.go", StartLine: 1, EndLine: 64, Content: contents[3]},
		}
		index := &projectIndex{chunks: chunks, tokens: map[string][]int{}}
		for chunkID, chunk := range chunks {
			for _, token := range expandedTokens(chunk.Path + "\n" + chunk.Content) {
				index.tokens[token] = append(index.tokens[token], chunkID)
			}
		}
		firstResult := selectRelevantChunks(index, query, queryTokens, maxChunks, maxChars)
		secondResult := selectRelevantChunks(index, query, queryTokens, maxChunks, maxChars)
		if !reflect.DeepEqual(firstResult, secondResult) {
			t.Fatalf("selection is nondeterministic: %#v != %#v", firstResult, secondResult)
		}
		if firstResult.UsedChars > maxChars || len(firstResult.Chunks) > maxChunks || firstResult.ReturnedChunks != len(firstResult.Chunks) || firstResult.CandidateChunks < firstResult.ReturnedChunks {
			t.Fatalf("unbounded selection: %#v", firstResult)
		}
		used := 0
		for left, chunk := range firstResult.Chunks {
			if !utf8.ValidString(chunk.Content) {
				t.Fatalf("invalid UTF-8 chunk: %q", chunk.Content)
			}
			used += len(chunk.Content)
			for right := left + 1; right < len(firstResult.Chunks); right++ {
				other := firstResult.Chunks[right]
				if chunk.Path == other.Path && chunk.StartLine <= other.EndLine && other.StartLine <= chunk.EndLine {
					t.Fatalf("overlapping chunks: %#v %#v", chunk, other)
				}
			}
		}
		if used != firstResult.UsedChars {
			t.Fatalf("used chars=%d metrics=%d", used, firstResult.UsedChars)
		}
	})
}
