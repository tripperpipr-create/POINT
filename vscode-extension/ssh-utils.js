// Remote paths are untrusted values; keep their normalization audit surface small.
function normalizeSSHRemotePath(value) {
  const trimmed = String(value || '').trim().replace(/\\/g, '/')
  if (!trimmed) return '~'
  if (trimmed === '/') return '/'
  return trimmed.replace(/\/+$/, '') || '/'
}

function sshRemotePathParent(value) {
  const current = normalizeSSHRemotePath(value)
  if (current === '/' || current === '~' || current === '.') return current
  const separator = current.lastIndexOf('/')
  if (separator < 0) return '.'
  if (separator === 0) return '/'
  const parent = current.slice(0, separator)
  return parent === '~' ? '~' : parent
}

function sshRemotePathJoin(base, entry) {
  const current = normalizeSSHRemotePath(base)
  const name = String(entry || '').replace(/\/+$/, '')
  if (!name || name === '.' || name === '..' || name.includes('/') || name.includes('\\') || /[\r\n\0]/.test(name)) {
    throw new Error('Недопустимое имя удалённого файла')
  }
  return current === '/' ? `/${name}` : `${current}/${name}`
}

function sshRemotePickerEntries(entries) {
  return (Array.isArray(entries) ? entries : [])
    .map(raw => String(raw || '').replace(/\r$/, ''))
    .filter(raw => raw && raw !== './' && raw !== '../' && !/[\r\n\0]/.test(raw))
    .map(raw => {
      const directory = raw.endsWith('/')
      const name = directory ? raw.slice(0, -1) : raw
      return {
        label: `${directory ? '$(folder)' : '$(file)'} ${name}`,
        description: directory ? 'каталог' : 'файл · открыть предпросмотр',
        remoteName: name,
        directory,
      }
    })
}

module.exports = {
  normalizeSSHRemotePath,
  sshRemotePathParent,
  sshRemotePathJoin,
  sshRemotePickerEntries,
}
