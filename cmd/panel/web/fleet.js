'use strict';
const { $, value: v, bind, run, show } = Panel;
const api = (path, method, body) => Panel.api('/v1/fleet/' + path, method, body);
let fileHash = '',
  fileTarget = '';
async function hosts() {
  if (!$('host')) return;
  const rows = await api('hosts');
  $('host').replaceChildren(
    ...rows.map((h) => {
      const o = document.createElement('option');
      o.value = h.id;
      o.textContent = h.id + ' · ' + h.address;
      return o;
    })
  );
  Panel.restore('host');
}
async function jobs() {
  if (!$('jobRows')) return;
  const all = await api('jobs');
  const rows = all.filter((j) => !v('jobStatus') || j.status === v('jobStatus'));
  $('jobRows').replaceChildren(
    ...rows.map((j) => {
      const tr = document.createElement('tr');
      for (const value of [
        (j.host || 'Cloudflare') + ' / ' + j.action,
        j.status,
        new Date(j.created).toLocaleString()
      ]) {
        const td = document.createElement('td');
        td.textContent = value;
        tr.append(td);
      }
      const td = document.createElement('td'),
        b = document.createElement('button');
      b.textContent = 'ดู';
      b.onclick = () => show(j);
      td.append(b);
      tr.append(td);
      return tr;
    })
  );
}
async function job(action, input = {}, wait = false) {
  const cloud = ['dns', 'r2-list', 'r2-download'].includes(action);
  if (!cloud && !v('host')) throw Error('เลือก VM ก่อน');
  const j = await api('jobs', 'POST', { host: cloud ? '' : v('host'), action, input });
  show(j);
  await jobs();
  if (!wait) return j;
  for (let i = 0; i < 90; i++) {
    await new Promise((r) => setTimeout(r, 1000));
    const s = await api('jobs/' + j.id);
    if (s.status === 'succeeded') {
      show(s.result);
      await jobs();
      return s.result;
    }
    if (['failed', 'interrupted'].includes(s.status)) throw Error(s.error);
  }
  throw Error('งานยังทำอยู่ ดูสถานะในรายการงาน');
}
let shownRelease = null,
  updateTimer,
  releaseSequence = 0;
function clearRelease() {
  ++releaseSequence;
  clearTimeout(updateTimer);
  shownRelease = null;
  $('applyRelease').disabled = true;
}
function updatePath() {
  const id = v('updateTarget');
  return 'updates' + (id ? '?node=' + encodeURIComponent(id) : '');
}
async function refreshRelease(data, reportError = false) {
  const path = updatePath(),
    seq = ++releaseSequence;
  const waiting = shownRelease && ['queued', 'applying'].includes(shownRelease.phase);
  clearTimeout(updateTimer);
  try {
    const s = await api(path, data ? 'POST' : 'GET', data);
    if (seq !== releaseSequence || path !== updatePath()) return;
    shownRelease = s;
    const labels = {
      installed: 'ติดตั้งแล้ว',
      queued: 'รอดำเนินการ',
      applying: 'กำลังอัปเดต',
      succeeded: 'อัปเดตสำเร็จ',
      failed: 'อัปเดตไม่สำเร็จ',
      rollback_failed: 'ต้องตรวจการกู้คืน'
    };
    const busy = ['queued', 'applying'].includes(s.phase);
    $('releaseInfo').textContent = [
      'ติดตั้ง ' + s.installed,
      'GitHub Latest ' + (s.latest || (s.check_error ? 'ตรวจไม่สำเร็จ' : 'ยังไม่มี stable release')),
      s.checked ? 'ตรวจเมื่อ ' + new Date(s.checked).toLocaleString('th-TH') : '',
      labels[s.phase] || s.phase,
      s.check_error || s.message
    ]
      .filter(Boolean)
      .join(' · ');
    $('applyRelease').disabled = !s.available || busy || s.phase === 'rollback_failed';
    if (busy) updateTimer = setTimeout(() => refreshRelease(), 3000);
  } catch (e) {
    if (seq !== releaseSequence) return;
    $('releaseInfo').textContent = e.message;
    $('applyRelease').disabled = true;
    if (waiting) updateTimer = setTimeout(() => refreshRelease(), 5000);
    if (reportError) throw e;
  }
}
async function loadReleaseTargets() {
  clearRelease();
  const seq = releaseSequence;
  try {
    const nodes = await api('update-targets');
    if (seq !== releaseSequence) return;
    $('updateTarget').replaceChildren(
      new Option('Panel', ''),
      ...nodes.map((id) => new Option('เครื่องเกม · ' + id, id))
    );
    Panel.restore('updateTarget');
    await refreshRelease();
  } catch (e) {
    if (seq === releaseSequence) $('releaseInfo').textContent = e.message;
  }
}
$('updateTarget')?.addEventListener('change', () => {
  clearRelease();
  refreshRelease();
});
bind('checkRelease', () => refreshRelease({ action: 'check' }, true));
bind('releaseStatus', () => refreshRelease(undefined, true));
bind('applyRelease', async () => {
  const target = shownRelease && shownRelease.latest;
  if (!target || $('applyRelease').disabled) return;
  const name = v('updateTarget');
  const effect = name
    ? 'เกมที่เปิดอยู่จะรีสตาร์ต'
    : 'Panel จะรีสตาร์ตและอาจขัดจังหวะงาน Fleet ที่กำลังทำอยู่';
  if (!confirm('อัปเดต ' + (name || 'Panel') + ' เป็น ' + target + '? ' + effect)) return;
  $('applyRelease').disabled = true;
  await refreshRelease({ action: 'apply', version: target }, true);
});

