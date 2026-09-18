// Путь от разговора с Мастером до запущенного квеста — на живом ядре.
//
// Мастер предлагает квест, предложение уходит в очередь решений, и оттуда его
// запускают. Каждое звено по отдельности проверено, а цепочка целиком — не была:
// именно так и вышло, что очередь решений не могла решить ничего. Она несёт в
// каждом элементе описание запроса, которым его решают, клиент собирал тело по
// одному правилу на всех, ядро отвергало чужие поля, и каждое нажатие
// возвращало 400. Сломано это было у четырёх видов решений из пяти, а заметить
// можно было только пройдя путь до конца.
//
// Здесь проходят все основные сценарии разговора: обычная реплика → создание
// агента с подтверждением → выбор и создание отряда → предложение квеста →
// очередь → запуск по описанию из очереди → квест и прогон Flow.

const { spawn } = require('child_process')
const fs = require('fs')
const net = require('net')
const path = require('path')

const repo = path.join(__dirname, '..')
const { resolveCoreBinary } = require('./lib/core-binary')
const workspace = path.join(repo, 'examples', 'go-health')

function freePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer()
    server.unref()
    server.on('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const address = server.address()
      server.close(() => resolve(address.port))
    })
  })
}

const failures = []
const check = (name, ok, detail) => {
  if (ok) console.log('ok   ' + name)
  else failures.push(`${name}\n     ${String(detail).slice(0, 300)}`)
}

