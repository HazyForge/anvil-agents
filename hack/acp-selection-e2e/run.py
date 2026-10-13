"""Actual runapi + standing + actor binaries; inert native ACP peers only.

Optional --parent-control builds the unchanged reviewed parent using a Go
overlay and proves its wrong-provider prompt behavior. Never calls a model.
"""
import argparse
import http.client
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import time
import uuid

parser=argparse.ArgumentParser();parser.add_argument('--parent-control',action='store_true');args=parser.parse_args()
REPO=Path(__file__).resolve().parents[2]
PEER=REPO/'hack/acp-selection-e2e/peer.py'
GO=shutil.which('go')
ROOT=Path(tempfile.mkdtemp(prefix='anvil-acp-selection-e2e-',dir='/tmp')).resolve()
OUT=Path(os.environ.get('ANVIL_SELECTION_EVIDENCE_DIR',str(ROOT/'evidence'))).resolve();OUT.mkdir(parents=True,exist_ok=True)
assert ROOT.parent==Path('/tmp') and ROOT.name.startswith('anvil-acp-selection-e2e-')
RESULT={'external_agents_or_models_invoked':False,'credentials_or_cluster_access':False,'parent_control':args.parent_control,'checks':[]}
ENV={**os.environ,'KUBECONFIG':'/dev/null','GOPROXY':'off'}

def port():
    with socket.socket() as s:s.bind(('127.0.0.1',0));return s.getsockname()[1]
def request(address,method,path,body=None,timeout=10):
    conn=http.client.HTTPConnection('127.0.0.1',address,timeout=timeout)
    try:
        conn.request(method,path,None if body is None else json.dumps(body),{'Authorization':'Bearer inert-local-marker','Content-Type':'application/json'})
        response=conn.getresponse();raw=response.read()
        try:data=json.loads(raw)
        except ValueError:data=raw.decode()
        return response.status,data
    finally:conn.close()
def audit(path):
    if not path.exists():return []
    return [json.loads(x) for x in path.read_text().splitlines()]
def wait(fn,seconds=8):
    deadline=time.monotonic()+seconds
    while time.monotonic()<deadline:
        try:value=fn()
        except OSError:value=None
        if value:return value
        time.sleep(.03)
    raise AssertionError('inert condition timed out')

overlay=[]
if args.parent_control:
    replace={}
    for name in ['internal/actoracp/client.go','internal/actoracp/server.go','cmd/anvil-actor-acp/main.go']:
        path=ROOT/name;path.parent.mkdir(parents=True,exist_ok=True)
        path.write_bytes(subprocess.check_output(['git','show','22d19b76:'+name],cwd=REPO))
        replace[str(REPO/name)]=str(path)
    replace[str(REPO/'internal/actoracp/selection.go')]=''
    p=ROOT/'parent-overlay.json';p.write_text(json.dumps({'Replace':replace}));overlay=['-overlay='+str(p)]
subprocess.run([GO,'build',*overlay,'-o',str(ROOT/'actor'),'./cmd/anvil-actor-acp'],cwd=REPO,env=ENV,check=True,timeout=90)
subprocess.run([GO,'build','-o',str(ROOT/'api'),'./hack/chat-coordination-e2e'],cwd=REPO,env=ENV,check=True,timeout=90)

