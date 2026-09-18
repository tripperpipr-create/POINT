// Опыт проекта: статистика, бюджет, резервные копии и ручное обучение.
//
// Девять веток о том, что проект накопил и что с этим делать: сводка
// расхода, потолок трат, снимок состояния и восстановление из него, поиск по
// прошлому опыту, ручной урок и откат автономного улучшения агента.
//
// Зависимостей нет: обработчик вызывается через .call(this, message).
// Восстановление из копии по-прежнему живёт в backup-controller.js — у него
// свой смоук, и сводить их в один файл значило бы прятать проверяемый шов.

const vscode = require('vscode')

async function handleLearningMessage(message) {
  switch (message.type) {
    case 'loadStatistics': {
      const statistics = await this.loadStatisticsSnapshot()
      this.post({ type: 'statistics', statistics })
      break
    }
    case 'createSystemBackup': {
      await this.service.request('/api/system/backups', {
        method: 'POST', body: JSON.stringify({ reason: 'manual' }),
      })
      const statistics = await this.loadStatisticsSnapshot()
      this.post({ type: 'statistics', statistics })
      void vscode.window.showInformationMessage('Проверенный backup создан. Retention применён автоматически.')
      break
    }
    case 'restoreSystemBackup': {
      await restoreSystemBackup({
        service: this.service, window: vscode.window, processIsAlive,
        refresh: () => this.refresh(), loadStatisticsSnapshot: () => this.loadStatisticsSnapshot(),
        post: message => this.post(message),
      })
      break
    }
    case 'searchExperience': {
      const query = String(message.query || '').trim()
      const items = await this.service.request(`/api/experience/search?q=${encodeURIComponent(query)}&limit=30`)
      this.post({ type: 'experienceSearch', query, items })
      break
    }
    case 'previewManualLearning': {
      const preview = await this.service.request('/api/learning/manual/preview', {
        method: 'POST', body: JSON.stringify(message.request || {}),
      })
      this.post({ type: 'manualLearningPreview', preview, request: message.request || {} })
      break
    }
    case 'applyManualLearning': {
      const improvement = await this.service.request('/api/learning/manual/apply', {
        method: 'POST', body: JSON.stringify(message.request || {}),
      })
      await this.refresh()
      this.post({ type: 'manualLearningApplied', improvement })
      void vscode.window.showInformationMessage('Урок применён и записан в журнал развития. Его можно точно откатить в Статистике.')
      break
    }
    case 'rollbackAgentImprovement': {
      const id = String(message.id || '').trim()
      if (!id) throw new Error('Не выбрана версия улучшения для отката.')
      const answer = await vscode.window.showWarningMessage(
        'Откатить последнюю применённую версию развития? Skill, Memory и постоянные Rules вернутся к точным предыдущим снимкам; хроника и доказательства останутся.',
        { modal: true },
        'Откатить версию',
      )
      if (answer !== 'Откатить версию') break
      await this.service.request(`/api/agent-improvements/${encodeURIComponent(id)}/rollback`, { method: 'POST', body: '{}' })
      await this.refreshGuildState()
      const [statistics, systemHealth] = await Promise.all([
        this.service.request('/api/statistics'),
        this.service.request('/api/system/diagnostics'),
      ])
      statistics.systemHealth = systemHealth
      this.post({ type: 'statistics', statistics })
      this.postState()
      void vscode.window.showInformationMessage('Версия Skill/Memory/Rules откачена; запись осталась в хронике развития.')
      break
    }
    case 'promoteAgentImprovement': {
      const id = String(message.id || '').trim()
      if (!id) throw new Error('Не выбрана canary-версия для продвижения.')
      const answer = await vscode.window.showWarningMessage(
        'Продвинуть проверенную canary-версию Skill во все совместимые проекты этого Blueprint? Точная предыдущая версия останется доступна для отката.',
        { modal: true },
        'Продвинуть Skill',
      )
      if (answer !== 'Продвинуть Skill') break
      await this.service.request(`/api/agent-improvements/${encodeURIComponent(id)}/promote`, { method: 'POST', body: '{}' })
      await this.refreshGuildState()
      const statistics = await this.service.request('/api/statistics')
      this.post({ type: 'statistics', statistics })
      this.postState()
      void vscode.window.showInformationMessage('Canary-версия Skill продвинута в Blueprint после явного подтверждения.')
      break
    }
    case 'saveBudget': {
      await this.service.request('/api/budget', {
        method: 'POST',
        body: JSON.stringify(message.budget || {}),
      })
      const statistics = await this.loadStatisticsSnapshot()
      this.post({ type: 'statistics', statistics })
      break
    }
  }
}

module.exports = { handleLearningMessage }
