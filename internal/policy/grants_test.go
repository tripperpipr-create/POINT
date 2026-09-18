package policy

import (
	"slices"
	"testing"

	"local-agent-workbench/internal/domain"
)

// Договор помощника проверяется здесь, а не только в приложении: он должен
// держаться правилом, а не тем, какие объекты кто-то не забыл не положить в
// его реестр.
func TestCompanionGrantsCoverReadingAndNothingElse(t *testing.T) {
	grants := CompanionGrants()
	for _, name := range []string{
		"list_files", "read_file", "search_text",
		"project_map", "search_code",
		"git_diff", "git_branches", "git_log", "git_tags",
	} {
		if !grants.Allows(name) {
			t.Errorf("помощник остался без %q", name)
		}
	}
	// Всё, что меняет файлы, репозиторий, процессы или ходит наружу, лежит в
	// других группах и достаться помощнику не может.
	for _, name := range []string{
		"propose_patch", "run_command", "docker_control", "docker_inspect",
		"ssh_read_remote", "ssh_exec_remote", "db_query", "db_exec", "db_list_connections",
	} {
		if grants.Allows(name) {
			t.Errorf("помощнику досталcя %q", name)
		}
	}
	// Навык — своя группа: помощник получит её вместе со скиллами, а пока
	// инструмент только отвечал бы, что надетых навыков нет.
	if grants.Allows("read_skill") {
		t.Error("read_skill выдан помощнику до того, как у него появились навыки")
	}
	// Пользовательский инструмент запускает заранее настроенную команду.
	// Группой такое не выдаётся ни одной сущности.
	if grants.Allows("customtool_deploy") {
		t.Error("пользовательский инструмент достался группой")
	}
	if grants.Allows("") || grants.Allows("no_such_tool") {
		t.Error("выдано несуществующее имя")
	}
}

// Имена едут в запрос к модели: неустойчивый порядок ломал бы кэш промпта на
// одном и том же наборе инструментов.
func TestGrantsToolNamesAreSortedAndUnique(t *testing.T) {
	grants := Grants{Groups: []string{"git", "read"}, Tools: []string{"git_diff", "customtool_deploy"}}
	names := grants.ToolNames()
	if !slices.IsSorted(names) {
		t.Fatalf("порядок неустойчив: %v", names)
	}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			t.Fatalf("имя %q повторяется: %v", name, names)
		}
		seen[name] = true
	}
	if !seen["customtool_deploy"] || !seen["git_log"] || !seen["read_file"] {
		t.Fatalf("выданное имя потеряно: %v", names)
	}
	if seen["propose_patch"] {
		t.Fatalf("невыданное имя добавилось: %v", names)
	}
}

// Профиль агента перечисляет имена, и правило для него то же самое.
func TestProfileGrantsFollowAllowedTools(t *testing.T) {
	grants := ProfileGrants(domain.AgentProfile{AllowedTools: []string{"read_file", "customtool_deploy"}})
	if !grants.Allows("read_file") || !grants.Allows("customtool_deploy") {
		t.Fatalf("перечисленное имя не разрешено: %#v", grants)
	}
	if grants.Allows("run_command") || grants.Allows("git_log") {
		t.Fatalf("неперечисленное имя разрешено: %#v", grants)
	}
}

// Имя группы — строка, и опечатка в ней не падает: такая выдача просто ничего
// не содержит, и отличить её от честно пустой нельзя. Поэтому каждая группа,
// которую кому-то выдают, обязана существовать в каталоге.
func TestGrantedGroupsExistInCatalog(t *testing.T) {
	known := map[string]bool{}
	for _, group := range domain.ToolCatalogGroups() {
		known[group] = true
	}
	granted := append(ProjectReadingGroups(), SkillGroup)
	for _, group := range granted {
		if !known[group] {
			t.Errorf("выдаётся несуществующая группа %q; в каталоге есть только %v", group, domain.ToolCatalogGroups())
		}
	}
	// Обратная сторона: группа, которой не выдают никому, — либо забытая
	// выдача, либо мёртвая запись в каталоге. Здесь это не ошибка, но список
	// должен быть виден.
	if len(known) == 0 {
		t.Fatal("каталог не объявил ни одной группы")
	}
}

// WithGroups отдаёт копию. Правка на месте раздала бы дописанную группу всем,
// кто получил базовый доступ той же сущности.
func TestWithGroupsDoesNotMutateTheBaseGrant(t *testing.T) {
	base := CompanionGrants()
	extended := base.WithGroups(SkillGroup)
	if !extended.Allows("read_skill") {
		t.Fatal("дописанная группа не выдана")
	}
	if base.Allows("read_skill") {
		t.Fatal("базовая выдача изменилась на месте")
	}
	if len(base.Groups) != len(ProjectReadingGroups()) {
		t.Fatalf("базовая выдача выросла: %v", base.Groups)
	}
	// Ещё одна копия от того же основания не должна унести чужую группу.
	if again := CompanionGrants(); again.Allows("read_skill") {
		t.Fatalf("общее значение доступа испорчено: %v", again.Groups)
	}
}

// Доверие перебивает только умолчание. Всё, что человек сказал явно, — запись в
// политике профиля, режим «подтверждать всё», запрет — сильнее накопленного
// счётчика: счётчик набирается сам, а указание даёт человек.
func TestTrustedCustomToolOverridesOnlyTheDefault(t *testing.T) {
	engine := Engine{TrustedCustomTool: func(name string) bool { return name == "customtool_tests" }}
	base := domain.AgentProfile{ApprovalMode: domain.ApprovalSafe}

	if decision := engine.Evaluate(base, "customtool_tests"); decision.RequiresApproval || decision.Policy != domain.ToolPolicyAllow {
		t.Fatalf("доверенный инструмент всё ещё спрашивает: %#v", decision)
	}
	if decision := engine.Evaluate(base, "customtool_other"); !decision.RequiresApproval {
		t.Fatalf("недоверенный инструмент перестал спрашивать: %#v", decision)
	}
	if decision := (Engine{}).Evaluate(base, "customtool_tests"); !decision.RequiresApproval {
		t.Fatalf("движок без справки о доверии начал доверять: %#v", decision)
	}

	explicit := base
	explicit.ToolPolicies = map[string]string{"customtool_tests": "ASK"}
	if decision := engine.Evaluate(explicit, "customtool_tests"); !decision.RequiresApproval {
		t.Fatalf("явное ASK перебито доверием: %#v", decision)
	}
	denied := base
	denied.ToolPolicies = map[string]string{"customtool_tests": "DENY"}
	if decision := engine.Evaluate(denied, "customtool_tests"); !decision.Denied {
		t.Fatalf("запрет перебит доверием: %#v", decision)
	}
	always := base
	always.ApprovalMode = domain.ApprovalAlways
	if decision := engine.Evaluate(always, "customtool_tests"); !decision.RequiresApproval {
		t.Fatalf("режим «подтверждать всё» перебит доверием: %#v", decision)
	}
	// Встроенных инструментов доверие не касается вовсе.
	if decision := (Engine{TrustedCustomTool: func(string) bool { return true }}).Evaluate(base, "run_command"); !decision.RequiresApproval {
		t.Fatalf("доверие протекло на встроенный инструмент: %#v", decision)
	}
}
