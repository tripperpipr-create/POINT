package policy

import (
	"slices"
	"sort"

	"local-agent-workbench/internal/domain"
)

// Доступ к инструментам принадлежит сущности, а не инструменту.
//
// Помощник читает и не пишет не потому, что инструменты такие, а потому что
// таков спрашивающий. Раньше это было записано отдельным списком имён у
// помощника и другим списком у агента, и каждая сущность проверяла себя дважды
// — при выдаче определений модели и при вызове. Шесть мест с одним правилом
// расходятся так же молча, как расходились девять списков объявлений.
//
// Здесь правило записано один раз. Grants говорит, что выдано; Allows отвечает
// на вопрос «можно ли», и его зовут обе проверки.
type Grants struct {
	// Groups — группы каталога целиком. Новый инструмент в группе достаётся
	// всем, кому группа выдана, без правки списков.
	Groups []string
	// Tools — точечные имена сверх групп. Здесь живут пользовательские
	// инструменты: их в каталоге нет, и группой они не выдаются.
	Tools []string
}

// ProjectReadingGroups — группы, инструменты которых читают проект и ничего в
// нём не меняют. Понятие одно на двоих: из него собран доступ помощника, и по
// нему же ворота готовности решают, умеет ли агент читать. Разъедься эти два
// места — и агент с одним git_diff молча перестал бы считаться читающим.
//
// Навык (`skill`) сюда не входит: read_skill читает надетый навык, а не
// проект. Docker, сеть и базы читают вовсе другие предметы.
func ProjectReadingGroups() []string {
	return []string{"read", "index", "git"}
}

// CompanionGrants — доступ помощника: чтение проекта, локальный индекс и
// справка по git. Ни файлов, ни репозитория, ни процессов он не меняет —
// помощник живёт в боковой панели, отвечает на каждую реплику и не должен
// трогать ничего без квеста и подтверждения.
func CompanionGrants() Grants {
	return Grants{Groups: ProjectReadingGroups()}
}

// SkillGroup — группа инструмента, читающего надетый навык. Выдаётся тому, у
// кого навыки есть: без них read_skill способен только ответить, что грузить
// нечего.
const SkillGroup = "skill"

// WithGroups — копия доступа с дописанными группами. Копия, а не правка на
// месте: базовый доступ сущности — общее значение, и дописать в него группу
// одному вызывающему значило бы раздать её всем.
func (g Grants) WithGroups(groups ...string) Grants {
	next := Grants{
		Groups: append(append([]string(nil), g.Groups...), groups...),
		Tools:  append([]string(nil), g.Tools...),
	}
	return next
}

// ProfileGrants — доступ агента и узла потока, который исполняется под его
// профилем. Пока это перечисление имён в профиле; группы профиль получит
// вместе со своей витриной.
func ProfileGrants(profile domain.AgentProfile) Grants {
	return Grants{Tools: profile.AllowedTools}
}

// Allows — единственное место, где записано правило доступа.
func (g Grants) Allows(tool string) bool {
	if tool == "" {
		return false
	}
	if slices.Contains(g.Tools, tool) {
		return true
	}
	if len(g.Groups) == 0 {
		return false
	}
	item, ok := domain.ToolCatalogEntry(tool)
	if !ok {
		// Имени нет в каталоге — значит это пользовательский инструмент либо
		// опечатка. Пользовательский запускает заранее настроенную команду, и
		// выдавать такое группой нельзя: только поимённо.
		return false
	}
	return slices.Contains(g.Groups, item.Category)
}

// ToolNames — имена, которые сущность может получить. Ими фильтруется список
// определений для модели, поэтому порядок устойчивый: иначе один и тот же
// набор инструментов приезжал бы к модели в разном порядке и ломал кэш промпта
// на ровном месте.
func (g Grants) ToolNames() []string {
	seen := make(map[string]bool, len(g.Tools)+len(g.Groups)*4)
	names := make([]string, 0, len(g.Tools)+len(g.Groups)*4)
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}
	for _, name := range g.Tools {
		add(name)
	}
	if len(g.Groups) > 0 {
		for _, item := range domain.BuiltInToolCatalog() {
			if slices.Contains(g.Groups, item.Category) {
				add(item.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}
