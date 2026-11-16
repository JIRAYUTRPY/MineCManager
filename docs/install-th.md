# คู่มือติดตั้ง MineCManager บน Ubuntu

คู่มือนี้อ้างอิงโค้ดใน branch `feat/management-scope` ของ PR #2 สำหรับ **เครื่องใหม่** ใช้ Bash และบัญชีที่มี sudo เริ่มจากเครื่องเดียวให้ทำงานครบก่อน แล้วจึงแยก Panel/Agent ตามหัวข้อท้ายเอกสาร

ยังไม่ได้ทดสอบการติดตั้งบน Ubuntu VM จริง: CI ตรวจ Go, browser และ build/test ของ bridge แล้ว แต่ส่วน systemd, Polkit, certificate และการเข้าเกมต้องตรวจบนเครื่องปลายทางตามขั้นตอนนี้

## 1. ภาพรวมและสิ่งที่ต้องเตรียม

| ส่วน | ที่อยู่เริ่มต้น | หน้าที่ |
|---|---|---|
| Nginx | 80 / 443 | HTTPS หน้าเว็บและ API |
| Panel | 127.0.0.1:9080 | หน้า Admin, สิทธิ์, keys, audit |
| Agent | 127.0.0.1:9081 | คุม systemd, plugin/config, backup/restore |
| Paper bridge | 127.0.0.1:9082 | ดู/แก้ inventory และสถิติ runtime |
| Minecraft Java | 25565/TCP | Client เข้าเล่นเกม |

เลือก RAM ของ VM ให้เหลือสำหรับ OS, Panel, Agent และ cache นอกเหนือจาก Java heap ตัวอย่างใช้ `-Xms1G -Xmx2G`; ปรับตามเครื่องและโลกจริง พื้นที่ดิสก์ต้องพอสำหรับ live server + backups + staging restore + root เดิมที่เก็บไว้

- Ubuntu 26.04 LTS, systemd, สิทธิ์ sudo
- Go **1.25 ขึ้นไป**, Java **21**, Maven สำหรับ build bridge
- Paper server **1.21.11** เพื่อให้ตรงกับ baseline ของ bridge ใน repo ไม่ใช่การระบุว่าเป็น Minecraft รุ่นใหม่ที่สุด
- Domain ตัวอย่าง: `mc-panel.siamdevcrew.com` และ `mc-api.siamdevcrew.com`
- รุ่นนี้เก็บ node/key เป็น JSON และ audit เป็น JSONL **ยังไม่ต้องติดตั้ง PostgreSQL/MongoDB**

หากมี server/world เดิม ห้ามรันขั้นตอนสร้างทับ path เดิม ให้หยุดเกม สำรองข้อมูล และตรวจ ownership ก่อนย้ายเข้าตาม layout นี้

## 2. ติดตั้ง dependency และดึง source

```bash
sudo apt update
sudo apt install -y git curl ca-certificates openssl python3 jq \
  build-essential golang-go openjdk-21-jdk-headless maven polkitd

go version
java -version
mvn -version

git clone --branch feat/management-scope --single-branch \
  https://github.com/JIRAYUTRPY/MineCManager.git
cd MineCManager
```

ถ้า repo เป็น private ให้ใช้ GitHub credential ของคุณหรือดาวน์โหลด source ของ PR หลัง login หาก branch ถูก merge/delete แล้ว ให้ใช้ `main` ที่มี commit นี้แทน

