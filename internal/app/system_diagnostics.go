package app

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
)

const (
	blockedDiskBytes  = 256 * 1024 * 1024
	degradedDiskBytes = 1024 * 1024 * 1024
	minimumDockerAPI  = "1.41"
)

// SystemDiagnostics is an explicit, read-mostly lifecycle probe. The response
// contains only named source checks and bounded metrics; credentials and file
// contents are never copied into it.
func (a *App) SystemDiagnostics(ctx context.Context) diagnostics.SystemReport {
	if ctx == nil {
		ctx = context.Background()
	}
	checks := []diagnostics.SystemCheck{
		{
			Code: "point_core", Status: diagnostics.SystemReady,
			Summary: "Point Core доступен", Metrics: map[string]any{"version": Version},
			RepairActions: []diagnostics.RepairAction{{ID: "restart_core", Label: "Перезапустить Point Core"}},
		},
	}
	checks = append(checks, a.databaseChecks(ctx)...)
	checks = append(checks,
		diskSpaceCheck(a.dataDir),
		a.sandboxCheck(),
		a.workspacePermissionCheck(),
		portCheck(os.Getenv("HTTP_ADDR")),
		a.backupCheck(ctx),
		a.providerConfigurationCheck(ctx),
	)
	return diagnostics.CompileSystemReport(checks, time.Now().UTC())
}

// enforceRunnableSystemLifecycle is the execution-side half of the System
// Health contract. The UI renders the same report, but a stale or alternate
// client must not be able to launch an agent while any source check is
// BLOCKED. DEGRADED intentionally remains runnable for safe limited work.
func (a *App) enforceRunnableSystemLifecycle(ctx context.Context) error {
	report := a.SystemDiagnostics(ctx)
	if report.Status != diagnostics.SystemBlocked {
		return nil
	}
	reasons := append([]string(nil), report.BlockingReasons...)
	actions := make([]string, 0, len(report.Checks))
	seenActions := map[string]bool{}
	for _, check := range report.Checks {
		if check.Status != diagnostics.SystemBlocked {
			continue
		}
		action := strings.TrimSpace(check.NextAction)
		if action != "" && !seenActions[action] {
			actions = append(actions, action)
			seenActions[action] = true
		}
	}
	if len(reasons) == 0 {
		reasons = []string{"локальная диагностика запретила запуск"}
	}
	message := "system lifecycle BLOCKED: " + strings.Join(reasons, "; ")
	if len(actions) > 0 {
		message += ". Безопасное следующее действие: " + strings.Join(actions, " ")
	}
	return errors.New(message)
}

func (a *App) databaseChecks(ctx context.Context) []diagnostics.SystemCheck {
	report, err := a.store.Health(ctx)
	if err != nil {
		return []diagnostics.SystemCheck{
			{
				Code: "sqlite", Status: diagnostics.SystemBlocked,
				Summary: "Проверка целостности SQLite не пройдена", Detail: err.Error(),
				NextAction:    "Откройте восстановление из последнего проверенного backup.",
				RepairActions: []diagnostics.RepairAction{{ID: "restore_database", Label: "Восстановить базу", RequiresConfirmation: true}},
			},
			{
				Code: "migration", Status: diagnostics.SystemBlocked,
				Summary: "Версия миграции не подтверждена", NextAction: "Сначала восстановите целостную базу данных.",
			},
		}
	}
	database := diagnostics.SystemCheck{
		Code: "sqlite", Status: diagnostics.SystemReady, Summary: "SQLite цела",
		Metrics: map[string]any{
			"integrity": report.Integrity, "foreignKeyViolations": report.ForeignKeyViolations,
		},
	}
	if report.ForeignKeyViolations > 0 {
		database.Status = diagnostics.SystemBlocked
		database.Summary = "В SQLite нарушены внешние ключи"
		database.NextAction = "Восстановите базу из последнего проверенного backup."
		database.RepairActions = []diagnostics.RepairAction{{ID: "restore_database", Label: "Восстановить базу", RequiresConfirmation: true}}
	}
	migration := diagnostics.SystemCheck{
		Code: "migration", Status: diagnostics.SystemReady, Summary: "Схема SQLite актуальна",
		Metrics: map[string]any{
			"version": report.MigrationVersion, "expectedVersion": report.ExpectedMigrationVersion,
			"missingVersions": report.MissingMigrationVersions,
		},
	}
	if !report.LastMigrationAt.IsZero() {
		migration.Metrics["lastMigrationAt"] = report.LastMigrationAt
	}
	if report.MigrationVersion != report.ExpectedMigrationVersion || len(report.MissingMigrationVersions) > 0 {
		migration.Status = diagnostics.SystemBlocked
		migration.Summary = "Набор миграций SQLite неполон"
		migration.NextAction = "Не запускайте агентов; создайте recovery point и восстановите или повторите миграцию."
		migration.RepairActions = []diagnostics.RepairAction{{ID: "create_backup", Label: "Создать recovery point"}}
	}
	return []diagnostics.SystemCheck{database, migration}
}

