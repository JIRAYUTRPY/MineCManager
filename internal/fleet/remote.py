"""Fixed SSH control program. User data arrives on stdin, never in shell code.
All actions require root/sudo and are serialized on the target VM.
"""
import base64, fcntl, hashlib, http.client, json, os, pathlib, pwd, re, shutil
import socket, stat, subprocess, sys, tarfile, tempfile, time, urllib.parse, urllib.request, uuid, selectors, signal

BASE = pathlib.Path('/srv/minecmanager')
STATE = pathlib.Path('/var/lib/minecmanager')
UNITS = pathlib.Path('/etc/systemd/system')
JVMS = pathlib.Path('/usr/lib/jvm')
UA = 'MineCManager/0.3 (https://github.com/JIRAYUTRPY/MineCManager)'

def run(*args, timeout=1800):
    p = subprocess.Popen(args, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, start_new_session=True)
    selector=selectors.DefaultSelector();selector.register(p.stdout,selectors.EVENT_READ)
    output=bytearray();deadline=time.monotonic()+timeout
    try:
        while selector.get_map():
            if time.monotonic()>deadline: raise ValueError('command timeout; inspect VM state')
            for key,_ in selector.select(.2):
                chunk=os.read(key.fileobj.fileno(),65536)
                if not chunk: selector.unregister(key.fileobj);continue
                output.extend(chunk)
                if len(output)>1048576: raise ValueError('command output exceeds 1 MiB; redirect or filter output')
        code=p.wait(timeout=max(.1,deadline-time.monotonic()))
        if code: raise ValueError('command failed: '+args[0]+' (exit '+str(code)+')')
        return output.decode(errors='replace')
    finally:
        selector.close()
        if p.poll() is None: os.killpg(p.pid,signal.SIGKILL);p.wait()
        p.stdout.close()

def ident(s):
    if not isinstance(s, str) or not re.fullmatch(r'[a-z][a-z0-9-]{0,31}', s):
        raise ValueError('ID must be lowercase letters/digits/hyphens, starting with a letter, max 32')
    return s

