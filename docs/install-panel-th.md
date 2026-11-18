# ติดตั้ง MineCManager แบบ Panel เดียว

คู่มือนี้ใช้ flow ใหม่: **ติดตั้ง Panel → เพิ่ม VM ผ่าน SSH → ดูทรัพยากร → ติดตั้ง Minecraft** ไม่ต้องติดตั้ง Agent, Go หรือ Java บนเครื่องเกมด้วยตัวเอง

## 1. ติดตั้ง Panel

เครื่อง Panel ต้องมี Git, Docker Engine และ Docker Compose plugin ติดตั้งตาม [Docker Ubuntu documentation](https://docs.docker.com/engine/install/ubuntu/) ก่อน ตัว Panel ใช้ Linux container และไม่ต้อง mount Docker socket

```bash
git clone https://github.com/JIRAYUTRPY/MineCManager.git
cd MineCManager
docker compose up -d --build
docker compose logs --tail=50 panel
docker compose exec panel cat /data/admin-key.txt
```

เปิด **http://127.0.0.1:9080/fleet** แล้วใส่ key จากคำสั่งสุดท้าย Key มีสิทธิ์ root บน VM ที่ลงทะเบียนไว้ ห้ามแชร์กับผู้เล่น

ถ้า Panel อยู่บน VPS ให้รันคำสั่งนี้บนคอมพิวเตอร์ของคุณ แล้วเปิด URL เดิม:

```bash
ssh -N -L 9080:127.0.0.1:9080 YOUR_USER@PANEL_IP
```

Compose เปิด HTTP เฉพาะ loopback โดยตั้งใจ หากต้องการเข้าผ่าน domain ให้วาง Nginx HTTPS บน host หน้า `127.0.0.1:9080` ดู [คู่มือเดิมส่วน Nginx/TLS](install-th.md) และเปิด `/fleet` ไม่ต้องเปิด port 9080 สู่ Internet การสร้าง DNS ผ่าน Panel ไม่ได้ติดตั้ง certificate ให้

ครั้งแรกระบบสร้าง config, admin key, vault key และ audit ใน named volume `panel-data` ครั้งถัดไปใช้ชุดเดิม **อย่าลบ volume หรือสร้าง vault key ใหม่** หากทำ key หายจะถอดรหัส credentials เดิมไม่ได้

## 2. เตรียม VM และเพิ่มเครื่อง

รองรับการติดตั้ง Minecraft อัตโนมัติบน **Ubuntu 24.04 / 26.04 ที่มี systemd, Python 3.12+ และ SSH** Java 17/21/25 ต้องมีใน apt repository ของ Ubuntu รุ่นนั้น หากเวอร์ชันที่เลือกต้องใช้ Java ที่ repository ไม่มี งานจะล้มเหลวโดยไม่เลือก Java รุ่นอื่นให้เอง

ข้อมูลที่ต้องมี:

| ช่อง | ใส่อะไร |
|---|---|
| ID | เช่น `game-01` อักษรเล็ก ตัวเลข ขีดกลาง เริ่มด้วยตัวอักษร ไม่เกิน 32 ตัว |
| Public / Private IP | ข้อมูลของเครื่อง จะมีทั้งสองหรือเฉพาะที่ใช้ก็ได้ |
| IP ที่ Panel ใช้เชื่อมต่อ | IP ที่ container ของ Panel ติดต่อได้จริง ไม่เลือกเส้นทางให้อัตโนมัติ |
| SSH port / user | ปกติ 22 และ root หรือ user ที่มี sudo |
| Password / Private key | อย่างใดอย่างหนึ่ง; private key ต้องไม่มี passphrase |
| Host fingerprint | SHA-256 ของ SSH host key ที่ตรวจสอบกับ VM console แล้ว |

1. เปิด **เครื่อง VM → เพิ่ม / เปลี่ยนข้อมูล VM**
2. กรอก IP และ port แล้วกด **อ่าน SSH fingerprint** การอ่านนี้ยังไม่ส่ง password และยังไม่ถือว่าเชื่อถือเครื่อง
3. เทียบกับผล `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub -E sha256` จาก provider console (ถ้า SSH เลือก key algorithm อื่น ให้เทียบ public key ของ algorithm นั้น)
4. ใส่ password หรือ private key ติ๊กยืนยัน fingerprint แล้วกด **ทดสอบการเชื่อมต่อ** เพื่อดู CPU, RAM, disk ของ `/`, uptime และ OS ข้อมูลเครื่องจะยังอยู่เฉพาะในฟอร์ม
5. กด **ตรวจสอบและบันทึก VM** ได้ทันทีเมื่อพร้อม Panel ตรวจ fingerprint, credentials และสิทธิ์อ่าน metrics ผ่าน root/sudo อีกครั้ง เมื่อผ่านจึงเก็บข้อมูลแบบ AES-256-GCM และแสดงทรัพยากรของเครื่อง

หากเปลี่ยน credentials แล้วทดสอบไม่ผ่าน ข้อมูลเดิมยังใช้ได้ การแก้ IP, port หรือ fingerprint ในฟอร์มต้องยืนยัน fingerprint ใหม่ หากมีงานค้างสำหรับ VM นั้น ให้รอจบก่อนเปลี่ยนปลายทาง SSH

User ที่ใช้ key อย่างเดียวต้องมี `sudo -n` ได้ หากใช้ password จะใช้ password เดียวกันกับ sudo ไม่รองรับ MFA, keyboard-interactive หรือ sudo password คนละชุด ต้องเปิด SSH จากเครื่อง Panel และมีเส้นทาง private network/VPN เองหากเลือก private IP

ถ้า host key เปลี่ยน ระบบจะไม่เชื่อมต่อ ตรวจสอบผ่าน provider console แล้วใช้ปุ่ม **เปลี่ยน credentials / fingerprint** ไม่มี auto-accept host key

**รุ่นนี้ใช้ SSH เป็น control transport โดยตรง** ส่ง control program ที่มากับ Panel ทุกครั้ง ไม่มี Agent daemon ที่ต้องติดตั้งบน VM และไม่มี API port เพิ่มบน VM ส่วน Agent API เดิมยังใช้ได้ที่หน้า `/` แต่ server ที่สร้างจากหน้า Fleet ไม่ได้ถูกลงทะเบียนใน Agent/Paper bridge อัตโนมัติ

## 3. ติดตั้ง Minecraft

เลือก VM แล้วกรอก Server ID, platform, Minecraft release version, RAM MiB และ TCP port อ่านและยอมรับ [Minecraft EULA](https://www.minecraft.net/eula) แล้วกดติดตั้ง

- **Paper:** ใช้ stable build จาก Fill v3 และตรวจ SHA-256
- **Purpur:** resolve build ที่เจาะจงก่อนดาวน์โหลดและตรวจ MD5 ที่ upstream ให้ (ไม่ใช่ security hash แบบ SHA-256)
- **Fabric:** เลือก stable loader/installer จาก Fabric Meta; artifact ส่งผ่าน HTTPS ไม่มี upstream checksum ใน flow นี้ ระบบเก็บ SHA-256 ของไฟล์ที่ดาวน์โหลด
- **Vanilla:** release manifest ของ Mojang และตรวจ SHA-1 ตาม manifest
- Java major อ่านจาก Mojang release metadata รองรับ 17, 21, 25 ไม่มี auto-update server หรือเดาเวอร์ชันแทน release ที่ขอ

ระบบติดตั้ง Java, สร้าง Linux account `mcm-game`, เก็บเกมที่ `/srv/minecmanager/<server-id>` และสร้าง `mcm-<server-id>.service` รองรับหลาย server บน VM เดียว ตรวจ RAM รวมที่จองให้ server ของ MineCManager พร้อมเหลือ 512 MiB ให้ OS และตรวจ port/path ไม่ให้ซ้ำ

การตรวจ RAM เป็น reservation ของรายการที่ Panel จัดการ ไม่ครอบคลุม workload อื่นหรือ native memory ของ JVM เลือก headroom เพิ่มเองตาม plugins/mods ระบบไม่แก้ firewall/cloud security group ให้ เปิด TCP port ของเกมจากช่องทางที่เหมาะสมก่อนเล่น

ติดตั้งสำเร็จหมายถึงสร้าง service และส่ง start แล้ว **ยังต้องเปิด Logs ดูข้อความ `Done`** และทดสอบเข้าเกมจริง ปุ่ม status/start/stop/restart/logs ใช้ service name เช่น `mcm-survival.service` ถ้าติดตั้งล้มเหลว ไฟล์ที่สร้างแล้วจะเก็บไว้ให้ตรวจ ไม่ลบทิ้งหรือเขียนทับเมื่อ retry; ใช้ OS console ตรวจ/ย้าย partial directory และ service หลังสำรองก่อนลองใหม่

## 4. จัดการไฟล์และ OS

หน้า Files ใช้ absolute path ได้ทั้ง VM ภายใต้สิทธิ์ root แสดง directory และอ่าน/แก้ไฟล์ UTF-8 ได้ไม่เกิน 1 MiB ก่อนบันทึกต้องอ่านไฟล์ก่อนเพื่อรับ SHA-256 หากเนื้อหาเปลี่ยนแล้วจะปฏิเสธ การเขียนเก็บสำเนาเดิมใน `/var/lib/minecmanager/file-history/` รักษา owner/group/mode ของไฟล์เดิม และไม่เขียนผ่าน symlink

**OS command console** ใช้รัน Bash แบบ non-interactive โดยยืนยัน `ROOT` ทุกครั้ง รองรับงานที่ยังไม่มี form เช่น สร้าง/ย้าย/ลบ/ดาวน์โหลดไฟล์ขนาดใหญ่, permissions, packages, processes และ firewall ผล stdout จำกัด 1 MiB และคำสั่งจำกัด 120 วินาที ไม่ใช่ interactive PTY terminal; คำสั่งที่รอถาม password/input จะใช้ไม่ได้

ตัวอย่างตรวจสอบ OS:

```bash
df -h
free -m
systemctl --failed
ss -lntp
```

ก่อนแก้ config ระบบให้เก็บสำเนาและตรวจ syntax เอง เช่น `nginx -t` แล้วค่อย restart Service controls รองรับชื่อ `.service` อื่นนอกเหนือ Minecraft ได้ **การสั่ง firewall, reboot, network หรือ SSH service อาจตัดการเชื่อมต่อของ Panel** ต้องมี provider console สำหรับกู้ระบบ ไม่มีระบบป้องกัน self-lockout อัตโนมัติในรุ่นนี้

## 5. เชื่อม Cloudflare

### DNS

ใน Cloudflare สร้าง API token สิทธิ์ **Zone Read + DNS Edit** จำกัดเฉพาะ zone ที่ต้องใช้ จากหน้า Settings ใน Panel กรอก DNS token แล้วกดบันทึก จากนั้นกด **ดู Zones** เพื่อหา zone ID

สร้าง/อัปเดต A, AAAA, CNAME หรือ SRV ได้ Record ID ว่างจะสร้างใหม่; ถ้าต้องการอัปเดตให้ใช้ ID จากผลการสร้างครั้งก่อน

ตัวอย่าง Minecraft port 25570:

1. A: `mc.example.com` → public IPv4 ของ VM แบบ DNS only
2. SRV name: `_minecraft._tcp.play.example.com`
3. SRV data: `{"priority":0,"weight":0,"port":25570,"target":"mc.example.com"}`

ผู้เล่นเข้า `play.example.com` ได้ตาม DNS/SRV resolution ของ client เกมไม่ใช้ Cloudflare HTTP proxy ช่อง **Hostname นี้ใช้เว็บ HTTP/HTTPS** มีไว้สำหรับ Panel/API/web hostname เท่านั้น DNS ไม่ได้ provision TLS, Tunnel, Spectrum หรือเปลี่ยน firewall

### R2

สร้าง private bucket และ [R2 S3 credentials](https://developers.cloudflare.com/r2/api/tokens/) ที่มี Object Read & Write ใน bucket นั้น กรอก account ID, bucket, Access Key ID และ Secret Access Key ใน Panel ใช้ endpoint `https://<account-id>.r2.cloudflarestorage.com` region `auto` โดยอัตโนมัติ

DNS token กับ R2 credentials เป็นคนละชุด เว้นช่อง secret ว่างขณะบันทึกเพื่อคงค่าเดิม Secrets ไม่ถูกส่งกลับมาทาง GET; เปลี่ยน secret ได้โดยใส่ค่าใหม่ ห้ามตั้ง bucket เป็น public

## 6. Backup, schedule และ restore

เลือก VM และ Server ID ก่อน:

- **สำรองตอนนี้:** หยุด server ถ้าเปิดอยู่ → archive โลก/config/plugins/mods ทั้ง server directory → เริ่ม server กลับ → อัปโหลด R2 ถ้าเลือกไว้
- หาก backup ล้มเหลวหลังหยุด server จะพยายามเริ่มกลับใน `finally` แต่เมื่อ VM/network/process หยุดกะทันหัน ยังต้องตรวจ service จริง
- Backup ปฏิเสธ symlink/device/special files; เก็บ archive และ `.sha256` ที่ `/var/lib/minecmanager/backups/<server-id>/`
- **Schedule:** กำหนด interval เป็นชั่วโมง, local retention 1–365 ชุด และ R2 on/off รอบแรกเริ่มหลัง interval นับจากบันทึก เวลาเก็บเป็น UTC ไม่มี cron/timezone editor
- Panel ต้องเปิดอยู่ ถ้าปิดไปหลายรอบ เมื่อกลับมาจะรันเพียงหนึ่งรอบที่ถึงกำหนด ไม่มีการไล่รันย้อนหลังทุกช่วง ถ้า host มี job ค้างจะข้ามรอบนั้น
- ถอน/หมดอายุ API key ของผู้สร้าง schedule จะทำให้ schedule หยุด งานที่อนุญาตและเริ่มไปแล้วอาจทำต่อจนจบ
- Local retention ลบเฉพาะ backup ที่เกินจำนวนหลังสร้าง/อัปโหลดสำเร็จ **R2 retention ใช้ bucket lifecycle** ตั้ง rule สำหรับ prefix `minecmanager/` ใน Cloudflare เอง [R2 lifecycle docs](https://developers.cloudflare.com/r2/buckets/object-lifecycles/)
- R2 upload เป็น single PUT URL หมดอายุ 1 ชั่วโมง ไม่มี multipart/resume; เหมาะกับ archive ที่ถ่ายโอนได้ภายในเวลานี้และไม่เกิน single PUT limit ของ R2

Restore local: หยุด server → เลือกชื่อ backup จากรายการ → ยืนยัน Server ID ระบบตรวจ SHA-256, ตรวจ archive paths, แตกใน staging และเก็บ root เดิมไว้ที่ `/srv/minecmanager/<id>.before-restore-<random>` หลัง restore **ไม่ start ให้อัตโนมัติ** ให้ตรวจไฟล์และ logs ก่อน start เอง

Restore R2: เลือก object key จากรายการ R2, ใส่ SHA-256 จากผล backup ที่เก็บไว้, หยุด server และกด **ดาวน์โหลดจาก R2 + กู้คืนบน VM** ระบบดาวน์โหลดเข้า VM, ตรวจ checksum แล้วใช้ restore flow เดียวกัน เก็บ checksum นอก Panel ด้วยเพราะประวัติ job จำกัด 1,000 รายการ

ถ้า process ตายระหว่างสอง rename ของ restore อาจเหลือ root เดิมอยู่ชื่อ `.before-restore-*` และยังไม่มี active root ตรวจ directory เหล่านี้ก่อน retry ไม่ลบ recovery directories อัตโนมัติ ต้องมีพื้นที่สำหรับ archive, staging และ root เดิม

## 7. งานเบื้องหลัง, สิทธิ์ และ audit

งานมีสถานะ `queued → running → succeeded/failed` Panel restart เปลี่ยนงาน `running` เป็น `interrupted` เพื่อไม่ส่งคำสั่งซ้ำโดยไม่ทราบผล ตรวจ VM ก่อน retry งาน queued ยังทำต่อได้หลัง restart และตรวจสิทธิ์ผู้สร้างอีกครั้งก่อน dispatch

รุ่นนี้ใช้ worker เดียวต่อ Panel และ lock คำสั่งหนึ่งงานต่อ VM ไม่ใช่ distributed queue งาน deploy ที่นานทำให้งานอื่นรอได้ เก็บ job input/result ใน vault ที่เข้ารหัส ตัด input หลัง job เสร็จ หน้า jobs แสดง 100 รายการล่าสุดและเก็บประวัติสูงสุด 1,000 งาน คำสั่งลับและ file contents ไม่อยู่ใน audit; audit เก็บ actor, target host, job ID/action, intent/result และ status ส่วน stdout/file contents อยู่ใน encrypted job result

Fleet ทุก endpoint ต้องใช้ scope ใหม่ **`admin:fleet`** ซึ่งหมายถึง trusted infrastructure/root administrator รวม credentials, DNS และ backup ของทุก VM ไม่ใช่สิทธิ์ Minecraft ราย server ผู้ใช้ Agent เดิมไม่มี scope นี้โดยอัตโนมัติ สร้าง key จากหน้า `/` โดยใช้ admin key เดิมได้ ห้ามให้ผู้เล่นหรือผู้ดูแล plugin ที่ไม่ควรเข้าถึง OS

Vault/audit เขียนไม่ได้จะหยุดรับ/dispatch งานใหม่ ไม่ retry mutation อัตโนมัติ กระบวนการทำงานเดียวใช้ process lock ของ Panel เดิม ห้ามรันหลาย replica บน volume เดียว

## 8. อัปเกรด, สำรอง Panel และถอนการติดตั้ง

หยุด Panel ก่อนทำ snapshot/backup named volume ของ Docker เพื่อให้ config/vault/audit สอดคล้องกัน เก็บทั้ง `panel.json`, `fleet.vault`, `vault.key`, `audit.jsonl`, `admin-key.txt` แบบเข้ารหัสและจำกัดสิทธิ์ **vault key ต้องอยู่ในชุดกู้คืน** การมี vault และ key พร้อมกันเท่ากับเข้าถึง SSH/R2 credentials ได้

```bash
docker compose stop panel
# ทำ encrypted volume snapshot/backup ด้วยระบบสำรองที่คุณใช้
git pull --ff-only
docker compose up -d --build
docker compose logs --tail=50 panel
```

`docker compose down` หยุด/ลบ container แต่เก็บ named volume ไว้ **ห้ามใช้ `down -v` หากต้องการเก็บข้อมูล** การนำ VM ออกจาก Panel ไม่ uninstall Minecraft, ลบโลก หรือปิด service บน VM

## 9. Troubleshooting / ก่อนใช้งานจริง

| อาการ | ตรวจ |
|---|---|
| 403 admin:fleet required | สร้าง key ใหม่ที่มี scope นี้; Agent admin:nodes ไม่เพียงพอ |
| SSH network connection failed | selected IP, route จาก container, SSH port, security group/firewall |
| SSH authentication or negotiation failed | user, key/password และวิธี authentication ที่ sshd รองรับ |
| could not read VM metrics / remote operation failed | Python 3, sudo rights, OS, connectivity; stderr ไม่แสดงเพื่อป้องกัน secrets หลุด |
| SSH host fingerprint mismatch | เทียบกับ provider console ห้ามยอมรับเพียงเพราะ IP ตรง |
| apt-get failed | apt lock, repository/network และ package Java major ที่ต้องการ |
| งาน interrupted/failed แต่ไฟล์มีแล้ว | ตรวจ VM จริงก่อน retry ไม่ลบ partial installation โดยไม่สำรอง |
| R2 upload failed | bucket credential scope, VM outbound HTTPS, object size/timeout; local backup ยังอยู่ |
| vault authentication failed | ต้องใช้ vault.key เดิมกับ vault ชุดเดียวกัน ไม่ regenerate |
| audit unavailable | พื้นที่/permissions/JSONL corruption แก้แล้ว restart; อย่าข้าม audit |

ก่อน production ให้ทดสอบ VM disposable: เพิ่ม VM, deploy platform/version ที่จะใช้จริง, เข้าเกม, restart, แก้ไฟล์, backup/local restore, R2 backup/restore, schedule, revoke key และ restart Panel ระหว่าง job CI ทดสอบจากหน้าเว็บถึง OpenSSH และ systemd บน Ubuntu 24.04 ของ GitHub โดยใช้ service ทดสอบและไฟล์ตัวอย่างสำหรับ backup/restore ส่วนการเข้าเกมและการกู้ข้อมูลจาก R2 ของคุณต้องทดสอบกับ server, บัญชี Cloudflare และ bucket ที่จะใช้จริง
