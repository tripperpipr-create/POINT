package workspace

import "strings"

// Правила проекта — то, что команда записала для агентов в корне репозитория:
// AGENTS.md и CLAUDE.md. Мастер их не читал вовсе, и на вопрос «как у нас
// принято?» отвечал по догадке, хотя ответ лежал в корне проекта.
//
// Правила — данные проекта, а не инструкции системы: они не меняют права,
// политику и утверждённый наряд. Кто их подмешивает, помечает их недоверенными.
var projectRuleFiles = []string{"AGENTS.md", "CLAUDE.md"}

// MaxProjectRulesBytes — сколько правил доезжает до модели. Больше — уже не
// правила, а документация: её модель прочтёт инструментом, когда понадобится.
const MaxProjectRulesBytes = 16 * 1024

type ProjectRules struct {
	Sources   []string
	Text      string
	Truncated bool
}

// ProjectRules читает файлы правил из корня рабочей папки. Отсутствующий или
// нечитаемый файл пропускается: правил может не быть, и это не ошибка.
func (f *FS) ProjectRules() ProjectRules {
	var rules ProjectRules
	var text strings.Builder
	for _, name := range projectRuleFiles {
		content, err := f.readContent(name, false, false)
		if err != nil || strings.TrimSpace(content.Content) == "" {
			continue
		}
		section := "## " + content.Path + "\n" + strings.TrimSpace(content.Content) + "\n"
		if left := MaxProjectRulesBytes - text.Len(); len(section) > left {
			if left <= 0 {
				rules.Truncated = true
				break
			}
			section = truncateUTF8(section, left)
			rules.Truncated = true
		}
		rules.Sources = append(rules.Sources, content.Path)
		text.WriteString(section)
		if rules.Truncated {
			break
		}
	}
	rules.Text = strings.TrimSpace(text.String())
	return rules
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && (value[cut]&0xC0) == 0x80 {
		cut--
	}
	return value[:cut]
}
