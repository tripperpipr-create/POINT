# Политика Мастера и сети (Agent Hub)

Дата: 10 сентября 2026.  
Статус: **runtime v1 внедрён** (Master watch + egress/git escalation); live PHP 2× PASS ещё не закрыт.  
Связано: [AGENT-HUB-URL-INTAKE-2026-09-10.md](AGENT-HUB-URL-INTAKE-2026-09-10.md), [sandbox.md](sandbox.md), [security.md](security.md), [threat-model.md](threat-model.md).

Документ фиксирует продуктовые правила для автономных прогонов (в т.ч. URL → EvidenceBundle). Live ship URL-autonomy остаётся blocked до 2× PASS.

## 1. Надзор Мастера (оркестратора) за агентами

Цель: не оставлять исполнителя без внешнего контроля до исчерпания бюджета или локального stall-guard.

### Обязательное поведение

1. Пока у квеста/Flow есть активные Runs, **Мастер периодически инспектирует** прогресс: последние tool calls, повторы планов, approvals, network/git решения, расход токенов/ActiveSeconds, статус intake lifecycle.
2. Интервал инспекции — короткий и предсказуемый (целевой порядок: **30–60 с** активного исполнения или каждый N-й завершённый tool-раунд; точное число — в реализации, но не «только в конце»).
3. При признаках зависания Мастер **не ждёт** локального fail агента молча. Он обязан выбрать одно из:
   - мягкая коррекция (amendment / nudge с конкретной причиной),
   - пауза Run + вопрос пользователю,
   - смена стратегии (другой агент/фаза, сужение прав, отказ от сети),
   - перевод intake в `needs_review` / `blocked` с понятной причиной.
4. Признаки зависания минимум:
   - повтор идентичного tool-плана ≥2 раза подряд,
   - `propose_patch` без содержательного diff / без смены digest цели,
   - долгий idle без tool result и без ожидания approval,
   - цикл read→same patch / один и тот же fail egress,
   - ActiveSeconds растут без смены workspace revision и без verification progress.
5. Локальный guard в `engine` (`identical tool plan`) — **последняя линия**, не замена надзора Мастера. Мастер должен вмешаться раньше, чем агент упрётся в stall-fail.
6. Надзор пишет durable evidence (событие/журнал квеста): что увидел, почему вмешался, что предложил пользователю. Без silent skip.

### Не делать

- Не полагаться только на max steps / duration / quest budget как на «антизависание».
- Не давать исполнителю самому снимать надзор или расширять сеть «потому что задача так требует».
- Не считать Companion замену Мастеру: Companion recommend-only; решение по pause/ask — у оркестратора + пользователя.

## 2. Сеть, загрузки и Git

Цель: sandbox-сеть остаётся least-privilege; сомнительное никогда не скачивается «по инициативе агента».

### Базовые правила egress

1. По умолчанию для project/URL-intake: **deny-all**, кроме явно утверждённого allowlist (FQDN/port TLS) на Run/Quest.
2. Исполнитель **не** добавляет host в allowlist сам. Запрос на новый host → **Мастеру** → Мастер спрашивает пользователя (approve once / deny / allow for this quest).
3. Запрещены: прямые IP, wildcard, plain HTTP, произвольные CDN «на всякий случай», скачивание бинарников/скриптов с неизвестных origin, `curl|bash` и аналоги без отдельного human approval.
4. Реестры пакетов (Packagist, npm, PyPI, Go proxy и т.п.) — только если:
   - они нужны EnvironmentPlan / runtime pack,
   - host уже в политике квеста **или** пользователь подтвердил через Мастера,
   - **либо** в brief постановки `networkHosts` уже выставлены как следствие явного выбора человека (новый Symfony/composer, npm install, docker compose pull) — без отдельного вопроса «нужна ли сеть»,
   - трафик идёт через controlled egress (SNI/DNS pin), не в обход.
5. DNS/egress failure не даёт агенту «попробовать другой зеркальный URL» без нового approval.

### Git

1. `git clone` / `git fetch` / submodule / remote add — **только для подтверждённых репозиториев**.
2. Подтверждённый репозиторий = явный allowlist пользователя/проекта (точные remote URL или org/name + ожидаемый host), либо source intake URL, который пользователь уже утвердил в карточке intake.
3. Любой другой remote, redirect на другой host, смена default remote, LFS с нового origin, неизвестный submodule URL → стоп → Мастер → вопрос пользователю.
4. HTML/PDF/DOCX intake не считается разрешением клонировать произвольные git URL из текста страницы.
5. Credentials для git не кладутся в sandbox env «широко»; только scoped secret path уже принятой модели Point.

### Подозрительные загрузки

