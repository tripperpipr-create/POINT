const fs = require('node:fs')
const path = require('node:path')
const assert = require('node:assert/strict')
const { execFileSync } = require('node:child_process')
const { pathToFileURL } = require('node:url')
const { chromium } = require('../.cache/code-oss/node_modules/@playwright/test')

// Страница стенда пересобирается перед прогоном. Она уже расходилась с
// продуктом на двое суток: смоук искал кнопку «Отправить ответы», которой в
// разметке больше нет, и при этом проходил — потому что мерил снимок.
const fixture = path.resolve('build/master-sessions.html')
fs.writeFileSync(fixture, execFileSync(process.execPath,
  [path.resolve(__dirname, 'render-hub-surface.js'), 'master', 'sessions'],
  { maxBuffer: 64 * 1024 * 1024 }))

// Модуль отдаётся странице так же, как продукту, — сборкой, а не вырезанием.
//
// Здесь и сломалась эта проверка. Скрипт вставлял исходник `master-session-ui.js`
// обычным <script>, заменив «export function» на «function» и убрав строку
// реэкспорта. Пока модуль ни от кого не зависел, это работало. Потом в нём
// появился `import { masterAnswerNote… } from './master-questions-views.js'` —
// в классическом скрипте это синтаксическая ошибка, вставка целиком не
// исполнялась, обработчик кликов не навешивался, и панель разговора никогда не
// открывалась. Дефект зарегистрирован как SMOKE_MASTER_SESSION_CONTROLS_STALE.
//
// Сборка esbuild'ом — тем же, что делает media/main.js, — переживает и
// следующий импорт: цепочку зависимостей считает она, а не этот файл.
async function bundleSessionUi() {
  const { build } = require('../vscode-extension/node_modules/esbuild')
  const resolveDir = path.resolve(__dirname, '..', 'vscode-extension', 'ui', 'client')
  const result = await build({
    stdin: {
      contents: "import { handleMasterSessionAction, patchMasterAnswerNote } from './master-session-ui.js'\n"
        + 'window.handleMasterSessionAction = handleMasterSessionAction\n'
        + 'window.patchMasterAnswerNote = patchMasterAnswerNote\n',
      resolveDir,
      loader: 'js',
    },
    bundle: true,
    format: 'iife',
    platform: 'browser',
    target: ['es2022'],
    write: false,
    logLevel: 'silent',
  })
  return result.outputFiles[0].text
}

