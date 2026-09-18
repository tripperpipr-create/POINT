package domain

// ToolCatalogItem describes a built-in capability in a UI-friendly, stable
// format. Execution remains controlled by the server-side registry and policy.
type ToolCatalogItem struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	// Category — она же группа доступа. Сущностям (помощник, оркестратор,
	// агент) выдаются группы, а не перечисления имён: иначе каждый новый
	// инструмент требует правки в каждом списке, и списки расходятся молча.
	// Пустой быть не может: это проверяет сверка реестра с каталогом в
	// internal/agent/tool_catalog_test.go.
	Category             string `json:"category"`
	Risk                 string `json:"risk"`
	RequiresApproval     bool   `json:"requiresApproval"`
	ProvidesVerification bool   `json:"providesVerification,omitempty"`
	// Replayable — результат инструмента можно показать модели повторно на
	// следующем круге: он ничего не менял, и с тех пор ничего не устарело по
	// его вине. Отсюда берётся список повторяемых в движке агента.
	Replayable bool `json:"replayable,omitempty"`
	// DefaultDeny — инструмент выходит за пределы рабочей папки, и без явного
	// разрешения в профиле ему отказывают. Отсюда берётся политика по
	// умолчанию, а не из отдельного перечисления имён.
	DefaultDeny bool `json:"defaultDeny,omitempty"`
	// WorkspaceWide — инструмент видит произвольные пути рабочей папки и не
	// принимает целевой путь, который движок мог бы отфильтровать. Как только
	// пользователь запретил хоть один путь, такой инструмент на это исполнение
	// становится недоступен.
	WorkspaceWide bool `json:"workspaceWide,omitempty"`
}

// toolCatalogIndex — тот же каталог по имени. Строится один раз: каталог
// неизменен, а спрашивают его на каждой проверке доступа.
var toolCatalogIndex = func() map[string]ToolCatalogItem {
	items := BuiltInToolCatalog()
	index := make(map[string]ToolCatalogItem, len(items))
	for _, item := range items {
		index[item.Name] = item
	}
	return index
}()

// ToolCatalogEntry — единственный способ узнать про встроенный инструмент что
// угодно: риск, группу, повторяемость, политику по умолчанию. Всё, что раньше
// перечисляло имена отдельными списками, спрашивает здесь.
func ToolCatalogEntry(name string) (ToolCatalogItem, bool) {
	item, ok := toolCatalogIndex[name]
	return item, ok
}

// ToolCatalogGroups — группы доступа, встречающиеся в каталоге.
//
// По нему сверяют имена групп, которые кому-то выдают: группа с опечаткой не
// падает, а молча не выдаёт ничего, и такую выдачу нельзя отличить от честно
// пустой.
func ToolCatalogGroups() []string {
	seen := map[string]bool{}
	groups := make([]string, 0, 8)
	for _, item := range BuiltInToolCatalog() {
		if item.Category == "" || seen[item.Category] {
			continue
		}
		seen[item.Category] = true
		groups = append(groups, item.Category)
	}
	return groups
}

// AgentProfileTemplate is a reusable starting point for creating a profile.
// Provider credentials and endpoints are deliberately not part of a template.
type AgentProfileTemplate struct {
	ID                 string       `json:"id"`
	Name               string       `json:"name"`
	Description        string       `json:"description"`
	RoleDescription    string       `json:"roleDescription"`
	SystemPrompt       string       `json:"systemPrompt"`
	Goals              []string     `json:"goals"`
	Rules              []string     `json:"rules"`
	AllowedTools       []string     `json:"allowedTools"`
	MaxSteps           int          `json:"maxSteps"`
	MaxDurationSeconds int          `json:"maxDurationSeconds"`
	ApprovalMode       ApprovalMode `json:"approvalMode"`
}

