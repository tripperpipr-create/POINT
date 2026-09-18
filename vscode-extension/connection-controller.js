// Подключения к моделям: сохранение, проверка, выбор по умолчанию, удаление.
//
// Домен выделен из composition root по той же причине, что и остальные
// контроллеры: четыре ветки одного предмета в общем switch росли вместе с
// самим предметом и уже вывели `extension.js` за строчный бюджет.
//
// Ключ живёт только в SecretStorage расширения. Наружу уходит `secretRef`, а
// само значение — транзитом в один запрос проверки и никогда не пишется ни в
// SQLite, ни в журнал.
function createConnectionController({ secrets, request, getConnections, upsert, remove, patch }) {
  // Правка без нового ключа обязана сохранить прежний secretRef: иначе
  // «переименовать подключение» тихо разлогинивало бы его.
  function keepSecretRef(message, provider) {
    if (message.apiKey) return `point.connection.${provider || 'custom'}.${Date.now()}`
    const previous = message.id ? getConnections().find(item => item.id === message.id) : undefined
    return String(message.secretRef || previous?.secretRef || '')
  }

  async function save(message) {
    const provider = String(message.provider || '')
    const secretRef = keepSecretRef(message, provider)
    if (message.apiKey && secretRef) await secrets.store(secretRef, String(message.apiKey))
    const saved = await request('/api/connections', {
      method: 'POST',
      body: JSON.stringify({
        id: message.id || '',
        provider,
        presetId: String(message.presetId || ''),
        displayName: String(message.displayName || provider || 'Подключение'),
        baseUrl: String(message.baseUrl || ''),
        // Версия API нужна только Azure и живёт при подключении: в адресе её не
        // спрятать, нормализация URL срезает query.
        apiVersion: String(message.apiVersion || ''),
        defaultModel: String(message.defaultModel || ''),
        secretRef,
        status: message.status || 'unknown',
      }),
    })
    upsert(saved)
  }

  async function probe(message) {
    const id = String(message.id || '')
    const connection = getConnections().find(item => item.id === id)
    const apiKey = connection?.secretRef ? await secrets.get(connection.secretRef) || '' : ''
    upsert(await request(`/api/connections/${encodeURIComponent(id)}/probe`, {
      method: 'POST',
      body: JSON.stringify({ apiKey }),
    }))
  }

  async function makeDefault(message) {
    const id = String(message.id || '')
    await request(`/api/connections/${encodeURIComponent(id)}/default`, { method: 'POST', body: '{}' })
    patch(getConnections().map(item => ({ ...item, isDefault: item.id === id })))
  }

  async function drop(message) {
    const id = String(message.id || '')
    const connection = getConnections().find(item => item.id === id)
    await request(`/api/connections/${encodeURIComponent(id)}`, { method: 'DELETE' })
    // Ядро отказывает, пока на подключение ссылаются, поэтому досюда доходит
    // только действительно свободное — ключ можно убирать вместе с ним.
    if (connection?.secretRef) await secrets.delete(connection.secretRef)
    remove(id)
  }

  // Возвращает true, если сообщение обработано: composition root по-прежнему
  // решает, что делать дальше (postState), но не знает подробностей домена.
  async function handle(message) {
    switch (message?.type) {
      case 'saveConnection': await save(message); return true
      case 'probeConnection': await probe(message); return true
      case 'defaultConnection': await makeDefault(message); return true
      case 'deleteConnection': await drop(message); return true
      default: return false
    }
  }

  return { handle }
}

module.exports = { createConnectionController }
