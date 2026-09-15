import test from 'node:test';
import assert from 'node:assert/strict';
import {rememberPendingSend, readPendingSend, clearPendingSend} from '../src/api/pendingChat.ts';

test('explicit no-launch retry gets fresh request ID; uncertain retry preserves ID and draft intent', () => {
  const values=new Map();
  globalThis.sessionStorage={getItem:key=>values.get(key)??null,setItem:(key,value)=>values.set(key,value),removeItem:key=>values.delete(key)};
  const original=rememberPendingSend('ns','thread','original message');
  const retry=rememberPendingSend('ns','thread','original message',{forceNew:true,preserveDraft:true});
  assert.notEqual(retry.id,original.id);
  assert.equal(retry.preserveDraft,true);
  assert.deepEqual(readPendingSend('ns','thread'),retry);
  assert.deepEqual(rememberPendingSend('ns','thread','original message',{preserveDraft:true}),retry);
  const other=rememberPendingSend('ns','different-thread','original message');
  assert.notEqual(other.id,retry.id);
  clearPendingSend('ns','thread');
  assert.equal(readPendingSend('ns','thread'),null);
});