func diskSpaceCheck(path string) diagnostics.SystemCheck {
	available, err := diagnostics.AvailableDiskBytes(path)
	if err != nil {
		return diagnostics.SystemCheck{
			Code: "disk_space", Status: diagnostics.SystemDegraded,
			Summary: "Свободное место не удалось измерить", Detail: err.Error(),
			NextAction: "Проверьте свободное место на диске данных Point.",
		}
	}
	check := diagnostics.SystemCheck{
		Code: "disk_space", Status: diagnostics.SystemReady, Summary: "Свободного места достаточно",
		Metrics: map[string]any{"availableBytes": available, "blockedBelowBytes": blockedDiskBytes, "degradedBelowBytes": degradedDiskBytes},
	}
	switch {
	case available < blockedDiskBytes:
		check.Status = diagnostics.SystemBlocked
		check.Summary = "Свободного места недостаточно для безопасной работы"
		check.NextAction = "Освободите минимум 256 MB перед запуском агента или восстановлением."
	case available < degradedDiskBytes:
		check.Status = diagnostics.SystemDegraded
		check.Summary = "Свободное место близко к безопасному минимуму"
		check.NextAction = "Освободите место; backup и длительные Runs могут быть ограничены."
	}
	return check
}

func (a *App) sandboxCheck() diagnostics.SystemCheck {
	capabilities := a.sandboxBackend.Capabilities()
	metrics := map[string]any{
		"backend": capabilities.Backend, "version": capabilities.Version,
		"apiVersion": capabilities.APIVersion, "image": capabilities.Image,
		"imageDigest": capabilities.ImageDigest, "strongOSBoundary": capabilities.StrongOSBoundary,
	}
	if !capabilities.StrongOSBoundary {
		return diagnostics.SystemCheck{
			Code: "sandbox", Status: diagnostics.SystemDegraded,
			Summary:    "Доступна безопасная ограниченная работа без strong sandbox",
			Detail:     "Host execution не включается автоматически; process/network isolation не подтверждены.",
			NextAction: "Настройте Docker sandbox для исполняемых инструментов.", Metrics: metrics,
			RepairActions: []diagnostics.RepairAction{{ID: "rebuild_sandbox_image", Label: "Подготовить закреплённый sandbox image"}},
		}
	}
	check := diagnostics.SystemCheck{Code: "sandbox", Status: diagnostics.SystemReady, Summary: "Docker sandbox готов", Metrics: metrics}
	if capabilities.ImageDigest == "" {
		check.Status = diagnostics.SystemBlocked
		check.Summary = "Digest sandbox image не подтверждён"
		check.NextAction = "Повторно проверьте или безопасно пересоберите закреплённый image."
		check.RepairActions = []diagnostics.RepairAction{{ID: "rebuild_sandbox_image", Label: "Подготовить закреплённый sandbox image"}}
		return check
	}
	expectedDigest := strings.TrimSpace(os.Getenv("POINT_SANDBOX_IMAGE_DIGEST"))
	if expectedDigest != "" {
		metrics["expectedImageDigest"] = expectedDigest
		if !strings.EqualFold(expectedDigest, capabilities.ImageDigest) {
			check.Status = diagnostics.SystemBlocked
			check.Summary = "Digest sandbox image не совпадает с policy"
			check.NextAction = "Не запускайте контейнер; пересоберите или загрузите закреплённый image."
			check.RepairActions = []diagnostics.RepairAction{{ID: "rebuild_sandbox_image", Label: "Восстановить закреплённый sandbox image"}}
			return check
		}
	}
	if capabilities.APIVersion == "" || compareDecimalVersion(capabilities.APIVersion, minimumDockerAPI) < 0 {
		check.Status = diagnostics.SystemBlocked
		check.Summary = "Docker Engine API несовместим"
		check.NextAction = "Обновите Docker Engine до API 1.41 или новее."
		metrics["minimumAPIVersion"] = minimumDockerAPI
	}
	return check
}

