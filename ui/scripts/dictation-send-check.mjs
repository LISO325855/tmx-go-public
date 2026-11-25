// Phone-browser reproduction using the real Composer, DictateButton, session and IndexedDB queue.
// Compare build 308 with the working tree; API response timing is controlled explicitly.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import path from 'node:path';
import { createServer } from 'node:http';
import { build } from 'esbuild';
import { chromium } from 'playwright';
const root = path.resolve(import.meta.dirname, '..');
const src = path.join(root, 'src');
const oldComposer = execFileSync('git', ['show', '36e051c2:ui/src/components/conversation/composer.tsx'], { encoding: 'utf8', windowsHide: true, cwd: root });
const oldQueue = execFileSync('git', ['show', '36e051c2:ui/src/lib/dictation-queue.ts'], { encoding: 'utf8', windowsHide: true, cwd: root });
const stubs = {
 'lib/api.ts': `export const api = { send: (...args) => { window.sent.push(args); return new Promise(resolve => window.completeSend = resolve); }, agentAction: async () => {}, typeText: async () => {} };`,
 'lib/store.ts': `export const useStore = fn => fn({status:'ready'});`,
 'lib/voice.ts': `export const useVoice = Object.assign(fn => fn({loaded: true, status: null}), {getState: () => ({status: null})}); export const loadVoice = async () => {}; export const voiceApi = {};`,
 'lib/router.ts': `export const navigate = () => {}; export const paths = {agent: id => '/agents/' + id, settings: () => '/settings'};`,
 'lib/dictation-outbox.ts': `export const useDictationOutbox = fn => fn({clips:[]});
   export const setDictationDelivery = fn => window.deliverClip = fn;
   export const flushDictationOutbox = async () => {};
   export const dictationScope = () => location.origin;
   export const keepDictation = async () => {};
   export const captureSentDictations = (target, body) => { const ids = window.queue.captureSent(location.origin, target, body); return () => { window.cleanup = window.queue.acknowledge(ids); }; };`,
 'hooks/use-classic.ts': `export const useClassic = () => false;`,
 'lib/utils.ts': `export const cn = (...v) => v.filter(Boolean).join(' ');`,
 'lib/terminal-keys.ts': `export const hasLiveScreen = () => false;`,
 'lib/attachments.ts': `export const MAX_FILE_BYTES = 1000; export const MAX_FILES = 4;`,
 'components/attach.tsx': `export const AttachButton = () => null; export const AttachChips = () => null; export const DropOverlay = () => null; export const useAttachments = () => ({readyIds:[], files:[], dropProps:{}, clear:()=>{}});`,
 'components/ui/button.tsx': `import React from 'react'; export const Button = React.forwardRef(({variant,size,asChild,...props},ref) => <button ref={ref} {...props}/>);`,
 'components/ui/tooltip.tsx': `export const Tooltip = ({children}) => children; export const TooltipTrigger = ({children}) => children; export const TooltipContent = () => null;`,
};
const scripts = {};
for (const variant of ['308', 'fixed']) {
 const result = await build({
  stdin: { contents: `import React from 'react'; import {createRoot} from 'react-dom/client';
   import {Composer} from './src/components/conversation/composer';
   import {DictationQueue, IndexedDictations} from './src/lib/dictation-queue';
   window.sent = []; window.storage = new IndexedDictations();
   let transcript = ''; window.queue = new DictationQueue(window.storage, {transcribe: async () => ({text:transcript})});
   window.transcribe = async text => { transcript = text; await window.queue.enqueue({id:crypto.randomUUID(),scope:location.origin,target:{id:'agent:head',label:'Head',path:'/agents/head'},data:'saved-audio',created:Date.now()}); await window.queue.flush(location.origin, window.deliverClip); };
   window.flush = () => window.queue.flush(location.origin, window.deliverClip);
   const root = createRoot(document.getElementById('root')); let n = 0;
   window.remount = () => root.render(<Composer key={++n} agent={{id:'head',name:'Head',harness:'codex',mode:'terminal',state:'idle'}}/>);
   window.remount();`, resolveDir:root, loader:'tsx' },
  bundle:true, write:false, format:'esm', jsx:'automatic',
  plugins:[{name:'controlled-services', setup(b) {
   b.onResolve({filter:/^@\//}, a => { const base = path.join(src,a.path.slice(2)); const file = ['.ts','.tsx'].map(ext=>base+ext).find(existsSync); if(file) return {path:file}; });
   b.onResolve({filter:/^sonner$/}, () => ({path:'sonner',namespace:'stub'}));
   b.onLoad({filter:/.*/,namespace:'stub'},()=>({contents:'export const toast = Object.assign(()=>{}, {error:()=>{}});'}));
   b.onLoad({filter:/\.[jt]sx?$/}, async a => {
    const rel=path.relative(src,a.path).replaceAll('\\','/');
    if(variant==='308' && rel==='components/conversation/composer.tsx') return {contents:oldComposer,loader:'tsx'};
    if(variant==='308' && rel==='lib/dictation-queue.ts') return {contents:oldQueue,loader:'ts'};
    if(stubs[rel]) return {contents:stubs[rel],loader:rel.endsWith('tsx')?'tsx':'ts'};
   });
  }}],
 });
 scripts[variant]=result.outputFiles[0].text;
}
const server=createServer((req,res)=>{
 const variant=req.url.includes('308')?'308':'fixed';
 res.setHeader('Content-Type',req.url.endsWith('.js')?'text/javascript':'text/html');
 res.end(req.url.endsWith('.js')?scripts[variant]:`<!doctype html><meta name="viewport" content="width=device-width, initial-scale=1"><div id="root"></div><script type="module" src="/${variant}.js"></script>`);
});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
let browser;
try {
 browser=await chromium.launch({headless:true});
 for(const variant of ['308','fixed']) {
  const context=await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true});
  const page=await context.newPage();
  await page.goto(`http://127.0.0.1:${server.address().port}/${variant}`);
  const box=page.getByRole('textbox',{name:'Message Head'});
  await box.fill('typed before transcript');
  await page.getByRole('button',{name:'Send',exact:true}).click();
  await page.waitForFunction(()=>window.completeSend);
  const transcript='These are the dictated words. '.repeat(6).slice(0,160);
  await page.evaluate(text=>window.transcribe(text),transcript);
  assert.ok((await box.inputValue()).includes(transcript.trim()),'real insertion reaches textarea');
  await page.evaluate(()=>window.completeSend());
  await page.waitForFunction(()=>!document.querySelector('button[aria-label="Send"]').disabled || document.querySelector('textarea').value==='');
  if(variant==='308') {
   assert.equal(await box.inputValue(),'','308 wipes late transcript on send completion');
   await page.evaluate(()=>window.flush());
   assert.equal(await box.inputValue(),'','delivered flag prevents automatic reinsertion');
   const saved=await page.evaluate(()=>window.storage.list());
   assert.equal(saved[0].text,transcript);
   assert.equal(saved[0].delivered,true);
   assert.equal(await page.evaluate(()=>window.sent[0][1]),'typed before transcript');
   await page.reload();
   await box.waitFor();
   await page.evaluate(()=>window.flush());
   assert.equal(await box.inputValue(),'','reload loses in-memory draft and delivered copy is not reinserted');
   assert.equal((await page.evaluate(()=>window.storage.list()))[0].text,transcript);
   console.log('308 reproduced: transcript inserted, then wiped; not sent; survives only in delivered=true IndexedDB copy.');
  } else {
   assert.ok((await box.inputValue()).includes(transcript.trim()));
   await page.evaluate(()=>window.remount());
   await page.waitForFunction(()=>document.querySelector('textarea').value.includes('dictated words'));
   await page.getByRole('button',{name:'Send',exact:true}).click();
   await page.evaluate(()=>window.completeSend());
   await page.waitForFunction(()=>document.querySelector('textarea').value==='');
   await page.evaluate(()=>window.cleanup);
   assert.deepEqual(await page.evaluate(()=>window.storage.list()),[]);
   assert.ok((await page.evaluate(()=>window.sent[1][1])).includes(transcript.trim()));
   console.log('Fixed: late transcript survives response and remount; next send includes it and removes its saved copy.');
  }
  await context.close();
 }
} finally { await browser?.close(); await new Promise(resolve=>server.close(resolve)); }
