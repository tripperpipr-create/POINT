// Обещания о секретах должны быть выполнимыми.
//
// Хаб писал: «Наружу уходит только ссылка на секрет (secretRef), сам секрет —
// никогда» — на странице, где заводят токены облачных моделей. Но ключ по
// определению уходит провайдеру: ядро ставит его в заголовок Authorization
// (internal/providers/openai.go), иначе запрос не авторизуется. Обещание было
// неверным ровно так, как его прочтёт человек, — и это самый дорогой род
// неверности: про приватность.
//
// Проверяем две вещи: обещание «никогда» не вернулось, а настоящая гарантия
// (в файлах, хронике и ответах ядра — только ссылка) осталась на месте.

const fs = require('fs')
const path = require('path')

const repo = path.join(__dirname, '..')
const main = fs.readFileSync(path.join(repo, 'vscode-extension/media/main.js'), 'utf8')
const providers = fs.readFileSync(path.join(repo, 'internal/providers/openai.go'), 'utf8')

const failures = []
const check = (name, ok, detail) => { if (!ok) failures.push(`${name}: ${detail}`) }

// Защита от холостого хода: если ядро перестанет отправлять ключ провайдеру,
// прежняя формулировка снова станет верной, и запрет ниже потеряет смысл.
check('ядро действительно отправляет ключ провайдеру',
  /Header\.Set\("Authorization", "Bearer "\+o\.config\.APIKey\)/.test(providers.replace(/\s/g, '')) ||
  /Authorization[\s\S]{0,40}APIKey/.test(providers),
  'в providers/openai.go не найдена отправка ключа — проверка обещания потеряла основание')

const claims = main.replace(/<[^>]+>/g, ' ')

// Само неверное обещание.
check('обещание «сам секрет — никогда» не вернулось',
  !/сам секрет\s*—\s*никогда/i.test(claims),
  'на экране снова написано, что ключ не уходит наружу')
check('обещание «ключ уходит только в SecretStorage» не вернулось',
  !/Ключ уходит только в SecretStorage/i.test(claims),
  'онбординг снова обещает, что ключ никуда не уходит')

// Настоящая гарантия обязана остаться: иначе честность превратится в молчание.
check('сказано, где остаётся только ссылка',
  /остаётся только ссылка/i.test(claims) && /secretRef/.test(main),
  'исчезло объяснение про secretRef')
check('сказано, куда ключ всё-таки уходит',
  /уходит выбранному провайдеру|уходит одному адресату/i.test(claims),
  'не сказано, что ключ уходит провайдеру')
check('хранение по-прежнему названо',
  /SecretStorage/.test(claims),
  'исчезло упоминание SecretStorage')

if (failures.length) {
  console.log('ОБЕЩАНИЯ О СЕКРЕТАХ — ПРОВАЛ:')
  for (const line of failures) console.log('  ' + line)
  process.exit(1)
}
console.log('обещания о секретах выполнимы: PASS')
