const { test } = require('node:test');
const assert = require('node:assert/strict');
const Controller = require('../internal/web/static/cast-controller.js');
const deferred = () => { let resolve, reject; const promise = new Promise((yes,no) => { resolve=yes; reject=no; }); return {promise,resolve,reject}; };
const flush = async () => { for(let i=0;i<20;i++) await Promise.resolve(); };
function fixture() {
  let now=0, sequence=0, creates=0, active=0, maxActive=0;
  const timers=new Map(), loads=[], removed=[], stopped=[];
  const picker=deferred();
  const data={mode:'slideshow',title:'Photos',expires:new Date(3600000).toISOString(),cleanup:'/remove/one',entries:[0,1,2,3].map(i=>({name:`${i}.jpg`,url:`media-${i}`,mime:'image/jpeg'}))};
  const clock={now:()=>now,setTimeout:(fn,delay)=>{const id=++sequence;timers.set(id,{fn,at:now+delay});return id;},clearTimeout:id=>timers.delete(id)};
  const adapter={pick:()=>picker.promise,load:(op,entry)=>{const d=deferred();loads.push({op,entry,...d});active++;maxActive=Math.max(maxActive,active);return d.promise.finally(()=>active--);},stop:async op=>stopped.push(op),toggle:()=>{},reconnect:()=>true};
  const api={create:async()=>{creates++;return structuredClone(data);},remove:async id=>removed.push(id)};
  const c=new Controller(adapter,api,clock,()=>{});
  return {c,adapter,api,clock,data,picker,timers,loads,removed,stopped,get creates(){return creates;},get maxActive(){return maxActive;},
    async begin(){const p=c.start({});picker.resolve({});await p;await flush();},
    async tick(ms){now+=ms;for(const [id,t] of [...timers])if(t.at<=now){timers.delete(id);t.fn();}await flush();}};
}
test('canceled picker creates no share; preparation waits for device selection',async()=>{
 const f=fixture();const p=f.c.start({});assert.equal(f.creates,0);
 f.picker.reject({cancelled:true});await p;assert.equal(f.creates,0);assert.equal(f.loads.length,0);assert.equal(f.c.operation,null);
});
test('coalesces rapid navigation and arms one timer after latest load',async()=>{
 const f=fixture();await f.begin();assert.equal(f.loads.length,1);assert.equal(f.timers.size,1);
 f.c.move(1);f.c.move(1);f.c.move(-1);f.c.move(1);assert.equal(f.loads.length,1);
 f.loads[0].resolve();await flush();assert.equal(f.loads.length,2);assert.equal(f.loads[1].entry.name,'2.jpg');
 f.loads[1].resolve();await flush();assert.equal(f.c.operation.displayed,2);assert.equal(f.timers.size,1);
 await f.tick(9999);assert.equal(f.loads.length,2);await f.tick(1);assert.equal(f.loads.length,3);assert.equal(f.maxActive,1);
});
test('pause/manual navigation, interval changes, visibility and explicit resume',async()=>{
 const f=fixture();await f.begin();f.loads[0].resolve();await flush();f.c.toggle();
 f.c.move(1);f.loads[1]?.resolve();await flush();f.loads[1].resolve();await flush();
 assert.equal(f.c.operation.playing,false);await f.tick(20000);assert.equal(f.loads.length,2);
 f.c.toggle();f.c.interval(2);await f.tick(1999);assert.equal(f.loads.length,2);await f.tick(1);assert.equal(f.loads.length,3);
 f.c.visibility(true);f.loads[2].resolve();await flush();await f.tick(50000);assert.equal(f.loads.length,3);
 f.c.visibility(false);await f.tick(50000);assert.equal(f.loads.length,3);f.c.toggle();await f.tick(2000);assert.equal(f.loads.length,4);
});
test('loop, shuffle and one-entry playback preserve the selected image',async()=>{
 const f=fixture();f.data.entries=f.data.entries.slice(0,1);await f.begin();f.loads[0].resolve();await flush();
 await f.tick(10000);assert.equal(f.c.operation.playing,false);assert.equal(f.loads.length,1);
 f.c.operation.loop=true;f.c.toggle();f.c.shuffle(true);assert.equal(f.c.operation.order.length,1);
 await f.tick(10000);assert.equal(f.loads.length,2);f.loads[1].resolve();await flush();assert.equal(f.c.operation.displayed,0);
});
test('Stop revokes immediately even while LOAD is hung; stale callbacks cannot restart',async()=>{
 const f=fixture();await f.begin();f.c.move(1);f.c.stop();assert.deepEqual(f.removed,['/remove/one']);assert.equal(f.timers.size,0);
 f.loads[0].resolve();await flush();assert.equal(f.c.operation,null);assert.equal(f.loads.length,1);assert.equal(f.stopped.length,1);
});
test('replacement waits for the old LOAD and cleans only the old share',async()=>{
 const f=fixture();await f.begin();f.data.cleanup='/remove/two';f.data.mode='video';f.data.media={name:'movie.mp4',url:'video',mime:'video/mp4'};
 await f.c.start({});await flush();assert.equal(f.loads.length,1);assert.deepEqual(f.removed,['/remove/one']);
 f.loads[0].reject(new Error('old image failed'));await flush();assert.equal(f.loads.length,2);assert.equal(f.loads[1].entry.url,'video');
 f.loads[1].resolve();await flush();assert.equal(f.c.operation.data.cleanup,'/remove/two');assert.equal(f.timers.size,1);assert.equal(f.maxActive,1);assert.equal(f.stopped.length,0);
});
test('a late creation response cleans its own share after Stop',async()=>{
 const f=fixture();const response=deferred();f.api.create=()=>response.promise;const p=f.c.start({});f.picker.resolve({});await flush();f.c.stop();
 response.resolve(f.data);await p;assert.deepEqual(f.removed,['/remove/one']);assert.equal(f.loads.length,0);
});
test('failed initial LOAD revokes; later failure pauses for retry; authorization stops',async()=>{
 const f=fixture();await f.begin();f.loads[0].reject(new Error('unsupported'));await flush();assert.equal(f.c.operation,null);assert.equal(f.removed.length,1);
 const g=fixture();await g.begin();g.loads[0].resolve();await flush();g.c.move(1);await flush();g.loads[1].reject(new Error('bad image'));await flush();
 assert.equal(g.c.operation.playing,false);assert.match(g.c.message,/1.jpg/);assert.equal(g.loads.length,2);
 g.c.retry();await flush();assert.equal(g.loads[2].entry.name,'1.jpg');g.loads[2].reject({authorization:true});await flush();assert.equal(g.c.operation,null);assert.equal(g.removed.length,1);
});
test('expiry clears pending state and disconnect never auto-resumes',async()=>{
 const f=fixture();f.data.expires=new Date(5000).toISOString();await f.begin();await f.tick(5000);assert.equal(f.c.operation,null);assert.equal(f.removed.length,1);
 const g=fixture();await g.begin();g.c.disconnect();g.loads[0].resolve();await flush();assert.equal(g.c.operation.displayed,null);assert.equal(g.c.operation.playing,false);
 await g.tick(20000);assert.equal(g.loads.length,1);g.c.toggle();await flush();assert.equal(g.loads.length,2);
});
test('deferred shuffle cannot change the identity of an in-flight selection',async()=>{
 const f=fixture();await f.begin();f.c.shuffle(true);f.loads[0].resolve();await flush();assert.equal(f.c.entry(f.c.operation,f.c.operation.displayed).name,'0.jpg');assert.equal(f.maxActive,1);
});
test('expiry remains armed during rapid selections and a hung LOAD',async()=>{
 const f=fixture();f.data.expires=new Date(1000).toISOString();await f.begin();f.c.move(1);f.c.move(1);assert.equal(f.timers.size,1);
 await f.tick(1000);assert.equal(f.c.operation,null);assert.equal(f.removed.length,1);
});
test('share cleanup failure cannot prevent stopping the owned receiver media',async()=>{
 const f=fixture();f.api.remove=async()=>{throw new Error('offline');};await f.begin();f.loads[0].resolve();await flush();f.c.stop();await flush();
 assert.equal(f.stopped.length,1);assert.match(f.c.message,/cleanup failed/);assert.equal(f.c.operation,null);
});
test('public-share Stop never invokes management revocation; empty lists fail clearly',async()=>{
 const f=fixture();delete f.data.cleanup;await f.begin();f.loads[0].resolve();await flush();f.c.stop();await flush();assert.equal(f.removed.length,0);
 const g=fixture();g.data.entries=[];await g.begin();assert.equal(g.c.operation,null);assert.match(g.c.message,/no JPEG or PNG/);assert.equal(g.removed.length,1);
});
test('hiding while playback is being prepared starts the slideshow paused',async()=>{
 const f=fixture();const response=deferred();f.api.create=()=>response.promise;const p=f.c.start({});f.picker.resolve({});await flush();f.c.visibility(true);
 response.resolve(f.data);await p;await flush();f.loads[0].resolve();await flush();f.c.visibility(false);
 assert.equal(f.c.operation.playing,false);await f.tick(20000);assert.equal(f.loads.length,1);
});

