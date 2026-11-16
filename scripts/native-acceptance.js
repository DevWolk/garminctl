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

function applicationSnapshot() {
  const facts = Object.fromEntries([...document.querySelectorAll('#application-inventory dt')]
    .map(term => [term.textContent, term.nextElementSibling?.textContent]));
  return {
    facts,
    note: document.querySelector('#application-inventory-note').textContent,
    includePrerelease: document.querySelector('#application-prerelease').checked,
    downloadDisabled: document.querySelector('#application-download-start').disabled,
  };
}

async function applicationReady() {
  await until(() => !document.querySelector('#application-native').hidden &&
    document.querySelector('#application-native').getAttribute('aria-busy') === 'false');
  return applicationSnapshot();
}

function expectedEvent(target, name, accept) {
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      target.removeEventListener(name, receive);
      reject(new Error(`Required native ${name} event did not arrive`));
    }, 10000);
    const receive = event => {
      if (!accept(event)) return;
      clearTimeout(timeout);
      target.removeEventListener(name, receive);
      resolve(event);
    };
    target.addEventListener(name, receive);
  });
}

async function inheritedAttributeViolation(attack) {
  // An accessible srcdoc lets the parent observe the real inherited-policy
  // violation. A blocked opaque frame cannot run its own reporting script.
  const frame = document.createElement('iframe');
  const loaded = expectedEvent(frame, 'load', () => true);
  frame.srcdoc = '<!doctype html><html><body></body></html>';
  document.body.append(frame);
  try {
    await loaded;
    const child = frame.contentDocument;
    if (child?.URL !== 'about:srcdoc') throw new Error('CSP probe did not load srcdoc');
    const blocked = expectedEvent(child, 'securitypolicyviolation', event =>
      event.blockedURI === 'inline' && event.effectiveDirective.startsWith('script-src'));
    // strict-dynamic permits non-parser-inserted scripts from trusted code.
    // Event attributes stay untrusted even when that code creates the element.
    const button = child.createElement('button');
    button.setAttribute('onclick', attack);
    child.body.append(button);
    button.click();
    const violation = await blocked;
    return { documentURL: child.URL, directive: violation.effectiveDirective, blockedURI: violation.blockedURI };
  } finally { frame.remove(); }
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
  let executed = false;
  const receive = event => {
    if (event.source === frame.contentWindow && event.data?.token === token) executed = true;
  };
  window.addEventListener('message', receive);
  const loaded = expectedEvent(frame, 'load', () => true);
  const attack = `fetch(${JSON.stringify(url)}, {method:'POST',mode:'no-cors',body:${JSON.stringify(payload)}});`;
  frame.srcdoc = `<script>parent.postMessage({token:${JSON.stringify(token)}}, '*');${attack}</script>`;
  document.body.append(frame);
  try {
    await loaded;
    const csp = await inheritedAttributeViolation(attack);
    // Drain queued postMessage tasks after both positive load/violation events.
    await pause(0);
    return { get: get.status, post: post.status, positive: positive.status, foreign: { loaded: true, executed }, csp };
  } finally {
    window.removeEventListener('message', receive);
    frame.remove();
  }
}

async function execute(command) {
  switch (command.action) {
    case 'snapshot': return snapshot();
    case 'application-open': {
      document.querySelector('[data-tab="application"]').click();
      return applicationReady();
    }
    case 'application-channel': {
      const channel = document.querySelector('#application-prerelease');
      if (channel.disabled) throw new Error('Application channel is unexpectedly locked');
      if (channel.checked !== command.includePrerelease) channel.click();
      return applicationReady();
    }
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
