# Оценка агентов и Skills

Agent Hub оценивает постоянную небольшую команду на реальных сохранённых Runs.
Единого «AI score» нет: интерфейс и API показывают исходные количества,
проценты только как производное отображение и причины каждого решения.

## Точная атрибуция

Новый Run сохраняет configuration snapshot schema v2:

- SHA-256 полного профиля;
- SHA-256 исполняемой конфигурации вместе с выбранными custom tools;
- `skillId`, revision, SHA-256 поведенческого payload и rollout status каждого
  реально загруженного Skill.

Служебные поля автономного rollout (`sourceRuns`, owner, promotion status и
подобные) не меняют payload digest. Они остаются в полном configuration digest.
Изменение инструкций, required tools или пользовательской runtime-конфигурации
меняет Skill digest. Старый snapshot v1 читается для истории, но не допускается
как benchmark evidence.

## Персональные benchmark-наборы

Набор принадлежит одному `ProjectAgent` и содержит от 1 до 50 кейсов. Каждый
кейс фиксирует точный task и явные критерии: ожидаемый terminal status,
обязательную healthy-диагностику, обязательную verification evidence и максимум
ошибок tools. Изменение набора увеличивает revision и меняет SHA-256 digest;
повторное сохранение того же содержания revision не увеличивает.

Оценка принимает ровно один отдельный terminal Run на кейс. Core проверяет
workspace, агента, точное совпадение task и snapshot v2, затем сохраняет:
configuration/profile digests, версии Skills, status, health, tool calls/failures,
verification и понятные причины PASS/FAIL. Содержимое файлов и финальный ответ
в benchmark record не копируются.

Before/after сравнение разрешено только для одной revision и digest набора и
требует различающуюся точную конфигурацию. Gate не усредняет кейсы: регрессией
является каждый кейс, который проходил before и перестал проходить after;
восстановившиеся кейсы перечисляются отдельно.

## Canary rollout Skills

Автономное обновление не перезаписывает активный Skill. Каждая новая ревизия
получает отдельный immutable ID и заменяет baseline только у canary-агента.
Verified trajectory с тем же workflow может назначить эту же точную ревизию
агенту второго проекта; он не создаёт новую версию и не распространяет её по
Blueprint.

После каждого terminal Run `SkillOutcome` связывает наблюдаемые status, health,
tool failures и verification с exact revision+digest. Gate использует три
последних Run кандидата и до пяти Run точного baseline:

- падение completion или healthy rate на 25 процентных пунктов;
- падение required-verification rate на 25 пунктов при минимум двух
  обязательных проверках в каждой выборке;
- рост tool-failure rate на 20 пунктов при минимум пяти вызовах в каждой
  выборке;
- для нового Skill или недостаточного baseline — два тяжёлых исхода из трёх.

Один или два Run оставляют status `pending`. Здоровая выборка должна охватывать
минимум два независимых workspace. После этого UI предлагает явное действие
«Продвинуть в Blueprint». Только пользователь распространяет кандидата на
совместимых агентов. Доказанная деградация автоматически откатывает последнюю
применённую ревизию и сохраняет весь журнал и причины.

## Проверка реализации

```powershell
go test ./internal/domain ./internal/storage ./internal/app ./internal/httpapi
cd vscode-extension
npm run check
```

Покрываются стабильность digests, сохранение managed revision под project
overrides, миграции, минимальная выборка, пороги before/after, автоматический
откат, immutable canary IDs, явное promotion и отображение исходных метрик.
