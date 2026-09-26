package orchestrator

import (
	"errors"
	"regexp"
	"strings"

	"local-agent-workbench/internal/domain"
)

// dockerWorkPattern matches instructions that ask an executor to run Docker,
// not ones that ask it to write a Dockerfile or docker-compose.yml.
var dockerWorkPattern = regexp.MustCompile(`(?i)\bdocker(?:\s+compose|-compose)?\s+(?:build|up|run|start|stop|restart|exec|ps|logs|down|pull)\b`)

// stdlibOnlyPattern matches the degradation the first live Go quest shipped:
// "use only the Go standard library because external module downloads are
// not permitted", which turned a pgx call into a hand-written wire protocol.
var stdlibOnlyPattern = regexp.MustCompile(`(?i)\b(?:only|solely)\s+(?:the\s+)?(?:go\s+|python\s+|node\s+)?standard\s+library\b|\bstdlib[- ]only\b|без\s+внешних\s+зависимост|только\s+стандартн\w*\s+библиотек`)

// validateStageEnvironment rejects stages the sandbox cannot perform and
// stages that quietly downgrade the approved design. Rejection is not the end
// of planning: the reason goes back to the model as RetryFeedback.
func validateStageEnvironment(stage PlanStage, req PlanRequest) error {
	if req.Environment == nil {
		return nil
	}
	if !req.Environment.DockerInSandbox && dockerWorkPattern.MatchString(stage.Instruction) {
		hostChecks := "критерии Compose"
		if len(req.Environment.HostVerifiedCriteria) > 0 {
			hostChecks = "критерии " + strings.Join(req.Environment.HostVerifiedCriteria, ", ")
		}
		return errors.New("в песочнице исполнителя нет Docker, а стадия поручает его запуск; " + hostChecks + " Point проверит на хосте после доставки — поручи исполнителю сборку и тесты без Docker")
	}
	if stdlibOnlyPattern.MatchString(stage.Instruction) && !briefAsksForStdlibOnly(req.Proposal.Brief) {
		return errors.New("стадия ограничивает исполнителя стандартной библиотекой, хотя утверждённое задание этого не требует; используй де-факто стандартную зависимость — реестры пакетов перечислены в executionEnvironment.networkHosts")
	}
	return nil
}

func briefAsksForStdlibOnly(brief *domain.TaskBrief) bool {
	if brief == nil {
		return false
	}
	parts := []string{brief.SourceRequest, brief.Goal}
	parts = append(parts, brief.Scope...)
	for _, decision := range brief.Decisions {
		parts = append(parts, decision.Decision)
	}
	text := strings.ToLower(strings.Join(parts, " "))
	return stdlibOnlyPattern.MatchString(text) || strings.Contains(text, "stdlib") ||
		strings.Contains(text, "стандартной библиотек") || strings.Contains(text, "без зависимост")
}
