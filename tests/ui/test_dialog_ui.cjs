'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

const ui = name => readFileSync(path.join(__dirname, '../../internal/ship/web/ui', name), 'utf8');

class Node {
  constructor() { this.listeners = {}; this.style = {}; this.children = []; this.textContent = ''; this.hidden = false; }
  addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); }
  dispatch(name, event = {}) { for (const fn of this.listeners[name] || []) fn(event); }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; }
  select() { this.selected = true; }
}

// A <dialog> whose close event is queued like a browser's, so a test can
// reopen the dialog before an earlier close event is delivered.
class Dialog extends Node {
  constructor(queue) { super(); this.open = false; this.returnValue = ''; this.queue = queue; }
  showModal() { if (this.open) throw new Error('InvalidStateError: already open'); this.open = true; }
  close(value) {
    if (!this.open) return;
    if (value !== undefined) this.returnValue = value;
    this.open = false;
    this.queue.push(() => this.dispatch('close'));
  }
}

function dialogFixture() {
  const queue = [], nodes = {};
  const dlg = new Dialog(queue);
  for (const id of ['dlg-title', 'dlg-body', 'dlg-ok']) nodes[id] = new Node();
  nodes.dlg = dlg;
  const window = {};
  vm.runInNewContext(ui('dialog.js'), {window, document: {getElementById: id => nodes[id], createElement: () => new Node()}});
  const flush = async () => { while (queue.length) queue.shift()(); await new Promise(setImmediate); };
  return {dlg, nodes, flush, confirm: window.confirmDialog, prompt: window.promptDialog};
}

const settled = async promise => Promise.race([promise, new Promise(done => setImmediate(() => done('pending')))]);

test('a second confirmation cancels the open one, and one answer reaches only its own request', async () => {
  const d = dialogFixture();
  const save = d.confirm({title: 'Save watch settings?', body: '<p>1 change</p>', ok: 'Save changes'});
  const discard = d.confirm({title: 'Discard unsaved changes?', body: '<p>Not applied</p>', ok: 'Discard'});
  assert.equal(await settled(save), false);
  assert.equal(d.nodes['dlg-title'].textContent, 'Discard unsaved changes?');
  assert.equal(d.dlg.open, true);
  d.dlg.close('ok'); await d.flush();
  assert.equal(await discard, true);
});

test('a close queued before the dialog reopened does not answer the new request', async () => {
  const d = dialogFixture();
  const first = d.confirm({title: 'Remove app?', body: '', ok: 'Remove'});
  d.dlg.close('ok');
  const second = d.confirm({title: 'Discard unsaved changes?', body: '', ok: 'Discard'});
  await d.flush();
  assert.equal(await settled(first), false);
  assert.equal(await settled(second), 'pending');
  d.dlg.close(); await d.flush();
  assert.equal(await second, false);
});

test('Escape after an accepted dialog does not reuse its answer', async () => {
  const d = dialogFixture();
  const accepted = d.confirm({title: 'Log weight?', body: '', ok: 'Log weight'});
  d.dlg.close('ok'); await d.flush();
  assert.equal(await accepted, true);
  const name = d.prompt('New playlist', 'Playlist name', 'My playlist', 'Create playlist');
  d.dlg.close(); await d.flush();
  assert.equal(await name, null);
  const confirmEscaped = d.confirm({title: 'Delete?', body: '', ok: 'Delete'});
  d.dlg.close(); await d.flush();
  assert.equal(await confirmEscaped, false);
});

test('Enter in the prompt accepts the typed name, but not while an IME is composing', async () => {
  const d = dialogFixture();
  const name = d.prompt('New playlist', 'Playlist name', 'My playlist', 'Create playlist');
  assert.equal(d.nodes['dlg-ok'].textContent, 'Create playlist');
  const [field] = d.nodes['dlg-body'].children, input = field.children[1];
  assert.equal(input.selected, true);
  input.value = '  Runs  ';
  let prevented = 0;
  input.dispatch('keydown', {key: 'Enter', isComposing: true, keyCode: 229, preventDefault: () => prevented++});
  await d.flush();
  assert.equal(d.dlg.open, true);
  input.dispatch('keydown', {key: 'Enter', isComposing: false, keyCode: 13, preventDefault: () => prevented++});
  await d.flush();
  assert.equal(prevented, 2);
  assert.equal(await name, 'Runs');
});

