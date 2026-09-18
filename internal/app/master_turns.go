package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
	"os"
	"strings"
	"time"
)

func (a *App) StartMasterTurnV2(ctx context.Context, req MasterTurnV2Request) (domain.MasterTurn, error) {
	currentWorkspaceID := a.currentWorldID()
	if req.WorkspaceID != "" && req.WorkspaceID != currentWorkspaceID {
		return domain.MasterTurn{}, errors.New("requested project is not the open project")
	}
	if len(req.Sources) > 16 {
		return domain.MasterTurn{}, errors.New("a master turn accepts at most 16 source snapshots")
	}
	attachments := make([]domain.MasterAttachment, 0, len(req.Sources))
	sources := make([]domain.SourceSnapshotRef, 0, len(req.Sources))
	for _, ref := range req.Sources {
		snapshot, err := a.store.GetSourceSnapshotV2(ctx, ref.ID)
		if err != nil {
			return domain.MasterTurn{}, fmt.Errorf("load source snapshot %q: %w", ref.ID, err)
		}
		if ref.Digest != "" && ref.Digest != snapshot.Digest {
			return domain.MasterTurn{}, fmt.Errorf("source snapshot %q digest mismatch", ref.ID)
		}
		if snapshot.WorkspaceID != "" && snapshot.WorkspaceID != currentWorkspaceID {
			return domain.MasterTurn{}, fmt.Errorf("source snapshot %q belongs to another project", ref.ID)
		}
		attachment := domain.MasterAttachment{ID: snapshot.ID, Name: snapshot.Label, Kind: "document", Content: snapshot.ExtractedText, MIME: snapshot.MediaType, SHA256: snapshot.Digest}
		if snapshot.Kind == "image" || strings.HasPrefix(snapshot.MediaType, "image/") {
			raw, readErr := os.ReadFile(snapshot.StoragePath)
			if readErr != nil {
				return domain.MasterTurn{}, fmt.Errorf("read image snapshot %q: %w", ref.ID, readErr)
			}
			attachment.Kind, attachment.Content = "image", base64.StdEncoding.EncodeToString(raw)
		}
		attachments = append(attachments, attachment)
		sources = append(sources, domain.SourceSnapshotRef{
			ID: snapshot.ID, Kind: snapshot.Kind, Label: snapshot.Label,
			Locator: snapshot.CanonicalURL, Digest: snapshot.Digest, MediaType: snapshot.MediaType,
		})
	}
	return a.startMasterTurn(ctx, MasterChatRequest{
		TurnID: req.TurnID, ConversationID: req.ConversationID, Message: req.Message,
		Attachments: attachments, Model: req.Model, TaskIntake: req.TaskIntake,
		ProposalID: req.ProposalID, APIKey: req.APIKey,
		PreviousAnswerRejected: req.PreviousAnswerRejected,
	}, func(finishCtx context.Context, result MasterChatView) (string, error) {
		return a.saveMasterWorkOrderV2(finishCtx, result.Response.Proposal, sources, result.Sessions.Active, result.Response.AgentDraft)
	})
}

// masterTurnGrace — запас поверх бюджета модели: за него ход успевает записать
// откат и причину. Без запаса срок ядра совпал бы с моделью, и разбор сгорел бы
// вместе с оборванным запросом.
const masterTurnGrace = 30 * time.Second

// masterTurnDeadline — срок хода целиком. Считается от бюджета модели, чтобы
// потолок ядра нельзя было случайно выставить короче.
func masterTurnDeadline(cfg domain.OrchestratorConfig) time.Duration {
	return orchestrator.MasterTurnBudget(cfg) + masterTurnGrace
}

func (a *App) StartMasterTurn(ctx context.Context, req MasterChatRequest) (domain.MasterTurn, error) {
	return a.startMasterTurn(ctx, req, nil)
}

type masterTurnCompletion func(context.Context, MasterChatView) (string, error)

