package app

// Сессии MCP: доступ сущности, выданный на время одного разговора.
//
// Исполнителю, который принимает инструменты только как MCP-сервер, нужен
// адрес и ключ. Ключ выдаётся под конкретную сущность — тот же исполнитель
// инструментов, что отвечает на обычном пути, — и закрывается вместе с работой:
// переживший её ключ становится дырой в правах.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"local-agent-workbench/internal/mcp"
)

// MCPHandler — точка входа протокола. Маршрут один на всех: кто спрашивает,
// решает ключ сессии.
func (a *App) MCPHandler() http.HandlerFunc {
	return a.mcpSessions.Handler()
}

// OpenMCPSession выдаёт ключ и адрес для исполнителя. Закрывать сессию обязан
// тот, кто её открыл, — возвращаемая функция для этого и нужна.
func (a *App) OpenMCPSession(subject string, executor mcp.Executor) (key string, closer func(), err error) {
	if executor == nil {
		return "", nil, errors.New("mcp session needs an executor")
	}
	raw := make([]byte, 24)
	if _, err = rand.Read(raw); err != nil {
		return "", nil, err
	}
	key = hex.EncodeToString(raw)
	if err = a.mcpSessions.Open(mcp.Session{Key: key, Subject: subject, Executor: executor}); err != nil {
		return "", nil, err
	}
	return key, func() { a.mcpSessions.Close(key) }, nil
}

// MCPSessionCount — сколько сессий открыто сейчас. Сессия, пережившая работу,
// видна только счётчиком, поэтому он есть.
func (a *App) MCPSessionCount() int { return a.mcpSessions.Count() }

// MCPStreamRefusal объясняет клиенту, что поток событий здесь не открывается.
// Молчаливый 405 маршрутизатора выглядит поломкой сервера, а дело всего лишь в
// том, что вызовы идут запрос-ответом.
func (a *App) MCPStreamRefusal() http.HandlerFunc {
	return a.mcpSessions.StreamRefusal()
}

func (a *App) OpenRunToolSession(subject string, executor mcp.Executor) (string, func(), error) {
	baseURL := strings.TrimSpace(a.SelfURL())
	if baseURL == "" {
		return "", nil, errors.New("core address is unknown, cannot publish tools over MCP")
	}
	key, closeSession, err := a.OpenMCPSession(subject, executor)
	if err != nil {
		return "", nil, err
	}
	config, err := mcp.ClientConfig(baseURL, key)
	if err != nil {
		closeSession()
		return "", nil, err
	}
	return config, closeSession, nil
}