func BuiltInToolCatalog() []ToolCatalogItem {
	return []ToolCatalogItem{
		{Name: "list_files", DisplayName: "Структура проекта", Description: "Показывает безопасное дерево файлов рабочей папки.", Category: "read", Risk: "LOW", Replayable: true, WorkspaceWide: true},
		{Name: "project_map", DisplayName: "Карта проекта", Description: "Строит компактный локальный индекс языков, папок и символов без отправки кода модели.", Category: "index", Risk: "LOW", Replayable: true, WorkspaceWide: true},
		{Name: "search_code", DisplayName: "Умный поиск кода", Description: "Возвращает только релевантные фрагменты из локального индекса с лимитом контекста.", Category: "index", Risk: "LOW", Replayable: true, WorkspaceWide: true},
		{Name: "read_file", DisplayName: "Чтение файлов", Description: "Читает текстовые файлы с номерами строк, исключая секреты.", Category: "read", Risk: "LOW", Replayable: true},
		{Name: "search_text", DisplayName: "Поиск по проекту", Description: "Ищет текст только внутри безопасных файлов workspace.", Category: "read", Risk: "LOW", Replayable: true, WorkspaceWide: true},
		{Name: "git_diff", DisplayName: "Просмотр Git diff", Description: "Показывает status и diff относительно HEAD (staged и unstaged) без записи файлов.", Category: "git", Risk: "LOW", Replayable: true, WorkspaceWide: true},
		{Name: "git_branches", DisplayName: "Git: ветки", Description: "Показывает текущую ветку, локальные и удалённые ветки с upstream и расхождением. Только чтение.", Category: "git", Risk: "LOW", Replayable: true},
		{Name: "git_log", DisplayName: "Git: история", Description: "Показывает последние коммиты: хеш, автор, дату, ссылки и заголовок. Только чтение.", Category: "git", Risk: "LOW", Replayable: true, WorkspaceWide: true},
		{Name: "git_tags", DisplayName: "Git: метки", Description: "Показывает метки с их коммитом, датой и подписью аннотации. Только чтение.", Category: "git", Risk: "LOW", Replayable: true},
		{Name: "read_skill", DisplayName: "Чтение навыка", Description: "Загружает полные инструкции экипированного skill. Добавляется автоматически, если у агента есть навыки.", Category: "skill", Risk: "LOW", Replayable: true},
		{Name: "team_inbox", DisplayName: "Сообщения команды", Description: "Читает адресованные агенту и общие сообщения текущего Flow.", Category: "team", Risk: "LOW", Replayable: true},
		{Name: "team_publish", DisplayName: "Сообщить команде", Description: "Публикует вопрос, блокер, контракт или результат внутри текущего Flow; не меняет права задания.", Category: "team", Risk: "LOW"},
		{Name: "permission_prompt", DisplayName: "Разрешение CLI", Description: "Решает, можно ли выполнить нативный инструмент CLI; запись, shell и сеть остаются за Point MCP.", Category: "team", Risk: "LOW"},
		{Name: "propose_patch", DisplayName: "Изменение файлов", Description: "Готовит точечный или полный diff; запись выполняется только после подтверждения.", Category: "write", Risk: "HIGH", RequiresApproval: true},
		{Name: "run_command", DisplayName: "Запуск команд", Description: "Запускает ограниченную по времени команду после явного разрешения. Успешный результат может служить доказательством готовности квеста.", Category: "execute", Risk: "CRITICAL", RequiresApproval: true, ProvidesVerification: true, WorkspaceWide: true},
		{Name: "docker_inspect", DisplayName: "Docker: обзор", Description: "Читает статус Docker CLI/демона, список контейнеров и образов, хвост логов. Без start/stop/удаления.", Category: "docker", Risk: "LOW"},
		{Name: "docker_control", DisplayName: "Docker: start/stop", Description: "Запускает или останавливает контейнер только после подтверждения. Удаление (rm/rmi/prune) недоступно.", Category: "docker", Risk: "CRITICAL", RequiresApproval: true},
		{Name: "ssh_test_connection", DisplayName: "Проверка SSH", Description: "Проверяет сохранённый SSH-профиль (ключ или ssh-agent). По умолчанию DENY; нужен network:<host>=ALLOW.", Category: "network", Risk: "MEDIUM", RequiresApproval: true, DefaultDeny: true},
		{Name: "ssh_list_remote", DisplayName: "Список на сервере", Description: "Читает удалённый каталог через SSH. По умолчанию DENY; мутаций нет.", Category: "network", Risk: "MEDIUM", RequiresApproval: true, DefaultDeny: true},
		{Name: "ssh_read_remote", DisplayName: "Файл на сервере", Description: "Читает ограниченный UTF-8 предпросмотр удалённого файла. По умолчанию DENY; мутаций нет.", Category: "network", Risk: "MEDIUM", RequiresApproval: true, DefaultDeny: true},
		{Name: "ssh_exec_remote", DisplayName: "Команда на сервере", Description: "Выполняет удалённую SSH-команду после подтверждения. По умолчанию DENY.", Category: "network", Risk: "CRITICAL", RequiresApproval: true, DefaultDeny: true},
		{Name: "db_list_connections", DisplayName: "Список БД", Description: "Показывает сохранённые подключения к SQLite/PostgreSQL/MySQL без секретов.", Category: "database", Risk: "LOW"},
		{Name: "db_schema", DisplayName: "Схема БД", Description: "Читает таблицы и колонки сохранённого подключения.", Category: "database", Risk: "MEDIUM", RequiresApproval: true, DefaultDeny: true},
		{Name: "db_query", DisplayName: "SQL-запрос", Description: "Выполняет только читающий SQL по сохранённому подключению. По умолчанию DENY.", Category: "database", Risk: "MEDIUM", RequiresApproval: true, DefaultDeny: true},
		{Name: "db_exec", DisplayName: "SQL-запись", Description: "Выполняет записывающий SQL только после явного подтверждения. По умолчанию DENY.", Category: "database", Risk: "CRITICAL", RequiresApproval: true, DefaultDeny: true},
	}
}

