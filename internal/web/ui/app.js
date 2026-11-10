'use strict';
const $ = (s) => document.querySelector(s);
// Use default preferences when browser storage is unavailable.
const prefs = {
  get(key, fallback) { try { const v = localStorage.getItem(key); return v === null ? fallback : v; } catch (e) { return fallback; } },
  set(key, value) { try { localStorage.setItem(key, value); } catch (e) {} },
};
const state = {
  connected: false, summary: null,
  rows: [], pending: new Map(),
  apps: [], app: null, appItems: [], appSchema: null, appValues: {}, appPending: new Map(), installed: { type: 'all', visibility: 'all', sort: prefs.get('instSort', 'watch') },
  path: '', decodePath: '', tab: 'overview',
  preview: { screen: 1, aod: false, timer: null },
};

// ---------- helpers ----------
async function api(method, url, body, raw) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    if (raw) opts.body = body; else { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
  }
  const res = await fetch(url, opts);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}
function esc(s) { return String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])); }
function fmt(v) { if (v === null || v === undefined) return ''; if (Array.isArray(v)) return v.map((x) => x === null ? '–' : fmt(x)).join(', '); return String(v); }
const acronyms = new Set(['utc', 'hr', 'ble', 'gps', 'id', 'bpm', 'ant', 'lte', 'wifi', 'ohr', 'hrv', 'ftp', 'aod', 'am', 'pm']);
function humanize(s) {
  if (!s) return '';
  const words = String(s).replace(/^unknown(\d+)$/, 'Field $1').replace(/([a-z0-9])([A-Z])/g, '$1 $2').replace(/([A-Z]+)([A-Z][a-z])/g, '$1 $2').split(/[\s_]+/).filter(Boolean)
    .map((w) => (acronyms.has(w.toLowerCase()) ? w.toUpperCase() : w.toLowerCase()));
  if (!words.length) return '';
  words[0] = words[0][0].toUpperCase() + words[0].slice(1);
  return words.join(' ');
}
function humanizeEnum(s) { return typeof s === 'string' ? humanize(s) : fmt(s); }
function hex6(n) { return (Number(n) >>> 0 & 0xFFFFFF).toString(16).padStart(6, '0').toUpperCase(); }
function el(html) { const t = document.createElement('template'); t.innerHTML = html.trim(); return t.content.firstElementChild; }

function toast(msg, kind) {
  const t = el(`<div class="toast ${kind || ''}" role="status">${esc(msg)}</div>`);
  $('#toasts').appendChild(t);
  setTimeout(() => t.remove(), kind === 'bad' ? 9000 : 5000);
}

function confirmDialog({ title, body, ok }) {
  return new Promise((resolve) => {
    const dlg = $('#dlg');
    $('#dlg-title').textContent = title;
    $('#dlg-body').innerHTML = body;
    $('#dlg-ok').textContent = ok || 'Confirm';
    dlg.returnValue = 'cancel';
    dlg.addEventListener('close', () => resolve(dlg.returnValue === 'ok'), { once: true });
    dlg.showModal();
  });
}

function setConn(kind, text) {
  const c = $('#conn');
  c.className = 'conn ' + kind;
  $('#conn-text').textContent = text;
}

// ---------- tabs ----------
document.querySelectorAll('.nav-item[data-tab]').forEach((b) => b.addEventListener('click', () => showTab(b.dataset.tab)));
function showTab(tab, fromRoute) {
  state.tab = tab;
  document.querySelectorAll('.nav-item').forEach((x) => x.classList.toggle('active', x.dataset.tab === tab));
  document.querySelectorAll('.tab').forEach((t) => t.classList.toggle('active', t.id === 'tab-' + tab));
  if (!fromRoute) { const [item, params] = tabRoute(tab); setRoute(tab, item, params); }
  if (state.connected) loadTab(tab); else if (tab === 'cloud') loadCloud(); else if (tab === 'store' && !state.store.loaded) loadStore();
  if (tab === 'application') window.applicationPage.activate();
  if (tab === 'ai' && window.loadMCP) window.loadMCP();
  updateActionBar();
}
function loadTab(tab) {
  if (tab === 'settings' && !state.rows.length) loadSettings();
  if (tab === 'apps' && !$('#apps-list').querySelector('.app-item')) loadApps();
  if (tab === 'installed') loadInstalled();
  if (tab === 'files') loadFiles(state.path, state.decodePath);
  if (tab === 'updates' && !state.updates.checkedAt) loadUpdates();
  if (tab === 'cloud') loadCloud();
  if (tab === 'store' && !state.store.loaded) loadStore();
  if (tab === 'health') loadHealth();
  if (tab === 'training') loadWorkouts();
  if (tab === 'music') loadMusic();
}

