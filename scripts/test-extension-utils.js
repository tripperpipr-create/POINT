const assert = require('node:assert/strict')
const fs = require('node:fs')
const os = require('node:os')
const path = require('node:path')
const {
  decisionResolvePath,
  companionFactPairs,
  isQuietApiRoute,
  readJsonFile,
  removeFileIfExists,
  upsertById,
  removeById,
} = require('../vscode-extension/extension-utils')

assert.equal(decisionResolvePath('/api/approvals/approval_1/resolve'), '/api/approvals/approval_1/resolve')
assert.equal(decisionResolvePath('/api/approvals/../resolve'), '')
assert.equal(decisionResolvePath('/api/change-sets/set_1/apply?force=1'), '')
assert.equal(isQuietApiRoute('GET', '/api/bootstrap?fresh=1'), true)
assert.equal(isQuietApiRoute('POST', '/api/custom-tools/execute'), false)

// Факты контекста приходят машинной строкой, и в одной строке пары идут и через
// точку с запятой, и через пробел. Разбор по одному разделителю читал «memories=6
// selected=3» как одно значение — окно проверки показывало бы мусор.
const factPairs = companionFactPairs([
  'gatherMode=lean; activeQuests=2; memories=6 selected=3',
  'codeContext=internal/git/branches.go:1-40,internal/git/tags.go:5-22',
  'codeContextDropped=2',
  'projectIndexFiles=511 symbols=30',
  'toolsUsed=git_log,git_diff',
  'projectIndexPartial=entries',
  'replyTruncated=true',
  'replyLimitTokens=1200',
  'replyCostMicroUsd=14639',
  'modelContextWindow=200000',
  'modelMaxOutput=32000',
  'cacheReadTokens=20122',
  'cacheWriteTokens=6046',
  'ideFailure=Снимок логов Point: 17:35 ERROR companion chat error',
])
const factMap = new Map(factPairs)
assert.equal(factMap.get('Сбор контекста'), 'сокращённый')
assert.equal(factMap.get('Память проекта'), 'использовано 3 из 6')
assert.equal(factMap.get('Отсеяно как не по теме'), '2 фрагм.')
assert.equal(factMap.get('Помощник смотрел'), 'git_log, git_diff')
assert.equal(factMap.get('Индекс неполон'), 'остановлен на пределе: entries')
assert.equal(factMap.get('Ответ'), 'оборван на пределе длины')
assert.equal(factMap.get('Предел ответа'), '1200 токенов')
// Деньги показываются центами: «$0.01» человек понимает, «14639» — нет.
assert.equal(factMap.get('Стоимость ответа'), '$0.01')
assert.equal(factMap.get('Окно модели'), '200K токенов')
assert.equal(factMap.get('Потолок ответа модели'), '32K токенов')
// Разделитель разрядов ставит сама среда, и он неразрывный: сравнивать надо с тем же способом записи, а не с похожим на глаз.
assert.equal(factMap.get('Прочитано из кэша'), (20122).toLocaleString('ru-RU') + ' токенов')
assert.equal(factMap.get('Записано в кэш'), (6046).toLocaleString('ru-RU') + ' токенов')
assert.equal(factMap.get('Индекс проекта'), '511 файлов, 30 символов')
assert.equal(factMap.get('Фрагменты кода'), 'internal/git/branches.go:1-40, internal/git/tags.go:5-22')
// Непереведённые факты не выдумываются: они остаются в сыром списке рядом.
assert.equal(factMap.has('ideFailure'), false)
// Возраст снимка меняет смысл ответа, поэтому читается словами, а не минутами:
// «1440» человек молча принимает за свежесть, «сутки назад» — нет.
const ageWords = minutes => new Map(companionFactPairs([`projectIndexAgeMinutes=${minutes}`])).get('Снимок индекса')
assert.equal(ageWords(0), 'только что')
assert.equal(ageWords(14), '14 мин назад')
assert.equal(ageWords(200), '3 ч назад')
assert.equal(ageWords(5000), '3 дн. назад')
// Пустой список не рисует пустой блок.
assert.deepEqual(companionFactPairs([]), [])
assert.deepEqual(companionFactPairs(undefined), [])

const source = [{ id: 'a', value: 1 }, { id: 'b', value: 2 }]
assert.deepEqual(upsertById(source, { id: 'b', value: 3 }), [{ id: 'a', value: 1 }, { id: 'b', value: 3 }])
assert.deepEqual(removeById(source, 'a'), [{ id: 'b', value: 2 }])

const fixture = path.join(os.tmpdir(), `point-extension-utils-${process.pid}-${Date.now()}.json`)
fs.writeFileSync(fixture, '{"ok":true}', 'utf8')
assert.deepEqual(readJsonFile(fixture), { ok: true })
removeFileIfExists(fixture)
removeFileIfExists(fixture)
assert.equal(readJsonFile(fixture), undefined)

console.log('extension-utils: ok')