async function main() {
  let binary
  try {
    binary = resolveCoreBinary()
  } catch (error) {
    console.log(String(error.message))
    process.exit(1)
  }
  fs.mkdirSync(path.join(repo, 'build'), { recursive: true })
  const dataDir = fs.mkdtempSync(path.join(repo, 'build', 'smoke-master-'))
  const port = await freePort()
  const base = `http://127.0.0.1:${port}`
  const child = spawn(binary, [], {
    cwd: workspace,
    env: { ...process.env, DATA_DIR: dataDir, WORKSPACE_ROOT: workspace, HTTP_ADDR: `127.0.0.1:${port}`, REDIS_ADDR: '' },
    windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'],
  })
  let logs = ''
  child.stdout.on('data', chunk => { logs += chunk })
  child.stderr.on('data', chunk => { logs += chunk })

  const token = () => {
    const file = path.join(dataDir, 'api-token')
    return fs.existsSync(file) ? fs.readFileSync(file, 'utf8').trim() : ''
  }
  const call = async (route, init = {}) => {
    const response = await fetch(base + route, {
      ...init,
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token()}`, ...(init.headers || {}) },
    })
    const text = await response.text()
    let payload
    try { payload = JSON.parse(text) } catch { payload = text }
    return { status: response.status, payload }
  }

  try {
    let healthy = false
    for (let attempt = 0; attempt < 100; attempt += 1) {
      const health = await call('/api/health').catch(() => null)
      if (health && health.status === 200) { healthy = true; break }
      await new Promise(resolve => setTimeout(resolve, 100))
    }
    if (!healthy) throw new Error(`ядро не поднялось. ${logs}`)

    const boot = await call('/api/bootstrap')
    const workspaceId = boot.payload?.currentWorkspace?.id
    const blueprint = boot.payload?.blueprints?.[0]
    if (!workspaceId || !blueprint) throw new Error('мир или каталог чертежей не пришли — проверять нечего')

    const config = await call('/api/orchestrator/config', { method: 'POST', body: JSON.stringify({
      id: 'master', workspaceId, preset: 'conductor', provider: 'ollama', model: 'qwen',
      planningDepth: 70, parallelism: 60, approvalStrictness: 40, teamPreference: 85,
      createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z',
    }) })
    check('мастер настроен', config.status === 200, JSON.stringify(config.payload))

    const greeting = await call('/api/master/chat', { method: 'POST', body: JSON.stringify({ message: 'Привет! Как дела?' }) })
    check('мастер поддерживает обычный разговор',
      greeting.status === 200 && Boolean(greeting.payload?.response?.reply) && !greeting.payload?.response?.proposal && !greeting.payload?.response?.actionProposal,
      JSON.stringify(greeting.payload?.response))

    const beforeAgentDraft = await call('/api/bootstrap')
    const agentDraft = await call('/api/master/chat', { method: 'POST', body: JSON.stringify({ message: 'Создай backend-агента для проверки API' }) })
    const agentAction = agentDraft.payload?.response?.actionProposal
    check('мастер предложил черновик агента вместо квеста',
      agentDraft.status === 200 && agentAction?.kind === 'create_agent' && !agentAction?.continuationPrompt && !agentDraft.payload?.response?.proposal,
      JSON.stringify(agentDraft.payload?.response))
    const afterAgentDraft = await call('/api/bootstrap')
    check('черновик агента не изменил ростер без подтверждения',
      (afterAgentDraft.payload?.projectAgents || []).length === (beforeAgentDraft.payload?.projectAgents || []).length,
      JSON.stringify(afterAgentDraft.payload?.projectAgents))
    if (!agentAction) throw new Error('без черновика агента проверять подтверждение нечем')

    const modifiedAgent = await call('/api/companion/actions/decide', { method: 'POST', body: JSON.stringify({
      proposalId: agentAction.id, action: 'modify', name: 'Smoke Master Guardian',
      roleDescription: 'Проверяет backend API', mission: 'Не пропускать регрессии API',
    }) })
    check('правки агента сохранились до создания',
      modifiedAgent.status === 200 && modifiedAgent.payload?.proposal?.status === 'modified' && modifiedAgent.payload?.agent?.name === 'Smoke Master Guardian',
      JSON.stringify(modifiedAgent.payload))
    const agentHistory = await call('/api/master/history')
    const agentReply = (agentHistory.payload?.history || []).find(message => message.actionProposalId === agentAction.id)
    const agentReload = await call('/api/bootstrap')
    const persistedAgentDraft = (agentReload.payload?.companionActionProposals || []).find(item => item.id === agentAction.id)
    check('черновик агента восстановился вместе с диалогом',
      Boolean(agentReply) && persistedAgentDraft?.status === 'modified' && persistedAgentDraft?.agent?.name === 'Smoke Master Guardian',
      JSON.stringify({ agentReply, persistedAgentDraft }))

    const appliedAgent = await call('/api/companion/actions/decide', { method: 'POST', body: JSON.stringify({
      proposalId: agentAction.id, action: 'apply',
    }) })
    check('агент создан после явного подтверждения',
      appliedAgent.status === 200 && appliedAgent.payload?.proposal?.status === 'applied' && appliedAgent.payload?.agent?.name === 'Smoke Master Guardian',
      JSON.stringify(appliedAgent.payload))

    const secondAgentDraft = await call('/api/master/chat', { method: 'POST', body: JSON.stringify({ message: 'Создай ещё одного QA-агента для проверки тестов' }) })
    const secondAgentAction = secondAgentDraft.payload?.response?.actionProposal
    if (!secondAgentAction) throw new Error('Мастер не подготовил второго агента для проверки ручного состава')
    const appliedSecondAgent = await call('/api/companion/actions/decide', { method: 'POST', body: JSON.stringify({
      proposalId: secondAgentAction.id, action: 'apply', name: 'Smoke Master Reviewer',
    }) })
    check('второй агент создан через тот же простой диалог',
      appliedSecondAgent.status === 200 && Boolean(appliedSecondAgent.payload?.agent?.id),
      JSON.stringify(appliedSecondAgent.payload))

    const teamDraft = await call('/api/master/chat', { method: 'POST', body: JSON.stringify({ message: 'Выбери отряд для backend-задачи' }) })
    const teamAction = teamDraft.payload?.response?.actionProposal
    check('мастер предложил подходящий отряд',
      teamDraft.status === 200 && teamAction?.kind === 'create_team' && teamAction?.team?.agentIds?.length > 0 && !teamDraft.payload?.response?.proposal,
      JSON.stringify(teamDraft.payload?.response))
    if (!teamAction) throw new Error('без черновика отряда проверять создание нечем')

    const selectedAgentId = appliedAgent.payload.agent.id
    const modifiedTeam = await call('/api/companion/actions/decide', { method: 'POST', body: JSON.stringify({
      proposalId: teamAction.id, action: 'modify', name: 'Smoke Master Squad', agentIds: [selectedAgentId],
    }) })
    check('ручной состав отряда сохранён до создания',
      modifiedTeam.status === 200 && modifiedTeam.payload?.proposal?.status === 'modified'
        && modifiedTeam.payload?.team?.agentIds?.length === 1 && modifiedTeam.payload.team.agentIds[0] === selectedAgentId,
      JSON.stringify(modifiedTeam.payload))
    const teamHistory = await call('/api/master/history')
    const teamReply = (teamHistory.payload?.history || []).find(message => message.actionProposalId === teamAction.id)
    const teamReload = await call('/api/bootstrap')
    const persistedTeamDraft = (teamReload.payload?.companionActionProposals || []).find(item => item.id === teamAction.id)
    check('выбранный отряд восстановился вместе с диалогом',
      Boolean(teamReply) && persistedTeamDraft?.status === 'modified' && persistedTeamDraft?.team?.agentIds?.length === 1,
      JSON.stringify({ teamReply, persistedTeamDraft }))

    const appliedTeam = await call('/api/companion/actions/decide', { method: 'POST', body: JSON.stringify({
      proposalId: teamAction.id, action: 'apply',
    }) })
    check('отряд создан после явного подтверждения',
      appliedTeam.status === 200 && appliedTeam.payload?.proposal?.status === 'applied'
        && appliedTeam.payload?.team?.agentIds?.length === 1 && appliedTeam.payload.team.agentIds[0] === selectedAgentId,
      JSON.stringify(appliedTeam.payload))

    const chat = await call('/api/master/chat', { method: 'POST', body: JSON.stringify({ message: 'Почини флаки-тест оплаты' }) })
    const proposal = chat.payload?.response?.proposal
    check('мастер предложил квест на задачу', Boolean(proposal), JSON.stringify(chat.payload?.response?.reply))
    if (!proposal) throw new Error('без предложения остальную цепочку проверять нечем')

    const modifiedQuest = await call('/api/quest-proposals/decide', { method: 'POST', body: JSON.stringify({
      proposalId: proposal.id, action: 'modify', title: 'Стабилизировать оплату',
      objectives: ['Устранить нестабильность теста оплаты'], definitionOfDone: ['Тест оплаты стабильно проходит'],
      teamAgentIds: [appliedSecondAgent.payload.agent.id],
    }) })
    check('квест и его отряд изменены до запуска',
      modifiedQuest.status === 200 && modifiedQuest.payload?.proposal?.status === 'modified'
        && modifiedQuest.payload?.proposal?.teamAgentIdsLocked === true
        && modifiedQuest.payload?.proposal?.teamAgentIds?.[0] === appliedSecondAgent.payload.agent.id,
      JSON.stringify(modifiedQuest.payload))
    const questHistory = await call('/api/master/history')
    const questReply = (questHistory.payload?.history || []).find(message => message.proposalId === proposal.id)
    const questReload = await call('/api/bootstrap')
    const persistedQuest = (questReload.payload?.questProposals || []).find(item => item.id === proposal.id)
    check('изменённый квест восстановился вместе с диалогом',
      Boolean(questReply) && persistedQuest?.title === 'Стабилизировать оплату' && persistedQuest?.teamAgentIdsLocked === true,
      JSON.stringify({ questReply, persistedQuest }))

    const queue = await call('/api/decisions')
    const item = (queue.payload?.items || []).find(entry => entry.id === proposal.id)
    check('предложение попало в очередь решений', Boolean(item), JSON.stringify(queue.payload?.items))
    if (!item) throw new Error('без элемента очереди проверять запуск неоткуда')

    // Тело собирается ровно по описанию из очереди — как это делает расширение.
    const body = {}
    if (item.resolve.field) {
      body[item.resolve.field] = item.resolve.acceptValue === undefined ? item.resolve.accept : item.resolve.acceptValue
    }
    if (item.resolve.idField) body[item.resolve.idField] = item.id
    const started = await call(item.resolve.path, { method: 'POST', body: JSON.stringify(body) })
    check('запуск из очереди принят ядром', started.status === 200, JSON.stringify(started.payload))
    check('запуск создал квест', Boolean(started.payload?.quest?.id), JSON.stringify(started.payload).slice(0, 200))
    check('запуск сохранил выбранный вручную отряд',
      started.payload?.team?.agentIds?.length === 1 && started.payload.team.agentIds[0] === appliedSecondAgent.payload.agent.id,
      JSON.stringify(started.payload?.team))

    const after = await call('/api/bootstrap')
    const quests = (after.payload?.quests || []).filter(quest => !quest.parentId)
    check('квест верхнего уровня ровно один', quests.length === 1, `их ${quests.length}`)
    check('прогон Flow запущен', (after.payload?.flowRuns || []).length > 0, 'ни одного прогона')

    const queueAfter = await call('/api/decisions')
    const stillWaiting = (queueAfter.payload?.items || []).some(entry => entry.id === proposal.id)
    check('решённое предложение ушло из очереди', !stillWaiting, 'предложение всё ещё ждёт решения')

    // Второй запуск того же предложения создал бы второй квест с тем же отрядом.
    const again = await call(item.resolve.path, { method: 'POST', body: JSON.stringify(body) })
    check('повторный запуск отвергнут', again.status !== 200, JSON.stringify(again.payload).slice(0, 160))
    const afterRepeat = await call('/api/bootstrap')
    check('второго квеста не появилось',
      (afterRepeat.payload?.quests || []).filter(quest => !quest.parentId).length === 1,
      'из одного предложения вышло больше одного квеста')
  } finally {
    child.kill()
  }

  if (failures.length) {
    console.log('ПУТЬ ДО КВЕСТА — ПРОВАЛ:')
    for (const line of failures) console.log('  ' + line)
    process.exit(1)
  }
  console.log('обычный разговор, агент, отряд и квест через Мастера: PASS')
}

main().catch(error => {
  console.log('ПУТЬ ДО КВЕСТА — ПРОВАЛ: ' + error.message)
  process.exit(1)
})
