package app

// Мост между инструментами сущности и исполнителем, который принимает их
// только по MCP.
//
// Ничего своего мост не решает: он берёт исполнителя инструментов сущности —
// тот же, что отвечает на обычном пути, — открывает под него сессию и отдаёт
// исполнителю адрес, ключ и поимённый список разрешённого. Права остаются там,
// где записаны: в policy.Grants у самой сущности.

import (
	"errors"
	"strings"

	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/mcp"
)

type companionToolBridge struct{ app *App }

func (b companionToolBridge) OpenToolSession(subject string, tools companion.ReadTools) (string, []string, func(), error) {
	if tools == nil {
		return "", nil, nil, errors.New("tool session needs tools")
	}
	baseURL := b.app.SelfURL()
	if strings.TrimSpace(baseURL) == "" {
		return "", nil, nil, errors.New("core address is unknown, cannot publish tools over MCP")
	}
	key, closeSession, err := b.app.OpenMCPSession(subject, tools)
	if err != nil {
		return "", nil, nil, err
	}
	config, err := mcp.ClientConfig(baseURL, key)
	if err != nil {
		closeSession()
		return "", nil, nil, err
	}
	// Разрешение выдаётся поимённо и ровно на то, что сущности доступно сейчас:
	// список приходит от её же исполнителя, а не из отдельного перечисления,
	// которое разошлось бы с правами при первом новом инструменте.
	definitions := tools.Definitions()
	allowed := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		allowed = append(allowed, definition.Name)
	}
	return config, allowed, closeSession, nil
}

// SelfURL — адрес, по которому ядро доступно исполнителям на этой машине. Его
// знает только тот, кто поднял сервер, поэтому он приходит извне.
func (a *App) SelfURL() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.selfURL
}

// SetSelfURL сообщает ядру его собственный адрес. Вызывается один раз при
// старте сервера: без адреса инструменты Point некуда публиковать.
func (a *App) SetSelfURL(url string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.selfURL = strings.TrimRight(strings.TrimSpace(url), "/")
}
