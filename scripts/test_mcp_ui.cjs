'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

const window = {};
vm.runInContext(readFileSync(path.join(__dirname, '../internal/web/ui/mcp-setup.js'), 'utf8'), vm.createContext({window}));
const endpoint = 'http://127.0.0.1:8787/mcp';
const setup = client => window.mcpSetup(client, endpoint);

test('each terminal client gets its own one-line command for the shown endpoint', () => {
  assert.equal(setup('claude').text, `claude mcp add --transport http --scope user garminctl ${endpoint}`);
  assert.equal(setup('codex').text, `codex mcp add garminctl --url ${endpoint}`);
  assert.equal(setup('gemini').text, `gemini mcp add --transport http --scope user garminctl ${endpoint}`);

  for (const client of ['claude', 'codex', 'gemini']) {
    assert.match(setup(client).label, /run once in your terminal$/);
    assert.equal(setup(client).action, 'Copy command');
  }
});

test('Antigravity gets a configuration block with its own URL key and file', () => {
  const {label, action, text} = setup('antigravity');
  assert.deepEqual(JSON.parse(text), {mcpServers: {garminctl: {serverUrl: endpoint}}});
  assert.match(label, /~\/\.gemini\/config\/mcp_config\.json$/);
  assert.equal(action, 'Copy configuration');
});

test('the selector lists every client once and an unknown choice falls back to the first', () => {
  assert.equal(window.mcpClients.map(client => client.id).join(' '), 'claude codex gemini antigravity');
  assert.deepEqual(setup('unknown'), setup('claude'));
});
