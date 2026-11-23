'use strict';
(() => {
  const { $, value, api, run, message, restore, remember } = Panel;
  const base = '/v1/manage/';
  let rows = [],
    roles = [],
    editing = '',
    editingRole = '';
  function options(el, names, first) {
    const list = names.map((n) => new Option(n, n));
    if (first) list.unshift(new Option(first, ''));
    el.replaceChildren(...list);
  }
  function renderUsers() {
    const filter = value('roleFilter');
    $('userList').replaceChildren(
      ...rows
        .filter((u) => !filter || u.role === filter)
        .map((u) => {
          const tr = document.createElement('tr');
          for (const text of [u.username, u.role, u.enabled ? 'เปิดใช้งาน' : 'ปิดใช้งาน']) {
            const td = document.createElement('td');
            td.textContent = text;
            tr.append(td);
          }
          const cell = document.createElement('td');
          if (u.role === 'admin') cell.textContent = 'ผู้ดูแลคนแรก';
          else {
            const button = document.createElement('button');
            button.textContent = 'แก้ไข';
            button.addEventListener('click', () => {
              editing = u.username;
              $('editingUser').textContent = editing;
              $('editUserCard').hidden = false;
              $('editRole').value = u.role;
              $('enabled').checked = u.enabled;
              $('resetPassword').value = '';
            });
            cell.append(button);
          }
          tr.append(cell);
          return tr;
        })
    );
  }
  async function users() {
    [rows, { roles }] = await Promise.all([api(base + 'users'), api(base + 'roles')]);
    const assignable = roles.map((r) => r.name).filter((n) => n !== 'admin');
    options($('newRole'), assignable);
    $('newRole').value = 'viewer';
    options($('editRole'), assignable);
    options(
      $('roleFilter'),
      roles.map((r) => r.name),
      'ทั้งหมด'
    );
    restore('roleFilter');
    renderUsers();
  }
  function showRole() {
    const r = roles.find((item) => item.name === value('selectedRole'));
    $('roleInfo').textContent = r ? (r.builtin ? 'บทบาทของระบบ' : 'บทบาทที่ผู้ดูแลกำหนด') : '';
    $('roleGrants').textContent = r?.scopes.join(', ') || '';
    editingRole = r && !r.builtin ? r.name : '';
    $('roleFormTitle').textContent = editingRole ? 'แก้ไขบทบาท' : 'สร้างบทบาท';
    $('roleName').value = editingRole;
    $('roleName').readOnly = !!editingRole;
    for (const box of $('grants').querySelectorAll('input'))
      box.checked = !!editingRole && r.scopes.includes(box.value);
  }
  async function loadRoles(select) {
    const data = await api(base + 'roles');
    roles = data.roles;
    options(
      $('selectedRole'),
      roles.map((r) => r.name),
      'เลือกบทบาท'
    );
    if (select) {
      $('selectedRole').value = select;
      await remember('selectedRole');
    } else restore('selectedRole');
    $('grants').replaceChildren(
      ...data.assignable.map((scope) => {
        const label = document.createElement('label'),
          box = document.createElement('input');
        box.type = 'checkbox';
        box.value = scope;
        label.append(box, ' ' + scope);
        return label;
      })
    );
    showRole();
  }
  $('roleFilter')?.addEventListener('change', renderUsers);
  $('createUser')?.addEventListener(
    'submit',
    run(async () => {
      await api(base + 'users', 'POST', {
        username: value('newUser'),
        role: value('newRole'),
        password: $('initialPassword').value
      });
      $('createUser').reset();
      await users();
      message('สร้างผู้ใช้แล้ว');
    })
  );
  $('editUser')?.addEventListener(
    'submit',
    run(async () => {
      const change = { role: value('editRole'), enabled: $('enabled').checked };
      if ($('resetPassword').value) change.password = $('resetPassword').value;
      await api(base + 'users/' + encodeURIComponent(editing), 'PATCH', change);
      $('resetPassword').value = '';
      $('editUserCard').hidden = true;
      await users();
      message('บันทึกแล้ว ผู้ใช้ต้องเข้าสู่ระบบใหม่');
    })
  );
  $('selectedRole')?.addEventListener('change', showRole);
  $('clearRole')?.addEventListener(
    'click',
    run(async () => {
      $('selectedRole').value = '';
      showRole();
      await remember('selectedRole');
      $('roleName').focus();
    })
  );
  $('roleForm')?.addEventListener(
    'submit',
    run(async () => {
      const name = value('roleName'),
        scopes = Array.from($('grants').querySelectorAll(':checked'), (box) => box.value);
      await api(
        base + 'roles' + (editingRole ? '/' + encodeURIComponent(editingRole) : ''),
        editingRole ? 'PUT' : 'POST',
        { name, scopes }
      );
      await loadRoles(name);
      message('บันทึกบทบาทแล้ว');
    })
  );
  run(() => ($('userList') ? users() : loadRoles()))();
})();