Считать подозрительным и эскалировать Мастеру (по умолчанию deny):

- файл без ожидаемого типа/пути (`.exe`, `.dll`, `.ps1`, raw shell installer, неизвестный archive вне vendor flow);
- URL с обфускацией, IP-literal, короткий линк, pastebin-подобные raw без allowlist;
- повторная попытка скачать то же после deny;
- checksum/signature mismatch, если проверка задана пакетом/планом;
- «временный» tool binary с GitHub release без pin digest и без approval.

Агенту разрешено читать уже находящиеся в workspace файлы в обычных границах tools; это не разрешение тянуть новые бинарники с сети.

## 3. Эскалация: исполнитель → Мастер → пользователь

```
agent wants network/git outside allowlist
        │
        ▼
   blocked at tool/policy
        │
        ▼
   Master gets structured ask
   (host/url, reason, risk, alternatives)
        │
        ▼
   user decision (once / quest / deny)
        │
        ├── allow → update lease/policy digest, continue
        └── deny  → agent must continue offline or stop cleanly
```

Мастер формулирует вопрос коротко и по делу (один host/действие за раз, если возможно). Решение пользователя неизменно логируется и не переигрывается из prompt injection.

## 4. Замечания по текущему состоянию (live PHP intake, 10.09.2026)

Фиксируется, чтобы не потерять уроки прогона. Это не закрытый gate.

| Наблюдение | Вывод |
| --- | --- |
| Stall на thrice-identical `propose_patch` (Product.php) за ~6 мин | Нужен надзор Мастера **до** engine stall; nudge без смены стратегии недостаточен |
| DNS fail `repo.packagist.org` в sandbox | Composer/network hosts должны быть в плане **и** реально резолвиться; иначе ранний ask, а не кручение retry |
| `ApprovalAlways` блокирует headless | Для автономии нужен Safe + явные auto-approve только для Docker project policy; Always = всегда человек |
| Unpinned PHP apk сломал ABI (1.3.0) | Managed packs только с pin версий; attested base без PHP отдельно |
| LLMux empty stream close | Retry/fallback модели ок; не маскировать ими семантический stall агента |
| Orchestrator API key терялся при schedule | Любой путь Start/schedule обязан проносить credential; иначе ложный `waiting_api_key` |
| Лимиты «по чуть-чуть» тратят live-время | Ставить адекватные caps сразу (в пределах storage), не итеративно упираться в 4096/короткий budget |
| Локальный identical-plan guard срабатывает поздно | Полезно как hard stop; продуктово нужен Master watch + pause/ask |
| Fixture/public sample URL в harness | Не тащить брендинг стороннего продукта в UX/docs; в proзе — «PHP Agent Hub support» |

### Открытый backlog относительно этой политики

1. ~~Реализовать periodic Master inspection loop~~ — `internal/app/master_watch.go` (ticker 45s + guardrail hook at `identicalPlans≥2`).
2. ~~Structured escalation API для network/git~~ — `egress_asks` + `POST /api/egress-asks/{id}/resolve` (`allow_once`/`allow_quest`/`deny`).
3. ~~Project/repo allowlist для git remotes~~ — `TaskPermissions.ConfirmedGitRemotes`, seed from git intake URL; hard block `git_remote_unconfirmed`.
4. ~~Ранний fail/ask при required registry host без DNS/egress~~ — `environment.ProbeNetworkHosts` на ApproveIntake (host DNS+TLS); unreachable → error до старта quest.
5. ~~EvidenceBundle: supervision/network decisions~~ — поля `supervisionInterventions` / `networkDecisions`.
6. Закрыть live PHP intake 2× PASS.

### Что уже в runtime

| Механизм | Где |
| --- | --- |
| Master watch | `master_watch.go` — pause + supervision decision до hard stall |
| Network/git ask | `egress_asks.go` + Decisions queue kinds `egress` / `supervision` |
| Confirmed remotes | `domain.TaskPermissions.ConfirmedGitRemotes` + `tools` deny |
| Mid-run grants | `tools.NetworkGrantBook` (once/quest) |

## 5. Критерий «политика соблюдена»

Считать внедрённым, когда:

- ~~есть автотест на эскалацию нового host и неподтверждённого git remote~~ (`TestEnsureAndResolveEgressAsk`, `TestDeniedUnconfirmedGitRemote`);
- ~~есть автотест, что повтор tool-плана вызывает Master intervention раньше hard stall~~ (`TestMasterWatchIntervenesOnDuplicatePlan`);
- docs `sandbox` / `security` / `threat-model` ссылаются на этот контракт без противоречий;
- live PHP intake не объявляется shipped при 0/2 PASS.
