'use strict';
// Only native composition supplies installation. Status reads never start work.
window.createApplicationInstall = ({downloaded, prerelease, busy, changed}) => {
  const get = name => document.getElementById('application-install-' + name);
  const start = get('start');
  const dialog = get('dialog');
  const cancel = get('cancel');
  const choices = get('package');
  const rollbackChoices = get('rollback-package');
  const rollbackStart = get('rollback-start');
  const rollbackKeep = get('rollback-keep');
  const reviewConsent = get('review-consent');
  const reviewConfirm = get('review-confirm');
  let native = null;
  let state = null;
  let known = false;
  let confirming = false;
  let starting = false;
  let canceling = false;
  let unresolved = false;
  let localError = '';
  let packages = [];
  let rollbacks = [];
  let rollbackReadError = '';
  let reviewKey = '';
  let submitting = false;
  let confirmationPending = false;
  let observedDownload = '';
  let timer = null;
  let epoch = 0;
  let observation = 0;
  const active = () => starting || unresolved || !!state?.active || !!state?.stopRequested;
  const text = error => error?.message || String(error);
  const leaving = () => ['accepted', 'uncertain'].includes(state?.phase);
  const selection = () => packages.find(row => row.cacheId === choices.value);
  const rollbackSelection = () => rollbacks.find(row => row.cacheId + ':' + row.snapshotId === rollbackChoices.value);
  function renderRollbacks(rows) {
    const previous = rollbackChoices.value;
    rollbacks = rows;
    rollbackChoices.replaceChildren();
    for (const row of rows) {
      const option = document.createElement('option');
      option.value = row.cacheId + ':' + row.snapshotId;
      option.textContent = row.tag + (row.prerelease ? ' (prerelease)' : '') + ' / backup ' + row.snapshotId.slice(0, 12) + (row.createdAt ? ' / ' + new Date(row.createdAt).toLocaleString() : '');
      rollbackChoices.append(option);
    }
    rollbackChoices.value = rows.some(row => row.cacheId + ':' + row.snapshotId === previous) ? previous : (rows[0] ? rows[0].cacheId + ':' + rows[0].snapshotId : '');
  }
  function renderReview() {
    const view = state?.review;
    const key = view ? state.id + ':' + view.reviewId : '';
    if (key === reviewKey) return;
    reviewKey = key;
    reviewConsent.checked = false;
    get('review-files').replaceChildren();
    if (!view) return;
    get('review-version').textContent = 'Return from ' + view.from + ' to ' + view.to + '.';
    get('review-backups').textContent = 'Restore backup: ' + view.targetSnapshotId + '. Newer data saved in backup: ' + view.safetySnapshotId + '.';
    if (view.keepCurrentData) get('review-backups').textContent = 'Current data will stay in place. Safety backup: ' + view.safetySnapshotId + '. Earlier version backup: ' + view.targetSnapshotId + '.';
    get('review-consent-label').textContent = view.keepCurrentData
      ? 'I agree to install the older version while keeping current data. A protected safety backup is retained.'
      : 'I agree to replace or remove the listed data. Newer changes to these files will be kept only in the safety backup.';
    reviewConfirm.textContent = view.keepCurrentData ? 'Keep current data and install older version' : 'Restore backup and install older version';
    get('review-count').textContent = (view.changes?.length || 0) + ' file changes';
    for (const file of view.changes || []) {
      const item = document.createElement('li');
      item.textContent = (file.operation === 'remove' ? 'Remove ' : 'Replace ') + file.area + '/' + file.path + ' (' + file.bytes + ' bytes)';
      get('review-files').append(item);
    }
  }
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
  // A restore that never wrote leaves the data as it was; anything else needs manual recovery.
  function restoreOutcome(restore) {
    if (!restore?.attempted) return 'Local data was not changed. Close the app and start it again.';
    const counts = restore.replaced + ' replaced, ' + restore.removed + ' removed';
    return (restore.completed ? 'The older data was restored (' + counts + '), but the older version was not installed.' : 'The data restore is partial: ' + counts + '.')
      + ' Keep both backups and do not retry. Close the app and recover manually using the retained package and backup IDs below.';
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
    get('rollback-section').hidden = !native?.startRollback;
    rollbackStart.disabled = !known || confirming || active() || leaving() || busy() || !rollbackSelection();
    rollbackChoices.disabled = confirming || active() || busy() || !rollbacks.length;
    rollbackKeep.disabled = confirming || active() || busy() || !rollbacks.length;
    get('rollback-hint').textContent = rollbackKeep.checked
      ? 'Current data stays in place and must pass a compatibility check with the older version. A fresh copy of it is saved first.'
      : 'This flow restores the selected older data backup. Current data compatibility is not confirmed. A fresh copy of newer data is saved before you review the replacements.';
    get('rollback-note').textContent = rollbackReadError || (rollbacks.length ? 'Packages and backups are checked again during preparation.' : 'An older saved package and its matching data backup are required. Use the Releases link for manual installation.');
    const reviewing = state?.kind === 'rollback' && state.phase === 'review';
    const canConfirm = reviewing && known && !unresolved && !submitting && !confirmationPending && !canceling;
    reviewConfirm.disabled = !canConfirm || !reviewConsent.checked;
    reviewConsent.disabled = !canConfirm;
    get('review').hidden = !reviewing && !(state?.kind === 'rollback' && state.stopRequested && !leaving());
    get('progress').hidden = reviewing;
    const closeOnly = state?.stopRequested && !state.active && !leaving();
    cancel.textContent = closeOnly ? 'Close application' : 'Cancel preparation';
    cancel.disabled = !closeOnly && (starting || submitting || canceling || !state?.canCancel);
    const messages = {
      verifying: state?.kind === 'rollback' ? 'Verifying both saved packages and preparing a protected copy of current data.' : 'Verifying the update and the current version’s recovery package.',
      'backing-up': 'Saving a protected copy of local application data.',
      handoff: 'Handing the update to the installer. The application will close.',
      canceling: 'Canceling preparation and cleaning up.',
      accepted: 'Closing the application to install the update. Do not retry.',
      uncertain: 'The installer may have accepted the update. Closing the application; do not retry.',
      canceled: state?.kind === 'rollback' ? 'Rollback preparation canceled.' : 'Installation preparation canceled.',
      review: 'Review the data replacements below. Installation has not started.',
    };
    const phase = submitting ? 'handoff' : starting ? 'verifying' : state?.phase;
    let message = messages[phase] || (phase === 'failed' ? 'Could not prepare installation: ' + state.error : '') || state?.lastResult || '';
    if (closeOnly) message = 'Application shutdown was requested before handoff completed. Close and restart the app manually. ' + (state.error || '');
    if (closeOnly && state.kind === 'rollback') message = 'Rollback did not complete. ' + restoreOutcome(state.restore) + ' ' + (state.error || '');
    if (state?.kind === 'rollback' && phase === 'handoff') message = state.review?.keepCurrentData
      ? 'Keeping current data and handing the older version to the installer. Do not interrupt or retry.'
      : 'Restoring the selected backup and handing the older version to the installer. Do not interrupt or retry.';
    if (localError) message = localError;
    get('title').textContent = reviewing ? 'Review rollback' : state?.kind === 'rollback' ? 'Preparing application rollback' : 'Preparing application update';
    get('status').textContent = message;
    get('detail').textContent = message;
    if ((active() || leaving()) && !dialog.open) dialog.showModal();
    if (!active() && !leaving() && dialog.open) dialog.close();
  }
  function render(value) {
    if (state && value.id < state.id) return;
    const refused = unresolved && value.id <= (state?.id || 0) ? localError : '';
    if (!state || state.id !== value.id || value.phase !== 'review') confirmationPending = false;
    state = value;
    renderReview();
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
      if (!statusOnly && !active() && !leaving() && native.savedRollbacks) {
        try {
          const rows = await native.savedRollbacks(prerelease());
          if (version !== epoch || read !== observation) return;
          rollbackReadError = '';
          renderRollbacks(rows);
        } catch (error) {
          if (version !== epoch || read !== observation) return;
          renderRollbacks([]);
          rollbackReadError = 'Could not read saved rollback choices: ' + text(error);
        }
        update();
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
  async function beginPreparation(request, confirm, run) {
    confirming = true; update(); changed();
    try {
      if (!await confirm(request.tag)) return;
      epoch++; clearPoll(); starting = true; unresolved = true; localError = ''; update(); changed();
      render(await run());
    } catch (error) {
      known = false;
      localError = 'Could not confirm preparation started: ' + text(error) + ' Reading status before another attempt.';
    } finally { confirming = false; starting = false; update(); changed(); poll(); }
  }
  async function begin() {
    const request = selection();
    if (start.disabled || !request || !native?.confirmInstallation) return;
    return beginPreparation(request, native.confirmInstallation, () => native.startInstallation({tag:request.tag, cacheId:request.cacheId, includePrerelease:prerelease()}, {confirmed:true, editsResolved:true}));
  }
  async function beginRollback() {
    const request = rollbackSelection();
    if (rollbackStart.disabled || !request || !native?.confirmRollbackPreparation) return;
    const keepCurrentData = rollbackKeep.checked;
    return beginPreparation(request, native.confirmRollbackPreparation, () => native.startRollback({package:{tag:request.tag, cacheId:request.cacheId, includePrerelease:prerelease()}, snapshotId:request.snapshotId, keepCurrentData}));
  }
  async function confirmRollback() {
    if (reviewConfirm.disabled || !native?.confirmRollback || !state?.review) return;
    const request = {installation:{confirmed:true,editsResolved:true}, data:{confirmed:true,editsResolved:true,reviewId:state.review.reviewId}};
    epoch++; clearPoll(); submitting = true; confirmationPending = true; unresolved = true; localError = ''; update(); changed();
    try { render(await native.confirmRollback(state.id, request)); }
    catch (error) { known = false; localError = 'Could not confirm rollback handoff: ' + text(error) + ' Reading status; do not retry.'; }
    finally { submitting = false; update(); changed(); poll(); }
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
  rollbackChoices.addEventListener('change', update);
  rollbackKeep.addEventListener('change', update);
  rollbackStart.addEventListener('click', beginRollback);
  reviewConsent.addEventListener('change', update);
  reviewConfirm.addEventListener('click', confirmRollback);
  cancel.addEventListener('click', stop);
  dialog.addEventListener('cancel', event => { event.preventDefault(); return stop(); });
  update();
  return {active, leaving, refresh, update, attach(bridge) { native = bridge; update(); }};
};
