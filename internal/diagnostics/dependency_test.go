package diagnostics

import "testing"

func TestConfirmedMissingDependencies(t *testing.T) {
	for _, text := range []string{"Module ts-jest in the transform option was not found.", "Cannot find package '@eslint/js' imported from eslint.config.mjs"} {
		f, ok := DiagnoseCommand(CommandRun{ExitCode: 1, Stderr: text, MissingDependencies: []string{"ts-jest", "@eslint/js"}})
		if !ok || f.Class != FailureRuntime || f.Signature != "dependency_missing" {
			t.Fatal(f)
		}
	}
	f, _ := DiagnoseCommand(CommandRun{ExitCode: 2, Stderr: "src/main.ts(3,4): error TS2322: Type 'string' is not assignable to type 'number'.", MissingDependencies: []string{"ts-jest"}})
	if f.Class != FailureCode {
		t.Fatal(f)
	}
	f, _ = DiagnoseCommand(CommandRun{ExitCode: 1, Stderr: "Cannot find package '@eslint/js'"})
	if f.Class != FailureCode {
		t.Fatal("unconfirmed absence classified as environment", f)
	}
}
