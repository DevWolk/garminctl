'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const script = readFileSync(path.join(__dirname, '../internal/web/ui/application.js'), 'utf8');
const downloadScript = readFileSync(path.join(__dirname, '../internal/web/ui/application-download.js'), 'utf8');

// A deliberately small DOM port: unsafe HTML writes fail instead of parsing it.
class Element {
  constructor() { this.children = []; this.textContent = ''; this.hidden = false; this.disabled = false; this.checked = false; this.value = ''; this.events = new Map(); this.attributes = new Map(); }
  set innerHTML(_) { throw new Error('Untrusted HTML write'); }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; }
  setAttribute(name, value) { this.attributes.set(name, value); }
  removeAttribute(name) { this.attributes.delete(name); if (name === 'value') delete this.value; }
  addEventListener(name, callback) { this.events.set(name, callback); }
  fire(name) { return this.events.get(name)(); }
}
function fixture(fetch = async () => { throw new Error('Unexpected network'); }, clock = {}) {
  const nodes = new Map();
  const get = name => { if (!nodes.has(name)) nodes.set(name, new Element()); return nodes.get(name); };
  const window = {};
  const context = vm.createContext({window, document: {getElementById: id => get(id.replace('application-', '')), createElement: () => new Element()}, fetch, AbortSignal, Date, setTimeout: clock.setTimeout || (() => 1), clearTimeout: clock.clearTimeout || (() => {})});
  vm.runInContext(downloadScript, context);
  vm.runInContext(script, context);
  return {page: window.applicationPage, get};
}
const release = {tag: 'v2.0.0', name: '<img src=x onerror=alert(1)>', notes: '<script>unsafe()</script>', prerelease: false};
function snapshot(extra = {}) {
  return {history: [release], available: release, checkedAt: '2026-11-08T10:00:00Z', fromCache: true, ...extra};
}
function bridge(overrides = {}) {
  return {
    info: async () => ({desktop: {version:'v1.0.0'}, cli: {version:'v1.1.0'}, backend: {version:'v0.9.0', owned:false}, os:'linux', arch:'amd64', installation:{kind:'unknown', reason:'Ownership not confirmed'}, versionAgreement:'different'}),
    history: async () => snapshot(), check: async () => snapshot({fromCache:false}), ...overrides,
  };
}

test('visiting browser page only reads backend; native attaches late without auto-refresh', async () => {
  let fetches=0, checks=0, reads=0;
  const ui=fixture(async url => { assert.equal(url, '/api/server'); fetches++; return {ok:true,json:async()=>({version:'browser-backend'})}; });
  assert.equal(fetches,0);
  await ui.page.activate();
  assert.equal(fetches,1);
  assert.equal(ui.get('inventory').children[1].textContent,'browser-backend');
  await ui.page.attach(bridge({history:async pre=>{assert.equal(pre,false);reads++;return snapshot();},check:async()=>{checks++;}}));
  await ui.page.activate();
  assert.equal(reads,1);assert.equal(checks,0);
  assert.equal(ui.get('browser').hidden,true);
  assert.match(ui.get('inventory').children.map(n=>n.textContent).join(' '),/v1.0.0 CLI v1.1.0 Backend \(reused\) v0.9.0/);
  assert.match(ui.get('inventory-note').textContent,/versions differ/);
  assert.match(ui.get('status').textContent,/Saved history. Last checked:/);
});

test('release notes and names remain literal text; changing selection uses saved history only', async () => {
  let saved=0,checks=0;
  const ui=fixture();
  await ui.page.attach(bridge({history:async pre=>{saved++;return snapshot(pre?{history:[{...release,tag:'v3.0.0-beta.1',prerelease:true}]}:{});},check:async()=>{checks++;}}));
  assert.equal(saved,0);
  await ui.page.activate();
  assert.equal(ui.get('notes').textContent,release.notes);
  assert.equal(ui.get('release-title').textContent,release.name);
  ui.get('prerelease').checked=true;
  await ui.get('prerelease').fire('change');
  assert.equal(saved,2);assert.equal(checks,0);
  assert.match(ui.get('release').children[0].textContent,/prerelease/);
});

