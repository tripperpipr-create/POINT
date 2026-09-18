const endpoint = process.argv[2];
const requestedView = process.argv[3];
if (!endpoint || !['composer', 'character'].includes(requestedView)) {
  throw new Error('Usage: node verify-point-agent-nav.mjs <endpoint> <composer|character>');
}

const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
async function waitFor(description, probe, timeout = 30000) {
  const deadline = Date.now() + timeout;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const value = await probe();
      if (value) return value;
    } catch (error) {
      lastError = error;
    }
    await delay(120);
  }
  throw new Error(`Timed out waiting for ${description}${lastError ? `: ${lastError.message}` : ''}`);
}


// У расширения несколько вебвью: Чертог, компаньон в боковой панели и окна
// инструментов. Раньше брался первый попавшийся кадр с нужным extensionId — и
// если им оказывался не Чертог, проверка тридцать секунд ждала разметку,
// которой на той поверхности нет, и падала «Timed out waiting for application
// context». Поэтому кандидатов перебираем и оставляем того, кто действительно
// отвечает разметкой Чертога.
function connect(url) {
  const socket = new WebSocket(url);
  const pending = new Map();
  const contexts = new Map();
  const runtimeErrors = [];
  let sequence = 0;
  socket.addEventListener('message', event => {
    const message = JSON.parse(String(event.data));
    if (message.method === 'Runtime.executionContextCreated') {
      contexts.set(message.params.context.id, message.params.context);
      return;
    }
    if (message.method === 'Runtime.executionContextDestroyed') {
      contexts.delete(message.params.executionContextId);
      return;
    }
    if (message.method === 'Runtime.exceptionThrown') {
      const details = message.params?.exceptionDetails || {};
      const description = details.exception?.description || details.text || 'Unknown runtime exception';
      runtimeErrors.push(String(description).slice(0, 1200));
      if (runtimeErrors.length > 8) runtimeErrors.shift();
      return;
    }
    if (!message.id || !pending.has(message.id)) return;
    const handlers = pending.get(message.id);
    pending.delete(message.id);
    if (message.error) handlers.reject(new Error(message.error.message)); else handlers.resolve(message.result);
  });
  const command = (method, params = {}) => {
    const id = ++sequence;
    socket.send(JSON.stringify({ id, method, params }));
    return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
  };
  const opened = new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, { once: true });
    socket.addEventListener('error', reject, { once: true });
  });
  return { socket, command, contexts, runtimeErrors, opened };
}

// Признак поверхности — корень вебвью и раскладка, которую ставит именно окно
// Чертога (html(webview, 'wide') в extension.js). Глобальные имена не годятся:
// бандл собран с format: 'iife', и верхние привязки закрыты замыканием.
async function tryHub(candidate, budgetMs) {
  const link = connect(candidate.webSocketDebuggerUrl);
  try {
    await link.opened;
    await Promise.all([link.command('Runtime.enable'), link.command('Page.enable')]);
    const deadline = Date.now() + budgetMs;
    while (Date.now() < deadline) {
      for (const context of link.contexts.values()) {
        try {
          const result = await link.command('Runtime.evaluate', {
            contextId: context.id,
            expression: `document.body?.dataset?.layout === 'wide' && Boolean(document.getElementById('root'))`,
            returnByValue: true,
          });
          if (result.result?.value === true) return { link, appContextId: context.id };
        } catch {}
      }
      await delay(120);
    }
  } catch {}
  link.socket.close();
  return null;
}

const hub = await waitFor('Point Agent Hub webview', async () => {
  const targets = await fetch(`${endpoint}/json/list`).then(response => response.json());
  const candidates = targets.filter(candidate =>
    candidate.type === 'iframe'
      && String(candidate.url).includes('extensionId=local-agent.local-agent-workbench')
      && candidate.webSocketDebuggerUrl);
  for (const candidate of candidates) {
    // Бюджет на кандидата короткий: их несколько, общее ожидание держит waitFor.
    const attempt = await tryHub(candidate, 2500);
    if (attempt) return attempt;
  }
  return null;
}, 60000);

const socket = hub.link.socket;
const command = hub.link.command;
const appContextId = hub.appContextId;

