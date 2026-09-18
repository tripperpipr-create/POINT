package domain

type MasterConversation struct {
	Model       string `json:"model,omitempty"`
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Title       string `json:"title"`
	Archived    bool   `json:"archived,omitempty"`
	Pinned      bool   `json:"pinned,omitempty"`
	Temporary   bool   `json:"temporary,omitempty"`
	Mode        string `json:"mode"`
	WorkMode    string `json:"workMode"`
	Summary     string `json:"summary,omitempty"`
	UpdatedAt   string `json:"updatedAt"`
	ParentID    string `json:"parentId,omitempty"`
}

// Строка каталога чатов Чертога — разговор из любого мира.
//
// Отдельный тип, а не расширение MasterConversation: тот алиасится в
// app.MasterSession и сериализуется в текущий интерфейс, и любое поле, добавленное
// туда, уедет в каждый ответ мастера.
//
// WorkspacePath заполняется только для текущего мира. Границу песочницы задаёт
// WORKSPACE_ROOT, то есть она равна текущему миру, и фильтровать по ней каталог
// бессмысленно — она отсекла бы все чужие записи. Но отдавать наружу абсолютные
// пути чужих проектов всё равно нельзя, поэтому их место занимает WorkspaceHash:
// по нему хост сшивает строку со своим реестром миров и берёт путь оттуда.
type MasterConversationRef struct {
	WorkspaceID   string `json:"workspaceId"`
	WorkspaceName string `json:"workspaceName"`
	WorkspaceHash string `json:"workspaceHash,omitempty"`
	WorkspacePath string `json:"workspacePath,omitempty"`
	ID            string `json:"id"`
	Title         string `json:"title"`
	UpdatedAt     string `json:"updatedAt"`
	Pinned        bool   `json:"pinned,omitempty"`
	Running       bool   `json:"running,omitempty"`
}

type MasterAttachment struct {
	Path      string `json:"path,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Content   string `json:"content,omitempty"`
	MIME      string `json:"mime,omitempty"`
	StartLine int    `json:"startLine,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}
type MasterMemoryEntry struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	SourceID  string `json:"sourceId,omitempty"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updatedAt"`
}
type MasterTurn struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversationId"`
	WorkspaceID    string `json:"workspaceId"`
	WorkOrderID    string `json:"workOrderId,omitempty"`
	Status         string `json:"status"`
	Reply          string `json:"reply"`
	Error          string `json:"error,omitempty"`
	RequestHash    string `json:"-"`
	UpdatedAt      string `json:"updatedAt"`
}
type MasterTurnEvent struct {
	Sequence       int64  `json:"sequence"`
	TurnID         string `json:"turnId"`
	ConversationID string `json:"conversationId"`
	Type           string `json:"type"`
	Text           string `json:"text,omitempty"`
	// Detail — подробности события в JSON: аргумент инструмента, его ответ,
	// накопленное рассуждение. Text остаётся короткой человеческой строкой,
	// потому что её показывают сборки, которые о подробностях не знают.
	Detail string `json:"detail,omitempty"`
}
type MasterMessagePage struct {
	Items   []CompanionMessage `json:"items"`
	Before  int64              `json:"before,omitempty"`
	HasMore bool               `json:"hasMore"`
}
