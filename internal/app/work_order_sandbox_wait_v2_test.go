package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
)

// dockerOffSandbox answers like a container backend that started without its
// daemon, until the test starts "Docker".
type dockerOffSandbox struct {
	*sandbox.Manager
	up atomic.Bool
}

func (b *dockerOffSandbox) Recheck(context.Context) error {
	if b.up.Load() {
		return nil
	}
	return fmt.Errorf("%w: Docker недоступен — запустите Docker Desktop и повторите запуск: pipe not found", sandbox.ErrUnavailable)
}

func (b *dockerOffSandbox) Capabilities() sandbox.Capabilities {
	capabilities := b.Manager.Capabilities()
	if !b.up.Load() {
		capabilities.Unavailable = "Docker недоступен — запустите Docker Desktop и повторите запуск"
	}
	return capabilities
}

// Live run 26.09: «Утвердить» with Docker stopped spent 108 s on a plan, the
// launch then failed on the sandbox, and the quest stayed «выполняется» with
// not a line in the feed. It now waits for Docker in the open and is handed
// back for continuation once the daemon answers.
func TestLaunchWithoutDockerWaitsInTheOpenAndIsReleasedWhenDockerAnswers(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	backend := &dockerOffSandbox{Manager: &sandbox.Manager{Root: t.TempDir()}}
	application, err := New(t.TempDir(), WithSandboxBackend(backend))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	ctx := context.Background()
	world := openTestWorld(t, application)
	order := managedWorkOrderV2()
	order.WorkspaceID = world.ID
	order.Workspace = domain.WorkspacePlan{Mode: "existing", Path: world.Path, Isolation: "snapshot"}
	assignReadyRosterForTest(t, application, &order)
	order, err = application.SaveWorkOrderV2(ctx, order)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := application.store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), "docker-off")
	if err != nil {
		t.Fatal(err)
	}
	quest, err := application.workOrderQuestV2(ctx, world.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	quest.Status = domain.QuestPreflight
	if err = application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}

	application.runWorkOrderLaunchV2(ctx, approval, quest, "")

	waiting, err := application.WorkOrderV2(ctx, order.ID)
	if err != nil || waiting.Runtime == nil {
		t.Fatalf("runtime unreadable: %v", err)
	}
	if waiting.Runtime.Status != domain.QuestPaused || !waiting.Runtime.WaitingForSandbox || waiting.Runtime.ResumeAfterRestart {
		t.Fatalf("launch without Docker must wait visibly: %#v", waiting.Runtime)
	}
	if !strings.Contains(waiting.Runtime.Message, "Docker Desktop") {
		t.Fatalf("card does not say what to do: %q", waiting.Runtime.Message)
	}
	messages, err := application.store.ListChatMessages(ctx, world.ID, "master", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) == 0 || !strings.Contains(messages[len(messages)-1].Content, "Docker Desktop") {
		t.Fatalf("the feed says nothing about Docker: %#v", messages)
	}

	application.resumeQuestsWaitingForSandboxV2(ctx)
	still, _ := application.WorkOrderV2(ctx, order.ID)
	if still.Runtime.ResumeAfterRestart {
		t.Fatal("a quest was released while Docker is still down")
	}

	backend.up.Store(true)
	application.resumeQuestsWaitingForSandboxV2(ctx)
	released, err := application.WorkOrderV2(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if released.Runtime.Status != domain.QuestPaused || released.Runtime.WaitingForSandbox || !released.Runtime.ResumeAfterRestart {
		t.Fatalf("Docker is back but the quest was not handed to the extension: %#v", released.Runtime)
	}
	// The launch never planned, so continuing returns it to preflight and a
	// fresh launch, not to a `running` nobody executes.
	status, err := application.store.ControlWorkOrderQuestV2(ctx, quest.ID, "resume", "")
	if err != nil || status != domain.QuestPreflight {
		t.Fatalf("continuation of a launch that never planned resumed to %s err=%v", status, err)
	}
}

func TestOwnRunningStatusIsNotTheHumansDecision(t *testing.T) {
	for status, moved := range map[domain.QuestStatus]bool{
		domain.QuestPreflight: false, domain.QuestRunning: false,
		domain.QuestPaused: true, domain.QuestCancelled: true, domain.QuestBlocked: true,
	} {
		if launchMovedByHumanV2(status) != moved {
			t.Fatalf("status %s: moved by human = %v, want %v", status, !moved, moved)
		}
	}
	if !sandbox.IsUnavailable(errors.New("start: Docker недоступен — запустите Docker Desktop и повторите запуск: x")) {
		t.Fatal("the refusal is not recognised after crossing a boundary as text")
	}
}
