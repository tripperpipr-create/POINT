package agent

import (
	"errors"
	"sync"
	"time"
)

// activeClock measures only intervals when the agent is actually working
// (model stream + tools). Pause and approval wait do not consume budget.
type activeClock struct {
	mu               sync.Mutex
	originalBudgetMs int64
	budgetMs         int64
	elapsedMs        int64
	extensions       int
	segmentStart     time.Time
}

func newActiveClock(budgetSeconds int) *activeClock {
	if budgetSeconds <= 0 {
		return &activeClock{}
	}
	budgetMs := int64(budgetSeconds) * 1000
	return &activeClock{originalBudgetMs: budgetMs, budgetMs: budgetMs}
}

func (c *activeClock) enabled() bool {
	return c != nil && c.budgetMs > 0
}

func (c *activeClock) start() {
	if !c.enabled() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.segmentStart.IsZero() {
		c.segmentStart = time.Now()
	}
}

func (c *activeClock) stop() {
	if !c.enabled() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.segmentStart.IsZero() {
		return
	}
	c.elapsedMs += time.Since(c.segmentStart).Milliseconds()
	c.segmentStart = time.Time{}
}

func (c *activeClock) snapshot() (elapsedMs int64, extensions int) {
	if c == nil {
		return 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	elapsed := c.elapsedMs
	if !c.segmentStart.IsZero() {
		elapsed += time.Since(c.segmentStart).Milliseconds()
	}
	return elapsed, c.extensions
}

func (c *activeClock) restore(elapsedMs int64, extensions int, budgetSeconds int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.elapsedMs = elapsedMs
	c.extensions = extensions
	c.segmentStart = time.Time{}
	if budgetSeconds > 0 {
		c.originalBudgetMs = int64(budgetSeconds) * 1000
		c.budgetMs = c.originalBudgetMs * int64(extensions+1)
	}
}

func (c *activeClock) exhausted() bool {
	if !c.enabled() {
		return false
	}
	elapsed, _ := c.snapshot()
	c.mu.Lock()
	budget := c.budgetMs
	c.mu.Unlock()
	return elapsed >= budget
}

func (c *activeClock) remainingMs() int64 {
	if !c.enabled() {
		return 0
	}
	elapsed, _ := c.snapshot()
	c.mu.Lock()
	budget := c.budgetMs
	c.mu.Unlock()
	left := budget - elapsed
	if left < 0 {
		return 0
	}
	return left
}

// extend grants one additional budget equal to the original ActiveSeconds.
// A second grant without a new task approval is rejected.
func (c *activeClock) extend() error {
	if !c.enabled() {
		return errors.New("active time budget is not enabled for this run")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.extensions >= 1 {
		return errors.New("active time may be extended only once without a new task approval")
	}
	c.budgetMs += c.originalBudgetMs
	c.extensions++
	return nil
}

func (c *activeClock) budgetSeconds() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.originalBudgetMs <= 0 {
		return 0
	}
	return int(c.originalBudgetMs / 1000)
}

func (c *activeClock) totalBudgetSeconds() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.budgetMs <= 0 {
		return 0
	}
	return int(c.budgetMs / 1000)
}

func (c *activeClock) extensionCount() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.extensions
}
