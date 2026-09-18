// Текст, который нельзя прочитать.
//
// Найдено замером на живой поверхности: подписи в 9–10px шли цветами
// --hub-faint / -2 / -3 и давали на фоне чертога контраст 3.31 / 2.84 / 2.31
// при пороге 4.5. Получалось наоборот здравому смыслу: чем мельче знак, тем
// хуже он виден. Среди пострадавших было не только оформление — так была набрана
// строка «Правка обработчика вебхука», то есть содержание.
//
// Проверка статическая и потому грубая: она берёт объявленные токены палитры и
// считает их контраст к самому тёмному фону. Настоящий фон бывает светлее —
// значит настоящий контраст не хуже расчётного, и ошибиться проверка может лишь
// в строгую сторону. Что именно видно на экране, меряет браузер; здесь важно
// другое — палитру правят редко и вслепую, и потемневший токен иначе разъедется
// с читаемостью молча.

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const tokens = fs.readFileSync(path.join(here, 'tokens.css'), 'utf8')

const valueOf = name => {
  const hit = tokens.match(new RegExp(`--${name}:\\s*(#[0-9A-Fa-f]{6})`))
  return hit ? hit[1] : ''
}

const luminance = hex => {
  const n = parseInt(hex.slice(1), 16)
  const channels = [(n >> 16) & 255, (n >> 8) & 255, n & 255].map(value => {
    const v = value / 255
    return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4)
  })
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2]
}

const contrast = (fg, bg) => {
  const a = luminance(fg)
  const b = luminance(bg)
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)
}

// Токены, которыми набирают текст. Порог обычный, 4.5: крупного набора этими
// ступенями не бывает — ими подписывают, а подписи мелкие.
const TEXT_TOKENS = [
  'hub-text', 'hub-text-2', 'hub-text-3', 'hub-text-4',
  'hub-muted', 'hub-muted-2',
  'hub-faint', 'hub-faint-2', 'hub-faint-3',
]
// --hub-faint-4 сюда не входит намеренно: это линии и разделители, а не буквы.
// Если им когда-нибудь наберут текст, проверка об этом не узнает — зато и не
// запретит линии быть еле заметными, какими они и должны быть.

const background = valueOf('surface-void') || '#0A0A0A'
const failures = []
for (const name of TEXT_TOKENS) {
  const color = valueOf(name)
  if (!color) {
    failures.push(`токен --${name} не найден в палитре — проверка смотрит не туда`)
    continue
  }
  const ratio = contrast(color, background)
  if (ratio < 4.5) {
    failures.push(`--${name} (${color}) на фоне ${background}: контраст ${ratio.toFixed(2)} при пороге 4.5`)
  }
}

// Порядок ступеней — часть замысла: «muted» ярче «faint», иначе имена лгут.
const ladder = ['hub-muted', 'hub-faint', 'hub-faint-2', 'hub-faint-3']
for (let index = 1; index < ladder.length; index += 1) {
  const above = valueOf(ladder[index - 1])
  const below = valueOf(ladder[index])
  if (!above || !below) continue
  if (contrast(below, background) > contrast(above, background)) {
    failures.push(`--${ladder[index]} ярче --${ladder[index - 1]} — имена ступеней перестали соответствовать порядку`)
  }
}

if (failures.length) {
  console.error('НЕЧИТАЕМЫЙ ТЕКСТ:')
  for (const line of failures) console.error('  · ' + line)
  console.error('  Мельче знак — нужен больший контраст, а не меньший.')
  process.exit(1)
}
console.log('текстовые ступени палитры читаются')