try {
  const evaluate = expression => command('Runtime.evaluate', {
    contextId: appContextId,
    expression,
    returnByValue: true,
    awaitPromise: true,
  }).then(result => {
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.text || 'Point Agent Hub evaluation failed');
    return result.result?.value;
  });

  // Чистый профиль открывается на онбординге. Он проходится теми же кнопками,
  // что и человеком: тест обязан доказывать первый запуск, а не обходить его
  // сообщением в хост — сообщение отправить неоткуда, да и обход проверял бы
  // не тот путь, которым пользуются.
  await waitFor('Agent Hub first screen', () => evaluate(`Boolean(document.querySelector('.hall'))`));
  for (let step = 0; step <= 12; step += 1) {
    // Маркеры намеренно не совпадают ни с одним идентификатором шага: у
    // последнего шага data-step равен "complete", и общее значение здесь уже
    // обрывало цикл на шаг раньше — кнопку «Готово» никто не нажимал.
    const moved = await evaluate(`(() => {
      const finish = document.querySelector('[data-action="complete-onboarding"]');
      if (finish) { finish.click(); return '@finished'; }
      const next = document.querySelector('.onboarding-footer [data-action="onboarding-step"].primary:not([disabled])');
      if (next) { next.click(); return next.dataset.step || '@next'; }
      return document.querySelector('.onboarding') ? '' : '@already-done';
    })()`);
    if (moved === '@finished' || moved === '@already-done') break;
    if (!moved) {
      const stuck = await evaluate(`(document.querySelector('.onboarding-panel h2')?.textContent || '').trim()`);
      throw new Error(`Onboarding has no way forward on step "${stuck}"`);
    }
    if (step === 12) throw new Error('Onboarding did not finish within 12 steps');
    await delay(200);
  }
  await waitFor('completed Agent Hub onboarding', () =>
    evaluate(`!document.querySelector('.onboarding') && Boolean(document.querySelector('.hall-main'))`));

  const hall = await evaluate(`(() => ({
    wordmark: document.querySelector('.hall-wordmark b')?.textContent?.trim() || '',
    navigation: Array.from(document.querySelectorAll('.hall-nav button > span'))
      .map(element => element.textContent?.replace(/\\s+/g, ' ').trim()),
    rail: Boolean(document.querySelector('.hall-rail')),
    main: Boolean(document.querySelector('.hall-main')),
    footer: document.querySelector('.hall-rail-foot')?.innerText.replace(/\\s+/g, ' ').trim() || '',
    bodyOverflow: Math.max(0, document.body.scrollWidth - document.body.clientWidth),
  }))()`);
  const expectedNavigation = ['ОБЗОР', 'МАСТЕР', 'РЕШЕНИЯ', 'ИЗМЕНЕНИЯ', 'КВЕСТЫ', 'ГИЛЬДИЯ'];
  if (hall.wordmark !== 'ЧЕРТОГ' || !hall.rail || !hall.main || !hall.footer ||
      JSON.stringify(hall.navigation) !== JSON.stringify(expectedNavigation) || hall.bodyOverflow > 1) {
    throw new Error(`Point Agent Hub shell verification failed: ${JSON.stringify(hall)}`);
  }

  if (requestedView === 'composer') {
    // Нажатие на рейку повторяем внутри ожидания: одиночный клик мог уйти до
    // того, как оболочка отрисовала рейку, и проверка ждала поверхность,
    // которую никто не открывал. Переключение вкладки идемпотентно.
    await waitFor('quest composer', async () => {
      if (await evaluate(`Boolean(document.querySelector('#agent-form #task'))`)) return true;
      await evaluate(`(() => { document.querySelector('.hall-nav [data-tab=\\"quests\\"]')?.click(); return true; })()`);
      return false;
    });
    const prepared = await evaluate(`(() => {
      const task = document.querySelector('#task');
      const preview = document.querySelector('[data-action="preview-run"]');
      const send = document.querySelector('.composer-actions .send');
      const form = document.querySelector('#agent-form');
      if (!task || !preview || !send || !form) return null;
      task.value = 'Проверь структуру проекта без изменений.';
      task.dispatchEvent(new Event('input', { bubbles: true }));
      const previewRect = preview.getBoundingClientRect();
      const sendRect = send.getBoundingClientRect();
      preview.click();
      return {
        labels: [preview.textContent, send.textContent].map(text => text?.replace(/\\s+/g, ' ').trim()),
        formWidth: Math.round(form.getBoundingClientRect().width),
        previewWidth: Math.round(previewRect.width),
        sendWidth: Math.round(sendRect.width),
        profile: {
          value: document.querySelector('#profile')?.value || '',
          options: Array.from(document.querySelectorAll('#profile option')).map(option => ({ id: option.value, label: option.textContent?.trim() || '', selected: option.selected })),
        },
      };
    })()`);
    const verified = await waitFor('successful local launch preview', () => evaluate(`(() => {
      const card = document.querySelector('.agent-preflight');
      if (!card || card.classList.contains('loading-inline') || card.classList.contains('idle')) return null;
      const stats = card.querySelector('.agent-preflight-stats');
      return {
        ready: card.classList.contains('ready'),
        heading: card.querySelector(':scope > header strong')?.textContent?.replace(/\\s+/g, ' ').trim() || '',
        text: card.innerText.replace(/\\s+/g, ' ').trim().slice(0, 1000),
        actions: Array.from(document.querySelectorAll('.composer-actions button'))
          .map(element => element.textContent?.replace(/\\s+/g, ' ').trim()),
        horizontalOverflow: Math.max(0, card.scrollWidth - card.clientWidth),
        statsColumns: stats ? getComputedStyle(stats).gridTemplateColumns.split(' ').filter(Boolean).length : 0,
      };
    })()`), 30000).catch(async () => {
      // Голое «Timed out» не говорит, что застряло: карточка может остаться в
      // загрузке, в простое или не отрисоваться вовсе — три разные причины.
      const composer = await evaluate(`(() => {
        const card = document.querySelector('.agent-preflight');
        return {
          cardClass: card ? card.className : null,
          cardText: card ? card.innerText.trim().slice(0, 240) : null,
          taskValue: (document.querySelector('#task') ? document.querySelector('#task').value : '').slice(0, 60),
          hasPreviewButton: Boolean(document.querySelector('[data-action=\"preview-run\"]')),
          activeTab: document.querySelector('.hall-nav button.is-active, .hall-nav button.active')?.textContent?.trim() || '',
          railTabs: Array.from(document.querySelectorAll('.hall-nav button')).map(b => (b.className.includes('active') ? '*' : '') + (b.textContent || '').trim()).slice(0, 8),
          headline: document.querySelector('h1, .hall-title h1')?.textContent?.trim().slice(0, 60) || '',
          mainClass: document.querySelector('.hall-main')?.className || null,
          hallBodyChildren: Array.from(document.querySelector('.hall-body')?.children || []).map(node => node.tagName.toLowerCase() + '.' + node.className),
          forms: Array.from(document.querySelectorAll('form')).map(node => ({ id: node.id, className: node.className, parent: (node.parentElement?.tagName.toLowerCase() || '') + '.' + (node.parentElement?.className || '') })),
          composers: document.querySelectorAll('footer.composer, .composer-actions').length,
          profile: {
            value: document.querySelector('#profile')?.value || '',
            options: Array.from(document.querySelectorAll('#profile option')).map(option => ({ id: option.value, label: option.textContent?.trim() || '', selected: option.selected })),
          },
          mainHtmlStart: document.querySelector('.hall-main')?.innerHTML?.replace(/\\s+/g, ' ').trim().slice(0, 600) || null,
          mainHtmlEnd: document.querySelector('.hall-main')?.innerHTML?.replace(/\\s+/g, ' ').trim().slice(-1200) || null,
        };
      })()`);
      throw new Error(`Point quest composer preview never resolved: ${JSON.stringify({ composer, runtimeErrors: hub.link.runtimeErrors })}`);
    });
    if (!prepared || prepared.labels[0] !== '✓ Разведка' || prepared.labels[1] !== 'Принять квест→' ||
        prepared.formWidth < 300 || prepared.previewWidth < 80 || prepared.sendWidth < 80 ||
        !verified.ready || verified.heading !== '✓ Запуск проверен' || verified.horizontalOverflow > 1 ||
        verified.statsColumns !== 2) {
      throw new Error(`Point quest composer verification failed: ${JSON.stringify({ prepared, verified })}`);
    }
    process.stdout.write(JSON.stringify({ hall, opened: { view: requestedView, prepared, verified } }));
  } else {
    // Нажатие на рейку повторяем внутри ожидания: одиночный клик мог уйти до
    // того, как оболочка отрисовала рейку, и проверка ждала поверхность,
    // которую никто не открывал. Переключение вкладки идемпотентно.
    await waitFor('agent guild surface', async () => {
      if (await evaluate(`Boolean(document.querySelector('.guild-roster'))`)) return true;
      await evaluate(`(() => { document.querySelector('.hall-nav [data-tab=\\"agents\\"]')?.click(); return true; })()`);
      return false;
    });
    const initialRosterCount = await evaluate(`document.querySelectorAll('.guild-roster .roster-card').length`);
    let createdAgent = null;
    if (initialRosterCount === 0) {
      const templateSelected = await evaluate(`(() => {
        const button = document.querySelector('.guild-roster [data-action="use-template"]');
        const name = button?.dataset.template || '';
        button?.click();
        return name;
      })()`);
      if (!templateSelected) throw new Error('The clean Agent Hub did not offer an agent class template');
      await waitFor('interactive agent constructor', () => evaluate(`Boolean(document.querySelector('#constructor-form'))`));
      const preparedHire = await evaluate(`(() => {
        const form = document.querySelector('#constructor-form');
        const name = document.querySelector('#constructor-name');
        const model = document.querySelector('#constructor-model');
        if (!form || !name || !model) return null;
        name.value = 'Point E2E Agent';
        name.dispatchEvent(new Event('input', { bubbles: true }));
        if (!model.value.trim()) {
          model.value = 'auto';
          model.dispatchEvent(new Event('input', { bubbles: true }));
        }
        const review = document.querySelector('[data-action="constructor-step"][data-step="review"]');
        review?.click();
        return { template: ${JSON.stringify(templateSelected)}, model: model.value.trim(), name: name.value, reviewClicked: Boolean(review) };
      })()`);
      if (!preparedHire?.reviewClicked) throw new Error(`The agent constructor could not reach review: ${JSON.stringify(preparedHire)}`);
      await waitFor('agent constructor review', () => evaluate(`Boolean(document.querySelector('[data-step-panel="review"]:not(.is-hidden)') && document.querySelector('[data-action="save-constructor"]'))`));
      const submitted = await evaluate(`(() => {
        const save = document.querySelector('[data-action="save-constructor"]');
        if (!save) return null;
        const result = { ...${JSON.stringify(preparedHire)}, label: save.textContent.replace(/\\s+/g, ' ').trim() };
        save.click();
        return result;
      })()`);
      if (!submitted) throw new Error('The agent constructor could not be submitted');
      if (submitted.label !== 'Сохранить агента') throw new Error(`A template hire looked like an edit: ${JSON.stringify(submitted)}`);
      createdAgent = await waitFor('new agent in guild roster', () => evaluate(`(() => {
        const card = document.querySelector('.guild-roster .roster-card');
        if (!card) return null;
        return {
          ...${JSON.stringify(submitted)},
          profileId: card.dataset.id || '',
          name: card.querySelector('.roster-card-head strong')?.textContent?.trim() || '',
        };
      })()`), 30000);
    }
    const guild = await waitFor('agent guild roster', () => evaluate(`(() => {
      const surface = document.querySelector('.guild-roster');
      if (!surface) return null;
      const cards = Array.from(surface.querySelectorAll('.roster-card'));
      const detail = surface.querySelector('.roster-detail');
      return {
        heading: surface.querySelector('.guild-heading h1')?.textContent?.replace(/\\s+/g, ' ').trim() || '',
        rosterCount: cards.length,
        names: cards.map(card => card.querySelector('.roster-card-head strong')?.textContent?.trim()).filter(Boolean),
        selectedName: detail?.querySelector(':scope > header h2')?.textContent?.trim() || '',
        selectedClass: detail?.querySelector(':scope > header p')?.textContent?.trim() || '',
        selectedLevel: detail?.querySelector(':scope > header em')?.textContent?.trim() || '',
        hasRoster: Boolean(surface.querySelector('.roster-list')),
        hasDetail: Boolean(detail),
        hasProgression: Boolean(surface.querySelector('.roster-progression')),
        canRecruit: Boolean(surface.querySelector('[data-action="new-profile"]')),
        canConstruct: Boolean(surface.querySelector('[data-action="open-agent-constructor"]')),
        canStartQuest: Boolean(surface.querySelector('[data-action="start-roster-quest"]')),
        horizontalOverflow: Math.max(0, surface.scrollWidth - surface.clientWidth),
      };
    })()`));
    if (guild.heading !== 'Команда текущего проекта' || guild.rosterCount < 1 || !guild.selectedName ||
        !guild.selectedClass || !guild.selectedLevel.startsWith('УР ') || !guild.hasRoster ||
        !guild.hasDetail || !guild.hasProgression || !guild.canRecruit || !guild.canConstruct ||
        !guild.canStartQuest || guild.horizontalOverflow > 1) {
      throw new Error(`Point agent guild verification failed: ${JSON.stringify(guild)}`);
    }
    process.stdout.write(JSON.stringify({ hall, opened: { view: requestedView, createdAgent, ...guild } }));
  }
} finally {
  socket.close();
}
