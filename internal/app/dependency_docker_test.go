package app

import (
	"context"
	"encoding/json"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/environment"
	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
	"os"
	"path/filepath"
	"testing"
)

func TestDependencyPreparationDocker(t *testing.T) {
	if os.Getenv("POINT_DEPENDENCY_DOCKER") != "1" {
		t.Skip("set POINT_DEPENDENCY_DOCKER=1 with Docker")
	}
	for _, mode := range []string{"bind", "volume"} {
		for _, manager := range []string{"npm", "go", "composer"} {
			t.Run(mode+"-"+manager, func(t *testing.T) {
				f := newVerificationFixture(t)
				root := t.TempDir()
				var files map[string]string
				command := ""
				if manager == "npm" {
					files = map[string]string{"lk-backend/source/package.json": `{"name":"probe","version":"1.0.0","devDependencies":{"tool":"file:deps/tool"}}`, "lk-backend/source/package-lock.json": `{"name":"probe","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"probe","version":"1.0.0","devDependencies":{"tool":"file:deps/tool"}},"deps/tool":{"version":"1.0.0","dev":true},"node_modules/tool":{"resolved":"deps/tool","link":true}}}`, "lk-backend/source/deps/tool/package.json": `{"name":"tool","version":"1.0.0","main":"index.js"}`, "lk-backend/source/deps/tool/index.js": "module.exports=42"}
					command = "cd lk-backend/source && node -e \"if(require('tool')!==42) process.exit(1)\""
				} else if manager == "go" {
					files = map[string]string{"go.mod": "module probe\n\ngo 1.25\n\nrequire example.com/tool v0.0.0\nreplace example.com/tool => ./tool\n", "tool/go.mod": "module example.com/tool\n\ngo 1.25\n", "tool/tool.go": "package tool\nconst Answer=42\n", "main_test.go": "package probe\nimport(\"testing\";\"example.com/tool\")\nfunc TestAnswer(t *testing.T){if tool.Answer!=42{t.Fatal(tool.Answer)}}\n", "vendor/modules.txt": "# example.com/tool v0.0.0 => ./tool\n## explicit; go 1.25\nexample.com/tool\n# example.com/tool => ./tool\n"}
					command = "mkdir -p .point/tmp && GOTMPDIR=/workspace/.point/tmp go test -mod=vendor ./..."
				} else {
					files = map[string]string{"composer.json": `{"name":"point/probe","repositories":[{"type":"path","url":"tool","options":{"symlink":false}},{"packagist.org":false}],"require-dev":{"point/tool":"1.0.0"}}`, "tool/composer.json": `{"name":"point/tool","version":"1.0.0","autoload":{"files":["tool.php"]}}`, "tool/tool.php": "<?php function point_probe(){return 42;}"}
					command = `php -r 'require "vendor/autoload.php"; if(point_probe()!==42) exit(1);'`
				}
				for name, body := range files {
					p := filepath.Join(root, filepath.FromSlash(name))
					os.MkdirAll(filepath.Dir(p), 0755)
					if e := os.WriteFile(p, []byte(body), 0644); e != nil {
						t.Fatal(e)
					}
				}
				args, _ := json.Marshal(map[string]string{"command": command})
				criteria := []domain.AcceptanceCriterion{{ID: "verify", Kind: "verification", Tool: "run_command", Arguments: args}}
				b := sandbox.NewContainerBackend(t.TempDir())
				if manager == "composer" {
					b.Image = "point-agent-sandbox-php:1.3.1"
				}
				if err := b.Probe(context.Background()); err != nil {
					t.Fatal(err)
				}
				if manager == "composer" {
					fs, err := workspace.Open(root)
					if err != nil {
						t.Fatal(err)
					}
					tool := tools.RunCommand{FS: fs, Executor: b, SandboxImage: b.Image, NetworkPolicy: "DENY"}
					out := tool.Execute(context.Background(), json.RawMessage(`{"command":"composer update --no-install --no-interaction --no-scripts","reason":"create offline fixture lock","timeoutSeconds":60}`))
					if !out.OK || toolResultExitCode(out) != 0 {
						t.Fatalf("fixture lock: %+v", out)
					}
				}
				plan := environment.DependencyPlanFor(root, criteria)
				if err := b.Probe(context.Background()); err != nil {
					t.Fatal(err)
				}
				record, err := b.Create(context.Background(), sandbox.CreateRequest{WorkspaceID: f.quest.WorkspaceID, QuestID: f.quest.ID, ExecutionID: f.execution.ID, WorkspacePath: root, StorageMode: mode, FileRulesVersion: filepolicy.Current})
				if err != nil {
					t.Fatal(err)
				}
				defer b.Close(context.Background(), record, root)
				f.app.sandboxBackend = b
				result, err := f.app.runCriteriaBatch(criteriaBatchInput{Context: context.Background(), Sandbox: record, QuestID: f.quest.ID, Criteria: criteria, NetworkPolicy: "DENY", WorkOrder: &domain.WorkOrder{Dependencies: plan}})
				if err != nil || !result.AllOK {
					t.Fatalf("batch=%+v err=%v", result, err)
				}
			})
		}
	}
}
