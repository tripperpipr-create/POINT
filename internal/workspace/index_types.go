// Что индекс отдаёт наружу: куски, попадания, карта проекта.
package workspace

type IndexedChunk struct {
	Path       string   `json:"path"`
	StartLine  int      `json:"startLine"`
	EndLine    int      `json:"endLine"`
	Language   string   `json:"language"`
	Symbols    []string `json:"symbols,omitempty"`
	Content    string   `json:"content"`
	FileSHA256 string   `json:"fileSha256,omitempty"`
	// IndexedSHA256 — отпечаток того, что реально попало в индекс.
	//
	// Для файла целиком он совпадает с FileSHA256. Файл длиннее лимита чтения
	// приходит обрезанным, и FileSHA256 у него пуст: отдавать наружу отпечаток
	// куска как отпечаток файла нельзя, по нему сверяют исходник перед
	// правкой. Свежесть индекса при этом сверять надо, поэтому отпечаток
	// прочитанного куска живёт отдельным полем и наружу не уходит.
	IndexedSHA256 string `json:"-"`
}

type RelevantChunk struct {
	IndexedChunk
	Score         int      `json:"score"`
	MatchedTokens []string `json:"matchedTokens,omitempty"`
}

type ContextSearchResult struct {
	Chunks                []RelevantChunk `json:"chunks"`
	CandidateChunks       int             `json:"candidateChunks"`
	ReturnedChunks        int             `json:"returnedChunks"`
	UsedChars             int             `json:"usedChars"`
	MaxChars              int             `json:"maxChars"`
	MaxChunks             int             `json:"maxChunks"`
	Truncated             bool            `json:"truncated"`
	RelatedFiles          []RelatedFile   `json:"relatedFiles,omitempty"`
	RelatedFilesTruncated bool            `json:"relatedFilesTruncated,omitempty"`
}

type RelatedFile struct {
	Path       string `json:"path"`
	Relation   string `json:"relation"`
	Via        string `json:"via,omitempty"`
	FileSHA256 string `json:"fileSha256"`
}

// IndexHit is a compact navigation result for the IDE. It never includes full chunk text.
type IndexHit struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Language string `json:"language,omitempty"`
	Score    int    `json:"score"`
	Snippet  string `json:"snippet,omitempty"`
}

type IndexSearchResult struct {
	Query  string      `json:"query"`
	Status IndexStatus `json:"status"`
	Hits   []IndexHit  `json:"hits"`
}
