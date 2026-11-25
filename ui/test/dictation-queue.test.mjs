import test from 'node:test';
import assert from 'node:assert/strict';
import { DictationQueue, DICTATION_SEND_MS } from '../src/lib/dictation-queue.ts';
class Disk {
 rows=new Map();
 async put(c){this.rows.set(c.id,structuredClone(c));}
 async list(){return structuredClone([...this.rows.values()]);}
 async remove(id){this.rows.delete(id);}
}
const clip=(id='first')=>({id,scope:'computer',target:{id:'chat:a'},data:'audio',created:1});
for(const failure of [422,401,502,0,'empty','poll']) test(`failure ${failure} is deleted and three later clips arrive in order`,async()=>{
 const disk=new Disk(), notices=[], inserted=[];
 const q=new DictationQueue(disk,{
  transcribe:async c=>{
   if(c.id==='bad'){
    if(failure==='empty')return {text:''};
    if(failure==='poll')return {transcript_id:'pending'};
    throw Object.assign(new Error('failed'),{status:failure});
   }
   return {text:c.id};
  },transcript:async()=>({state:'failed',text:'',error:'failed'})
 },c=>notices.push(c));
 await q.enqueue(clip('bad'));
 await q.flush('computer');
 if(failure==='poll')await q.flush('computer');
 assert.deepEqual(await disk.list(),[]);
 assert.equal(notices.length,1);assert.equal(notices[0].data,'');
 for(let i=1;i<=3;i++)await q.enqueue({...clip(`good${i}`),created:i+1});
 const deliver=c=>{inserted.push(c.text);return true;};
 await q.flush('computer',deliver);await q.flush('computer',deliver);
 assert.deepEqual(inserted,['good1','good2','good3']);
 assert.deepEqual(await disk.list(),[], 'neither failed nor successful audio stays');
});
test('held words go only to transient review, never to disk or automatic delivery',async()=>{
 const disk=new Disk(), notices=[];
 const q=new DictationQueue(disk,{transcribe:async()=>({text:'unsure words',held:true,held_reason:'suspect'})},c=>notices.push(c));
 await q.enqueue(clip());await q.flush('computer',()=>assert.fail('must review'));
 assert.deepEqual(await disk.list(),[]);
 assert.equal(notices[0].text,'unsure words');assert.equal(notices[0].heldReason,'suspect');assert.equal(notices[0].data,'');
 await new DictationQueue(disk,{}).flush('computer',()=>assert.fail('nothing survives reload'));
});
test('pending transcription never blocks later same-target text',async()=>{
 const disk=new Disk(),inserted=[];
 let q=new DictationQueue(disk,{transcribe:async c=>c.id==='first'?{transcript_id:'p'}:{text:c.id}});
 await q.enqueue(clip());await q.enqueue({...clip('second'),created:2});
 await q.flush('computer',c=>{inserted.push(c.text);return true;});assert.deepEqual(inserted,['second']);
 q=new DictationQueue(disk,{transcribe:async()=>assert.fail('no reupload'),transcript:async()=>({state:'done',text:'first'})});
 await q.flush('computer',c=>{inserted.push(c.text);return true;});
 assert.deepEqual(inserted,['second','first']);
 assert.deepEqual(await disk.list(),[]);
});
test('a fallback note is shown once after normal delivery, the text itself unchanged',async()=>{
 const disk=new Disk(), notices=[], inserted=[];
 const q=new DictationQueue(disk,{transcribe:async()=>({text:'hello fae',note:'Fae unreachable, used local'})},c=>notices.push(c));
 await q.enqueue(clip());
 await q.flush('computer',c=>{inserted.push(c.text);return true;});
 assert.deepEqual(inserted,['hello fae']); // delivered normally: a note never holds or edits the words
 assert.deepEqual(await disk.list(),[]);
 assert.equal(notices.length,1);
 assert.equal(notices[0].delivered,true);
 assert.equal(notices[0].note,'Fae unreachable, used local');
 assert.equal(notices[0].text,'hello fae');
 assert.ok(!notices[0].held);
});
test('a clip without a note delivers with no notice at all',async()=>{
 const disk=new Disk(), notices=[];
 const q=new DictationQueue(disk,{transcribe:async()=>({text:'plain words'})},c=>notices.push(c));
 await q.enqueue(clip());
 await q.flush('computer',()=>true);
 assert.equal(notices.length,0);
});
test('concurrent passes do not upload a clip twice',async()=>{
 const disk=new Disk();let resolve,calls=0;const wait=new Promise(r=>resolve=r);
 const q=new DictationQueue(disk,{transcribe:async()=>{calls++;await wait;return {text:'words'};}});
 await q.enqueue(clip());const first=q.flush('computer');await q.flush('computer');resolve();await first;assert.equal(calls,1);
});