func BuiltInAgentTemplates() []AgentProfileTemplate {
	return []AgentProfileTemplate{
		{
			ID: "developer", Name: "Разработчик", Description: "Исследует проект, вносит изменения и проверяет их тестами.",
			RoleDescription: "Универсальный инженер-разработчик, ориентированный на корректные минимальные изменения.",
			SystemPrompt:    "Ты опытный инженер-разработчик. Сначала изучи относящийся к задаче код и ограничения проекта. Составь короткий план и выполняй только необходимые действия. Изменения предлагай через diff, не обходи подтверждения и обязательно проверь результат подходящими тестами. Не утверждай, что работа завершена, пока это не подтверждено инструментами. Не описывай вызовы инструментов в ответе — IDE показывает их сама.",
			Goals:           []string{"Реализовать задачу минимальным корректным изменением", "Оставить проект в проверенном рабочем состоянии"},
			Rules:           []string{"Сначала использовать карту и поиск кода", "Не менять нерелевантные файлы", "Опасные действия выполнять только после подтверждения", "После изменений запускать узкий verifier; предпочитать process-инструмент с providesVerification, если он доступен"},
			AllowedTools:    []string{"project_map", "search_code", "list_files", "read_file", "search_text", "git_diff", "propose_patch", "run_command"},
			MaxSteps:        30, MaxDurationSeconds: 900, ApprovalMode: ApprovalSafe,
		},
		{
			ID: "reviewer", Name: "Ревьюер", Description: "Проводит доказательное ревью без изменения файлов.",
			RoleDescription: "Старший ревьюер кода, который ищет ошибки, риски и пробелы в тестах.",
			SystemPrompt:    "Ты строгий и конструктивный ревьюер кода. Изучи реализацию и связанные тесты, проверяй каждое замечание по исходникам и ранжируй выводы по серьёзности. Не изменяй файлы. Для каждого замечания укажи конкретный файл, причину, сценарий отказа и минимальное направление исправления. Если существенных проблем нет, скажи об этом прямо.",
			Goals:           []string{"Найти доказуемые дефекты и риски без изменения проекта"},
			Rules:           []string{"Каждое замечание связывать с файлом и сценарием отказа", "Не выдавать предположение за подтверждённый дефект"},
			AllowedTools:    []string{"project_map", "search_code", "list_files", "read_file", "search_text", "git_diff"},
			MaxSteps:        24, MaxDurationSeconds: 600, ApprovalMode: ApprovalSafe,
		},
		{
			ID: "tester", Name: "Инженер по тестированию", Description: "Находит сценарии отказа, запускает проверки и усиливает тесты.",
			RoleDescription: "Инженер по качеству, специализирующийся на воспроизводимых проверках и регрессиях.",
			SystemPrompt:    "Ты инженер по тестированию. Сначала определи наблюдаемое поведение и риски, затем найди существующие тесты и воспроизведи проблему. Команды запускай только с ясной причиной. Если нужны новые тесты или минимальное исправление, предложи diff и после принятия повторно выполни релевантные проверки. Отделяй подтверждённые результаты от предположений.",
			Goals:           []string{"Получить воспроизводимый результат проверки", "Закрыть регрессию тестом"},
			Rules:           []string{"Сначала найти существующие проверки", "Фиксировать фактическую команду и результат", "После изменений запускать узкий verifier; предпочитать process-инструмент с providesVerification, если он доступен"},
			AllowedTools:    []string{"project_map", "search_code", "list_files", "read_file", "search_text", "git_diff", "propose_patch", "run_command"},
			MaxSteps:        35, MaxDurationSeconds: 1200, ApprovalMode: ApprovalSafe,
		},
		{
			ID: "analyst", Name: "Аналитик проекта", Description: "Разбирается в любых текстовых данных и объясняет систему без изменений.",
			RoleDescription: "Технический аналитик, который превращает исходные данные проекта в проверяемые выводы.",
			SystemPrompt:    "Ты технический аналитик. Исследуй только данные, относящиеся к запросу, связывай выводы с конкретными файлами и явно отмечай неизвестное. Не изменяй файлы и не запускай команды. В конце дай структурированное резюме, ключевые факты, риски и следующие разумные действия.",
			Goals:           []string{"Дать компактную проверяемую карту системы без изменения файлов"},
			Rules:           []string{"Ссылаться на конкретные исходники", "Явно отмечать неизвестное и допущения"},
			AllowedTools:    []string{"project_map", "search_code", "list_files", "read_file", "search_text", "git_diff"},
			MaxSteps:        24, MaxDurationSeconds: 600, ApprovalMode: ApprovalSafe,
		},
	}
}
