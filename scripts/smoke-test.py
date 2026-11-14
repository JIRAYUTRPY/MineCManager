#!/usr/bin/env python3
"""Build and exercise real Panel -> Agent HTTP. No live systemd/server required."""
import hashlib, io, json, os, pathlib, socket, subprocess, tempfile, time, urllib.request, urllib.error, zipfile
base=pathlib.Path(__file__).resolve().parents[1]
def call(path, method='GET', body=None, headers=None):
    h={'Authorization':'Bearer '+token, **(headers or {})}
    if isinstance(body,dict): body=json.dumps(body).encode();h['Content-Type']='application/json'
    r=urllib.request.Request('http://127.0.0.1:9080/v1/'+path, data=body,method=method,headers=h)
    with urllib.request.urlopen(r, timeout=10) as res:
        b=res.read();return json.loads(b) if b else None
# Never attach to existing services on these fixed application ports.
for port in (9080,9081):
    with socket.socket() as sock: sock.bind(('127.0.0.1',port))
with tempfile.TemporaryDirectory(prefix='mcm-smoke-') as td:
    d=pathlib.Path(td);root=d/'server';root.mkdir();(root/'plugins').mkdir();(root/'custom-world').mkdir();(root/'custom-world'/'level.dat').write_bytes(b'original');(root/'server.properties').write_text('motd=before\n');state=d/'state';state.mkdir()
    bin=d/'bin';bin.mkdir();(bin/'systemctl').write_text('#!/bin/sh\ncase "$1" in is-active) echo inactive; exit 3;; start|stop|restart) exit 0;; *) exit 1;; esac\n');(bin/'systemctl').chmod(0o755)
    for name in ('agent','panel'):subprocess.run(['go','build','-o',str(bin/name),'./cmd/'+name],cwd=base,check=True)
    token='smoke-panel-key-'+os.urandom(32).hex();agent_token=os.urandom(32).hex()
    cfg={'keys':[{'id':'admin','sha256':hashlib.sha256(token.encode()).hexdigest(),'scopes':['nodes:read','nodes:control','plugins:read','plugins:write','configs:read','configs:write','backups:read','backups:write','backups:restore','admin:keys','admin:nodes','audit:read']}],'nodes':[{'id':'local','url':'http://127.0.0.1:9081','token_env':'SMOKE_AGENT_TOKEN'}]}
    (d/'panel.json').write_text(json.dumps(cfg));env={**os.environ,'PATH':str(bin)+':'+os.environ['PATH'],'MCM_AGENT_TOKEN':agent_token,'SMOKE_AGENT_TOKEN':agent_token,'MCM_SERVER_ROOT':str(root),'MCM_SYSTEMD_UNIT':'minecraft.service','MCM_STATE_ROOT':str(state),'MCM_PANEL_CONFIG':str(d/'panel.json'),'MCM_AUDIT_PATH':str(d/'audit.jsonl')}
    # Eliminate inherited transport configuration for this isolated loopback test.
    for k in ('MCM_AGENT_ADDR','MCM_SERVER_CERT','MCM_SERVER_KEY','MCM_CLIENT_CERT','MCM_CLIENT_KEY','MCM_CA_CERT'):env.pop(k,None)
    processes=[]
    try:
        for name in ('agent','panel'):processes.append(subprocess.Popen([str(bin/name)],env=env))
        for _ in range(100):
            try:call('nodes/local/status');break
            except (OSError,urllib.error.URLError):time.sleep(.05)
        else:raise RuntimeError('services did not start')
        assert call('nodes/local/status')['state']=='inactive'
        z=io.BytesIO()
        with zipfile.ZipFile(z,'w') as jar:jar.writestr('plugin.yml','name: Smoke\nversion: "1"\nmain: smoke.Main\n')
        call('nodes/local/plugins/Smoke.jar','PUT',z.getvalue(),{'Content-Type':'application/java-archive'})
        assert call('nodes/local/plugins')[0]['name']=='Smoke.jar'
        before=call('nodes/local/config?path=server.properties')
        call('nodes/local/config?path=server.properties','PUT',b'motd=after\n',{'If-Match':'"'+before['sha256']+'"'})
        snap=call('nodes/local/backups','POST',{})
        (root/'custom-world'/'level.dat').write_bytes(b'changed')
        restored=call('nodes/local/backups/'+snap['id']+'/restore','POST',{'confirm':snap['id']})
        assert (root/'custom-world'/'level.dat').read_bytes()==b'original'
        assert (pathlib.Path(restored['previous_root'])/'custom-world'/'level.dat').read_bytes()==b'changed'
        key=call('keys','POST',{'id':'reader','scopes':['nodes:read'],'nodes':['local']})
        call('keys/reader','DELETE')
        assert call('audit?limit=100')['records']
        events=[json.loads(line) for line in (d/'audit.jsonl').read_text().splitlines()]
        assert any(e['phase']=='result' and e['status']==201 for e in events)
        assert token not in (d/'audit.jsonl').read_text() and key['token'] not in (d/'panel.json').read_text()
        if os.environ.get('MCM_UI_TEST')=='1':subprocess.run(['node',str(base/'scripts'/'ui-smoke.cjs')],env={**os.environ,'MCM_UI_KEY':token},check=True)
        print('PASS: actual panel/agent auth, status, JAR install, config edit, backup, verified restore, key revoke and audit')
    finally:
        for p in processes:p.terminate()
        for p in processes:
            try:p.wait(timeout=5)
            except subprocess.TimeoutExpired:p.kill();p.wait()