test('explicit check is single flight, reports cache fallback and persistence errors', async () => {
  let resolve,checks=0;
  const ui=fixture();
  await ui.page.attach(bridge({check:()=>{checks++;return new Promise(done=>{resolve=done;});}}));
  await ui.page.activate();
  const pending=ui.get('check').fire('click');
  await ui.get('check').fire('click');
  assert.equal(checks,1);assert.equal(ui.get('check').disabled,true);assert.equal(ui.get('progress').hidden,false);
  assert.doesNotMatch(ui.get('status').textContent,/Loading/);
  resolve(snapshot({refreshError:'offline <script>',cacheError:'disk full'}));
  await pending;
  assert.match(ui.get('status').textContent,/Could not refresh: offline <script>/);
  assert.match(ui.get('status').textContent,/Could not save history: disk full/);
  assert.equal(ui.get('check').disabled,false);assert.equal(ui.get('progress').hidden,true);
});

test('failed refresh preserves visible history; failed channel read clears it', async () => {
  let fail=false;
  const ui=fixture();
  await ui.page.attach(bridge({history:async()=>{if(fail)throw new Error('Unreadable cache');return snapshot();},check:async()=>{throw new Error('offline');}}));
  await ui.page.activate();
  await ui.get('check').fire('click');
  assert.equal(ui.get('notes').textContent,release.notes);
  assert.match(ui.get('status').textContent,/offline/);
  fail=true;ui.get('prerelease').checked=true;
  await ui.get('prerelease').fire('change');
  assert.equal(ui.get('release').children.length,0);
  assert.equal(ui.get('release').disabled,true);
  assert.match(ui.get('status').textContent,/Unreadable cache/);
});

test('missing cache, empty channel and partial native failures remain explicit', async () => {
  const ui=fixture();
  await ui.page.attach(bridge({info:async()=>{throw new Error('CLI probe failed');},history:async()=>null,check:async()=>snapshot({available:null,history:[],fromCache:false})}));
  await ui.page.activate();
  assert.match(ui.get('inventory-note').textContent,/CLI probe failed/);
  assert.match(ui.get('status').textContent,/No saved release history/);
  await ui.get('check').fire('click');
  assert.match(ui.get('status').textContent,/No newer release/);
  assert.equal(ui.get('release').disabled,true);
});

test('late browser response cannot replace native versions', async () => {
  let resolve;
  const ui=fixture(()=>new Promise(done=>{resolve=done;}));
  const loading=ui.page.activate();
  await ui.page.attach(bridge());
  resolve({ok:true,json:async()=>({version:'stale-browser'})});
  await loading;
  assert.doesNotMatch(ui.get('inventory').children.map(n=>n.textContent).join(' '),/stale-browser/);
});

test('desktop bridge supplies typed reads and preparation methods and routes Help', async () => {
  const source=readFileSync(path.join(__dirname,'../internal/desktop/desktop.js'),'utf8').replace(/^import .*;\n/,'');
  const calls=[];let bridge,event,navigation;
  const window={applicationPage:{attach:value=>{bridge=value;}}};
  const document={addEventListener:()=>{}};
  vm.runInNewContext(source,{window,document,Call:{ByName:(...args)=>{calls.push(args);return Promise.resolve();}},Clipboard:{SetText:()=>{}},Events:{On:(name,handler)=>{assert.equal(name,'desktop:action');event=handler;}},navigate:(...args)=>{navigation=args;}});
  assert.equal(calls.length,1);
  await bridge.info();await bridge.history(false);await bridge.check(true);await bridge.downloadStatus();await bridge.startDownload('v1.2.0',false);await bridge.cancelDownload(7);
  const prefix='github.com/DevWolk/garminctl/internal/containers/application/ui/native.Service.';
  assert.deepEqual(calls.slice(1),[[prefix+'Info'],[prefix+'History',false],[prefix+'Check',true],[prefix+'DownloadStatus'],[prefix+'StartDownload','v1.2.0',false],[prefix+'CancelDownload',7]]);
  await event({data:'application'});
  assert.deepEqual(navigation,['application','']);
});

function downloadClock() {
  let next=0;
  const tasks=new Map();
  return {setTimeout(fn){const id=++next;tasks.set(id,fn);return id;},clearTimeout(id){tasks.delete(id);},async tick(){const item=tasks.entries().next().value;assert.ok(item,'expected scheduled status read');tasks.delete(item[0]);return item[1]();},size:()=>tasks.size};
}
function idleDownload(){return {id:0,phase:'idle',active:false};}