;(async () => {
  const browser = await chromium.launch({ channel: 'msedge', headless: true })
  try {
    const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
    await page.goto(pathToFileURL(path.resolve('build/master-sessions.html')).href)
    const source = await bundleSessionUi()
    // Ввод подключён так же, как в продукте (`ui/client/main.js`): счётчик у
    // «Продолжить» пересчитывается на каждом знаке, и без этого кнопка остаётся
    // `aria-disabled` — отвечать было бы нечем.
    await page.addScriptTag({ content: source + `
      window.posted = []; window.answers = []; window.drafts = {}; window.cursor = {};
      const hubRoot = document.querySelector('#root');
      hubRoot.addEventListener('click', event => {
        const target = event.target.closest('[data-action]'); if (!target) return;
        window.handleMasterSessionAction({action:target.dataset.action,target,root:hubRoot,vscode:{postMessage:value=>window.posted.push(value)},sending:false,send:value=>window.answers.push(value),render:()=>{},persist:()=>{},drafts:()=>window.drafts,cursor:()=>window.cursor})
      });
      hubRoot.addEventListener('input', event => {
        if (event.target.closest('.hall-compose')) window.patchMasterAnswerNote(hubRoot, window.drafts);
      });` })
    // Клик мимо кнопки не отправляет реплику.
    //
    // Настоящий closest, а не подставной: `data-action="master-send"` висел на
    // самом `<form>`, и доставка кликов находила форму от любого места внутри
    // неё. Смоуки на бандле этого увидеть не могли — они кликают выдуманным
    // действием. Здесь страница настоящая, и правило проверяется по ней.
    const owners = await page.evaluate(() => {
      const form = document.querySelector('form.hall-compose')
      if (!form) return { нетФормы: true }
      const at = selector => {
        const node = form.querySelector(selector)
        return node ? (node.closest('[data-action]')?.dataset.action ?? null) : 'узла нет'
      }
      return {
        поле: at('#master-input'),
        подсказка: at('.hall-compose-hint'),
        меню: at('.hall-work-menu > summary'),
        кнопка: at('.hall-compose-send'),
      }
    })
    assert.equal(owners.нетФормы, undefined, 'композер не нарисован — проверять нечего')
    for (const место of ['поле', 'подсказка', 'меню']) {
      assert.notEqual(owners[место], 'master-send',
        `клик по «${место}» отправит реплику: ближайшее действие — ${owners[место]}`)
    }
    assert.equal(owners.кнопка, 'master-send', 'кнопка отправки потеряла своё действие')

    // Поиск по разговорам и выбор строки проверяются теперь в
    // scripts/smoke-chat-directory-search.js и smoke-chat-directory-groups.js:
    // список стал кросс-проектным, уехал в свой модуль и больше не прячет
    // строки в готовом дереве, а отсеивает их при отрисовке. Здесь остались
    // только панели разговора, которые живут внутри чата.
    await page.getByRole('button',{name:'Действия с разговором'}).click()
    await page.locator('[data-master-session-title]').fill('Новый заголовок')
    await page.getByRole('button', { name: 'Переименовать', exact: true }).click()
    assert.equal((await page.evaluate(() => window.posted.at(-1))).value, 'Новый заголовок')
    await page.getByRole('button', { name: 'Память', exact: false }).click()
    await page.locator('[data-master-memory]').fill('Примеры на Go')
    await page.getByRole('button', { name: 'Сохранить память' }).click()
    assert.equal((await page.evaluate(() => window.posted.at(-1))).value, 'Примеры на Go')
    // Подробность ответа переехала из ряда управления в меню «•••»: её меняют
    // раз в месяц, а место в ряду она занимала всегда. «Память» это меню
    // закрывает — панели не открываются вдвоём, — поэтому открываем заново.
    await page.getByRole('button', { name: 'Действия с разговором' }).click()
    // Форма ответа читается из сессии честно: в снимке стоит «questions», и до
    // правки меню показывало «Авто», потому что знало только три формы из пяти.
    assert.equal(await page.locator('.hall-answer-style summary').innerText(), 'Ответ: Сначала вопросы')
    await page.locator('.hall-answer-style summary').click()
    await page.getByRole('button', { name: 'Подробно', exact: true }).click()
    assert.equal((await page.evaluate(() => window.posted.at(-1))).value, 'detailed')
    await page.locator('.hall-question .hall-question-extra').fill('Разработка Go-сервисов')
    await page.getByRole('button', { name: 'Продолжить', exact: true }).click()
    assert.match(await page.evaluate(() => window.answers[0]), /Мой ответ: Разработка Go-сервисов/)
    // «Новый чат» живёт в том же меню «•••», что и подробность ответа, и оно
    // уже открыто предыдущим шагом. Нажать «•••» ещё раз значило бы его
    // закрыть: панели переключаются, а не копятся.
    await page.locator('[data-session-panel="history"]').getByRole('button', { name: 'Новый чат', exact: false }).click()
    assert.equal((await page.evaluate(() => window.posted.at(-1))).action, 'new')
    for (const width of [390, 800, 1280, 1920]) {
      await page.setViewportSize({ width, height: 900 })
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
      await page.screenshot({path:'build/master-v2-'+width+'.png'})
    }
    console.log('Master chat controls: history search, switching, rename, memory, modes, question answers, responsive layout: PASS')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