// ---------- routes ----------
// Paths preserve the selected tab and item; query parameters preserve store searches.
// Preferences use localStorage; unsaved edits stay in memory.
const tabNames = new Set([...document.querySelectorAll('.nav-item[data-tab]')].map((b) => b.dataset.tab));
function parseRoute() {
  const [tab, ...rest] = location.pathname.replace(/^\//, '').split('/');
  let item = rest.join('/');
  try { item = decodeURIComponent(item); } catch (e) { item = ''; }
  return { tab: tabNames.has(tab) ? tab : 'overview', item, params: new URLSearchParams(location.search) };
}
function routePath(tab, item, params) {
  let p = '/' + tab;
  if (item) p += '/' + item.replace(/[%#?\s]/g, encodeURIComponent);
  const q = params ? params.toString() : '';
  if (q) p += '?' + q;
  return p === '/overview' ? '/' : p;
}
// Replace history for searches and default selections; push it for navigation.
function setRoute(tab, item, params, replace) {
  const p = routePath(tab, item, params);
  if (p === location.pathname + location.search) return;
  history[replace ? 'replaceState' : 'pushState'](null, '', p);
}
function tabRoute(tab) {
  if (tab === 'apps') return [state.app ? state.app.name : ''];
  if (tab === 'store') return [state.store.selected || '', storeParams()];
  if (tab === 'health') return [state.health.selAct || ''];
  if (tab === 'files') return [state.decodePath || state.path, state.decodePath ? new URLSearchParams({ view: 'decode' }) : null];
  return [''];
}
function storeParams() { const p = new URLSearchParams(); if (state.store.q) p.set('q', state.store.q); return p; }
function navigate(tab, item) { setRoute(tab, item); applyRoute(); }
// App references accept names, settings files and store IDs, ignoring case.
function findApp(ref) {
  const r = ref.toLowerCase();
  return state.apps.find((a) => a.fileName && [a.name, a.fileName, a.id].some((v) => v && v.toLowerCase() === r));
}
// Loaders resolve route items whose data has not arrived yet.
let routeTurn = 0;
async function applyRoute() {
  const turn = ++routeTurn;
  const r = parseRoute();
  if (r.tab === 'apps' && r.item && $('#apps-list .app-item')) {
    const a = findApp(r.item);
    if (a && !await selectApp(a, true)) return;
    if (turn !== routeTurn) return;
  }
  if (r.tab === 'health') {
    state.health.selAct = r.item || null;
    if (!r.item) { $('#act-detail').hidden = true; if (state.health.acts) renderActivities(); } else if (state.connected) showActivity(r.item);
  }
  if (r.tab === 'files') {
    state.decodePath = r.params.get('view') === 'decode' ? r.item : '';
    state.path = state.decodePath ? r.item.slice(0, Math.max(0, r.item.lastIndexOf('/'))) : r.item;
  }
  if (r.tab === 'store') {
    const q = r.params.get('q') || '';
    if (q !== state.store.q) { state.store.q = q; $('#store-q').value = q; if (state.store.loaded) loadStore(); }
    if (r.item && r.item !== state.store.selected) showStoreApp(r.item);
    else if (!r.item) { state.store.selected = null; $('#store-detail').hidden = true; document.querySelectorAll('.store-card').forEach((c) => c.classList.remove('active')); }
  }
  showTab(r.tab, true);
}
window.addEventListener('popstate', applyRoute);
// Ask before reloading or closing with unsaved edits.
window.addEventListener('beforeunload', (ev) => {
  if (state.pending.size || state.appPending.size || state.cloudPending.size) { ev.preventDefault(); ev.returnValue = true; }
});

if ('launchQueue' in window) {
  window.launchQueue.setConsumer((params) => {
    if (!params.targetURL) return;
    const u = new URL(params.targetURL);
    if (u.origin !== location.origin || (u.pathname === '/' && !u.search)) return;
    const p = u.pathname + u.search;
    if (p === location.pathname + location.search) return;
    history.pushState(null, '', p);
    applyRoute();
  });
}

// ---------- connection + overview ----------
async function connect() {
  setConn('busy', 'Connecting…');
  $('#btn-connect').disabled = true;
  try {
    const st = await api('POST', '/api/connect');
    state.connected = true; state.summary = st.summary;
    storeConnectionChanged();
    setConn('ok', 'Connected');
    $('#devname').textContent = st.summary.model;
    renderOverview(st.summary);
    loadNewFiles();
    loadTab(state.tab);
    $('#btn-connect').textContent = 'Reconnect';
  } catch (e) {
    const wasConnected = state.connected;
    state.connected = false;
    if (wasConnected) storeConnectionChanged();
    setConn('bad', 'Not connected');
    toast('Could not connect: ' + e.message, 'bad');
  } finally { $('#btn-connect').disabled = false; }
}
function renderFree() { const el2 = $('#apps-free'); if (el2 && state.summary && state.summary.storage) el2.textContent = `${fmtSize(state.summary.storage.freeBytes)} free on the watch.`; }
function renderOverview(s) {
  renderFree();
  $('#overview-empty').hidden = true;
  $('#overview').hidden = false;
  $('#hero-model').textContent = s.model;
  $('#hero-sub').innerHTML = `<span>Software <span class="mono">${esc(s.software)}</span></span><span>Unit ID <span class="mono">${s.unitId}</span></span><span>Part <span class="mono">${esc(s.partNumber)}</span></span><button class="linkish" id="hero-apps" type="button">${s.appCount} apps and glances</button>`;
  $('#hero-apps').onclick = () => showTab('installed');
  const now = new Date();
  if (!state.heroValues) $('#hero-face').textContent = `${pad2(now.getHours())}\n${pad2(now.getMinutes())}`;
  $('#overview-when').textContent = `Read ${now.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`;
  renderHeroState();
  loadHeroFace();
  loadToday();
  loadRecentActs();
}
// Show the connected watch's Easy+ settings with the editor's sample metrics.
async function loadHeroFace() {
  if (!window.EasyFace) return;
  try {
    const apps = await fetchApps();
    const app = apps.find((a) => a.fileName && EasyFace.supports(a.id));
    if (!app) return;
    const as = await api('GET', `/api/apps/${encodeURIComponent(app.fileName)}/settings`);
    let rows = state.rows;
    if (!rows.length) { try { rows = (await api('GET', '/api/settings')).rows; } catch (e) { rows = []; } }
    state.heroValues = as.values;
    state.hero24h = (rows.find((r) => r.field === 'timeMode') || {}).value === 'hour24';
    renderHeroFace();
  } catch (e) {}
}
function renderHeroFace() {
  if (!state.heroValues) return;
  $('#hero-face').innerHTML = EasyFace.render(state.heroValues, { screen: 1, aod: false, systemIs24h: state.hero24h });
}
setInterval(() => { if (state.tab === 'overview') renderHeroFace(); }, 30000);
// Follow connection and sync events from the USB watcher.
let autoSeen = Date.now();
async function pollStatus() {
  try {
    const st = await api('GET', '/api/status');
    for (const ev of (st.auto && st.auto.events) || []) {
      const t = new Date(ev.at).getTime();
      if (t <= autoSeen) continue;
      autoSeen = t;
      toast(ev.text, ev.kind === 'error' ? 'bad' : ev.kind === 'unplugged' ? 'warn' : 'ok');
      if (ev.kind === 'synced') { loadNewFiles(); if (state.tab === 'cloud') loadCloud(); }
    }
    if (st.connected && !state.connected) {
      state.connected = true; state.summary = st.summary; setConn('ok', 'Connected');
      storeConnectionChanged();
      $('#devname').textContent = st.summary.model; $('#btn-connect').textContent = 'Reconnect';
      renderOverview(st.summary); loadNewFiles(); loadTab(state.tab);
    } else if (!st.connected && state.connected) {
      state.connected = false; setConn('bad', 'Unplugged');
      storeConnectionChanged();
      state.rows = []; state.apps = []; state.app = null;
    }
  } catch (e) {}
}
setInterval(pollStatus, 7000);
$('#hero-watch').onclick = () => showTab('apps');
function storageLine(s) {
  if (!s || !s.storage) return '';
  const used = s.storage.capacityBytes - s.storage.freeBytes, pct = Math.round(used / s.storage.capacityBytes * 100);
  return `<span class="hint">Storage: ${fmtSize(s.storage.freeBytes)} free of ${fmtSize(s.storage.capacityBytes)}</span><span class="bar storage" title="${pct}% used"><i style="width:${pct}%"></i></span>`;
}
async function renderHeroState() {
  const st = $('#hero-state');
  st.innerHTML = '<span class="big"><span class="dot"></span>Connected over USB</span>' + storageLine(state.summary);
  try {
    const c = await api('GET', '/api/cloud/status');
    st.innerHTML += c.loggedIn ? `<span class="hint">Garmin Connect: ${esc(c.profile && c.profile.name ? c.profile.name : c.email)}</span>` : '<span class="hint">Garmin Connect: not signed in</span>';
  } catch (e) {}
}
async function loadToday() {
  const box = $('#today');
  try {
    const rows = await api('GET', '/api/health/daily?days=2');
    const d = rows[rows.length - 1], y = rows[0];
    if (d.error) throw new Error(d.error);
    const tile = (k, v, unit, sub, pct, teal) => `<div class="tile"><div class="k">${k}</div><div class="v">${v}${unit ? `<small>${unit}</small>` : ''}</div>${pct !== undefined ? `<div class="bar"><i class="${teal ? 'teal' : ''}" style="width:${Math.max(0, Math.min(100, pct))}%"></i></div>` : ''}<div class="s">${sub}</div></div>`;
    box.innerHTML = tile('Steps today', d.steps.toLocaleString(), '', `goal ${d.stepGoal.toLocaleString()}`, d.steps / Math.max(1, d.stepGoal) * 100)
      + tile('Resting heart rate', d.restingHr || '–', 'bpm', y.restingHr ? `yesterday ${y.restingHr}` : '')
      + tile('Sleep', d.sleepSec ? fmtHM(d.sleepSec) : '–', '', y.sleepSec ? `night before ${fmtHM(y.sleepSec)}` : '')
      + tile('Body battery', `${d.bodyBatteryLow}–${d.bodyBatteryHigh}`, '', `stress avg ${d.avgStress}`, d.bodyBatteryHigh, true)
      + tile('Calories', d.calories.toLocaleString(), 'kcal', `${d.activeCalories.toLocaleString()} active`);
  } catch (e) {
    box.innerHTML = `<div class="tile prompt"><div class="k">${/signed in/.test(e.message) ? 'Sign in to Garmin Connect to view daily health.' : esc(e.message)}</div></div>`;
  }
}
async function loadRecentActs() {
  const ul = $('#recent-acts');
  try {
    const list = await api('GET', '/api/health/activities');
    const acts = list.activities.slice(0, 5);
    ul.innerHTML = acts.length ? '' : `<li class="muted">${list.pending ? 'Decoding activities from the watch…' : 'No activities on the watch.'}</li>`;
    for (const a of acts) {
      const li = el(`<li><span><span class="sport">${esc(humanize(a.sport))}</span> <span class="when">${esc(new Date(a.start).toLocaleDateString([], { month: 'short', day: 'numeric' }))}</span></span><span class="num">${a.distanceM ? (a.distanceM / 1000).toFixed(2) + ' km' : ''}</span><span class="num">${fmtDur(a.timerSec)}</span></li>`);
      li.style.cursor = 'pointer';
      li.onclick = () => navigate('health', a.file);
      ul.appendChild(li);
    }
    if (list.pending) setTimeout(loadRecentActs, 3000);
  } catch (e) { ul.innerHTML = `<li class="muted">${esc(e.message)}</li>`; }
}
$('#nav-online').open = prefs.get('navOnline', '0') === '1'; $('#nav-online').addEventListener('toggle', () => prefs.set('navOnline', $('#nav-online').open ? '1' : '0'));
const fileTypes = { 2: 'Settings file', 3: 'Sport profile', 5: 'Workout', 7: 'Schedule', 8: 'Locations', 11: 'Goals', 29: 'Personal records', 37: 'Golf clubs', 39: 'Adjustments', 41: 'Settings changes' };
async function describeFit(path) {
  try {
    const msgs = await api('GET', '/api/decode?path=' + encodeURIComponent(path));
    const fid = msgs.find((m) => m.num === 0);
    const type = fid && fid.fields.find((f) => f.num === 0);
    const t = type ? type.raw : null;
    const changes = msgs.filter((m) => m.num === 217).length;
    let what = fileTypes[t] || (t ? `FIT type ${t}` : 'FIT file');
    if (changes) what += ` (${changes} field${changes > 1 ? 's' : ''})`;
    const creator = fid && fid.fields.find((f) => f.name === 'product');
    if (creator && creator.raw === 65534) what += ' from Garmin Connect';
    return what;
  } catch (e) { return 'FIT file'; }
}
async function loadNewFiles() {
  const ul = $('#newfiles');
  try {
    const entries = await api('GET', '/api/files?path=' + encodeURIComponent('GARMIN/NewFiles'));
    const changes = await api('GET', '/api/apps/pending').catch(() => []);
    const total = entries.length + (changes.length ? 1 : 0);
    $('#badge-pending').hidden = total === 0;
    $('#badge-pending').textContent = total;
    if (!total) { ul.innerHTML = '<li class="muted">No pending changes.</li>'; return; }
    ul.innerHTML = '';
    if (changes.length) {
      const li = el(`<li><div><div class="what">App list: ${changes.length} change${changes.length === 1 ? '' : 's'}</div><div class="meta">${esc(changes.map((c) => `${c.name} → ${c.hidden ? 'hidden' : 'shown'}`).join(', '))} · <span class="mono">GARMIN/Apps/IN.BIN</span></div></div><button class="btn small danger">Cancel</button></li>`);
      li.querySelector('button').onclick = async () => {
        if (!await confirmDialog({ title: 'Cancel the app-list changes?', body: '<p>The watch keeps its current activity list and glance loop.</p>', ok: 'Cancel changes' })) return;
        try { await api('DELETE', '/api/apps/pending'); toast('App-list changes cancelled'); state.installed.pending = []; loadNewFiles(); if (state.tab === 'installed') renderInstalled(); } catch (err) { toast(err.message, 'bad'); }
      };
      ul.appendChild(li);
    }
    for (const e of entries) {
      const li = el(`<li><div><div class="what">Reading…</div><div class="meta">${esc((e.modified || '').replace('T', ' '))} · ${e.size.toLocaleString()} B · <span class="mono">${esc(e.name)}</span></div></div><button class="btn small danger">Remove</button></li>`);
      li.querySelector('button').onclick = async () => {
        if (!await confirmDialog({ title: 'Remove pending file?', body: `<p>${esc(e.name)} will not be applied to the watch.</p>`, ok: 'Remove' })) return;
        try { await api('DELETE', '/api/file?path=' + encodeURIComponent('GARMIN/NewFiles/' + e.name)); toast('Removed ' + e.name); loadNewFiles(); } catch (err) { toast(err.message, 'bad'); }
      };
      ul.appendChild(li);
      describeFit('GARMIN/NewFiles/' + e.name).then((d) => { li.querySelector('.what').textContent = d; });
    }
  } catch (e) { ul.innerHTML = `<li class="muted">${esc(e.message)}</li>`; }
}
$('#btn-connect').onclick = connect;
$('#btn-refresh').onclick = async () => {
  if (!state.connected) return connect();
  try { await api('POST', '/api/refresh'); } catch (e) {}
  state.rows = []; state.apps = []; state.pending.clear(); state.appPending.clear(); state.app = null;
  updateActionBar();
  try { const st = await api('GET', '/api/status?connect=1'); if (st.summary) renderOverview(st.summary); } catch (e) {}
  loadNewFiles();
  loadTab(state.tab);
  toast('Reloaded from the watch');
};

// ---------- watch settings ----------
const groupTitles = {
  deviceSettings: 'Watch', userProfile: 'User profile', zonesTarget: 'Zone targets', hrZone: 'Heart rate zones',
  bikeProfile: 'Bike', hrmProfile: 'Heart rate strap', connectivity: 'Connectivity', ohrSettings: 'Wrist heart rate',
  alarm: 'Alarms', sleepSchedule: 'Sleep schedule', sensorPairing: 'Paired sensors',
};
const groupOrder = ['deviceSettings', 'userProfile', 'ohrSettings', 'connectivity', 'alarm', 'sleepSchedule', 'hrZone', 'zonesTarget', 'bikeProfile', 'hrmProfile', 'sensorPairing'];
async function loadSettings() {
  $('#settings-groups').innerHTML = '<p class="muted">Loading watch settings…</p>';
  try {
    const data = await api('GET', '/api/settings');
    state.rows = data.rows;
    renderSettings();
  } catch (e) { $('#settings-groups').innerHTML = `<p class="muted">${esc(e.message)}</p>`; }
}
function rowKey(r) { return `${r.mesgNum}[${r.index}].${r.fieldNum}`; }
function isEditable(r) {
  return r.known && !Array.isArray(r.raw) && r.type !== 'string' && r.mesgNum !== 0 && r.mesgNum !== 49 && r.value !== undefined;
}
function renderSettings() {
  const root = $('#settings-groups');
  const q = $('#settings-filter').value.trim().toLowerCase();
  const showUnknown = $('#settings-unknown').checked;
  root.innerHTML = '';
  const counts = {};
  for (const r of state.rows) counts[r.mesg] = Math.max(counts[r.mesg] || 0, r.index + 1);
  const groups = new Map();
  for (const r of state.rows) {
    if (r.mesgNum === 0 || r.mesgNum === 49) continue;
    if (!showUnknown && !r.known) continue;
    const label = humanize(r.field);
    const hay = `${r.mesg} ${label} ${r.field} ${fmt(r.value)} ${r.note || ''}`.toLowerCase();
    if (q && !hay.includes(q)) continue;
    if (!groups.has(r.mesg)) groups.set(r.mesg, []);
    groups.get(r.mesg).push(r);
  }
  const names = [...groups.keys()].sort((a, b) => {
    const ia = groupOrder.indexOf(a), ib = groupOrder.indexOf(b);
    return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib) || a.localeCompare(b);
  });
  const jump = $('#settings-jump'); jump.innerHTML = ''; jump.hidden = names.length < 2;
  if (!names.length) { root.innerHTML = '<p class="muted">No settings match.</p>'; return; }
  for (const name of names) {
    const rows = groups.get(name);
    const title = groupTitles[name] || (name.startsWith('mesg') || name.startsWith('unknown') ? `Undocumented message ${rows[0].mesgNum}` : humanize(name));
    const chip = el(`<button class="chip" type="button">${esc(title)} <span class="muted">${rows.length}</span></button>`);
    jump.appendChild(chip);
    const g = el(`<section class="group"><div class="group-head"><h2>${esc(title)}</h2><span class="count">${rows.length} field${rows.length > 1 ? 's' : ''}${counts[name] > 1 ? ` · ${counts[name]} entries` : ''}</span></div><div class="rows"></div></section>`);
    const list = g.querySelector('.rows');
    for (const r of rows) list.appendChild(settingRow(r, counts[name] > 1));
    root.appendChild(g);
    chip.onclick = () => g.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }
}
function settingRow(r, repeated) {
  const key = rowKey(r);
  const label = (repeated ? `${r.index + 1}. ` : '') + humanize(r.field);
  const tags = [];
  if (r.managedByConnect) tags.push('<span class="tag connect" title="Also update this setting in Garmin Connect">Garmin Connect</span>');
  if (!r.known) tags.push('<span class="tag">undocumented</span>');
  else if (r.confidence) tags.push(`<span class="tag guess">${esc(r.confidence)}</span>`);
  const meta = [r.units, r.note].filter(Boolean).join(' · ');
  let control;
  if (isEditable(r) && r.options && r.options.length) {
    const cur = fmt(r.value);
    const opts = r.options.includes(cur) ? r.options : [cur, ...r.options];
    control = `<select data-key="${esc(key)}">${opts.map((o) => `<option value="${esc(o)}" ${o === cur ? 'selected' : ''}>${esc(humanizeEnum(o))}</option>`).join('')}</select>`;
  } else if (isEditable(r)) {
    control = `<input type="number" step="any" data-key="${esc(key)}" value="${esc(fmt(r.value))}">`;
  } else {
    control = `<span class="mono">${esc(fmt(r.value)) || '–'}</span>`;
  }
  const row = el(`<div class="row ${isEditable(r) ? '' : 'readonly'}"><div><div class="label">${esc(label)}${tags.join('')}</div><div class="key">${esc(r.mesg)}.${esc(r.field)}</div></div><div class="control">${control}</div><div class="meta">${esc(meta)}</div></div>`);
  const ctl = row.querySelector('[data-key]');
  if (ctl) {
    if (state.pending.has(key)) { ctl.value = state.pending.get(key).value; ctl.classList.add('changed'); }
    ctl.addEventListener('change', () => {
      const orig = fmt(r.value);
      if (ctl.value === orig) { state.pending.delete(key); ctl.classList.remove('changed'); }
      else {
        const to = ctl.tagName === 'SELECT' ? humanizeEnum(ctl.value) : ctl.value;
        state.pending.set(key, { mesg: String(r.mesgNum), index: r.index, field: String(r.fieldNum), value: ctl.value, label: `${groupTitles[r.mesg] || humanize(r.mesg)} · ${label}`, from: ctl.tagName === 'SELECT' ? humanizeEnum(orig) : orig, to });
        ctl.classList.add('changed');
      }
      updateActionBar();
    });
  }
  return row;
}
$('#settings-filter').addEventListener('input', renderSettings);
$('#settings-unknown').addEventListener('change', renderSettings);

// ---------- action bar (shared) ----------
function updateActionBar() {
  const bar = $('#actionbar');
  const n = state.tab === 'apps' ? state.appPending.size : state.tab === 'settings' ? state.pending.size : state.tab === 'cloud' ? state.cloudPending.size : 0;
  bar.hidden = n === 0;
  if (!n) return;
  const what = state.tab === 'apps' ? (state.app ? state.app.name : 'app') : state.tab === 'cloud' ? 'Garmin Connect' : 'watch';
  $('#actionbar-text').textContent = `${n} unsaved change${n > 1 ? 's' : ''} · ${what}`;
}
$('#btn-discard').onclick = () => {
  if (state.tab === 'apps') { state.appPending.clear(); renderAppSettings(); renderPreview(); } else if (state.tab === 'cloud') { state.cloudPending.clear(); renderCloudSettings(); } else { state.pending.clear(); renderSettings(); }
  updateActionBar();
};
$('#btn-review').onclick = () => (state.tab === 'apps' ? applyAppChanges() : state.tab === 'cloud' ? applyCloudChanges() : applySettings());
async function applySettings() {
  const list = [...state.pending.values()];
  const body = `<ul class="changes">${list.map((c) => `<li><span>${esc(c.label)}</span><span><span class="from">${esc(c.from)}</span><span class="to">${esc(c.to)}</span></span></li>`).join('')}</ul><p class="note">Unplug the watch after saving to apply these changes.</p>`;
  if (!await confirmDialog({ title: 'Save watch settings?', body, ok: 'Save changes' })) return;
  try {
    const res = await api('POST', '/api/settings/apply', { changes: list.map(({ mesg, index, field, value }) => ({ mesg, index, field, value })) });
    state.pending.clear(); updateActionBar();
    toast(res.note, res.note.includes('Warning') ? 'warn' : '');
    loadNewFiles();
  } catch (e) { toast(e.message, 'bad'); }
}

// ---------- apps ----------
const sections = [
  ['📊', 'Progress bars'], ['⌚', 'Fields on the face'], ['🎨', 'Colors'], ['⚙️', 'Options'], ['🔄️', 'Second screen'], ['🔄', 'Second screen'],
  ['1️⃣', 'Fonts'], ['🌍', 'Time zones'],
];
function splitLabel(label) {
  for (const [emoji, title] of sections) if (label.startsWith(emoji)) return { section: title, text: label.slice(emoji.length).trim() };
  const m = label.match(/^(\p{Extended_Pictographic}️?)\s*/u);
  if (m) return { section: 'Other', text: label.slice(m[0].length) };
  return { section: 'General', text: label };
}
async function fetchApps() {
  if (!state.apps.length) state.apps = await api('GET', '/api/apps');
  return state.apps;
}
async function loadApps() {
  const root = $('#apps-list');
  root.innerHTML = '<p class="muted">Reading installed apps…</p>';
  try {
    await fetchApps();
    root.innerHTML = '';
    const seen = new Set();
    for (const a of state.apps) {
      if (!a.fileName || seen.has(a.fileName)) continue;
      seen.add(a.fileName);
      const b = el(`<button class="app-item" data-file="${esc(a.fileName)}"><span class="app-ico">${esc(a.name.slice(0, 2).toUpperCase())}</span><span class="body"><span class="app-name">${esc(a.name)}</span><span class="app-sub">${esc(a.typeName)} · v${a.version}</span></span></button>`);
      b.onclick = () => selectApp(a);
      root.appendChild(b);
    }
    markAppUpdates();
    const natives = state.apps.filter((a) => !a.fileName).length;
    const note = el(`<p class="apps-note">${natives} built-in entries in <button class="linkish" type="button">Apps &amp; glances</button>.</p>`);
    note.querySelector('button').onclick = () => showTab('installed');
    root.appendChild(note);
    if (!state.app) {
      // The app named in the URL, else the watch face, else the first one.
      const want = parseRoute(), shown = state.apps.filter((a) => seen.has(a.fileName));
      const pick = (want.tab === 'apps' && want.item && findApp(want.item)) || shown.find((a) => a.typeName === 'watchface') || shown[0];
      if (pick) await selectApp(pick, true);
    }
  } catch (e) { root.innerHTML = `<p class="muted">${esc(e.message)}</p>`; }
}
let appSelection = 0, appDiscardPrompt = null;
async function selectApp(app, replace) {
  const selection = ++appSelection;
  if (state.app && state.app.fileName === app.fileName) return true;
  const current = state.app;
  if (current && state.appPending.size) {
    if (!appDiscardPrompt) {
      appDiscardPrompt = confirmDialog({
        title: 'Discard unsaved changes?',
        body: `<p>${state.appPending.size} changes to ${esc(current.name)} are not applied yet.</p>`,
        ok: 'Discard',
      }).finally(() => { appDiscardPrompt = null; });
    }
    const discard = await appDiscardPrompt;
    if (selection !== appSelection || state.app !== current) return false;
    if (!discard) { setRoute('apps', current.name, null, true); showTab('apps', true); return false; }
  }
  document.querySelectorAll('.app-item').forEach((x) => x.classList.toggle('active', x.dataset.file === app.fileName));
  loadAppSettings(app);
  setRoute('apps', app.name, null, replace);
  return true;
}
async function loadAppSettings(app) {
  state.app = app; state.appPending.clear(); state.appSchema = null; state.appItems = []; updateActionBar();
  $('#app-store').href = 'https://apps.garmin.com/apps/' + app.id; $('#app-store').hidden = !app.id;
  $('#app-title').textContent = app.name;
  $('#app-hint').textContent = `${humanize(app.typeName)} · ${app.fileName}.SET`;
  $('#app-filter').hidden = false;
  $('#app-remove').hidden = app.native || !app.fileName || /^toystore$/i.test(app.fileName);
  $('#app-remove').onclick = () => removeApp(app);
  $('#app-groups').innerHTML = '<p class="muted">Reading settings…</p>';
  const notice = $('#app-notice'); notice.hidden = true; state.appReadOnly = false;
  try {
    const as = await api('GET', `/api/apps/${encodeURIComponent(app.fileName)}/settings`);
    if (state.app !== app) return;
    state.appValues = as.values || {}; state.appItems = as.items || [];
    if (!state.appItems.length) {
      notice.hidden = false;
      notice.textContent = `No settings available for ${app.name}.`;
    } else if (as.schemaError && /404/.test(as.schemaError)) {
      state.appReadOnly = true; notice.hidden = false;
      notice.textContent = 'No settings page found in the store. These values are read-only.';
    } else if (as.schemaError) {
      notice.hidden = false;
      notice.textContent = `Could not load setting labels from the store: ${as.schemaError}. Showing raw values.`;
    } else {
      const schema = await api('GET', `/api/apps/${encodeURIComponent(app.fileName)}/schema`).catch(() => null);
      if (state.app !== app) return;
      state.appSchema = schema;
    }
    renderAppSettings();
    setupPreview(app);
  } catch (e) {
    if (state.app !== app) return;
    if (/not found/i.test(e.message)) {
      notice.hidden = false;
      notice.textContent = `No settings file found for ${app.name}.`;
      $('#app-hint').textContent = humanize(app.typeName);
      $('#app-groups').innerHTML = '';
      setupPreview(app);
    } else $('#app-groups').innerHTML = `<p class="muted">${esc(e.message)}</p>`;
  }
}
async function removeApp(app) {
  const ok = await confirmDialog({
    title: `Remove ${app.name} from the watch?`,
    body: '<p>Permanently deletes the app, its settings and data. Unplug the watch to finish removal.</p><p class="note">If this is your current watch face, choose another face on the watch afterwards.</p>',
    ok: 'Remove',
  });
  if (!ok) return;
  try {
    const r = await api('DELETE', `/api/apps/${encodeURIComponent(app.fileName)}`);
    toast(`${app.name}: ${r.removed.length} file${r.removed.length === 1 ? '' : 's'} deleted. Unplug the watch to finish.`);
  } catch (e) { toast(e.message, 'bad'); }
}
function schemaFor(key) { return state.appSchema ? state.appSchema.settings.find((s) => s.key === key) || null : null; }
function renderAppSettings() {
  const root = $('#app-groups');
  const q = $('#app-filter').value.trim().toLowerCase();
  root.innerHTML = '';
  const order = state.appSchema ? state.appSchema.settings.map((s) => s.key).filter((k) => k in state.appValues) : state.appItems.map((i) => i.key);
  const groups = new Map();
  let scope = '';
  for (const k of order) {
    const sc = schemaFor(k);
    const v = state.appValues[k];
    if (sc && sc.readOnly && /^=+$/.test(sc.label || '')) { // separators in the store form, e.g. "=== Screen 2 settings ==="
      const m = String(v || sc.default || '').replace(/=/g, '').trim();
      scope = /end/i.test(m) ? '' : m.replace(/\s*settings\s*$/i, '');
      continue;
    }
    const { section, text } = splitLabel(sc && sc.label ? sc.label : humanize(k));
    if (q && !k.toLowerCase().includes(q) && !text.toLowerCase().includes(q)) continue;
    const title = scope ? `${section} · ${scope}` : section;
    if (!groups.has(title)) groups.set(title, []);
    groups.get(title).push({ k, sc, v, text });
  }
  if (!groups.size) { root.innerHTML = order.length ? '<p class="muted">No settings match.</p>' : ''; return; }
  for (const [section, items] of groups) {
    const g = el(`<section class="group"><div class="group-head"><h2>${esc(section)}</h2><span class="count">${items.length}</span></div><div class="rows"></div></section>`);
    const list = g.querySelector('.rows');
    for (const it of items) list.appendChild(appRow(it));
    root.appendChild(g);
  }
}
function appRow({ k, sc, v, text }) {
  const t = typeof v;
  const colorKey = /color|_bg$/i.test(k);
  let control; let readonly = false;
  if (state.appReadOnly || (sc && sc.readOnly)) {
    readonly = true;
    control = `<span class="${state.appReadOnly ? 'mono' : 'muted'}">${esc(String(v ?? ''))}</span>`;
  } else if (sc && sc.type === 'select') {
    const cur = String(v);
    const sw = colorKey && t === 'number' && v >= 0 ? `<span class="swatch" style="background:#${hex6(v)}"></span>` : '';
    control = `${sw}<select data-key="${esc(k)}">${sc.options.map((o) => `<option value="${esc(o.value)}" ${cur === o.value ? 'selected' : ''}>${esc(o.label)}</option>`).join('')}</select>`;
  } else if (sc && sc.type === 'number') {
    const range = sc.min || sc.max ? `${sc.min || ''} to ${sc.max || ''}` : '';
    control = `<input type="number" data-key="${esc(k)}" value="${v}" ${sc.min ? `min="${esc(sc.min)}"` : ''} ${sc.max ? `max="${esc(sc.max)}"` : ''} step="${esc(sc.step || 'any')}" title="${esc(range)}">${range ? `<span class="muted">${esc(range)}</span>` : ''}`;
  } else if (t === 'boolean' || (sc && sc.type === 'toggle')) {
    control = `<input type="checkbox" class="switch" data-key="${esc(k)}" ${v ? 'checked' : ''}>`;
  } else if (t === 'string' && colorKey && /^[0-9A-Fa-f]{6}$/.test(v)) {
    control = `<input type="color" data-color="${esc(k)}" value="#${esc(v)}"><input data-key="${esc(k)}" value="${esc(v)}" maxlength="6" style="width:110px" class="mono">`;
  } else if (t === 'number') {
    control = `<input type="number" data-key="${esc(k)}" value="${v}" step="any">`;
  } else {
    control = `<input data-key="${esc(k)}" value="${esc(String(v ?? ''))}" ${sc && sc.maxLength ? `maxlength="${sc.maxLength}"` : ''}>`;
  }
  const row = el(`<div class="row app-row ${readonly ? 'readonly' : ''}"><div><div class="label">${esc(text)}</div><div class="key">${esc(k)}</div></div><div class="control">${control}</div></div>`);
  const ctl = row.querySelector('[data-key]');
  if (!ctl) return row;
  const picker = row.querySelector('[data-color]');
  const isBool = ctl.type === 'checkbox';
  if (state.appPending.has(k)) { ctl.classList.add('changed'); if (isBool) ctl.checked = state.appPending.get(k).value; else ctl.value = state.appPending.get(k).value; }
  const onChange = () => {
    let nv = isBool ? ctl.checked : (ctl.type === 'number' || (sc && sc.type === 'select' && t === 'number') ? Number(ctl.value) : ctl.value);
    if (picker && /^[0-9A-Fa-f]{6}$/.test(ctl.value)) picker.value = '#' + ctl.value;
    const same = nv === v || (ctl.type === 'number' && Number.isNaN(nv));
    if (same) { state.appPending.delete(k); ctl.classList.remove('changed'); }
    else {
      const toLabel = sc && sc.type === 'select' ? (sc.options.find((o) => o.value === String(nv)) || {}).label || String(nv) : (isBool ? (nv ? 'On' : 'Off') : String(nv));
      const fromLabel = sc && sc.type === 'select' ? (sc.options.find((o) => o.value === String(v)) || {}).label || String(v) : (isBool ? (v ? 'On' : 'Off') : String(v));
      state.appPending.set(k, { value: nv, label: text, from: fromLabel, to: toLabel });
      ctl.classList.add('changed');
    }
    updateActionBar();
    const sw = row.querySelector('.swatch');
    if (sw && typeof nv === 'number') sw.style.background = nv >= 0 ? '#' + hex6(nv) : 'transparent';
    renderPreview();
  };
  // Track typing before Back or a captured link can switch the selected app.
  ctl.addEventListener(isBool || ctl.tagName === 'SELECT' ? 'change' : 'input', onChange);
  if (picker) picker.addEventListener('input', () => { ctl.value = picker.value.slice(1).toUpperCase(); onChange(); });
  return row;
}
$('#app-filter').addEventListener('input', renderAppSettings);

// ---------- watch face preview ----------
function previewValues() {
  const v = Object.assign({}, state.appValues);
  for (const [k, c] of state.appPending) v[k] = c.value;
  return v;
}
function setupPreview(app) {
  const box = $('#face-preview');
  const ok = window.EasyFace && EasyFace.supports(app.id);
  box.hidden = !ok;
  if (state.preview.timer) { clearInterval(state.preview.timer); state.preview.timer = null; }
  if (!ok) return;
  state.preview.screen = 1;
  document.querySelectorAll('.seg-btn').forEach((b) => b.classList.toggle('active', b.dataset.screen === '1'));
  renderPreview();
  state.preview.timer = setInterval(() => { if (!box.hidden && state.tab === 'apps') renderPreview(); }, 1000);
}
function renderPreview() {
  const box = $('#face-preview');
  if (box.hidden || !state.app) return;
  const v = previewValues();
  $('#seg-screen2').disabled = !v.secondscreen;
  if (!v.secondscreen && state.preview.screen === 2) { state.preview.screen = 1; document.querySelectorAll('.seg-btn').forEach((b) => b.classList.toggle('active', b.dataset.screen === '1')); }
  const labelFor = (key, id) => { const sc = schemaFor(key); const o = sc && sc.options.find((x) => x.value === String(id)); return o ? o.label : id; };
  const systemIs24h = (state.rows.find((r) => r.field === 'timeMode') || {}).value === 'hour24';
  $('#face').innerHTML = EasyFace.render(v, { screen: state.preview.screen, aod: state.preview.aod, labelFor, systemIs24h });
}
$('#face').addEventListener('click', (ev) => {
  const hit = ev.target.closest('[data-key]');
  if (hit) {
    const key = hit.dataset.key;
    const row = document.querySelector(`#app-groups [data-key="${CSS.escape(key)}"]`);
    if (row) {
      const r = row.closest('.row');
      r.scrollIntoView({ behavior: 'smooth', block: 'center' });
      r.classList.remove('flash'); void r.offsetWidth; r.classList.add('flash');
      row.focus({ preventScroll: true });
      ev.stopPropagation();
      return;
    }
  }
  const v = previewValues();
  if (!v.secondscreen) return;
  state.preview.screen = state.preview.screen === 1 ? 2 : 1;
  document.querySelectorAll('.seg-btn').forEach((b) => b.classList.toggle('active', b.dataset.screen === String(state.preview.screen)));
  renderPreview();
});
document.querySelectorAll('.seg-btn').forEach((b) => b.addEventListener('click', () => {
  state.preview.screen = Number(b.dataset.screen);
  document.querySelectorAll('.seg-btn').forEach((x) => x.classList.toggle('active', x === b));
  renderPreview();
}));
$('#preview-aod').addEventListener('change', () => { state.preview.aod = $('#preview-aod').checked; renderPreview(); });

async function applyAppChanges() {
  if (!state.app) return;
  const list = [...state.appPending.entries()];
  const body = `<ul class="changes">${list.map(([, c]) => `<li><span>${esc(c.label)}</span><span><span class="from">${esc(c.from)}</span><span class="to">${esc(c.to)}</span></span></li>`).join('')}</ul><p class="note">Changes are saved directly to the watch.</p>`;
  if (!await confirmDialog({ title: `Save to ${state.app.name}?`, body, ok: 'Save to watch' })) return;
  const values = Object.fromEntries(list.map(([k, c]) => [k, c.value]));
  try {
    const as = await api('PUT', `/api/apps/${encodeURIComponent(state.app.fileName)}/settings`, { values });
    state.appValues = as.values; state.appItems = as.items || []; state.appPending.clear(); updateActionBar();
    renderAppSettings(); renderPreview();
    toast(`Saved ${list.length} setting${list.length > 1 ? 's' : ''} to ${state.app.name}`);
  } catch (e) { toast(e.message, 'bad'); }
}

// ---------- files ----------
let filesRequest = 0;
async function loadFiles(path, decodePath = '') {
  const request = ++filesRequest;
  state.path = path;
  state.decodePath = decodePath;
  setRoute('files', decodePath || path, decodePath ? new URLSearchParams({ view: 'decode' }) : null);
  loadDecode(decodePath);
  const parts = path ? path.split('/') : [];
  let acc = '';
  const crumbs = [`<a href="#" data-p="">Watch</a>`];
  for (const p of parts) { acc += (acc ? '/' : '') + p; crumbs.push(`<span class="sep">/</span><a href="#" data-p="${esc(acc)}">${esc(p)}</a>`); }
  $('#crumbs').innerHTML = crumbs.join('');
  $('#crumbs').querySelectorAll('a').forEach((a) => a.onclick = (ev) => { ev.preventDefault(); loadFiles(a.dataset.p); });
  const tb = $('#files-table tbody');
  tb.innerHTML = '<tr><td colspan="4" class="muted">Reading…</td></tr>';
  try {
    const entries = await api('GET', '/api/files?path=' + encodeURIComponent(path));
    if (request !== filesRequest) return;
    state.files = { path, entries };
    renderFiles();
  } catch (e) { if (request === filesRequest) tb.innerHTML = `<tr><td colspan="4" class="muted">${esc(e.message)}</td></tr>`; }
}
state.filesSort = { key: 'name', dir: 1 };
document.querySelectorAll('#files-table th.sortable').forEach((th) => th.onclick = () => {
  const k = th.dataset.sort;
  state.filesSort = { key: k, dir: state.filesSort.key === k ? -state.filesSort.dir : (k === 'name' ? 1 : -1) };
  if (state.files) renderFiles();
});
function renderFiles() {
  const { path, entries } = state.files;
  const tb = $('#files-table tbody');
  const { key, dir } = state.filesSort;
  document.querySelectorAll('#files-table th.sortable').forEach((th) => { th.classList.toggle('asc', th.dataset.sort === key && dir === 1); th.classList.toggle('desc', th.dataset.sort === key && dir === -1); });
  entries.sort((a, b) => {
    if (a.dir !== b.dir) return a.dir ? -1 : 1;
    const v = key === 'size' ? (a.size || 0) - (b.size || 0) : key === 'modified' ? String(a.modified || '').localeCompare(String(b.modified || '')) : a.name.localeCompare(b.name);
    return v * dir;
  });
  {
    tb.innerHTML = entries.length ? '' : '<tr><td colspan="4" class="muted">Empty folder.</td></tr>';
    for (const e of entries) {
      const full = (path ? path + '/' : '') + e.name;
      const tr = document.createElement('tr');
      if (e.dir) {
        tr.className = 'dir';
        tr.innerHTML = `<td>${esc(e.name)}/</td><td class="num"></td><td class="muted">${esc((e.modified || '').replace('T', ' '))}</td><td></td>`;
        tr.onclick = () => loadFiles(full);
      } else {
        const isFit = /\.fit$/i.test(e.name);
        const decodeParams = new URLSearchParams({ view: 'decode' });
        tr.innerHTML = `<td>${esc(e.name)}</td><td class="num">${e.size.toLocaleString()}</td><td class="muted">${esc((e.modified || '').replace('T', ' '))}</td><td class="actions"><a href="/api/file?path=${encodeURIComponent(full)}">Download</a>${isFit ? `<a class="decode-link" href="${esc(routePath('files', full, decodeParams))}">Decode</a>` : ''}<button class="btn small danger">Delete</button></td>`;
        if (isFit) tr.querySelector('.decode-link').onclick = (ev) => {
          if (ev.ctrlKey || ev.metaKey || ev.shiftKey || ev.altKey) return;
          ev.preventDefault();
          setRoute('files', full, decodeParams);
          applyRoute();
        };
        tr.querySelector('button').onclick = async () => {
          if (!await confirmDialog({ title: 'Delete from the watch?', body: `<p>${esc(full)} will be removed permanently.</p>`, ok: 'Delete' })) return;
          try { await api('DELETE', '/api/file?path=' + encodeURIComponent(full)); toast('Deleted ' + e.name); } catch (err) { toast(err.message, 'bad'); }
          loadFiles(path);
        };
      }
      tb.appendChild(tr);
    }
  }
}
async function loadDecode(path) {
  const panel = $('#decode-panel'), groups = $('#decode-groups'), status = $('#decode-status');
  panel.hidden = !path;
  if (!path) { state.decodeView = null; groups.replaceChildren(); return; }
  if (state.decodeView && state.decodeView.path === path) return;
  const view = state.decodeView = { path };
  $('#decode-title').textContent = path.split('/').pop();
  $('#decode-download').href = '/api/decode?path=' + encodeURIComponent(path);
  $('#decode-download').download = path.split('/').pop() + '.json';
  $('#decode-close').href = routePath('files', state.path);
  $('#decode-close').onclick = (ev) => { ev.preventDefault(); navigate('files', state.path); };
  status.textContent = 'Decoding FIT…';
  groups.replaceChildren();
  try {
    const messages = await api('GET', '/api/decode?path=' + encodeURIComponent(path)) || [];
    if (state.decodeView !== view) return;
    const byName = new Map();
    for (const message of messages) {
      const name = message.name || `Message ${message.num}`;
      if (!byName.has(name)) byName.set(name, []);
      byName.get(name).push(message);
    }
    status.textContent = `${messages.length.toLocaleString()} messages in ${byName.size} groups. Expand a group to view its values.`;
    for (const [name, records] of byName) {
      const details = el(`<details><summary>${esc(name)} <span class="muted">(${records.length.toLocaleString()})</span></summary></details>`);
      let rendered = false;
      details.addEventListener('toggle', () => {
        if (!details.open || rendered) return;
        rendered = true;
        renderDecodeGroup(details, name, records);
      });
      groups.appendChild(details);
    }
  } catch (e) { if (state.decodeView === view) status.textContent = e.message; }
}
function renderDecodeGroup(details, name, records) {
  const fields = new Map();
  for (const m of records) for (const f of m.fields || []) if (!fields.has(f.num)) fields.set(f.num, f);
  const columns = [...fields.values()];
  const wrapper = el(`<div class="table-wrap tall" tabindex="0" role="region" aria-label="${esc(name)} messages">
    <table class="files decode-table"><caption>${esc(name)} messages</caption><thead><tr><th scope="col">#</th>${columns.map((f) => `<th scope="col">${esc(f.name || 'Field ' + f.num)}${f.units ? ` (${esc(f.units)})` : ''}</th>`).join('')}</tr></thead><tbody></tbody></table>
  </div>`);
  details.appendChild(wrapper);
  // Paginate large activity files to limit DOM size.
  const pageSize = 100;
  let offset = 0;
  const pager = el('<div class="toolbar decode-pager"><button type="button" class="btn small">Previous</button><span role="status"></span><button type="button" class="btn small">Next</button></div>');
  const [previous, next] = pager.querySelectorAll('button');
  function renderPage() {
    wrapper.querySelector('tbody').innerHTML = records.slice(offset, offset + pageSize).map((m, i) => {
      const values = new Map((m.fields || []).map((f) => [f.num, f.value]));
      return `<tr><th scope="row">${m.index === undefined ? offset + i + 1 : m.index + 1}</th>${columns.map((f) => `<td>${esc(values.get(f.num) == null ? '–' : fmt(values.get(f.num)))}</td>`).join('')}</tr>`;
    }).join('');
    previous.disabled = offset === 0;
    next.disabled = offset + pageSize >= records.length;
    pager.querySelector('span').textContent = `${offset + 1}–${Math.min(offset + pageSize, records.length)} of ${records.length.toLocaleString()}`;
    wrapper.scrollTop = 0;
  }
  previous.onclick = () => { offset -= pageSize; renderPage(); };
  next.onclick = () => { offset += pageSize; renderPage(); };
  if (records.length > pageSize) details.appendChild(pager);
  renderPage();
}
$('#upload-file').addEventListener('change', async () => {
  const f = $('#upload-file').files[0];
  if (!f) return;
  const full = (state.path ? state.path + '/' : '') + f.name;
  if (!await confirmDialog({ title: 'Upload to the watch?', body: `<p>${esc(f.name)} (${f.size.toLocaleString()} bytes) will be written to <code>${esc(full)}</code>, replacing any file with that name.</p>`, ok: 'Upload' })) { $('#upload-file').value = ''; return; }
  try { await api('POST', '/api/file?path=' + encodeURIComponent(full), await f.arrayBuffer(), true); toast('Uploaded ' + f.name); } catch (e) { toast(e.message, 'bad'); }
  $('#upload-file').value = '';
  loadFiles(state.path);
});

// ---------- updates ----------
state.updates = { software: null, apps: null, inventory: null, checkedAt: null, busy: false, written: new Set() };
function fmtSize(n) { return n >= 1e9 ? (n / 1e9).toFixed(2) + ' GB' : n >= 1e6 ? (n / 1e6).toFixed(1) + ' MB' : Math.round(n / 1e3) + ' kB'; }
async function loadUpdates() {
  if (state.updates.busy) return;
  if (!state.connected) { toast('Connect the watch first', 'warn'); return; }
  state.updates.busy = true;
  $('#btn-check-updates').disabled = true;
  $('#updates-status').textContent = 'Checking for updates…';
  $('#sw-list').innerHTML = '<li class="muted">Checking Garmin\'s update service…</li>';
  $('#app-updates').innerHTML = '<li class="muted">Checking the Connect IQ store…</li>';
  const [sw, apps, inv] = await Promise.allSettled([api('GET', '/api/updates/software'), api('GET', '/api/updates/apps'), api('GET', '/api/software')]);
  const u = state.updates;
  u.software = sw.status === 'fulfilled' ? sw.value : null; u.softwareError = sw.reason && sw.reason.message;
  u.apps = apps.status === 'fulfilled' ? apps.value : null; u.appsError = apps.reason && apps.reason.message;
  u.inventory = inv.status === 'fulfilled' ? inv.value : null;
  u.checkedAt = new Date(); u.busy = false;
  $('#btn-check-updates').disabled = false;
  renderUpdates();
}
function renderUpdates() {
  const u = state.updates;
  const swAvail = (u.software || []).filter((x) => !x.isProductInstalled && !u.written.has(x.partNumber));
  const appAvail = (u.apps || []).filter((x) => x.updateAvailable);
  const n = swAvail.length + appAvail.length;
  $('#badge-updates').hidden = n === 0; $('#badge-updates').textContent = n;
  $('#updates-status').textContent = `Checked ${u.checkedAt.toLocaleTimeString()} · ` + (n ? `${n} update${n > 1 ? 's' : ''} available` : u.softwareError || u.appsError ? 'some checks failed' : 'no updates found');

  const sw = $('#sw-list'); sw.innerHTML = '';
  if (u.softwareError) sw.innerHTML = `<li class="muted">Could not reach Garmin: ${esc(u.softwareError)}</li>`;
  else if (!u.software.length) sw.innerHTML = '<li class="muted">No software updates found.</li>';
  for (const x of u.software || []) {
    const written = u.written.has(x.partNumber);
    const tag = written ? '<span class="tag new">Written · unplug to install</span>' : x.isProductInstalled ? '<span class="tag ok">Installed</span>' : `<span class="tag new">Update</span>`;
    const kind = x.isPrimaryFirmware ? 'Watch firmware' : x.dataType || 'Data';
    const sev = x.severity && !/^(unspecified|low|normal|optional)$/i.test(x.severity) ? `<span class="tag${/mandatory|critical|high/i.test(x.severity) ? ' crit' : ''}">${esc(x.severity)}</span>` : '';
    const notes = (x.releaseNotes || []).filter(Boolean);
    const li = el(`<li><div><div class="what"><b>${esc(x.productName)}</b><span class="ver">${esc(x.version)}</span>${tag}${sev}</div>
      <div class="meta">${esc(kind)} · ${fmtSize(x.sizeBytes)} · <span class="mono">${esc(x.installPath)}/${esc(x.fileName)}</span>${x.isRestartRequired ? ' · restarts the watch' : ''}</div>
      ${notes.length ? `<details><summary>What changed</summary><ul>${notes.slice(0, 20).map((l) => `<li>${esc(l)}</li>`).join('')}</ul></details>` : ''}</div>
      <div class="side-btns"></div></li>`);
    const btns = li.querySelector('.side-btns');
    if (!written) {
      const b = el(`<button class="btn small${x.isProductInstalled ? '' : ' primary'}">${x.isProductInstalled ? 'Reinstall' : 'Install'}</button>`);
      b.onclick = () => installUpdate(x);
      btns.appendChild(b);
    }
    sw.appendChild(li);
  }

  const al = $('#app-updates'); al.innerHTML = '';
  if (u.appsError) al.innerHTML = `<li class="muted">${esc(u.appsError)}</li>`;
  else if (!(u.apps || []).length) al.innerHTML = '<li class="muted">No store apps installed.</li>';
  for (const a of u.apps || []) {
    const tag = a.error ? `<span class="tag">${esc(a.error)}</span>` : a.updateAvailable ? '<span class="tag new">Update available</span>' : '<span class="tag ok">Up to date</span>';
    const ver = a.updateAvailable ? `v${a.installedVersion} → v${a.latestVersion}${a.latestName ? ' (' + esc(a.latestName) + ')' : ''}` : `v${a.installedVersion}`;
    al.appendChild(el(`<li><div><div class="what"><b>${esc(a.app.name)}</b><span class="ver">${ver}</span>${tag}</div><div class="meta">${esc(a.app.typeName)}</div></div>
      <div class="side-btns"><a class="btn small" href="${esc(a.storeUrl)}" target="_blank" rel="noopener">Store ↗</a></div></li>`));
  }

  const inv = u.inventory || [];
  const part = (p) => `<div class="part"><span class="mono">${esc(p.partNumber)}</span><b>${p.version.toFixed(2)}</b></div>`;
  const head = inv.slice(0, 12), rest = inv.slice(12);
  $('#sw-inventory').innerHTML = head.length ? head.map(part).join('') : '<span class="muted">Nothing reported.</span>';
  $('#sw-inventory-more').hidden = !rest.length;
  $('#sw-inventory-more').querySelector('summary').textContent = `${rest.length} more components`;
  $('#sw-inventory-rest').innerHTML = rest.map(part).join('');
  markAppUpdates();
}
function markAppUpdates() {
  const byFile = new Map((state.updates.apps || []).map((a) => [a.app.fileName, a]));
  document.querySelectorAll('.app-item').forEach((b) => {
    const a = byFile.get(b.dataset.file);
    const old = b.querySelector('.tag'); if (old) old.remove();
    if (a && a.updateAvailable) b.querySelector('.app-sub').appendChild(el(`<span class="tag new">v${a.latestVersion} in store</span>`));
  });
}
async function installUpdate(x) {
  const notes = (x.releaseNotes || []).filter(Boolean).slice(0, 15);
  const restart = x.isRestartRequired || x.isPrimaryFirmware;
  const ok = await confirmDialog({
    title: `${x.isProductInstalled ? 'Reinstall' : 'Install'} ${x.productName} ${x.version}?`,
    body: `<p>Download ${fmtSize(x.sizeBytes)} from Garmin. Unplug the watch to install the update.${restart ? ' The watch will restart. Keep it charged and let the restart finish.' : ''}</p>${notes.length ? `<ul class="notes">${notes.map((l) => `<li>${esc(l)}</li>`).join('')}</ul>` : ''}`,
    ok: 'Download and install',
  });
  if (!ok) return;
  toast(`Downloading ${x.productName}…`);
  try {
    const res = await api('POST', '/api/updates/software/install', { partNumber: x.partNumber });
    state.updates.written.add(x.partNumber);
    toast(res.note);
    renderUpdates();
    loadNewFiles();
  } catch (e) { toast(e.message, 'bad'); }
}
$('#btn-check-updates').onclick = loadUpdates;
$('#prg-file').addEventListener('change', async () => {
  const f = $('#prg-file').files[0];
  if (!f) return;
  const ok = await confirmDialog({ title: 'Install app on the watch?', body: `<p>Copy ${esc(f.name)} (${fmtSize(f.size)}) to the watch. Unplug it to finish installation.</p>`, ok: 'Install' });
  if (ok) {
    try { const res = await api('POST', '/api/apps/install', await f.arrayBuffer(), true); toast(res.note); state.apps = []; loadNewFiles(); } catch (e) { toast(e.message, 'bad'); }
  }
  $('#prg-file').value = '';
});

// ---------- health & activities ----------
state.health = { days: 14, daily: null, acts: null, selDay: null, selAct: null, loadedDays: 0 };
function svgChart(w, h, inner) { return `<svg viewBox="0 0 ${w} ${h}" preserveAspectRatio="none">${inner}</svg>`; }
function barChart(rows, key, opts) {
  const W = 400, H = 120, pad = 18, n = rows.length, bw = (W - pad) / n;
  const max = Math.max(opts.max || 0, ...rows.map((r) => r[key] || 0)) || 1;
  let g = '';
  rows.forEach((r, i) => {
    const v = r[key] || 0, bh = v / max * (H - pad - 4);
    g += `<rect class="bar${state.health.selDay === r.date ? ' sel' : ''}" data-date="${r.date}" x="${(pad + i * bw + 1).toFixed(1)}" y="${(H - pad - bh).toFixed(1)}" width="${Math.max(1, bw - 2).toFixed(1)}" height="${bh.toFixed(1)}"><title>${r.date}: ${v}${opts.unit || ''}</title></rect>`;
    if (i % Math.ceil(n / 7) === 0) g += `<text class="lbl" x="${(pad + i * bw + bw / 2).toFixed(1)}" y="${H - 5}" text-anchor="middle">${r.date.slice(5)}</text>`;
  });
  if (opts.goal) { const y = H - pad - opts.goal / max * (H - pad - 4); g += `<line class="goal" x1="${pad}" x2="${W}" y1="${y.toFixed(1)}" y2="${y.toFixed(1)}"/>`; }
  g += `<text class="lbl" x="2" y="10">${max}${opts.unit || ''}</text>`;
  return svgChart(W, H, g);
}
function lineChart(rows, keys, opts) {
  const W = 400, H = 120, pad = 18, n = rows.length;
  const vals = rows.flatMap((r) => keys.map((k) => r[k] || 0)).filter((v) => v > 0);
  const max = Math.max(...vals, 1), min = opts.zero ? 0 : Math.min(...vals);
  const x = (i) => pad + i * (W - pad) / Math.max(1, n - 1), y = (v) => H - pad - (v - min) / Math.max(1, max - min) * (H - pad - 8);
  let g = '';
  keys.forEach((k, ki) => {
    const pts = rows.map((r, i) => r[k] ? `${x(i).toFixed(1)},${y(r[k]).toFixed(1)}` : null).filter(Boolean);
    g += `<polyline class="line${ki ? ' teal' : ''}" points="${pts.join(' ')}"/>`;
  });
  rows.forEach((r, i) => { if (i % Math.ceil(n / 7) === 0) g += `<text class="lbl" x="${x(i).toFixed(1)}" y="${H - 5}" text-anchor="middle">${r.date.slice(5)}</text>`; });
  g += `<text class="lbl" x="2" y="10">${max}${opts.unit || ''}</text><text class="lbl" x="2" y="${H - pad}">${min}</text>`;
  return svgChart(W, H, g);
}
function seriesChart(series, opts) {
  if (!series || !series.length) return '<p class="muted">No data.</p>';
  const W = 800, H = 120, pad = 18;
  const t0 = series[0][0], t1 = series[series.length - 1][0] || t0 + 1;
  const vals = series.map((p) => p[1]).filter((v) => v >= 0);
  const max = opts.max || Math.max(...vals, 1), min = opts.zero ? 0 : Math.min(...vals);
  const x = (t) => pad + (t - t0) / Math.max(1, t1 - t0) * (W - pad), y = (v) => H - pad - (v - min) / Math.max(1, max - min) * (H - pad - 8);
  const pts = series.filter((p) => p[1] >= 0).map((p) => `${x(p[0]).toFixed(1)},${y(p[1]).toFixed(1)}`);
  let g = opts.area ? `<polygon class="area" points="${x(series[0][0]).toFixed(1)},${H - pad} ${pts.join(' ')} ${x(series[series.length - 1][0]).toFixed(1)},${H - pad}"/>` : '';
  g += `<polyline class="line${opts.teal ? ' teal' : ''}" points="${pts.join(' ')}"/>`;
  for (let h = 0; h <= 24; h += 6) { const t = new Date(t0); t.setHours(h, 0, 0, 0); if (t >= t0 && t <= t1) g += `<text class="lbl" x="${x(t.getTime()).toFixed(1)}" y="${H - 5}" text-anchor="middle">${h}:00</text>`; }
  g += `<text class="lbl" x="2" y="10">${max}</text><text class="lbl" x="2" y="${H - pad}">${min}</text>`;
  return svgChart(W, H, g);
}
async function loadHealth() {
  if (!state.connected) return;
  const days = Number($('#health-days').value) || 14;
  if (!state.health.daily || state.health.loadedDays !== days) {
    $('#health-status').textContent = 'Loading from Garmin Connect…';
    try { state.health.daily = await api('GET', '/api/health/daily?days=' + days); state.health.loadedDays = days; $('#health-status').textContent = days > 14 ? 'Calories are unavailable for ranges over 14 days.' : ''; loadExtras(); }
    catch (e) { $('#health-charts').innerHTML = `<p class="muted">${esc(e.message)}${/signed in/.test(e.message) ? ' (sign in on the Garmin Connect tab)' : ''}</p>`; $('#health-status').textContent = ''; }
  }
  if (state.health.daily) renderDaily();
  loadActivities();
}
function renderDaily() {
  const rows = state.health.daily.filter((r) => !r.error);
  if (!rows.length) { $('#health-charts').innerHTML = '<p class="muted">No health data available for these days.</p>'; return; }
  const goal = rows[rows.length - 1].stepGoal;
  const last = rows[rows.length - 1];
  $('#health-charts').innerHTML = `
    <div class="chart"><h3>Steps · today ${last.steps.toLocaleString()} of ${goal.toLocaleString()}</h3>${barChart(rows, 'steps', { goal, max: goal })}</div>
    <div class="chart"><h3>Resting heart rate · ${last.restingHr} bpm</h3>${lineChart(rows, ['restingHr'], { unit: '' })}</div>
    <div class="chart"><h3>Sleep · ${fmtDur(last.sleepSec)} last night</h3>${barChart(rows.map((r) => ({ date: r.date, h: Math.round(r.sleepSec / 360) / 10 })), 'h', { unit: 'h', max: 9 })}</div>
    <div class="chart"><h3>Stress (avg) · ${last.avgStress}</h3>${barChart(rows, 'avgStress', { max: 100 })}</div>
    <div class="chart"><h3>Body battery · ${last.bodyBatteryLow}–${last.bodyBatteryHigh}</h3>${lineChart(rows, ['bodyBatteryHigh', 'bodyBatteryLow'], { zero: true })}</div>
    ${rows.some((r) => r.calories) ? `<div class="chart"><h3>Calories · ${last.calories} kcal</h3>${barChart(rows, 'calories', {})}</div>` : ''}`;
  document.querySelectorAll('#health-charts .bar').forEach((b) => { b.onclick = () => showDay(b.dataset.date); });
}
async function loadExtras() {
  if (state.health.extras) return;
  $('#extras-status').textContent = 'Loading from Garmin Connect…';
  try { state.health.extras = await api('GET', '/api/health/extras'); $('#extras-status').textContent = ''; renderExtras(); }
  catch (e) { $('#extras-status').textContent = e.message; $('#extras-tiles').innerHTML = ''; }
}
function renderExtras() {
  const x = state.health.extras;
  const tile = (k, v, s) => `<div class="tile"><div class="k">${k}</div><div class="v">${v}</div><div class="s">${s}</div></div>`;
  $('#extras-tiles').innerHTML = (x.vo2max ? tile('VO2 max', x.vo2max.toFixed(1), `as of ${esc(x.vo2maxDate)}`) : '')
    + (x.fitnessAge ? tile('Fitness age', Math.round(x.fitnessAge), `you are ${x.chronologicalAge}`) : '')
    + (x.hrv ? tile('HRV last night', `${x.hrv.lastNight}<small>ms</small>`, `weekly ${x.hrv.weeklyAvg} ms · ${esc(humanize(x.hrv.status.toLowerCase()))}`) : '')
    + (x.badges.length ? tile('Badges', x.badges.length, `${x.badges.reduce((a, b) => a + b.points, 0)} points`) : '');
  const tb = $('#records-table tbody'); tb.innerHTML = '';
  for (const r of x.records) {
    const tr = el(`<tr${r.activityId ? ' class="act"' : ''}><td>${esc(r.label)}</td><td class="num"><b>${esc(r.display)}</b></td><td>${esc(r.date)}</td></tr>`);
    if (r.activityId) tr.onclick = () => window.open(`https://connect.garmin.com/modern/activity/${r.activityId}`, 'garmin-connect');
    tb.appendChild(tr);
  }
  if (!x.records.length) tb.innerHTML = '<tr><td colspan="3" class="muted">No personal records yet.</td></tr>';
  const wl = $('#weight-list'); wl.innerHTML = x.weight.length ? '' : '<li class="muted">No weigh-ins in the last year. Log your weight above.</li>';
  for (const w of x.weight.slice(-8).reverse()) wl.appendChild(el(`<li><span><span class="what">${w.kg.toFixed(1)} kg</span>${w.bmi ? ` <span class="meta">BMI ${w.bmi.toFixed(1)}</span>` : ''}</span><span class="meta">${esc(w.date)}</span></li>`));
  const bl = $('#badges-list'); bl.innerHTML = x.badges.length ? '' : '<li class="muted">No badges yet.</li>';
  for (const b of x.badges.slice(0, 8)) bl.appendChild(el(`<li><span><span class="what">${esc(b.name)}</span>${b.count > 1 ? ` <span class="tag">×${b.count}</span>` : ''}</span><span class="meta">${esc(b.date)} · ${b.points} pt</span></li>`));
  for (const e of x.errors || []) toast(e, 'warn');
}
$('#music-q').addEventListener('input', () => { if (state.music.lib) renderMusic(); });
$('#weight-form').addEventListener('submit', async (ev) => {
  ev.preventDefault();
  const kg = Number($('#weight-kg').value);
  const ok = await confirmDialog({ title: `Log ${kg.toFixed(1)} kg in Garmin Connect?`, body: '<p>Adds a manual weigh-in with the current date and time.</p>', ok: 'Log weight' });
  if (!ok) return;
  try {
    await api('POST', '/api/health/weight', { kg });
    toast(`Logged ${kg.toFixed(1)} kg`);
    $('#weight-kg').value = ''; state.health.extras = null; loadExtras();
  } catch (e) { toast(e.message, 'bad'); }
});
function fmtDur(sec) { sec = Math.round(sec || 0); const h = Math.floor(sec / 3600), m = Math.floor(sec % 3600 / 60), s = sec % 60; return h ? `${h}:${pad2(m)}:${pad2(s)}` : `${m}:${pad2(s)}`; }
function pad2(n) { return String(n).padStart(2, '0'); }
function fmtHM(sec) { const h = Math.floor(sec / 3600), m = Math.round(sec % 3600 / 60); return h ? `${h}h ${pad2(m)}m` : `${m}m`; }
async function showDay(date) {
  state.health.selDay = date; renderDaily();
  const box = $('#day-detail'); box.hidden = false; box.innerHTML = `<p class="muted">Loading ${date}…</p>`;
  try {
    const d = await api('GET', '/api/health/day?date=' + date);
    const sl = d.sleep ? `<div class="chart"><h3>Sleep · ${fmtDur(d.sleep.deepSec + d.sleep.lightSec + d.sleep.remSec)} · score ${d.sleep.score} ${esc(d.sleep.quality || '')}</h3><svg viewBox="0 0 800 40" preserveAspectRatio="none">${(() => { const lv = d.sleep.levels || []; if (!lv.length) return ''; const t0 = lv[0][0], t1 = d.sleep.endMs; return lv.map((p, i) => { const e = i + 1 < lv.length ? lv[i + 1][0] : t1; const cls = ['sleep-deep', 'sleep-light', 'sleep-rem', 'sleep-awake'][Math.min(3, Math.max(0, Math.round(p[1])))]; return `<rect class="${cls}" x="${((p[0] - t0) / (t1 - t0) * 800).toFixed(1)}" y="${[10, 6, 2, 0][Math.min(3, Math.round(p[1]))]}" width="${((e - p[0]) / (t1 - t0) * 800).toFixed(1)}" height="${40 - [10, 6, 2, 0][Math.min(3, Math.round(p[1]))]}"/>`; }).join(''); })()}</svg><div class="hint">deep ${fmtDur(d.sleep.deepSec)} · light ${fmtDur(d.sleep.lightSec)} · REM ${fmtDur(d.sleep.remSec)} · awake ${fmtDur(d.sleep.awakeSec)}</div></div>` : '';
    box.innerHTML = `<h2>${date}</h2><div class="charts">
      <div class="chart"><h3>Heart rate</h3>${seriesChart(d.heartRate, { zero: false })}</div>
      <div class="chart"><h3>Stress</h3>${seriesChart(d.stress, { max: 100, zero: true, area: true, teal: true })}</div>
      <div class="chart"><h3>Body battery</h3>${seriesChart(d.bodyBattery, { max: 100, zero: true, teal: true })}</div>${sl}</div>${d.errors && d.errors.length ? `<p class="hint">${esc(d.errors.join(' · '))}</p>` : ''}`;
  } catch (e) { box.innerHTML = `<p class="muted">${esc(e.message)}</p>`; }
}
$('#health-days').value = prefs.get('healthDays', '14');
$('#health-days').addEventListener('change', () => { prefs.set('healthDays', $('#health-days').value); state.health.daily = null; loadHealth(); });
let actsTimer = null;
async function loadActivities() {
  clearTimeout(actsTimer);
  try {
    const list = await api('GET', '/api/health/activities');
    state.health.acts = list;
    renderActivities();
    if (state.health.selAct && $('#act-detail').hidden) showActivity(state.health.selAct);
    if (list.pending > 0) { $('#acts-status').textContent = `Decoding ${list.pending} of ${list.total} files from the watch…`; actsTimer = setTimeout(loadActivities, 3000); }
    else $('#acts-status').textContent = `${list.total} activities`;
  } catch (e) { $('#acts-table tbody').innerHTML = `<tr><td colspan="7" class="muted">${esc(e.message)}</td></tr>`; }
}
function pace(spd) { if (!spd) return ''; const s = 1000 / spd; return `${Math.floor(s / 60)}:${pad2(Math.round(s % 60))} /km`; }
state.health.actsShown = 40;
state.health.sport = 'all'; state.health.actsSort = prefs.get('actsSort', 'date'); $('#acts-sort').value = state.health.actsSort;
function renderActivities() {
  const tb = $('#acts-table tbody'); tb.innerHTML = '';
  const everything = state.health.acts.activities;
  const counts = {};
  for (const a of everything) counts[a.sport] = (counts[a.sport] || 0) + 1;
  const chips = $('#acts-sports'); chips.innerHTML = '';
  if (!counts[state.health.sport] && state.health.sport !== 'all') state.health.sport = 'all';
  for (const sp of ['all', ...Object.keys(counts).sort((a, b) => counts[b] - counts[a])]) {
    const c = el(`<button class="chip${state.health.sport === sp ? ' active' : ''}" type="button">${sp === 'all' ? 'All' : esc(humanize(sp))} <span class="muted">${sp === 'all' ? everything.length : counts[sp]}</span></button>`);
    c.onclick = () => { state.health.sport = sp; state.health.actsShown = 40; renderActivities(); };
    chips.appendChild(c);
  }
  const all = everything.filter((a) => state.health.sport === 'all' || a.sport === state.health.sport);
  const key = state.health.actsSort;
  const num = (v) => Number(v) || 0;
  all.sort((a, b) => key === 'date-asc' ? new Date(a.start) - new Date(b.start) : key === 'distance' ? num(b.distanceM) - num(a.distanceM) : key === 'time' ? num(b.timerSec) - num(a.timerSec) : key === 'hr' ? num(b.avgHr) - num(a.avgHr) : key === 'kcal' ? num(b.calories) - num(a.calories) : new Date(b.start) - new Date(a.start));
  for (const a of all.slice(0, state.health.actsShown)) {
    const tr = el(`<tr class="act${state.health.selAct === a.file ? ' sel' : ''}"><td>${esc(new Date(a.start).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' }))}</td><td>${esc(humanize(a.sport))}${a.subSport ? ' · ' + esc(humanize(a.subSport)) : ''}</td><td class="num">${a.distanceM ? (a.distanceM / 1000).toFixed(2) + ' km' : ''}</td><td class="num">${fmtDur(a.timerSec)}</td><td class="num">${a.sport === 'running' || a.sport === 'walking' || a.sport === 'hiking' ? pace(a.avgSpeedMs) : a.avgSpeedMs ? (a.avgSpeedMs * 3.6).toFixed(1) + ' km/h' : ''}</td><td class="num">${a.avgHr || ''}</td><td class="num">${a.calories || ''}</td></tr>`);
    tr.onclick = () => showActivity(a.file);
    tb.appendChild(tr);
  }
  if (!all.length) tb.innerHTML = '<tr><td colspan="7" class="muted">No activities available in this view.</td></tr>';
  if (all.length > state.health.actsShown) {
    const tr = el(`<tr><td colspan="7"><button class="btn small">Show ${Math.min(60, all.length - state.health.actsShown)} more of ${all.length}</button></td></tr>`);
    tr.querySelector('button').onclick = () => { state.health.actsShown += 60; renderActivities(); };
    tb.appendChild(tr);
  }
}
$('#acts-sort').addEventListener('change', () => { state.health.actsSort = $('#acts-sort').value; prefs.set('actsSort', state.health.actsSort); if (state.health.acts) renderActivities(); });
async function showActivity(file) {
  state.health.selAct = file; if (state.health.acts) renderActivities();
  setRoute('health', file);
  const box = $('#act-detail'); box.hidden = false; box.innerHTML = '<p class="muted">Reading the activity from the watch…</p>';
  try {
    const d = await api('GET', '/api/health/activities/' + encodeURIComponent(file));
    const gps = d.points.filter((p) => p.lat && p.lon);
    const hr = d.points.filter((p) => p.hr).map((p) => [p.t * 1000, p.hr]);
    const alt = d.points.filter((p) => p.alt !== undefined).map((p) => [p.t * 1000, Math.round(p.alt)]);
    const spd = d.points.filter((p) => p.spd > 0).map((p) => [p.t * 1000, Math.round(p.spd * 36) / 10]);
    const cad = d.points.filter((p) => p.cad > 0).map((p) => [p.t * 1000, p.cad]);
    const pace = (ms) => ms > 0 ? `${Math.floor(1000 / ms / 60)}:${pad2(Math.round(1000 / ms % 60))} /km` : '';
    const avgSpd = spd.length ? spd.reduce((a, p) => a + p[1], 0) / spd.length : 0;
    box.innerHTML = `<h2>${esc(humanize(d.sport))} · ${esc(new Date(d.start).toLocaleString())}</h2>
      <dl class="facts"><dt>Distance</dt><dd>${(d.distanceM / 1000).toFixed(2)} km</dd><dt>Time</dt><dd>${fmtDur(d.timerSec)} (${fmtDur(d.elapsedSec)} elapsed)</dd><dt>Heart rate</dt><dd>${d.avgHr} avg · ${d.maxHr} max</dd><dt>Calories</dt><dd>${d.calories}</dd><dt>Ascent</dt><dd>${d.ascentM} m</dd></dl>
      ${gps.length ? '<div class="map" id="act-map"></div>' : ''}
      ${hr.length ? `<div class="chart"><h3>Heart rate</h3>${seriesChartT(hr)}</div>` : ''}
      ${alt.length && d.ascentM ? `<div class="chart"><h3>Elevation</h3>${seriesChartT(alt, true)}</div>` : ''}
      ${spd.length > 5 ? `<div class="chart"><h3>Speed · avg ${avgSpd.toFixed(1)} km/h${/run|walk|hik/i.test(d.sport) ? ` (${pace(avgSpd / 3.6)})` : ''}</h3>${seriesChartT(spd)}</div>` : ''}
      ${cad.length > 5 ? `<div class="chart"><h3>Cadence</h3>${seriesChartT(cad)}</div>` : ''}
      ${d.laps && d.laps.length > 1 ? `<table class="files"><thead><tr><th>Lap</th><th class="num">Time</th><th class="num">Distance</th><th class="num">HR</th></tr></thead><tbody>${d.laps.map((l, i) => `<tr><td>${i + 1}</td><td class="num">${fmtDur(l.elapsedSec)}</td><td class="num">${(l.distanceM / 1000).toFixed(2)} km</td><td class="num">${l.avgHr || ''}</td></tr>`).join('')}</tbody></table>` : ''}`;
    if (gps.length) drawMap(gps);
  } catch (e) { box.innerHTML = `<p class="muted">${esc(e.message)}</p>`; }
}
function seriesChartT(series, area) {
  const W = 800, H = 110, pad = 18, t1 = series[series.length - 1][0] || 1;
  const vals = series.map((p) => p[1]), max = Math.max(...vals), min = Math.min(...vals);
  const x = (t) => pad + t / t1 * (W - pad), y = (v) => H - pad - (v - min) / Math.max(1, max - min) * (H - pad - 8);
  const pts = series.map((p) => `${x(p[0]).toFixed(1)},${y(p[1]).toFixed(1)}`);
  let g = area ? `<polygon class="area" points="${x(0).toFixed(1)},${H - pad} ${pts.join(' ')} ${x(t1).toFixed(1)},${H - pad}"/>` : '';
  g += `<polyline class="line${area ? ' teal' : ''}" points="${pts.join(' ')}"/>`;
  for (let m = 0; m * 60000 <= t1; m += Math.max(5, Math.ceil(t1 / 60000 / 6 / 5) * 5)) g += `<text class="lbl" x="${x(m * 60000).toFixed(1)}" y="${H - 5}" text-anchor="middle">${m}'</text>`;
  g += `<text class="lbl" x="2" y="10">${max}</text><text class="lbl" x="2" y="${H - pad}">${min}</text>`;
  return svgChart(W, H, g);
}
function drawMap(gps) {
  const box = $('#act-map');
  if (window.L) {
    const map = L.map(box, { zoomControl: false, attributionControl: true });
    L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', { maxZoom: 18, attribution: '© OpenStreetMap' }).addTo(map);
    const line = L.polyline(gps.map((p) => [p.lat, p.lon]), { color: '#ff7a2a', weight: 3 }).addTo(map);
    map.fitBounds(line.getBounds(), { padding: [12, 12] });
    return;
  }
  const lats = gps.map((p) => p.lat), lons = gps.map((p) => p.lon);
  const minLat = Math.min(...lats), maxLat = Math.max(...lats), minLon = Math.min(...lons), maxLon = Math.max(...lons);
  const k = Math.cos((minLat + maxLat) / 2 * Math.PI / 180);
  const w = (maxLon - minLon) * k || 1e-6, h = (maxLat - minLat) || 1e-6, sc = Math.min(380 / w, 260 / h);
  const pts = gps.map((p) => `${(10 + (p.lon - minLon) * k * sc).toFixed(1)},${(10 + (maxLat - p.lat) * sc).toFixed(1)}`);
  box.innerHTML = `<svg viewBox="0 0 400 280"><polyline points="${pts.join(' ')}" fill="none" stroke="#ff7a2a" stroke-width="2.5" stroke-linejoin="round"/></svg>`;
}

// ---------- workouts ----------
async function loadWorkouts() {
  if (!state.connected) return;
  previewWorkout();
  const ul = $('#wk-list');
  try {
    const list = await api('GET', '/api/workouts');
    ul.innerHTML = list.length ? '' : '<li class="muted">No workouts in Garmin Connect yet.</li>';
    for (const w of list) {
      const li = el(`<li><div><div class="what">${esc(w.name)}</div><div class="meta">${esc(w.sport)} · ${fmtDur(w.estimatedSec)}${w.updated ? ' · ' + esc(w.updated) : ''}</div></div><div class="side-btns"><button class="btn small primary">Send to watch</button><button class="btn small danger">Delete</button></div></li>`);
      const [send, del] = li.querySelectorAll('button');
      send.onclick = async () => { send.disabled = true; try { const r = await api('POST', `/api/workouts/${w.id}/send?name=${encodeURIComponent(w.name)}`); toast(r.pulled && r.pulled.length ? 'Written to the watch; unplug it to see the workout' : 'Queued for the watch; sync later', r.pulled && r.pulled.length ? '' : 'warn'); loadNewFiles(); } catch (e) { toast(e.message, 'bad'); } finally { send.disabled = false; } };
      del.onclick = async () => { if (!await confirmDialog({ title: `Delete “${w.name}” from Garmin Connect?`, body: '<p>The workout stays on the watch if it was sent already.</p>', ok: 'Delete' })) return; try { await api('DELETE', '/api/workouts/' + w.id); loadWorkouts(); } catch (e) { toast(e.message, 'bad'); } };
      ul.appendChild(li);
    }
  } catch (e) { ul.innerHTML = `<li class="muted">${esc(e.message)}</li>`; }
}
function wkSpec() { try { return JSON.parse($('#wk-spec').value); } catch (e) { toast('Check the workout JSON: ' + e.message, 'bad'); return null; } }
let wkTimer = null;
$('#wk-spec').addEventListener('input', () => { clearTimeout(wkTimer); wkTimer = setTimeout(previewWorkout, 500); });
async function previewWorkout() {
  let spec; try { spec = JSON.parse($('#wk-spec').value); } catch (e) { $('#wk-preview').textContent = 'Complete the JSON to preview the workout.'; return; }
  try { const r = await api('POST', '/api/workouts/preview', { spec }); $('#wk-preview').textContent = describeSteps(r.spec.steps, 0) + `\n≈ ${fmtDur(r.connect.estimatedDurationInSecs)} total`; }
  catch (e) { $('#wk-preview').textContent = e.message; }
}
function describeSteps(steps, depth) {
  return steps.map((s) => s.repeat ? `${'  '.repeat(depth)}${s.repeat}×\n${describeSteps(s.steps, depth + 1)}` : `${'  '.repeat(depth)}${s.type || 'interval'}: ${s.duration || s.distance || (s.calories ? s.calories + ' kcal' : 'lap button')}${s.target ? ' @ ' + s.target : ''}`).join('\n');
}
async function createWorkout(local) {
  const spec = wkSpec(); if (!spec) return;
  $('#wk-status').textContent = local ? 'Writing to the watch…' : 'Saving in Garmin Connect…';
  try {
    const r = await api('POST', '/api/workouts', { spec, send: !local, local });
    $('#wk-status').textContent = ''; toast(r.note, /failed|queued/.test(r.note) ? 'warn' : '');
    loadWorkouts(); loadNewFiles();
  } catch (e) { $('#wk-status').textContent = ''; toast(e.message, 'bad'); }
}
$('#wk-create').onclick = () => createWorkout(false);
$('#wk-local').onclick = () => createWorkout(true);

// ---------- music ----------
state.music = { lib: null, sel: new Set() };
// ---------- apps & glances (the watch's app registry, OUT.BIN) ----------
const kindLabel = { activity: 'Activity', glance: 'Glance', app: 'App', watchface: 'Watch face', widget: 'Widget', datafield: 'Data field', music: 'Music provider', audio: 'Audio' };
const kindPlural = { activity: 'Activities', glance: 'Glances', app: 'Apps', watchface: 'Watch faces', widget: 'Widgets', datafield: 'Data fields', music: 'Music', audio: 'Audio' };
const kindOrder = Object.keys(kindLabel);
async function loadInstalled() {
  const tb = $('#inst-table tbody');
  if (!state.apps.length) tb.innerHTML = '<tr><td colspan="6" class="muted">Loading installed apps…</td></tr>';
  try { await fetchApps(); state.installed.pending = await api('GET', '/api/apps/pending').catch(() => []); renderInstalled(); }
  catch (e) { tb.innerHTML = `<tr><td colspan="6" class="muted">${esc(e.message)}</td></tr>`; }
}
function renderInstalled() {
  const apps = state.apps, q = $('#inst-filter').value.trim().toLowerCase();
  const counts = {};
  for (const a of apps) counts[a.typeName] = (counts[a.typeName] || 0) + 1;
  const chips = $('#inst-types'); chips.innerHTML = '';
  const kinds = ['all', ...kindOrder.filter((k) => counts[k])];
  if (!counts[state.installed.type] && state.installed.type !== 'all') state.installed.type = 'all';
  for (const k of kinds) {
    const c = el(`<button class="chip${state.installed.type === k ? ' active' : ''}" type="button">${k === 'all' ? 'All' : kindPlural[k]} <span class="muted">${k === 'all' ? apps.length : counts[k]}</span></button>`);
    c.onclick = () => { state.installed.type = k; renderInstalled(); };
    chips.appendChild(c);
  }
  const pending = new Map((state.installed.pending || []).map((c) => [c.id + '/' + c.typeName, c.hidden]));
  const vis = state.installed.visibility;
  const rows = apps.filter((a) => (state.installed.type === 'all' || a.typeName === state.installed.type) && (!q || `${a.name} ${a.typeName} ${a.fileName || ''}`.toLowerCase().includes(q)))
    .filter((a) => {
      const p = pending.get(a.id + '/' + a.typeName);
      const hiddenNow = p !== undefined ? p : a.hidden;
      return vis === 'all' || (vis === 'pending' ? p !== undefined : vis === 'hidden' ? hiddenNow : !hiddenNow);
    });
  const byKind = (a, b) => kindOrder.indexOf(a.typeName) - kindOrder.indexOf(b.typeName);
  if (state.installed.sort === 'name') rows.sort((a, b) => a.name.localeCompare(b.name) || byKind(a, b));
  else if (state.installed.sort === 'source') rows.sort((a, b) => Number(a.native) - Number(b.native) || byKind(a, b));
  else rows.sort(byKind); // stable: keeps the watch's own order within a kind
  const tb = $('#inst-table tbody'); tb.innerHTML = '';
  for (const a of rows) {
    const listed = ['activity', 'glance', 'app', 'widget', 'datafield'].includes(a.typeName);
    const pend = pending.get(a.id + '/' + a.typeName);
    const stateCell = !listed ? '' : pend !== undefined ? `<span class="tag new" style="margin:0" title="Applied when the watch is unplugged">${pend ? 'Hidden' : 'Shown'} on unplug</span>` : a.hidden ? '<span class="tag" style="margin:0">Hidden</span>' : '<span class="tag ok" style="margin:0">Shown</span>';
    const tr = el(`<tr><td><b>${esc(a.name)}</b></td><td>${esc(kindLabel[a.typeName] || a.typeName)}</td><td>${a.native ? '<span class="muted">Built-in</span>' : `Connect IQ <span class="muted">v${a.version}</span>`}</td><td>${stateCell}</td><td class="num">${a.size ? fmtSize(a.size) : ''}</td><td class="actions"></td></tr>`);
    const act = tr.querySelector('.actions');
    if (listed) {
      const target = pend !== undefined ? !pend : !a.hidden; // what the button will set
      const b = el(`<button class="btn small" type="button">${target ? 'Hide' : 'Show'}</button>`);
      b.onclick = () => setAppHidden(a, target);
      act.appendChild(b);
    }
    if (a.fileName) {
      const b = el('<button class="btn small" type="button">Settings</button>');
      b.onclick = () => navigate('apps', a.name);
      act.appendChild(b);
    }
    if (!a.native && a.id) act.appendChild(el(`<a class="btn small" href="https://apps.garmin.com/apps/${esc(a.id)}" target="garmin-store" rel="noopener">Store ↗</a>`));
    if (!a.native && a.fileName && !/^toystore$/i.test(a.fileName)) {
      const rm = el('<button class="btn small danger" type="button">Remove</button>');
      rm.onclick = () => removeApp(a);
      act.appendChild(rm);
    }
    tb.appendChild(tr);
  }
  if (!rows.length) tb.innerHTML = '<tr><td colspan="6" class="muted">No apps match these filters.</td></tr>';
  const ciq = rows.filter((a) => !a.native).length;
  const hidden = rows.filter((a) => a.hidden && ['activity', 'glance', 'app'].includes(a.typeName)).length;
  $('#inst-status').textContent = `${rows.length} of ${apps.length} · ${ciq} from Connect IQ, ${rows.length - ciq} built in · ${hidden} hidden from the lists${pending.size ? ` · ${pending.size} change${pending.size === 1 ? '' : 's'} waiting for unplug` : ''}`;
}
async function setAppHidden(a, hidden) {
  try {
    const r = await api('PUT', `/api/apps/${encodeURIComponent(a.fileName || a.id)}/hidden`, { hidden });
    toast(`${a.name}: ${hidden ? 'hidden from' : 'shown in'} the watch's lists after the next unplug.`);
    state.installed.pending = await api('GET', '/api/apps/pending').catch(() => state.installed.pending);
    renderInstalled(); loadNewFiles();
  } catch (e) { toast(e.message, 'bad'); }
}
$('#inst-filter').addEventListener('input', () => { if (state.apps.length) renderInstalled(); });
$('#inst-sort').value = state.installed.sort;
$('#inst-sort').addEventListener('change', () => { state.installed.sort = $('#inst-sort').value; prefs.set('instSort', state.installed.sort); if (state.apps.length) renderInstalled(); });
document.querySelectorAll('#inst-visibility .chip').forEach((c) => c.addEventListener('click', () => {
  state.installed.visibility = c.dataset.v;
  document.querySelectorAll('#inst-visibility .chip').forEach((x) => x.classList.toggle('active', x === c));
  if (state.apps.length) renderInstalled();
}));

async function loadMusic() {
  if (!state.connected) return;
  try { state.music.lib = await api('GET', '/api/music'); renderMusic(); }
  catch (e) { $('#music-table tbody').innerHTML = `<tr><td colspan="5" class="muted">${esc(e.message)}</td></tr>`; }
}
function renderMusic() {
  const lib = state.music.lib, tb = $('#music-table tbody'); tb.innerHTML = '';
  const mq = ($('#music-q').value || '').trim().toLowerCase();
  for (const t of lib.tracks.filter((t) => !mq || `${t.artist || ''} ${t.album || ''} ${t.title}`.toLowerCase().includes(mq))) {
    const tr = el(`<tr><td class="chk"><input type="checkbox" data-path="${esc(t.path)}"${state.music.sel.has(t.path) ? ' checked' : ''}></td><td>${esc(t.artist || '')}</td><td>${esc(t.album || '')}</td><td title="${esc(t.path)}">${esc(t.title)}${t.folder !== 'Music' ? ` <span class="tag">${esc(t.folder)}</span>` : ''}</td><td class="num">${fmtSize(t.size)}</td></tr>`);
    tr.querySelector('input').onchange = (ev) => { if (ev.target.checked) state.music.sel.add(t.path); else state.music.sel.delete(t.path); updateMusicButtons(); };
    tb.appendChild(tr);
  }
  const prov = lib.providers || [];
  if (!lib.tracks.length) tb.innerHTML = `<tr><td colspan="5" class="muted">${prov.length ? 'No audio files found. Streaming downloads appear only in their apps.' : 'No audio files found. Add MP3, M4A, AAC, WAV or FLAC files above.'}</td></tr>`;
  const pv = $('#music-providers'); pv.hidden = !prov.length;
  if (prov.length) pv.innerHTML = `<h2>Streaming apps</h2><ul class="pending">${prov.map((p) => `<li><span><div class="what">${esc(p.name)} <span class="tag">v${p.version}</span></div><div class="meta">Download cache</div></span><span class="num">${fmtSize(p.cacheBytes)}</span></li>`).join('')}</ul><p class="hint">Manage downloads in each app on the watch.</p>`;
  $('#music-status').textContent = `${lib.tracks.length} tracks · ${fmtSize(lib.bytes)}${state.summary && state.summary.storage ? ` · ${fmtSize(state.summary.storage.freeBytes)} free on the watch` : ''}`;
  const ul = $('#music-playlists'); ul.innerHTML = lib.playlists.length ? '' : '<li class="muted">Select tracks, then choose “Create playlist”.</li>';
  for (const p of lib.playlists) {
    const li = el(`<li><div><div class="what">${esc(p.name)}</div><div class="meta">${p.tracks.length} tracks · <span class="mono">${esc(p.path)}</span></div></div><button class="btn small danger">Delete</button></li>`);
    li.querySelector('button').onclick = async () => { if (!await confirmDialog({ title: `Delete playlist “${p.name}”?`, body: '<p>The tracks stay on the watch.</p>', ok: 'Delete' })) return; try { await api('DELETE', '/api/music?path=' + encodeURIComponent(p.path)); loadMusic(); } catch (e) { toast(e.message, 'bad'); } };
    ul.appendChild(li);
  }
  updateMusicButtons();
}
function updateMusicButtons() { const n = state.music.sel.size; $('#music-playlist').disabled = n === 0; $('#music-delete').disabled = n === 0; }
$('#music-files').addEventListener('change', async () => {
  const files = [...$('#music-files').files]; if (!files.length) return;
  const folder = $('#music-folder').value;
  let done = 0;
  for (const f of files) {
    $('#music-status').textContent = `Copying ${f.name} (${done + 1}/${files.length})…`;
    try { await api('POST', `/api/music?folder=${encodeURIComponent(folder)}&name=${encodeURIComponent(f.name)}`, await f.arrayBuffer(), true); done++; }
    catch (e) { toast(`${f.name}: ${e.message}`, 'bad'); }
  }
  $('#music-files').value = '';
  toast(`${done} file${done === 1 ? '' : 's'} copied to the watch`);
  loadMusic();
});
$('#music-playlist').onclick = async () => {
  const tracks = state.music.lib.tracks.map((t) => t.path).filter((p) => state.music.sel.has(p));
  const name = await promptDialog('New playlist', 'Playlist name', 'My playlist');
  if (!name) return;
  try { await api('PUT', '/api/music/playlists/' + encodeURIComponent(name), { tracks }); toast(`Playlist “${name}” written`); state.music.sel.clear(); loadMusic(); } catch (e) { toast(e.message, 'bad'); }
};
$('#music-delete').onclick = async () => {
  const paths = [...state.music.sel];
  if (!await confirmDialog({ title: `Delete ${paths.length} file${paths.length > 1 ? 's' : ''} from the watch?`, body: `<ul class="changes">${paths.map((p) => `<li><span>${esc(p)}</span></li>`).join('')}</ul>`, ok: 'Delete' })) return;
  for (const p of paths) { try { await api('DELETE', '/api/music?path=' + encodeURIComponent(p)); } catch (e) { toast(e.message, 'bad'); } }
  state.music.sel.clear(); loadMusic();
};
function promptDialog(title, label, value) {
  return new Promise((resolve) => {
    const dlg = $('#dlg');
    $('#dlg-title').textContent = title;
    $('#dlg-body').innerHTML = `<label class="form" style="display:block">${esc(label)}<input id="dlg-input" type="text" value="${esc(value)}" style="width:100%;margin-top:6px"></label>`;
    $('#dlg-ok').textContent = 'Create playlist';
    dlg.addEventListener('close', () => resolve(dlg.returnValue === 'ok' ? $('#dlg-input').value.trim() : null), { once: true });
    dlg.showModal(); $('#dlg-input').select();
  });
}

// ---------- Connect IQ store ----------
state.store = { loaded: false, q: '', type: prefs.get('storeType', 'watchface'), sort: prefs.get('storeSort', 'mostPopular'), page: 0, apps: [], total: -1, selected: null, busy: false, generation: 0 };
function storeConnectionChanged() {
  const st = state.store;
  st.generation++;
  st.loaded = false; st.busy = false; st.page = 0; st.apps = []; st.total = -1;
  if (state.tab === 'store') loadStore();
  if (st.selected) showStoreApp(st.selected);
}
function fmtCount(n) { return n >= 1e6 ? (n / 1e6).toFixed(1) + 'M' : n >= 1e3 ? Math.round(n / 1e3) + 'k' : String(n); }
function stars(r) { return r ? `★ ${Number(r).toFixed(1)}` : 'no rating'; }
async function loadStore(more) {
  const st = state.store;
  if (st.busy) return;
  const generation = st.generation;
  st.busy = true;
  if (!more) { st.page = 0; st.apps = []; $('#store-grid').innerHTML = '<p class="muted">Searching the store…</p>'; setRoute('store', st.selected, storeParams(), true); }
  try {
    const params = new URLSearchParams({ q: st.q, type: st.q ? st.type : (st.type || 'watchface'), sort: st.sort, page: st.page });
    const view = await api('GET', '/api/store?' + params.toString());
    if (generation !== st.generation) return;
    st.loaded = true;
    if (!st.typesRendered) { renderStoreControls(view); st.typesRendered = true; }
    st.apps = st.apps.concat(view.apps); st.total = view.total;
    renderStore();
    $('#store-more').hidden = view.apps.length < 24;
    $('#store-count').textContent = st.total >= 0 ? `${st.apps.length} of ${st.total} results` : `${st.apps.length} shown`;
  } catch (e) { if (generation === st.generation) $('#store-grid').innerHTML = `<p class="muted">${esc(e.message)}</p>`; }
  finally { if (generation === st.generation) st.busy = false; }
}
function renderStoreControls(view) {
  const types = $('#store-types'); types.innerHTML = '';
  for (const t of view.types) {
    const b = el(`<button class="chip${t.key === state.store.type ? ' active' : ''}" data-type="${esc(t.key)}">${esc(t.label)}</button>`);
    b.onclick = () => { state.store.type = t.key; prefs.set('storeType', t.key); document.querySelectorAll('#store-types .chip').forEach((x) => x.classList.toggle('active', x.dataset.type === t.key)); loadStore(); };
    types.appendChild(b);
  }
  const sort = $('#store-sort'); sort.innerHTML = view.sorts.map((s) => `<option value="${esc(s.key)}">${esc(s.label)}</option>`).join('');
  sort.value = state.store.sort;
}
function storeFilters() {
  return { rating: Number($('#sf-rating').value), reviews: Number($('#sf-reviews').value), price: $('#sf-price').value, state: $('#sf-state').value, fits: $('#sf-fits').checked, settings: $('#sf-settings').checked };
}
function storeMatches(a, f) {
  const free = !a.price || /^(free|0|0[.,]00.*)$/i.test(String(a.price).trim());
  if (f.rating && !(Number(a.rating) >= f.rating)) return false;
  if (f.reviews && !(Number(a.reviewCount) >= f.reviews)) return false;
  if (f.price === 'free' && !free) return false;
  if (f.price === 'paid' && free) return false;
  if (f.state === 'installed' && !a.installed) return false;
  if (f.state === 'update' && !a.updateAvailable) return false;
  if (f.state === 'new' && a.installed) return false;
  if (f.fits && state.summary && state.summary.storage && a.sizeBytes > state.summary.storage.freeBytes) return false;
  if (f.settings && !a.hasSettings) return false;
  return true;
}
function renderStore() {
  const grid = $('#store-grid'); grid.innerHTML = '';
  const f = storeFilters();
  const shown = state.store.apps.filter((a) => storeMatches(a, f));
  $('#store-filtered').textContent = shown.length === state.store.apps.length ? '' : `${shown.length} of ${state.store.apps.length} loaded apps match`;
  for (const a of shown) {
    const badge = a.updateAvailable ? '<span class="tag new">Update</span>' : a.installed ? '<span class="tag ok">Installed</span>' : state.connected && !a.compatible ? '<span class="tag">Not for this watch</span>' : '';
    const card = el(`<button class="store-card${state.store.selected === a.id ? ' active' : ''}" data-id="${esc(a.id)}">
      ${a.iconUrl ? `<img src="${esc(a.iconUrl)}" alt="" loading="lazy">` : '<span class="app-ico">?</span>'}
      <span class="body"><span class="name">${esc(a.name)}</span><span class="dev">${esc(a.developer)}</span>
      <span class="meta"><b>${stars(a.rating)}</b> (${a.reviewCount}) · ${fmtCount(a.downloads)} downloads</span>
      <span class="meta">${esc(a.price || 'Free')}${a.trialMode ? ' · trial' : ''} ${badge}</span></span></button>`);
    card.onclick = () => showStoreApp(a.id);
    grid.appendChild(card);
  }
  if (!state.store.apps.length) grid.innerHTML = '<p class="muted">No apps found. Try another search or app type.</p>';
  else if (!shown.length) grid.innerHTML = '<p class="muted">No loaded apps match. Change the filters or select Show more.</p>';
}
document.querySelectorAll('#store-filters select, #store-filters input').forEach((x) => x.addEventListener('change', () => { if (state.store.loaded) renderStore(); }));
async function showStoreApp(id) {
  const generation = state.store.generation;
  state.store.selected = id;
  setRoute('store', id, storeParams());
  document.querySelectorAll('.store-card').forEach((c) => c.classList.toggle('active', c.dataset.id === id));
  const box = $('#store-detail'); box.hidden = false; box.innerHTML = '<p class="muted">Loading…</p>';
  try {
    const d = await api('GET', '/api/store/apps/' + encodeURIComponent(id));
    if (generation !== state.store.generation || state.store.selected !== id) return;
    const status = !state.connected ? '<span class="tag">Connect a watch to check compatibility</span>' : d.updateAvailable ? `<span class="tag new">Installed v${d.installedVersion} · store has v${d.internalVersion}</span>` : d.installed ? `<span class="tag ok">Installed v${d.installedVersion}</span>` : d.compatible ? '<span class="tag">Not installed</span>' : '<span class="tag">Not for this watch</span>';
    box.innerHTML = `
      <div class="head">${d.iconUrl ? `<img class="icon" src="${esc(d.iconUrl)}" alt="">` : ''}<div><h2>${esc(d.name)}</h2><div class="hint">${esc(d.developer)} · ${esc(d.type)}</div></div></div>
      <div>${status}</div>
      <dl class="facts">
        <dt>Rating</dt><dd>${stars(d.rating)} from ${d.reviewCount} reviews</dd>
        <dt>Downloads</dt><dd>${fmtCount(d.downloads)}</dd>
        <dt>Version</dt><dd>${esc(d.version)}${d.updated ? ' · ' + esc(d.updated) : ''}</dd>
        <dt>Price</dt><dd>${esc(d.price || 'Free')}${d.trialMode ? ' (trial available)' : ''}</dd>
        ${d.sizeBytes ? `<dt>Size</dt><dd>${fmtSize(d.sizeBytes)}${state.connected ? ' on this watch' : ''}${state.connected && state.summary && state.summary.storage ? ` · ${fmtSize(state.summary.storage.freeBytes)} free` : ''}</dd>` : ''}
        ${d.hasSettings !== undefined && d.hasSettings !== null ? `<dt>Settings</dt><dd>${d.hasSettings ? 'editable here once installed' : 'none'}</dd>` : ''}
        ${d.permissions && d.permissions.length ? `<dt>Permissions</dt><dd>${esc(d.permissions.join(', '))}</dd>` : ''}
      </dl>
      <div class="btns"><a class="btn primary" href="${esc(d.storeUrl)}" target="garmin-store" rel="noopener">Open in store ↗</a>${d.installed && d.hasSettings ? '<button class="btn" id="store-edit">Edit settings</button>' : ''}</div>
      <p class="hint">${d.installed && !d.updateAvailable ? 'No update found.' : 'Queue the install or update in the Connect IQ phone app, then sync on the Garmin Connect tab.'}</p>
      ${d.description ? `<h3>About</h3><div class="text">${esc(d.description)}</div>` : ''}
      ${d.whatsNew ? `<h3>What's new</h3><div class="text">${esc(d.whatsNew)}</div>` : ''}
      ${d.reviews && d.reviews.length ? `<h3>Reviews</h3>${d.reviews.filter((r) => r.text).slice(0, 6).map((r) => `<div class="review"><b>${'★'.repeat(r.rating)}</b> ${esc(r.reviewer)}${r.version ? ' · v' + esc(r.version) : ''}<div>${esc(r.text)}</div></div>`).join('')}` : ''}`;
    const edit = $('#store-edit');
    if (edit) edit.onclick = async () => { const apps = await fetchApps().catch(() => []); const a = apps.find((x) => x.fileName && x.name.toLowerCase().startsWith(d.name.toLowerCase().slice(0, 4))); navigate('apps', a ? a.name : ''); };
  } catch (e) { if (generation === state.store.generation && state.store.selected === id) box.innerHTML = `<p class="muted">${esc(e.message)}</p>`; }
}
let storeTimer = null;
$('#store-q').addEventListener('input', () => { clearTimeout(storeTimer); storeTimer = setTimeout(() => { state.store.q = $('#store-q').value.trim(); loadStore(); }, 350); });
$('#store-sort').addEventListener('change', () => { state.store.sort = $('#store-sort').value; prefs.set('storeSort', state.store.sort); loadStore(); });
$('#store-more').onclick = () => { state.store.page++; loadStore(true); };

// ---------- Garmin Connect ----------
state.cloud = { status: null, settings: [], messages: [] };
state.cloudPending = new Map();
let cloudTimer = null;
async function loadCloud() {
  let st;
  try { st = await api('GET', '/api/cloud/status'); } catch (e) { toast(e.message, 'bad'); return; }
  state.cloud.status = st;
  $('#cloud-login').hidden = st.loggedIn;
  $('#cloud-main').hidden = !st.loggedIn;
  if (!st.loggedIn) { $('#mfa-form').hidden = true; $('#login-form').hidden = false; return; }
  const name = st.profile && st.profile.name ? `${st.profile.name} · ${st.email}` : st.email;
  const reg = st.deviceRegistered ? `watch registered as “${st.deviceName}”` : 'this watch is not on the account';
  $('#cloud-who').innerHTML = `Signed in as <b>${esc(name)}</b> <span class="hint">· ${esc(reg)}${st.error ? ' · ' + esc(st.error) : ''}</span>`;
  if (!state.connected) { $('#cloud-messages').innerHTML = '<li class="muted">Connect the watch to see its queue.</li>'; $('#cloud-settings').innerHTML = '<p class="muted">Connect the watch to load its settings.</p>'; return; }
  loadCloudMessages();
  loadCloudSettings();
  pollSync();
}
async function loadCloudMessages() {
  const ul = $('#cloud-messages');
  try {
    const msgs = await api('GET', '/api/cloud/messages');
    state.cloud.messages = msgs;
    const fresh = msgs.filter((m) => m.messageStatus === 'new');
    $('#badge-cloud').hidden = fresh.length === 0; $('#badge-cloud').textContent = fresh.length;
    if (!msgs.length) { ul.innerHTML = '<li class="muted">No items queued.</li>'; return; }
    ul.innerHTML = msgs.map((m) => `<li><div><div class="what">${esc(m.metaData.messageName || m.messageType)} <span class="tag${m.messageStatus === 'new' ? ' new' : ''}">${esc(m.messageStatus)}</span></div><div class="meta">${esc(m.messageType)} · ${esc(m.metaData.fileType)} · ${esc(m.deviceXmlDataType)}</div></div></li>`).join('');
  } catch (e) { ul.innerHTML = `<li class="muted">${esc(e.message)}</li>`; }
}
async function loadCloudSettings() {
  const root = $('#cloud-settings');
  try { state.cloud.settings = await api('GET', '/api/cloud/settings'); renderCloudSettings(); }
  catch (e) { root.innerHTML = `<p class="muted">${esc(e.message)}</p>`; }
}
function renderCloudSettings() {
  const root = $('#cloud-settings'); root.innerHTML = '';
  for (const it of state.cloud.settings) {
    const pending = state.cloudPending.get(it.key);
    const cur = pending !== undefined ? pending : it.value;
    let ctl;
    if (it.options && it.options.length) {
      ctl = el(`<select>${it.options.map((o) => `<option value="${esc(JSON.stringify(o.value))}">${esc(o.label)}</option>`).join('')}</select>`);
      ctl.value = JSON.stringify(cur);
      if (ctl.value !== JSON.stringify(cur)) { ctl.appendChild(el(`<option value="${esc(JSON.stringify(cur))}">${esc(fmt(cur))}</option>`)); ctl.value = JSON.stringify(cur); }
    } else if (it.type === 'bool') {
      ctl = el(`<input type="checkbox" class="switch"${cur ? ' checked' : ''}>`);
    } else if (it.type === 'number') {
      ctl = el(`<input type="number" value="${esc(cur)}">`);
    } else if (it.type === 'list' || it.type === 'object') {
      ctl = el(`<textarea spellcheck="false">${esc(JSON.stringify(cur, null, 1))}</textarea>`);
    } else {
      ctl = el(`<input type="text" value="${esc(cur)}">`);
    }
    const row = el(`<div class="row cloud-row${pending !== undefined ? ' changed' : ''}"><div><div class="label">${esc(humanize(it.key))}<span class="tag connect">Connect</span></div><div class="key">${esc(it.key)}</div>${it.hint ? `<div class="meta">${esc(it.hint)}</div>` : ''}</div><div class="control"></div></div>`);
    row.querySelector('.control').appendChild(ctl);
    ctl.addEventListener('change', () => {
      let v;
      if (it.options && it.options.length) v = JSON.parse(ctl.value);
      else if (it.type === 'bool') v = ctl.checked;
      else if (it.type === 'number') v = Number(ctl.value);
      else if (it.type === 'list' || it.type === 'object') { try { v = JSON.parse(ctl.value); } catch (e) { toast('Enter valid JSON for ' + humanize(it.key), 'bad'); return; } }
      else v = ctl.value;
      if (JSON.stringify(v) === JSON.stringify(it.value)) state.cloudPending.delete(it.key); else state.cloudPending.set(it.key, v);
      row.classList.toggle('changed', state.cloudPending.has(it.key));
      updateActionBar();
    });
    root.appendChild(row);
  }
  if (!state.cloud.settings.length) root.innerHTML = '<p class="muted">Connect has no settings record for this watch.</p>';
}
async function applyCloudChanges() {
  const list = [...state.cloudPending.entries()];
  const body = `<ul class="changes">${list.map(([k, v]) => { const it = state.cloud.settings.find((s) => s.key === k) || {}; return `<li><span>${esc(humanize(k))}</span><span><span class="from">${esc(fmt(it.value))}</span><span class="to">${esc(typeof v === 'object' ? JSON.stringify(v) : fmt(v))}</span></span></li>`; }).join('')}</ul><p class="note">Save in Garmin Connect and download any settings file it queues. Unplug the watch to apply downloaded changes.</p>`;
  if (!await confirmDialog({ title: 'Save in Garmin Connect?', body, ok: 'Save' })) return;
  try {
    const res = await api('PUT', '/api/cloud/settings', { changes: Object.fromEntries(list) });
    state.cloudPending.clear(); updateActionBar();
    toast(res.note, res.pulled && res.pulled.length ? '' : 'warn');
    loadCloudSettings(); loadCloudMessages(); loadNewFiles();
  } catch (e) { toast(e.message, 'bad'); }
}
$('#login-form').addEventListener('submit', async (ev) => {
  ev.preventDefault();
  $('#btn-login').disabled = true;
  try {
    const res = await api('POST', '/api/cloud/login', { email: $('#login-email').value.trim(), password: $('#login-password').value });
    $('#login-password').value = '';
    if (res.needsMfa) { $('#login-form').hidden = true; $('#mfa-form').hidden = false; $('#mfa-hint').textContent = `Garmin sent a verification code by ${res.mfaMethod}.`; $('#mfa-code').focus(); }
    else { toast('Signed in'); loadCloud(); }
  } catch (e) { toast(e.message, 'bad'); }
  finally { $('#btn-login').disabled = false; }
});
$('#mfa-form').addEventListener('submit', async (ev) => {
  ev.preventDefault();
  try { await api('POST', '/api/cloud/mfa', { code: $('#mfa-code').value.trim() }); $('#mfa-code').value = ''; toast('Signed in'); loadCloud(); }
  catch (e) { toast(e.message, 'bad'); }
});
$('#btn-logout').onclick = async () => {
  if (!await confirmDialog({ title: 'Sign out of Garmin Connect?', body: '<p>The saved session token is deleted. Nothing on the watch changes.</p>', ok: 'Sign out' })) return;
  try { await api('POST', '/api/cloud/logout'); state.cloudPending.clear(); updateActionBar(); loadCloud(); } catch (e) { toast(e.message, 'bad'); }
};
$('#btn-sync').onclick = async () => {
  try {
    await api('POST', '/api/cloud/sync', { upload: $('#sync-upload').checked, download: $('#sync-download').checked });
    $('#sync-log').innerHTML = '';
    pollSync();
  } catch (e) { toast(e.message, 'bad'); }
};
async function pollSync() {
  clearTimeout(cloudTimer);
  let j;
  try { j = await api('GET', '/api/cloud/sync'); } catch (e) { return; }
  const log = $('#sync-log');
  if (j.log && j.log.length !== log.children.length) {
    log.innerHTML = j.log.map((l) => `<li class="${/: uploaded|->/.test(l) ? 'ok' : /failed|error|: .*\(.*\)$/.test(l) && !/duplicate/.test(l) ? 'bad' : ''}">${esc(l)}</li>`).join('');
    log.scrollTop = log.scrollHeight;
  }
  $('#btn-sync').disabled = !!j.running;
  if (j.running) { $('#sync-status').textContent = `Syncing… ${j.log ? j.log.length : 0} files so far`; cloudTimer = setTimeout(pollSync, 1000); return; }
  if (j.error) { $('#sync-status').textContent = 'Failed: ' + j.error; return; }
  if (j.result) {
    const u = j.result.upload;
    const parts = [];
    if (u) parts.push(`uploaded ${u.uploaded}, already in Connect ${u.duplicate}, failed ${u.failed}, unchanged ${u.skipped}`);
    const pulled = (j.result.pulled || []).length;
    parts.push(`${pulled} item${pulled === 1 ? '' : 's'} written to the watch`);
    $('#sync-status').textContent = `Done ${new Date(j.endedAt).toLocaleTimeString()}: ${parts.join('; ')}`;
    if (!state.cloud.syncSeen || state.cloud.syncSeen !== j.endedAt) { state.cloud.syncSeen = j.endedAt; loadCloudMessages(); loadNewFiles(); if ((j.result.pulled || []).length) { state.apps = []; } }
  } else $('#sync-status').textContent = '';
}

// ---------- boot ----------
applyRoute();
api('GET', '/api/status').then((st) => {
  if (st.connected) { state.connected = true; state.summary = st.summary; storeConnectionChanged(); setConn('ok', 'Connected'); $('#devname').textContent = st.summary.model; $('#btn-connect').textContent = 'Reconnect'; renderOverview(st.summary); loadNewFiles(); loadTab(state.tab); }
}).catch(() => {});
