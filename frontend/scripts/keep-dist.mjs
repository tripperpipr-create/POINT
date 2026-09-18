// Каталог `frontend/dist` обязан пережить собственную сборку.
//
// `go:embed all:frontend/dist` в main.go отказывается собираться на пустом
// каталоге, поэтому `.gitkeep` лежит в репозитории (см. .gitignore). А у Vite
// стоит `emptyOutDir: true`, и он выносит содержимое каталога целиком — вместе
// с этим `.gitkeep`. Получалось, что обычная сборка удаляет отслеживаемый файл.
//
// Молча это жило потому, что job `frontend` в CI чистоту дерева не проверяет.
// А релизный workflow — проверяет: он собирает frontend, а ближе к концу
// требует, чтобы `git status --porcelain` был пуст, и падал бы на
// `D frontend/dist/.gitkeep` — по причине, не имеющей к релизу отношения.
//
//   node scripts/keep-dist.mjs

import { mkdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'

const dist = path.join(import.meta.dirname, '..', 'dist')
mkdirSync(dist, { recursive: true })
writeFileSync(path.join(dist, '.gitkeep'), '')
