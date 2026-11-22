# ติดตั้งและอัปเดต MineCManager จาก GitHub Releases

ใช้ `setup.py` ไฟล์เดียวสำหรับเครื่อง Panel และเครื่องเกมแยก VM สคริปต์ทำงานบน Ubuntu 24.04/26.04 ที่มี systemd, Python 3.11 ขึ้นไป, OpenSSL และ OpenSSH รองรับ Linux amd64 และ arm64 ใช้แพ็กเกจที่ build จาก GitHub Releases ของ `JIRAYUTRPY/MineCManager`

## 1. ติดตั้งบน Panel VM

หลังมี stable release แรก ดาวน์โหลด installer จากหน้า Releases ตัวอย่างด้านล่างตรวจ digest ของ installer จาก GitHub ก่อนเรียก sudo รันจากโฟลเดอร์ที่บัญชีคุณเป็นเจ้าของ

```bash
set -e
api=https://api.github.com/repos/JIRAYUTRPY/MineCManager/releases/latest
read -r url digest < <(curl -fsSL "$api" | python3 -c '
import json, sys
r = json.load(sys.stdin)
a, = [a for a in r["assets"] if a["name"] == "setup.py"]
assert not r["draft"] and not r["prerelease"] and a["digest"].startswith("sha256:")
print(a["browser_download_url"], a["digest"][7:])')
curl -fL "$url" -o setup.py
printf '%s  setup.py\n' "$digest" | sha256sum -c -
sudo python3 setup.py panel
```

Panel ฟังที่ `127.0.0.1:9080` อ่าน bootstrap key บนเครื่องด้วย `sudo cat /var/lib/mcm-managed-panel/admin-key.txt` เปิดผ่าน reverse proxy HTTPS เดิม หรือใช้ SSH tunnel จากคอมที่เปิด browser

```bash
ssh -L 9080:127.0.0.1:9080 ops@panel-vm
```

เปิด `http://127.0.0.1:9080/fleet` เพิ่ม game VM ผ่าน SSH แล้วติดตั้ง **Paper 1.21.11** ด้วย Server ID ที่ต้องการ เช่น `survival` หากมี Paper รุ่นนี้ที่สร้างจาก Fleet อยู่แล้วให้ใช้ ID เดิม ขั้นตอนติดตั้งเกมและ EULA อยู่ใน [คู่มือ Fleet](install-panel-th.md)

## 2. จับคู่ VM เกม

บน Panel VM ให้ยืนยัน SSH host key ของเครื่องเกมกับ provider console และเตรียม OpenSSH alias ของผู้ดูแลให้ root บนเครื่อง Panel ใช้งานได้ ตัวอย่างนี้ใช้ alias `game-vm` ที่ล็อกอินด้วย key และใช้ `sudo -n` ได้ กำหนด private IP ที่ Panel ติดต่อเครื่องเกมได้จริง อนุญาต TCP 9081 จาก Panel VM ถึง game VM ส่วน Paper bridge ใช้ loopback 9082

```bash
sudo python3 /opt/minecmanager/current/setup.py pair survival 10.20.0.15 --ssh game-vm
```

คำสั่งนี้สร้าง CA/client certificate บน Panel, ส่ง server certificate และ Agent token ผ่าน SSH, ติดตั้ง Agent กับ bridge JAR บนเครื่องเกม จากนั้นลงทะเบียน node `survival` ผ่าน Panel API ถ้า key เริ่มต้นถูก revoke แล้ว ระบุ `--admin-key-file /path/to/private-admin-key` ที่มี `admin:nodes` การจับคู่ครั้งแรกรีสตาร์ต Panel เพื่อโหลด node token และรีสตาร์ตเกมถ้าเดิมเปิดอยู่ วางแผนช่วงที่ไม่มี Fleet job ทำงาน

ถ้าใช้ SSH key คนละชุดกับเครื่อง Panel ให้รัน `pair survival 10.20.0.15` โดยละ `--ssh` แล้วส่งไฟล์ `pair.json` ที่แสดงและ `setup.py` ให้ผู้ดูแล VM เกมผ่านช่องทางส่วนตัว บน game VM รัน `sudo python3 setup.py game /root/pair.json` เก็บ enrollment bundle สิทธิ์ 0600 เพราะมี private key และ token การรันติดตั้งซ้ำด้วย role/ID เดิมจะคืนสถานะปัจจุบัน

เข้า `/` แล้วกด **เชื่อมต่อ / โหลด nodes → survival → ผู้เล่นออนไลน์** ด้วย key ที่มี `nodes:read` และ `players:read` ผล `[]` หมายถึงถึง Paper แล้วและขณะนั้นไม่มีใครออนไลน์ ตัว bridge ใช้พอร์ตตายตัว จึงจับคู่ Paper ได้หนึ่ง instance ต่อ game VM ผ่านวิธีนี้ ไฟล์โลกและ backup ของ Fleet ยังจัดการผ่าน `/fleet`

## 3. ตรวจและอัปเดต

เปิด **/fleet → อัปเดต** ด้วยสิทธิ์ `admin:fleet` เลือก Panel หรือชื่อ node แล้วกด **ตรวจ GitHub Releases** หน้าเว็บแสดงเวอร์ชันที่ติดตั้ง เวอร์ชัน Latest เวลาเช็กล่าสุด และผลของงาน ปุ่ม **อัปเดต** ใช้ได้เมื่อมีเวอร์ชันใหม่กว่า Timer บนแต่ละ VM ตรวจทุก 6 ชั่วโมงโดยมีเวลาสุ่มเพิ่มสูงสุด 15 นาที การติดตั้งเกิดขึ้นเมื่อผู้ดูแลกดปุ่ม

