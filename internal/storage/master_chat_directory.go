package storage

import (
	"context"
	"local-agent-workbench/internal/domain"
)

// Каталог разговоров мастера по всем мирам сразу.
//
// Чертог стал домом, и его левая панель показывает чаты не одного проекта, а
// всех. Обычный MasterConversations этого дать не может: он параметризован
// текущим миром, и на нём держится вся изоляция мастера. Поэтому каталог —
// отдельный запрос, сознательно кросс-мировой и сознательно бедный: только
// метаданные, ни одной реплики.
//
// Прецедент не новый: PurgeTemporaryMasterConversations и InterruptMasterTurns
// ходят по всем мирам по тем же таблицам.
//
// Признак «идёт ответ» берётся подзапросом EXISTS, а не отдельным кросс-мировым
// аналогом MasterActiveTurns: перечисленные статусы — ровно те, что описаны в
// частичном уникальном индексе master_one_active_turn, так что подзапрос
// попадает в индекс и стоит одного обращения.
const masterDirectoryQuery = `
SELECT w.id, w.name, w.path, c.id, c.title, c.updated_at, c.pinned,
  EXISTS(SELECT 1 FROM master_turns t
         WHERE t.workspace_id = c.workspace_id AND t.conversation_id = c.id
           AND t.status IN ('preparing','waiting','streaming','tools')) AS running
FROM master_conversations c
JOIN workspaces w ON w.id = c.workspace_id
WHERE c.temporary = 0 AND c.archived = 0
ORDER BY c.updated_at DESC, c.id
LIMIT ?`

func (s *SQLite) MasterConversationDirectory(ctx context.Context, limit int) ([]domain.MasterConversationRef, error) {
	if limit <= 0 {
		limit = 300
	}
	rows, err := s.db.QueryContext(ctx, masterDirectoryQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.MasterConversationRef{}
	for rows.Next() {
		v := domain.MasterConversationRef{}
		if err = rows.Scan(&v.WorkspaceID, &v.WorkspaceName, &v.WorkspacePath, &v.ID, &v.Title, &v.UpdatedAt, &v.Pinned, &v.Running); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
