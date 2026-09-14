import assert from 'node:assert/strict';
import test from 'node:test';
// The browser fixture covers the stream import; copy only this pure parser via
// a data module so Node's native TS loader need not resolve Vite extensionless imports.
import {readFile} from 'node:fs/promises';
import ts from 'typescript';
const source = await readFile(new URL('../src/api/localChat.ts', import.meta.url), 'utf8');
const parser = source.slice(source.indexOf('const textBlocks'));
const js = ts.transpile(parser, {target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext});
const {localReply} = await import(`data:text/javascript;base64,${Buffer.from(js).toString('base64')}`);
test('Prime extracts final native assistant text and excludes tool output and thinking', () => {
  const output = [
    {type:'message_end',message:{role:'toolResult',content:[{type:'text',text:'private tool output'}]}},
    {type:'message_end',message:{role:'assistant',stopReason:'toolUse',content:[{type:'text',text:'interim'}, {type:'toolCall',name:'ipython'}]}},
    {type:'message_end',message:{role:'assistant',stopReason:'stop',content:[{type:'thinking',thinking:'private thought'},{type:'text',text:'Actual result'}]}},
  ].map(e=>JSON.stringify(e)).join('\n');
  assert.equal(localReply(output,'prime'),'Actual result');
  assert.equal(localReply(output+'\n'+JSON.stringify({type:'message_end',message:{role:'assistant',stopReason:'error',content:[]}}),'prime'),'');
  assert.equal(localReply('arbitrary native error','prime'),'');
});
test('other local harness replies retain their own public envelope', () => {
  assert.equal(localReply(JSON.stringify({event:'result',result:{status:'SUCCESS',response:'AGY answer'}}),'agy'),'AGY answer');
  assert.equal(localReply(JSON.stringify({type:'item.completed',item:{type:'agent_message',text:'Codex answer'}}),'codex'),'Codex answer');
  assert.equal(localReply(JSON.stringify({type:'text',part:{text:'OpenCode answer'}}),'opencode'),'OpenCode answer');
});
