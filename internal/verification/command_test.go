package verification

import "testing"

func TestIsCommandAcceptsKnownVerificationCommands(t *testing.T) {
	for _, command := range []string{
		"go test ./... -count=1",
		"cd internal && go vet ./...",
		"npm test",
		"pnpm run typecheck",
		"yarn lint --fix=false",
		"python -m pytest -q",
		"cargo clippy --all-targets",
		"dotnet build --no-restore",
		"npx eslint src",
		"npx vitest run",
		"npx jest --ci",
		"node --test",
		"deno test",
		"phpunit",
		"php bin/phpunit",
		"vendor/bin/phpunit",
		"vendor/bin/phpunit 2>&1",
		"bundle exec rspec",
		"mix test",
		"flutter test",
		"composer test",
		"cmake --build build --target test",
		".\\gradlew.bat test",
		"./mvnw verify",
		"make",
		"ctest --output-on-failure",
	} {
		if !IsCommand(command) {
			t.Errorf("verification command was rejected: %q", command)
		}
	}
}

func TestIsCommandRejectsWeakOrFailureMaskingCommands(t *testing.T) {
	for _, command := range []string{
		"go version",
		"echo go test ./...",
		"echo tests passed",
		"go test ./... || echo ignored",
		"go test ./... | tee result.txt",
		"go test ./...; exit 0",
		"go test ./... & echo detached",
		"python script.py",
		"npm start",
		"",
	} {
		if IsCommand(command) {
			t.Errorf("weak verification command was accepted: %q", command)
		}
	}
}
