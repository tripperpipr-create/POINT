package app

import (
	"time"

	"local-agent-workbench/internal/domain"
)

// Живой ход будит свой поток событий сам, а не ждёт опроса.
//
// Поток SSE опрашивал SQLite каждые 25 мс — два запроса на опрос и на каждого
// подключённого клиента, всё время хода, даже когда модель минуту думала
// молча. Теперь новое событие закрывает канал ожидания, и поток читает базу
// ровно тогда, когда в ней что-то появилось. База остаётся источником истины:
// переподключение по Last-Event-ID читает её как раньше, а редкий запасной
// опрос страхует от пропущенного сигнала.
const masterTurnFallbackPoll = time.Second

// MasterTurnSignal отдаёт канал, который закроется при следующем событии хода.
// Для хода, который уже не идёт, канала нет: ждать нечего, и поток прочтёт
// итог запасным опросом.
func (a *App) MasterTurnSignal(turn domain.MasterTurn) <-chan struct{} {
	key := turn.WorkspaceID + "/" + turn.ID
	a.masterTurnsMu.Lock()
	defer a.masterTurnsMu.Unlock()
	if _, live := a.masterTurnCancels[key]; !live {
		return nil
	}
	if a.masterTurnSignals == nil {
		a.masterTurnSignals = map[string]chan struct{}{}
	}
	signal, ok := a.masterTurnSignals[key]
	if !ok {
		signal = make(chan struct{})
		a.masterTurnSignals[key] = signal
	}
	return signal
}

// MasterTurnFallbackPoll — как часто поток перечитывает базу без сигнала.
func (a *App) MasterTurnFallbackPoll() time.Duration { return masterTurnFallbackPoll }

func (a *App) notifyMasterTurn(key string) {
	a.masterTurnsMu.Lock()
	defer a.masterTurnsMu.Unlock()
	if signal, ok := a.masterTurnSignals[key]; ok {
		close(signal)
		delete(a.masterTurnSignals, key)
	}
}

// Сколько раз в секунду ход записывает растущий ответ. Каждая дельта модели
// писала в базу весь ответ целиком дважды — в ход и в журнал событий, — и
// длинный ответ стоил тысяч записей растущего текста. Десяти раз в секунду
// глазу хватает, а дописанный хвост уходит с ближайшим другим событием хода
// или с его итогом.
const masterReplySaveInterval = 100 * time.Millisecond
