'use strict';
// Only native composition supplies installation. Status reads never start work.
window.createApplicationInstall = ({downloaded, prerelease, busy, changed}) => {
  const get = name => document.getElementById('application-install-' + name);
  const start = get('start');
  const dialog = get('dialog');
  const cancel = get('cancel');
  const choices = get('package');
  let native = null;
  let state = null;
  let known = false;
  let confirming = false;
  let starting = false;
  let canceling = false;
  let unresolved = false;
  let localError = '';
  let packages = [];
  let observedDownload = '';
  let timer = null;
  let epoch = 0;
  let observation = 0;
  const active = () => starting || unresolved || !!state?.active || !!state?.stopRequested;
  const text = error => error?.message || String(error);
  const leaving = () => ['accepted', 'uncertain'].includes(state?.phase);
  const selection = () => packages.find(row => row.cacheId === choices.value);
  function renderPackages(rows) {
    const previous = choices.value;
    packages = rows;
    choices.replaceChildren();
    for (const row of rows) {
      const option = document.createElement('option');
      option.value = row.cacheId;
      option.textContent = row.tag + (row.prerelease ? ' (prerelease)' : '');
      choices.append(option);
    }
    choices.value = rows.some(row => row.cacheId === previous) ? previous : (rows[0]?.cacheId || '');
  }
  function clearPoll() { if (timer !== null) clearTimeout(timer); timer = null; }
  function update() {
    const receipt = downloaded();
    if (receipt && receipt.cacheId !== observedDownload && !active()) {
      observedDownload = receipt.cacheId;
      if (native?.savedPackages) {
        known = false;
        Promise.resolve().then(refresh);
      } else {
        if (!packages.some(row => row.cacheId === receipt.cacheId)) renderPackages([...packages, {tag:receipt.tag, cacheId:receipt.cacheId}]);
        choices.value = receipt.cacheId;
      }
    }
    start.hidden = !native?.startInstallation;
    start.disabled = !known || confirming || active() || leaving() || busy() || !selection();
    choices.disabled = confirming || active() || busy() || !packages.length;
    const closeOnly = state?.stopRequested && !state.active && !leaving();
    cancel.textContent = closeOnly ? 'Close application' : 'Cancel preparation';
    cancel.disabled = !closeOnly && (starting || canceling || !state?.canCancel);
    const messages = {
      verifying: 'Verifying the update and the current version’s recovery package.',
      'backing-up': 'Saving a protected copy of local application data.',
      handoff: 'Handing the update to the installer. The application will close.',
      canceling: 'Canceling preparation and cleaning up.',
      accepted: 'Closing the application to install the update. Do not retry.',
      uncertain: 'The installer may have accepted the update. Closing the application; do not retry.',
      canceled: 'Installation preparation canceled.',
    };
    const phase = starting ? 'verifying' : state?.phase;
    let message = messages[phase] || (phase === 'failed' ? 'Could not prepare installation: ' + state.error : '');
    if (closeOnly) message = 'Application shutdown was requested before handoff completed. Close and restart the app manually. ' + (state.error || '');
    if (localError) message = localError;
    get('status').textContent = message;
    get('detail').textContent = message;
    if ((active() || leaving()) && !dialog.open) dialog.showModal();
    if (!active() && !leaving() && dialog.open) dialog.close();
  }
  function render(value) {
    if (state && value.id < state.id) return;
    const refused = unresolved && value.id <= (state?.id || 0) ? localError : '';
    state = value;
    unresolved = false;
    localError = refused;
    known = true;
    update(); changed();
  }
  function poll() {
    if ((state?.active || unresolved) && timer === null) timer = setTimeout(() => { timer = null; return refresh(); }, 300);
  }
  async function refresh(statusOnly = false) {
    clearPoll();
    const version = epoch;
    const read = ++observation;
    if (!native?.installationStatus) { known = false; update(); return; }
    try {
      const result = await native.installationStatus();
      if (version !== epoch || read !== observation) return;
      render(result);
      if (!statusOnly && !active() && !leaving() && native.savedPackages) {
        const rows = await native.savedPackages(prerelease());
        if (version !== epoch || read !== observation) return;
        renderPackages(rows); update();
      }
      poll();
    } catch (error) {
      if (version !== epoch || read !== observation) return;
      known = false;
      localError = 'Could not read saved packages or installation status. ' + text(error);
      // An unresolved operation keeps its modal/guard; it is never offered replay.
      update(); changed(); poll();
    }
  }
  async function begin() {
    if (start.disabled || !native?.confirmInstallation) return;
    const request = selection();
    if (!request) return;
    confirming = true; update(); changed();
    try {
      if (!await native.confirmInstallation(request.tag)) return;
      epoch++; clearPoll(); starting = true; unresolved = true; localError = ''; update(); changed();
      render(await native.startInstallation({tag:request.tag, cacheId:request.cacheId, includePrerelease:prerelease()}, {confirmed:true, editsResolved:true}));
    } catch (error) {
      // The native start method returns before handoff. A lost start reply is
      // resolved only by Status, never by another Start.
      known = false;
      localError = 'Could not confirm preparation started: ' + text(error) + ' Reading status before another attempt.';
    } finally { confirming = false; starting = false; update(); changed(); poll(); }
  }
  async function stop() {
    if (state?.stopRequested && !state.active && !leaving()) { await native?.closeWindow?.(); return; }
    if (!state?.active || !state.canCancel || canceling || !native?.cancelInstallation) return;
    epoch++; clearPoll(); canceling = true; update();
    try { render(await native.cancelInstallation(state.id)); }
    catch (error) { localError = 'Could not cancel preparation: ' + text(error); }
    finally { canceling = false; update(); poll(); }
  }
  start.addEventListener('click', begin);
  choices.addEventListener('change', update);
  cancel.addEventListener('click', stop);
  dialog.addEventListener('cancel', event => { event.preventDefault(); return stop(); });
  update();
  return {active, leaving, refresh, update, attach(bridge) { native = bridge; update(); }};
};
