'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const script = readFileSync(path.join(__dirname, '../internal/web/ui/application.js'), 'utf8');
const downloadScript = readFileSync(path.join(__dirname, '../internal/web/ui/application-download.js'), 'utf8');
const installScript = readFileSync(path.join(__dirname, '../internal/web/ui/application-install.js'), 'utf8');

// A deliberately small DOM port: unsafe HTML writes fail instead of parsing it.
class Element {
  constructor() { this.children = []; this.textContent = ''; this.hidden = false; this.disabled = false; this.checked = false; this.value = ''; this.events = new Map(); this.attributes = new Map(); }
  set innerHTML(_) { throw new Error('Untrusted HTML write'); }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; }
  setAttribute(name, value) { this.attributes.set(name, value); }
  removeAttribute(name) { this.attributes.delete(name); if (name === 'value') delete this.value; }
  addEventListener(name, callback) { this.events.set(name, callback); }
  fire(name, ...args) { return this.events.get(name)(...args); }
  showModal() { this.open = true; }
  close() { this.open = false; }
}
function fixture(fetch = async () => { throw new Error('Unexpected network'); }, clock = {}) {
  const nodes = new Map();
  const get = name => { if (!nodes.has(name)) nodes.set(name, new Element()); return nodes.get(name); };
  const window = {};
  const context = vm.createContext({window, document: {getElementById: id => get(id.replace('application-', '')), createElement: () => new Element()}, fetch, AbortSignal, Date, setTimeout: clock.setTimeout || (() => 1), clearTimeout: clock.clearTimeout || (() => {})});
  vm.runInContext(downloadScript, context);
  vm.runInContext(installScript, context);
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
  await bridge.installationStatus();await bridge.startInstallation({tag:'v1.2.0',cacheId:'cached'},{confirmed:true,editsResolved:true});await bridge.cancelInstallation(8);
  await bridge.savedPackages(true);
  await bridge.savedRollbacks(true);await bridge.startRollback({package:{tag:'v0.9.0',cacheId:'old'},snapshotId:'backup'});await bridge.confirmRollback(9,{data:{reviewId:'review'}});
  const prefix='github.com/DevWolk/garminctl/internal/containers/application/ui/native.Service.';
  assert.deepEqual(calls.slice(1),[[prefix+'Info'],[prefix+'History',false],[prefix+'Check',true],[prefix+'DownloadStatus'],[prefix+'StartDownload','v1.2.0',false],[prefix+'CancelDownload',7],[prefix+'InstallationStatus'],[prefix+'StartInstallation',{tag:'v1.2.0',cacheId:'cached'},{confirmed:true,editsResolved:true}],[prefix+'CancelInstallation',8],[prefix+'SavedPackages',true],[prefix+'SavedRollbacks',true],[prefix+'StartRollback',{package:{tag:'v0.9.0',cacheId:'old'},snapshotId:'backup'}],[prefix+'ConfirmRollback',9,{data:{reviewId:'review'}}]]);
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

function readyPackage() {return {id:1,tag:release.tag,phase:'ready',active:false,result:{tag:release.tag,cacheId:'retained-id',digest:'signed-digest',bytes:20}};}
function idleInstallation() {return {id:0,phase:'idle',active:false,canCancel:false,stopRequested:false};}

test('installation needs explicit confirmation and cannot start while merely visiting or selecting',async()=>{
  const ui=fixture();let starts=0,confirms=0;
  await ui.page.attach(bridge({downloadStatus:async()=>readyPackage(),installationStatus:async()=>idleInstallation(),confirmInstallation:async tag=>{assert.equal(tag,release.tag);confirms++;return false;},startInstallation:async()=>{starts++;}}));
  await ui.page.activate();
  assert.equal(starts,0);assert.equal(confirms,0);assert.equal(ui.get('install-start').disabled,false);
  await ui.get('install-start').fire('click');
  assert.equal(starts,0);assert.equal(confirms,1);assert.equal(ui.get('install-dialog').open,undefined);
  ui.get('release').value='1';await ui.get('release').fire('change');
  // An unavailable release cannot select a different package through stale download state.
  await ui.page.activate();assert.equal(starts,0);
});

test('confirmed install blocks edits, polls preparation and refuses cancellation after handoff',async()=>{
  const clock=downloadClock();const ui=fixture(undefined,clock);
  let current=idleInstallation(),starts=0,cancels=0;
  await ui.page.attach(bridge({downloadStatus:async()=>readyPackage(),installationStatus:async()=>current,confirmInstallation:async()=>true,startInstallation:async(request,consent)=>{
    assert.deepEqual(JSON.parse(JSON.stringify(request)),{tag:release.tag,cacheId:'retained-id',includePrerelease:false});
    assert.deepEqual(JSON.parse(JSON.stringify(consent)),{confirmed:true,editsResolved:true});
    starts++;return current={id:1,tag:release.tag,phase:'verifying',active:true,canCancel:true};
  },cancelInstallation:async()=>{cancels++;}}));
  await ui.page.activate();await ui.get('install-start').fire('click');
  assert.equal(starts,1);assert.equal(ui.get('install-dialog').open,true);assert.equal(ui.page.installing(),true);
  assert.equal(ui.get('download-start').disabled,true);assert.equal(ui.get('check').disabled,true);
  await ui.get('install-start').fire('click');assert.equal(starts,1);
  current={...current,phase:'handoff',canCancel:false};await clock.tick();
  let prevented=false;
  await ui.get('install-dialog').fire('cancel',{preventDefault(){prevented=true;}});
  assert.equal(prevented,true);assert.equal(cancels,0);assert.equal(ui.get('install-cancel').disabled,true);assert.equal(ui.get('install-dialog').open,true);
  current={...current,phase:'accepted',active:false,stopRequested:true};await clock.tick();
  assert.match(ui.get('install-detail').textContent,/Do not retry/);assert.equal(ui.page.installing(),true);
});

test('lost installation start reply is resolved by status without replay; cancellation waits for cleanup',async()=>{
  const clock=downloadClock();const ui=fixture(undefined,clock);
  let current=idleInstallation(),starts=0,cancels=0;
  await ui.page.attach(bridge({downloadStatus:async()=>readyPackage(),installationStatus:async()=>current,confirmInstallation:async()=>true,startInstallation:async()=>{
    starts++;current={id:1,tag:release.tag,phase:'backing-up',active:true,canCancel:true};throw new Error('lost reply');
  },cancelInstallation:async id=>{assert.equal(id,1);cancels++;return current={...current,phase:'canceling',canCancel:false};}}));
  await ui.page.activate();await ui.get('install-start').fire('click');
  assert.equal(ui.get('install-dialog').open,true);assert.match(ui.get('install-status').textContent,/Reading status/);
  await ui.get('install-start').fire('click');assert.equal(starts,1);
  await clock.tick();await ui.get('install-cancel').fire('click');
  assert.equal(cancels,1);assert.equal(ui.get('install-dialog').open,true);assert.equal(ui.page.installing(),true);
  current={...current,phase:'canceled',active:false};await clock.tick();
  assert.equal(ui.get('install-dialog').open,false);assert.equal(ui.page.installing(),false);assert.equal(starts,1);
});

test('reload observes installation without starting it; stopped failure requires closing the app',async()=>{
  const ui=fixture();let starts=0,closes=0;
  await ui.page.attach(bridge({downloadStatus:async()=>readyPackage(),installationStatus:async()=>({id:3,phase:'failed',active:false,stopRequested:true,error:'Shutdown deadline expired'}),startInstallation:async()=>{starts++;},closeWindow:async()=>{closes++;}}));
  await ui.page.activate();
  assert.equal(starts,0);assert.equal(ui.get('install-dialog').open,true);assert.match(ui.get('install-detail').textContent,/Close and restart/);
  assert.equal(ui.get('install-cancel').textContent,'Close application');
  await ui.get('install-cancel').fire('click');assert.equal(closes,1);assert.equal(starts,0);
});

test('desktop confirmation resolves unsaved edits and blocks reload while installation owns the window',async()=>{
  const source=readFileSync(path.join(__dirname,'../internal/desktop/desktop.js'),'utf8').replace(/^import .*;\n/,'');
  let bridge,event;const prompts=[],calls=[],pending=new Map([['setting',1]]);let installing=false,answer=false,reloads=0;
  const window={applicationPage:{attach:value=>{bridge=value;},installing:()=>installing}};
  vm.runInNewContext(source,{window,document:{addEventListener:()=>{}},Call:{ByName:(...args)=>{calls.push(args);return Promise.resolve();}},Clipboard:{SetText:()=>{}},Events:{On:(_,fn)=>{event=fn;}},state:{pending,appPending:new Map(),cloudPending:new Map()},confirmDialog:async value=>{prompts.push(value);return answer;},location:{reload(){reloads++;}}});
  assert.equal(await bridge.confirmInstallation(release.tag),false);assert.equal(prompts.length,1);
  answer=true;assert.equal(await bridge.confirmInstallation(release.tag),true);assert.equal(prompts.length,3);assert.equal(pending.size,1);
  assert.equal(await bridge.confirmRollbackPreparation('v0.9.0'),true);assert.equal(prompts.length,5);assert.equal(pending.size,1);assert.match(prompts[4].body,/No data is replaced/);
  installing=true;await event({data:'reload'});assert.equal(reloads,0);
  await event({data:'close'});assert.match(calls.at(-1)[0],/desktop.App.FinishClose$/);
});

test('saved packages remain selectable after app restart without network or a new download',async()=>{
  const ui=fixture();let starts=0,lists=0;
  await ui.page.attach(bridge({history:async()=>null,downloadStatus:async()=>idleDownload(),installationStatus:async()=>idleInstallation(),savedPackages:async pre=>{lists++;assert.equal(pre,false);return [{tag:release.tag,cacheId:'offline-id',prerelease:false}];},confirmInstallation:async()=>true,startInstallation:async req=>{starts++;assert.equal(req.cacheId,'offline-id');return {id:1,phase:'failed',active:false,error:'fixture refusal before handoff'};}}));
  await ui.page.activate();
  assert.equal(lists,1);assert.equal(starts,0);assert.equal(ui.get('install-package').children[0].textContent,release.tag);assert.equal(ui.get('install-start').disabled,false);
  await ui.get('install-start').fire('click');assert.equal(starts,1);
});

test('late installation status cannot re-enable cancellation after a newer cancel decision',async()=>{
  const clock=downloadClock();const ui=fixture(undefined,clock);
  let current=idleInstallation(),deferStatus=false,resolveStatus;
  await ui.page.attach(bridge({downloadStatus:async()=>readyPackage(),installationStatus:()=>deferStatus?new Promise(done=>{resolveStatus=done;}):Promise.resolve(current),confirmInstallation:async()=>true,startInstallation:async()=>current={id:1,tag:release.tag,phase:'verifying',active:true,canCancel:true},cancelInstallation:async()=>current={...current,phase:'canceling',canCancel:false}}));
  await ui.page.activate();await ui.get('install-start').fire('click');
  deferStatus=true;const observing=ui.page.activate();await Promise.resolve();
  await ui.get('install-cancel').fire('click');
  resolveStatus({id:1,tag:release.tag,phase:'verifying',active:true,canCancel:true});await observing;
  assert.equal(ui.get('install-cancel').disabled,true);assert.match(ui.get('install-detail').textContent,/Canceling preparation/);
});

function rollbackOption() {return {tag:'v0.9.0',cacheId:'old-package',snapshotId:'old-data',files:1,createdAt:'2026-11-01T10:00:00Z'};}
function rollbackReview() {return {reviewId:'review-a',from:'v1.0.0',to:'v0.9.0',targetSnapshotId:'old-data',safetySnapshotId:'new-data',changes:[{area:'config',path:'<img src=x onerror=alert(1)>',operation:'replace',bytes:12}]};}
function reviewingRollback() {return {id:4,kind:'rollback',tag:'v0.9.0',phase:'review',active:true,canCancel:true,review:rollbackReview()};}

test('rollback lists local choices and waits for an explicit reviewed data confirmation',async()=>{
  const clock=downloadClock(),ui=fixture(undefined,clock);
  let current=idleInstallation(),starts=0,confirms=0;
  await ui.page.attach(bridge({installationStatus:async()=>current,savedRollbacks:async()=>[rollbackOption()],confirmRollbackPreparation:async()=>true,startRollback:async req=>{
    starts++;assert.equal(req.package.cacheId,'old-package');assert.equal(req.snapshotId,'old-data');
    return current={id:4,kind:'rollback',phase:'verifying',active:true,canCancel:true};
  },confirmRollback:async(id,consent)=>{confirms++;assert.equal(id,4);assert.equal(consent.data.reviewId,'review-a');assert.equal(consent.data.confirmed,true);assert.equal(consent.installation.editsResolved,true);return current={...current,phase:'handoff',canCancel:false};}}));
  await ui.page.activate();
  assert.equal(starts,0);assert.equal(confirms,0);assert.equal(ui.get('install-rollback-start').disabled,false);
  await ui.get('install-rollback-start').fire('click');
  assert.equal(starts,1);assert.equal(confirms,0);assert.equal(ui.get('install-dialog').open,true);
  current=reviewingRollback();await clock.tick();
  assert.equal(ui.get('install-review').hidden,false);assert.equal(ui.get('install-review-confirm').disabled,true);
  assert.equal(ui.get('install-review-files').children.length,1);
  assert.match(ui.get('install-review-files').children[0].textContent,/<img src=x/);
  assert.match(ui.get('install-review-backups').textContent,/old-data.*new-data/);
  ui.get('install-review-consent').checked=true;await ui.get('install-review-consent').fire('change');
  await ui.get('install-review-confirm').fire('click');
  assert.equal(confirms,1);assert.equal(ui.get('install-cancel').disabled,true);
});

test('reload resumes rollback review without preparing or confirming; a changed review clears consent',async()=>{
  const clock=downloadClock(),ui=fixture(undefined,clock);let current=reviewingRollback(),starts=0,confirms=0;
  await ui.page.attach(bridge({installationStatus:async()=>current,startRollback:async()=>{starts++;},confirmRollback:async()=>{confirms++;}}));
  await ui.page.activate();
  assert.equal(starts,0);assert.equal(confirms,0);assert.equal(ui.get('install-dialog').open,true);
  assert.equal(ui.get('install-review-consent').checked,false);
  ui.get('install-review-consent').checked=true;await ui.get('install-review-consent').fire('change');
  current={...current,review:{...current.review,reviewId:'replacement-review'}};await clock.tick();
  assert.equal(ui.get('install-review-consent').checked,false);assert.equal(ui.get('install-review-confirm').disabled,true);
});

test('lost rollback confirmation reply polls status and never automatically confirms twice',async()=>{
  const clock=downloadClock(),ui=fixture(undefined,clock);let current=reviewingRollback(),confirms=0;
  await ui.page.attach(bridge({installationStatus:async()=>current,confirmRollback:async()=>{confirms++;current={...current,phase:'handoff',canCancel:false};throw new Error('lost confirmation reply');}}));
  await ui.page.activate();ui.get('install-review-consent').checked=true;await ui.get('install-review-consent').fire('change');
  await ui.get('install-review-confirm').fire('click');
  assert.equal(confirms,1);assert.equal(ui.get('install-review-confirm').disabled,true);assert.equal(ui.get('install-dialog').open,true);
  await clock.tick();assert.equal(confirms,1);assert.equal(ui.get('install-cancel').disabled,true);
});

test('rollback cancellation keeps the modal until cleanup; partial restore does not invite restart',async()=>{
  const clock=downloadClock(),ui=fixture(undefined,clock);let current=reviewingRollback(),cancels=0;
  await ui.page.attach(bridge({installationStatus:async()=>current,cancelInstallation:async()=>{cancels++;return current={...current,phase:'canceling',canCancel:false};}}));
  await ui.page.activate();
  await ui.get('install-dialog').fire('cancel',{preventDefault(){}});
  assert.equal(cancels,1);assert.equal(ui.get('install-dialog').open,true);assert.equal(ui.get('install-review-confirm').disabled,true);
  current={...current,phase:'canceled',active:false};await clock.tick();assert.equal(ui.get('install-dialog').open,false);
  current={...current,phase:'failed',stopRequested:true,restore:{attempted:true,completed:false,replaced:1,removed:0},error:'fixture partial failure'};
  await ui.page.activate();assert.equal(ui.get('install-dialog').open,true);assert.match(ui.get('install-detail').textContent,/recover manually/i);assert.doesNotMatch(ui.get('install-detail').textContent,/restart the app manually/i);
});

test('a lost rollback confirmation that was not delivered requires cancel before a new review',async()=>{
  const clock=downloadClock(),ui=fixture(undefined,clock);let current=reviewingRollback(),confirms=0;
  await ui.page.attach(bridge({installationStatus:async()=>current,confirmRollback:async()=>{confirms++;throw new Error('request not delivered');}}));
  await ui.page.activate();ui.get('install-review-consent').checked=true;await ui.get('install-review-consent').fire('change');
  await ui.get('install-review-confirm').fire('click');await clock.tick();
  assert.equal(confirms,1);assert.equal(ui.get('install-review-confirm').disabled,true);assert.equal(ui.get('install-cancel').disabled,false);
});

test('corrupt rollback metadata does not disable an otherwise valid ordinary update',async()=>{
  const ui=fixture();
  await ui.page.attach(bridge({downloadStatus:async()=>idleDownload(),installationStatus:async()=>idleInstallation(),savedPackages:async()=>[{tag:release.tag,cacheId:'available-update'}],savedRollbacks:async()=>{throw new Error('corrupt backup metadata');},startInstallation:async()=>{throw new Error('not clicked');},confirmInstallation:async()=>true}));
  await ui.page.activate();
  assert.equal(ui.get('install-start').disabled,false);assert.equal(ui.get('install-rollback-start').disabled,true);
  assert.match(ui.get('install-rollback-note').textContent,/corrupt backup metadata/);
});
