package diagnostics

import (
	"strings"
	"testing"
)

// Вывод приёмки квеста 30.09.2026 (сокращён): npm 12 заблокировал скрипты
// установки, vue-demi осталась с вариантом для Vue 3, и rollup упал.
const blockedScriptsBuildStderr = `npm warn deprecated vue@2.7.16: Vue 2 has reached EOL and is no longer actively maintained.
npm warn install-scripts 7 packages had install scripts blocked because they are not covered by allowScripts:
npm warn install-scripts   @parcel/watcher@2.5.6 (install: node scripts/build-from-source.js)
npm warn install-scripts   cpu-features@0.0.10 (install: node buildcheck.js > buildcheck.gypi && node-gyp rebuild)
npm warn install-scripts   vue-demi@0.14.10 (postinstall: node -e "try{require('./scripts/postinstall.js')}catch(e){}")
npm warn install-scripts   ssh2@1.17.0 (install: node install.js)
npm warn install-scripts   vue-demi@0.13.11 (postinstall: node ./scripts/postinstall.js)
npm notice New minor version of npm available! 12.0.2 -> 12.1.0
../fonts/Display-Regular.ttf referenced in ../fonts/Display-Regular.ttf didn't resolve at build time, it will remain unchanged to be resolved at runtime
x Build failed in 17.43s
error during build:
node_modules/pinia/dist/pinia.mjs (6:9): "hasInjectionContext" is not exported by "node_modules/pinia/node_modules/vue-demi/lib/index.mjs", imported by "node_modules/pinia/dist/pinia.mjs".
npm error code 1
npm error command sh -c npm run build:embed`

func TestDiagnoseBlockedInstallScriptsNamesPackagesAndBuildConsequence(t *testing.T) {
	failure, ok := DiagnoseCommand(CommandRun{Command: "cd app && npm ci && npm run verify", ExitCode: 1, Stderr: blockedScriptsBuildStderr})
	if !ok || failure.Class != FailureRuntime || failure.Signature != "npm_install_scripts_blocked" {
		t.Fatalf("failure=%#v", failure)
	}
	for _, want := range []string{"npm 12 заблокировал скрипты установки", "vue-demi", "ssh2", "hasInjectionContext"} {
		if !strings.Contains(failure.Cause, want) {
			t.Fatalf("cause %q lacks %q", failure.Cause, want)
		}
	}
	if strings.Count(failure.Cause, "vue-demi") != 2 { // один раз в списке пакетов, один раз в сборке
		t.Fatalf("package list repeats names: %q", failure.Cause)
	}
	if !strings.Contains(failure.Hint, "allowScripts") {
		t.Fatalf("hint=%q", failure.Hint)
	}
}

func TestDiagnoseMissingPackDestinationIsCriterionDefect(t *testing.T) {
	failure, ok := DiagnoseCommand(CommandRun{
		Command:  "cd app && npm ci && npm pack --pack-destination /tmp/app-pack && tar -tzf /tmp/app-pack/*.tgz",
		ExitCode: 254,
		Stderr:   "npm error code ENOENT\nnpm error syscall open\nnpm error path /tmp/app-pack/app-1.0.0.tgz\nnpm error errno -2\nnpm error enoent ENOENT: no such file or directory, open '/tmp/app-pack/app-1.0.0.tgz'",
	})
	if !ok || failure.Class != FailureCriterion || !strings.Contains(failure.Hint, "mkdir -p /tmp/app-pack") {
		t.Fatalf("failure=%#v", failure)
	}
}

func TestDiagnoseMissingProgramNamesIt(t *testing.T) {
	failure, _ := DiagnoseCommand(CommandRun{Command: "docker compose up", ExitCode: 127, Stderr: "/bin/sh: docker: not found"})
	if failure.Class != FailureRuntime || failure.Cause != "в образе нет программы docker" {
		t.Fatalf("failure=%#v", failure)
	}
}

func TestDiagnoseDeniedHostNeedsHuman(t *testing.T) {
	failure, _ := DiagnoseCommand(CommandRun{ExitCode: 1, DeniedHosts: []string{"nodejs.org:443"}})
	if failure.Class != FailureHuman || !strings.Contains(failure.Cause, "nodejs.org:443") {
		t.Fatalf("failure=%#v", failure)
	}
}

func TestDiagnoseTransientNetworkFailure(t *testing.T) {
	failure, _ := DiagnoseCommand(CommandRun{ExitCode: 1, Stderr: "npm error network read tcp 172.18.0.3:41234->104.16.0.35:443: wsarecv: An existing connection was forcibly closed"})
	if failure.Class != FailureTransient {
		t.Fatalf("failure=%#v", failure)
	}
}

func TestDiagnosePlainBuildErrorSkipsSummaryLine(t *testing.T) {
	failure, _ := DiagnoseCommand(CommandRun{ExitCode: 1, Stderr: "x Build failed in 3.2s\nerror during build:\nsrc/main.ts (4:2): Unexpected token\n    at parse"})
	if failure.Class != FailureCode || failure.Cause != "сборка: src/main.ts (4:2): Unexpected token" {
		t.Fatalf("failure=%#v", failure)
	}
}

func TestDiagnoseTimeoutAndSuccess(t *testing.T) {
	if _, ok := DiagnoseCommand(CommandRun{ExitCode: 0, Stderr: "npm warn install-scripts 1 packages had install scripts blocked"}); ok {
		t.Fatal("a passing command has no failure cause, whatever it warned")
	}
	failure, _ := DiagnoseCommand(CommandRun{ExitCode: -1, TimedOut: true, Timeout: "5m0s"})
	if failure.Signature != "timeout" || !strings.Contains(failure.Cause, "5m0s") {
		t.Fatalf("failure=%#v", failure)
	}
}
