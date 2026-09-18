// Ошибки ядра доходят до человека по-русски.
//
// Ядро отвечает по-английски — это язык его логов и API. Через
// describeCoreFailure текст попадает прямо в красную полосу русскоязычного
// Хаба: «daily hub budget exceeded; new runs are blocked until…» вместо
// объяснения, что исчерпан дневной лимит.
//
// Проверяем и обратную сторону: незнакомую ошибку глотать нельзя — человек
// должен увидеть хоть что-то, а не пустоту или чужую формулировку.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const repo = path.join(__dirname, '..')
const source = fs.readFileSync(path.join(repo, 'vscode-extension/extension.js'), 'utf8')

// Из расширения берём только нужные объявления: целиком оно требует vscode.
const table = source.match(/const CORE_FAILURE_HINTS = \[[\s\S]*?\n\]/)
const fn = source.match(/function describeCoreFailure\(error\) \{[\s\S]*?\n\}/)
if (!table || !fn) {
  console.log('не найдены CORE_FAILURE_HINTS или describeCoreFailure — проверка вхолостую')
  process.exit(1)
}
const context = { console }
vm.runInNewContext(`${table[0]}\n${fn[0]}\nthis.__describe = describeCoreFailure`, context)
const describe = context.__describe

const failures = []
const check = (name, ok, detail) => { if (!ok) failures.push(`${name}: ${detail}`) }

const cases = [
  ['дневной лимит', 'daily hub budget exceeded; new runs are blocked until budget resets or hard stop is disabled', /Дневной лимит расходов исчерпан/],
  ['месячный лимит', 'monthly hub budget exceeded; new runs are blocked until budget resets or hard stop is disabled', /Месячный лимит расходов исчерпан/],
  ['бюджет квеста', 'quest cost budget exceeded: used 120 cents, limit 100', /Бюджет квеста исчерпан/],
  ['набор в неподходящем состоянии', 'change set cannot be applied from status applied', /нельзя применить из текущего состояния/],
  ['откат неприменённого', 'only an applied change set can be reverted; current status is pending', /Откатить можно только применённый/],
  ['путь за пределы проекта', 'change path escapes workspace: "../evil.txt"', /за пределы проекта/],
  ['персонаж по умолчанию', 'the default profile cannot be deleted', /удалить нельзя/],
  ['проект не открыт', 'workspace is not open', /Проект не открыт/],
  ['чужой проект', 'resource belongs to another project world', /другому проекту/],
]

for (const [name, english, expected] of cases) {
  const got = describe(new Error(english))
  check(name, expected.test(got), `получено: ${got}`)
  check(`${name}: английский текст не показан`, !/[a-z]{4,} [a-z]{4,}/.test(got), `получено: ${got}`)
}

// Сеть переводилась и раньше — убеждаемся, что новая таблица её не перехватила.
check('сетевой сбой объясняется по-прежнему',
  /ядро Point не отвечает/i.test(describe(new Error('fetch failed'))),
  describe(new Error('fetch failed')))

// Незнакомую ошибку глотать нельзя: человеку нужен хоть какой-то след.
const unknown = describe(new Error('some brand new core failure'))
check('незнакомая ошибка не теряется', unknown.includes('some brand new core failure'), unknown)

// Пустая ошибка тоже обязана что-то сказать.
check('пустая ошибка названа', describe(new Error('')).length > 10, describe(new Error('')))

if (failures.length) {
  console.log('ТЕКСТЫ ОШИБОК ЯДРА — ПРОВАЛ:')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('ошибки ядра доходят по-русски: PASS')
