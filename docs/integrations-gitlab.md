# Интеграция GitLab через MCP

Статус: этап 1 в работе, 25 сентября 2026 года. План и решения владельца описаны в
разделе «Решения» ниже. Живой приёмки на GitLab владельца (19.3.2, внутренняя
сеть) ещё не было.

## Как устроено

Point не ходит в REST API GitLab сам. Он запускает MCP-сервер GitLab как
долгоживущий процесс stdio и вызывает его инструменты из ядра. Модель в этом не
участвует:

- окно «GitLab» (списки MR и пайплайнов) и карточка MR получают данные
  детерминированными вызовами инструментов;
- кнопки «Комментировать», «Одобрить», «Merge», «Перезапустить» — действие
  владельца: оно выполняется сразу и пишется в журнал интеграций;
- агентам и Мастеру инструменты GitLab на этапе 1 не выдаются.

## Сервер и рецепт запуска

Закреплён `@zereight/mcp-gitlab@2.1.66` (MIT). Целостность пакета из реестра npm:
`sha512-KiWiaRzQQw8i+CFwY4zQgsVrLAbj81Dok3ZUqdUIYOhQIs5DOskxoZtqNp1qjdBRYKD03HPmV+4Wv1OinyDfXw==`.

```text
npx -y @zereight/mcp-gitlab@2.1.66
GITLAB_API_URL=https://<ваш GitLab>/api/v4
GITLAB_PERSONAL_ACCESS_TOKEN=<секрет, из SecretStorage>
GITLAB_TOOLSETS=merge_requests
GITLAB_TOOLS=<21 инструмент из таблицы ниже>
GITLAB_PERMISSION_MODE=modify
GITLAB_DENIED_TOOLS_REGEX=^discover_tools$
```

Замеры спайка (`tools/list`, сервер запущен с поддельным адресом — список
инструментов к GitLab не обращается):

| Настройка | Инструментов | Схемы в JSON |
| --- | --- | --- |
| по умолчанию | 118 | 174 КБ |
| рецепт выше | 46 | 77 КБ |
| только нужные Point | 21 | 39 КБ |

Сократить список до 21 средствами самого сервера нельзя. `GITLAB_TOOLS`
добавляет инструменты к наборам, а не сужает их, а `GITLAB_DENIED_TOOLS_REGEX`
ограничен 200 символами. Поэтому главная граница — у Point:
- адаптер вызывает только инструменты из таблицы;
- инструменты сервера по умолчанию выключены для агентов;
- `GITLAB_PERMISSION_MODE=modify` на стороне сервера убирает удаление и
  разрушительные инструменты;
- `discover_tools` запрещён: он добавляет инструменты во время работы.

Для внутреннего сертификата GitLab сервер читает `GITLAB_CA_CERT_PATH`, Node —
`NODE_EXTRA_CA_CERTS`.

## Права токена

Для просмотра достаточно `read_api`. Комментарии, одобрение, merge и перезапуск
джоба требуют `api`. Если токен только на чтение, действия в окне заперты с
причиной.

## Экран → инструмент

Ответы сервера — JSON объектов REST API GitLab в `content[0].text` (без
`structuredContent`). Исключение — лог джоба: он приходит простым текстом.

| Экран | Инструмент | Аргументы Point |
| --- | --- | --- |
| Кто я (для «На моём ревью») | `whoami` | — |
| Проект по git remote | `get_project` | `project_id` = путь `group/name` |
| MR: мои / на ревью / все открытые | `list_merge_requests` | `project_id`, `state=opened`, `author_username` / `reviewer_username` / без фильтра |
| Карточка MR | `get_merge_request` | `project_id`, `merge_request_iid` |
| Одобрения | `get_merge_request_approval_state` | `project_id`, `merge_request_iid` |
| Обсуждение | `mr_discussions` | `project_id`, `merge_request_iid`, `page`, `per_page`; ответ `{items, pagination}` |
| Список изменённых файлов | `list_merge_request_changed_files` | `project_id`, `merge_request_iid` |
| Diff файла | `get_merge_request_file_diff` (запасной — `get_merge_request_diffs`) | `project_id`, `merge_request_iid`, `file_paths` |
| Содержимое файла для diff IDE | `get_file_contents` | `project_id`, `file_path`, `ref` = `diff_refs.base_sha` / `head_sha` |
| Комментарий | `create_merge_request_note` | `project_id`, `merge_request_iid`, `body` |
| Ответ в нить | `create_merge_request_discussion_note` | + `discussion_id` |
| Одобрить / снять | `approve_merge_request` / `unapprove_merge_request` | + `sha` для одобрения |
| Merge | `merge_merge_request` | + `sha` (ожидаемая голова), `should_remove_source_branch` |
| Пайплайны ветки | `list_pipelines` | `project_id`, `ref` |
| Пайплайны MR | `list_merge_request_pipelines` | `project_id`, `merge_request_iid` |
| Пайплайн и джобы | `get_pipeline`, `list_pipeline_jobs` | `project_id`, `pipeline_id` |
| Лог джоба | `get_pipeline_job_output` | `project_id`, `job_id`, `limit`, `offset` |
| Перезапуск джоба | `retry_pipeline_job` | `project_id`, `job_id` |

Если у подключённого сервера инструмента нет, экран сообщает «сервер этого не
умеет» и называет инструмент. Остальные экраны работают.

## Фикстуры

`internal/integrations/gitlab/testdata/zereight-2.1.66/`:
- `manifest.json` — версия, целостность, протокол, рецепт, дата;
- `tools-list.json` — определения 21 инструмента, снятые со сборки 2.1.66.

Образцы ответов инструментов пока синтетические. Они собраны по схемам ответов
сервера (`schemas.js` 2.1.66) и REST API GitLab и помечены в `manifest.json`.
Заменить их снятием с GitLab владельца — часть живой приёмки.

## Решения

- Встроенный MCP-сервер GitLab (вход через OAuth) — этап 3.
- Задачи GitLab, «Обсудить с Мастером», доставка «результат → MR» и
  инструменты для агентов — этап 2.
