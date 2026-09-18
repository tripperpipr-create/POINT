package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
)

func saveTestConnection(t *testing.T, application *App, id, preset, name string) domain.Connection {
	t.Helper()
	conn, err := application.SaveConnection(connections.UpsertRequest{
		ID: id, Provider: domain.ProviderOpenAI, PresetID: preset,
		DisplayName: name, BaseURL: "https://example.invalid/v1", SecretRef: "secret:" + id,
	})
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

// Два ключа одного провайдера — личный и рабочий, dev и prod — были тем самым
// случаем, на котором запрос молча уходил с чужим ключом: поиск брал первое
// совпадение по пресету. Теперь точная ссылка решает, а неоднозначность
// называется вслух.
func TestResolveConnectionPrefersExplicitLinkAndRefusesToGuess(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	work := saveTestConnection(t, application, "conn-work", "openai", "Рабочий ключ")
	personal := saveTestConnection(t, application, "conn-personal", "openai", "Личный ключ")

	resolved, err := application.ResolveConnection(ConnectionRef{ConnectionID: personal.ID, ProviderPreset: "openai"})
	if err != nil {
		t.Fatalf("явная ссылка не разрешилась: %v", err)
	}
	if resolved.ID != personal.ID {
		t.Fatalf("выбрано %q вместо явно указанного %q", resolved.ID, personal.ID)
	}

	_, err = application.ResolveConnection(ConnectionRef{ProviderPreset: "openai", Label: "агента SAGE-7"})
	if !errors.Is(err, ErrConnectionAmbiguous) {
		t.Fatalf("неоднозначность разрешилась молча: err=%v", err)
	}
	// Сообщение должно называть кандидатов: иначе человеку нечего выбирать.
	for _, name := range []string{work.DisplayName, personal.DisplayName, "агента SAGE-7"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("в сообщении нет %q: %s", name, err.Error())
		}
	}
}

func TestResolveConnectionFallsBackToSinglePresetMatch(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	only := saveTestConnection(t, application, "conn-only", "llmux", "Шлюз компании")

	// Строка без connectionId — это профиль, переживший миграцию 31 в
	// однозначном мире. Совместимость обязана работать без правки профиля.
	resolved, err := application.ResolveConnection(ConnectionRef{ProviderPreset: "llmux"})
	if err != nil {
		t.Fatalf("единственное совпадение не разрешилось: %v", err)
	}
	if resolved.ID != only.ID {
		t.Fatalf("выбрано %q вместо %q", resolved.ID, only.ID)
	}

	if _, err = application.ResolveConnection(ConnectionRef{ConnectionID: "conn-deleted"}); err == nil {
		t.Fatal("исчезнувшее подключение разрешилось без ошибки")
	}
	if _, err = application.ResolveConnection(ConnectionRef{ProviderPreset: "groq", Label: "компаньона"}); err == nil {
		t.Fatal("отсутствие подключения не названо ошибкой")
	}
}

