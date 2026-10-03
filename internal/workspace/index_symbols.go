package workspace

import (
	"regexp"
	"strings"
)

// Символы фрагмента индекса: объявления и маршруты.
//
// Прежний разбор брал строки с префиксом «func », «class » и т. п. и терял
// почти всё, по чему ищут реализацию: Go-метод `func (r *T) Name(` давал
// пустое имя, PHP-методы (`public function x`), TS-методы и стрелочные
// функции не попадали вовсе, маршрутов (`/api/documents`) не было. search_code
// по имени метода или пути API ничего не находил, и Мастер уходил в медленный
// search_text (разбор 03.10). Теперь разбор знает объявления своих языков, а
// маршруты популярных фреймворков становятся символами — строкой пути.

type symbolRule struct {
	pattern *regexp.Regexp
	// names — номера групп с именами; пустая группа пропускается.
	names []int
	// joined — группы, склеиваемые точкой в одно имя (тип.метод).
	joined []int
}

func symbolPattern(pattern string, names ...int) symbolRule {
	return symbolRule{pattern: regexp.MustCompile(pattern), names: names}
}

var (
	goSymbolRules = []symbolRule{
		{pattern: regexp.MustCompile(`^func\s+\(\s*\w*\s*\*?\s*([A-Za-z_]\w*)(?:\[[^\]]*\])?\s*\)\s*([A-Za-z_]\w*)`), names: []int{2}, joined: []int{1, 2}},
		symbolPattern(`^func\s+([A-Za-z_]\w*)`, 1),
		symbolPattern(`^type\s+([A-Za-z_]\w*)`, 1),
		symbolPattern(`^\s+([A-Z]\w*)\s+(?:struct|interface)\s*\{`, 1),
	}
	phpSymbolRules = []symbolRule{
		symbolPattern(`^\s*(?:(?:abstract|final|readonly)\s+)*(?:class|interface|trait|enum)\s+([A-Za-z_]\w*)`, 1),
		symbolPattern(`^\s*(?:(?:public|protected|private|static|abstract|final)\s+)*function\s+&?\s*([A-Za-z_]\w*)`, 1),
	}
	scriptSymbolRules = []symbolRule{
		symbolPattern(`^\s*export\s+(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)`, 1),
		symbolPattern(`^\s*(?:async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)`, 1),
		symbolPattern(`^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`, 1),
		symbolPattern(`^\s*(?:export\s+)?(?:interface|type|enum)\s+([A-Za-z_$][\w$]*)`, 1),
		symbolPattern(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::[^=]+)?=\s*(?:async\s+)?(?:function\b|\([^)]*\)\s*(?::[^=]+)?=>|[A-Za-z_$][\w$]*\s*=>)`, 1),
		symbolPattern(`^\s+(?:(?:public|private|protected|static|readonly|async|override|get|set)\s+)*([A-Za-z_$][\w$]*)\s*\([^)]*\)\s*(?::\s*[^{=;]+)?\{\s*$`, 1),
	}
	pythonSymbolRules = []symbolRule{
		symbolPattern(`^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)`, 1),
		symbolPattern(`^\s*class\s+([A-Za-z_]\w*)`, 1),
	}
	// Маршруты — путь целиком, как его ищет человек: «/api/documents».
	routeSymbolRules = []symbolRule{
		symbolPattern(`\b(?:router|app|route|server|api)\s*\.\s*(?:get|post|put|patch|delete|all|use)\s*\(\s*['"\x60](/[^'"\x60\s]*)`, 1),
		symbolPattern(`@(?:Controller|Get|Post|Put|Patch|Delete|All|RequestMapping|GetMapping|PostMapping)\s*\(\s*(?:path\s*[:=]\s*)?['"]([^'"]+)['"]`, 1),
		symbolPattern(`Route::(?:get|post|put|patch|delete|any|match|prefix|resource|apiResource)\s*\(\s*(?:\[[^\]]*\]\s*,\s*)?['"]([^'"]+)['"]`, 1),
		symbolPattern(`(?:#\[|@)Route\s*\(\s*(?:path\s*:\s*)?['"]([^'"]+)['"]`, 1),
		symbolPattern(`\bHandleFunc\s*\(\s*"(?:[A-Z]+\s+)?(/[^"]*)"`, 1),
		symbolPattern(`\.\s*(?:GET|POST|PUT|PATCH|DELETE|Handle|Group)\s*\(\s*"(/[^"]*)"`, 1),
		// Битрикс: urlrewrite.php — 'CONDITION' => '#^/api/documents/#'.
		symbolPattern(`['"]CONDITION['"]\s*=>\s*['"]#\^?(/[^#'"]*)`, 1),
	}
	scriptKeywords = map[string]bool{"if": true, "for": true, "while": true, "switch": true, "catch": true, "return": true, "function": true, "else": true, "do": true, "with": true}
)

func symbolRulesFor(language string) []symbolRule {
	switch language {
	case "Go":
		return goSymbolRules
	case "PHP":
		return phpSymbolRules
	case "JavaScript", "TypeScript":
		return scriptSymbolRules
	case "Python":
		return pythonSymbolRules
	}
	return nil
}

// extractSymbols — имена объявлений и пути маршрутов во фрагменте. Для
// прочих языков остаётся прежний разбор по префиксу строки.
func extractSymbols(language string, lines []string) []string {
	rules := symbolRulesFor(language)
	var result []string
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		result = append(result, value)
	}
	for _, line := range lines {
		line = strings.TrimRight(line, "\r\n")
		matched := false
		for _, item := range rules {
			groups := item.pattern.FindStringSubmatch(line)
			if groups == nil {
				continue
			}
			name := ""
			for _, index := range item.names {
				if index < len(groups) && groups[index] != "" {
					name = groups[index]
				}
			}
			if language != "Go" && scriptKeywords[name] {
				continue
			}
			if len(item.joined) > 0 {
				parts := make([]string, 0, len(item.joined))
				for _, index := range item.joined {
					if index < len(groups) && groups[index] != "" {
						parts = append(parts, groups[index])
					}
				}
				add(strings.Join(parts, "."))
			}
			add(name)
			matched = true
			break
		}
		if !matched && rules == nil {
			add(prefixSymbol(line))
		}
		if strings.ContainsAny(line, "'\"`") {
			for _, item := range routeSymbolRules {
				for _, groups := range item.pattern.FindAllStringSubmatch(line, -1) {
					add(groups[1])
				}
			}
		}
	}
	return result
}

func prefixSymbol(line string) string {
	trimmed := strings.TrimSpace(line)
	for _, prefix := range []string{"func ", "type ", "class ", "interface ", "def ", "function ", "export function ", "export class "} {
		if strings.HasPrefix(trimmed, prefix) {
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			if cut := strings.IndexAny(value, "({:< =\t"); cut >= 0 {
				value = value[:cut]
			}
			return value
		}
	}
	return ""
}
