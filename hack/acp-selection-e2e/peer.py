"""Inert ACP participant. Never resolves a CLI, calls a model, or uses tools."""
import json
import os
from pathlib import Path
import sys
import threading
import time

role, scenario, log_file = sys.argv[1:]
selection={}
expected_model="synthetic/codex-model" if role=="A" else "synthetic/opencode-model"
lock = threading.Lock()
sessions = {}
active = {}
failed_syntheses = set()

def send(message):
    with lock:
        print(json.dumps(message), flush=True)

def audit(event, **fields):
    with lock:
        with open(log_file, 'a') as output:
            output.write(json.dumps({'role':role,'event':event,**fields})+'\n')

def result(request_id, data):
    send({'jsonrpc':'2.0','id':request_id,'result':data})

def chunk(session, text):
    send({'jsonrpc':'2.0','method':'session/update','params':{'sessionId':session,'update':{'sessionUpdate':'agent_message_chunk','content':{'type':'text','text':text}}}})

def options(session, complete=True):
    model, mode=selection[session]
    models=[{'value':'foreign/bootstrap'}]
    modes=[{'value':'plan'}]
    if complete:
        models.append({'value':expected_model});modes.append({'value':'build'})
    return [
        {'id':'model','type':'select','currentValue':model,'options':models},
        {'id':'mode','type':'select','currentValue':mode,'options':modes},
    ]

def catalog_update(session):
    time.sleep(.15)
    audit('catalog_ready',session=session)
    send({'jsonrpc':'2.0','method':'session/update','params':{'sessionId':session,'update':{'sessionUpdate':'config_option_update','configOptions':options(session)}}})

def prompt(request):
    session = request['params']['sessionId']
    text = ''.join(item['text'] for item in request['params']['prompt'])
    history = json.loads(text.split('CONVERSATION_JSON:\n',1)[1])
    latest = history[-1]['content']
    cancel = threading.Event()
    active[session] = cancel
    audit('prompt', session=session, latest=latest, model=selection[session][0], mode=selection[session][1])
    if role == 'A' and 'PEER_RESULTS_JSON:\n' in latest:
        data = json.loads(latest.split('PEER_RESULTS_JSON:\n',1)[1])
        assert len(data['results']) == 1
        peer = data['results'][0]
        if 'FAIL_SYNTH_ONCE' in peer['content'] and session not in failed_syntheses:
            failed_syntheses.add(session)
            send({'jsonrpc':'2.0','id':request['id'],'error':{'code':-32000,'message':'inert synthesis failure'}})
            audit('failed',session=session)
            return
        reply = 'INERT_FINAL: ' + (peer['content'] if peer['status']=='succeeded' else 'peer failed: '+peer['content'])
        if 'SYNTH_CANCEL' in peer['content']:
            chunk(session,'inert synthesis in progress')
            if cancel.wait(15):
                audit('cancelled',session=session)
                result(request['id'],{'stopReason':'cancelled'})
                return
    elif role == 'A':
        reply = json.dumps({'reply':'I have asked the inert reviewer; I will answer here when it finishes.','messages':[{'profileName':'inert-b','content':'Review task: '+latest}]})
    else:
        if 'CANCEL_PEER' in latest or 'BLOCK_BUSY' in latest:
            chunk(session,'inert reviewer in progress')
            audit('blocked',session=session)
            if cancel.wait(2.5 if 'BLOCK_BUSY' in latest else 15):
                audit('cancelled',session=session)
                result(request['id'],{'stopReason':'cancelled'})
                return
        if 'FAIL_PEER' in latest:
            # A native failure is a terminal error; fixtures do not fabricate a reply.
            send({'jsonrpc':'2.0','id':request['id'],'error':{'code':-32000,'message':'inert failure'}})
            audit('failed',session=session)
            return
        reply = 'INERT_B_RESULT: reviewed '+latest
    chunk(session,reply)
    time.sleep(.25)
    audit('completed',session=session)
    result(request['id'],{'stopReason':'end_turn'})
    active.pop(session,None)

for line in sys.stdin:
    request=json.loads(line)
    method=request['method']
    if method=='initialize':
        assert request['params']['clientCapabilities']['terminal'] is False
        result(request['id'],{'protocolVersion':1,'agentCapabilities':{'sessionCapabilities':{'close':{}}}})
    elif method=='session/new':
        assert request['params']['mcpServers']==[]
        session=f'inert-{role}-{len(sessions)+1}'
        sessions[session]=[]
        selection[session]=[expected_model,'build'] if role=='A' else ['foreign/bootstrap','plan']
        complete=role=='A' or scenario in ['mismatch','mode-mismatch']
        result(request['id'],{'sessionId':session,'configOptions':options(session,complete)})
        audit('new',session=session,complete=complete)
        if role=='B' and scenario=='delay':threading.Thread(target=catalog_update,args=(session,),daemon=True).start()
    elif method=='session/set_config_option':
        session=request['params']['sessionId']
        key=request['params']['configId'];value=request['params']['value']
        assert value==(expected_model if key=='model' else 'build')
        if not (role=='B' and (scenario=='mismatch' or (scenario=='mode-mismatch' and key=='mode'))):
            selection[session][0 if key=='model' else 1]=value
        audit('selection',session=session,key=key,value=value,actual_model=selection[session][0],actual_mode=selection[session][1])
        result(request['id'],{'configOptions':options(session)})
    elif method=='session/close':
        audit('closed',session=request['params']['sessionId'])
        result(request['id'],{})
    elif method=='session/prompt':
        threading.Thread(target=prompt,args=(request,),daemon=True).start()
    elif method=='session/cancel':
        event=active.get(request['params']['sessionId'])
        if event: event.set()
    else:
        raise RuntimeError('unexpected inert protocol call')