func TestConnectionIDRoundTripsAcrossProfileBlueprintAndProjectAgentAndResolvesLiveEndpoint(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	world := openTestWorld(t, application)
	conn := saveTestConnection(t, application, "conn-roundtrip", "openai", "Round trip")
	profile, err := application.SaveProfile(domain.AgentProfile{
		ID: "profile-roundtrip", Name: "Roundtrip", RoleDescription: "Review code", SystemPrompt: "Review code",
		ConnectionID: conn.ID, Provider: domain.ProviderOpenAI, ProviderPreset: "openai", BaseURL: "https://stale.invalid/v1",
		Model: "gpt-test", AllowedTools: []string{"read_file"}, MaxOutputTokens: 1024, ContextWindowTokens: 8192,
		MaxSteps: 20, MaxDurationSeconds: 120, ApprovalMode: domain.ApprovalSafe,
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.ConnectionID != conn.ID {
		t.Fatalf("profile lost connection: %#v", profile)
	}
	blueprint, err := application.SaveBlueprint(domain.BlueprintFromProfile(profile))
	if err != nil {
		t.Fatal(err)
	}
	if blueprint.ConnectionID != conn.ID {
		t.Fatalf("blueprint lost connection: %#v", blueprint)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(world.ID, blueprint))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := application.store.GetProjectAgent(context.Background(), agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	all, err := application.store.ListAllProjectAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ConnectionID != conn.ID || len(all) == 0 || all[0].ConnectionID != conn.ID {
		t.Fatalf("project-agent round trip lost connection: loaded=%#v all=%#v", loaded, all)
	}
	updated, err := application.SaveConnection(connections.UpsertRequest{
		ID: conn.ID, Provider: domain.ProviderOpenAI, PresetID: "openai", DisplayName: conn.DisplayName,
		BaseURL: "https://new-endpoint.invalid/v1", SecretRef: conn.SecretRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := application.runtimeSnapshotForProjectAgent(world.ID, loaded, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Profile.BaseURL != updated.BaseURL || snapshot.Profile.ConnectionID != conn.ID {
		t.Fatalf("snapshot used stale copied endpoint: %#v", snapshot.Profile)
	}
}

func TestProviderMatrixRoundTripsAndResolvesLiveTargetsForEveryActorRole(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	world := openTestWorld(t, application)

	cases := []struct {
		name, preset, baseURL, nextBaseURL, apiVersion, nextAPIVersion, model string
		provider                                                              domain.ProviderKind
	}{
		{name: "ollama", preset: "ollama", baseURL: "http://127.0.0.1:11434", nextBaseURL: "http://127.0.0.1:11435", model: "qwen2.5-coder", provider: domain.ProviderOllama},
		{name: "openai", preset: "openai", baseURL: "https://openai.example.invalid/v1", nextBaseURL: "https://openai-next.example.invalid/v1", model: "gpt-test", provider: domain.ProviderOpenAI},
		{name: "anthropic", preset: "anthropic", baseURL: "https://anthropic.example.invalid/v1", nextBaseURL: "https://anthropic-next.example.invalid/v1", model: "claude-test", provider: domain.ProviderAnthropic},
		{name: "azure", preset: "azure-openai", baseURL: "https://point.openai.azure.com/openai/deployments/primary", nextBaseURL: "https://point-next.openai.azure.com/openai/deployments/primary", apiVersion: "2025-01-01-preview", nextAPIVersion: "2025-04-01-preview", model: "primary", provider: domain.ProviderAzureOpenAI},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			connectionID := "conn-matrix-" + testCase.name
			conn, saveErr := application.SaveConnection(connections.UpsertRequest{
				ID: connectionID, Provider: testCase.provider, PresetID: testCase.preset,
				DisplayName: testCase.name, BaseURL: testCase.baseURL, APIVersion: testCase.apiVersion,
				SecretRef: "secret:" + connectionID, DefaultModel: testCase.model,
			})
			if saveErr != nil {
				t.Fatal(saveErr)
			}
			profile, saveErr := application.SaveProfile(domain.AgentProfile{
				ID: "profile-matrix-" + testCase.name, Name: "Matrix " + testCase.name,
				RoleDescription: "Read code", SystemPrompt: "Read code",
				ConnectionID: conn.ID, Provider: testCase.provider, ProviderPreset: testCase.preset,
				BaseURL: "https://stale.example.invalid/v1", APIVersion: "stale",
				AllowedTools: []string{"read_file"}, MaxOutputTokens: 1024, ContextWindowTokens: 8192,
				MaxSteps: 20, MaxDurationSeconds: 120, ApprovalMode: domain.ApprovalSafe,
			})
			if saveErr != nil {
				t.Fatal(saveErr)
			}
			if profile.Model != testCase.model {
				t.Fatalf("profile did not inherit connection default model: got %q want %q", profile.Model, testCase.model)
			}
			storedProfiles, listErr := application.storedProfiles(context.Background())
			if listErr != nil {
				t.Fatal(listErr)
			}
			profileRoundTripped := false
			for _, storedProfile := range storedProfiles {
				if storedProfile.ID == profile.ID && storedProfile.ConnectionID == conn.ID {
					profileRoundTripped = true
					break
				}
			}
			if !profileRoundTripped {
				t.Fatalf("profile %q lost connection %q", profile.ID, conn.ID)
			}
			projectAgent, saveErr := application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(world.ID, domain.BlueprintFromProfile(profile)))
			if saveErr != nil {
				t.Fatal(saveErr)
			}

			companionConfig, _ := companion.PresetDefaults("balanced")
			companionConfig.ID = "companion-matrix-" + testCase.name
			companionConfig.ConnectionID, companionConfig.ProviderPreset = conn.ID, testCase.preset
			companionConfig.Provider, companionConfig.BaseURL, companionConfig.APIVersion = domain.ProviderOpenAI, "https://stale.example.invalid/v1", "stale"
			companionConfig.MaxOutputTokens = 1024
			companionConfig, saveErr = application.SaveCompanionConfig(companionConfig)
			if saveErr != nil {
				t.Fatal(saveErr)
			}
			if companionConfig.Model != testCase.model {
				t.Fatalf("companion did not inherit connection default model: got %q want %q", companionConfig.Model, testCase.model)
			}

			masterConfig, _ := orchestrator.PresetDefaults("conductor")
			masterConfig.ID = "master-matrix-" + testCase.name
			masterConfig.ConnectionID, masterConfig.ProviderPreset = conn.ID, testCase.preset
			masterConfig.Provider, masterConfig.BaseURL, masterConfig.APIVersion = domain.ProviderOpenAI, "https://stale.example.invalid/v1", "stale"
			masterConfig, saveErr = application.SaveOrchestratorConfig(masterConfig)
			if saveErr != nil {
				t.Fatal(saveErr)
			}
			if masterConfig.Model != testCase.model {
				t.Fatalf("master did not inherit connection default model: got %q want %q", masterConfig.Model, testCase.model)
			}

			updated, saveErr := application.SaveConnection(connections.UpsertRequest{
				ID: conn.ID, Provider: testCase.provider, PresetID: testCase.preset, DisplayName: conn.DisplayName,
				BaseURL: testCase.nextBaseURL, APIVersion: testCase.nextAPIVersion, SecretRef: conn.SecretRef, DefaultModel: testCase.model,
			})
			if saveErr != nil {
				t.Fatal(saveErr)
			}
			snapshot, snapshotErr := application.runtimeSnapshotForProjectAgent(world.ID, projectAgent, time.Now().UTC())
			if snapshotErr != nil {
				t.Fatal(snapshotErr)
			}
			loadedCompanion, loadErr := application.store.GetCompanionConfig(context.Background(), world.ID)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if loadedCompanion.APIVersion != testCase.apiVersion {
				t.Fatalf("companion compatibility API version was not persisted: got %q want %q", loadedCompanion.APIVersion, testCase.apiVersion)
			}
			loadedCompanion, loadErr = application.resolveCompanionConnection(loadedCompanion)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			loadedMaster, loadErr := application.store.GetOrchestratorConfig(context.Background(), world.ID)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if loadedMaster.APIVersion != testCase.apiVersion {
				t.Fatalf("master compatibility API version was not persisted: got %q want %q", loadedMaster.APIVersion, testCase.apiVersion)
			}
			loadedMaster, loadErr = application.resolveOrchestratorConnection(loadedMaster)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			for actor, target := range map[string]struct {
				connectionID, baseURL, apiVersion string
			}{
				"agent":     {snapshot.Profile.ConnectionID, snapshot.Profile.BaseURL, snapshot.Profile.APIVersion},
				"companion": {loadedCompanion.ConnectionID, loadedCompanion.BaseURL, loadedCompanion.APIVersion},
				"master":    {loadedMaster.ConnectionID, loadedMaster.BaseURL, loadedMaster.APIVersion},
			} {
				if target.connectionID != conn.ID || target.baseURL != updated.BaseURL || target.apiVersion != updated.APIVersion {
					t.Fatalf("%s resolved stale target: %#v want connection=%q base=%q api=%q", actor, target, conn.ID, updated.BaseURL, updated.APIVersion)
				}
			}
		})
	}
}

func TestAzureConnectionRequiresExplicitAPIVersion(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	_, err = application.SaveConnection(connections.UpsertRequest{
		Provider: domain.ProviderAzureOpenAI, PresetID: "azure-openai", DisplayName: "Azure",
		BaseURL: "https://point.openai.azure.com/openai/deployments/primary",
	})
	if err == nil || !strings.Contains(err.Error(), "API version") {
		t.Fatalf("Azure connection without API version was accepted: %v", err)
	}
}

// Удаление подключения, которым пользуются, ломало бы следующий квест молча:
// профиль продолжал бы утверждать, что настроен.
func TestDeleteConnectionRefusesWhileReferenced(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	openTestWorld(t, application)

	conn := saveTestConnection(t, application, "conn-used", "openai", "Ключ проекта")
	if _, err = application.SaveBlueprint(domain.AgentBlueprint{
		ID: "bp-1", Name: "SAGE-7", RoleDescription: "Разведка", SystemPrompt: "роль",
		ConnectionID: conn.ID, Provider: domain.ProviderOpenAI, ProviderPreset: "openai",
		PrimaryModel: "gpt-4o-mini", AllowedTools: []string{"read_file"},
	}); err != nil {
		t.Fatal(err)
	}

	err = application.DeleteConnection(conn.ID)
	if err == nil {
		t.Fatal("подключение удалилось вместе со ссылкой на него")
	}
	if !strings.Contains(err.Error(), "SAGE-7") {
		t.Fatalf("отказ не называет, кто мешает: %s", err.Error())
	}

	other := saveTestConnection(t, application, "conn-free", "groq", "Свободный ключ")
	if err = application.DeleteConnection(other.ID); err != nil {
		t.Fatalf("свободное подключение не удалилось: %v", err)
	}
}

// Флаг «по умолчанию» обязан быть один: два таких подключения вернули бы выбор
// к угадыванию.
func TestSetDefaultConnectionKeepsSingleDefault(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	first := saveTestConnection(t, application, "conn-a", "openai", "A")
	second := saveTestConnection(t, application, "conn-b", "groq", "B")

	if err = application.SetDefaultConnection(first.ID); err != nil {
		t.Fatal(err)
	}
	if err = application.SetDefaultConnection(second.ID); err != nil {
		t.Fatal(err)
	}
	list, err := application.ListConnections()
	if err != nil {
		t.Fatal(err)
	}
	defaults := make([]string, 0, 1)
	for _, item := range list {
		if item.IsDefault {
			defaults = append(defaults, item.ID)
		}
	}
	if len(defaults) != 1 || defaults[0] != second.ID {
		t.Fatalf("подключений по умолчанию %v, ожидалось только %q", defaults, second.ID)
	}
	if err = application.SetDefaultConnection("conn-missing"); err == nil {
		t.Fatal("несуществующее подключение стало подключением по умолчанию")
	}
}

func TestLookupModelPrefersLongestPrefixAndAdmitsUnknown(t *testing.T) {
	if window := domain.DefaultContextWindow("claude-sonnet-4-5"); window != 200000 {
		t.Fatalf("контекстное окно семейства Sonnet 4: %d", window)
	}
	// Агрегаторы отдают ID с префиксом провайдера — семейство определяется
	// частью после косой черты.
	if window := domain.DefaultContextWindow("anthropic/claude-sonnet-4-5"); window != 200000 {
		t.Fatalf("ID агрегатора не разобран: %d", window)
	}
	if _, ok := domain.LookupModel("внутренняя-модель-компании"); ok {
		t.Fatal("незнакомая модель выдана за известную")
	}
	if window := domain.DefaultContextWindow("внутренняя-модель-компании"); window != 0 {
		t.Fatalf("для незнакомой модели выдумано окно %d вместо честного нуля", window)
	}
}