func (a *App) workspacePermissionCheck() diagnostics.SystemCheck {
	a.mu.RLock()
	workspace := a.currentWorkspace
	a.mu.RUnlock()
	if workspace == nil || strings.TrimSpace(workspace.Path) == "" {
		return diagnostics.SystemCheck{
			Code: "workspace_permissions", Status: diagnostics.SystemDegraded,
			Summary: "Проект не открыт", NextAction: "Откройте локальную папку проекта.",
		}
	}
	file, err := os.CreateTemp(workspace.Path, ".point-permission-*")
	if err != nil {
		return diagnostics.SystemCheck{
			Code: "workspace_permissions", Status: diagnostics.SystemBlocked,
			Summary: "Point не может безопасно писать в workspace", Detail: err.Error(),
			NextAction: "Исправьте права папки или откройте доступный локальный checkout.",
		}
	}
	name := file.Name()
	closeErr := file.Close()
	removeErr := os.Remove(name)
	if closeErr != nil || removeErr != nil {
		return diagnostics.SystemCheck{
			Code: "workspace_permissions", Status: diagnostics.SystemBlocked,
			Summary:    "Проверочный файл workspace не удалось освободить",
			Detail:     errors.Join(closeErr, removeErr).Error(),
			NextAction: "Проверьте права и блокировки антивируса для папки проекта.",
		}
	}
	return diagnostics.SystemCheck{Code: "workspace_permissions", Status: diagnostics.SystemReady, Summary: "Права workspace подтверждены"}
}

func portCheck(address string) diagnostics.SystemCheck {
	address = strings.TrimSpace(address)
	if address == "" {
		address = "127.0.0.1:8080"
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return diagnostics.SystemCheck{
			Code: "core_port", Status: diagnostics.SystemBlocked,
			Summary: "Адрес Point Core некорректен", Detail: err.Error(),
			NextAction:    "Исправьте HTTP_ADDR и перезапустите Point Core.",
			RepairActions: []diagnostics.RepairAction{{ID: "restart_core", Label: "Перезапустить Point Core"}},
		}
	}
	return diagnostics.SystemCheck{
		Code: "core_port", Status: diagnostics.SystemReady,
		Summary: "Порт Point Core занят ожидаемым процессом", Metrics: map[string]any{"port": port},
	}
}

