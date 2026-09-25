package mcpclient

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Resolver собирает конфигурацию сервера перед запуском: команду и отпечаток
// доверия — из хранилища, секреты — из памяти ядра, окружение —
// ChildEnvironment. Отказ — *Error рода NotTrusted или SecretLocked: это не
// сбой сервера, и в счёт сбоев он не идёт.
type Resolver func(ctx context.Context, id string) (Config, error)

// SupervisorOptions — пороги надзора. Нулевые значения заменяются умолчаниями.
type SupervisorOptions struct {
	Idle          time.Duration
	MaxConcurrent int
	BackoffMin    time.Duration
	BackoffMax    time.Duration
	FailureWindow time.Duration
	MaxFailures   int
	ReapEvery     time.Duration
	Now           func() time.Time
	Dial          func(context.Context, Config) (*Client, error)
}

// Status — состояние сервера для экрана «Интеграции».
type Status struct {
	Running   bool       `json:"running"`
	Starting  bool       `json:"starting"`
	Broken    bool       `json:"broken"`
	Failures  int        `json:"failures"`
	NextStart time.Time  `json:"nextStart,omitempty"`
	LastError error      `json:"-"`
	Info      ServerInfo `json:"info"`
	Stale     bool       `json:"stale"`
}

// Supervisor держит живые соединения ядра с MCP-серверами.
//
// Сервер запускается по первому обращению, гасится после простоя и
// поднимается снова по следующему. Упавший сервер поднимается с нарастающей
// паузой; после серии сбоев подряд он остаётся выключенным, пока владелец не
// нажмёт «Перезапустить» (Reset). Надзор свой у каждого ядра: тёплые ядра
// процессы не делят.
type Supervisor struct {
	resolve Resolver
	opts    SupervisorOptions

	mu      sync.Mutex
	servers map[string]*slot

	stop     chan struct{}
	stopOnce sync.Once
}

type slot struct {
	client    *Client
	starting  chan struct{}
	failures  []time.Time
	nextStart time.Time
	broken    bool
	lastErr   error
	lastLog   string
	lastUsed  time.Time
	active    int
	sem       chan struct{}
}

// NewSupervisor создаёт надзор и запускает уборку простаивающих серверов.
func NewSupervisor(resolve Resolver, opts SupervisorOptions) *Supervisor {
	if opts.Idle <= 0 {
		opts.Idle = 10 * time.Minute
	}
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = 4
	}
	if opts.BackoffMin <= 0 {
		opts.BackoffMin = time.Second
	}
	if opts.BackoffMax <= 0 {
		opts.BackoffMax = time.Minute
	}
	if opts.FailureWindow <= 0 {
		opts.FailureWindow = 5 * time.Minute
	}
	if opts.MaxFailures <= 0 {
		opts.MaxFailures = 5
	}
	if opts.ReapEvery <= 0 {
		opts.ReapEvery = 30 * time.Second
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Dial == nil {
		opts.Dial = Dial
	}
	s := &Supervisor{resolve: resolve, opts: opts, servers: map[string]*slot{}, stop: make(chan struct{})}
	go s.reap()
	return s
}

// Call выполняет fn с живым клиентом сервера id, поднимая его при нужде.
func (s *Supervisor) Call(ctx context.Context, id string, fn func(*Client) error) error {
	client, release, err := s.acquire(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	return fn(client)
}

// Probe — «Проверить» владельца: сервер поднимается заново с текущей
// конфигурацией, счёт сбоев сбрасывается, список инструментов читается свежим.
func (s *Supervisor) Probe(ctx context.Context, id string) (ServerInfo, []Tool, error) {
	s.Stop(id)
	s.Reset(id)
	var info ServerInfo
	var tools []Tool
	err := s.Call(ctx, id, func(client *Client) error {
		info = client.Info()
		var listErr error
		tools, listErr = client.ListTools(ctx)
		return listErr
	})
	return info, tools, err
}

func (s *Supervisor) slotFor(id string) *slot {
	sl := s.servers[id]
	if sl == nil {
		sl = &slot{sem: make(chan struct{}, s.opts.MaxConcurrent)}
		s.servers[id] = sl
	}
	return sl
}

func (s *Supervisor) acquire(ctx context.Context, id string) (*Client, func(), error) {
	for {
		s.mu.Lock()
		sl := s.slotFor(id)
		if sl.client != nil && closed(sl.client.Done()) {
			sl.lastLog = sl.client.Log()
			sl.lastErr = &Error{Kind: KindExited, Detail: "server process exited", Stderr: sl.client.log.Tail(4 << 10)}
			sl.client = nil
			s.recordFailure(sl)
		}
		if sl.broken {
			err := s.unavailable(sl)
			s.mu.Unlock()
			return nil, nil, err
		}
		if sl.client != nil {
			client := sl.client
			sl.active++
			sl.lastUsed = s.opts.Now()
			s.mu.Unlock()
			select {
			case sl.sem <- struct{}{}:
			case <-ctx.Done():
				s.release(sl)
				return nil, nil, newError(KindTimeout, ctx.Err(), "server is busy")
			}
			return client, func() { <-sl.sem; s.release(sl) }, nil
		}
		if sl.starting != nil {
			wait := sl.starting
			s.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, nil, newError(KindTimeout, ctx.Err(), "server is still starting")
			}
		}
		if delay := sl.nextStart.Sub(s.opts.Now()); delay > 0 {
			// Короткую паузу подождать дешевле, чем показать ошибку; длинная —
			// честный отказ с временем следующей попытки.
			if delay > 2*time.Second {
				err := &Error{Kind: KindUnavailable, Detail: fmt.Sprintf("server failed recently; next start in %s", delay.Round(time.Second)), err: sl.lastErr}
				s.mu.Unlock()
				return nil, nil, err
			}
			s.mu.Unlock()
			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return nil, nil, newError(KindTimeout, ctx.Err(), "waiting for restart")
			}
		}
		sl.starting = make(chan struct{})
		s.mu.Unlock()

		client, err := s.start(ctx, id)

		s.mu.Lock()
		close(sl.starting)
		sl.starting = nil
		if err != nil {
			sl.lastErr = err
			if kind := KindOf(err); kind != KindNotTrusted && kind != KindSecretLocked {
				var typed *Error
				if errors.As(err, &typed) && typed.Stderr != "" {
					sl.lastLog = typed.Stderr
				}
				s.recordFailure(sl)
			}
			s.mu.Unlock()
			return nil, nil, err
		}
		sl.client = client
		sl.lastErr = nil
		s.mu.Unlock()
	}
}

