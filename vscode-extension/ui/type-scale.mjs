// Размер текста задаётся шкалой, а не числом в слое.
//
// Замер живых поверхностей показал: 58% текста Хаба мельче 11px, и смысловое —
// обещания, подписи под числами, счётчики — набрано теми же ступенями, что и
// служебные рубрики. Причина не в шкале: мимо неё шли 62 литерала прямо в
// слоях (40 раз 9px, 22 раза 10px) в сокращённой записи `font:`. Пока литералы
// живут в слоях, любое решение о размере неисполнимо: поднимаешь токен — а
// половина надписей не двигается.
//
// Проверка статическая и намеренно грубая. Что видно на экране, меряет браузер
// (scripts/audit-hub-layout.mjs считает вычисленные размеры на отрисованных
// страницах); здесь важно другое — размер правят редко и вслепую, и вернуть
// литерал легче всего именно в сокращённой записи, где он теряется среди
// толщины и интерлиньяжа.
//
//   node vscode-extension/ui/type-scale.mjs

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const layersDir = path.join(here, 'layers')
const tokensFile = path.join(here, 'tokens.css')

// Пол шкалы. Ниже него в интерфейсе не опускаемся: 9px читается как шум даже
// при нормальном контрасте, а контраст стережёт отдельная проверка.
const FLOOR_PX = 10

const failures = []
const fail = message => failures.push(message)

// ── 1. Ступени шкалы не опускаются ниже пола ──────────────────────────────
//
// Шкала одна: --t-*. Вторая, дробная (--nc-t-*, плотность 0.7), была отменена
// вместе со сведением регистра оболочки к одному канону — теперь эти имена
// обязаны быть синонимами через var(), и это стережёт проверка 1b ниже.
// Дробные ступени считаются наравне с целыми: --nc-t-chip: 9.5px пряталась
// от проверки дважды — чужим префиксом и дробью.
const tokens = fs.readFileSync(tokensFile, 'utf8')
const scales = [
  { prefix: 't', label: 'продукт', pattern: /--(t-[a-z0-9-]+):\s*(\d+(?:\.\d+)?)px/g },
]
let totalSteps = 0
for (const scale of scales) {
  const steps = [...tokens.matchAll(scale.pattern)].map(match => ({ name: match[1], px: Number(match[2]) }))
  if (!steps.length) {
    fail(`в tokens.css не нашлось ни одной ступени --${scale.prefix}-*: проверять нечего`)
    continue
  }
  totalSteps += steps.length
  for (const step of steps) {
    if (step.px < FLOOR_PX) fail(`ступень --${step.name} (${scale.label}) задана ${step.px}px — ниже пола ${FLOOR_PX}px`)
  }

  // Две ступени с одним значением — почти всегда опечатка. Осознанный синоним
  // объявляется через var(), и такой случай проверка пропускает.
  const byValue = new Map()
  for (const step of steps) {
    const list = byValue.get(step.px) || []
    list.push(step.name)
    byValue.set(step.px, list)
  }
  for (const [px, names] of byValue) {
    if (names.length > 1) {
      fail(`ступени ${names.map(name => '--' + name).join(', ')} задают одно и то же значение ${px}px — это опечатка либо синоним, и синоним объявляется через var()`)
    }
  }
}

// ── 1b. Второй шкалы кеглей не существует ──────────────────────────────────
//
// Ступень, отмытая через токен, проходила мимо всех проверок: трещотка
// `ui/build.mjs` считает нарушения в слоях, а `--nc-t-num: 10.5px`, объявленная
// здесь, для неё чистая. Так регистр оболочки и оказался описан двумя шкалами
// сразу — дробной у окон инструментов и целой у канона. Теперь `--nc-t-*` и
// `--nc-r*` — синонимы канона, и объявляются только через var().
for (const match of tokens.matchAll(/--(nc-t-[a-z0-9-]+|nc-r|nc-r-[a-z0-9-]+):\s*([^;}]+)/g)) {
  const value = match[2].trim()
  if (value.startsWith('var(')) continue
  fail(`--${match[1]} задан значением «${value}» — у окон инструментов своей шкалы нет, `
    + 'имя обязано быть синонимом ступени канона через var()')
}

// ── 2. В слоях нет литеральных размеров ───────────────────────────────────
// Ищем и полную запись, и сокращённую: во второй размер стоит перед «/» и
// именно там прятались все 62 найденных литерала.
const literalLonghand = /font-size:\s*(\d+(?:\.\d+)?)px/g
const literalShorthand = /font:\s*[^;{}]*?\b(\d+(?:\.\d+)?)px(?=\s*\/)/g

for (const name of fs.readdirSync(layersDir).filter(file => file.endsWith('.css')).sort()) {
  const source = fs.readFileSync(path.join(layersDir, name), 'utf8')
  const lines = source.split(/\r?\n/)
  for (const [index, line] of lines.entries()) {
    for (const pattern of [literalLonghand, literalShorthand]) {
      pattern.lastIndex = 0
      for (const hit of line.matchAll(pattern)) {
        fail(`layers/${name}:${index + 1}: размер ${hit[1]}px задан числом — возьмите ступень шкалы (${hit[0].slice(0, 48)})`)
      }
    }
  }
}

if (failures.length) {
  console.error('ШКАЛА КЕГЛЯ НАРУШЕНА:')
  for (const message of failures) console.error('  · ' + message)
  process.exit(1)
}
console.log(`размеры текста берутся из шкалы: ступеней ${totalSteps}, пол ${FLOOR_PX}px, синонимы окон инструментов сверены`)
