package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestParseIndependentReviewReadsTheLastVerdict(t *testing.T) {
	result := "Проверил файлы.\n```json\n{\"criteria\":[{\"id\":\"old\",\"status\":\"passed\"}]}\n```\nИтог:\n```json\n" +
		`{"criteria":[{"id":"lockfile","status":"failed","evidence":"package-lock.json:1 — нет ssh2"},{"id":"build","status":"passed","evidence":"ok"}]}` + "\n```"
	verdicts, ok := parseIndependentReview(result)
	if !ok || len(verdicts) != 2 || verdicts[0].ID != "lockfile" || verdicts[0].Status != "failed" {
		t.Fatalf("вердикт: %+v ok=%v", verdicts, ok)
	}
	if _, ok = parseIndependentReview("Всё хорошо, замечаний нет."); ok {
		t.Fatal("вердикт найден там, где его нет")
	}
}

// Q12 (E6): ревью закрывалось без единого запуска модели и ни на что не
// влияло. Проваленный критерий в вердикте проверяющего проваливает этап, а
// рассказы писателей до него не доходят.
func TestIndependentReviewVerdictFailsTheStageAndSeesNoNarrative(t *testing.T) {
	f := newVerificationFixture(t)
	ctx := context.Background()
	review := domain.FlowNode{ID: "node_review", Kind: domain.FlowNodeAgent, Name: "Implementation review", Config: map[string]any{"stageRole": domain.StageRoleImplReview, "writeFiles": false}}
	writer := f.writer
	flow := domain.FlowGraph{ID: "flow_review", Nodes: []domain.FlowNode{writer, review}, Edges: []domain.FlowEdge{{ID: "e", From: writer.ID, To: review.ID}}}
	run := f.flowRun
	run.ID = "flowrun_review"
	run.Snapshot = map[string]any{"graph": flow}
	run.NodeStates = map[string]domain.FlowNodeState{
		writer.ID: {Status: "completed", Output: map[string]any{"executionId": "exec-writer", "result": "Я всё сделал идеально, lock-файл синхронизирован.", "changedFiles": []string{"package.json"}}},
	}
	run.StartedAt = time.Now().UTC()
	if err := f.app.store.SaveFlowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	failure, verdicts := f.app.independentReviewFailure(ctx, run.ID, review.ID, "```json\n"+`{"criteria":[{"id":"lockfile","status":"failed","evidence":"package-lock.json — нет ssh2"}]}`+"\n```")
	if !strings.Contains(failure, "lockfile") || !strings.Contains(failure, "нет ssh2") || len(verdicts) != 1 {
		t.Fatalf("провал по вердикту не назван: %q %+v", failure, verdicts)
	}
	if failure, _ = f.app.independentReviewFailure(ctx, run.ID, writer.ID, "```json\n"+`{"criteria":[{"id":"x","status":"failed"}]}`+"\n```"); failure != "" {
		t.Fatal("вердикт писателя принят за вердикт проверяющего")
	}
	if failure, _ = f.app.independentReviewFailure(ctx, run.ID, review.ID, "Замечаний нет."); failure != "" {
		t.Fatal("ревью без вердикта провалило этап")
	}
	inputs := flowNodeContext(f.quest, flow, run, review.ID, nil)
	encoded, _ := json.Marshal(inputs)
	if strings.Contains(string(encoded), "идеально") {
		t.Fatal("рассказ писателя дошёл до независимого проверяющего")
	}
	if !strings.Contains(string(encoded), "package.json") {
		t.Fatal("изменённые файлы не дошли до проверяющего")
	}
	if writerInputs, _ := json.Marshal(flowNodeContext(f.quest, flow, run, writer.ID, nil)); strings.Contains(string(writerInputs), "\"toNode\":\"Implementation review\"") {
		t.Fatal("контекст писателя перепутан с контекстом проверяющего")
	}
}
