// Кому в ленте Мастера входить с движением, а кому — молча.
//
// Лента пересобирается почти на каждое событие хода, и ошибка в этом плане
// выглядит одинаково дурно в обе стороны: либо весь разговор мерцает на
// каждом событии, либо новая реплика появляется рывком. Глазом это ловится
// только на живом ходе, поэтому правила проверяются здесь, по отдельности.

import assert from 'node:assert/strict'
import { masterFeedEnterPlan, masterFeedKeys } from '../vscode-extension/ui/client/master-feed-motion.js'

const seen = keys => new Set(keys)

assert.deepEqual(masterFeedEnterPlan(new Set(), ['u:a:1', 'a:t1']), [], 'первая отрисовка — без движения')
assert.deepEqual(masterFeedEnterPlan(seen(['u:a:1', 'a:t1']), ['u:a:1', 'a:t1', 'u:b:1'], { fresh: true }), [], 'смена разговора — без движения')
assert.deepEqual(masterFeedEnterPlan(seen(['u:a:1', 'a:t1']), ['u:a:1', 'a:t1', 'u:b:1']), ['u:b:1'], 'новая реплика в хвосте входит')
assert.deepEqual(masterFeedEnterPlan(seen(['u:a:1', 'a:t1']), ['u:a:1', 'a:t1', 'u:b:1', 'a:t2']), ['u:b:1', 'a:t2'], 'реплика и блок хода входят вместе')
assert.deepEqual(masterFeedEnterPlan(seen(['u:c:1', 'a:t3']), ['u:a:1', 'a:t1', 'u:b:1', 'a:t2', 'u:c:1', 'a:t3']), [], '«Показать раньше»: ранние встают выше виденного и не входят')
assert.deepEqual(masterFeedEnterPlan(seen(['u:a:1']), ['u:a:1', 'a:1', 'a:2', 'a:3', 'a:4']), [], 'пачка больше трёх — загрузка, без движения')
assert.deepEqual(masterFeedEnterPlan(seen(['u:a:1', 'a:t1']), ['u:a:1', 'a:t1']), [], 'готовый ответ с номером хода блока не входит второй раз')
assert.deepEqual(masterFeedEnterPlan(seen(['u:a:1', 'a:turn']), ['u:a:1', 'a:msg-9'], { streamGone: true, masters: new Set(['a:msg-9']) }), [],
  'ответ без номера хода, сменивший блок хода, — его преемник и не входит')
assert.deepEqual(masterFeedEnterPlan(seen(['u:a:1', 'a:t1']), ['u:a:1', 'a:t1', 'c:workorder-7']), ['c:workorder-7'], 'новая карточка в хвосте входит')

// Реплика человека: ожидающая и сохранённая — один ключ, повтор текста — другой.
const node = (cls, text, data = {}) => ({
  classList: { contains: name => cls.split(' ').includes(name) },
  dataset: data,
  querySelector: () => ({ textContent: text }),
})
const keys = masterFeedKeys([
  node('hall-turn is-user-turn', 'Почини вебхук'),
  node('hall-turn is-master-turn', '', { feedKey: 'a:t1' }),
  node('hall-turn is-user-turn', 'Почини вебхук'),
  node('hall-deck', '', { workOrderId: 'wo-1' }),
])
assert.equal(keys[1], 'a:t1')
assert.notEqual(keys[0], keys[2], 'тот же текст второй раз — другая реплика')
assert.match(keys[0], /^u:[0-9a-z]+:1$/)
assert.equal(keys[3], 'c:wo-1')
const pending = masterFeedKeys([node('hall-turn is-user-turn', 'Почини вебхук')])[0]
const saved = masterFeedKeys([node('hall-turn is-user-turn', 'Почини вебхук')])[0]
assert.equal(pending, saved, 'ожидающая и сохранённая реплика — один узел')

console.log('движение ленты Мастера: PASS')
