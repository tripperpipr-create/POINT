package environment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRequestsHTTP(t *testing.T) {
	reqs := parseRequestsHTTP(`### Calculate Price
POST http://127.0.0.1:8337/calculate-price
Accept: application/json
Content-Type: application/json

{
  "product": 1
}

### Purchase
POST http://127.0.0.1:8337/purchase

{"product":1}
`)
	if len(reqs) != 2 {
		t.Fatalf("reqs=%#v", reqs)
	}
	if reqs[0].Method != "POST" || reqs[0].Path != "/calculate-price" || !strings.Contains(reqs[0].Body, `"product"`) {
		t.Fatalf("first=%#v", reqs[0])
	}
	if reqs[1].Path != "/purchase" {
		t.Fatalf("second=%#v", reqs[1])
	}
}

func TestResolvePHPVerificationCommandBuildsSmoke(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "requests.http"), []byte("POST http://127.0.0.1:8337/calculate-price\n\n{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ResolvePHPVerificationCommand(root, PHPRequestsHTTPSmoke)
	if got != "php .point/http-smoke.php" {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".point", "http-smoke.php")); err != nil {
		t.Fatal(err)
	}
	legacy := ResolvePHPVerificationCommand(root, "php bin/phpunit")
	if legacy != "php .point/http-smoke.php" {
		t.Fatalf("legacy rewrite=%q", legacy)
	}
}
