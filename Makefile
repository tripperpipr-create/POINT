# Цели повторяют job'ы CI одна в одну.
#
# Раньше `make test` был уже CI: ни одного JS-смоука, ни реестра
# качества. Локальная команда «протестировать», которая проверяет меньше
# затвора, перекладывает поиск отказа на пуш и на чужой раннер, где разбор
# стоит на порядок дороже. Имена целей совпадают с именами job'ов, чтобы
# по красному job'у было видно, что запускать у себя.
.PHONY: test test-docs test-go test-race test-frontend test-extension lint \
        check-docs check-release performance build-ui build-desktop run-api \
        docker-up docker-down

# job `docs`
test-docs:
	node scripts/check-docs.mjs
	node scripts/check-quality-gate.mjs
	node scripts/check-release-contracts.mjs

# job `go`
test-go: lint
	go vet ./...
	go mod verify
	go test ./... -count=1

# Набор проверок — в staticcheck.conf. Версия закреплена та же, что в CI:
# линтер, который у человека и на раннере разный, хуже отсутствующего.
lint:
	go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...

# Детектор гонок требует CGO и gcc в PATH. На машине разработки их может
# не быть — тогда эта цель не запускается, и проверка остаётся за CI.
# В `test` она поэтому не входит: отказ сборки читался бы как отказ тестов.
test-race:
	go test ./internal/... -race -count=1

# job `frontend`
test-frontend:
	cd frontend && npm run build

# job `extension` — самый долгий: собирает ядро, CSS/JS/runtime и гоняет
# смоуки Хаба по собранному вебвью.
test-extension:
	cd vscode-extension && npm run check

test: test-docs test-go test-frontend test-extension

# Совместимость со старыми именами.
check-docs:
	node scripts/check-docs.mjs

check-release: test-docs

performance:
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-point-performance-slo.ps1

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
