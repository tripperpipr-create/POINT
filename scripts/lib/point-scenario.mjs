// Приведение живого окна Point в состояние сценария: доверие к папке, открытый
// файл, нижняя панель, палитра команд.
//
// Код жил внутри audit-point-workbench.mjs, и это была ловушка метода: стенд
// запускает `-Probe` **вместо** аудита, поэтому любой зонд мерил окно в покое,
// какой бы `-Scenario` ему ни передали. Пять циклов подряд замеры «на сценарии
// file» относились к пустому окну. Теперь подготовка общая, и зонд обязан её
// вызвать сам.
// Список сценариев объявлен, и незнакомое имя — ошибка, а не тихий `idle`.
// docs/UI-UX-LOOP.md звал `-Scenario welcome`, которого здесь никогда не было:
// стенд принимал имя, окно оставалось в покое, и замер «на стартовом экране»
// относился к пустому редактору. Та же ловушка, что и с `-Probe` вместо аудита,
// только тише — падения нет, есть бодрый ответ не про то.
export const SCENARIOS = ['idle', 'file', 'nested', 'panel', 'palette'];

export async function applyScenario({ command, evaluate, scenario }) {
  if (!SCENARIOS.includes(scenario)) {
    throw new Error(`Сценарий «${scenario}» не объявлен. Известные: ${SCENARIOS.join(', ')}.`);
  }
  const wait = ms => new Promise(resolve => setTimeout(resolve, ms));
  // Форма нажатия перенесена из аудита без изменений: `rawKeyDown` + `keyUp`
  // с обоими кодами. Ctrl+J и Ctrl+Shift+P на ней работают, проверено циклами.
  const pressKey = async (key, code, keyCode, modifiers = 0) => {
    await command('Input.dispatchKeyEvent', { type: 'rawKeyDown', key, code, windowsVirtualKeyCode: keyCode, nativeVirtualKeyCode: keyCode, modifiers });
    await command('Input.dispatchKeyEvent', { type: 'keyUp', key, code, windowsVirtualKeyCode: keyCode, nativeVirtualKeyCode: keyCode, modifiers });
  };

  // Диалог доверия перекрывает всё окно и делает любой замер бессмысленным.
  await evaluate(`(() => {
    const dialog = Array.from(document.querySelectorAll('.monaco-dialog-box'))
      .find(item => (item.innerText || '').includes('Вы доверяете этому проекту?'));
    const button = Array.from(dialog?.querySelectorAll('.monaco-button') || [])
      .find(item => (item.textContent || '').includes('Доверять проекту'));
    if (button) { button.click(); return true; }
    return false;
  })()`);
  await wait(600);

  // Вложенный файл. Крошка на файле из корня показывает ровно то же, что
  // вкладка, и повтор в ней неотличим от совпадения. Чтобы судить о верхней
  // части редактора, нужен файл в папке: тогда видно, что крошка несёт путь.
  if (scenario === 'nested') {
    const folder = await evaluate(`(() => {
      const row = Array.from(document.querySelectorAll('.explorer-viewlet .monaco-list-row'))
        .find(item => item.querySelector('.codicon-tree-item-expanded, .monaco-tl-twistie.collapsible'));
      if (!row) return null;
      const box = row.getBoundingClientRect();
      return { x: Math.round(box.left + 40), y: Math.round(box.top + box.height / 2) };
    })()`);
    if (folder) {
      await command('Input.dispatchMouseEvent', { type: 'mousePressed', x: folder.x, y: folder.y, button: 'left', clickCount: 1 });
      await command('Input.dispatchMouseEvent', { type: 'mouseReleased', x: folder.x, y: folder.y, button: 'left', clickCount: 1 });
      await wait(900);
    }
    const nested = await evaluate(`(() => {
      const rows = Array.from(document.querySelectorAll('.explorer-viewlet .monaco-list-row'));
      const row = rows.find(item => {
        const label = item.querySelector('.label-name');
        const depth = Number(item.getAttribute('aria-level') || 1);
        return depth > 1 && label && /\.(go|ts|py|rs|java|md)$/.test((label.textContent || '').trim());
      });
      if (!row) return null;
      const box = row.getBoundingClientRect();
      return { x: Math.round(box.left + Math.min(box.width - 8, Math.max(40, box.width * 0.5))), y: Math.round(box.top + box.height / 2) };
    })()`);
    if (nested) {
      for (const clickCount of [1, 2]) {
        await command('Input.dispatchMouseEvent', { type: 'mousePressed', x: nested.x, y: nested.y, button: 'left', clickCount });
        await command('Input.dispatchMouseEvent', { type: 'mouseReleased', x: nested.x, y: nested.y, button: 'left', clickCount });
        if (clickCount === 1) await wait(120);
      }
      await wait(1800);
    }
  } else if (scenario === 'file') {
    // Двойной клик по строке проводника: в Point, как в любой IDE, одиночный
    // выделяет, а открывает вкладку двойной.
    const point = await evaluate(`(() => {
      const label = Array.from(document.querySelectorAll('.explorer-viewlet .label-name'))
        .find(element => /\.(go|md|mod)$/.test((element.textContent || '').trim()));
      const row = label?.closest('.monaco-list-row');
      if (!row) return null;
      const box = row.getBoundingClientRect();
      return { x: Math.round(box.left + Math.min(box.width - 8, Math.max(24, box.width * 0.45))), y: Math.round(box.top + box.height / 2) };
    })()`);
    if (point) {
      for (const clickCount of [1, 2]) {
        await command('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount });
        await command('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount });
        if (clickCount === 1) await wait(120);
      }
      await wait(1600);
    }
  } else if (scenario === 'panel') {
    await pressKey('J', 'KeyJ', 74, 2); // Ctrl+J — нижняя панель
    await wait(1800);
  } else if (scenario === 'palette') {
    await pressKey('P', 'KeyP', 80, 10); // Ctrl+Shift+P — палитра команд
    await wait(1200);
  }
}