ตัวตรวจอ่าน `/releases/latest` ของ repo นี้เท่านั้น รับเฉพาะ published stable release ที่ tag มีรูปแบบ `vMAJOR.MINOR.PATCH` เมื่อไม่มี release จะแสดงว่าไม่มีรุ่นให้อัปเดต เมื่อ GitHub ใช้งานไม่ได้จะแสดงข้อผิดพลาด ก่อนติดตั้ง worker ตรวจว่า release ยังตรงกับรุ่นที่กด แล้วตรวจ GitHub asset digest และ `SHA256SUMS` ก่อนหยุด service

การอัปเดต Panel รีสตาร์ตเฉพาะ Panel การอัปเดต VM เกมใช้ lock เดียวกับ Fleet หากกำลัง backup หรือ restore ให้รอ job นั้นจบแล้วกดอัปเดตอีกครั้ง ตัวอัปเดตเปลี่ยน Agent กับ bridge พร้อมกัน เกมที่เปิดอยู่จะเริ่มกลับและตรวจ bridge หลังอัปเดต เกมที่ปิดอยู่จะคงสถานะปิด ขณะ service รีสตาร์ตหน้าเว็บอาจขาดการติดต่อชั่วคราว กด **โหลดสถานะ** เพื่อตรวจผลได้อีกครั้ง เมื่อ health check ไม่ผ่าน worker สลับกลับ binary/JAR รุ่นเดิมและบันทึกผล ถ้าไฟดับระหว่างสลับรุ่น worker จะกู้รุ่นก่อนหน้าเมื่อเครื่องเริ่มใหม่

| ตำแหน่ง | เนื้อหา |
|---|---|
| `/opt/minecmanager/releases/<tag>` และ `current` | โปรแกรม, bridge และ installer แต่ละรุ่น |
| `/etc/mcm-managed` | CA, TLS, node token และการจับคู่ ควรสำรองแบบเข้ารหัส |
| `/var/lib/mcm-managed-panel` | config, API keys, audit, Fleet vault และ vault key |
| `/var/lib/mcm-release` | สถานะการตรวจและงานอัปเดต |
| `/srv/minecmanager/<id>` | world, plugins และ config ของเกม |

ดู log ด้วย `sudo journalctl -u mcm-release-apply -u mcm-release-check` และอ่านสถานะด้วย `sudo python3 /opt/minecmanager/current/setup.py status` กรณี `rollback_failed` ให้แก้เหตุใน log สำรอง state และตรวจ unit/พื้นที่ดิสก์ แล้วใช้ `sudo python3 /opt/minecmanager/current/setup.py recover` เพื่อกู้ release ก่อนหน้าอีกครั้ง ใบรับรองเครื่องมีอายุ 825 วัน เก็บ CA และวางแผนต่ออายุผ่านกระบวนการจัดการ certificate ของระบบ

### เครื่องที่ติดตั้งด้วย Compose หรือ systemd เดิม

เลือก VM ใหม่สำหรับการติดตั้งแบบ managed หรือย้ายใน maintenance window ตัว installer ตรวจ service/port ที่มีเจ้าของอยู่และหยุดก่อนติดตั้ง สำหรับ Compose เดิม ให้หยุด Panel เดิม สำรอง named volume ทั้งชุดก่อนย้าย คัดลอก `panel.json`, `fleet.vault`, `vault.key`, `audit.jsonl` และ `admin-key.txt` จาก `/data` มาไว้ใน `/var/lib/mcm-managed-panel` และให้ user `mcm-panel` เป็นเจ้าของไฟล์ด้วยสิทธิ์ 0600 ถ้ามี node เดิม ให้ตั้ง env ของ token และ path ของ CA/client certificate ใน `/etc/mcm-managed/panel-local.env` (root:root, 0600) ก่อนเริ่ม service กรณีใช้หลาย CA ให้จัด trust bundle และ client certificate ที่ทุก Agent ยอมรับ มีเพียง instance เดียวที่ควรเขียนข้อมูลชุดนี้ การย้ายจาก agent/manual bridge เดิมให้สำรอง service, secret และ JAR เดิมก่อน เครื่องที่ใช้ Compose ต่อยังใช้ UI และจัดการ node แบบ managed ได้ ปุ่มอัปเดตตัว Panel เองต้องมี managed updater บน host

## 4. ออก release

หลัง merge ให้สร้าง tag จาก commit ที่ต้องการ release ตัวอย่างสำหรับรุ่น `v0.4.0`

```bash
git switch main
git pull --ff-only
git tag -a v0.4.0 -m 'MineCManager v0.4.0'
git push origin v0.4.0
```

workflow **Release** ทดสอบ สร้าง Panel/Agent สำหรับ amd64 และ arm64, build Paper bridge ด้วย Java 21 แล้วทดสอบ systemd install/update/rollback บน Ubuntu ก่อนอัปโหลด installer, binary, JAR และ checksum ทั้งหมดขึ้น draft และ publish เมื่อครบ PR จะตรวจชุดติดตั้งโดยไม่สร้าง GitHub Release