// Pulls one top-level function out of app.js, which needs a full page to run as a whole.
function extract(source, name) {
  const start = source.search(new RegExp(`^(async )?function ${name}\\(`, 'm'));
  assert.notEqual(start, -1, `${name} not found in app.js`);
  let i = source.indexOf('{', source.indexOf(')', start)), depth = 0;
  const stack = [];
  for (; i < source.length; i++) {
    const c = source[i], top = stack.at(-1);
    if (top === "'" || top === '"') { if (c === '\\') i++; else if (c === top) stack.pop(); continue; }
    if (top === '`') {
      if (c === '\\') i++; else if (c === '`') stack.pop(); else if (c === '$' && source[i + 1] === '{') { stack.push('{'); i++; }
      continue;
    }
    if (c === "'" || c === '"' || c === '`') { stack.push(c); continue; }
    if (c === '/' && source[i + 1] === '/') { i = source.indexOf('\n', i); continue; }
    if (c === '{') { if (top === '{') stack.push('{'); else depth++; continue; }
    if (c === '}') { if (top === '{') { stack.pop(); continue; } if (--depth === 0) return source.slice(start, i + 1); }
  }
  throw new Error(`${name} is not closed`);
}

function appFixture({refresh = async () => ({}), confirm = true} = {}) {
  const source = ui('app.js');
  const nodes = new Map();
  const $ = sel => { if (!nodes.has(sel)) nodes.set(sel, Object.assign(new Node(), {innerHTML: 'stale', hidden: false})); return nodes.get(sel); };
  const calls = [], toasts = [], prompts = [], loaded = [];
  const app = {name: 'Easy+', fileName: 'EASY'};
  const state = {connected: true, tab: 'apps', rows: [{}], apps: [app], app, pending: new Map([['s', {}]]), appPending: new Map([['k', {value: 1, label: 'Key', from: '0', to: '1'}]]), preview: {timer: null}};
  const context = {
    $, state, toast: (msg, kind) => toasts.push([msg, kind]), esc: String, clearInterval: () => {},
    confirmDialog: async value => { prompts.push(value); return typeof confirm === 'function' ? confirm(context) : confirm; },
    api: async (method, url) => { calls.push(`${method} ${url}`); if (url === '/api/refresh') return refresh(); return {}; },
    connect: () => calls.push('connect'), updateActionBar: () => {}, renderOverview: () => {}, loadNewFiles: () => {},
    loadTab: tab => loaded.push(tab), renderAppSettings: () => {}, renderPreview: () => {},
  };
  vm.runInNewContext(['var watchEpoch = 0;', ...['resetWatchData', 'refreshFromWatch', 'applyAppChanges'].map(name => extract(source, name)),
    'this.refresh = refreshFromWatch; this.reset = resetWatchData; this.applyApp = applyAppChanges;'].join('\n'), context);
  return {context, state, calls, toasts, prompts, loaded, $};
}

test('Reload asks before dropping unsaved edits and keeps them when declined', async () => {
  const f = appFixture({confirm: false});
  await f.context.refresh();
  assert.equal(f.prompts.length, 1);
  assert.deepEqual(f.calls, []);
  assert.equal(f.state.appPending.size, 1);
  assert.equal(f.state.app.name, 'Easy+');
});

test('a failed reload is reported and keeps the edits and the selected app', async () => {
  const f = appFixture({refresh: async () => { throw new Error('watch busy'); }});
  await f.context.refresh();
  assert.deepEqual(f.calls, ['POST /api/refresh']);
  assert.deepEqual(f.toasts, [['Could not reload from the watch: watch busy', 'bad']]);
  assert.equal(f.state.appPending.size, 1);
  assert.equal(f.state.app.name, 'Easy+');
});

test('a reload empties the app panel so the app list and settings are read again', async () => {
  const f = appFixture();
  await f.context.refresh();
  assert.equal(f.state.app, null);
  assert.equal(f.state.pending.size + f.state.appPending.size, 0);
  assert.doesNotMatch(f.$('#apps-list').innerHTML, /stale/);
  assert.equal(f.$('#app-filter').hidden, true);
  assert.deepEqual(f.loaded, ['apps']);
  assert.deepEqual(f.toasts.at(-1), ['Reloaded from the watch', undefined]);
});

test('a save confirmed after the watch data was reset writes nothing', async () => {
  const f = appFixture({confirm: context => { context.reset(); return true; }});
  await f.context.applyApp();
  assert.deepEqual(f.calls, []);
  assert.match(f.toasts.at(-1)[0], /Not saved/);
});
