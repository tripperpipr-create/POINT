import { useEffect, useMemo, useState } from 'react'
import { humanError } from '../i18n'
import type { AgentProfile, Workspace } from '../types'

const steps = ['Знакомство', 'Модель', 'Проект', 'Готово']
const examples = [
  'Объясни архитектуру этого проекта и найди основные точки входа.',
  'Найди потенциальную ошибку, предложи минимальное исправление и добавь тест.',
  'Проверь проект и предложи три наиболее полезных улучшения без изменения файлов.',
]

interface OnboardingProps {
  profiles: AgentProfile[]
  selectedId: string
  workspace?: Workspace
  apiKey: string
  onSelectProfile: (id: string) => void
  onApiKeyChange: (value: string) => void
  onSaveProfile: (profile: AgentProfile) => Promise<AgentProfile>
  canSelectWorkspace: boolean
  onOpenWorkspace: () => Promise<void>
  onFinish: (example?: string) => void
}

export default function Onboarding({ profiles, selectedId, workspace, apiKey, onSelectProfile, onApiKeyChange, onSaveProfile, canSelectWorkspace, onOpenWorkspace, onFinish }: OnboardingProps) {
  const source = useMemo(() => profiles.find(profile => profile.id === selectedId) ?? profiles[0], [profiles, selectedId])
  const [step, setStep] = useState(0)
  const [draft, setDraft] = useState<AgentProfile>(() => ({ ...source, allowedTools: [...source.allowedTools] }))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (source.id !== draft.id) setDraft({ ...source, allowedTools: [...source.allowedTools] })
  }, [source, draft.id])

  const setProvider = (provider: AgentProfile['provider']) => {
    const isDesktop = Boolean(window.go?.app?.App)
    setDraft(value => ({
      ...value,
      provider,
      baseUrl: provider === 'ollama'
        ? (isDesktop ? 'http://127.0.0.1:11434' : 'http://host.docker.internal:11434')
        : 'https://api.openai.com/v1',
      model: provider === 'ollama' ? 'qwen2.5-coder:7b' : 'gpt-4.1-mini',
    }))
  }

  const next = async () => {
    setError('')
    if (step === 1) {
      if (!draft.baseUrl.trim() || !draft.model.trim()) {
        setError('Укажите адрес сервиса и название модели.')
        return
      }
      try {
        const url = new URL(draft.baseUrl)
        if (!['http:', 'https:'].includes(url.protocol)) throw new Error('protocol')
      } catch {
        setError('Адрес сервиса должен быть корректным HTTP- или HTTPS-адресом.')
        return
      }
      setBusy(true)
      try {
        const saved = await onSaveProfile(draft)
        onSelectProfile(saved.id)
      } catch (reason) {
        setError(humanError(reason))
        return
      } finally {
        setBusy(false)
      }
    }
    if (step === 2 && !workspace) {
      setError(canSelectWorkspace ? 'Сначала откройте папку с проектом.' : 'Подключите рабочую папку при запуске web-клиента и обновите страницу.')
      return
    }
    setStep(value => Math.min(value + 1, steps.length - 1))
  }

  const openWorkspace = async () => {
    setBusy(true)
    setError('')
    try { await onOpenWorkspace() } catch (reason) { setError(humanError(reason)) } finally { setBusy(false) }
  }

  return <div className="onboarding-backdrop">
    <section className="onboarding" role="dialog" aria-modal="true" aria-labelledby="onboarding-title">
      <header className="onboarding-header">
        <div className="onboarding-brand"><span className="onboarding-logo"><img src="/point-icon.png" alt=""/></span><div><strong>Point</strong><small>ЛЁГКАЯ СРЕДА РАЗРАБОТКИ</small></div></div>
        <button className="onboarding-skip" onClick={() => onFinish()}>Пропустить</button>
      </header>

      <nav className="onboarding-steps" aria-label="Шаги настройки">
        {steps.map((label, index) => <div key={label} className={`${index === step ? 'active' : ''} ${index < step ? 'done' : ''}`}>
          <span>{index < step ? '✓' : index + 1}</span><small>{label}</small>
        </div>)}
      </nav>

      <div className="onboarding-content">
        {step === 0 && <div className="onboarding-intro">
          <span className="eyebrow">Добро пожаловать</span>
          <h1 id="onboarding-title">Ваш код. Ваш ритм.<br/><em>Одна главная точка.</em></h1>
          <p>Point — лёгкая самостоятельная IDE с быстрым переключением проектов и локальным coding-агентом. Все действия агента видны, а потенциально опасные операции требуют вашего решения.</p>
          <div className="onboarding-benefits">
            <article><b>01</b><strong>Быстрые проекты</strong><p>Переключайтесь через Ctrl Alt P без тяжёлого менеджера окон.</p></article>
            <article><b>02</b><strong>Ленивый запуск</strong><p>Агент и локальное ядро включаются только когда нужны.</p></article>
            <article><b>03</b><strong>Изменения через diff</strong><p>Вы увидите каждую правку до её применения.</p></article>
          </div>
        </div>}

        {step === 1 && <div className="onboarding-form">
          <span className="eyebrow">Шаг 2 из 4</span><h2 id="onboarding-title">Подключите модель</h2>
          <p>Выберите существующий профиль и укажите, где запущена модель. Ключ API хранится только в памяти до закрытия приложения.</p>
          <label className="onboarding-field">Профиль агента<select value={selectedId} onChange={event => onSelectProfile(event.target.value)}>{profiles.map(profile => <option key={profile.id} value={profile.id}>{profile.name}</option>)}</select></label>
          <div className="provider-cards">
            <button className={draft.provider === 'ollama' ? 'selected' : ''} onClick={() => setProvider('ollama')}><span>LOCAL</span><strong>Ollama</strong><small>Модель работает на вашем компьютере</small></button>
            <button className={draft.provider === 'openai-compatible' ? 'selected' : ''} onClick={() => setProvider('openai-compatible')}><span>API</span><strong>OpenAI-совместимый</strong><small>Облачный или собственный endpoint</small></button>
          </div>
          <div className="onboarding-fields-row"><label className="onboarding-field">Адрес сервиса<input value={draft.baseUrl} onChange={event => setDraft(value => ({ ...value, baseUrl: event.target.value }))}/></label><label className="onboarding-field">Модель<input value={draft.model} onChange={event => setDraft(value => ({ ...value, model: event.target.value }))}/></label></div>
          {draft.provider === 'openai-compatible' && <label className="onboarding-field">Ключ API<input type="password" autoComplete="off" value={apiKey} placeholder="Не сохраняется на диске" onChange={event => onApiKeyChange(event.target.value)}/></label>}
          <div className="onboarding-note"><span>i</span><p>{draft.provider === 'ollama' ? 'Перед началом убедитесь, что Ollama запущена и нужная модель загружена.' : 'Поддерживаются сервисы с endpoint /chat/completions в формате OpenAI.'}</p></div>
        </div>}

        {step === 2 && <div className="onboarding-project">
          <span className="eyebrow">Шаг 3 из 4</span><h2 id="onboarding-title">Откройте проект</h2>
          <p>Агент получит доступ только к выбранной рабочей папке. Выход за её границы и переходы через внешние символьные ссылки заблокированы.</p>
          <button className={`workspace-dropzone ${workspace ? 'ready' : ''}`} onClick={openWorkspace} disabled={busy || !canSelectWorkspace}>
            <span className="folder-illustration"><i/></span>
            {workspace ? <><strong>{workspace.name}</strong><small>{workspace.path}</small><em>{canSelectWorkspace ? 'Нажмите, чтобы выбрать другую папку' : 'Рабочая папка подключена при запуске'}</em></> : <><strong>{!canSelectWorkspace ? 'Рабочая папка не подключена' : busy ? 'Открываем папку…' : 'Выбрать папку проекта'}</strong><small>В web-режиме рабочая папка подключается при запуске приложения</small></>}
          </button>
          <div className="safety-row"><span>✓ Только выбранная папка</span><span>✓ Секреты скрываются</span><span>✓ Запись после подтверждения</span></div>
        </div>}

        {step === 3 && <div className="onboarding-ready">
          <div className="ready-mark">✓</div><span className="eyebrow">Настройка завершена</span><h2 id="onboarding-title">Всё готово к работе</h2>
          <p>Начните со своего запроса или выберите один из примеров — он появится в поле задачи.</p>
          <div className="example-tasks">{examples.map((example, index) => <button key={example} onClick={() => onFinish(example)}><b>0{index + 1}</b><span>{example}</span><em>Использовать →</em></button>)}</div>
        </div>}
        {error && <div className="onboarding-error">{error}</div>}
      </div>

      <footer className="onboarding-footer">
        <span>{step + 1} / {steps.length}</span>
        <div>{step > 0 && <button className="button ghost" onClick={() => { setError(''); setStep(value => value - 1) }}>Назад</button>}{step < steps.length - 1 ? <button className="button primary onboarding-next" disabled={busy} onClick={next}>{busy ? 'Сохраняем…' : step === 0 ? 'Начать настройку' : 'Продолжить'} <span>→</span></button> : <button className="button primary onboarding-next" onClick={() => onFinish()}>Открыть рабочую среду <span>→</span></button>}</div>
      </footer>
    </section>
  </div>
}
