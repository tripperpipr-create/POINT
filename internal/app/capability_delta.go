package app

import (
	"context"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
)

// Что изменится, если выдать агенту умение или экипировать навык.
//
// Карточка навыка показывала список требуемых инструментов и карту политик —
// точные данные, которые ничего не говорят человеку. «Требует run_command»
// не отвечает на вопрос, ради которого навык и берут: что агент начнёт мочь и
// перестанет ли квест обрываться без доказательства.
//
// Разница считается тем же кодом, что и сама годность, поэтому обещание в
// карточке и поведение движка совпадают по построению.

type CapabilityDeltaRequest struct {
	Profile domain.AgentProfile `json:"profile"`
	// AddTools — умения, которые появятся: напрямую или как требования навыка.
	AddTools []string `json:"addTools,omitempty"`
	// RemoveTools — умения, которые отбирают.
	RemoveTools []string `json:"removeTools,omitempty"`
}

type CapabilityDelta struct {
	Before AgentCapability `json:"before"`
	After  AgentCapability `json:"after"`
	// Gained/Lost — изменения в том, что агент умеет делать.
	Gained []string `json:"gained,omitempty"`
	Lost   []string `json:"lost,omitempty"`
	// Resolved/Introduced — блокировки, которые снялись или появились. Это
	// важнее возможностей: именно они решают, состоится ли квест.
	Resolved   []string `json:"resolved,omitempty"`
	Introduced []string `json:"introduced,omitempty"`
	// Lines — итог человеческим языком. Пустой список означает «ничего не
	// изменится», и это честный ответ, а не повод придумать эффект.
	Lines []string `json:"lines"`
}

func withTools(profile domain.AgentProfile, add, remove []string) domain.AgentProfile {
	drop := make(map[string]bool, len(remove))
	for _, name := range remove {
		drop[strings.TrimSpace(name)] = true
	}
	seen := make(map[string]bool, len(profile.AllowedTools)+len(add))
	tools := make([]string, 0, len(profile.AllowedTools)+len(add))
	for _, name := range profile.AllowedTools {
		if drop[name] || seen[name] {
			continue
		}
		seen[name] = true
		tools = append(tools, name)
	}
	for _, name := range add {
		name = strings.TrimSpace(name)
		if name == "" || drop[name] || seen[name] {
			continue
		}
		seen[name] = true
		tools = append(tools, name)
	}
	profile.AllowedTools = tools
	return profile
}

func diffStrings(before, after []string) (added, removed []string) {
	inBefore := make(map[string]bool, len(before))
	for _, item := range before {
		inBefore[item] = true
	}
	inAfter := make(map[string]bool, len(after))
	for _, item := range after {
		inAfter[item] = true
	}
	for _, item := range after {
		if !inBefore[item] {
			added = append(added, item)
		}
	}
	for _, item := range before {
		if !inAfter[item] {
			removed = append(removed, item)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func abilities(c AgentCapability) []string {
	list := make([]string, 0, 4)
	if c.CanRead {
		list = append(list, "читать код")
	}
	if c.CanWrite {
		list = append(list, "предлагать правки")
	}
	if c.CanRunCommands {
		list = append(list, "запускать команды")
	}
	if c.CanVerify {
		list = append(list, "подтверждать результат")
	}
	return list
}

// CapabilityDeltaFor сравнивает годность агента до и после изменения умений.
func (a *App) CapabilityDeltaFor(ctx context.Context, req CapabilityDeltaRequest) CapabilityDelta {
	before := a.AgentCapabilityFor(ctx, req.Profile)
	after := a.AgentCapabilityFor(ctx, withTools(req.Profile, req.AddTools, req.RemoveTools))

	gained, lost := diffStrings(abilities(before), abilities(after))
	// Порядок аргументов обратный: снятая блокировка — это то, что было в
	// «до» и исчезло в «после».
	introduced, resolved := diffStrings(before.Blocking, after.Blocking)

	delta := CapabilityDelta{
		Before: before, After: after,
		Gained: gained, Lost: lost,
		Resolved: resolved, Introduced: introduced,
	}

	lines := make([]string, 0, 4)
	if len(gained) > 0 {
		lines = append(lines, "Появится: "+strings.Join(gained, ", ")+".")
	}
	if len(lost) > 0 {
		lines = append(lines, "Пропадёт: "+strings.Join(lost, ", ")+".")
	}
	for _, item := range resolved {
		lines = append(lines, "Снимется препятствие: "+item+".")
	}
	for _, item := range introduced {
		lines = append(lines, "Появится препятствие: "+item+".")
	}
	// Изменение списка подтверждений человек заметит сразу — он будет
	// отвечать на эти запросы во время каждого прогона.
	askAdded, askRemoved := diffStrings(before.NeedsApproval, after.NeedsApproval)
	if len(askAdded) > 0 {
		lines = append(lines, "Начнёт спрашивать вас: "+strings.Join(askAdded, ", ")+".")
	}
	if len(askRemoved) > 0 {
		lines = append(lines, "Перестанет спрашивать: "+strings.Join(askRemoved, ", ")+".")
	}
	if len(lines) == 0 {
		lines = append(lines, "Ничего не изменится: эти умения у агента уже есть.")
	}
	delta.Lines = lines
	return delta
}
