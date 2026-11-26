'use strict';
// Preparation belongs to the native window; a page reload only resumes polling.
window.createApplicationDownload = ({selection, busy, changed}) => {
  const get = name => document.getElementById('application-download-' + name);
  const button = get('start');
  const cancel = get('cancel');
  const progress = get('progress');
  const detail = get('detail');
  const status = get('status');
  let native = null;
  let state = null;
  let known = false;
  let starting = false;
  let preparingTag = null;
  let canceling = false;
  let epoch = 0;
  let timer = null;
  let observation = 0;
  let localError = '';
  const active = () => starting || !!state?.active;
  const text = error => error?.message || String(error);
  function clearPoll() { if (timer !== null) clearTimeout(timer); timer = null; }
  function update() {
    button.disabled = !known || !selection() || busy() || active() || !!state?.disabledReason;
    cancel.hidden = !active();
    cancel.disabled = starting || canceling || state?.phase === 'canceling';
    const phase = starting ? 'checking' : canceling ? 'canceling' : state?.phase;
    const tag = starting ? preparingTag : state?.tag;
    get('release').textContent = tag ? 'Package: ' + tag : '';
    const messages = {
      checking: 'Checking this installation and release.',
      resolving: 'Finding the package for this installation.',
      downloading: 'Downloading the selected package.',
      verifying: 'Verifying and saving the package.',
      recovery: 'Saving the package of the running version as the way back.',
      canceling: 'Canceling download and cleaning up.',
    };
    const total = Number.isSafeInteger(state?.total) && state.total > 0 ? state.total : 0;
    const written = Number.isSafeInteger(state?.written) ? Math.max(0, Math.min(state.written, total)) : 0;
    progress.hidden = !active() && phase !== 'ready';
    if (total && (phase === 'downloading' || phase === 'recovery' || phase === 'ready')) {
      progress.max = total;
      progress.value = written;
    } else { progress.removeAttribute('value'); }
    detail.textContent = messages[phase] || '';
    const bytes = `${written.toLocaleString()} of ${total.toLocaleString()} bytes`;
    if (phase === 'downloading' && total) detail.textContent = bytes;
    if (phase === 'recovery' && total) detail.textContent = 'Package of the running version: ' + bytes;
    // Byte updates use the semantic progress element, not a noisy live region.
    let announcement = state?.disabledReason || '';
    if (phase === 'ready') announcement = 'Verified package saved for ' + state.tag + '.';
    if (phase === 'canceled') announcement = 'Download canceled.';
    if (phase === 'failed') announcement = 'Could not prepare the package: ' + state.error;
    if (localError) announcement = localError;
    if (status.textContent !== announcement) status.textContent = announcement;
  }
  function render(value) {
    if (state && value.id < state.id) return;
    state = value;
    localError = '';
    known = true;
    update();
    changed();
  }
  function poll() {
    if (state?.active && timer === null) timer = setTimeout(() => { timer = null; return refresh(); }, 500);
  }
  async function refresh() {
    clearPoll();
    const requestEpoch = epoch;
    const requestObservation = ++observation;
    if (!native?.downloadStatus) {
      render({phase: 'idle', disabledReason: 'Package downloads are unavailable in this build.'});
      return;
    }
    try {
      const value = await native.downloadStatus();
      if (requestEpoch !== epoch || requestObservation !== observation) return;
      render(value);
      poll();
    } catch (error) {
      if (requestEpoch !== epoch || requestObservation !== observation) return;
      known = false;
      update();
      localError = 'Could not read download status. Reopen this tab to retry. ' + text(error);
      update();
      changed();
    }
  }
  async function start() {
    if (button.disabled || !native?.startDownload) return;
    const selected = selection();
    if (!selected) return;
    epoch++;
    clearPoll();
    starting = true;
    preparingTag = selected.tag;
    localError = '';
    update(); changed();
    try { render(await native.startDownload(selected.tag, selected.includePrerelease)); }
    catch (error) { localError = 'Could not start download: ' + text(error); }
    finally { starting = false; preparingTag = null; update(); changed(); poll(); }
  }
  async function stop() {
    if (!state?.active || canceling || !native?.cancelDownload) return;
    epoch++;
    clearPoll();
    canceling = true;
    localError = '';
    update(); changed();
    try { render(await native.cancelDownload(state.id)); }
    catch (error) { localError = 'Could not cancel download: ' + text(error); }
    finally { canceling = false; update(); changed(); poll(); }
  }
  button.addEventListener('click', start);
  cancel.addEventListener('click', stop);
  update();
  return {active, refresh, update, package() { return state?.phase === 'ready' && state.result ? {...state.result} : null; }, attach(bridge) { native = bridge; }};
};