func (a *App) startMasterTurn(ctx context.Context, req MasterChatRequest, complete masterTurnCompletion) (domain.MasterTurn, error) {
	if strings.TrimSpace(req.Message) == "" || len(req.Message) > 32768 {
		return domain.MasterTurn{}, errors.New("сообщение должно содержать от 1 до 32768 байт")
	}
	if req.TurnID == "" {
		req.TurnID = domain.NewID("turn")
	}
	if len(req.TurnID) > 128 {
		return domain.MasterTurn{}, errors.New("неверный идентификатор хода")
	}
	w := a.currentWorldID()
	cfg, err := a.masterConfig(ctx, w)
	if err != nil {
		return domain.MasterTurn{}, err
	}
	if req.Model != "" {
		cfg.Model = req.Model
	}
	briefing := a.masterProjectFacts(ctx)
	service, sessions, err := a.sessionMasterService(ctx, a.masterChatService(ctx, briefing), req.ConversationID)
	if err != nil {
		return domain.MasterTurn{}, err
	}
	req.ConversationID = sessions.Active
	if req.Model == "" {
		req.Model = sessions.Model
	}
	if req.Model != "" {
		cfg.Model = req.Model
	}
	if _, _, _, err = masterAttachments(req.Attachments, cfg, a.masterAttachmentReference(ctx, cfg)); err != nil {
		return domain.MasterTurn{}, err
	}
	hashReq := req
	hashReq.APIKey = ""
	raw, _ := json.Marshal(hashReq)
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	a.masterTurnsMu.Lock()
	defer a.masterTurnsMu.Unlock()
	if a.masterTurnsStopping {
		return domain.MasterTurn{}, errors.New("ядро останавливается")
	}
	existing, err := a.store.MasterTurn(ctx, w, req.TurnID)
	if err == nil {
		if existing.RequestHash != hash {
			return existing, errors.New("идентификатор уже принадлежит другому запросу")
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return existing, err
	}
	turn := domain.MasterTurn{ID: req.TurnID, ConversationID: req.ConversationID, WorkspaceID: w, Status: "preparing", RequestHash: hash}
	if err = a.store.SaveMasterTurn(ctx, turn); err != nil {
		return turn, errors.New("в этом разговоре уже идёт ответ; дождитесь его или остановите")
	}
	// Срок хода берётся из бюджета Мастера, а не из круглого числа. Свой
	// потолок в пять минут был меньше этого бюджета и всегда срабатывал первым:
	// поток к модели обрывался на середине ответа, и ход записывался как
	// остановленный человеком.
	budget := masterTurnDeadline(cfg)
	runCtx, cancel := context.WithTimeout(context.Background(), budget)
	if a.masterTurnCancels == nil {
		a.masterTurnCancels = map[string]context.CancelFunc{}
	}
	key := w + "/" + turn.ID
	a.masterTurnCancels[key] = cancel
	a.masterTurnsWG.Add(1)
	go func() {
		defer a.masterTurnsWG.Done()
		defer cancel()
		defer func() { a.masterTurnsMu.Lock(); delete(a.masterTurnCancels, key); a.masterTurnsMu.Unlock() }()
		emitDetail := func(kind, text, detail string) {
			_, _ = a.store.AppendMasterEvent(context.Background(), w, domain.MasterTurnEvent{TurnID: turn.ID, ConversationID: turn.ConversationID, Type: kind, Text: text, Detail: detail})
		}
		emit := func(kind, text string) { emitDetail(kind, text, "") }
		service.OnProgress = func(kind, text, detail string) {
			switch kind {
			case "reply":
				if text == turn.Reply {
					return
				}
				turn.Reply = text
				turn.Status = "streaming"
			case "reasoning":
				// Размышление не меняет состояние хода: оно идёт и в ожидании модели,
				// и между обращениями к инструментам. Событие уходит в ленту, а строка
				// состояния и запись хода остаются прежними — иначе каждая мысль стоила
				// бы записи в таблицу ходов.
				emitDetail(kind, text, detail)
				return
			default:
				turn.Status = "tools"
			}
			_ = a.store.SaveMasterTurn(context.Background(), turn)
			emitDetail(kind, text, detail)
		}
		turn.Status = "waiting"
		_ = a.store.SaveMasterTurn(context.Background(), turn)
		emit("status", "waiting")
		result, runErr := a.masterChatPrepared(runCtx, req, w, cfg, briefing, service, sessions)
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			// Срок и остановка человеком — разные события. Пока оба назывались
			// «Ответ остановлен», молчание модели выглядело как чужое действие,
			// и менять было нечего: ни срока, ни причины на экране.
			turn.Status = "failed"
			turn.Error = fmt.Sprintf("Мастер не ответил за %d мин: модель не уложилась в срок хода", int(budget.Minutes()))
		} else if runCtx.Err() != nil {
			turn.Status = "cancelled"
			turn.Error = "Ответ остановлен"
		} else if runErr != nil {
			turn.Status = "failed"
			turn.Error = runErr.Error()
		} else {
			turn.Status = "completed"
			turn.Reply = result.Response.Reply
			if result.Response.FallbackReason != "" {
				turn.Status = "failed"
				turn.Error = result.Response.FallbackReason
			} else if complete != nil {
				workOrderID, completeErr := complete(context.Background(), result)
				if completeErr != nil {
					turn.Status = "failed"
					turn.Error = "не удалось подготовить карточку запуска: " + completeErr.Error()
				} else if workOrderID != "" {
					turn.WorkOrderID = workOrderID
					emit("work_order", workOrderID)
				}
			}
		}
		if turn.Status == "cancelled" || runErr != nil {
			_ = service.Store.SaveCompanionMessage(context.Background(), domain.CompanionMessage{ID: domain.NewID("msg"), WorkspaceID: w, ConversationID: turn.ConversationID, TurnID: turn.ID, Speaker: "master", Role: "assistant", Content: turn.Reply, Mode: turn.Status, FallbackReason: turn.Error, CreatedAt: time.Now()})
		}
		_ = a.store.SaveMasterTurn(context.Background(), turn)
		emit("done", turn.Status)
	}()
	return turn, nil
}
func (a *App) CancelMasterTurn(ctx context.Context, id string) error {
	w := a.currentWorldID()
	if _, err := a.store.MasterTurn(ctx, w, id); err != nil {
		return err
	}
	a.masterTurnsMu.Lock()
	defer a.masterTurnsMu.Unlock()
	if cancel := a.masterTurnCancels[w+"/"+id]; cancel != nil {
		cancel()
	}
	return nil
}
func (a *App) MasterTurn(ctx context.Context, id string) (domain.MasterTurn, error) {
	return a.store.MasterTurn(ctx, a.currentWorldID(), id)
}
func (a *App) MasterTurnEvents(ctx context.Context, id string, after int64) ([]domain.MasterTurnEvent, error) {
	return a.store.MasterEvents(ctx, a.currentWorldID(), id, after)
}

// Bind an established stream to its original workspace even if the Hub switches projects.
func (a *App) MasterTurnStream(ctx context.Context, turn domain.MasterTurn, after int64) ([]domain.MasterTurnEvent, domain.MasterTurn, error) {
	current, err := a.store.MasterTurn(ctx, turn.WorkspaceID, turn.ID)
	if err != nil {
		return nil, current, err
	}
	events, err := a.store.MasterEvents(ctx, turn.WorkspaceID, turn.ID, after)
	return events, current, err
}
func (a *App) stopMasterTurns() {
	a.masterTurnsMu.Lock()
	a.masterTurnsStopping = true
	for _, cancel := range a.masterTurnCancels {
		cancel()
	}
	a.masterTurnsMu.Unlock()
	a.masterTurnsWG.Wait()
}