func (a *App) backupCheck(ctx context.Context) diagnostics.SystemCheck {
	if a.backupManager == nil {
		return diagnostics.SystemCheck{
			Code: "backup", Status: diagnostics.SystemBlocked,
			Summary: "Сервис backup не настроен", NextAction: "Перезапустите Point Core и проверьте каталог данных.",
		}
	}
	snapshot, err := a.backupManager.Latest(ctx)
	if errors.Is(err, os.ErrNotExist) {
		return missingBackupCheck()
	}
	if err != nil {
		return diagnostics.SystemCheck{
			Code: "backup", Status: diagnostics.SystemDegraded,
			Summary: "Последний backup не прошёл проверку", Detail: err.Error(),
			NextAction:    "Создайте новый backup; повреждённый snapshot не будет использован автоматически.",
			RepairActions: []diagnostics.RepairAction{{ID: "create_backup", Label: "Создать новый backup"}},
		}
	}
	return diagnostics.SystemCheck{
		Code: "backup", Status: diagnostics.SystemReady, Summary: "Последний backup проверен",
		Metrics: map[string]any{
			"createdAt": snapshot.CreatedAt, "sizeBytes": snapshot.Database.SizeBytes,
			"sha256": snapshot.Database.SHA256, "integrity": snapshot.Database.Integrity,
		},
		RepairActions: []diagnostics.RepairAction{
			{ID: "create_backup", Label: "Создать backup сейчас"},
			{ID: "restore_database", Label: "Восстановить из backup", RequiresConfirmation: true},
		},
	}
}

func missingBackupCheck() diagnostics.SystemCheck {
	return diagnostics.SystemCheck{
		Code: "backup", Status: diagnostics.SystemDegraded,
		Summary: "Проверенного backup пока нет", NextAction: "Создайте первый проверенный backup.",
		RepairActions: []diagnostics.RepairAction{{ID: "create_backup", Label: "Создать backup"}},
	}
}

func (a *App) providerConfigurationCheck(ctx context.Context) diagnostics.SystemCheck {
	profiles, err := a.storedProfiles(ctx)
	if err != nil {
		return diagnostics.SystemCheck{
			Code: "provider_model", Status: diagnostics.SystemBlocked,
			Summary: "Конфигурацию provider не удалось прочитать", Detail: err.Error(),
			NextAction: "Сначала восстановите целостную конфигурацию SQLite.",
		}
	}
	configured := 0
	providersSeen := map[domain.ProviderKind]bool{}
	models := map[string]bool{}
	for _, profile := range profiles {
		if profile.Provider == "" || strings.TrimSpace(profile.Model) == "" {
			continue
		}
		configured++
		providersSeen[profile.Provider] = true
		models[strings.TrimSpace(profile.Model)] = true
	}
	metrics := map[string]any{"configuredProfiles": configured, "providers": len(providersSeen), "models": len(models)}
	if configured == 0 {
		return diagnostics.SystemCheck{
			Code: "provider_model", Status: diagnostics.SystemBlocked,
			Summary: "Provider и модель не настроены", NextAction: "Настройте модель в onboarding и выполните проверку подключения.", Metrics: metrics,
			RepairActions: []diagnostics.RepairAction{{ID: "recheck_provider", Label: "Проверить provider"}},
		}
	}
	return diagnostics.SystemCheck{
		Code: "provider_model", Status: diagnostics.SystemDegraded,
		Summary:    "Provider и модели настроены, но live-доступность ещё не подтверждена",
		Detail:     "System health не получает API-ключи. Live recheck выполняется отдельным явным действием через SecretStorage IDE.",
		NextAction: "Повторно проверьте выбранный provider и наличие требуемой модели.", Metrics: metrics,
		RepairActions: []diagnostics.RepairAction{{ID: "recheck_provider", Label: "Повторно проверить provider"}},
	}
}

func compareDecimalVersion(left, right string) int {
	parse := func(value string) []int {
		parts := strings.Split(strings.TrimSpace(value), ".")
		result := make([]int, len(parts))
		for index, part := range parts {
			result[index], _ = strconv.Atoi(part)
		}
		return result
	}
	a, b := parse(left), parse(right)
	for index := 0; index < max(len(a), len(b)); index++ {
		var av, bv int
		if index < len(a) {
			av = a[index]
		}
		if index < len(b) {
			bv = b[index]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}
