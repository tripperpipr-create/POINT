package acceptance_test

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

const matrixFixtureVersion = "2"

type runtimeMatrixFixture struct {
	Files                                          map[string]string
	Source, Command, Probe, ProbeCommand, Solution string
	Dependencies                                   *domain.DependencyPlan
	Hosts                                          []string
}

func matrixFixture(stack string) (runtimeMatrixFixture, error) {
	f := runtimeMatrixFixture{Files: map[string]string{}}
	project := domain.DependencyProject{}
	switch stack {
	case "node-typescript":
		f.Source, f.Command = "codes.ts", "npm test"
		f.Solution = `export function parseCodes(input: string): string[] {
const values = input.split(',').map(v=>v.trim()).filter(Boolean);
if(values.some(v=>!/^[A-Za-z0-9-]+$/.test(v))) throw new Error('Invalid product code');
return [...new Set(values.map(v=>v.toUpperCase()))].sort();
}
`
		f.Files["codes.ts"] = "export function parseCodes(input: string): string[] { return []; }\n"
		f.Files["package.json"] = `{"name":"point-runtime-matrix-node","version":"1.0.0","private":true,"type":"module","scripts":{"test":"tsc && node --test codes.test.mjs"},"devDependencies":{"typescript":"5.9.3"}}` + "\n"
		f.Files["package-lock.json"] = `{"name":"point-runtime-matrix-node","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"point-runtime-matrix-node","version":"1.0.0","devDependencies":{"typescript":"5.9.3"}},"node_modules/typescript":{"version":"5.9.3","resolved":"https://registry.npmjs.org/typescript/-/typescript-5.9.3.tgz","integrity":"sha512-jl1vZzPDinLr9eUt3J/t7V6FgNEw9QjvBPdysz9KfQDD41fQrC2Y4vKQdiaUpFT4bXlb1RHhLpp8wtm6M5TgSw==","dev":true,"license":"Apache-2.0","bin":{"tsc":"bin/tsc","tsserver":"bin/tsserver"},"engines":{"node":">=14.17"}}}}` + "\n"
		f.Files["tsconfig.json"] = `{"compilerOptions":{"target":"ES2022","module":"NodeNext","strict":true,"outDir":"dist"},"include":["codes.ts"]}` + "\n"
		f.Files["codes.test.mjs"] = `import test from 'node:test'; import assert from 'node:assert/strict'; import {parseCodes} from './dist/codes.js';
test('normalization and sort',()=>assert.deepEqual(parseCodes(' b2, a-1,B2, '),['A-1','B2']));
test('empty',()=>assert.deepEqual(parseCodes(' , , '),[]));
test('invalid character',()=>assert.throws(()=>parseCodes('a!')));
`
		f.Probe = `import assert from 'node:assert/strict'; import {parseCodes} from './dist/codes.js';
assert.deepEqual(parseCodes(' x-1, 02 ,X-1,z-9'),['02','X-1','Z-9']);
assert.deepEqual(parseCodes(''),[]);
for (const value of ['a/b','a;b','é','a_b','ß','ſ','ı']) assert.throws(()=>parseCodes(value));
`
		f.ProbeCommand = "./node_modules/.bin/tsc && node independent.mjs"
		project = domain.DependencyProject{Manager: "npm", Commands: []domain.SetupCommand{{Command: "npm ci --ignore-scripts --no-audit --no-fund", TimeoutSeconds: 600}}, ManifestPaths: []string{"package.json", "package-lock.json"}, ExpectedPaths: []string{"node_modules/typescript/package.json"}}
		f.Hosts = []string{"registry.npmjs.org:443"}
	case "go":
		f.Source, f.Command = "codes.go", "go test -count=1 ./..."
		f.Solution = `package codes
import("strings";"sort";"fmt")
func ParseCodes(input string)([]string,error){out:=[]string{};seen:=map[string]bool{};for _,part:=range strings.Split(input,","){v:=strings.TrimSpace(part);if v==""{continue};for _,c:=range v{if !(c>='A'&&c<='Z'||c>='a'&&c<='z'||c>='0'&&c<='9'||c=='-'){return nil,fmt.Errorf("invalid product code")}};v=strings.ToUpper(v);if !seen[v]{out=append(out,v);seen[v]=true}};sort.Strings(out);return out,nil}
`
		f.Files["go.mod"] = "module point.local/runtime-matrix\n\ngo 1.24.0\n\nrequire github.com/google/uuid v1.6.0\n"
		f.Files["go.sum"] = "github.com/google/uuid v1.6.0 h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0=\ngithub.com/google/uuid v1.6.0/go.mod h1:TIyPZe4MgqvfeYDBFedMoGGpEw/LqOeaOT+nhxU+yHo=\n"
		f.Files["codes.go"] = "package codes\n\nfunc ParseCodes(input string) ([]string, error) { return []string{}, nil }\n"
		f.Files["codes_test.go"] = `package codes
import("testing"; "reflect"; "github.com/google/uuid")
func TestCodes(t *testing.T) { if _,err:=uuid.Parse(uuid.NewString());err!=nil{t.Fatal(err)}; got,err:=ParseCodes(" b2, a-1,B2, ");if err!=nil||!reflect.DeepEqual(got,[]string{"A-1","B2"}){t.Fatalf("%v %v",got,err)} }
func TestEmpty(t *testing.T){got,err:=ParseCodes(" , , ");if err!=nil||len(got)!=0{t.Fatalf("%v %v",got,err)}}
func TestInvalid(t *testing.T){if _,err:=ParseCodes("a!");err==nil{t.Fatal("invalid code accepted")}}
`
		f.Probe = `package codes
import("testing"; "reflect")
func TestIndependent(t *testing.T){got,err:=ParseCodes(" x-1, 02 ,X-1,z-9");if err!=nil||!reflect.DeepEqual(got,[]string{"02","X-1","Z-9"}){t.Fatalf("%v %v",got,err)};for _,v:=range []string{"a/b","a;b","é","a_b","ß","ſ","ı"}{if _,err:=ParseCodes(v);err==nil{t.Fatalf("accepted %q",v)}};got,err=ParseCodes("");if err!=nil||len(got)!=0{t.Fatal(got,err)}}
`
		f.ProbeCommand = "go test -count=1 -run '^TestIndependent$' ./..."
		project = domain.DependencyProject{Manager: "go", Commands: []domain.SetupCommand{{Command: "go mod download", TimeoutSeconds: 600}}, ManifestPaths: []string{"go.mod", "go.sum"}}
		f.Hosts = []string{"proxy.golang.org:443", "sum.golang.org:443", "storage.googleapis.com:443"}
	case "php":
		f.Source, f.Command = "codes.php", "php codes-test.php"
		f.Solution = `<?php
function parseCodes(string $input): array {$out=[];foreach(explode(',',$input) as $part){$v=strtoupper(trim($part));if($v==='')continue;if(!preg_match('/^[A-Z0-9-]+$/D',$v))throw new InvalidArgumentException('Invalid product code');$out[$v]=true;}$keys=array_map('strval',array_keys($out));sort($keys,SORT_STRING);return $keys;}
`
		f.Files["codes.php"] = "<?php\nfunction parseCodes(string $input): array { return []; }\n"
		manifest := map[string]any{"name": "point/runtime-matrix-php", "require": map[string]string{"php": ">=8.0", "psr/log": "3.0.2"}}
		var encoded bytes.Buffer
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(manifest)
		raw := []byte(strings.TrimSpace(encoded.String()))
		f.Files["composer.json"] = string(raw) + "\n"
		sum := md5.Sum(raw) // Composer's lock freshness format, not a trust digest.
		lock := map[string]any{"content-hash": hex.EncodeToString(sum[:]), "packages": []any{map[string]any{"name": "psr/log", "version": "3.0.2", "source": map[string]string{"type": "git", "url": "https://github.com/php-fig/log.git", "reference": "f16e1d5863e37f8d8c2a01719f5b34baa2b714d3"}, "dist": map[string]string{"type": "zip", "url": "https://api.github.com/repos/php-fig/log/zipball/f16e1d5863e37f8d8c2a01719f5b34baa2b714d3", "reference": "f16e1d5863e37f8d8c2a01719f5b34baa2b714d3", "shasum": ""}, "require": map[string]string{"php": ">=8.0.0"}, "autoload": map[string]any{"psr-4": map[string]string{`Psr\Log\`: "src"}}, "license": []string{"MIT"}}}, "packages-dev": []any{}, "aliases": []any{}, "minimum-stability": "stable", "stability-flags": map[string]any{}, "prefer-stable": false, "prefer-lowest": false, "platform": map[string]string{"php": ">=8.0"}, "platform-dev": map[string]any{}, "plugin-api-version": "2.6.0"}
		raw, _ = json.MarshalIndent(lock, "", "  ")
		f.Files["composer.lock"] = string(raw) + "\n"
		f.Files["codes-test.php"] = `<?php
require 'vendor/autoload.php'; require 'codes.php'; new Psr\Log\NullLogger();
function check(bool $ok): void { if (!$ok) throw new RuntimeException('assertion failed'); }
check(parseCodes(' b2, a-1,B2, ') === ['A-1','B2']); check(parseCodes(' , , ') === []);
$invalid=false; try {parseCodes('a!');} catch(InvalidArgumentException $e){$invalid=true;} check($invalid);
`
		f.Probe = `<?php
require 'codes.php';
if(parseCodes(' x-1, 02 ,X-1,z-9') !== ['02','X-1','Z-9'] || parseCodes('') !== []) throw new RuntimeException('independent result differs');
foreach(['a/b','a;b','é','a_b','ß','ſ','ı'] as $v) { $invalid=false; try{parseCodes($v);}catch(InvalidArgumentException $e){$invalid=true;} if(!$invalid)throw new RuntimeException('invalid code accepted'); }
`
		f.ProbeCommand = "php independent.php"
		project = domain.DependencyProject{Manager: "composer", Commands: []domain.SetupCommand{{Command: "composer install --no-interaction --no-progress --prefer-dist --no-scripts --no-plugins", TimeoutSeconds: 600}}, ManifestPaths: []string{"composer.json", "composer.lock"}, ExpectedPaths: []string{"vendor/autoload.php"}}
		f.Hosts = []string{"repo.packagist.org:443", "api.github.com:443", "codeload.github.com:443"}
	default:
		return f, fmt.Errorf("unknown benchmark project %q", stack)
	}
	f.Files[".gitignore"] = "node_modules/\nvendor/\ndist/\n"
	f.Dependencies = &domain.DependencyPlan{Version: "1", Projects: []domain.DependencyProject{project}}
	return f, domain.ValidateDependencyPlan(f.Dependencies)
}

func TestRuntimeMatrixFixturesPinDependenciesAndChangeBoundary(t *testing.T) {
	for _, stack := range []string{"node-typescript", "go", "php"} {
		f, err := matrixFixture(stack)
		if err != nil || f.Files[f.Source] == "" || f.Probe == "" || f.Command == "" || len(f.Hosts) == 0 {
			t.Fatalf("fixture %s: %+v %v", stack, f, err)
		}
		for _, p := range f.Dependencies.Projects {
			for _, file := range p.ManifestPaths {
				if f.Files[file] == "" {
					t.Fatalf("missing pinned manifest: %s/%s", stack, file)
				}
			}
		}
	}
}

// Verify reference answers against the private behavior probe in a clean Linux
// workspace. This does not call a model or produce full-quest performance proof.
func TestRuntimeMatrixFixtureBehaviorIntegration(t *testing.T) {
	if os.Getenv("POINT_RUNTIME_FIXTURE_BEHAVIOR") != "1" {
		t.Skip("explicit isolated native fixture verification required")
	}
	root := os.Getenv("POINT_BENCH_TRIAL_DIR")
	if !filepath.IsAbs(root) {
		t.Fatal("absolute isolated fixture directory required")
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, stack := range []string{"node-typescript", "go", "php"} {
		t.Run(stack, func(t *testing.T) {
			f, err := matrixFixture(stack)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, stack)
			project := filepath.Join(dir, "reference")
			if err := os.MkdirAll(project, 0755); err != nil {
				t.Fatal(err)
			}
			f.Files[f.Source] = f.Solution
			for name, body := range f.Files {
				if err := os.WriteFile(filepath.Join(project, name), []byte(body), 0644); err != nil {
					t.Fatal(err)
				}
			}
			proof, err := matrixIndependentProbe(t, dir, project, f)
			matrixJSON(t, dir, "behavior-proof.json", map[string]any{"fixtureVersion": matrixFixtureVersion, "sourceDigest": matrixDigest(f.Files), "probeDigest": matrixDigest(f.Probe), "proof": proof, "error": fmt.Sprint(err)})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
