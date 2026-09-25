// Package mcpclient — клиент Model Context Protocol: Point как потребитель
// чужих MCP-серверов (GitLab, Jira и любой сервер, добавленный владельцем).
//
// Сервер в соседнем пакете internal/mcp устроен наоборот: он отдаёт
// инструменты Point внешним исполнителям. Общего у них — только протокол.
//
// Клиент написан руками, без SDK. Нужны три метода (initialize, tools/list,
// tools/call), строгие отказы на всё, что сервер может попросить у клиента
// (модель, корни, ввод человека), потолки на размер и время и свой контроль
// над тем, куда идёт соединение. SDK принёс бы восемь модулей ради того же —
// против правила docs/DEPENDENCIES.md «одна зависимость — одна причина».
package mcpclient

import (
	"errors"
	"fmt"
	"strings"
)

// Kind — род сбоя. Ядро переводит его в человеческие problem и fix
// (app.describeMCPFailure); сам текст ошибки остаётся для журнала.
type Kind string

const (
	// KindNotTrusted — команда сервера изменилась или ни разу не одобрена.
	KindNotTrusted Kind = "not_trusted"
	// KindSecretLocked — секрет сервера ещё не передан ядру хостом.
	KindSecretLocked Kind = "secret_locked"
	// KindSpawn — программа не запустилась (нет npx, нет файла, отказ ОС).
	KindSpawn Kind = "spawn"
	// KindExited — процесс сервера завершился; в Error.Stderr его последние слова.
	KindExited Kind = "exited"
	// KindHandshake — сервер ответил на приветствие не так, как положено.
	KindHandshake Kind = "handshake"
	// KindTimeout — ответ не пришёл вовремя.
	KindTimeout Kind = "timeout"
	// KindAuth — удалённый сервер отказал в доступе (401/403).
	KindAuth Kind = "auth"
	// KindNetwork — соединение не установлено или оборвалось.
	KindNetwork Kind = "network"
	// KindPrivateHostDenied — адрес во внутренней сети без разрешения владельца.
	KindPrivateHostDenied Kind = "private_host_denied"
	// KindProtocol — сервер нарушил протокол (не JSON, чужой ответ, редирект).
	KindProtocol Kind = "protocol"
	// KindTool — инструмент выполнился и сообщил об ошибке (isError) или
	// сервер вернул JSON-RPC ошибку на вызов.
	KindTool Kind = "tool"
	// KindTooLarge — сообщение сервера больше потолка.
	KindTooLarge Kind = "too_large"
	// KindUnavailable — сервер остановлен после серии сбоев и ждёт владельца.
	KindUnavailable Kind = "unavailable"
)

// Error — сбой, который знает свой род. Текст уже прошёл зачистку секретов:
// он попадает в LastError и на экран «Интеграции».
type Error struct {
	Kind   Kind
	Detail string
	// Stderr — хвост stderr процесса на момент сбоя (только stdio).
	Stderr string
	// Status — HTTP-статус удалённого сервера, если он был.
	Status int
	err    error
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("mcp ")
	b.WriteString(string(e.Kind))
	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.err }

// KindOf возвращает род сбоя или пустую строку для чужой ошибки.
func KindOf(err error) Kind {
	var target *Error
	if errors.As(err, &target) {
		return target.Kind
	}
	return ""
}

func newError(kind Kind, cause error, format string, args ...any) *Error {
	return &Error{Kind: kind, Detail: fmt.Sprintf(format, args...), err: cause}
}
