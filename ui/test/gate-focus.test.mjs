import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
import {trapGateFocus,rememberGateFocus,restoreGateFocus,guardTerminalForGate} from '../src/lib/gate-focus.ts';
const read=p=>readFileSync(new URL('../src/'+p,import.meta.url),'utf8');
function fixture(){
 const listeners=new Map();
 const doc={activeElement:null,addEventListener(k,f){listeners.set(k,f);},removeEventListener(k){listeners.delete(k);}};
 const element=(terminal=false)=>({isConnected:true,closest:()=>terminal?{}:null,blur(){if(doc.activeElement===this)doc.activeElement=null;},focus(){doc.activeElement=this;listeners.get('focusin')?.({target:this,preventDefault(){},stopImmediatePropagation(){}});}});
 const pin=element(),outside=element(true),cancel=element(),draft=element();
 const root={ownerDocument:doc,contains:e=>[root,pin,cancel].includes(e),querySelector:()=>pin,focus(){pin.focus();}};
 return {doc,root,pin,outside,cancel,draft,listeners};
}
test('gate focuses PIN and traps focus/keys; terminal focus is never restored',()=>{
 const f=fixture();globalThis.document=f.doc;f.outside.focus();rememberGateFocus();
 const cleanup=trapGateFocus(f.root);assert.equal(f.doc.activeElement,f.pin);
 f.outside.focus();assert.equal(f.doc.activeElement,f.pin,'programmatic terminal focus is pulled back');
 let stopped=false,prevented=false;
 f.listeners.get('keydown')({target:f.outside,preventDefault(){prevented=true;},stopImmediatePropagation(){stopped=true;}});
 assert.ok(stopped&&prevented);assert.equal(f.doc.activeElement,f.pin);
 cleanup();f.doc.activeElement=null;restoreGateFocus();assert.equal(f.doc.activeElement,null);
 f.draft.focus();rememberGateFocus();const clean=trapGateFocus(f.root);clean();restoreGateFocus();assert.equal(f.doc.activeElement,f.draft);
 delete globalThis.document;
});
test('gate synchronously disables terminal stdin and blocks its actual data callback',()=>{
 let confirming=null,subscriber,blurs=0,input;const writes=[];
 const gate={getState:()=>({confirming}),subscribe(fn){subscriber=fn;return ()=>subscriber=null;}};
 const terminal={options:{},blur(){blurs++;},onData(fn){input=fn;}};
 const unguard=guardTerminalForGate(terminal,gate);
 const source=read('components/terminal-view.tsx');
 const start=source.indexOf('    const sub = t.onData('),end=source.indexOf('\n    });',start)+8;
 vm.runInNewContext(source.slice(start,end),{t:terminal,useGate:gate,agentOfTerminalPath:()=>undefined,path:'/terminal',useStore:{getState:()=>({agents:{}})},session:{write:d=>writes.push(d)}});
 confirming={method:'pin'};subscriber();assert.equal(terminal.options.disableStdin,true);assert.equal(blurs,1);
 input('synthetic-input');assert.deepEqual(writes,[]);
 confirming={method:'os'};subscriber();input('synthetic-input');assert.deepEqual(writes,[]);
 confirming=null;subscriber();assert.equal(terminal.options.disableStdin,false);assert.equal(blurs,2);input('normal command');assert.deepEqual(writes,['normal command']);unguard();
 assert.match(source,/focusTick && !useGate.getState\(\).confirming/);
 assert.match(source,/autoFocus && !useGate.getState\(\).confirming/);
 const dialog=read('components/owner-confirm.tsx');assert.equal((dialog.match(/<Dialog modal /g)||[]).length,2);assert.match(dialog,/onCloseAutoFocus:[\s\S]*?preventDefault\(\)/);assert.match(dialog,/<PinInput[^>]*autoFocus/);assert.match(dialog,/trapGateFocus\(root.current\)/);
});