class Journey:
    def __init__(self,case):
        self.case=case;self.processes=[];self.logs={};self.bindings={}
        env={'PATH':'/usr/bin:/bin','HOME':str(ROOT),'KUBECONFIG':'/dev/null','LANG':'C.UTF-8'}
        for role in ['A','B']:
            address=port();log=OUT/(case+'-'+role+'.jsonl');log.write_text('')
            model='synthetic/codex-model' if role=='A' else 'synthetic/opencode-model'
            command=[str(ROOT/'actor'),'--listen=127.0.0.1:'+str(address),'--stdio-command=/usr/bin/python3',
                '--stdio-arg='+str(PEER),'--stdio-arg='+role,'--stdio-arg='+case,'--stdio-arg='+str(log),
                '--workdir='+str(ROOT),'--turn-timeout=5s']
            if not args.parent_control:command+=['--expected-model='+model,'--expected-mode=build','--catalog-ready-timeout='+('2s' if case=='cancel' else '500ms')]
            p=subprocess.Popen(command,cwd=ROOT,env=env,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            self.processes.append(p);self.logs[role]=log;self.bindings[role]=address
            wait(lambda:p.poll() is None and request(address,'GET','/readyz')[0]==200)
        self.api=port()
        p=subprocess.Popen([str(ROOT/'api'),'127.0.0.1:'+str(self.api),'127.0.0.1:'+str(self.bindings['A']),'127.0.0.1:'+str(self.bindings['B'])],cwd=ROOT,env=env,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        self.processes.append(p);wait(lambda:p.poll() is None and request(self.api,'GET','/readyz')[0]==200)
        self.base='/api/v1/namespaces/agents/chat/threads'
    def call(self,method,path,body=None,status=200):
        code,value=request(self.api,method,path,body);assert code==status,(method,path,code,value);return value
    def start(self):
        created=self.call('POST',self.base,{'profileName':'inert-a','mode':'persona','title':'Inert selection '+self.case,
            'metadata':{'coordination':{'enabled':True,'allowedProfiles':['inert-b'],'returnReplies':True}}},201)
        self.thread=created['id']
        accepted=self.call('POST',self.base+'/'+self.thread+'/messages',{'requestId':str(uuid.uuid4()),'content':'INERT_SELECTION_'+self.case},202)
        self.parent=accepted['turn']['id'];self.child=accepted['turn']['delegates'][0]['threadId']
    def detail(self):return self.call('GET',self.base+'/'+self.thread)
    def state(self,wanted):
        detail=self.detail();turn=next(x for x in detail['turns'] if x['id']==self.parent)
        return detail if turn.get('coordination',{}).get('status')==wanted else None
    def stop(self):
        for p in reversed(self.processes):
            if p.poll() is None:p.terminate()
            try:p.wait(timeout=5)
            except subprocess.TimeoutExpired:p.kill();p.wait(timeout=2);raise AssertionError('owned inert process shutdown exceeded bound')

try:
    cases=['mismatch','timeout'] if args.parent_control else ['delay','mismatch','mode-mismatch','timeout','cancel']
    for case in cases:
        journey=Journey.__new__(Journey);journey.processes=[]
        try:
            journey.__init__(case);journey.start()
            if case=='cancel':
                wait(lambda:any(x['event']=='new' for x in audit(journey.logs['B'])))
                cancelled=journey.call('POST',journey.base+'/'+journey.thread+'/turns/'+journey.parent+'/cancel')
                assert cancelled['coordination']['status']=='cancelled'
                final=wait(lambda:journey.state('cancelled'));time.sleep(.2)
            else:final=wait(lambda:journey.state('done'))
            def terminal_child():
                value=journey.call('GET',journey.base+'/'+journey.child)
                return value if value['turns'][0]['status'] in ['succeeded','failed','cancelled'] else None
            child=wait(terminal_child)
            logsA=audit(journey.logs['A']);logsB=audit(journey.logs['B'])
            promptsB=[x for x in logsB if x['event']=='prompt'];promptsA=[x for x in logsA if x['event']=='prompt']
            check={'case':case,'parent_turn_id':journey.parent,'peer_thread_id':journey.child,'peer_status':child['turns'][0]['status'],
                'native_prompt_counts':{'A':len(promptsA),'B':len(promptsB)},'B_prompt_selections':[{'model':x['model'],'mode':x['mode']} for x in promptsB]}
            assert child['metadata']['sourceTurnId']==journey.parent and child['metadata']['sourceThreadId']==journey.thread
            if args.parent_control:
                check['corrected_assertion_passed']=len(promptsB)==0
                check['parent_defect_reproduced']=len(promptsB)==1 and promptsB[0]['model']=='foreign/bootstrap' and promptsB[0]['mode']=='plan'
                assert check['parent_defect_reproduced'] and not check['corrected_assertion_passed'],'parent control did not expose wrong-selection prompt'
            elif case=='delay':
                assert len(promptsA)==2 and len(promptsB)==1 and child['turns'][0]['status']=='succeeded'
                assert promptsB[0]['model']=='synthetic/opencode-model' and promptsB[0]['mode']=='build'
                assert [x['event'] for x in logsB].index('catalog_ready') < [x['event'] for x in logsB].index('prompt')
                assert len([m for m in final['messages'] if m['role']=='assistant' and m['content'].startswith('INERT_FINAL:')])==1
            elif case=='cancel':
                assert len(promptsB)==0 and len(promptsA)==1
                assert not [m for m in final['messages'] if m['content'].startswith('INERT_FINAL:')]
            else:
                assert len(promptsB)==0 and child['turns'][0]['status']=='failed'
                assert len(promptsA)==2
                assert len([m for m in final['messages'] if m['role']=='assistant' and m['content'].startswith('INERT_FINAL: peer failed:')])==1
            check['passed']=True;RESULT['checks'].append(check)
            (OUT/(case+'-source-transcript.json')).write_text(json.dumps(final,indent=2)+'\n')
            (OUT/(case+'-peer-transcript.json')).write_text(json.dumps(child,indent=2)+'\n')
            print(json.dumps(check),flush=True)
        finally:journey.stop()
    RESULT['all_passed']=True
finally:
    remaining=[]
    for path in Path('/proc').iterdir():
        if not path.name.isdigit():continue
        try:
            if str(ROOT).encode() in (path/'cmdline').read_bytes():remaining.append(int(path.name))
        except OSError:pass
    RESULT['owned_processes_remaining']=remaining
    assert not remaining,'owned inert runtime remains active'
    if not OUT.is_relative_to(ROOT):shutil.rmtree(ROOT);RESULT['temporary_runtime_removed']=not ROOT.exists()
    (OUT/'results.json').write_text(json.dumps(RESULT,indent=2)+'\n')
    print(json.dumps(RESULT,indent=2),flush=True)
