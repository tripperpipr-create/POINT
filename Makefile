.PHONY: test check-docs check-release performance build-ui build-desktop run-api docker-up docker-down

check-docs:
	node scripts/check-docs.mjs

check-release:
	node scripts/check-docs.mjs
	node scripts/check-release-contracts.mjs

performance:
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-point-performance-slo.ps1

test:
	go test ./...
	cd frontend && npm run build

build-ui:
	cd frontend && npm ci && npm run build

# Wails requires -tags production (or `wails build`). Plain `go build .` shows a dialog and exits.
build-desktop:
	wails build -platform windows/amd64

run-api:
	go run ./cmd/server

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down
