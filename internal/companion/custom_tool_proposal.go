package companion

import (
	"context"
	"regexp"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// commandFromMessage достаёт команду из сообщения человека дословно.
//
// Обратные кавычки, «ёлочки» или обычные кавычки — три способа, которыми человек
// обычно выделяет команду. Ничего не найдено — значит команды не было, и
// придумывать её нельзя.
var commandInMessage = regexp.MustCompile("`([^`\n]{1,1000})`|«([^»\n]{1,1000})»|\"([^\"\n]{1,1000})\"")

func commandFromMessage(message string) string {
	match := commandInMessage.FindStringSubmatch(message)
	if match == nil {
		return ""
	}
	for _, group := range match[1:] {
		if command := strings.TrimSpace(group); command != "" {
			return command
		}
	}
	return ""
}

// companionToolName — имя черновика по первым словам команды. Человек всё равно
// правит его в окне подтверждения; здесь важно лишь, чтобы имя было осмысленным
// и коротким.
func companionToolName(command string) string {
	fields := strings.Fields(command)
	if len(fields) > 3 {
		fields = fields[:3]
	}
	name := strings.Join(fields, " ")
	if runes := []rune(name); len(runes) > 60 {
		name = string(runes[:60])
	}
	if strings.TrimSpace(name) == "" {
		return "Самодельный инструмент"
	}
	return name
}

// proposeCustomToolAction готовит черновик самодельного инструмента.
//
// Команда берётся из сообщения человека дословно и никогда не сочиняется:
// инструмент — это команда с именем, и сочинённая строка исполнялась бы потом
// на его машине. Создаёт инструмент подтверждение человека, а в права агента он
// не попадает и после этого — выдаёт их человек отдельно.
func (s Service) proposeCustomToolAction(ctx context.Context, cfg domain.CompanionConfig, message string, projectContext gatheredContext) (ChatResponse, error) {
	response := ChatResponse{Level: "suggestion", Mode: "deterministic", FactsUsed: projectContext.Facts}
	command := commandFromMessage(message)
	if command == "" {
		response.Reply = "Назовите команду, которую должен запускать инструмент, и выделите её обратными кавычками или кавычками — придумывать команду за вас я не буду."
		response.Questions = []string{"Какую команду должен запускать инструмент?"}
		return response, nil
	}
	now := time.Now().UTC()
	name := companionToolName(command)
	tool := domain.CustomTool{
		ID: domain.NewID("customtool"), Kind: domain.CustomToolCommand,
		DisplayName: name,
		Description: "Запускает «" + command + "» после подтверждения.",
		Command:     command, CWD: ".", TimeoutSeconds: 120,
		CreatedAt: now, UpdatedAt: now,
	}
	proposal := domain.CompanionActionProposal{
		ID: domain.NewID("companionaction"), WorkspaceID: cfg.WorkspaceID, Kind: domain.CompanionActionCreateTool,
		Title:     "Создать инструмент · " + name,
		Rationale: "Команда взята из вашего сообщения дословно. После подтверждения инструмент будет создан, но ни одному агенту не выдан: права выдаёт человек отдельным действием.",
		Tool:      &tool, Status: "pending", CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.SaveCompanionActionProposal(ctx, proposal); err != nil {
		return ChatResponse{}, err
	}
	response.ActionProposal = &proposal
	response.Reply = "Подготовил инструмент «" + name + "» с командой «" + command + "». Проверьте команду: создание произойдёт только после подтверждения, и агенту инструмент сам по себе не достанется."
	return response, nil
}
