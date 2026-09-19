// Подключения: своё окно и выбор модели прямо в разговоре.
//
// Подключений может быть сколько угодно, заводят их один раз и надолго, а
// выбирают из них часто. До этой проверки список жил вкладкой внутри Хаба
// агентов, а сменить модель можно было только через мастер настройки — то есть
// за сменой модели надо было идти в агентов.
//
// Здесь закрепляются три правила:
//   1. окно подключений — своё, и открывается своей командой;
//   2. список подключений живёт в одном месте, а не в двух;
//   3. чип модели стоит и у Помощника, и у Мастера, и меняет сохранённую
//      настройку целиком, унося за собой адрес и вид провайдера.

const assert = require('assert')
const fs = require('fs')
const path = require('path')
const { extensionHostSource } = require('./lib/extension-host-source')

const root = path.resolve(__dirname, '..')
const read = file => fs.readFileSync(path.join(root, file), 'utf8')

// Хост читается пакетом, а не одним файлом: показ окна связей уехал в
// `hub-surfaces-controller.js`, где провайдер приходит доводом. Приёмник
// сводится к `this.`, чтобы утверждения ниже значили то же, что и раньше.
const extension = extensionHostSource(root).split('provider.').join('this.')
const main = read('vscode-extension/ui/client/main.js')
const clientDir = path.join(root, 'vscode-extension/ui/client')
const client = fs.readdirSync(clientDir)
  .filter(name => name.endsWith('.js'))
  .map(name => fs.readFileSync(path.join(clientDir, name), 'utf8'))
  .join('\n')
const picker = read('vscode-extension/ui/client/model-picker.js')
const infrastructure = read('vscode-extension/ui/client/infrastructure-views.js')
const runtime = read('vscode-extension/ui/client/quest-runtime-views.js')
const manifest = JSON.parse(read('vscode-extension/package.json'))
const registry = read('distribution/resources/point-tool-windows.ts.txt')

// ── 1. Своё окно ────────────────────────────────────────────────────────────
assert.ok(
  manifest.contributes.commands.some(item => item.command === 'localAgent.openConnections'),
  'подключения обязаны открываться своей командой',
)
assert.ok(
  manifest.activationEvents.includes('onCommand:localAgent.openConnections'),
  'команда подключений обязана будить расширение',
)
assert.match(extension, /showConnections\(\)\s*\{/, 'у окна подключений должен быть свой метод показа')
assert.match(extension, /createWebviewPanel\(\s*'point\.connections'/, 'подключения — своя панель редактора, а не вкладка Хаба')
assert.match(extension, /this\.html\(this\.connectionsPanel\.webview, 'connections'\)/, 'окно обязано просить свою раскладку')
// Панель, о которой не знает рассылка, показывает состояние на момент открытия
// и больше не обновляется: заведённое подключение не появилось бы в списке.
assert.match(extension, /this\.connectionsPanel && this\.connectionsPanel\.visible !== false/, 'рассылка состояния обязана знать об окне')
assert.match(extension, /\|\| \(this\.connectionsPanel && this\.connectionsPanel\.visible\)/, 'видимость Хаба обязана считать и это окно')
// Состояние рассылается поимённо: `post()` пропускает `type: 'state'` дальше,
// но собирает и отправляет его `postState`. Панель, которой нет в его списке,
// открывается пустой — с экраном «Выберите проект» при открытом проекте.
assert.match(
  extension,
  /this\.connectionsPanel && \(force \|\| this\.connectionsPanel\.visible !== false\)/,
  'postState обязан слать состояние в окно подключений',
)
assert.match(registry, /'Подключения', 'plug', 'localAgent\.openConnections'/, 'окно обязано быть в реестре окон')
assert.match(client, /function isConnectionsView\(\)/, 'вебвью обязано различать своё окно подключений')

// ── 2. Один список, а не два ────────────────────────────────────────────────
// Вкладка Хаба оставляет только вход. Держать каталог в двух местах значило бы
// завести две правды о том, чем отвечает Помощник и Мастер.
assert.match(infrastructure, /if \(isConnectionsView\(\)\) \{[\s\S]{0,600}connectionManagerHtml/, 'каталог подключений рисуется в своём окне')
const hubBranch = infrastructure.slice(infrastructure.indexOf('<span>СВЯЗИ</span>'))
assert.ok(
  !hubBranch.slice(0, hubBranch.indexOf('</main>')).includes('connectionManagerHtml'),
  'вкладка Хаба не должна повторять каталог подключений',
)
assert.match(infrastructure, /data-action="open-connections"/, 'из Хаба должен быть вход в окно подключений')

// ── 3. Выбор модели в разговоре ─────────────────────────────────────────────
assert.match(picker, /function modelChipHtml\(/, 'чип модели обязан быть общим, а не своим у каждого разговора')
assert.match(picker, /function modelPickerListHtml\(/, 'у чипа обязан быть список подключений и их моделей')
assert.match(client, /modelChipHtml\(\{ target: 'companion'/, 'чип обязан стоять у Помощника')
assert.match(client, /modelChipHtml\(\{ target: 'master'/, 'чип обязан стоять у Мастера')
// Имя модели не должно встать четвёртым местом: у Помощника чип занял строку
// «модель подключена / локальный режим», у Мастера — кнопку «Настройки мастера».
assert.doesNotMatch(client, /'модель подключена' : 'локальный режим'/, 'чип обязан заменить прежнюю подпись, а не встать рядом')

// Настройка уходит целиком: ядро принимает конфигурацию, а не одно поле.
assert.match(picker, /type: 'saveCompanionConfig', config: bound\(/, 'выбор у Помощника обязан сохранять всю настройку')
assert.match(picker, /type: 'saveOrchestratorConfig', config: bound\(/, 'выбор у Мастера обязан сохранять всю настройку')
// Адрес и вид провайдера принадлежат подключению. Оставь их прежними — и ядро
// пошло бы к старому серверу под новым ключом.
assert.match(picker, /providerPreset: connection\?\.presetId/, 'смена подключения обязана унести за собой пресет')
assert.match(picker, /baseUrl: connection\?\.baseUrl \|\| ''/, 'смена подключения обязана унести за собой адрес')
// Поведение чипа принадлежит модулю выбора модели, а не общему обработчику
// кликов: main.js стоит у предела в семь тысяч строк, и растить его нечем.
assert.match(picker, /function handleModelChipAction\(/, 'действия чипа обязаны жить в модуле')
assert.match(client, /handleModelChipAction\(\{ action, target, vscode \}\)/, 'обработчик кликов обязан звать разбор действий чипа')

console.log('smoke-point-connections: ok')