ตรวจว่า Go >= 1.25; หาก package ของเครื่องเก่ากว่านั้นให้ติดตั้งตาม [Go installation](https://go.dev/doc/install) ก่อน build ถ้ามี Java หลายรุ่น ใช้ `JAVA_HOME` ชี้ JDK 21 สำหรับ Maven และใช้ Java 21 ใน unit ของ Minecraft ด้านล่าง

```bash
mkdir -p bin
go test -race ./...
go vet ./...
go build -o bin/mcm-agent ./cmd/agent
go build -o bin/mcm-panel ./cmd/panel
mvn -B -ntp -f paper-bridge/pom.xml verify
```

ผลลัพธ์คือ `bin/mcm-agent`, `bin/mcm-panel` และ `paper-bridge/target/mcm-paper-bridge-0.2.0.jar` หากไม่ build Java เอง ดาวน์โหลด artifact `mcm-paper-bridge` จาก GitHub Actions ของ **commit เดียวกับ source** แล้วแตก ZIP ให้ได้ JAR และวางไว้ที่ `paper-bridge/target/mcm-paper-bridge-0.2.0.jar` เพื่อให้คำสั่งติดตั้งด้านล่างใช้ได้

บันทึก revision ที่กำลังจะ deploy ก่อนติดตั้ง:

```bash
git rev-parse HEAD
sha256sum bin/mcm-agent bin/mcm-panel paper-bridge/target/mcm-paper-bridge-0.2.0.jar
```

เก็บผลลัพธ์นี้ในบันทึก deploy โดยไม่รวม secrets; checksum ที่คำนวณเองใช้เทียบไฟล์ก่อน/หลังโอน ไม่ใช่หลักฐานยืนยันผู้เผยแพร่ ให้รับ artifact จาก repository/CI ที่เชื่อถือได้

รัน `python3 scripts/smoke-test.py` ได้ก่อนติดตั้ง services โดยพอร์ต 9080–9082 ต้องว่าง การทดสอบนี้ใช้ systemctl/Paper จำลอง ไม่แตะ Minecraft จริง

## 3. สร้าง user และ directory

รันจาก directory repo ต่อเนื่องกับข้อก่อนหน้า:

```bash
getent group minecraft >/dev/null || sudo groupadd --system minecraft
id minecraft >/dev/null 2>&1 || sudo useradd --system --gid minecraft \
  --home-dir /srv/minecraft-host/server --no-create-home --shell /usr/sbin/nologin minecraft
id mcm-agent >/dev/null 2>&1 || sudo useradd --system --gid minecraft \
  --home-dir /var/lib/mcm-agent --no-create-home --shell /usr/sbin/nologin mcm-agent
id mcm-panel >/dev/null 2>&1 || sudo useradd --system --user-group \
  --home-dir /var/lib/mcm-panel --no-create-home --shell /usr/sbin/nologin mcm-panel

sudo install -d -o root -g minecraft -m 0770 /srv/minecraft-host
sudo install -d -o minecraft -g minecraft -m 0770 /srv/minecraft-host/server
sudo install -d -o minecraft -g minecraft -m 0770 /srv/minecraft-host/server/plugins
sudo install -d -o mcm-agent -g minecraft -m 0700 /var/lib/mcm-agent
sudo install -d -o mcm-panel -g mcm-panel -m 0700 /var/lib/mcm-panel
sudo install -d -o root -g root -m 0755 /etc/mcm
sudo install -m 0755 bin/mcm-agent /usr/local/bin/mcm-agent
sudo install -m 0755 bin/mcm-panel /usr/local/bin/mcm-panel
```

ถ้า user มีอยู่แล้ว ให้ตรวจ `id mcm-agent`: **primary group ต้องเป็น minecraft** อย่าเปลี่ยนกลุ่มของบัญชีที่ใช้งานอื่นอยู่โดยไม่ตรวจผลกระทบ

แยก user เพื่อไม่ให้ Minecraft อ่าน key/state ของระบบจัดการ ส่วน game files ใช้ group `minecraft` ร่วมกัน Agent ต้องเขียน parent `/srv/minecraft-host` ได้เพื่อ restore ด้วย directory rename อย่าตั้ง root ให้เป็น symlink หรือ mount point แยกจาก parent เพราะ rename ตอน restore จะใช้ไม่ได้

## 4. สร้าง token และ bootstrap config

มี secret 3 ชุด: Agent token, Paper bridge token และ Panel bootstrap API key อย่าใช้ชุดเดียวกัน

คำสั่งนี้สร้างไฟล์ใหม่เท่านั้นและหยุดเมื่อพบไฟล์เดิม **อย่ารันซ้ำเพื่อ rotate key** ใช้ API key lifecycle/คู่มือ rotation แทน Secret ไม่ถูกพิมพ์ลง terminal ระหว่างสร้าง

```bash
sudo python3 - <<'PY'
import hashlib, json, os, pathlib, pwd, secrets
paths = [
    '/etc/mcm/agent.env', '/etc/mcm/paper.env', '/etc/mcm/panel.env',
    '/var/lib/mcm-panel/panel.json', '/root/mcm-bootstrap.key',
]
if any(pathlib.Path(p).exists() for p in paths):
    raise SystemExit('พบ config/key เดิม: หยุดเพื่อไม่เขียนทับ ให้ตรวจไฟล์ก่อน')
agent_token, paper_token, panel_key = [secrets.token_hex(32) for _ in range(3)]
cfg = json.loads(pathlib.Path('config/panel.example.json').read_text())
cfg['keys'][0]['sha256'] = hashlib.sha256(panel_key.encode()).hexdigest()
def create(path, text, user='root'):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as f:
        f.write(text)
        f.flush()
        os.fsync(f.fileno())
    account = pwd.getpwnam(user)
    os.chown(path, account.pw_uid, account.pw_gid)
create('/etc/mcm/agent.env',
    f'MCM_AGENT_TOKEN={agent_token}\nMCM_PAPER_TOKEN={paper_token}\n'
    'MCM_SERVER_ROOT=/srv/minecraft-host/server\n'
    'MCM_SYSTEMD_UNIT=minecraft.service\nMCM_STATE_ROOT=/var/lib/mcm-agent\n')
create('/etc/mcm/paper.env', f'MCM_PAPER_TOKEN={paper_token}\n')
create('/etc/mcm/panel.env',
    'MCM_PANEL_CONFIG=/var/lib/mcm-panel/panel.json\n'
    'MCM_AUDIT_PATH=/var/lib/mcm-panel/audit.jsonl\n'
    f'MCM_NODE_01_TOKEN={agent_token}\n')
create('/var/lib/mcm-panel/panel.json', json.dumps(cfg, indent=2)+'\n', 'mcm-panel')
create('/root/mcm-bootstrap.key', panel_key+'\n')
print('สร้าง config และ key แล้ว; bootstrap key อยู่ที่ /root/mcm-bootstrap.key')
PY
```

หากขั้นตอนล้มเหลวกลางทาง ไฟล์บางส่วนอาจถูกสร้างแล้ว ให้ตรวจและแก้ไขไฟล์ที่สร้างในครั้งนั้นก่อน ไม่ลบไฟล์เดิมทั้งหมดเพื่อรันซ้ำ

## 5. เตรียม Paper และยอมรับ EULA ด้วยตัวเอง

ดาวน์โหลด **Paper 1.21.11** จาก [Paper downloads](https://papermc.io/downloads/paper) เลือก version/build และตรวจ SHA-256 กับข้อมูลจากผู้ให้บริการ เก็บ build number/checksum ไว้กับบันทึก deploy วางไฟล์ที่ดาวน์โหลดเป็น `/tmp/paper-1.21.11.jar` แล้วติดตั้ง:

```bash
sha256sum /tmp/paper-1.21.11.jar
sudo install -o minecraft -g minecraft -m 0660 \
  /tmp/paper-1.21.11.jar /srv/minecraft-host/server/paper.jar
sudo install -o minecraft -g minecraft -m 0660 \
  paper-bridge/target/mcm-paper-bridge-0.2.0.jar \
  /srv/minecraft-host/server/plugins/MineCManagerBridge.jar
```

อ่าน [Minecraft EULA](https://www.minecraft.net/eula) ก่อน หากคุณยอมรับแล้วจึงสร้างไฟล์ต่อไปนี้:

```bash
printf 'eula=true\n' | sudo tee /srv/minecraft-host/server/eula.txt >/dev/null
sudo chown minecraft:minecraft /srv/minecraft-host/server/eula.txt
sudo chmod 0660 /srv/minecraft-host/server/eula.txt
```

สร้าง `server.properties` สำหรับเครื่องใหม่ เปิด authentication ของ Minecraft ไว้ ตัวอย่างนี้เปิด whitelist; เพิ่มผู้เล่นในขั้นตอนถัดไป:

```bash
sudo tee /srv/minecraft-host/server/server.properties >/dev/null <<'PROPERTIES'
server-port=25565
online-mode=true
white-list=true
enforce-whitelist=true
enable-rcon=false
motd=MineCManager Server
PROPERTIES
sudo chown minecraft:minecraft /srv/minecraft-host/server/server.properties
sudo chmod 0660 /srv/minecraft-host/server/server.properties
```

เติม `whitelist.json` ด้วย UUID ของบัญชี Minecraft ที่ตรวจสอบแล้ว: `[{"uuid":"UUID-จริง","name":"ชื่อบัญชีจริง"}]` ใช้ UUID รูปแบบมีขีดจากบัญชีที่ถูกต้อง อย่าใช้ offline UUID หรือปิด `online-mode` เพื่อแก้ปัญหาเข้าเกมไม่ได้ ตั้ง owner `minecraft:minecraft` และ mode `0660` เช่นเดียวกับ config ด้านบน

### ตรวจสิทธิ์ก่อนเริ่ม services

คำสั่งนี้อ่านเฉพาะข้อมูลสิทธิ์ ไม่สร้างไฟล์ในโลกเกม:

```bash
sudo -u mcm-agent test -w /srv/minecraft-host
sudo -u mcm-agent test -w /srv/minecraft-host/server
sudo -u minecraft test -w /srv/minecraft-host/server
sudo -u mcm-panel test -r /var/lib/mcm-panel/panel.json
sudo -u mcm-panel test -w /var/lib/mcm-panel
sudo -u mcm-agent test -w /var/lib/mcm-agent
sudo -u minecraft test ! -r /etc/mcm/agent.env
sudo -u minecraft test ! -r /var/lib/mcm-panel/panel.json
sudo -u minecraft test ! -x /var/lib/mcm-agent
```

ทุกบรรทัดต้อง exit 0 (ไม่มี output) หากไม่ผ่าน ให้แก้ user/group/permissions ของ path นั้นก่อนเริ่ม services ตรวจ `namei -l PATH` เพื่อดูสิทธิ์ของ parent ทุกชั้น ไม่แก้โดยใช้ `chmod 777` หรือรัน Agent เป็น root

## 6. ติดตั้ง systemd และ Polkit

ตัวอย่าง unit สำหรับเครื่อง **amd64** ใช้ Java 21 แบบระบุ path โดยตรง หากเป็น ARM64 เปลี่ยน path ให้ตรงกับผล `dpkg -L openjdk-21-jre-headless | grep '/bin/java$'`

```bash
sudo tee /etc/systemd/system/minecraft.service >/dev/null <<'UNIT'
[Unit]
Description=Minecraft Paper Server
After=network-online.target
[Service]
User=minecraft
Group=minecraft
WorkingDirectory=/srv/minecraft-host/server
EnvironmentFile=/etc/mcm/paper.env
ExecStart=/usr/lib/jvm/java-21-openjdk-amd64/bin/java -Xms1G -Xmx2G -jar paper.jar --nogui
Restart=on-failure
RestartSec=10
KillSignal=SIGINT
KillMode=control-group
TimeoutStopSec=180
UMask=0007
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/srv/minecraft-host/server
[Install]
WantedBy=multi-user.target
UNIT

sudo install -m 0644 deploy/mcm-agent.service /etc/systemd/system/mcm-agent.service
sudo install -m 0644 deploy/mcm-panel.service /etc/systemd/system/mcm-panel.service
sudo install -m 0644 deploy/49-mcm-agent.rules /etc/polkit-1/rules.d/49-mcm-agent.rules
sudo systemctl daemon-reload
sudo systemctl enable mcm-agent mcm-panel
sudo systemctl start mcm-agent mcm-panel
sudo systemctl start minecraft
sudo systemctl --no-pager status mcm-agent mcm-panel minecraft
```

ยังไม่ enable Minecraft ให้เริ่มอัตโนมัติจนกว่าจะผ่านการทดสอบรอบแรก หากชื่อ unit ต่างจาก `minecraft.service` ต้องแก้ทั้ง unit name, `MCM_SYSTEMD_UNIT` และ Polkit rule ให้ตรงกัน

ดูผล startup:

```bash
sudo journalctl -u mcm-agent -u mcm-panel -u minecraft -n 100 --no-pager
sudo ss -lntp | grep -E ':9080|:9081|:9082|:25565'
```

คาดหวัง 9080/9081/9082 ฟังเฉพาะ `127.0.0.1`; 25565 ขึ้นเมื่อ Paper เริ่มสำเร็จ ไม่ควรเห็น error ว่า bridge token ขาดหรือ JAR ไม่เข้ากับ API version

## 7. เข้าหน้าเว็บและทดสอบ API ครั้งแรก

ก่อนตั้ง HTTPS ให้ใช้ SSH tunnel จากเครื่องของคุณ:

```bash
ssh -N -L 9080:127.0.0.1:9080 YOUR_SSH_USER@YOUR_VM_IP
```

เปิด `http://127.0.0.1:9080` บนเครื่องที่เปิด tunnel แสดง bootstrap key ด้วย `sudo cat /root/mcm-bootstrap.key` ใน session ส่วนตัว คัดลอกไปเก็บใน password manager และใช้กรอกหน้าเว็บ ห้ามส่ง key ใน URL หรือเปิดหน้า public HTTP แล้วกรอก key

ทดสอบ API จาก VM ด้วย Bash helper นี้ โดยไม่ใส่ key ลง command history/argument ของ curl:

```bash
read -r -s -p 'Panel API key: ' MCM_API_KEY
printf '\n'
mcm_api() {
  curl --fail-with-body --silent --show-error \
    --config <(printf 'header = "Authorization: Bearer %s"\n' "$MCM_API_KEY") "$@"
}
mcm_api http://127.0.0.1:9080/v1/nodes | jq
mcm_api http://127.0.0.1:9080/v1/nodes/minecraft-01/status | jq
mcm_api http://127.0.0.1:9080/v1/nodes/minecraft-01/metrics | jq
mcm_api http://127.0.0.1:9080/v1/nodes/minecraft-01/paper/players | jq
```

คาดหวัง node `minecraft-01`, status `active`, metrics และรายชื่อผู้เล่น (`[]` หากยังไม่มีใครออนไลน์) ชุด key ที่ helper นี้ใช้เป็น hex ที่สร้างไว้ หลีกเลี่ยงใช้ helper กับ custom key ที่มี quote/newline

ตรวจ service action ผ่าน Panel:

```bash
mcm_api -X POST -H 'Content-Type: application/json' \
  -d '{"action":"stop"}' http://127.0.0.1:9080/v1/nodes/minecraft-01/actions
mcm_api http://127.0.0.1:9080/v1/nodes/minecraft-01/status | jq
mcm_api -X POST http://127.0.0.1:9080/v1/nodes/minecraft-01/backups | jq
mcm_api -X POST -H 'Content-Type: application/json' \
  -d '{"action":"start"}' http://127.0.0.1:9080/v1/nodes/minecraft-01/actions
```

หยุดแล้วต้องได้ `inactive`; สร้าง backup แล้วต้องได้ ID และ manifest หากยัง `deactivating` ให้รอจนหยุดสมบูรณ์ก่อน การแก้ plugin/config และ restore ทำได้เฉพาะตอนหยุด

หลังทดสอบเสร็จ `unset MCM_API_KEY` และ `unset -f mcm_api` หยุด/เริ่มผ่านเว็บได้ในครั้งต่อไป ตรวจ audit ที่หน้า Admin หรือ `/v1/audit` และสร้าง key แยกตามงาน อย่าแจก bootstrap key ให้ทุกคน

## 8. เปิด HTTPS ด้วย Nginx

ตั้ง DNS A ของสองโดเมนมาที่ VM และตรวจ AAAA ให้ถูกต้องด้วย ตั้ง firewall/cloud firewall ให้เข้า TCP 80/443 ได้เพื่อออก certificate และเปิด 25565 เฉพาะเมื่อพร้อมให้เข้าเกม คง SSH access ของคุณไว้ **ไม่เปิด public 9080–9082**

ถ้าใช้ Cloudflare ให้เริ่มด้วย DNS-only เพื่อแยกปัญหา certificate/origin จาก proxy หลัง HTTPS ใช้ได้จึงปรับตามนโยบายของคุณ Client Minecraft ควรใช้ DNS record สำหรับเกมที่เชื่อมต่อ TCP 25565 ได้โดยตรง ไม่ใช่ HTTP proxy ปกติ

```bash
sudo apt install -y nginx certbot python3-certbot-nginx
sudo tee /etc/nginx/sites-available/minecmanager >/dev/null <<'NGINX'
server {
    listen 80;
    server_name mc-panel.siamdevcrew.com;
    client_max_body_size 17m;
    location / {
        proxy_pass http://127.0.0.1:9080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 660s;
        proxy_send_timeout 120s;
    }
}
server {
    listen 80;
    server_name mc-api.siamdevcrew.com;
    client_max_body_size 17m;
    location /v1/ {
        proxy_pass http://127.0.0.1:9080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 660s;
        proxy_send_timeout 120s;
    }
    location / { return 404; }
}
NGINX
sudo ln -s /etc/nginx/sites-available/minecmanager /etc/nginx/sites-enabled/minecmanager
sudo nginx -t
sudo systemctl reload nginx
sudo certbot --nginx -d mc-panel.siamdevcrew.com -d mc-api.siamdevcrew.com --redirect
sudo certbot renew --dry-run
```

ถ้า symlink มีอยู่แล้ว ตรวจว่าเป็น config เดียวกันก่อน ไม่เขียนทับ site อื่น คำสั่ง Certbot จะถาม email/เงื่อนไขกับคุณ ห้ามกรอก Panel key จน certificate ถูกต้องและ redirect ไป HTTPS แล้ว

- หน้าเว็บ: `https://mc-panel.siamdevcrew.com`
- API ภายนอก: `https://mc-api.siamdevcrew.com/v1/nodes`
- หน้าเว็บยังเรียก `/v1/` บนโดเมนของตัวเองด้วย จึงต้องให้ `mc-panel` proxy API ได้ ไม่ต้องเปิด CORS ข้ามโดเมน
- ไม่ต้องใส่ Agent token ใน Nginx; Panel เป็นผู้แนบ token ไปยัง Agent

## 9. แยก Panel กับ Agent คนละ VM ด้วย mTLS

ทำหลัง single-host ผ่านแล้ว: VM เกมรัน Minecraft + Agent + bridge ส่วน VM ควบคุมรัน Panel + Nginx คู่มือนี้ใช้ private DNS `agent01.internal` ซึ่ง Panel ต้อง resolve ได้

### ย้าย Panel เดิมโดยคง keys และ audit

อย่าสร้าง bootstrap ชุดใหม่บน VM Panel หากต้องการเก็บ key IDs และ audit history เดิม ให้ดำเนินการดังนี้:

1. เตรียม user `mcm-panel`, binary, `/var/lib/mcm-panel`, `/etc/mcm` และ service unit บน VM ใหม่ แต่ยังไม่ start Panel
2. หยุด `mcm-panel` บน VM เดิมก่อนคัดลอก state ให้ไม่มี API mutation ระหว่างย้าย; Minecraft/Agent ยังทำงานต่อได้
3. โอน `/var/lib/mcm-panel/panel.json`, `/var/lib/mcm-panel/audit.jsonl` และ `/etc/mcm/panel.env` ผ่านช่องทางผู้ดูแลที่ปลอดภัย ไม่ส่งผ่าน Git หรือแนบใน issue ไม่ต้องโอนไฟล์ `panel.json.lock`
4. บน VM ใหม่ตั้ง directory `/var/lib/mcm-panel` เป็น owner `mcm-panel:mcm-panel`, mode 0700; ตั้ง JSON/audit เป็น owner เดียวกัน mode 0600 และ `/etc/mcm/panel.env` เป็น root:root mode 0600 ถ้ายังไม่มี audit file ให้ service สร้างเอง ไม่ทับ audit ที่มีอยู่ด้วยไฟล์ว่าง
5. ออกและติดตั้ง certificates ตามหัวข้อถัดไป แล้วแก้ node URL/Panel env บนเครื่องใหม่ก่อนเริ่ม service
6. ตรวจ API ผ่าน loopback ของ Panel ใหม่ จากนั้นย้าย DNS/Nginx และตรวจ HTTPS จาก client หลังผ่านจึง `sudo systemctl disable mcm-panel` บน VM เดิมเพื่อป้องกันเปิด controller เก่ากลับมา

หาก rollback การย้าย ให้หยุด Panel ใหม่ก่อน และตรวจว่ามีการสร้าง/revoke key หรือแก้ registration บนเครื่องใหม่หรือไม่ ต้องนำ config/audit ล่าสุดกลับด้วย มิฉะนั้น key ที่ถูก revoke อาจกลับมาใช้ได้จาก config เก่า อย่าเปิดสอง Panel พร้อมกันระหว่างสลับกลับ

### Certificate สำหรับ Agent และ Panel

สร้าง PKI ในเครื่องผู้ดูแลที่เชื่อถือได้ ไม่ใช่ใน game directory ตัวอย่างนี้สร้าง CA ใหม่สำหรับระบบนี้โดยเฉพาะ; ถ้ามีองค์กร PKI ให้ขอ certificate จากระบบนั้นแทน:

```bash
mkdir -m 0700 mcm-pki
cd mcm-pki
umask 077
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 -out ca.key
openssl req -x509 -new -key ca.key -sha256 -days 3650 \
  -subj '/CN=MineCManager CA' -out ca.crt \
  -addext 'basicConstraints=critical,CA:TRUE' -addext 'keyUsage=critical,keyCertSign,cRLSign'
openssl req -new -newkey rsa:3072 -nodes -keyout agent.key -out agent.csr -subj '/CN=agent01.internal'
printf '%s\n' 'basicConstraints=critical,CA:FALSE' 'keyUsage=critical,digitalSignature,keyEncipherment' \
  'extendedKeyUsage=serverAuth' 'subjectAltName=DNS:agent01.internal' > agent.ext
openssl x509 -req -in agent.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out agent.crt -days 365 -sha256 -extfile agent.ext
openssl req -new -newkey rsa:3072 -nodes -keyout panel.key -out panel.csr -subj '/CN=mcm-panel'
printf '%s\n' 'basicConstraints=critical,CA:FALSE' 'keyUsage=critical,digitalSignature' \
  'extendedKeyUsage=clientAuth' > panel.ext
openssl x509 -req -in panel.csr -CA ca.crt -CAkey ca.key -CAserial ca.srl \
  -out panel.crt -days 365 -sha256 -extfile panel.ext
openssl verify -CAfile ca.crt -purpose sslserver agent.crt
openssl verify -CAfile ca.crt -purpose sslclient panel.crt
```

เก็บ `ca.key` ไว้ออฟไลน์/ระบบ secrets ของผู้ดูแล **ไม่คัดลอกไปสอง VM** ส่งไฟล์ผ่านช่องทางที่เชื่อถือได้ แล้วติดตั้งดังนี้:

| VM | Directory (0700) | Owner | ไฟล์ (0600) |
|---|---|---|---|
| เกม | /etc/mcm/tls/agent | mcm-agent: minecraft | agent.crt, agent.key, ca.crt |
| Panel | /etc/mcm/tls/panel | mcm-panel: mcm-panel | panel.crt, panel.key, ca.crt |

parent `/etc/mcm/tls` ใช้ 0755 เพื่อให้แต่ละ service traverse ไป directory ของตัวเองได้ ใช้ `sudo install -d` และ `sudo install -o USER -g GROUP -m 0600 SOURCE TARGET` ตามตาราง ต้องสร้าง user/service directories บนแต่ละ VM เช่นเดียวกับ single-host เฉพาะส่วนที่เครื่องนั้นใช้

เพิ่ม/แก้ด้วย `sudoedit /etc/mcm/agent.env` บน VM เกม:

```dotenv
MCM_AGENT_ADDR=0.0.0.0:9081
MCM_SERVER_CERT=/etc/mcm/tls/agent/agent.crt
MCM_SERVER_KEY=/etc/mcm/tls/agent/agent.key
MCM_CA_CERT=/etc/mcm/tls/agent/ca.crt
```

เพิ่มใน `/etc/mcm/panel.env` บน VM Panel:

```dotenv
MCM_CLIENT_CERT=/etc/mcm/tls/panel/panel.crt
MCM_CLIENT_KEY=/etc/mcm/tls/panel/panel.key
MCM_CA_CERT=/etc/mcm/tls/panel/ca.crt
```

`MCM_NODE_01_TOKEN` บน Panel ต้องตรงกับ `MCM_AGENT_TOKEN` บน VM เกม โดยคัดลอกเฉพาะ secret นี้ผ่านช่องทางผู้ดูแล ไม่คัดลอก `agent.env` ทั้งไฟล์ให้ Panel เพิ่ม firewall allow TCP 9081 **เฉพาะ private IP ของ Panel** ก่อนเปิด listener

หยุด Panel (`sudo systemctl stop mcm-panel`) แล้วแก้ `nodes[].url` ของ `minecraft-01` ใน `/var/lib/mcm-panel/panel.json` เป็น `https://agent01.internal:9081` ด้วย `sudoedit` รักษา owner/mode เดิม จากนั้น restart Agent บน VM เกม และ start Panel บน VM ควบคุม

ตรวจ `getent hosts agent01.internal` บน Panel, เรียก `/v1/nodes/minecraft-01/status` ผ่าน Panel และทดสอบ HTTPS ไป Agent โดยไม่มี client cert ซึ่งต้องเชื่อมต่อไม่สำเร็จ ใช้ SAN ที่ตรงกับ URL จริง; หากใช้ IP ต้องออก certificate ที่มี `IP:...` SAN แทน DNS อย่างเดียว **ห้ามแก้ด้วยการปิด certificate verification**

HTTP API ของ bridge ยังอยู่บน loopback VM เกม ไม่ต้องเปิดเพิ่ม และ Minecraft ไม่ต้องมี client certificate ของ Panel

## 10. ตรวจรับก่อนเปิดใช้งาน

- Login หน้าเว็บผ่าน HTTPS; key อ่านอย่างเดียวต้องสั่ง restart/แก้ inventory ไม่ได้
- Agent คุมได้เฉพาะ `minecraft.service`; ตรวจ Polkit rule และลอง action ที่ไม่อนุญาตบน unit ทดสอบที่ไม่มีผลกับงานจริง
- หยุด → แก้ config → rollback → เริ่ม ต้องทำงานครบ
- ทดสอบ backup/restore **บนโลกทดสอบ** ตรวจว่า world/plugins กลับมา และ Minecraft เขียนข้อมูลต่อได้
- เข้าด้วย Minecraft Java version ตรงกับ server และบัญชีใน whitelist; `/paper/players` ต้องเห็น UUID
- อ่าน inventory ก่อนแทนที่ item; ย้าย item ในเกมแล้วส่ง hash เดิมต้องได้ 412
- ตรวจ audit มี intent/result และ key/node/action ถูกต้อง
- เก็บ backup ของ `/var/lib/mcm-agent`, `/var/lib/mcm-panel`, `/etc/mcm`, units และ Polkit แยกนอกเครื่อง พร้อมสิทธิ์เข้าถึงที่เหมาะสม snapshot ของ Agent ครอบคลุม **server root เท่านั้น**

หลังผ่านจึง `sudo systemctl enable minecraft` เปิดพอร์ตเกมตามนโยบาย และเก็บบันทึก version/build/checksum ที่ deploy

### ทดสอบสิทธิ์และหมุนเวียน Panel key

หลัง bootstrap ทำงาน ให้ใช้หน้า Administration สร้าง key ตามหน้าที่ เช่น `nodes:read,plugins:read` พร้อม node restriction `minecraft-01` ทดสอบ key ใหม่นี้ด้วยการอ่าน status และลอง POST action: ต้องได้ 403 โดยไม่มีการสั่งงาน Agent

เมื่อต้องเปลี่ยน admin key ให้สร้าง key ใหม่ที่มี `admin:keys` และ scopes ที่ต้องใช้ เก็บ token ที่แสดงครั้งเดียวใน password manager ทดสอบ login/API ด้วย key ใหม่ในอีก session แล้วจึง revoke key เก่า ห้าม revoke key ที่กำลังใช้โดยยังไม่เก็บและทดสอบ key ใหม่ หลัง revoke bootstrap สำเร็จ ไฟล์ `/root/mcm-bootstrap.key` จะใช้ login ไม่ได้อีก ให้จัดการไฟล์นี้ตามนโยบาย secrets ของคุณ

Agent token และ Paper bridge token เปลี่ยนผ่าน env files คนละขั้นตอนกับ Panel key:

| Secret | ต้องแก้ที่ใดให้ตรงกัน | Service ที่ต้อง restart |
|---|---|---|
| Agent token | `MCM_AGENT_TOKEN` ใน agent.env และ `MCM_NODE_01_TOKEN` ใน panel.env | mcm-agent, mcm-panel |
| Paper token | `MCM_PAPER_TOKEN` ใน agent.env และ paper.env | mcm-agent, minecraft |

ระบบยังไม่รองรับรับ token เก่า/ใหม่พร้อมกัน จัดช่วงเปลี่ยนให้ไม่มี mutation ค้าง และคาดว่าจะเรียก API ไม่ได้ชั่วคราวจนทุก service โหลดค่าใหม่ครบ อย่ารันสคริปต์ bootstrap ซ้ำเพื่อหมุนเวียน secret

## 11. อัปเดตและกู้คืน

1. ตรวจ commit ที่จะใช้และให้ CI ผ่าน เก็บ binary/JAR ชุดเดิมและ backup config/state
2. สร้าง backup เกมขณะหยุด จากนั้นหยุด `minecraft`, `mcm-agent`, `mcm-panel`
3. Build จาก commit ที่เลือก ใช้ `install` แทน binaries/JAR ที่ path เดิม **ไม่รันข้อสร้าง secrets ใหม่**
4. เริ่ม Agent/Panel ตรวจ API ก่อนเริ่ม Minecraft แล้วตรวจ bridge/เข้าเกม
5. หากต้อง rollback binary/JAR ให้หยุด services แล้วคืนไฟล์ชุดเดิม; อย่า downgrade world ที่ถูก upgrade โดยไม่มี world backup ที่เข้ากัน

Restore ต้องมีพื้นที่ staging และจะเก็บ root เก่าไว้ใน `.mcm-restore-*-previous` หากไฟดับระหว่าง rename อาจต้องคืนชื่อ directory ด้วยผู้ดูแล อ่าน [ขั้นตอนและข้อจำกัด backup](backup.md) ก่อนกู้คืน ไม่ลบ root เก่าจนกว่าจะทดสอบเกมผ่าน

## 12. Troubleshooting

| อาการ | ตรวจอะไร / วิธีแก้ |
|---|---|
| Agent ไม่ขึ้น | `journalctl -u mcm-agent`; token >= 32 chars, root มีอยู่และเป็น absolute directory, state อยู่นอก root |
| `another daemon owns ...` | มี process อีกตัวใช้ state/config เดียวกัน; ตรวจ process และหยุดตัวซ้ำ ไม่ลบ lock เพื่อบังคับรันสองตัว |
| Panel 403 | key ผิด/หมดอายุ/revoked, scope หรือ node restriction ไม่ตรง อย่าสับสนกับ agent token |
| Panel 502 | Agent/Paper ไม่ขึ้น, token ไม่ตรง, URL/certificate/firewall ผิด; mutation อาจมีผลแล้ว ให้ตรวจ state ก่อน retry |
| `audit unavailable` / 503 | ตรวจ ownership/พื้นที่ดิสก์/สิทธิ์ แล้วหยุด Panel และเก็บสำเนา log ก่อนตรวจรายการค้าง หลังแก้ต้อง restart; ระบบจะไม่กลับมาส่งคำสั่งเองและไม่ยอมเริ่มหาก log มี record เสีย/ไม่ครบ ดูวิธีกู้คืนใน [Panel audit recovery](panel.md#audit-failure-recovery) |
| service action 502 | ดู Agent log; ตรวจ Polkit user/unit/verb และ `systemctl status minecraft` |
| edit/backup 409 | ต้อง `inactive`; service `failed` หรือ `unknown` ไม่ถือว่ายืนยันว่าหยุด ตรวจสาเหตุก่อนทำต่อ |
| config/plugin/item 412 | อ่านข้อมูล/hash ใหม่แล้วส่ง quoted `If-Match` ที่ตรงกับปัจจุบัน |
| upload 413 | JAR limit 16 MiB; Nginx body limit ต้องไม่น้อยกว่า request; config limit 1 MiB |
| bridge 503 / ไม่มี 9082 | ตรวจ JAR version, Java 21 และ `MCM_PAPER_TOKEN` ใน Minecraft/Agent ต้องตรงกัน; restart หลังแก้ env |
| เข้าเกมไม่ได้ | Paper startup, TCP 25565, version, `online-mode`, whitelist UUID; เป็นคนละ port กับ Panel |
| restore rename ล้มเหลว | parent เขียนได้หรือไม่, root เป็น mount point/symlink หรือไม่, staging อยู่ filesystem เดียวกับ root |
| เกมเขียนไม่ได้หลัง restore | `mcm-agent` primary group = minecraft, Minecraft อยู่กลุ่มเดียวกัน, root 0770 และ restored files group writable |
| ย้าย Panel แล้ว key เก่าไม่ทำงานหรือ audit หาย | ตรวจว่าโอน panel.json/audit เดิมก่อน start และไม่ได้สร้าง bootstrap ใหม่; หยุด service ก่อนแก้ state |
| certificate verify failed | ตรวจชื่อ SAN, CA, วันหมดอายุ, client/server EKU และนาฬิกา VM |

## เอกสารอ้างอิง

- [Paper getting started และตาราง Java](https://docs.papermc.io/paper/getting-started/)
- [Ubuntu 26.04: OpenJDK 21](https://packages.ubuntu.com/resolute/openjdk-21-jre-headless)
- [Ubuntu 26.04: Certbot Nginx](https://packages.ubuntu.com/resolute/python3-certbot-nginx)
- [Panel scopes/key lifecycle](panel.md), [Paper bridge](paper-bridge.md), [OpenAPI](../api/openapi.yaml)
