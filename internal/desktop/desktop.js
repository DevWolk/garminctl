import { Call, Clipboard, Events } from '/wails/runtime.js';

window.runtime ??= {};
window.runtime.ClipboardSetText = Clipboard.SetText;

(() => {
  const invoke = (name, ...args) => Call.ByName('github.com/DevWolk/garminctl/internal/desktop.App.' + name, ...args);
  const native = {
    Ready: () => invoke('Ready'),
    FinishClose: () => invoke('FinishClose'),
    OpenExternal: (raw) => invoke('OpenExternal', raw),
    Download: (relative, name) => invoke('Download', relative, name),
  };
  const applicationCall = (name, ...args) => Call.ByName('github.com/DevWolk/garminctl/internal/containers/application/ui/native.Service.' + name, ...args);
  window.applicationPage.attach({
    info: () => applicationCall('Info'),
    history: (includePrerelease) => applicationCall('History', includePrerelease),
    check: (includePrerelease) => applicationCall('Check', includePrerelease),
  });
  let confirming = false;
  async function action(name) {
    if (name === 'back') { history.back(); return; }
    if (name === 'application') { navigate('application', ''); return; }
    if (name === 'mcp') { navigate('ai', ''); return; }
    if (confirming) return;
    confirming = true;
    try {
      if (state.pending.size || state.appPending.size || state.cloudPending.size) {
        if (!await confirmDialog({ title: 'Discard unsaved changes?', body: '<p>Your changes have not been applied yet.</p>', ok: 'Discard' })) return;
      }
      if (name === 'close') await native.FinishClose();
      if (name === 'reload') { state.pending.clear(); state.appPending.clear(); state.cloudPending.clear(); location.reload(); }
    } finally { confirming = false; }
  }
  Events.On('desktop:action', event => action(event.data));
  document.addEventListener('keydown', (event) => {
    if (event.key === 'F5' || ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'r')) {
      event.preventDefault(); action('reload');
    }
  });
  function openExternal(url) { native.OpenExternal(url).catch(error => toast(String(error), 'bad')); }
  window.open = (raw) => {
    const url = new URL(raw, location.href);
    if (url.origin !== location.origin) openExternal(url.href);
    else { history.pushState(null, '', url.pathname + url.search); applyRoute(); }
    return null;
  };
  document.addEventListener('click', (event) => {
    const link = event.target.closest('a[href]');
    if (!link) return;
    const url = new URL(link.getAttribute('href'), location.href);
    if (url.protocol === 'http:' || url.protocol === 'https:') {
      if (url.origin !== location.origin) { event.preventDefault(); openExternal(url.href); return; }
    }
    if (url.pathname === '/api/file' || url.pathname === '/api/decode') {
      event.preventDefault();
      const name = link.download || (url.searchParams.get('path') || 'download').split('/').pop();
      native.Download(url.pathname + url.search, name).catch(error => toast(String(error), 'bad'));
    }
  }, true);
  native.Ready();
})();
