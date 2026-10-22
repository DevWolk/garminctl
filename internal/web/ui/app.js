'use strict';
const $ = (s) => document.querySelector(s);
const state = {
  connected: false, summary: null,
  rows: [], pending: new Map(),
  apps: [], app: null, appItems: [], appSchema: null, appValues: {}, appPending: new Map(),
  path: '', tab: 'overview',
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
    $('#dlg-ok').textContent = ok || 'OK';
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
document.querySelectorAll('.nav-item').forEach((b) => b.addEventListener('click', () => showTab(b.dataset.tab)));
function showTab(tab) {
  state.tab = tab;
  document.querySelectorAll('.nav-item').forEach((x) => x.classList.toggle('active', x.dataset.tab === tab));
  document.querySelectorAll('.tab').forEach((t) => t.classList.toggle('active', t.id === 'tab-' + tab));
  if (state.connected) loadTab(tab); else if (tab === 'cloud') loadCloud();
  updateActionBar();
}
function loadTab(tab) {
  if (tab === 'settings' && !state.rows.length) loadSettings();
  if (tab === 'apps' && !state.apps.length) loadApps();
  if (tab === 'files') loadFiles(state.path);
  if (tab === 'updates' && !state.updates.checkedAt) loadUpdates();
  if (tab === 'cloud') loadCloud();
}

// ---------- connection + overview ----------
async function connect() {
  setConn('busy', 'Connecting…');
  $('#btn-connect').disabled = true;
  try {
    const st = await api('POST', '/api/connect');
    state.connected = true; state.summary = st.summary;
    setConn('ok', 'Connected');
    $('#devname').textContent = st.summary.model;
    renderOverview(st.summary);
    loadNewFiles();
    loadTab(state.tab);
    $('#btn-connect').textContent = 'Reconnect';
  } catch (e) {
    state.connected = false;
    setConn('bad', 'Not connected');
    toast('Could not connect: ' + e.message, 'bad');
  } finally { $('#btn-connect').disabled = false; }
}
// Retail part numbers (the support site's "identifier"); the device XML only carries the firmware part.
const retailSKU = { 'vívoactive 5': '010-02862-10', 'vivoactive 5': '010-02862-10' };
function renderOverview(s) {
  const sku = retailSKU[(s.model || '').toLowerCase()] || retailSKU[s.model];
  $('#link-manual').href = sku ? `https://support.garmin.com/en-US/?identifier=${sku}&tab=manuals` : 'https://support.garmin.com/en-US/';
  $('#overview-empty').hidden = true;
  $('#overview').hidden = false;
  $('#facts').innerHTML = `
    <dt>Model</dt><dd>${esc(s.model)}</dd>
    <dt>Firmware</dt><dd>${esc(s.software)}</dd>
    <dt>Unit ID</dt><dd class="mono">${s.unitId}</dd>
    <dt>Part number</dt><dd class="mono">${esc(s.partNumber)}</dd>
    <dt>Apps and glances</dt><dd>${s.appCount} entries</dd>
    <dt>FIT profile</dt><dd>${esc(s.fitProfile)}</dd>`;
}
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
    $('#badge-pending').hidden = entries.length === 0;
    $('#badge-pending').textContent = entries.length;
    if (!entries.length) { ul.innerHTML = '<li class="muted">Nothing waiting. The watch is up to date with this computer.</li>'; return; }
    ul.innerHTML = '';
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
  $('#settings-groups').innerHTML = '<p class="muted">Reading Settings.fit…</p>';
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
  if (!names.length) { root.innerHTML = '<p class="muted">No settings match.</p>'; return; }
  for (const name of names) {
    const rows = groups.get(name);
    const title = groupTitles[name] || (name.startsWith('mesg') || name.startsWith('unknown') ? `Undocumented message ${rows[0].mesgNum}` : humanize(name));
    const g = el(`<section class="group"><div class="group-head"><h2>${esc(title)}</h2><span class="count">${rows.length} field${rows.length > 1 ? 's' : ''}${counts[name] > 1 ? ` · ${counts[name]} entries` : ''}</span></div><div class="rows"></div></section>`);
    const list = g.querySelector('.rows');
    for (const r of rows) list.appendChild(settingRow(r, counts[name] > 1));
    root.appendChild(g);
  }
}
function settingRow(r, repeated) {
  const key = rowKey(r);
  const label = (repeated ? `${r.index + 1}. ` : '') + humanize(r.field);
  const tags = [];
  if (r.managedByConnect) tags.push('<span class="tag connect" title="Garmin Connect re-sends this field on every sync; change it in Connect too">Garmin Connect</span>');
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
  $('#actionbar-text').textContent = state.tab === 'cloud' ? `${n} change${n > 1 ? 's' : ''} not yet saved in Garmin Connect` : `${n} ${what} change${n > 1 ? 's' : ''} not yet on the watch`;
}
$('#btn-discard').onclick = () => {
  if (state.tab === 'apps') { state.appPending.clear(); renderAppSettings(); renderPreview(); } else if (state.tab === 'cloud') { state.cloudPending.clear(); renderCloudSettings(); } else { state.pending.clear(); renderSettings(); }
  updateActionBar();
};
$('#btn-review').onclick = () => (state.tab === 'apps' ? applyAppChanges() : state.tab === 'cloud' ? applyCloudChanges() : applySettings());
async function applySettings() {
  const list = [...state.pending.values()];
  const body = `<ul class="changes">${list.map((c) => `<li><span>${esc(c.label)}</span><span><span class="from">${esc(c.from)}</span><span class="to">${esc(c.to)}</span></span></li>`).join('')}</ul><p class="note">A settings-change file is written to the watch now. It is applied when the USB session ends: unplug the watch or eject it.</p>`;
  if (!await confirmDialog({ title: 'Write to the watch?', body, ok: 'Write changes' })) return;
  try {
    const res = await api('POST', '/api/settings/apply', { changes: list.map(({ mesg, index, field, value }) => ({ mesg, index, field, value })) });
    state.pending.clear(); updateActionBar();
    toast(`Written. ${res.note}`, res.note.includes('Warning') ? 'warn' : '');
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
async function loadApps() {
  const root = $('#apps-list');
  root.innerHTML = '<p class="muted">Reading installed apps…</p>';
  try {
    state.apps = await api('GET', '/api/apps');
    root.innerHTML = '';
    const seen = new Set();
    for (const a of state.apps) {
      if (!a.fileName || seen.has(a.fileName)) continue;
      seen.add(a.fileName);
      const b = el(`<button class="app-item" data-file="${esc(a.fileName)}"><span class="app-ico">${esc(a.name.slice(0, 2).toUpperCase())}</span><span><div class="app-name">${esc(a.name)}</div><div class="app-sub">${esc(a.typeName)} · v${a.version}</div></span></button>`);
      b.onclick = () => { document.querySelectorAll('.app-item').forEach((x) => x.classList.remove('active')); b.classList.add('active'); loadAppSettings(a); };
      root.appendChild(b);
    }
    markAppUpdates();
    const natives = state.apps.filter((a) => !a.fileName).length;
    root.appendChild(el(`<p class="apps-note">${natives} built-in glances, activities and controls are not editable here.</p>`));
  } catch (e) { root.innerHTML = `<p class="muted">${esc(e.message)}</p>`; }
}
async function loadAppSettings(app) {
  state.app = app; state.appPending.clear(); state.appSchema = null; state.appItems = []; updateActionBar();
  $('#app-store').href = 'https://apps.garmin.com/apps/' + app.id; $('#app-store').hidden = !app.id;
  $('#app-title').textContent = app.name;
  $('#app-hint').textContent = `${humanize(app.typeName)} · settings file ${app.fileName}.SET`;
  $('#app-filter').hidden = false;
  $('#app-groups').innerHTML = '<p class="muted">Reading settings…</p>';
  try {
    const as = await api('GET', `/api/apps/${encodeURIComponent(app.fileName)}/settings`);
    state.appValues = as.values; state.appItems = as.items || [];
    if (as.schemaError) toast('No settings definition from the store: ' + as.schemaError, 'warn');
    else state.appSchema = await api('GET', `/api/apps/${encodeURIComponent(app.fileName)}/schema`).catch(() => null);
    renderAppSettings();
    setupPreview(app);
  } catch (e) { $('#app-groups').innerHTML = `<p class="muted">${esc(e.message)}</p>`; }
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
  if (!groups.size) { root.innerHTML = '<p class="muted">No settings match.</p>'; return; }
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
  if (sc && sc.readOnly) {
    readonly = true;
    control = `<span class="muted">${esc(String(v ?? ''))}</span>`;
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
  ctl.addEventListener('change', onChange);
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
  const body = `<ul class="changes">${list.map(([, c]) => `<li><span>${esc(c.label)}</span><span><span class="from">${esc(c.from)}</span><span class="to">${esc(c.to)}</span></span></li>`).join('')}</ul><p class="note">${esc(state.app.name)} reloads its settings right after the file is written.</p>`;
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
async function loadFiles(path) {
  state.path = path;
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
    entries.sort((a, b) => (a.dir === b.dir ? a.name.localeCompare(b.name) : a.dir ? -1 : 1));
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
        tr.innerHTML = `<td>${esc(e.name)}</td><td class="num">${e.size.toLocaleString()}</td><td class="muted">${esc((e.modified || '').replace('T', ' '))}</td><td class="actions"><a href="/api/file?path=${encodeURIComponent(full)}">Download</a>${isFit ? `<a href="/api/decode?path=${encodeURIComponent(full)}" target="_blank" rel="noopener">Decode</a>` : ''}<button class="btn small danger">Delete</button></td>`;
        tr.querySelector('button').onclick = async () => {
          if (!await confirmDialog({ title: 'Delete from the watch?', body: `<p>${esc(full)} will be removed permanently.</p>`, ok: 'Delete' })) return;
          try { await api('DELETE', '/api/file?path=' + encodeURIComponent(full)); toast('Deleted ' + e.name); } catch (err) { toast(err.message, 'bad'); }
          loadFiles(path);
        };
      }
      tb.appendChild(tr);
    }
  } catch (e) { tb.innerHTML = `<tr><td colspan="4" class="muted">${esc(e.message)}</td></tr>`; }
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
function fmtSize(n) { return n >= 1e6 ? (n / 1e6).toFixed(1) + ' MB' : Math.round(n / 1e3) + ' kB'; }
async function loadUpdates() {
  if (state.updates.busy) return;
  if (!state.connected) { toast('Connect the watch first', 'warn'); return; }
  state.updates.busy = true;
  $('#btn-check-updates').disabled = true;
  $('#updates-status').textContent = 'Asking Garmin…';
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
  $('#updates-status').textContent = `Checked ${u.checkedAt.toLocaleTimeString()} · ` + (n ? `${n} update${n > 1 ? 's' : ''} available` : 'everything is up to date');

  const sw = $('#sw-list'); sw.innerHTML = '';
  if (u.softwareError) sw.innerHTML = `<li class="muted">Could not reach Garmin: ${esc(u.softwareError)}</li>`;
  else if (!u.software.length) sw.innerHTML = '<li class="muted">Garmin offers nothing for this watch right now.</li>';
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
    body: `<p>${fmtSize(x.sizeBytes)} is downloaded from Garmin, checked against its checksum and written to <code>${esc(x.installPath)}/${esc(x.fileName)}</code>. The watch installs it after you unplug it${restart ? ', then restarts. Keep it charged and do not interrupt the restart' : ''}.</p>${notes.length ? `<ul class="notes">${notes.map((l) => `<li>${esc(l)}</li>`).join('')}</ul>` : ''}`,
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
  const ok = await confirmDialog({ title: 'Install this app on the watch?', body: `<p>${esc(f.name)} (${fmtSize(f.size)}) is copied to <code>GARMIN/APPS/MEDIA</code>, where Garmin Express puts new apps. The watch registers it after you unplug it.</p>`, ok: 'Install' });
  if (ok) {
    try { const res = await api('POST', '/api/apps/install', await f.arrayBuffer(), true); toast(res.note); state.apps = []; loadNewFiles(); } catch (e) { toast(e.message, 'bad'); }
  }
  $('#prg-file').value = '';
});

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
    if (!msgs.length) { ul.innerHTML = '<li class="muted">Nothing queued. Connect has delivered everything.</li>'; return; }
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
      else if (it.type === 'list' || it.type === 'object') { try { v = JSON.parse(ctl.value); } catch (e) { toast('Not valid JSON', 'bad'); return; } }
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
  const body = `<ul class="changes">${list.map(([k, v]) => { const it = state.cloud.settings.find((s) => s.key === k) || {}; return `<li><span>${esc(humanize(k))}</span><span><span class="from">${esc(fmt(it.value))}</span><span class="to">${esc(typeof v === 'object' ? JSON.stringify(v) : fmt(v))}</span></span></li>`; }).join('')}</ul><p class="note">Saved in Garmin Connect's record for this watch. Connect then generates a settings file, which is written to GARMIN/NewFiles and applied when you unplug the watch.</p>`;
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
    parts.push(`${(j.result.pulled || []).length} item(s) written to the watch`);
    $('#sync-status').textContent = `Done ${new Date(j.endedAt).toLocaleTimeString()}: ${parts.join('; ')}`;
    if (!state.cloud.syncSeen || state.cloud.syncSeen !== j.endedAt) { state.cloud.syncSeen = j.endedAt; loadCloudMessages(); loadNewFiles(); if ((j.result.pulled || []).length) { state.apps = []; } }
  } else $('#sync-status').textContent = '';
}

// ---------- boot ----------
api('GET', '/api/status').then((st) => {
  if (st.connected) { state.connected = true; state.summary = st.summary; setConn('ok', 'Connected'); $('#devname').textContent = st.summary.model; $('#btn-connect').textContent = 'Reconnect'; renderOverview(st.summary); loadNewFiles(); }
}).catch(() => {});
