package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
)

// Каталог чатов Чертога: разговоры всех миров, сгруппированные по проектам.
//
// Это осознанное послабление правила «одно ядро — один мир». Левая панель
// Чертога показывает чаты всех проектов, а ядро поднимается только с открытой
// папкой; спросить каталог больше не у кого. Послабление стоит рядом с уже
// существующим (guardAgentImprovementWorld) и ограничено так же жёстко:
//
//   - только чтение и только метаданные: заголовок, время, признак хода. Ни
//     одной реплики, ни summary, ни путей внутри проектов;
//   - ни requireWorkspace, ни guardWorld — иначе метод не сработает в том самом
//     окне старта, ради которого он и написан;
//   - currentWorkspace не трогается, master.active.<мир> не переписывается.
//     Выбор чата в чужом мире делают уже после переключения, обычным
//     UpdateMasterSession в своём мире.
//
// Инвариант «есть ровно один текущий мир», на котором стоят все 18 вызовов
// guardWorld, этим методом не ослабляется: он ничего не открывает и ничего не
// решает — он рассказывает.
const (
	masterDirectoryChatLimit  = 300
	masterDirectoryWorldLimit = 40
)

type MasterChatDirectoryWorld struct {
	WorkspaceID string                         `json:"workspaceId"`
	Name        string                         `json:"name"`
	Hash        string                         `json:"hash"`
	Path        string                         `json:"path,omitempty"`
	Current     bool                           `json:"current"`
	Chats       []domain.MasterConversationRef `json:"chats"`
}

type MasterChatDirectory struct {
	CurrentWorkspaceID string                     `json:"currentWorkspaceId"`
	Worlds             []MasterChatDirectoryWorld `json:"worlds"`
	Truncated          bool                       `json:"truncated"`
}

// Отпечаток пути мира. Повторяет normalizedWorkspaceRoot + sha256 из расширения
// (project-registry.js, runtimePaths в extension.js), чтобы хост сшил строку
// каталога со своей записью реестра и взял путь оттуда, а не из ядра.
func workspacePathHash(path string) string {
	normalized := filepath.Clean(path)
	if absolute, err := filepath.Abs(path); err == nil {
		normalized = absolute
	}
	if runtime.GOOS == "windows" {
		normalized = strings.ToLower(normalized)
	}
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])[:24]
}

func (a *App) MasterChatDirectory(ctx context.Context) (MasterChatDirectory, error) {
	current := a.currentWorldID()
	rows, err := a.store.MasterConversationDirectory(ctx, masterDirectoryChatLimit+1)
	if err != nil {
		return MasterChatDirectory{}, err
	}
	out := MasterChatDirectory{CurrentWorkspaceID: current, Worlds: []MasterChatDirectoryWorld{}}
	if len(rows) > masterDirectoryChatLimit {
		rows = rows[:masterDirectoryChatLimit]
		out.Truncated = true
	}
	index := map[string]int{}
	for _, row := range rows {
		position, known := index[row.WorkspaceID]
		if !known {
			world := MasterChatDirectoryWorld{
				WorkspaceID: row.WorkspaceID,
				Name:        row.WorkspaceName,
				Hash:        workspacePathHash(row.WorkspacePath),
				Current:     row.WorkspaceID == current && current != "",
			}
			// Путь наружу отдаётся только для открытого мира. Чужие проекты
			// опознаются отпечатком: путь для них хост берёт из своего реестра,
			// и открыть можно только то, что реестр уже знает.
			if world.Current {
				world.Path = row.WorkspacePath
			}
			position = len(out.Worlds)
			index[row.WorkspaceID] = position
			out.Worlds = append(out.Worlds, world)
		}
		chat := row
		if !out.Worlds[position].Current {
			chat.WorkspacePath = ""
		}
		chat.WorkspaceHash = out.Worlds[position].Hash
		out.Worlds[position].Chats = append(out.Worlds[position].Chats, chat)
	}
	// Запрос уже отсортирован по времени, поэтому порядок миров — это порядок их
	// первого (самого свежего) чата. Остаётся поднять текущий мир наверх: с него
	// человек и смотрит.
	sort.SliceStable(out.Worlds, func(left, right int) bool {
		return out.Worlds[left].Current && !out.Worlds[right].Current
	})
	if len(out.Worlds) > masterDirectoryWorldLimit {
		out.Worlds = out.Worlds[:masterDirectoryWorldLimit]
		out.Truncated = true
	}
	return out, nil
}
