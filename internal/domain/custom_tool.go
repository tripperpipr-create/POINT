package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

type CustomToolKind string

const (
	CustomToolCommand CustomToolKind = "command"
	CustomToolProcess CustomToolKind = "process"
)

type CustomToolParameterType string

const (
	CustomToolParameterString        CustomToolParameterType = "string"
	CustomToolParameterInteger       CustomToolParameterType = "integer"
	CustomToolParameterEnum          CustomToolParameterType = "enum"
	CustomToolParameterWorkspacePath CustomToolParameterType = "workspace_path"
)

type CustomToolParameter struct {
	Name        string                  `json:"name"`
	DisplayName string                  `json:"displayName"`
	Description string                  `json:"description"`
	Type        CustomToolParameterType `json:"type"`
	Required    bool                    `json:"required"`
	EnumValues  []string                `json:"enumValues,omitempty"`
	MaxLength   int                     `json:"maxLength,omitempty"`
}

// CustomTool is a user-authored capability. Legacy command tools keep a fixed
// shell command. Process tools execute a fixed program directly (without a
// shell) and substitute only validated typed parameters into individual argv
// templates. Both kinds always require one-time approval.
type CustomTool struct {
	ID                   string                `json:"id"`
	Kind                 CustomToolKind        `json:"kind"`
	DisplayName          string                `json:"displayName"`
	Description          string                `json:"description"`
	Command              string                `json:"command"`
	Program              string                `json:"program,omitempty"`
	Arguments            []string              `json:"arguments,omitempty"`
	Parameters           []CustomToolParameter `json:"parameters,omitempty"`
	ProvidesVerification bool                  `json:"providesVerification,omitempty"`
	CWD                  string                `json:"cwd"`
	TimeoutSeconds       int                   `json:"timeoutSeconds"`
	// Revision растёт при каждом сохранении существующего инструмента. Имя
	// занято одним инструментом, и повторное сохранение — новая его редакция,
	// а не второй `run_tests_2` рядом.
	Revision int `json:"revision,omitempty"`
	// TrustedRuns — сколько раз человек подтвердил успешный прогон этой самой
	// команды. Считается только до порога: дальше подтверждений не будет, и
	// считать станет нечего.
	TrustedRuns int       `json:"trustedRuns,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type CustomToolTemplate struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Tool        CustomTool `json:"tool"`
}

func BuiltInCustomToolTemplates() []CustomToolTemplate {
	return []CustomToolTemplate{
		{ID: "go-tests", Name: "Тесты Go", Description: "Запускает все тесты Go без оболочки.", Tool: CustomTool{Kind: CustomToolProcess, DisplayName: "Запустить тесты Go", Description: "Запускает полный набор тестов Go в проекте.", Program: "go", Arguments: []string{"test", "./..."}, ProvidesVerification: true, CWD: ".", TimeoutSeconds: 300}},
		{ID: "git-status", Name: "Git status", Description: "Показывает краткое состояние репозитория.", Tool: CustomTool{Kind: CustomToolProcess, DisplayName: "Проверить Git status", Description: "Показывает изменённые и новые файлы репозитория.", Program: "git", Arguments: []string{"status", "--short"}, CWD: ".", TimeoutSeconds: 60}},
		{ID: "python-script", Name: "Python-скрипт", Description: "Запускает выбранный агентом скрипт только внутри рабочей папки.", Tool: CustomTool{Kind: CustomToolProcess, DisplayName: "Запустить Python-скрипт", Description: "Запускает существующий Python-скрипт из рабочей папки.", Program: "python", Arguments: []string{"{{script}}"}, Parameters: []CustomToolParameter{{Name: "script", DisplayName: "Скрипт", Description: "Путь к Python-скрипту внутри рабочей папки.", Type: CustomToolParameterWorkspacePath, Required: true, MaxLength: 1024}}, CWD: ".", TimeoutSeconds: 120}},
		{ID: "fixed-command", Name: "Фиксированная команда", Description: "Совместимый режим для составной команды без параметров модели.", Tool: CustomTool{Kind: CustomToolCommand, DisplayName: "Новая фиксированная команда", Description: "Выполняет заранее заданную команду после подтверждения.", Command: "go test ./...", ProvidesVerification: true, CWD: ".", TimeoutSeconds: 120}},
		{ID: "docker-compose-ps", Name: "Docker Compose ps", Description: "Показывает сервисы compose-проекта в рабочей папке.", Tool: CustomTool{Kind: CustomToolProcess, DisplayName: "Docker Compose ps", Description: "Список сервисов текущего docker compose проекта.", Program: "docker", Arguments: []string{"compose", "ps"}, CWD: ".", TimeoutSeconds: 60}},
		{ID: "docker-compose-up", Name: "Docker Compose up", Description: "Поднимает сервисы compose без удаления томов (без -v).", Tool: CustomTool{Kind: CustomToolProcess, DisplayName: "Docker Compose up", Description: "docker compose up -d после подтверждения. Не выполняет down -v / prune.", Program: "docker", Arguments: []string{"compose", "up", "-d"}, CWD: ".", TimeoutSeconds: 300}},
	}
}

// CustomToolTrustThreshold — сколько подтверждённых человеком успешных прогонов
// переводит самодельный инструмент в доверенные.
//
// Предел здесь человеческий, а не технический: сто самодельных инструментов —
// сто окон подтверждения, после чего человек жмёт «да» не читая, и защита
// превращается в ритуал. Пять — это пять раз, когда команду прочитали и с ней
// согласились.
const CustomToolTrustThreshold = 5

// TrustEligible — может ли инструмент вообще накапливать доверие.
//
// Два условия, и оба обязательны. Инструмент должен быть помечен как дающий
// доказательство: доверяют проверяющим прогонам — тестам, линту, сборке, — а не
// правкам. И у него не должно быть свободных параметров: их значение выбирает
// модель, и доверие к инструменту стало бы доверием к любому будущему
// аргументу.
func (t CustomTool) TrustEligible() bool {
	return t.ProvidesVerification && len(t.Parameters) == 0
}

// Trusted — набрал ли инструмент доверие и может ли исполняться без окна.
func (t CustomTool) Trusted() bool {
	return t.TrustEligible() && t.TrustedRuns >= CustomToolTrustThreshold
}

// ExecutableSignature — отпечаток исполняемой части инструмента.
//
// Доверие принадлежит команде, а не названию. Правка имени или описания его
// сохраняет, правка того, что запустится, — обнуляет: иначе под доверенным
// именем однажды окажется другая команда.
func (t CustomTool) ExecutableSignature() string {
	parts := []string{string(t.Kind), t.Command, t.Program, t.CWD, strconv.Itoa(t.TimeoutSeconds)}
	parts = append(parts, t.Arguments...)
	for _, parameter := range t.Parameters {
		parts = append(parts, parameter.Name, string(parameter.Type), strconv.FormatBool(parameter.Required))
		parts = append(parts, parameter.EnumValues...)
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}