func (s *Supervisor) start(ctx context.Context, id string) (*Client, error) {
	cfg, err := s.resolve(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.opts.Dial(ctx, cfg)
}

func (s *Supervisor) release(sl *slot) {
	s.mu.Lock()
	sl.active--
	sl.lastUsed = s.opts.Now()
	s.mu.Unlock()
}

// recordFailure — под замком: сбой в окне, пауза перед следующим запуском,
// а после MaxFailures — выключение до владельца.
func (s *Supervisor) recordFailure(sl *slot) {
	now := s.opts.Now()
	kept := sl.failures[:0]
	for _, at := range sl.failures {
		if now.Sub(at) < s.opts.FailureWindow {
			kept = append(kept, at)
		}
	}
	sl.failures = append(kept, now)
	if len(sl.failures) >= s.opts.MaxFailures {
		sl.broken = true
		return
	}
	backoff := s.opts.BackoffMin << (len(sl.failures) - 1)
	if backoff > s.opts.BackoffMax || backoff <= 0 {
		backoff = s.opts.BackoffMax
	}
	sl.nextStart = now.Add(backoff)
}

func (s *Supervisor) unavailable(sl *slot) error {
	detail := fmt.Sprintf("server failed %d times in a row and is stopped until restarted", len(sl.failures))
	if sl.lastErr != nil {
		detail += ": " + sl.lastErr.Error()
	}
	return &Error{Kind: KindUnavailable, Detail: detail, err: sl.lastErr}
}

// Reset — владелец просит попробовать снова: счёт сбоев и пауза сбрасываются.
func (s *Supervisor) Reset(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sl := s.servers[id]; sl != nil {
		sl.broken = false
		sl.failures = nil
		sl.nextStart = time.Time{}
		sl.lastErr = nil
	}
}

// Stop гасит сервер. Следующее обращение поднимет его с новой конфигурацией:
// так применяется правка сервера владельцем.
func (s *Supervisor) Stop(id string) {
	s.mu.Lock()
	var client *Client
	if sl := s.servers[id]; sl != nil && sl.client != nil {
		client = sl.client
		sl.lastLog = client.Log()
		sl.client = nil
	}
	s.mu.Unlock()
	if client != nil {
		_ = client.Close()
	}
}

// Forget гасит сервер и забывает его — сервер удалён.
func (s *Supervisor) Forget(id string) {
	s.Stop(id)
	s.mu.Lock()
	delete(s.servers, id)
	s.mu.Unlock()
}

// StopAll гасит всё — ядро завершается.
func (s *Supervisor) StopAll() {
	s.stopOnce.Do(func() { close(s.stop) })
	s.mu.Lock()
	clients := make([]*Client, 0, len(s.servers))
	for _, sl := range s.servers {
		if sl.client != nil {
			clients = append(clients, sl.client)
			sl.client = nil
		}
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, client := range clients {
		wg.Add(1)
		go func(client *Client) {
			defer wg.Done()
			_ = client.Close()
		}(client)
	}
	wg.Wait()
}

// Status — состояние сервера для экрана.
func (s *Supervisor) Status(id string) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	sl := s.servers[id]
	if sl == nil {
		return Status{}
	}
	status := Status{
		Starting:  sl.starting != nil,
		Broken:    sl.broken,
		Failures:  len(sl.failures),
		NextStart: sl.nextStart,
		LastError: sl.lastErr,
	}
	if sl.client != nil && !closed(sl.client.Done()) {
		status.Running = true
		status.Info = sl.client.Info()
		status.Stale = sl.client.Stale()
	}
	return status
}

// Log — зачищенный журнал сервера: живого или последнего упавшего.
func (s *Supervisor) Log(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sl := s.servers[id]
	if sl == nil {
		return ""
	}
	if sl.client != nil {
		return sl.client.Log()
	}
	return sl.lastLog
}

func (s *Supervisor) reap() {
	ticker := time.NewTicker(s.opts.ReapEvery)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.reapIdle()
		}
	}
}

func (s *Supervisor) reapIdle() {
	now := s.opts.Now()
	s.mu.Lock()
	var idle []*Client
	for _, sl := range s.servers {
		if sl.client != nil && sl.active == 0 && now.Sub(sl.lastUsed) >= s.opts.Idle {
			sl.lastLog = sl.client.Log()
			idle = append(idle, sl.client)
			sl.client = nil
		}
	}
	s.mu.Unlock()
	for _, client := range idle {
		_ = client.Close()
	}
}

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
