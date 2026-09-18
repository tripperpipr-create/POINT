package verification

import (
	"regexp"
	"strings"
)

var commandPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^go\s+(?:test|vet|build)(?:\s|$)`),
	regexp.MustCompile(`^(?:npm|pnpm|yarn|bun)\s+(?:(?:run\s+)?(?:test|build|lint|typecheck|check|ci))(?:\s|$)`),
	regexp.MustCompile(`^(?:python(?:3)?\s+-m\s+)?pytest(?:\s|$)`),
	regexp.MustCompile(`^cargo\s+(?:test|check|clippy|build)(?:\s|$)`),
	regexp.MustCompile(`^dotnet\s+(?:test|build)(?:\s|$)`),
	regexp.MustCompile(`^(?:npx\s+)?(?:tsc|eslint|vitest|jest)(?:\s|$)`),
	regexp.MustCompile(`^node\s+--test(?:\s|$)`),
	regexp.MustCompile(`^deno\s+test(?:\s|$)`),
	regexp.MustCompile(`^ctest(?:\s|$)`),
	regexp.MustCompile(`^cmake\s+--build(?:\s|$)`),
	regexp.MustCompile(`^(?:(?:make|ninja)$|(?:make|ninja)\s+(?:test|check|build|all)(?:\s|$))`),
	regexp.MustCompile(`^(?:mvn|mvnw|mvnw\.cmd)\s+(?:test|verify|package)(?:\s|$)`),
	regexp.MustCompile(`^(?:gradle|gradlew|gradlew\.bat)\s+(?:test|check|build)(?:\s|$)`),
	regexp.MustCompile(`^(?:php\s+)?(?:vendor/bin/phpunit|bin/phpunit|phpunit)(?:\s|$)`),
	regexp.MustCompile(`^point-php-requests-http-smoke(?:\s|$)`),
	regexp.MustCompile(`^php\s+\.point/http-smoke\.php(?:\s|$)`),
	regexp.MustCompile(`^(?:bundle\s+exec\s+)?rspec(?:\s|$)`),
	regexp.MustCompile(`^mix\s+test(?:\s|$)`),
	regexp.MustCompile(`^flutter\s+test(?:\s|$)`),
	regexp.MustCompile(`^composer\s+(?:test|check)(?:\s|$)`),
}

var redirectSuffix = regexp.MustCompile(`\s+2>&1\s*$`)

var explicitTaskPattern = regexp.MustCompile(`(?i)(` +
	`запуст(?:и|ить|ите)\s+(?:все\s+)?тест|` +
	`прогон(?:и|ите|ять)\s+(?:все\s+)?тест|` +
	`тесты?\s+(?:должн\S*\s+)?(?:проход|зел[её]н)|` +
	`проверь\S*\s+(?:сборк|линт|компиляц|тест)|` +
	`критер(?:ий|ии)\s+готовност|` +
	`acceptance\s+criteria|` +
	`должен\s+проходить\s+(?:тест|сборк)|` +
	`(?:go|cargo)\s+test|` +
	`npm\s+(?:run\s+)?test|` +
	`pytest|` +
	`run\s+(?:the\s+)?tests?|` +
	`tests?\s+(?:must\s+)?pass|` +
	`must\s+pass\s+tests?|` +
	`run\s+(?:the\s+)?(?:build|lint|typecheck)|` +
	`(?:build|lint|typecheck)\s+(?:must\s+)?pass` +
	`)`) //nolint:lll

// TaskRequires reports whether the task text explicitly demands executable
// test/build/lint evidence rather than a text-only review or explanation.
func TaskRequires(task string) bool { return explicitTaskPattern.MatchString(task) }

// IsCommand reports whether a shell command contains a conservative, recognizable
// test/build/lint/static-analysis step and preserves that step's failure status.
// Pipelines, alternative branches and unconditional separators are rejected
// because they can turn a failing verifier into an exit-zero shell result.
func IsCommand(command string) bool {
	normalized := strings.ToLower(strings.TrimSpace(command))
	if normalized == "" || strings.ContainsAny(normalized, "\r\n;|") {
		return false
	}
	normalized = strings.TrimSpace(redirectSuffix.ReplaceAllString(normalized, ""))
	withoutAnd := strings.ReplaceAll(normalized, "&&", "")
	if strings.Contains(withoutAnd, "&") {
		return false
	}
	recognized := false
	for _, segment := range strings.Split(normalized, "&&") {
		segment = normalizeExecutable(strings.TrimSpace(segment))
		for _, pattern := range commandPatterns {
			if pattern.MatchString(segment) {
				recognized = true
				break
			}
		}
	}
	return recognized
}

// CanonicalPHPUnitCommand maps equivalent PHPUnit invocations to one identity
// so `php bin/phpunit` and `vendor/bin/phpunit` count as the same criterion.
func CanonicalPHPUnitCommand(command string) string {
	normalized := strings.ToLower(strings.TrimSpace(command))
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	switch {
	case normalized == "phpunit",
		strings.HasPrefix(normalized, "phpunit "),
		normalized == "vendor/bin/phpunit",
		strings.HasPrefix(normalized, "vendor/bin/phpunit "),
		normalized == "bin/phpunit",
		strings.HasPrefix(normalized, "bin/phpunit "),
		normalized == "php bin/phpunit",
		strings.HasPrefix(normalized, "php bin/phpunit "),
		normalized == "php vendor/bin/phpunit",
		strings.HasPrefix(normalized, "php vendor/bin/phpunit "),
		normalized == "point-php-requests-http-smoke",
		strings.HasPrefix(normalized, "point-php-requests-http-smoke "),
		normalized == "php .point/http-smoke.php",
		strings.HasPrefix(normalized, "php .point/http-smoke.php "):
		return "phpunit"
	default:
		return ""
	}
}

func normalizeExecutable(segment string) string {
	for _, prefix := range []string{"./", `.\`} {
		if strings.HasPrefix(segment, prefix) {
			return strings.TrimPrefix(segment, prefix)
		}
	}
	return segment
}
