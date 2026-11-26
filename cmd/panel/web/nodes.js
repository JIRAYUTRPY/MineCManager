'use strict';
const { $, value: val, bind, run, show } = Panel;
let configHash = '',
  loadedPath = '';
async function api(path, method, body, headers) {
  const data = await Panel.api('/v1/' + path, method, body, headers);
  show(data);
  return data;
}
const nodePath = () => {
  if (!val('node')) throw Error('เลือก node ก่อน');
  return 'nodes/' + encodeURIComponent(val('node')) + '/';
};

document.querySelectorAll('[data-action]').forEach(
  (b) =>
    (b.onclick = run(async () => {
      const a = b.dataset.action;
      if (a === 'status') return api(nodePath() + 'status');
      if (confirm('ยืนยัน ' + a + ' บน ' + val('node') + '?'))
        return api(nodePath() + 'actions', 'POST', { action: a });
    }))
);
bind('plugins', () => api(nodePath() + 'plugins'));
bind('pluginInfo', async () => {
  const v = await api(nodePath() + 'plugins/' + encodeURIComponent(val('plugin')));
  $('pluginHash').value = v.plugin ? v.plugin.sha256 : '';
});
bind('upload', async () => {
  const f = $('jar').files[0];
  if (!f) throw Error('เลือกไฟล์ JAR');
  if (!confirm('ยืนยันติดตั้ง plugin?')) return;
  return api(nodePath() + 'plugins/' + encodeURIComponent(val('plugin')), 'PUT', f, {
    'Content-Type': 'application/java-archive',
    ...(val('pluginHash') ? { 'If-Match': '"' + val('pluginHash') + '"' } : {})
  });
});
bind('rollbackPlugin', () => {
  if (confirm('คืนค่า plugin?'))
    return api(
      nodePath() + 'plugins/' + encodeURIComponent(val('plugin')),
      'POST',
      { version: val('pluginVersion') },
      val('pluginHash') ? { 'If-Match': '"' + val('pluginHash') + '"' } : { 'If-None-Match': '*' }
    );
});
bind('deletePlugin', () => {
  if (confirm('ลบ plugin และเก็บเวอร์ชันสำหรับกู้คืน?'))
    return api(nodePath() + 'plugins/' + encodeURIComponent(val('plugin')), 'DELETE', undefined, {
      'If-Match': '"' + val('pluginHash') + '"'
    });
});
bind('readConfig', async () => {
  const path = nodePath() + 'config?path=' + encodeURIComponent(val('configPath'));
  const v = await api(path);
  $('configContent').value = v.content;
  configHash = v.sha256;
  loadedPath = path;
});
const configTarget = () => {
  const path = nodePath() + 'config?path=' + encodeURIComponent(val('configPath'));
  if (!configHash || loadedPath !== path) throw Error('อ่าน config ของ node/path นี้ก่อน');
  return path;
};
bind('saveConfig', async () => {
  const target = configTarget();
  if (!confirm('บันทึก config?')) return;
  const v = await api(target, 'PUT', $('configContent').value, {
    'Content-Type': 'text/plain',
    'If-Match': '"' + configHash + '"'
  });
  configHash = v.sha256;
});
bind('rollbackConfig', async () => {
  const target = configTarget();
  if (!confirm('คืนค่า config?')) return;
  await api(target, 'POST', { version: val('configVersion') }, { 'If-Match': '"' + configHash + '"' });
  configHash = '';
});
bind('backups', () => api(nodePath() + 'backups'));
bind('backup', () => {
  if (confirm('สร้าง backup ขณะเซิร์ฟเวอร์หยุด?')) return api(nodePath() + 'backups', 'POST', {});
});
bind('restore', () => {
  const id = val('backupID');
  if (prompt('พิมพ์ Backup ID เพื่อยืนยันการกู้คืน') !== id || !id) return;
  return api(nodePath() + 'backups/' + encodeURIComponent(id) + '/restore', 'POST', { confirm: id });
});
let inventoryData = null,
  inventoryTarget = '';
bind('players', () => api(nodePath() + 'paper/players'));
bind('resources', () => api(nodePath() + 'paper/resources'));
bind('inventory', async () => {
  inventoryTarget = nodePath() + 'paper/players/' + encodeURIComponent(val('playerUUID')) + '/inventory';
  inventoryData = await api(inventoryTarget);
});
bind('writeItem', async () => {
  const target = nodePath() + 'paper/players/' + encodeURIComponent(val('playerUUID')) + '/inventory';
  if (!inventoryData || inventoryTarget !== target) throw Error('อ่าน inventory ของผู้เล่นนี้ก่อน');
  const slot = Number(val('slot')),
    item = inventoryData.slots.find((i) => i.slot === slot);
  if (!item) throw Error('Slot ไม่ถูกต้อง');
  if (!confirm('แทนที่ item ใน slot ' + slot + '?')) return;
  await api(
    target + '/' + slot,
    'PUT',
    {
      material: val('material'),
      amount: Number(val('amount')),
      ...(val('itemName') ? { display_name: val('itemName') } : {})
    },
    { 'If-Match': '"' + item.sha256 + '"' }
  );
  inventoryData = null;
});
bind('keys', () => api('keys'));
bind('audit', () => api('audit?limit=100'));
const list = (id) =>
  val(id)
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
bind('createKey', () =>
  api('keys', 'POST', {
    id: val('keyID'),
    scopes: list('scopes'),
    nodes: list('keyNodes'),
    ...(val('expiry') ? { expires_at: val('expiry') } : {})
  })
);
bind('revoke', () => {
  if (confirm('ยกเลิก key ' + val('revokeID') + '?'))
    return api('keys/' + encodeURIComponent(val('revokeID')), 'DELETE');
});
bind('register', () =>
  api('nodes', 'POST', { id: val('nodeID'), url: val('nodeURL'), token_env: val('tokenEnv') })
);
bind('removeNode', () => {
  if (confirm('นำ node ออกจากทะเบียน?'))
    return api('registrations/' + encodeURIComponent(val('removeNodeID')), 'DELETE');
});
run(async () => {
  const node = $('node');
  if (node?.tagName === 'SELECT') {
    const nodes = await Panel.api('/v1/nodes');
    node.replaceChildren(...nodes.map((n) => new Option(n.id, n.id)));
    Panel.restore('node');
  }
  if ($('audit')) await api('audit?limit=100');
  if ($('keys')) await api('keys');
})();