test('migration/reset invalidates late requests so old clips cannot reappear',async()=>{
 const disk=new Disk();let release;const wait=new Promise(r=>release=r);
 const q=new DictationQueue(disk,{transcribe:async()=>{await wait;return {text:'old words'};}});
 await q.enqueue(clip());const flushing=q.flush('computer',()=>assert.fail('old words must not deliver'));
 await new Promise(r=>setImmediate(r));await q.reset();release();await flushing;
 assert.deepEqual(await disk.list(),[]);
});

test('pending item expires without polling again or blocking later words',async()=>{
 const disk=new Disk(),notices=[],inserted=[];let polls=0;
 const q=new DictationQueue(disk,{transcribe:async()=>({text:'later'}),transcript:async()=>{polls++;return {state:'pending'};}},c=>notices.push(c));
 await q.enqueue({...clip(),transcriptId:'p',pendingUntil:Date.now()-1,data:''});
 await q.enqueue(clip('second'));
 await q.flush('computer',c=>{inserted.push(c.text);return true;});
 assert.equal(polls,0);assert.equal(notices.length,1);assert.equal(notices[0].data,'');assert.deepEqual(inserted,['later']);assert.deepEqual(await disk.list(),[]);
});

test('a hung pending poll is dropped at its deadline',async()=>{
 const disk=new Disk(),notices=[];
 const q=new DictationQueue(disk,{transcript:()=>new Promise(()=>{})},c=>notices.push(c));
 await q.enqueue({...clip(),data:'',transcriptId:'p',pendingUntil:Date.now()+20});
 await q.flush('computer');assert.deepEqual(await disk.list(),[]);assert.equal(notices.length,1);
});

test('stalled send aborts at 15 seconds; newly queued dictation proceeds immediately without replay',async t=>{
 t.mock.timers.enable({apis:['setTimeout']});
 const disk=new Disk(),notices=[],sent=[];let stalledSignal,release;
 const q=new DictationQueue(disk,{transcribe:async c=>({text:c.id})},c=>notices.push(c));
 await q.enqueue({...clip('stalled'),text:'first words',data:''});
 const deliver=(c,signal)=>{sent.push(c.id);if(c.id==='stalled'){stalledSignal=signal;return new Promise(r=>release=r);}return true;};
 const flushing=q.flush('computer',deliver);
 await new Promise(r=>setImmediate(r));
 await q.enqueue({...clip('next'),created:2});
 await q.flush('computer',deliver);assert.deepEqual(sent,['stalled']);
 t.mock.timers.tick(DICTATION_SEND_MS-1);await new Promise(r=>setImmediate(r));assert.equal(stalledSignal.aborted,false);
 t.mock.timers.tick(1);await flushing;
 assert.equal(stalledSignal.aborted,true);assert.deepEqual(sent,['stalled','next']);
 assert.equal(notices.length,1);assert.equal(notices[0].heldReason,'send_uncertain');assert.equal(notices[0].text,'first words');assert.equal(notices[0].data,'');
 assert.deepEqual(await disk.list(),[]);
 release(true);await new Promise(r=>setImmediate(r));await q.flush('computer',deliver);
 assert.deepEqual(sent,['stalled','next']);assert.equal(notices.length,1);
});
