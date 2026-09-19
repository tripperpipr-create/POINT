package policy

import (
	"strings"

	"local-agent-workbench/internal/domain"
)

type Decision struct {
	RequiresApproval bool
	Denied           bool
	Reason           string
	Risk             domain.ToolRisk
	Policy           domain.ToolPolicy
}

type Engine struct {
	// TrustedCustomTool отвечает, доверен ли самодельный инструмент. Пустой
	// движок не доверяет никому: доверие приходит только оттуда, где есть
	// хранилище со счётчиком подтверждений.
	TrustedCustomTool func(name string) bool
}

// trusted — доверие только для самодельных инструментов. Встроенным оно ни к
// чему: их подтверждение описано риском и политикой.
func (e Engine) trusted(toolName string) bool {
	return strings.HasPrefix(toolName, "customtool_") && e.TrustedCustomTool != nil && e.TrustedCustomTool(toolName)
}

func (e Engine) Evaluate(profile domain.AgentProfile, toolName string) Decision {
	risk := RiskForTool(toolName)
	policy := PolicyForTool(profile, toolName)
	decision := Decision{Risk: risk, Policy: policy}
	// Доверенный инструмент исполняется без окна, но перебить он может только
	// умолчание. Явная запись в политике профиля — стоячее указание человека,
	// «подтверждать всё» — тоже, а DENY доверием не отменяется вовсе.
	_, explicit := profile.ToolPolicies[toolName]
	if !explicit && policy != domain.ToolPolicyDeny && profile.ApprovalMode != domain.ApprovalAlways && e.trusted(toolName) {
		decision.Policy = domain.ToolPolicyAllow
		decision.Reason = "Инструмент подтверждён вручную нужное число раз и переведён в доверенные"
		return decision
	}
	switch policy {
	case domain.ToolPolicyDeny:
		decision.Denied = true
		decision.Reason = "Tool policy is DENY"
		return decision
	case domain.ToolPolicyAsk:
		decision.RequiresApproval = true
		decision.Reason = "Tool policy requires confirmation"
		return decision
	}
	if profile.ApprovalMode == domain.ApprovalAlways {
		decision.RequiresApproval = true
		decision.Reason = "Agent profile requires confirmation for every tool"
		return decision
	}
	if strings.HasPrefix(toolName, "customtool_") {
		decision.RequiresApproval = true
		decision.Reason = "Пользовательский инструмент запускает заранее настроенную команду"
		return decision
	}
	switch toolName {
	case "run_command":
		decision.RequiresApproval = true
		decision.Reason = "Commands can execute arbitrary local programs"
	case "propose_patch":
		decision.RequiresApproval = true
		decision.Reason = "File changes require accepting the proposed diff"
	case "docker_control":
		decision.RequiresApproval = true
		decision.Reason = "Docker start/stop изменяет локальные контейнеры"
	case "ssh_exec_remote":
		decision.RequiresApproval = true
		decision.Reason = "Удалённая SSH-команда может изменить сервер"
	case "ssh_test_connection", "ssh_list_remote", "ssh_read_remote":
		decision.RequiresApproval = true
		decision.Reason = "SSH обращается к внешней сети и требует подтверждения"
	case "db_exec":
		decision.RequiresApproval = true
		decision.Reason = "Запись в БД необратима через sandbox Change Set"
	case "db_query", "db_schema":
		decision.RequiresApproval = true
		decision.Reason = "Запросы к БД обращаются к внешним данным и требуют подтверждения"
	}
	return decision
}

// RiskForTool описывает, что инструмент делает, а не кому он разрешён: доступ
// решают grants сущности. Риск берётся из записи каталога — единственного
// места, где он объявлен.
func RiskForTool(toolName string) domain.ToolRisk {
	switch {
	// Пользовательского инструмента в каталоге нет и быть не может: он
	// запускает заранее настроенную команду, и это критично всегда.
	case strings.HasPrefix(toolName, "customtool_"):
		return domain.ToolRiskCritical
	default:
		if item, ok := domain.ToolCatalogEntry(toolName); ok {
			switch risk := domain.ToolRisk(item.Risk); risk {
			case domain.ToolRiskLow, domain.ToolRiskMedium, domain.ToolRiskHigh, domain.ToolRiskCritical:
				return risk
			}
		}
		// Незнакомое имя поблажки не получает.
		return domain.ToolRiskMedium
	}
}

// reservedNetworkTools — имена, которых в каталоге нет, потому что таких
// инструментов пока не существует. Запрет заведён заранее: он должен ждать их
// появления, а не догонять его.
var reservedNetworkTools = map[string]bool{"http_request": true, "fetch_url": true, "web_search": true}

// toolDefaultsToDeny — выходит ли инструмент за пределы рабочей папки. Признак
// живёт в записи каталога, а не в перечислении имён здесь: иначе новый
// сетевой инструмент получал бы ALLOW по умолчанию из-за забытой строки.
func toolDefaultsToDeny(toolName string) bool {
	if reservedNetworkTools[toolName] {
		return true
	}
	item, ok := domain.ToolCatalogEntry(toolName)
	return ok && item.DefaultDeny
}

func PolicyForTool(profile domain.AgentProfile, toolName string) domain.ToolPolicy {
	if toolDefaultsToDeny(toolName) {
		if raw, ok := profile.ToolPolicies[toolName]; ok {
			return parseToolPolicy(raw, domain.ToolPolicyDeny)
		}
		return domain.ToolPolicyDeny
	}
	if raw, ok := profile.ToolPolicies[toolName]; ok {
		return parseToolPolicy(raw, domain.ToolPolicyAsk)
	}
	switch RiskForTool(toolName) {
	case domain.ToolRiskLow:
		return domain.ToolPolicyAllow
	case domain.ToolRiskMedium:
		return domain.ToolPolicyAsk
	case domain.ToolRiskHigh, domain.ToolRiskCritical:
		return domain.ToolPolicyAsk
	default:
		return domain.ToolPolicyAsk
	}
}

func parseToolPolicy(raw string, fallback domain.ToolPolicy) domain.ToolPolicy {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "ALLOW":
		return domain.ToolPolicyAllow
	case "ASK":
		return domain.ToolPolicyAsk
	case "DENY":
		return domain.ToolPolicyDeny
	default:
		return fallback
	}
}
