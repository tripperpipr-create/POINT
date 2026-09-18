import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const ts = require('../frontend/node_modules/typescript');
const source = fs.readFileSync(new URL('../distribution/resources/point-tool-windows.ts.txt', import.meta.url), 'utf8');
const commands = new Map();
const bindings = [];
const menus = [];
const Parts = { SIDEBAR_PART: 'left', PANEL_PART: 'bottom', AUXILIARYBAR_PART: 'right', EDITOR_PART: 'editor' };
const ids = ['ICommandService', 'IContextViewService', 'IKeybindingService', 'IWorkbenchLayoutService'];
let popup;
let visible = { left: true, bottom: false, right: true };
let focused;
const executed = [];
const anchor = { getClientRects: () => [1], classList: { contains: () => false } };
const layout = {
  isVisible: part => visible[part],
  setPartHidden: (hidden, part) => { visible[part] = !hidden; },
  focusPart: part => { focused = part; },
};
const services = {
  IWorkbenchLayoutService: layout,
  ICommandService: { executeCommand: id => executed.push(id) },
  IContextViewService: {},
  IKeybindingService: { lookupKeybinding: id => id === 'workbench.view.explorer' ? { getLabel: () => 'Ctrl+Alt+1' } : undefined },
};
const mocks = {
  ...Object.fromEntries(ids.map(id => [id, id])),
  getActiveWindow: () => ({ document: { querySelector: () => anchor } }),
  AnchorAlignment: { RIGHT: 1 },
  KeyCode: { F12: 12 }, KeyMod: { CtrlCmd: 1, Shift: 2 },
  CommandsRegistry: { registerCommand: (id, handler) => commands.set(id, handler) },
  KeybindingsRegistry: { registerKeybindingRule: rule => bindings.push(rule) },
  KeybindingWeight: { ExternalExtension: 400 },
  MenuId: { CommandPalette: 'palette', MenubarViewMenu: 'view' },
  MenuRegistry: { appendMenuItem: (...args) => menus.push(args) },
  Parts, showPointPopup: (_service, _anchor, options) => { popup = options; },
  default: { applicationName: 'point' },
};
vm.runInNewContext(ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText,
  { require: () => mocks, exports: {}, WeakMap, Map });
const accessor = { get: id => services[id] };
const toggle = commands.get('point.toggleToolWindows');
toggle(accessor);
assert.deepEqual(visible, { left: false, bottom: false, right: false });
assert.equal(focused, 'editor');
toggle(accessor);
assert.deepEqual(visible, { left: true, bottom: false, right: true }, 'restore exactly the original panel visibility');
visible = { left: false, bottom: true, right: false };
toggle(accessor); toggle(accessor);
assert.deepEqual(visible, { left: false, bottom: true, right: false }, 'a new cycle takes a fresh snapshot');
visible = { left: false, bottom: false, right: false };
toggle(accessor);
assert.equal(visible.left, true, 'an empty layout can recover its project panel');
commands.get('point.toolWindows')(accessor);
let rows = popup.sections('').flatMap(section => section.rows);
assert.equal(rows.find(row => row.label === 'Левая панель').current, true);
assert.equal(rows.find(row => row.label === 'Правая панель').current, false);
assert.ok(!rows.some(row => row.label === 'Переименовать символ'));
rows.find(row => row.label === 'Разделить справа').run();
assert.equal(executed.pop(), 'workbench.action.splitEditorRight');
commands.get('point.keymap')(accessor);
rows = popup.sections('ctrl+alt+1').flatMap(section => section.rows);
assert.equal(rows.length, 1, 'search uses the actual user keybinding');
assert.equal(rows[0].label, 'Проект');
assert.equal(popup.sections('no-such-command').length, 0);
assert.ok(bindings.some(rule => rule.id === 'point.toggleToolWindows' && rule.weight > 400));
assert.equal(menus.length, 6);
console.log('Tool windows: visibility restore, empty layout recovery, commands, keymap search and registration passed.');