def atomic(path, data, mode=0o600):
    path = pathlib.Path(path)
    fd, tmp = tempfile.mkstemp(prefix='.mcm-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as f:
            f.write(data); f.flush(); os.fchmod(f.fileno(), mode); os.fsync(f.fileno())
        os.replace(tmp, path)
        d = os.open(path.parent, os.O_DIRECTORY); os.fsync(d); os.close(d)
    finally:
        if os.path.exists(tmp): os.unlink(tmp)

def metadata(sid):
    return json.loads((STATE / 'servers' / (ident(sid) + '.json')).read_text())

def unit(sid): return 'mcm-' + ident(sid) + '.service'

def active(sid):
    return subprocess.run(['systemctl', 'is-active', '--quiet', unit(sid)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0

def snapshot_active(sid):
    """A missing unit, lost system bus or transitioning unit cannot prove a stopped world."""
    output = run('systemctl', 'show', '--property=LoadState,ActiveState', unit(sid), timeout=20)
    fields = dict(line.split('=', 1) for line in output.splitlines() if '=' in line)
    if fields.get('LoadState') != 'loaded':
        raise ValueError('cannot verify the managed service; check that its systemd unit is loaded')
    state = fields.get('ActiveState')
    if state not in ('active', 'inactive'):
        raise ValueError('service must reach a stable active or inactive state before backup or restore')
    return state == 'active'

def fetch_json(url):
    with urllib.request.urlopen(urllib.request.Request(url, headers={'User-Agent': UA}), timeout=30) as r:
        return json.loads(r.read(4*1024*1024))

def resolve(platform, version):
    if not re.fullmatch(r'[0-9]+\.[0-9]+(?:\.[0-9]+)?', version): raise ValueError('release version required')
    manifest = fetch_json('https://piston-meta.mojang.com/mc/game/version_manifest_v2.json')
    item = next((v for v in manifest['versions'] if v['id'] == version and v['type'] == 'release'), None)
    if not item: raise ValueError('unknown Minecraft release')
    details = fetch_json(item['url']); java = details.get('javaVersion', {}).get('majorVersion', 8)
    if java not in (17,21,25): raise ValueError('supported Java versions: 17, 21, 25; choose a modern release')
    checksum = None; algorithm = 'sha256'; build = version
    if platform == 'vanilla':
        artifact = details['downloads']['server']; url = artifact['url']; checksum = artifact['sha1']; algorithm = 'sha1'
    elif platform == 'paper':
        builds = fetch_json('https://fill.papermc.io/v3/projects/paper/versions/' + version + '/builds')
        b = next((b for b in builds if b.get('channel') == 'STABLE'), None)
        if not b: raise ValueError('no stable Paper build for this version')
        artifact = b['downloads']['server:default']; url = artifact['url']; checksum = artifact['checksums']['sha256']; build = str(b['id'])
    elif platform == 'purpur':
        b = fetch_json('https://api.purpurmc.org/v2/purpur/' + version + '/latest')
        build = str(b['build']); url = 'https://api.purpurmc.org/v2/purpur/' + version + '/' + build + '/download'
        checksum = b.get('md5'); algorithm = 'md5'
    elif platform == 'fabric':
        loaders = fetch_json('https://meta.fabricmc.net/v2/versions/loader/' + version)
        loader = next(x['loader']['version'] for x in loaders if x['loader']['stable'])
        installer = next(x['version'] for x in fetch_json('https://meta.fabricmc.net/v2/versions/installer') if x['stable'])
        url = 'https://meta.fabricmc.net/v2/versions/loader/' + version + '/' + loader + '/' + installer + '/server/jar'; build = loader + '/' + installer
    else: raise ValueError('unsupported platform')
    return url, checksum, algorithm, java, build

def metrics():
    mem = {x.split(':')[0]: int(x.split()[1])*1024 for x in pathlib.Path('/proc/meminfo').read_text().splitlines()}
    disk = shutil.disk_usage('/')
    def cpu():
        v = list(map(int, pathlib.Path('/proc/stat').read_text().splitlines()[0].split()[1:9])); return sum(v), v[3]+v[4]
    a, idle = cpu(); time.sleep(.2); b, idle2 = cpu()
    return dict(os=pathlib.Path('/etc/os-release').read_text(), cpu_count=os.cpu_count(), cpu_percent=round(100*(1-(idle2-idle)/max(1,b-a)),1), ram_total=mem['MemTotal'], ram_available=mem['MemAvailable'], disk_total=disk.total, disk_free=disk.free, uptime_seconds=float(pathlib.Path('/proc/uptime').read_text().split()[0]))

def deploy(q):
    sid = ident(q['server']); root = BASE / sid; port = int(q['port']); ram = int(q['ram_mb'])
    if q.get('eula') is not True: raise ValueError('explicit Minecraft EULA acceptance required')
    if not 1024 <= port <= 65535 or not 512 <= ram <= 1048576: raise ValueError('invalid port or RAM')
    if root.exists() or (STATE/'servers'/(sid+'.json')).exists(): raise ValueError('server ID/path exists; deploy never overwrites it')
    if (UNITS/unit(sid)).exists(): raise ValueError('service already exists')
    records = list((STATE/'servers').glob('*.json'))
    reserved = sum(json.loads(p.read_text())['ram_mb'] for p in records)
    if ram*1048576 + reserved*1048576 > metrics()['ram_total'] - 512*1048576: raise ValueError('insufficient unreserved RAM (512 MiB OS reserve)')
    if any(json.loads(p.read_text())['port']==port for p in records): raise ValueError('port already reserved')
    with socket.socket() as sock: sock.bind(('0.0.0.0', port))
    url, checksum, algorithm, java, build = resolve(q['platform'], q['version'])
    os_release = pathlib.Path('/etc/os-release').read_text()
    if 'ID=ubuntu' not in os_release: raise ValueError('automated deployment supports Ubuntu only')
    run('apt-get','update')
    run('env','DEBIAN_FRONTEND=noninteractive','apt-get','install','-y','openjdk-'+str(java)+'-jre-headless','ca-certificates')
    try: pwd.getpwnam('mcm-game')
    except KeyError: run('useradd','--system','--user-group','--home-dir',str(BASE),'--shell','/usr/sbin/nologin','mcm-game')
    BASE.mkdir(mode=0o755, parents=True, exist_ok=True)
    root.mkdir(mode=0o750)
    try:
        with urllib.request.urlopen(urllib.request.Request(url,headers={'User-Agent':UA}),timeout=120) as response, open(root/'server.jar','wb') as f:
            size=0; digest=hashlib.new(algorithm)
            while True:
                chunk=response.read(1024*1024)
                if not chunk: break
                size+=len(chunk)
                if size>512*1024*1024: raise ValueError('JAR exceeds 512 MiB')
                digest.update(chunk); f.write(chunk)
        if checksum and digest.hexdigest()!=checksum: raise ValueError('upstream JAR checksum mismatch')
        import zipfile
        with zipfile.ZipFile(root/'server.jar') as z:
            if 'META-INF/MANIFEST.MF' not in z.namelist(): raise ValueError('invalid JAR')
        (root/'eula.txt').write_text('eula=true\n')
        (root/'server.properties').write_text('server-port='+str(port)+'\nonline-mode=true\nenable-rcon=false\n')
        account=pwd.getpwnam('mcm-game')
        for p in [root,*root.iterdir()]: os.chown(p,account.pw_uid,account.pw_gid)
        # Resolve the requested JVM explicitly; do not rely on update-alternatives.
        candidates=list(JVMS.glob('java-'+str(java)+'-openjdk-*/bin/java'))
        if len(candidates)!=1: raise ValueError('unable to locate requested Java runtime')
        service='[Unit]\nDescription=MineCManager '+sid+'\nAfter=network-online.target\n[Service]\nUser=mcm-game\nGroup='+str(account.pw_gid)+'\nWorkingDirectory='+str(root)+'\nExecStart='+str(candidates[0])+' -Xms512M -Xmx'+str(ram)+'M -jar server.jar nogui\nRestart=on-failure\nTimeoutStopSec=180\nKillSignal=SIGINT\nSuccessExitStatus=0 130 143\nUMask=0027\nNoNewPrivileges=true\nProtectSystem=strict\nReadWritePaths='+str(root)+'\nProtectHome=true\nPrivateTmp=true\n[Install]\nWantedBy=multi-user.target\n'
        atomic(UNITS/unit(sid),service.encode(),0o644)
        record=dict(server=sid,platform=q['platform'],version=q['version'],build=build,java=java,port=port,ram_mb=ram,path=str(root),sha256=hashlib.sha256((root/'server.jar').read_bytes()).hexdigest())
        atomic(STATE/'servers'/(sid+'.json'),json.dumps(record).encode())
        run('systemctl','daemon-reload');run('systemctl','enable','--now',unit(sid))
        return record | {'note':'service started; inspect logs for Minecraft Done before connecting'}
    except Exception:
        # Retain diagnostics and any partial world. Never overwrite on retry.
        raise

def file_action(q):
    path=pathlib.Path(q['path'])
    if not path.is_absolute() or '\x00' in str(path): raise ValueError('absolute path required')
    if q['action']=='files':
        return {'entries':[{'name':p.name,'directory':p.is_dir(),'symlink':p.is_symlink(),'size':p.lstat().st_size} for p in list(path.iterdir())[:1000]]}
    if q['action']=='read':
        with open(path,'rb') as f: data=f.read(1048577)
        if len(data)>1048576: raise ValueError('file editor limit 1 MiB')
        return {'content':base64.b64encode(data).decode(),'sha256':hashlib.sha256(data).hexdigest()}
    if q.get('confirm') != str(path): raise ValueError('confirm must equal absolute path')
    if path.is_symlink() or not path.is_file(): raise ValueError('write requires existing regular file; symlinks rejected')
    with open(path,'rb') as f: old=f.read(1048577)
    if len(old)>1048576: raise ValueError('file editor limit 1 MiB')
    if q.get('sha256') != hashlib.sha256(old).hexdigest(): raise ValueError('file changed; reload before saving')
    data=base64.b64decode(q['content'],validate=True)
    if len(data)>1048576: raise ValueError('file editor limit 1 MiB')
    oldstat=path.stat(); backup=STATE/'file-history'/uuid.uuid4().hex;atomic(backup,old)
    atomic(path,data,stat.S_IMODE(oldstat.st_mode));os.chown(path,oldstat.st_uid,oldstat.st_gid)
    return {'sha256':hashlib.sha256(data).hexdigest(),'previous_copy':str(backup)}

def backup(q):
    sid=ident(q['server']);m=metadata(sid);root=pathlib.Path(m['path']);folder=STATE/'backups'/sid;folder.mkdir(parents=True,exist_ok=True)
    name=time.strftime('%Y%m%dT%H%M%SZ',time.gmtime())+'-'+uuid.uuid4().hex[:8]+'.tar.gz';target=folder/name
    was_active=snapshot_active(sid)
    if was_active: run('systemctl','stop',unit(sid))
    try:
        if snapshot_active(sid): raise ValueError('server did not stop')
        # Refuse links/devices so restore has an unambiguous contained tree.
        for p in root.rglob('*'):
            if p.is_symlink() or not (p.is_file() or p.is_dir()): raise ValueError('backup tree contains a link or special file')
        with tarfile.open(str(target)+'.partial','w:gz') as tar: tar.add(root,arcname='server')
        os.replace(str(target)+'.partial',target)
    finally:
        if was_active: run('systemctl','start',unit(sid))
    with open(target,'rb') as f: sha=hashlib.file_digest(f,'sha256').hexdigest()
    atomic(str(target)+'.sha256',sha.encode())
    if q.get('upload_url'):
        u=urllib.parse.urlsplit(q['upload_url']);conn=http.client.HTTPSConnection(u.hostname,timeout=180)
        conn.putrequest('PUT',u.path+'?'+u.query);conn.putheader('Content-Length',str(target.stat().st_size));conn.endheaders()
        with open(target,'rb') as f:
            while chunk:=f.read(1024*1024): conn.send(chunk)
        response=conn.getresponse();response.read(4096)
        if response.status not in (200,201,204): raise ValueError('R2 upload failed; local backup retained')
        conn.close()
    retain=int(q.get('retain',7))
    if not 1<=retain<=365: raise ValueError('retention must be 1..365')
    for p in sorted(folder.glob('*.tar.gz'))[:-retain]: p.unlink();pathlib.Path(str(p)+'.sha256').unlink(missing_ok=True)
    return {'backup':name,'sha256':sha,'bytes':target.stat().st_size,'r2_key':q.get('r2_key','')}

def import_r2(q):
    sid=ident(q['server']);metadata(sid)
    if snapshot_active(sid) or q.get('confirm')!=sid: raise ValueError('stop server and confirm ID before importing R2 backup')
    expected=q.get('sha256','')
    if not re.fullmatch(r'[a-f0-9]{64}',expected): raise ValueError('expected SHA-256 from backup result required')
    folder=STATE/'backups'/sid;folder.mkdir(parents=True,exist_ok=True)
    name=time.strftime('%Y%m%dT%H%M%SZ',time.gmtime())+'-'+uuid.uuid4().hex[:8]+'.tar.gz'
    path=folder/name;partial=pathlib.Path(str(path)+'.partial');digest=hashlib.sha256();size=0
    try:
        with urllib.request.urlopen(q['download_url'],timeout=120) as response,open(partial,'xb') as f:
            budget=max(0,shutil.disk_usage(folder).free-256*1024*1024)
            while chunk:=response.read(1024*1024):
                size+=len(chunk)
                if size>budget: raise ValueError('insufficient disk space for R2 download')
                digest.update(chunk);f.write(chunk)
            f.flush();os.fsync(f.fileno())
        if digest.hexdigest()!=expected: raise ValueError('R2 archive checksum mismatch')
        os.rename(partial,path);atomic(str(path)+'.sha256',expected.encode())
        return restore({'server':sid,'backup':name,'confirm':sid})
    finally: partial.unlink(missing_ok=True)

def restore(q):
    sid=ident(q['server']);m=metadata(sid);root=pathlib.Path(m['path'])
    name=q['backup']
    if not re.fullmatch(r'[0-9TZ]+-[a-f0-9]{8}\.tar\.gz',name): raise ValueError('invalid backup ID')
    target=STATE/'backups'/sid/name
    if q.get('confirm')!=sid or snapshot_active(sid): raise ValueError('stop server and confirm server ID before restoring')
    with open(target,'rb') as f: digest=hashlib.file_digest(f,'sha256').hexdigest()
    if digest!=pathlib.Path(str(target)+'.sha256').read_text(): raise ValueError('backup checksum mismatch')
    stage=pathlib.Path(tempfile.mkdtemp(prefix='.restore-',dir=BASE))
    try:
        with tarfile.open(target) as tar:
            members=tar.getmembers()
            if any(not (m.isfile() or m.isdir()) or not m.name.startswith('server') for m in members): raise ValueError('invalid archive member')
            if sum(m.size for m in members)>shutil.disk_usage(BASE).free: raise ValueError('insufficient restore disk space')
            tar.extractall(stage,filter='data')
        restored=stage/'server';account=pwd.getpwnam('mcm-game')
        for p in [restored,*restored.rglob('*')]:os.chown(p,account.pw_uid,account.pw_gid)
        previous=BASE/(sid+'.before-restore-'+uuid.uuid4().hex)
        if snapshot_active(sid): raise ValueError('server started during restore; stop it before retrying')
        os.rename(root,previous)
        try: os.rename(restored,root)
        except Exception: os.rename(previous,root);raise
        return {'restored':name,'previous_root':str(previous),'state':'stopped'}
    finally: shutil.rmtree(stage)

def main(q):
    if os.geteuid()!=0: raise ValueError('root or sudo access required')
    if q['action'] == 'metrics':
        return metrics()
    STATE.mkdir(mode=0o700,parents=True,exist_ok=True)
    with open(STATE/'control.lock','a') as lock:
        fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
        for d in ('servers','file-history','backups'): (STATE/d).mkdir(mode=0o700,exist_ok=True)
        action=q['action']
        if action=='servers':return {'servers':[json.loads(p.read_text())|{'active':active(p.stem)} for p in (STATE/'servers').glob('*.json')]}
        if action=='deploy':return deploy(q)
        if action in ('files','read','write'):return file_action(q)
        if action=='backup':return backup(q)
        if action=='restore':return restore(q)
        if action=='r2-restore':return import_r2(q)
        if action=='backups':
            sid=ident(q['server']);return {'backups':[{'name':p.name,'bytes':p.stat().st_size} for p in (STATE/'backups'/sid).glob('*.tar.gz')]}
        if action=='service':
            service=q['service'];verb=q['verb']
            if not re.fullmatch(r'[a-zA-Z0-9@_.-]+\.service',service) or service.startswith('-'):raise ValueError('invalid service name')
            if verb not in ('start','stop','restart','status','logs'):raise ValueError('invalid service action')
            if verb=='logs':return {'output':run('journalctl','-u',service,'-n','200','--no-pager',timeout=30)}
            if verb=='status':return {'output':run('systemctl','show',service,'--property=ActiveState,SubState,MainPID,LoadState',timeout=30)}
            if q.get('confirm')!=service:raise ValueError('confirm service name')
            return {'output':run('systemctl',verb,service)}
        if action=='command':
            command=q.get('command','')
            if not command or len(command)>8192 or q.get('confirm')!='ROOT':raise ValueError('explicit ROOT confirmation required')
            # Deliberately privileged, separately authorized OS administrator operation.
            return {'output':run('timeout','120','bash','-lc',command,timeout=130)}
        raise ValueError('unknown action')

if __name__=='__main__':
    try:
        for line in sys.stdin:
            if line.rstrip('\n')=='MCM_INPUT_V1': break
        else: raise ValueError('missing input frame')
        print(json.dumps(main(json.load(sys.stdin))))
    except Exception as e:
        # Avoid leaking file contents, credentials or presigned URLs from exceptions.
        safe = str(e) if isinstance(e,ValueError) else type(e).__name__ + ': remote operation failed'
        print(json.dumps({'error':safe}))