test('native download shows counted bytes, remains canceling until cleanup and permits retry',async()=>{
  const clock=downloadClock();const ui=fixture(undefined,clock);
  let current=idleDownload(), starts=0,cancels=0;
  await ui.page.attach(bridge({downloadStatus:async()=>current,startDownload:async(tag,pre)=>{assert.equal(tag,release.tag);assert.equal(pre,false);starts++;return current={id:starts,tag,phase:'downloading',written:2,total:4,active:true};},cancelDownload:async id=>{assert.equal(id,current.id);cancels++;return current={...current,phase:'canceling'};}}));
  await ui.page.activate();
  assert.equal(starts,0);assert.equal(ui.get('download-start').disabled,false);
  await ui.get('download-start').fire('click');
  assert.equal(ui.get('download-progress').value,2);assert.equal(ui.get('download-progress').max,4);
  assert.equal(ui.get('check').disabled,true);assert.equal(ui.get('release').disabled,true);
  assert.match(ui.get('download-detail').textContent,/2 of 4 bytes/);
  assert.equal(ui.get('download-status').textContent,'');
  current={...current,phase:'verifying',written:4};await clock.tick();
  assert.equal(ui.get('download-progress').value,undefined);
  assert.match(ui.get('download-detail').textContent,/Verifying/);
  await ui.get('download-cancel').fire('click');
  assert.equal(cancels,1);assert.equal(ui.get('download-cancel').disabled,true);
  assert.equal(ui.get('download-start').disabled,true);
  current={...current,phase:'canceled',active:false};await clock.tick();
  assert.match(ui.get('download-status').textContent,/Download canceled/);
  assert.equal(ui.get('download-progress').hidden,true);assert.equal(ui.get('download-cancel').hidden,true);
  assert.equal(ui.get('download-start').disabled,false);
  await ui.get('download-start').fire('click');assert.equal(starts,2);
});

test('saved preparation after page reload is observed without starting another download',async()=>{
  const clock=downloadClock();const ui=fixture(undefined,clock);
  let current={id:7,tag:'v2.0.0-beta.1',phase:'downloading',written:3,total:4,active:true},starts=0;
  await ui.page.attach(bridge({downloadStatus:async()=>current,startDownload:async()=>{starts++;}}));
  await ui.page.activate();assert.equal(starts,0);
  assert.match(ui.get('download-release').textContent,/v2.0.0-beta.1/);
  current={...current,phase:'ready',written:4,active:false,result:{bytes:4}};await clock.tick();
  assert.match(ui.get('download-status').textContent,/Verified package saved/);
  assert.equal(ui.get('download-progress').value,4);assert.equal(ui.get('check').disabled,false);
  assert.equal(clock.size(),0);
});

test('disabled key, start error and failed status remain visible with recovery',async()=>{
  const ui=fixture();let current={...idleDownload(),disabledReason:'No trusted key'},statusFails=false;
  await ui.page.attach(bridge({downloadStatus:async()=>{if(statusFails)throw new Error('bridge offline');return current;},startDownload:async()=>{throw new Error('build mismatch');}}));
  await ui.page.activate();
  assert.equal(ui.get('download-start').disabled,true);assert.match(ui.get('download-status').textContent,/No trusted key/);
  current=idleDownload();await ui.page.activate();
  await ui.get('download-start').fire('click');assert.match(ui.get('download-status').textContent,/build mismatch/);
  statusFails=true;await ui.page.activate();assert.equal(ui.get('download-start').disabled,true);assert.match(ui.get('download-status').textContent,/Reopen this tab/);
  statusFails=false;await ui.page.activate();assert.equal(ui.get('download-start').disabled,false);
});

test('late status response cannot overwrite cancellation or a newer observation',async()=>{
  const clock=downloadClock();const ui=fixture(undefined,clock);
  let current=idleDownload(),defer=false,resolve;
  await ui.page.attach(bridge({downloadStatus:()=>defer?new Promise(done=>{resolve=done;}):Promise.resolve(current),startDownload:async()=>current={id:1,tag:release.tag,phase:'downloading',written:1,total:4,active:true},cancelDownload:async()=>current={...current,phase:'canceling'}}));
  await ui.page.activate();await ui.get('download-start').fire('click');
  defer=true;const polling=clock.tick();
  const stale={...current};await ui.get('download-cancel').fire('click');resolve(stale);await polling;
  assert.match(ui.get('download-detail').textContent,/Canceling/);
  assert.equal(ui.get('download-cancel').disabled,true);
  const older=clock.tick();
  defer=false;current={...current,phase:'canceled',active:false};await ui.page.activate();
  resolve(stale);await older;
  assert.match(ui.get('download-status').textContent,/Download canceled/);
  assert.equal(ui.get('download-start').disabled,false);
  assert.equal(clock.size(),0);
});
