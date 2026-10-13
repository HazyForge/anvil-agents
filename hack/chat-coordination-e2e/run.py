"""Actual API→ACP adapter A→ACP adapter B→automatic A synthesis local E2E.
All participants are inert Python subprocesses; no real agents/models/credentials.
"""
from concurrent.futures import ThreadPoolExecutor
import http.client
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import time
import uuid

FIXTURE=Path(__file__).resolve().parent
ROOT=FIXTURE.parents[1]
BUILD=Path(os.environ.get('INERT_CHAT_E2E_OUTPUT',str(ROOT/'.runtime'/'chat-coordination-e2e'))).resolve()
MODE='postgres' if os.environ.get('INERT_CHAT_DATABASE_URL') else 'memory'
OUT=BUILD/MODE
OUT.mkdir(parents=True,exist_ok=True)
GO=os.environ.get('INERT_CHAT_E2E_GO','go')
build_env={**os.environ,'KUBECONFIG':'/dev/null','GOTOOLCHAIN':'local','GOPROXY':'off'}
for name,package in [('anvil-actor-acp','./cmd/anvil-actor-acp'),('collaboration-api','./hack/chat-coordination-e2e')]:
    subprocess.run([GO,'build','-o',str(BUILD/name),package],cwd=ROOT,env=build_env,check=True)
RESULT={'mode':'actual runapi + ProcessBackend + AtenetClient + two actual ACP adapter binaries + inert stdio peers','external_agents_or_models_invoked':False,'kubernetes':'in-memory client','store':'isolated PostgreSQL' if os.environ.get('INERT_CHAT_DATABASE_URL') else 'memory','checks':[]}

def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1',0))
        return s.getsockname()[1]

def request(address,method,path,body=None):
    conn=http.client.HTTPConnection('127.0.0.1',address,timeout=20)
    headers={'Authorization':'Bearer inert-local-marker','Content-Type':'application/json'}
    conn.request(method,path,None if body is None else json.dumps(body),headers)
    response=conn.getresponse()
    data=response.read()
    conn.close()
    try: data=json.loads(data)
    except ValueError: data=data.decode()
    return response.status,data

def ready(process,address):
    until=time.monotonic()+6
    while time.monotonic()<until:
        if process.poll() is not None: raise RuntimeError('inert fixture exited: '+process.stderr.read().decode())
        try:
            if request(address,'GET','/readyz')[0]==200:return
        except OSError:pass
        time.sleep(.03)
    raise RuntimeError('fixture readiness timeout')

def audit(path):
    return [json.loads(x) for x in path.read_text().splitlines()] if path.exists() else []

def wait(fn,timeout=12):
    until=time.monotonic()+timeout
    while time.monotonic()<until:
        value=fn()
        if value:return value
        time.sleep(.05)
    raise AssertionError('local E2E condition timed out')

