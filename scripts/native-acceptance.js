// Served only by the CI fixture proxy, never embedded in the application.
import { Call } from '/wails/runtime.js';

const prefix = 'github.com/DevWolk/garminctl/internal/desktop.App.';
const boot = crypto.randomUUID();
const downloads = new Map();
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));

async function until(read) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    const value = read();
    if (value) return value;
    await pause(50);
  }
  throw new Error('Native fixture page did not reach the required state');
}

function snapshot() {
  return {
    boot,
    dialog: document.querySelector('#dlg').open,
    title: document.querySelector('#dlg-title').textContent,
    pending: !document.querySelector('#actionbar').hidden,
    path: location.pathname,
  };
}

async function runtimeProbe() {
  const get = await fetch('/wails/runtime');
  const post = await fetch('/wails/runtime', { method: 'POST', body: '{}' });
  // Ready is idempotent and has no file, clipboard, browser or shutdown effect.
  const payload = JSON.stringify({ object: 0, method: 0, args: {
    'call-id': crypto.randomUUID(), methodName: prefix + 'Ready', args: [],
  } });
  const positive = await fetch('/wails/runtime', { method: 'POST', body: payload });
  const frame = document.createElement('iframe');
  frame.setAttribute('sandbox', 'allow-scripts');
  const token = crypto.randomUUID();
  const url = new URL('/wails/runtime', location.href).href;
  const foreign = new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error('Foreign-origin probe did not finish')), 10000);
    const receive = event => {
      if (event.source !== frame.contentWindow || event.data?.token !== token) return;
      clearTimeout(timeout);
      window.removeEventListener('message', receive);
      resolve(event.data.outcome);
    };
    window.addEventListener('message', receive);
  });
  frame.srcdoc = `<script>fetch(${JSON.stringify(url)}, {method:'POST',mode:'no-cors',body:${JSON.stringify(payload)}})
    .then(r => parent.postMessage({token:${JSON.stringify(token)},outcome:r.type}, '*'))
    .catch(() => parent.postMessage({token:${JSON.stringify(token)},outcome:'browser-blocked'}, '*'))</script>`;
  document.body.append(frame);
  try {
    return { get: get.status, post: post.status, positive: positive.status, foreign: await foreign };
  } finally { frame.remove(); }
}

async function execute(command) {
  switch (command.action) {
    case 'snapshot': return snapshot();
    case 'edit': {
      document.querySelector('[data-tab="settings"]').click();
      const control = await until(() => document.querySelector('#settings-groups [data-key]'));
      if (control.tagName === 'SELECT') {
        const alternative = [...control.options].find(option => option.value !== control.value);
        if (!alternative) throw new Error('Fixture has no alternative setting value');
        control.value = alternative.value;
      } else { control.value = String(Number(control.value) + 1); }
      control.dispatchEvent(new Event('change', { bubbles: true }));
      return snapshot();
    }
    case 'confirm': {
      if (!document.querySelector('#dlg').open) throw new Error('No edit confirmation is visible');
      document.querySelector(`#dlg button[value="${command.accept ? 'ok' : 'cancel'}"]`).click();
      return snapshot();
    }
    case 'download': {
      const id = command.id;
      downloads.set(id, { done: false });
      Call.ByName(prefix + 'Download', '/api/file?path=GARMIN%2FGarminDevice.xml', 'GarminDevice.xml')
        .then(() => downloads.set(id, { done: true }))
        .catch(error => downloads.set(id, { done: true, error: String(error) }));
      return { id };
    }
    case 'download-result': return downloads.get(command.download) || null;
    case 'runtime': return runtimeProbe();
    default: throw new Error('Unknown native fixture command');
  }
}

await until(() => window.runtime?.ClipboardSetText);
await fetch('/__native/ready', { method: 'POST', body: JSON.stringify({ boot }) });
for (;;) {
  try {
    const response = await fetch('/__native/next', { cache: 'no-store' });
    if (response.status === 204) { await pause(100); continue; }
    const command = await response.json();
    let result;
    try { result = { value: await execute(command) }; }
    catch (error) { result = { error: String(error) }; }
    await fetch('/__native/result', { method: 'POST', body: JSON.stringify({ id: command.id, ...result }) });
  } catch { await pause(100); }
}
