package app

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"local-agent-workbench/internal/domain"
)

// Состояние мира не растёт вечно от разговоров с Мастером.
//
// Каждая сказанная Мастеру задача создаёт предложение квеста, решённое остаётся
// строкой в базе навсегда, а состояние мира запрашивается часто: при открытии
// панели, после каждого решения, после хода, создавшего предложение. Замер на
// живом ядре: шестьдесят ходов — 38 КиБ предложений в каждом bootstrap, и
// дальше только больше.
//
// Открытые при этом обязаны доезжать все, сколько бы их ни было: по ним
// считается очередь решений и рисуются карточки на разбор, и старое нерешённое
// не должно пропасть с экрана из-за предела, поставленного ради решённых.
func TestTrimResolvedProposalsKeepsOpenAndCapsResolved(t *testing.T) {
	var proposals []domain.QuestProposal
	// Порядок как из хранилища: от новых к старым.
	for index := 0; index < 120; index++ {
		status := "ignored"
		if index%20 == 0 {
			status = "pending"
		}
		proposals = append(proposals, domain.QuestProposal{
			ID:     fmt.Sprintf("qp-%03d", index),
			Title:  fmt.Sprintf("Задача %d", index),
			Status: status,
		})
	}

	trimmed := trimResolvedProposals(proposals)

	open, resolved := 0, 0
	for _, proposal := range trimmed {
		if proposal.Status == "pending" || proposal.Status == "modified" {
			open++
		} else {
			resolved++
		}
	}
	if open != 6 {
		t.Fatalf("открытых предложений доехало %d из 6 — нерешённое пропало с экрана", open)
	}
	if resolved != bootstrapResolvedProposals {
		t.Fatalf("решённых доехало %d, предел %d", resolved, bootstrapResolvedProposals)
	}

	// Оставаться должны свежие: разговор показывает решённое у той реплики, что
	// его создала, а видно в нём последние реплики. Сравниваем с началом того же
	// списка, а не пересчитываем границу заново — иначе проверка повторяла бы
	// сам алгоритм и соглашалась бы с любой его ошибкой.
	var wantResolved []string
	for _, proposal := range proposals {
		if proposal.Status == "ignored" && len(wantResolved) < bootstrapResolvedProposals {
			wantResolved = append(wantResolved, proposal.ID)
		}
	}
	var gotResolved []string
	for _, proposal := range trimmed {
		if proposal.Status == "ignored" {
			gotResolved = append(gotResolved, proposal.ID)
		}
	}
	for index := range wantResolved {
		if gotResolved[index] != wantResolved[index] {
			t.Fatalf("вместо свежих решённых доехали другие: на месте %d — %q, ожидалось %q",
				index, gotResolved[index], wantResolved[index])
		}
	}

	// Мир без предложений остаётся миром без предложений, а не nil-ом.
	if got := trimResolvedProposals(nil); got == nil || len(got) != 0 {
		t.Fatalf("пустой список превратился в %#v", got)
	}
}

// Пустой ростер — это ответ, а не молчание.
//
// Интерфейс решает, знает ли ядро про Hub, по наличию ключей blueprints и
// projectAgents в bootstrap. С omitempty пустой список исчезал из ответа, и
// «агентов пока нет» становилось неотличимо от «ядро старое». По второму
// прочтению конструктор уходит на legacy-путь сохранения, где теряются
// личность, миссия, ограничения, навыки и проектные правила — пять шагов
// настройки из десяти, молча.
func TestHubBootstrapAlwaysNamesAgentCollections(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if boot.ProjectAgents == nil {
		t.Fatal("пустой ростер остался nil — JSON превратит его в null")
	}
	empty, err := json.Marshal(boot)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(empty, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"blueprints", "projectAgents"} {
		value, ok := decoded[key]
		if !ok {
			t.Fatalf("ключ %q пропал из пустого bootstrap — интерфейс примет ядро за старое и потеряет половину настройки", key)
		}
		if _, ok := value.([]any); !ok {
			t.Fatalf("коллекция %q пришла как %T, нужен JSON-массив даже при нуле элементов", key, value)
		}
	}
}

func TestCompanionDefaultIsNotHumanConfiguration(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if boot.Companion == nil || boot.Companion.Configured {
		t.Fatalf("технический default выдал себя за выбор человека: %#v", boot.Companion)
	}
	saved, err := application.SaveCompanionConfig(*boot.Companion)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Configured {
		t.Fatal("явное сохранение не отметило настройку завершённой")
	}
	boot, err = application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if boot.Companion == nil || !boot.Companion.Configured {
		t.Fatalf("признак явной настройки не пережил storage: %#v", boot.Companion)
	}
}
