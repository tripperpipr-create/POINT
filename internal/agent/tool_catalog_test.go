package agent

import (
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/policy"
	"local-agent-workbench/internal/workspace"
)

// Реестр и каталог — две половины одного объявления: реестр умеет исполнять,
// каталог говорит, кому и на каких условиях. Разъезжаются они молча: имя в
// реестре без записи в каталоге нельзя записать в профиль (так пропали
// git-инструменты и read_skill), а запись без реестра показывает в витрине
// инструмент, которого не существует (так жил code_index).
func TestToolCatalogMatchesBuiltInRegistry(t *testing.T) {
	fs, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Навык экипирован намеренно: без него read_skill в реестр не попадает, и
	// сверка не заметила бы, что записи для него нет.
	profile := domain.AgentProfile{
		ID: "agent-catalog", Model: "m",
		EquippedSkills: []domain.SkillRuntime{{ID: "skill-1", Name: "Проверка", Instructions: "делай"}},
	}
	registry, _ := BuildToolRegistryWithSources(fs, nil, nil, nil, profile)

	catalog := map[string]domain.ToolCatalogItem{}
	for _, item := range domain.BuiltInToolCatalog() {
		if item.Category == "" {
			t.Fatalf("инструмент %q объявлен без группы доступа", item.Name)
		}
		if _, duplicate := catalog[item.Name]; duplicate {
			t.Fatalf("инструмент %q объявлен в каталоге дважды", item.Name)
		}
		catalog[item.Name] = item
	}

	registered := map[string]bool{}
	for _, name := range registry.Names() {
		registered[name] = true
		if strings.HasPrefix(name, "customtool_") {
			continue
		}
		if _, ok := catalog[name]; !ok {
			t.Errorf("инструмент %q собран в реестре, но записи в каталоге нет: его нельзя выдать профилю", name)
		}
	}
	for name := range catalog {
		if !registered[name] {
			t.Errorf("инструмент %q объявлен в каталоге, но в реестре его нет", name)
		}
	}
}

// Риск и повторяемость теперь читаются из записи, и проверка держит смысл
// самих значений: читающий инструмент не должен внезапно стать критичным, а
// пишущий — повторяемым.
func TestToolCatalogRiskAndReplayStayConsistent(t *testing.T) {
	for _, item := range domain.BuiltInToolCatalog() {
		risk := policy.RiskForTool(item.Name)
		if string(risk) != item.Risk {
			t.Errorf("%s: риск в каталоге %q, а policy отдаёт %q", item.Name, item.Risk, risk)
		}
		if item.Replayable && (item.RequiresApproval || risk != domain.ToolRiskLow) {
			t.Errorf("%s: повторяемым объявлен инструмент, который не только читает", item.Name)
		}
		if isReplayableReadTool(item.Name) != item.Replayable {
			t.Errorf("%s: повторяемость реестра разошлась с каталогом", item.Name)
		}
	}
}

// Список инструментов с доступом ко всей рабочей папке решает, что станет
// недоступно, как только пользователь запретил хоть один путь. Он переехал в
// каталог, и проверка держит ровно прежний набор: лишнее имя здесь тихо
// отбирает у агента инструмент, недостающее — тихо отдаёт запрещённый путь.
func TestWorkspaceWideAccessMatchesTheKnownSet(t *testing.T) {
	expected := map[string]bool{
		"project_map": true, "search_code": true, "list_files": true,
		"search_text": true, "git_diff": true, "run_command": true,
		// git_log без пути перечисляет коммиты по всей папке, а с путём
		// движок его не фильтрует: целевой путь берут только у propose_patch
		// и read_file. Пока человек запретил хоть один путь, история
		// недоступна целиком — заголовки коммитов по запретному файлу это
		// тоже рассказ о нём.
		"git_log": true,
	}
	for _, item := range domain.BuiltInToolCatalog() {
		if item.WorkspaceWide != expected[item.Name] {
			t.Errorf("%s: доступ ко всей рабочей папке объявлен как %v, ожидался %v", item.Name, item.WorkspaceWide, expected[item.Name])
		}
		if toolHasWorkspaceWideAccess(item.Name) != expected[item.Name] {
			t.Errorf("%s: движок и каталог разошлись", item.Name)
		}
	}
	// Пользовательский инструмент в каталоге не объявлен, и по умолчанию
	// считается видящим всё.
	if !toolHasWorkspaceWideAccess("customtool_deploy") {
		t.Error("пользовательский инструмент перестал считаться видящим всю папку")
	}
}