// Model the adapter's ownership check: Stop can affect only the operation
// whose LOAD actually supplied the receiver's current media.
function trackReceiver(f) {
  let owner = null;
  const load = f.adapter.load;
  f.adapter.load = (op, entry) => load(op, entry).then(() => { owner = op; });
  f.adapter.stop = async op => {
    if (owner === op) { f.stopped.push(op); owner = null; }
  };
  return { get owner() { return owner; } };
}
for (const failure of ['picker cancellation', 'preparation failure']) {
  test(`an aborted replacement stops previous owned media after ${failure}`, async () => {
    const f = fixture();
    const receiver = trackReceiver(f);
    await f.begin();
    const previous = f.c.operation;
    f.loads[0].resolve();
    await flush();
    assert.equal(receiver.owner, previous);
    const picker = deferred();
    f.adapter.pick = () => picker.promise;
    if (failure === 'preparation failure') f.api.create = async () => { throw new Error('unavailable'); };
    const replacement = f.c.start({});
    await flush(); // The old operation's scheduled Stop sees the replacement.
    if (failure === 'picker cancellation') picker.reject({ cancelled: true });
    else picker.resolve({});
    await replacement;
    await flush();
    assert.equal(f.c.operation, null);
    assert.equal(receiver.owner, null);
    assert.deepEqual(f.stopped, [previous]);
    assert.deepEqual(f.removed, ['/remove/one']);
  });
}
test('canceling a replacement stops an old LOAD that settles after cancellation', async () => {
  const f = fixture();
  const receiver = trackReceiver(f);
  await f.begin();
  const previous = f.c.operation;
  const picker = deferred();
  f.adapter.pick = () => picker.promise;
  const replacement = f.c.start({});
  picker.reject({ cancelled: true });
  await replacement;
  assert.equal(f.stopped.length, 0);
  f.loads[0].resolve();
  await flush();
  assert.equal(receiver.owner, null);
  assert.deepEqual(f.stopped, [previous]);
});
test('late aborted-replacement work cannot stop newer media', async () => {
  const f = fixture();
  const receiver = trackReceiver(f);
  await f.begin();
  f.loads[0].resolve();
  await flush();
  const response = deferred();
  f.api.create = () => response.promise;
  const replacement = f.c.start({});
  await flush();
  const latestData = structuredClone(f.data);
  latestData.cleanup = '/remove/latest';
  latestData.entries.forEach(entry => { entry.url += '-latest'; });
  f.api.create = async () => latestData;
  await f.c.start({});
  await flush();
  const latest = f.c.operation;
  f.loads[1].resolve();
  await flush();
  const abandonedData = structuredClone(f.data);
  abandonedData.cleanup = '/remove/abandoned';
  response.resolve(abandonedData);
  await replacement;
  await flush();
  assert.equal(receiver.owner, latest);
  assert.equal(f.stopped.length, 0);
  assert.deepEqual(f.removed, ['/remove/one', '/remove/abandoned']);
  f.c.stop();
  await flush();
  assert.equal(receiver.owner, null);
  assert.deepEqual(f.stopped, [latest]);
});
test('an empty public manifest with omitted entries shows the no-pictures message', async () => {
  const f = fixture();
  delete f.data.entries; // Matches the server's omitempty JSON representation.
  delete f.data.cleanup; // Existing public shares must never be revoked.
  await f.begin();
  assert.equal(f.c.operation, null);
  assert.match(f.c.message, /no JPEG or PNG/);
  assert.equal(f.loads.length, 0);
  assert.equal(f.removed.length, 0);
});
test('Previous at the first picture preserves the scheduled advancement', async () => {
  const f = fixture();
  await f.begin();
  f.loads[0].resolve();
  await flush();
  await f.tick(4000);
  const timer = f.c.operation.timer;
  f.c.move(-1);
  assert.equal(f.c.operation.playing, true);
  assert.equal(f.c.operation.timer, timer);
  await f.tick(5999);
  assert.equal(f.loads.length, 1);
  await f.tick(1);
  assert.equal(f.loads.length, 2);
  assert.equal(f.loads[1].entry.name, '1.jpg');
});
test('Next at the last picture preserves automatic slideshow completion', async () => {
  const f = fixture();
  await f.begin();
  f.loads[0].resolve();
  await flush();
  f.c.move(3);
  await flush();
  f.loads[1].resolve();
  await flush();
  const timer = f.c.operation.timer;
  f.c.move(1);
  assert.equal(f.c.operation.timer, timer);
  assert.equal(f.c.operation.playing, true);
  await f.tick(10000);
  assert.equal(f.c.operation.playing, false);
  assert.match(f.c.message, /Slideshow finished/);
  assert.equal(f.loads.length, 2);
  assert.equal(f.timers.size, 1); // Expiry remains enforced after completion.
});
