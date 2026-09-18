package providers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	maxStreamAttempts = 3
	baseRetryDelay    = 250 * time.Millisecond
	maxRetryDelay     = 30 * time.Second
)

func retryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusConflict || status == http.StatusTooManyRequests || status >= 500
}

func retryDelay(retryAfter string, failedAttempt int, now time.Time) time.Duration {
	retryAfter = strings.TrimSpace(retryAfter)
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds >= 0 {
		return minDuration(time.Duration(seconds)*time.Second, maxRetryDelay)
	}
	if at, err := http.ParseTime(retryAfter); err == nil {
		return minDuration(maxDuration(0, at.Sub(now)), maxRetryDelay)
	}
	delay := baseRetryDelay
	for attempt := 1; attempt < failedAttempt; attempt++ {
		delay *= 2
		if delay >= maxRetryDelay {
			return maxRetryDelay
		}
	}
	return delay
}

func announceRetry(ctx context.Context, onEvent func(ModelEvent) error, failedAttempt int, delay time.Duration, message string) error {
	if err := onEvent(ModelEvent{Kind: EventRetry, Attempt: failedAttempt + 1, DelayMs: delay.Milliseconds(), Message: message}); err != nil {
		return err
	}
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

func maxDuration(left, right time.Duration) time.Duration {
	if left > right {
		return left
	}
	return right
}

// Сколько времени должно остаться попытке, чтобы она имела смысл. Провайдеру,
// не успевшему даже прислать заголовки, следующая попытка нужна целиком:
// начатая на остатке бюджета, она умрёт вместе с оборванным запросом и унесёт
// с собой время, которого не хватит уже на местный откат.
const minAttemptBudget = 5 * time.Second

// shouldRetry отвечает, начинать ли следующую попытку. Кроме числа попыток и
// отмены он смотрит на срок контекста: у ожидания есть край, и попытка, которая
// заведомо в него не влезает, — это не вторая попытка, а потерянный ответ.
func shouldRetry(ctx context.Context, failedAttempt int, delay time.Duration) bool {
	if failedAttempt >= maxStreamAttempts || ctx.Err() != nil {
		return false
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return true
	}
	return time.Until(deadline) >= delay+minAttemptBudget
}
