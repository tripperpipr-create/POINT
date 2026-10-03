package orchestrator

import (
	"context"
	"log/slog"
	"time"
)

// masterPrefetchTimeout — сколько ход ждёт подсказку индекса. Подсказка
// экономит первые круги поиска (по ~40 с каждый), но ради неё ход не ждёт:
// индекс либо готов и отвечает за доли секунды, либо подсказки нет.
const masterPrefetchTimeout = 2 * time.Second

// prefetchCode отдаёт код проекта, найденный готовым индексом по реплике
// человека, до первого круга. Замер 25.09–02.10: Мастер начинал почти каждый
// ход с search_text/list_files, и каждый такой круг стоил ~40 с. Сбой или
// задержка поставщика ход не роняют — круг идёт без подсказки.
func (s ChatService) prefetchCode(ctx context.Context, message string) string {
	if s.Prefetch == nil || message == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, masterPrefetchTimeout)
	defer cancel()
	found := make(chan string, 1)
	go func() {
		defer func() {
			if recover() != nil {
				found <- ""
			}
		}()
		found <- s.Prefetch(ctx, message)
	}()
	select {
	case code := <-found:
		return code
	case <-ctx.Done():
		slog.Info("master prefetch timed out", "timeout_ms", masterPrefetchTimeout.Milliseconds())
		return ""
	}
}
