package main

// Ядро живёт, пока открыт хотя бы один Point.
//
// Расширение запускает ядро отвязанным: закрытие окна не должно рвать работу,
// а соседнее окно подхватывает то же ядро по дескриптору. Обратная сторона —
// сироты. Приложение закрыли, а ядра остались в диспетчере: убрать их может
// только окно Чертога, и только если оно закрывается последним, а при жёстком
// выходе deactivate и вовсе не успевает.
//
// Поэтому ядро само смотрит на хозяев. Каждое окно Point раз в пять секунд
// обновляет в каталоге runtime свою отметку host-<pid>.json, а окно,
// присоединённое к ядру, — ещё и аренду lease-*.json. Нет ни одной свежей
// отметки дольше ownerGrace — приложение закрыто, и ядро выходит тем же путём,
// что по сигналу. Идущий квест при следующем запуске встанет на паузу по
// контрольной точке (application.Startup), шаг посреди изменения получит
// «исход неизвестен» — так решено владельцем 27.09.2026.
//
// Наблюдение включает только расширение (POINT_OWNER_DIR): ядро, запущенное
// руками, стендом или тестом, хозяев не ждёт.

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// ownerFreshness — отметка моложе этого значит «окно живо». Окна
	// обновляют её раз в пять секунд, аренды считаются свежими столько же.
	ownerFreshness = 20 * time.Second
	// ownerGrace — сколько ядро ждёт без единой свежей отметки. Перезапуск
	// окна и смена проекта в Чертоге укладываются в это время с запасом.
	ownerGrace = 30 * time.Second
	ownerPoll  = 5 * time.Second
)

// ownersAlive — есть ли в каталоге свежая отметка хоть одного окна Point:
// host-*.json или аренда lease-*.json. Нечитаемый каталог хозяев не
// подтверждает — отсчёт grace идёт дальше.
func ownersAlive(dir string, now time.Time) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || !(strings.HasPrefix(name, "host-") || strings.HasPrefix(name, "lease-")) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var mark struct {
			UpdatedAt int64 `json:"updatedAt"`
		}
		if json.Unmarshal(raw, &mark) != nil || mark.UpdatedAt <= 0 {
			continue
		}
		age := now.Sub(time.UnixMilli(mark.UpdatedAt))
		if age < ownerFreshness && age > -ownerFreshness {
			return true
		}
	}
	return false
}

// ownerCheck — один шаг наблюдения: когда хозяин был виден последним и не
// пора ли выходить. Выход — только после grace подряд без свежих отметок:
// смена проекта в Чертоге на мгновение оставляет каталог без них.
func ownerCheck(dir string, current, lastSeen time.Time, grace time.Duration) (time.Time, bool) {
	if ownersAlive(dir, current) {
		return current, false
	}
	return lastSeen, current.Sub(lastSeen) >= grace
}

// watchOwners закрывает возвращённый канал, когда свежих отметок нет дольше
// grace подряд. Отсчёт начинается со старта: окну, которое ядро подняло,
// нужно время записать первую аренду.
func watchOwners(ctx context.Context, dir string, grace, poll time.Duration, now func() time.Time, logger *slog.Logger) <-chan struct{} {
	orphaned := make(chan struct{})
	go func() {
		lastSeen := now()
		ticker := time.NewTicker(poll)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var gone bool
				if lastSeen, gone = ownerCheck(dir, now(), lastSeen, grace); gone {
					logger.Info("no Point window alive, shutting down", "owner_dir", dir, "grace", grace.String())
					close(orphaned)
					return
				}
			}
		}
	}()
	return orphaned
}