class Journey:
    def __init__(self,case):
        self.processes=[]
        self.case=case
        self.logs={}
        self.bindings={}
        env={'PATH':'/usr/bin:/bin','HOME':str(OUT),'KUBECONFIG':'/dev/null'}
        for role in ['A','B']:
            address=port()
            slug=re.sub(r'[^a-z0-9]+','-',case.lower()).strip('-')
            logfile=OUT/f'{slug}-{role}.jsonl'
            logfile.write_text('')
            args=[str(BUILD/'anvil-actor-acp'),f'--listen=127.0.0.1:{address}','--stdio-command=/usr/bin/python3','--stdio-arg='+str(FIXTURE/'peer.py'),'--stdio-arg='+role,'--stdio-arg='+str(logfile),'--workdir='+str(OUT),'--turn-timeout=20s']
            process=subprocess.Popen(args,cwd=OUT,env=env,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
            self.processes.append(process)
            ready(process,address)
            self.logs[role]=logfile
            self.bindings[role]=address
        self.api=port()
        if os.environ.get('INERT_CHAT_DATABASE_URL'):env['INERT_CHAT_DATABASE_URL']=os.environ['INERT_CHAT_DATABASE_URL']
        args=[str(BUILD/'collaboration-api'),f'127.0.0.1:{self.api}',f'127.0.0.1:{self.bindings["A"]}',f'127.0.0.1:{self.bindings["B"]}']
        process=subprocess.Popen(args,cwd=OUT,env=env,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
        self.processes.append(process)
        self.api_process=process
        self.api_args=args
        self.api_env=env.copy()
        ready(process,self.api)
        self.base='/api/v1/namespaces/agents/chat/threads'
    def call(self,method,path,body=None,status=200):
        actual,data=request(self.api,method,path,body)
        assert actual==status,(method,path,actual,data)
        return data
    def create(self,profile='inert-a',coord=True):
        body={'profileName':profile,'mode':'persona','title':'Local inert '+self.case}
        if coord:body['metadata']={'coordination':{'enabled':True,'allowedProfiles':['inert-b'],'returnReplies':True}}
        return self.call('POST',self.base,body,201)
    def detail(self):return self.call('GET',self.base+'/'+self.thread)
    def start(self,task):
        self.thread=self.create()['id']
        self.request_id=str(uuid.uuid4())
        self.task=task
        accepted=self.call('POST',self.base+'/'+self.thread+'/messages',{'content':task,'requestId':self.request_id},202)
        self.parent=accepted['turn']['id']
        assert accepted['turn']['coordination']['status']=='waiting',accepted
        assert len(accepted['turn']['delegates'])==1
        self.child=accepted['turn']['delegates'][0]['threadId']
        return accepted
    def parent_state(self,state):
        detail=self.detail()
        parent=next(t for t in detail['turns'] if t['id']==self.parent)
        return detail if parent.get('coordination',{}).get('status')==state else None
    def cancel(self):return self.call('POST',self.base+'/'+self.thread+'/turns/'+self.parent+'/cancel')
    def final_messages(self,detail):return [m for m in detail['messages'] if m['role']=='assistant' and m['content'].startswith('INERT_FINAL:')]
    def child_terminal(self):
        detail=self.call('GET',self.base+'/'+self.child)
        return detail if detail['turns'][0]['status'] in ['succeeded','failed','cancelled'] else None
    def restart_api(self):
        self.api_process.terminate()
        self.api_process.wait(timeout=5)
        self.api_process.stderr.close()
        self.processes.remove(self.api_process)
        self.api_process=subprocess.Popen(self.api_args,cwd=OUT,env=self.api_env,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
        self.processes.append(self.api_process)
        ready(self.api_process,self.api)
    def stop(self):
        for process in reversed(self.processes):
            if process.poll() is None:process.terminate()
            try:process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill();process.wait(timeout=2)
                raise AssertionError('fixture shutdown exceeded bound')
            process.stderr.close()

def case(name,fn):
    journey=Journey.__new__(Journey)
    try:
        journey.__init__(name)
        evidence=fn(journey)
        RESULT['checks'].append({'name':name,'passed':True,**evidence})
    finally:journey.stop()

def automatic(j):
    initial=j.start('LOCAL_ROUND_TRIP')
    before=j.detail()
    assert not j.final_messages(before)
    child=j.call('GET',j.base+'/'+j.child)
    assert child['metadata']['sourceTurnId']==j.parent
    assert child['metadata']['sourceThreadId']==j.thread
    assert child['metadata']['sourceProfileName']=='inert-a'
    assert child['messages'][0]['metadata']['authorProfile']=='inert-a'
    final=wait(lambda:j.parent_state('done'))
    messages=j.final_messages(final)
    assert len(messages)==1 and 'INERT_B_RESULT' in messages[0]['content'],final
    assert len(final['turns'])==2
    assert all(t['status']=='succeeded' for t in final['turns'])
    assert len([x for x in audit(j.logs['A']) if x['event']=='prompt'])==2
    assert len([x for x in audit(j.logs['B']) if x['event']=='prompt'])==1
    native_a=[x['session'] for x in audit(j.logs['A']) if x['event']=='prompt']
    assert native_a[0]==native_a[1],native_a
    # Duplicate POST delivery, independent HTTP reconnections and concurrent
    # observe-only refreshes must preserve one final and one child execution.
    with ThreadPoolExecutor(max_workers=4) as pool:
        replies=list(pool.map(lambda _:j.call('POST',j.base+'/'+j.thread+'/messages',{'content':j.task,'requestId':j.request_id},202),range(4)))
    assert all(x['turn']['id']==j.parent for x in replies)
    j.call('POST',j.base+'/'+j.thread+'/messages',{'content':'different task','requestId':j.request_id},409)
    j.call('POST',j.base+'/'+j.thread+'/turns/'+j.parent+'/cancel',status=409)
    with ThreadPoolExecutor(max_workers=6) as pool:
        reads=list(pool.map(lambda _:j.detail(),range(12)))
    assert all(len(j.final_messages(d))==1 and len(d['turns'])==2 for d in reads)
    assert len([x for x in audit(j.logs['A']) if x['event']=='prompt'])==2
    assert len([x for x in audit(j.logs['B']) if x['event']=='prompt'])==1
    (OUT/'automatic-transcript.json').write_text(json.dumps(final,indent=2)+'\n')
    return {'parent_turn_id':j.parent,'peer_thread_id':j.child,'final_turn_id':final['turns'][1]['id'],'native_prompt_counts':{'A':2,'B':1},'duplicate_posts':4,'reconnect_reads':12,'automatic_final_replies':1,'initial_state':initial['turn']['coordination']['status'],'origin_native_session_reused':True}

def cancelled(j,task,blocked_role):
    j.start(task)
    if blocked_role:
        wait(lambda:any(x['event']=='prompt' and ('PEER_RESULTS_JSON' in x.get('latest','') if blocked_role=='A' else 'CANCEL_PEER' in x.get('latest','')) for x in audit(j.logs[blocked_role])))
    first=j.cancel()
    second=j.cancel()
    assert first['coordination']['status']==second['coordination']['status']=='cancelled'
    wait(j.child_terminal)
    if blocked_role=='A':wait(lambda:all(t['status'] not in ['waiting','queued','running'] for t in j.detail()['turns']))
    time.sleep(.35)
    detail=j.detail()
    assert not j.final_messages(detail),detail
    assert len([m for m in detail['messages'] if m['content'].startswith('Coordination cancelled. Late')])==1
    # Fresh reads cannot resurrect the journey or start a synthesis.
    for _ in range(3):assert j.parent_state('cancelled') and not j.final_messages(j.detail())
    if blocked_role:
        prompts=[x for x in audit(j.logs[blocked_role]) if x['event']=='prompt']
        completions=[x for x in audit(j.logs[blocked_role]) if x['event']=='completed']
        assert len(completions)<len(prompts),(prompts,completions)
        # The adapter owns/reaps its inert native child after HTTP cancellation.
        wait(lambda:request(j.bindings[blocked_role],'GET','/readyz')[0]==503)
    if not blocked_role: assert not [x for x in audit(j.logs['B']) if x['event']=='prompt']
    return {'coordination_state':'cancelled','late_final_replies':0,'duplicate_cancel_messages':1,'blocked_participant':blocked_role,'native_peer_closed':bool(blocked_role)}

def failure(j):
    j.start('FAIL_PEER')
    final=wait(lambda:j.parent_state('done'))
    replies=j.final_messages(final)
    assert len(replies)==1 and 'peer failed:' in replies[0]['content'],final
    child=j.call('GET',j.base+'/'+j.child)
    assert child['turns'][0]['status']=='failed'
    assert len([x for x in audit(j.logs['B']) if x['event']=='prompt'])==1
    return {'peer_status':'failed','automatic_user_failure_explanation':True,'peer_replays':0}

def scope(j):
    j.call('POST',j.base,{'profileName':'inert-a','mode':'persona','title':'invalid scope','metadata':{'coordination':{'enabled':True,'allowedProfiles':['inert-foreign'],'returnReplies':True}}},400)
    return {'cross_application_target':'rejected before dispatch'}

def busy_recipient(j):
    peer=j.create('inert-b',False)
    with ThreadPoolExecutor(max_workers=1) as pool:
        running=pool.submit(j.call,'POST',j.base+'/'+peer['id']+'/messages',{'content':'BLOCK_BUSY','requestId':str(uuid.uuid4())},202)
        wait(lambda:any(x['event']=='blocked' for x in audit(j.logs['B'])))
        j.start('QUEUED_WHILE_B_BUSY')
        child=j.call('GET',j.base+'/'+j.child)
        assert child['turns'][0]['status']=='waiting',child
        assert running.result()['turn']['status']=='succeeded'
    final=wait(lambda:j.parent_state('done'))
    assert len(j.final_messages(final))==1
    native_b=[x['session'] for x in audit(j.logs['B']) if x['event']=='prompt']
    assert len(native_b)==2 and native_b[0]!=native_b[1],native_b
    return {'recipient_initial_status':'waiting','recipient_runs_after_prior_work':True,'automatic_final_replies':1}

def restart_after_peer(j):
    j.start('RECOVER_PERSISTED_PEER')
    wait(j.child_terminal)
    assert j.parent_state('waiting'),j.detail()
    j.restart_api()
    final=wait(lambda:j.parent_state('done'))
    assert len(j.final_messages(final))==1
    assert len([x for x in audit(j.logs['B']) if x['event']=='prompt'])==1
    assert len([x for x in audit(j.logs['A']) if x['event']=='prompt'])==2
    return {'api_restarted_after_peer_receipt':True,'automatic_final_replies':1,'peer_replays':0}

def retry_synthesis(j):
    j.start('FAIL_SYNTH_ONCE')
    failed=wait(lambda:j.parent_state('failed'))
    source=failed['turns'][-1]
    saved=next(m for m in failed['messages'] if m['id']==source['userMessageId'])
    assert source['resumption'] and saved['metadata']['authorKind']=='agent_results'
    j.call('POST',j.base+'/'+j.thread+'/messages',{'content':saved['content'],'requestId':str(uuid.uuid4())},400)
    body={'retryTurnId':source['id'],'retryCount':source.get('retryCount',0)}
    with ThreadPoolExecutor(max_workers=4) as pool:
        receipts=list(pool.map(lambda _:j.call('POST',j.base+'/'+j.thread+'/messages',body,202),range(4)))
    assert all(x['turn']['id']==source['id'] and x['turn']['resumption'] and x['turn']['parentTurnId']==j.parent and x['turn']['retryCount']==1 for x in receipts)
    final=wait(lambda:j.parent_state('done'))
    assert len(j.final_messages(final))==1 and len(final['turns'])==2
    assert len([x for x in audit(j.logs['A']) if x['event']=='prompt'])==3
    assert len([x for x in audit(j.logs['B']) if x['event']=='prompt'])==1
    assert len([m for m in final['messages'] if m.get('metadata',{}).get('authorKind')=='agent_results'])==1
    return {'same_synthesis_turn':True,'same_parent':True,'service_input_count':1,'duplicate_retry_receipts':4,'extra_peer_fanouts':0,'automatic_final_replies':1}

case('failed synthesis retry preserves provenance and never repeats peer',retry_synthesis)
case('automatic round-trip + identity/correlation + duplicate/reconnect',automatic)
case('busy recipient waits then completes automatic round-trip',busy_recipient)
if os.environ.get('INERT_CHAT_DATABASE_URL'):
    case('API restart recovers peer receipt and resumes origin once',restart_after_peer)
case('cancel waiting peer before launch',lambda j:cancelled(j,'WAITING_CANCEL',None))
case('cancel running peer',lambda j:cancelled(j,'CANCEL_PEER','B'))
case('cancel running synthesis',lambda j:cancelled(j,'SYNTH_CANCEL','A'))
case('terminal peer failure returns to originating harness',failure)
case('existing application allowlist boundary',scope)
RESULT['all_passed']=True
RESULT['processes_stopped']=True
(OUT/'results.json').write_text(json.dumps(RESULT,indent=2)+'\n')
print(json.dumps(RESULT,indent=2))
