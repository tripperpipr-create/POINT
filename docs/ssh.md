# SSH в Point IDE

Point использует системный OpenSSH и хранит в SQLite только профиль без секрета.
Пароль, если выбран этот способ входа, остаётся в VS Code SecretStorage и
передаётся ядру одноразово только для текущего запроса.

Актуально для Point `1.2.2` на 26 августа 2026 года. Это SSH-инструменты и
read-only preview, а не Remote-SSH workspace. Полный каталог API находится в
[api.md](api.md).

## Пользовательский маршрут

1. Откройте **Point: Подключиться к серверу** или раздел **Связи**.
2. Сохраните host, port, user, способ входа и начальный удалённый путь.
3. **Проверить** выполняет bounded probe; **SSH-терминал** открывает нативный
   OpenSSH; **Файлы …** запускает навигатор с настроенного пути.
4. Каталог можно обходить без повторного ввода пути. Выбор UTF-8 файла открывает
   в редакторе безопасный read-only по смыслу предпросмотр до 64 КиБ. Документ
   untitled намеренно не сохраняется обратно на сервер автоматически.

Имена каталога выдаются по одному на строку с `/` для директорий. Пути проходят
shell quoting, переводы строк/NUL отклоняются, а бинарный файл не декодируется как
текст. Сохранённый пароль не доступен агентским инструментам: для автономных
операций используйте ключ или ssh-agent.

## Инструменты агента

- `ssh_test_connection` — read-only probe;
- `ssh_list_remote` — read-only listing;
- `ssh_read_remote` — bounded UTF-8 preview without arbitrary command execution;
- `ssh_exec_remote` — одна удалённая команда, всегда критическая мутация с
  подтверждением.

По умолчанию сеть закрыта. Для конкретного хоста требуется
`toolPolicies["network:<host>"] = "ALLOW"`; `ssh_exec_remote` всё равно проходит
approval.

## API

- `POST /api/servers`
- `DELETE /api/servers/{id}`
- `POST /api/servers/{id}/probe`
- `POST /api/servers/{id}/list`
- `POST /api/servers/{id}/read`
- `GET /api/servers/{id}/terminal`

`read` возвращает `{ path, content, truncated, profile }` и принимает
`{ path, password? }`. Это preview API, не универсальная передача бинарных файлов.
