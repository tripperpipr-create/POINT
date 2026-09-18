const vscode = acquireVsCodeApi()

function selectCommitRow(hash) {
  for (const row of document.querySelectorAll('.commit-row')) {
    row.classList.toggle('selected', row.dataset.hash === hash)
  }
}

function applyDetail(message) {
  const detail = document.querySelector('.commit-detail')
  if (detail && typeof message.detailHtml === 'string') detail.innerHTML = message.detailHtml
  if (message.selectedHash) selectCommitRow(message.selectedHash)
}

function applySnapshot(message) {
  const listScroll = document.querySelector('.commit-list > div')
  const scrollTop = listScroll?.scrollTop ?? 0
  if (typeof message.bodyHtml === 'string') {
    const root = document.querySelector('body')
    if (!root) return
    const script = root.querySelector('script')
    root.innerHTML = message.bodyHtml
    if (script) root.appendChild(script)
  } else {
    if (typeof message.headerHtml === 'string') {
      const header = document.querySelector('.chronicle > header')
      if (header) header.outerHTML = message.headerHtml
    }
    if (typeof message.workingTreeHtml === 'string') {
      const tree = document.querySelector('.working-tree')
      if (tree) tree.outerHTML = message.workingTreeHtml
    }
    if (typeof message.commitListHtml === 'string') {
      const list = document.querySelector('.commit-list > div')
      if (list) list.innerHTML = message.commitListHtml
    }
    applyDetail(message)
  }
  const nextList = document.querySelector('.commit-list > div')
  if (nextList) nextList.scrollTop = scrollTop
  if (message.selectedHash) selectCommitRow(message.selectedHash)
}

document.body.addEventListener('click', event => {
  const target = event.target.closest('[data-action]')
  if (!target) return
  const action = target.dataset.action
  if (action === 'selectCommit') vscode.postMessage({ type: 'selectCommit', hash: target.dataset.hash || '' })
  if (action === 'refresh') vscode.postMessage({ type: 'refresh' })
  if (action === 'openFile') vscode.postMessage({ type: 'openFile', path: target.dataset.path || '' })
  if (action === 'openWorkingTree') vscode.postMessage({ type: 'openWorkingTree' })
})

window.addEventListener('message', event => {
  const message = event.data || {}
  if (message.type === 'detail') applyDetail(message)
  if (message.type === 'snapshot') applySnapshot(message)
})
