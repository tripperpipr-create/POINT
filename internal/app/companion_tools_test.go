package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/policy"
	"local-agent-workbench/internal/workspace"
)

// Grants и собранный реестр — два разных места, и разойтись они могут молча:
// выданное имя без реализации просто исчезает из Definitions, и модель узнаёт
// об инструменте только из отказа. Проверка держит их вместе.
func TestCompanionReadToolsExposeEveryAllowedName(t *testing.T) {
	fs, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tools := newCompanionReadTools(fs, nil)
	defined := map[string]bool{}
	for _, definition := range tools.Definitions() {
		defined[definition.Name] = true
	}
	for _, name := range policy.CompanionGrants().ToolNames() {
		if !defined[name] {
			t.Fatalf("инструмент %q разрешён, но в реестре его нет: %v", name, defined)
		}
	}
	// Вопросы про историю и метки приходят к компаньону так же часто, как про
	// ветки, а git_branches про refs/tags не знает.
	for _, name := range []string{"git_branches", "git_log", "git_tags"} {
		if !defined[name] {
			t.Fatalf("компаньон остался без %q: %v", name, defined)
		}
	}
	if len(defined) != len(policy.CompanionGrants().ToolNames()) {
		t.Fatalf("в реестре есть лишнее сверх выданного: %v против %v", defined, policy.CompanionGrants().ToolNames())
	}
	// Без надетых навыков read_skill помощнику не выдаётся: он смог бы только
	// ответить, что грузить нечего.
	if defined["read_skill"] {
		t.Fatalf("read_skill выдан помощнику без навыков: %v", defined)
	}
	// Пишущий инструмент компаньону недоступен даже по имени.
	refused := tools.Execute(context.Background(), "propose_patch", json.RawMessage(`{}`))
	if refused.OK || refused.Error == nil || refused.Error.Code != "tool_not_allowed" {
		t.Fatalf("пишущий инструмент не отклонён: %#v", refused)
	}
}

// Навык у помощника — практика, которую правит человек, а не правка кода. С
// надетым навыком ему нужен инструмент, читающий этот навык; без навыков он
// остаётся лишним именем в списке.
func TestCompanionReadToolsGainSkillReaderWithEquippedSkills(t *testing.T) {
	fs, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tools := newCompanionReadTools(fs, []domain.SkillRuntime{
		{ID: "skill-commits", Name: "Имена коммитов", Instructions: "Пиши подлежащее и действие."},
	})
	defined := map[string]bool{}
	for _, definition := range tools.Definitions() {
		defined[definition.Name] = true
	}
	if !defined["read_skill"] {
		t.Fatalf("помощник с навыком остался без read_skill: %v", defined)
	}
	if defined["propose_patch"] || defined["run_command"] {
		t.Fatalf("вместе с навыком приехало право менять: %v", defined)
	}
	result := tools.Execute(context.Background(), "read_skill", json.RawMessage(`{"id":"skill-commits"}`))
	if !result.OK {
		t.Fatalf("навык не читается: %#v", result)
	}
	// Право менять файлы навык не приносит.
	refused := tools.Execute(context.Background(), "propose_patch", json.RawMessage(`{}`))
	if refused.OK || refused.Error == nil || refused.Error.Code != "tool_not_allowed" {
		t.Fatalf("пишущий инструмент не отклонён: %#v", refused)
	}
}

// Навык проверяется на экипировке и у помощника: практику, которой нужен
// недоступный ему инструмент, надеть нельзя. Иначе она молча ничего не делала
// бы, а человек считал бы её работающей.
func TestCompanionRefusesSkillItCannotUse(t *testing.T) {
	application := newTestApp(t)
	var err error
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil || boot.Companion == nil {
		t.Fatalf("настройка помощника недоступна: %v", err)
	}
	writing, err := application.SaveSkill(domain.SkillDefinition{
		Name: "Правка файлов", Instructions: "Готовь патч.", RequiredTools: []string{"propose_patch"},
	})
	if err != nil {
		t.Fatal(err)
	}
	reading, err := application.SaveSkill(domain.SkillDefinition{
		Name: "Имена коммитов", Instructions: "Пиши подлежащее и действие.", RequiredTools: []string{"git_log"},
	})
	if err != nil {
		t.Fatal(err)
	}

	refused := *boot.Companion
	refused.SkillIDs = []string{writing.ID}
	if _, err = application.SaveCompanionConfig(refused); err == nil || !strings.Contains(err.Error(), "propose_patch") {
		t.Fatalf("навык с правкой файлов надет на помощника: %v", err)
	}

	// Навык, укладывающийся в его доступ, надевается и переживает хранилище.
	accepted := *boot.Companion
	accepted.SkillIDs = []string{reading.ID}
	saved, err := application.SaveCompanionConfig(accepted)
	if err != nil {
		t.Fatalf("навык по силам помощнику отклонён: %v", err)
	}
	if len(saved.SkillIDs) != 1 || saved.SkillIDs[0] != reading.ID {
		t.Fatalf("надетый навык не сохранился: %#v", saved.SkillIDs)
	}
	boot, err = application.Bootstrap()
	if err != nil || boot.Companion == nil || len(boot.Companion.SkillIDs) != 1 {
		t.Fatalf("навык не пережил storage: %v %#v", err, boot.Companion)
	}
}

