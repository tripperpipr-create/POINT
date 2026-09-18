package environment

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"local-agent-workbench/internal/domain"
)

// PHPRequestsHTTPSmoke is the declared verification command when a Composer
// project ships requests.http but no PHPUnit harness.
const PHPRequestsHTTPSmoke = "point-php-requests-http-smoke"

var httpRequestLine = regexp.MustCompile(`(?i)^(GET|POST|PUT|PATCH|DELETE)\s+(https?://[^/\s]+)(/[^\s]*)?`)

func phpVerificationCommand(root string) *domain.EnvironmentCommand {
	if hasPHPUnitHarness(root) {
		bin := "vendor/bin/phpunit"
		if exists(root, "bin/phpunit") {
			bin = "bin/phpunit"
		}
		return command("tests", "Run PHPUnit", "php", bin)
	}
	if exists(root, "requests.http") {
		return command("tests", "Smoke HTTP endpoints from requests.http via App\\Kernel", PHPRequestsHTTPSmoke)
	}
	return nil
}

func hasPHPUnitHarness(root string) bool {
	if exists(root, "bin/phpunit") || exists(root, "phpunit.xml") || exists(root, "phpunit.xml.dist") {
		return true
	}
	return composerMentionsPHPUnit(root)
}

func composerMentionsPHPUnit(root string) bool {
	raw, err := os.ReadFile(filepath.Join(root, "composer.json"))
	if err != nil {
		return false
	}
	var doc struct {
		Require    map[string]string `json:"require"`
		RequireDev map[string]string `json:"require-dev"`
		Scripts    map[string]any    `json:"scripts"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return false
	}
	for _, block := range []map[string]string{doc.Require, doc.RequireDev} {
		for name := range block {
			if strings.Contains(strings.ToLower(name), "phpunit") {
				return true
			}
		}
	}
	for name := range doc.Scripts {
		if strings.EqualFold(name, "test") || strings.Contains(strings.ToLower(name), "phpunit") {
			return true
		}
	}
	return false
}

func isPHPUnitShellCommand(command string) bool {
	normalized := strings.ToLower(strings.TrimSpace(command))
	normalized = strings.TrimSuffix(normalized, " 2>&1")
	switch {
	case normalized == "phpunit",
		strings.HasPrefix(normalized, "phpunit "),
		normalized == "bin/phpunit",
		strings.HasPrefix(normalized, "bin/phpunit "),
		normalized == "vendor/bin/phpunit",
		strings.HasPrefix(normalized, "vendor/bin/phpunit "),
		normalized == "php bin/phpunit",
		strings.HasPrefix(normalized, "php bin/phpunit "),
		normalized == "php vendor/bin/phpunit",
		strings.HasPrefix(normalized, "php vendor/bin/phpunit "):
		return true
	default:
		return false
	}
}

func resolveExistingPHPUnit(root string) string {
	for _, rel := range []string{"bin/phpunit", "vendor/bin/phpunit"} {
		if exists(root, rel) {
			return "php " + rel
		}
	}
	return ""
}

// ResolvePHPVerificationCommand rewrites declared PHP checks to a runnable shell
// command against the current tip (phpunit binary or requests.http kernel smoke).
func ResolvePHPVerificationCommand(root, command string) string {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return cmd
	}
	if cmd == PHPRequestsHTTPSmoke || strings.HasPrefix(cmd, PHPRequestsHTTPSmoke+" ") {
		if built := buildPHPRequestsHTTPSmokeShell(root); built != "" {
			return built
		}
		return cmd
	}
	if isPHPUnitShellCommand(cmd) {
		if resolved := resolveExistingPHPUnit(root); resolved != "" {
			return resolved
		}
		if exists(root, "requests.http") {
			if built := buildPHPRequestsHTTPSmokeShell(root); built != "" {
				return built
			}
		}
	}
	return cmd
}

type parsedHTTPRequest struct {
	Method string
	Path   string
	Body   string
}

func parseRequestsHTTP(content string) []parsedHTTPRequest {
	var out []parsedHTTPRequest
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		m := httpRequestLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		method := strings.ToUpper(m[1])
		path := m[3]
		if path == "" {
			path = "/"
		}
		i++
		for i < len(lines) {
			hdr := strings.TrimSpace(lines[i])
			if hdr == "" {
				i++
				break
			}
			if strings.HasPrefix(hdr, "###") || httpRequestLine.MatchString(hdr) {
				break
			}
			i++
		}
		var bodyLines []string
		for i < len(lines) {
			raw := lines[i]
			trim := strings.TrimSpace(raw)
			if strings.HasPrefix(trim, "###") || httpRequestLine.MatchString(trim) {
				break
			}
			bodyLines = append(bodyLines, raw)
			i++
		}
		body := strings.TrimSpace(strings.Join(bodyLines, "\n"))
		out = append(out, parsedHTTPRequest{Method: method, Path: path, Body: body})
		i--
	}
	return out
}

func buildPHPRequestsHTTPSmokeShell(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, "requests.http"))
	if err != nil {
		return ""
	}
	reqs := parseRequestsHTTP(string(raw))
	if len(reqs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<?php\ndeclare(strict_types=1);\n")
	b.WriteString("putenv('APP_ENV=test'); $_ENV['APP_ENV']='test'; $_SERVER['APP_ENV']='test';\n")
	b.WriteString("putenv('APP_SECRET=point-http-smoke'); $_ENV['APP_SECRET']='point-http-smoke'; $_SERVER['APP_SECRET']='point-http-smoke';\n")
	b.WriteString("require 'vendor/autoload.php';\n")
	b.WriteString("use Symfony\\Component\\HttpFoundation\\Request;\n")
	b.WriteString("if (!class_exists('App\\\\Kernel')) { fwrite(STDERR, \"App\\\\Kernel missing\\n\"); exit(1); }\n")
	b.WriteString("$kernel = new App\\Kernel('test', true);\n")
	b.WriteString("try { $kernel->boot(); } catch (Throwable $e) {\n")
	b.WriteString("  fwrite(STDERR, 'boot failed: '.$e->getMessage().PHP_EOL);\n")
	b.WriteString("  for ($p=$e->getPrevious(); $p; $p=$p->getPrevious()) fwrite(STDERR, 'caused by: '.$p->getMessage().PHP_EOL);\n")
	b.WriteString("  exit(1);\n}\n")
	b.WriteString("$fail = 0;\n$cases = [\n")
	for _, req := range reqs {
		bodyB64 := base64.StdEncoding.EncodeToString([]byte(req.Body))
		fmt.Fprintf(&b, "  [%q, %q, %q],\n", req.Method, req.Path, bodyB64)
	}
	b.WriteString("];\n")
	b.WriteString(`foreach ($cases as [$method, $path, $bodyB64]) {
  $body = base64_decode($bodyB64, true);
  if ($body === false) { $body = ''; }
  $server = ['CONTENT_TYPE' => 'application/json', 'HTTP_ACCEPT' => 'application/json'];
  $request = Request::create($path, $method, [], [], [], $server, $body);
  try {
    $response = $kernel->handle($request);
  } catch (Throwable $e) {
    echo $method, ' ', $path, ' => EX', PHP_EOL, $e->getMessage(), PHP_EOL;
    for ($p = $e->getPrevious(); $p; $p = $p->getPrevious()) {
      fwrite(STDERR, 'caused by: '.$p->getMessage().PHP_EOL);
    }
    $fail++;
    continue;
  }
  $code = $response->getStatusCode();
  $content = $response->getContent();
  echo $method, ' ', $path, ' => ', $code, PHP_EOL, $content, PHP_EOL;
  if ($code >= 400) {
    $fail++;
    if ($decoded = json_decode($content, true)) {
      if (!empty($decoded['detail'])) fwrite(STDERR, 'detail: '.$decoded['detail'].PHP_EOL);
      if (!empty($decoded['class'])) fwrite(STDERR, 'class: '.$decoded['class'].PHP_EOL);
    }
  }
  $kernel->terminate($request, $response);
}
exit($fail === 0 ? 0 : 1);
`)
	dir := filepath.Join(root, ".point")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	scriptPath := filepath.Join(dir, "http-smoke.php")
	if err := os.WriteFile(scriptPath, []byte(b.String()), 0o600); err != nil {
		return ""
	}
	return "php .point/http-smoke.php"
}
