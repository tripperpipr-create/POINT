// Общий стенд смоуков вебвью: собранный media/main.js в изолированном
// контексте с фейковым DOM. Раскладка — как у настоящей поверхности
// (`data-layout`), сообщения хоста — через window.message, нажатия — через
// тот же обработчик, что у человека.
//
// Смоук видит разметку и отправленные хосту сообщения. Раскладку и цвет он
// не видит — это работа стенда (render-hub-surface.js) и аудита.

const fs = require('fs')
const path = require('path')
const vm = require('vm')

const mainPath = path.join(__dirname, '..', '..', 'vscode-extension', 'media', 'main.js')

function bootWebview({ layout = 'wide', dataset = {} } = {}) {
  const listeners = {}
  const posted = []
  const root = {
    innerHTML: '',
    addEventListener(type, callback) { (listeners[`root:${type}`] ||= []).push(callback) },
    querySelector() { return null },
    querySelectorAll() { return [] },
  }
  const context = {
    acquireVsCodeApi: () => ({ postMessage(message) { posted.push(message) }, getState() {}, setState() {} }),
    document: { getElementById: id => (id === 'root' ? root : undefined), body: { dataset: { layout, ...dataset } } },
    window: { addEventListener(type, callback) { listeners[`window:${type}`] = callback } },
    console, Date, Map, Set, CSS: { escape: value => String(value) },
    requestAnimationFrame(callback) { callback(); return 0 },
    cancelAnimationFrame() {},
    setTimeout(callback) { callback(); return 0 },
    clearTimeout() {},
  }
  vm.runInNewContext(fs.readFileSync(mainPath, 'utf8'), context, { filename: 'media/main.js' })
  const fire = (type, event) => (listeners[`root:${type}`] || []).forEach(callback => callback(event))
  const target = (dataset, extra = {}) => ({ dataset, closest: () => null, matches: () => false, ...extra })
  return {
    root,
    posted,
    send(data) { listeners['window:message']({ data }) },
    click(dataset, extra = {}) {
      const element = target(dataset, extra)
      fire('click', { target: { ...element, closest: selector => (selector === '[data-action]' ? element : null) } })
    },
    type(key, value) {
      for (const kind of ['input', 'change']) fire(kind, { target: target({ draft: key }, { value, type: 'text' }) })
    },
    state(extra = {}) {
      listeners['window:message']({ data: { type: 'state', service: { state: 'running' }, workspaceTrusted: true, workspace: 'ai-ide', boot: {}, ...extra } })
    },
    take() { return posted.splice(0) },
  }
}

module.exports = { bootWebview }