// Разбор надетых навыков помощника: что доезжает до промпта, а что молча
// отбрасывается. Отбрасывать приходится по двум причинам — навык выключили в
// проекте или его правкой увели за пределы доступа помощника, — и обе должны
// оставлять разговор рабочим, а не ронять его.
func TestResolveCompanionSkillsKeepsFitAndDropsDrifted(t *testing.T) {
	application := newTestApp(t)
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	boot, err := application.Bootstrap()
	if err != nil || boot.Companion == nil {
		t.Fatalf("настройка помощника недоступна: %v", err)
	}
	fit, err := application.SaveSkill(domain.SkillDefinition{
		Name: "Имена коммитов", Instructions: "Пиши подлежащее и действие.", RequiredTools: []string{"git_log"},
	})
	if err != nil {
		t.Fatal(err)
	}
	drifting, err := application.SaveSkill(domain.SkillDefinition{
		Name: "Разбор диффа", Instructions: "Смотри git_diff.", RequiredTools: []string{"git_diff"},
	})
	if err != nil {
		t.Fatal(err)
	}
	switched, err := application.SaveSkill(domain.SkillDefinition{
		Name: "Выключенный", Instructions: "Не должен доехать.", RequiredTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := *boot.Companion
	cfg.SkillIDs = []string{fit.ID, drifting.ID, switched.ID}
	if _, err = application.SaveCompanionConfig(cfg); err != nil {
		t.Fatalf("навыки по силам помощнику отклонены: %v", err)
	}
	if resolved := application.resolveCompanionSkills(ctx, view.Workspace.ID); len(resolved) != 3 {
		t.Fatalf("надетые навыки не доехали: %#v", resolved)
	}

	// Навык правят и после экипировки: этот ушёл за пределы доступа помощника.
	drifting.RequiredTools = []string{"propose_patch"}
	if _, err = application.SaveSkill(drifting); err != nil {
		t.Fatal(err)
	}
	// А этот выключили в проекте.
	if err = application.store.SaveProjectSkill(ctx, domain.ProjectSkillInstance{
		ID: domain.NewID("projectskill"), WorkspaceID: view.Workspace.ID, SkillID: switched.ID, Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}

	resolved := application.resolveCompanionSkills(ctx, view.Workspace.ID)
	if len(resolved) != 1 || resolved[0].ID != fit.ID {
		t.Fatalf("остаться должен был только подходящий навык: %#v", resolved)
	}
	if resolved[0].Instructions != "Пиши подлежащее и действие." {
		t.Fatalf("инструкции навыка потерялись: %#v", resolved[0])
	}
}

// CLI-провайдеры помощника сняты: сохранение Claude Code CLI должно отвергаться.
func TestCompanionRejectsClaudeCLI(t *testing.T) {
	application := newTestApp(t)
	var err error
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil || boot.Companion == nil {
		t.Fatalf("настройка помощника недоступна: %v", err)
	}
	cfg := *boot.Companion
	cfg.Provider = domain.ProviderClaudeCLI
	cfg.Model = "haiku"
	cfg.BaseURL = ""
	if _, err := application.SaveCompanionConfig(cfg); err == nil {
		t.Fatal("локальный CLI принят настройкой после снятия CLI")
	} else if !strings.Contains(err.Error(), "removed") && !strings.Contains(err.Error(), "CLI") {
		t.Fatalf("ожидался отказ снятого CLI, got %v", err)
	}
}