for (const field of ['address', 'port', 'fingerprint']) {
  $(field)?.addEventListener('input', () => {
    $('trust').checked = false;
  });
}
bind('discover', async () => {
  const address = v('address'),
    port = Number(v('port'));
  $('trust').checked = false;
  const result = await api('discover', 'POST', { address, port });
  if (address !== v('address') || port !== Number(v('port'))) return;
  $('fingerprint').value = result.fingerprint;
  $('trust').checked = false;
  show(result.fingerprint);
});

function hostInput() {
  if (!$('trust').checked) throw Error('ตรวจสอบ SSH fingerprint กับ VM console ก่อน');
  return {
    id: v('hostID'),
    public_ip: v('publicIP'),
    private_ip: v('privateIP'),
    address: v('address'),
    port: Number(v('port')),
    user: v('user'),
    fingerprint: v('fingerprint'),
    password: $('password').value,
    private_key: $('privateKey').value
  };
}
bind('testHost', async () => {
  const input = hostInput();
  const result = await api('hosts/test', 'POST', input);
  show({ vm: result.id, address: result.address, metrics: result.metrics });
});
async function saveHost(method) {
  const input = hostInput();
  const result = await api('hosts', method, input);
  $('password').value = '';
  $('privateKey').value = '';
  await hosts();
  $('host').value = result.id;
  await Panel.remember('host');
  renderMetrics(result.metrics);
  show({ vm: result.id, address: result.address, metrics: result.metrics });
}
bind('addHost', () => saveHost('POST'));
bind('replaceHost', () => {
  if (confirm('เปลี่ยนข้อมูลการเชื่อมต่อของ ' + v('hostID') + '?')) return saveHost('PUT');
});
bind('deleteHost', async () => {
  if (confirm('นำ ' + v('host') + ' ออกจาก Panel?')) {
    await api('hosts/' + v('host'), 'DELETE');
    await hosts();
    await Panel.remember('host');
    $('metricsView').replaceChildren();
  }
});
$('host')?.addEventListener('change', () => $('metricsView')?.replaceChildren());
function renderMetrics(m) {
  const usedGiB = (total, available) =>
    ((total - available) / 2 ** 30).toFixed(1) + ' / ' + (total / 2 ** 30).toFixed(1) + ' GiB';
  const rows = [
    ['CPU', m.cpu_count + ' cores · ' + m.cpu_percent + '%'],
    ['RAM', usedGiB(m.ram_total, m.ram_available)],
    ['Disk', usedGiB(m.disk_total, m.disk_free)],
    ['Uptime', (m.uptime_seconds / 3600).toFixed(1) + ' h']
  ];
  $('metricsView').replaceChildren(
    ...rows.map(([title, value]) => {
      const card = document.createElement('div');
      card.className = 'badge';
      card.textContent = title + ': ' + value;
      return card;
    })
  );
}
async function displayMetrics() {
  const id = v('host');
  const result = await job('metrics', {}, true);
  if (id === v('host')) renderMetrics(result);
}
bind('metrics', displayMetrics);
bind('hostServers', () => job('servers', {}, true));
bind('deploy', () => {
  if (!$('eula').checked) throw Error('ต้องยอมรับ EULA');
  if (confirm('ติดตั้ง Minecraft บน ' + v('host') + '?'))
    return job('deploy', {
      server: v('serverID'),
      platform: v('platform'),
      version: v('version'),
      ram_mb: Number(v('ram')),
      port: Number(v('gamePort')),
      eula: true
    });
});
document.querySelectorAll('[data-verb]').forEach(
  (b) =>
    (b.onclick = run(() => {
      const verb = b.dataset.verb;
      if (!['logs', 'status'].includes(verb) && !confirm(verb + ' ' + v('service') + '?')) return;
      return job('service', { service: v('service'), verb, confirm: v('service') }, true);
    }))
);
bind('listFiles', () => job('files', { path: v('path') }, true));
bind('readFile', async () => {
  const target = v('host') + ':' + v('path');
  const x = await job('read', { path: v('path') }, true);
  $('fileContent').value = new TextDecoder('utf-8', { fatal: true }).decode(
    Uint8Array.from(atob(x.content), (c) => c.charCodeAt(0))
  );
  fileHash = x.sha256;
  fileTarget = target;
});
bind('saveFile', async () => {
  if (!fileHash || fileTarget !== v('host') + ':' + v('path')) throw Error('อ่านไฟล์นี้ก่อน');
  if (!confirm('บันทึก ' + v('path') + '?')) return;
  let str = '';
  for (const b of new TextEncoder().encode($('fileContent').value)) str += String.fromCharCode(b);
  const x = await job(
    'write',
    { path: v('path'), confirm: v('path'), sha256: fileHash, content: btoa(str) },
    true
  );
  fileHash = x.sha256;
});
bind('runCommand', () => {
  if (prompt('คำสั่งนี้รันระดับ root บน ' + v('host') + ' พิมพ์ ROOT เพื่อยืนยัน') === 'ROOT')
    return job('command', { command: $('command').value, confirm: 'ROOT' }, true);
});
bind('saveCloud', async () => {
  await api('cloud', 'PUT', {
    token: v('cfToken'),
    account: v('account'),
    bucket: v('bucket'),
    access_key: v('accessKey'),
    secret_key: v('secretKey')
  });
  for (const id of ['cfToken', 'accessKey', 'secretKey']) $(id).value = '';
  show(await api('cloud'));
});
bind('cloudStatus', async () => show(await api('cloud')));
bind('zones', async () => show(await api('zones')));
bind('saveDNS', () => {
  if (!confirm('เปลี่ยน Cloudflare DNS ของ ' + v('dnsName') + '?')) return;
  return job(
    'dns',
    {
      zone: v('zone'),
      name: v('dnsName'),
      type: v('dnsType'),
      content: v('dnsContent'),
      record_id: v('recordID'),
      web: $('webProxy').checked,
      ...(v('dnsType') === 'SRV' ? { data: JSON.parse($('srv').value) } : {})
    },
    true
  );
});
bind('backupNow', () => {
  if (confirm('หยุด server ชั่วคราวเพื่อสำรอง แล้วเริ่มกลับ?'))
    return job('backup', { server: v('serverID'), retain: Number(v('retain')), r2: $('r2').checked });
});
bind('saveSchedule', async () => {
  if (confirm('เปิด schedule ที่หยุด server ชั่วคราวตามเวลา?'))
    show(
      await api('schedules', 'PUT', {
        id: v('scheduleID'),
        host: v('host'),
        server: v('serverID'),
        every_hours: Number(v('hours')),
        retain: Number(v('retain')),
        r2: $('r2').checked,
        enabled: true
      })
    );
});
bind('schedules', async () => show(await api('schedules')));
bind('removeSchedule', async () => {
  if (confirm('ลบ schedule นี้?')) show(await api('schedules/' + v('scheduleID'), 'DELETE'));
});
bind('backups', () => job('backups', { server: v('serverID') }, true));
bind('restore', () => {
  const sid = v('serverID');
  if (prompt('พิมพ์ Server ID เพื่อยืนยัน restore (ต้องหยุด server ก่อน)') === sid)
    return job('restore', { server: sid, backup: v('backupName'), confirm: sid });
});
bind('r2List', () => job('r2-list', {}, true));
bind('r2Download', () => job('r2-download', { key: v('r2Key') }, true));
bind('r2Restore', () => {
  const sid = v('serverID');
  if (prompt('พิมพ์ Server ID เพื่อยืนยัน restore จาก R2') === sid)
    return job('r2-restore', { server: sid, key: v('r2Key'), sha256: v('r2SHA'), confirm: sid });
});
bind('refreshJobs', jobs);
$('jobStatus')?.addEventListener('change', run(jobs));
run(async () => {
  await hosts();
  if ($('jobRows')) {
    await jobs();
    setInterval(() => jobs().catch(() => {}), 5000);
  }
  if ($('updateTarget')) await loadReleaseTargets();
})();
