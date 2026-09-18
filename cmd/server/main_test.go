package main

import (
	"os"
	"strings"
	"testing"
)

func TestEnvFallsBackOnlyWhenUnset(t *testing.T) {
	t.Setenv("POINT_TEST_ENV", "")
	if got := env("POINT_TEST_ENV", "по умолчанию"); got != "по умолчанию" {
		t.Fatalf("пустая переменная равнозначна незаданной: %q", got)
	}
	t.Setenv("POINT_TEST_ENV", "задано")
	if got := env("POINT_TEST_ENV", "по умолчанию"); got != "задано" {
		t.Fatalf("заданная переменная сильнее умолчания: %q", got)
	}
}

// Порядок запуска ядра держится на двух причинах, записанных комментариями, и
// обе ломаются одной перестановкой строк.
//
// Слушать надо до OpenWorkspace: проверка здоровья из IDE не должна ждать
// обхода большого дерева, иначе расширение решит, что ядро не поднялось.
// Startup — до того, как порт открыт: он приводит в порядок квесты, брошенные
// остановленным ядром, и без него первый же запрос увидит их «идущими».
//
// Проверка смотрит на форму исходника, потому что поднять настоящий main в
// тесте нельзя: он ждёт сигнала и не возвращается.
func TestStartupOrderKeepsHealthCheckReachable(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	startup := strings.Index(text, "application.Startup(")
	listen := strings.Index(text, "server.ListenAndServe()")
	openWorkspace := strings.Index(text, "application.OpenWorkspace(")
	for name, at := range map[string]int{
		"application.Startup":       startup,
		"server.ListenAndServe":     listen,
		"application.OpenWorkspace": openWorkspace,
	} {
		if at < 0 {
			t.Fatalf("%s не найден — проверка прошла бы вхолостую", name)
		}
	}
	if startup > listen {
		t.Error("Startup обязан пройти до открытия порта: иначе первый запрос увидит брошенные квесты идущими")
	}
	if listen > openWorkspace {
		t.Error("слушать надо до OpenWorkspace: проверка здоровья не должна ждать обхода большого дерева")
	}
}

// Токен доступа читается до того, как что-либо поднято: без него сервер
// поднимать нечем, и падать надо на первом шаге, а не после инициализации.
func TestAPITokenResolvedBeforeApplication(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	token := strings.Index(text, "httpapi.ResolveAPIToken(")
	application := strings.Index(text, "app.New(")
	if token < 0 || application < 0 {
		t.Fatal("не найдены ResolveAPIToken или app.New — проверка прошла бы вхолостую")
	}
	if token > application {
		t.Error("токен разрешается до создания приложения: отказ должен случиться до инициализации, а не после")
	}
}
