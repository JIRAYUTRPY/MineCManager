'use strict';
window.Panel = (() => {
  const $ = (id) => document.getElementById(id);
  const value = (id) => $(id).value.trim();
  const page = document.body.dataset.page;
  const fields = (document.body.dataset.fields || '').split(/\s+/).filter(Boolean);
  const stored = Object.fromEntries(
    Array.from(document.querySelectorAll('#pageState [data-field]'), (el) => [el.dataset.field, el.value])
  );
  const message = (text, error = false) => {
    $('message').textContent = text;
    $('message').dataset.error = String(error);
  };
  const show = (data) => {
    if ($('output'))
      $('output').textContent = typeof data === 'string' ? data : JSON.stringify(data, null, 2);
  };
  const run = (fn) => async (event) => {
    event?.preventDefault();
    if (event) message('');
    try {
      await fn(event);
    } catch (err) {
      message(err.message, true);
    }
  };
  const bind = (id, fn) => $(id)?.addEventListener('click', run(fn));

  async function api(path, method = 'GET', body, headers = {}) {
    const options = {
      method,
      headers: { 'X-MCM-Request': '1', ...headers },
      credentials: 'same-origin',
      cache: 'no-store',
      redirect: 'error'
    };
    if (body !== undefined) {
      if (body instanceof Blob || typeof body === 'string') options.body = body;
      else {
        options.headers['Content-Type'] = 'application/json';
        options.body = JSON.stringify(body);
      }
    }
    if (path.startsWith('/v1/me/preferences/')) options.keepalive = true;
    const response = await fetch(path, options),
      raw = await response.text();
    if (!response.ok) {
      if (response.status === 401 && page !== 'login' && page !== 'setup')
        location.assign('/login?next=' + encodeURIComponent(location.pathname));
      if (path === '/v1/auth/login' && response.status === 403)
        throw Error('ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง หรือบัญชีถูกปิด');
      if (path === '/v1/auth/bootstrap' && response.status === 403)
        throw Error('Admin key ไม่ถูกต้องหรือหมดสิทธิ์');
      throw Error(response.status + ' · ' + raw.trim());
    }
    try {
      return JSON.parse(raw);
    } catch {
      return raw;
    }
  }

  function restore(id) {
    const el = $(id);
    if (!el || !Object.hasOwn(stored, id)) return;
    if (el.tagName === 'SELECT' && !Array.from(el.options).some((o) => o.value === stored[id])) return;
    el.value = stored[id];
  }
  let pending = Promise.resolve(),
    writes = 0;
  function remember(id) {
    if (!fields.includes(id) || !$(id)) return Promise.resolve();
    const selection = $(id).value;
    if (stored[id] === selection && !writes) return Promise.resolve();
    ++writes;
    const save = pending
      .then(() => api('/v1/me/preferences/' + page, 'PUT', { [id]: selection }))
      .then(() => {
        stored[id] = selection;
      })
      .finally(() => {
        --writes;
      });
    pending = save.catch((err) => message('บันทึกค่าที่เลือกไม่สำเร็จ · ' + err.message, true));
    return save;
  }
  for (const field of fields) {
    restore(field);
    $(field)?.addEventListener('change', () => {
      remember(field).catch(() => {});
    });
  }
  document.addEventListener('click', (event) => {
    const a = event.target.closest('a[href]');
    if (
      !writes ||
      !a ||
      a.origin !== location.origin ||
      a.target ||
      event.ctrlKey ||
      event.metaKey ||
      event.shiftKey
    )
      return;
    event.preventDefault();
    pending.then(() => location.assign(a.href));
  });
  $('authForm')?.addEventListener(
    'submit',
    run(async () => {
      const button = $('authForm').querySelector('button');
      button.disabled = true;
      try {
        const input = { username: value('username'), password: $('password').value };
        const setup = page === 'setup';
        if (setup) input.admin_key = $('bootstrapKey').value;
        await api('/v1/auth/' + (setup ? 'bootstrap' : 'login'), 'POST', input);
        location.replace(document.body.dataset.next || '/');
      } finally {
        button.disabled = false;
      }
    })
  );
  bind('logout', async () => {
    await pending;
    await api('/v1/auth/logout', 'POST', {});
    location.replace('/login');
  });
  $('passwordForm')?.addEventListener(
    'submit',
    run(async () => {
      await api('/v1/me/password', 'POST', {
        current_password: $('oldPassword').value,
        new_password: $('newPassword').value
      });
      location.replace('/login');
    })
  );
  return { $, value, api, bind, run, show, message, restore, remember };
})();
