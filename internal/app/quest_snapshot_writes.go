package app

import (
	"context"
	"errors"
	"log/slog"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/storage"
)

// saveLoadedQuest пишет снимок квеста, только если статус в базе всё ещё
// loaded — тот, с которым квест был прочитан. Между чтением и записью человек
// успевает отменить квест, а шлюз — вынести вердикт; SaveQuest полным
// снимком возвращал такой квест в прежний статус, и вкладки снова показывали
// его активным (Q01). Проигравшая запись не повторяется: решение, принятое
// раньше, сильнее снимка, снятого до него.
func (a *App) saveLoadedQuest(ctx context.Context, quest domain.Quest, loaded domain.QuestStatus, what string) error {
	err := a.store.SaveQuestIfStatusV2(ctx, quest, loaded)
	if errors.Is(err, storage.ErrQuestStatusChanged) {
		slog.Info("quest snapshot skipped: status changed meanwhile", "quest_id", quest.ID, "write", what, "loaded", loaded)
	} else if err != nil {
		slog.Warn("quest snapshot not persisted", "quest_id", quest.ID, "write", what, "error", err)
	}
	return err
}
