package agent

import "context"

// StageVerifier — проверка Point перед завершением этапа.
//
// Пишущий этап узнавал о проваленной приёмке только после неё: его задание
// несёт один ручной критерий stage-complete, и приёмочные команды он не
// обязан проходить. Квест 30.09 так потерял 57 минут. Теперь, когда этап
// собрался завершиться, ядро может само прогнать критерии и вернуть причину
// провала агенту — пока тот ещё в работе и может исправить.
//
// Решает, нужна ли проверка и что гонять, приложение: движок не знает ни
// графа Flow, ни наряда.
type StageVerifier interface {
	VerifyBeforeCompletion(ctx context.Context, request StageVerifyRequest) StageVerifyOutcome
}

type StageVerifyRequest struct {
	RunID       string
	ExecutionID string
	QuestID     string
	FlowRunID   string
	FlowNodeID  string
	SandboxPath string
}

type StageVerifyOutcome struct {
	// Ran — проверка была (или её исход взят из уже сделанной на том же
	// дереве). Нет — этап завершается как раньше.
	Ran    bool `json:"ran"`
	Passed bool `json:"passed"`
	// Reused — исход взят из прежнего прогона на том же дереве и образе.
	Reused     bool     `json:"reused,omitempty"`
	ResultID   string   `json:"resultId,omitempty"`
	TreeDigest string   `json:"treeDigest,omitempty"`
	Summaries  []string `json:"summaries,omitempty"`
	// Feedback — что сказать агенту при провале: причины по проверкам.
	Feedback string `json:"-"`
}

// maxPreAcceptChecks — сколько раз проверка возвращает этап в работу. Дальше
// этап завершается, а вердикт выносит приёмка: проверка помогает исправить
// раньше, но не становится вторым судьёй со своими правилами отказа.
const maxPreAcceptChecks = 2

func (e *Engine) SetStageVerifier(verifier StageVerifier) { e.stageVerifier = verifier }
