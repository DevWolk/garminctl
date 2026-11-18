'use strict';
// The native bridge accepts only channel selection, never versions or paths.
(() => {
  const get = (name) => document.getElementById('application-' + name);
  const page = get('native');
  const status = get('status');
  const channel = get('prerelease');
  const check = get('check');
  const choices = get('release');
  let native = null;
  let active = false;
  let busy = false;
  let history = null;
  let installations = null;
  const downloads = window.createApplicationDownload({
    selection: () => {
      const release = history?.history?.[Number(choices.value)];
      return release ? {tag: release.tag, includePrerelease: channel.checked} : null;
    },
    busy: () => busy || !!installations?.active() || !!installations?.leaving(),
    changed: updateControls,
  });
  installations = window.createApplicationInstall({
    downloaded: () => downloads.package(),
    prerelease: () => channel.checked,
    busy: () => busy || downloads.active(),
    changed: updateControls,
  });
  function updateControls() {
    const locked = busy || downloads.active() || !!installations?.active() || !!installations?.leaving();
    check.disabled = channel.disabled = locked;
    choices.disabled = !history?.history?.length || locked;
    downloads.update();
    installations?.update();
  }

  function text(name, value) { get(name).textContent = value; }
  function errorText(error) { return error?.message || String(error); }
  function setBusy(value) {
    busy = value;
    updateControls();
    get('progress').hidden = !value;
    page.setAttribute('aria-busy', String(value));
  }
  function fact(label, value) {
    const term = document.createElement('dt');
    const detail = document.createElement('dd');
    term.textContent = label;
    detail.textContent = value || 'Unknown';
    get('inventory').append(term, detail);
  }
  function version(value) {
    return (value?.version || 'Unknown') + (value?.error ? ' — ' + value.error : '');
  }
  function inventory(info) {
    get('inventory').replaceChildren();
    fact('Desktop', version(info.desktop));
    fact('CLI', version(info.cli));
    fact(info.backend?.owned ? 'Backend (owned)' : 'Backend (reused)', version(info.backend));
    fact('Platform', [info.os, info.arch].filter(Boolean).join(' / '));
    const kind = {setup: 'Windows Setup', deb: 'DEB', rpm: 'RPM', portable: 'Portable'}[info.installation?.kind] || 'Unknown';
    fact('Installation', kind);
    text('inventory-note', [info.versionAgreement === 'different' ? 'The desktop, CLI and backend versions differ.' : '', info.installation?.reason || ''].filter(Boolean).join(' '));
  }
  function notes() {
    const release = history?.history?.[Number(choices.value)];
    text('release-title', release ? (release.name || release.tag) : 'Release notes');
    const published = release?.publishedAt && new Date(release.publishedAt);
    text('release-date', published && published.getUTCFullYear() > 1 ? 'Published: ' + published.toLocaleString() : '');
    text('notes', release ? (release.notes || 'No release notes provided.') : 'No release selected.');
  }
  function renderHistory(result) {
    history = result;
    choices.replaceChildren();
    for (const [index, release] of (result?.history || []).entries()) {
      const option = document.createElement('option');
      option.value = String(index);
      option.textContent = release.tag + (release.prerelease ? ' (prerelease)' : '');
      choices.append(option);
    }
    updateControls();
    choices.value = '0';
    notes();
    if (!result) {
      status.textContent = 'No saved release history. Check for updates when online.';
      return;
    }
    const checked = new Date(result.checkedAt).toLocaleString();
    const available = result.available ? 'New release available: ' + result.available.tag + '.' : 'No newer release found in this channel.';
    status.textContent = [available, (result.fromCache ? 'Saved history. Last checked: ' : 'Checked: ') + checked + '.', result.refreshError ? 'Could not refresh: ' + result.refreshError : '', result.cacheError ? 'Could not save history: ' + result.cacheError : ''].filter(Boolean).join(' ');
  }
  async function readHistory(refresh) {
    if (!native || busy || downloads.active() || installations.active() || installations.leaving()) return;
    setBusy(true);
    try {
      renderHistory(await (refresh ? native.check(channel.checked) : native.history(channel.checked)));
    } catch (error) {
      // Preserve the last visible snapshot, while making a failed check explicit.
      // A failed channel switch must not retain releases from another channel.
      if (!refresh) renderHistory(null);
      status.textContent = 'Could not read releases: ' + errorText(error);
    } finally { setBusy(false); await installations.refresh(); }
  }
  async function readNative() {
    setBusy(true);
    const results = await Promise.allSettled([
      Promise.resolve().then(() => native.info()),
      Promise.resolve().then(() => native.history(channel.checked)),
      downloads.refresh(),
      installations.refresh(),
    ]);
    if (results[0].status === 'fulfilled') inventory(results[0].value);
    else text('inventory-note', 'Could not read installed versions: ' + errorText(results[0].reason));
    if (results[1].status === 'fulfilled') renderHistory(results[1].value);
    else status.textContent = 'Could not read saved releases: ' + errorText(results[1].reason);
    setBusy(false);
  }
  async function readBrowser() {
    try {
      const response = await fetch('/api/server', {signal: AbortSignal.timeout(10000)});
      if (!response.ok) throw new Error('Backend information is unavailable.');
      const info = await response.json();
      if (native) return; // A late native attachment owns the inventory now.
      get('inventory').replaceChildren();
      fact('Backend', info.version);
    } catch (error) {
      if (!native) text('inventory-note', 'Could not read backend version: ' + errorText(error));
    }
  }
  check.addEventListener('click', () => readHistory(true));
  channel.addEventListener('change', () => readHistory(false));
  choices.addEventListener('change', notes);
  window.applicationPage = {
    activate() {
      if (active) return native ? Promise.all([downloads.refresh(), installations.refresh()]) : undefined;
      active = true;
      return native ? readNative() : readBrowser();
    },
    attach(bridge) {
      if (native) return;
      native = bridge;
      downloads.attach(bridge);
      installations.attach(bridge);
      page.hidden = false;
      get('browser').hidden = true;
      if (active) return readNative();
      return installations.refresh(true);
    },
    installing() { return installations.active() || installations.leaving(); },
  };
})();
