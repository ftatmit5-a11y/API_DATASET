'use strict';

const state = {
  user: null,
  users: [],
};

const el = {
  userForm: document.getElementById('user-form'),
  username: document.getElementById('new-username'),
  display: document.getElementById('new-display'),
  role: document.getElementById('new-role'),
  password: document.getElementById('new-password'),
  table: document.getElementById('user-table'),
  count: document.getElementById('user-count'),
  selfForm: document.getElementById('self-form'),
  selfCurrent: document.getElementById('self-current'),
  selfPassword: document.getElementById('self-password'),
  toast: document.getElementById('toast'),
  identity: document.getElementById('identity'),
  logout: document.getElementById('logout-btn'),
};

let toastTimer = 0;

// api() lives in common.js: it adds the CSRF header and redirects to the login
// page when the session expires.

function toast(text, isError = false) {
  el.toast.textContent = text;
  el.toast.className = 'toast show' + (isError ? ' error' : '');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    el.toast.className = 'toast';
  }, 3400);
}

function json(method, value) {
  return {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(value),
  };
}

function cell(text) {
  const td = document.createElement('td');
  td.textContent = text ?? '';
  return td;
}

function roleChip(role) {
  const chip = mkEl('span', 'role-tag ' + (role === 'teacher' ? 'role-teacher' : 'role-student'));
  chip.textContent = role === 'teacher' ? 'อาจารย์' : 'นักศึกษา';
  return chip;
}


function render() {
  el.count.textContent = String(state.users.length);
  el.table.innerHTML = '';

  const table = mkEl('table');
  const head = mkEl('thead');
  const headRow = mkEl('tr');
  for (const label of ['ชื่อที่แสดง', 'ชื่อผู้ใช้', 'บทบาท', 'จัดการ']) {
    headRow.appendChild(mkEl('th', null, label));
  }
  head.appendChild(headRow);
  table.appendChild(head);

  const body = mkEl('tbody');
  for (const user of state.users) {
    const row = mkEl('tr');
    row.appendChild(cell(user.display_name));
    row.appendChild(cell(user.username));

    const roleCell = mkEl('td');
    roleCell.appendChild(roleChip(user.role));
    if (user.id === state.user.id) {
      roleCell.appendChild(mkEl('span', 'muted', ' (คุณ)'));
    }
    row.appendChild(roleCell);

    const actions = mkEl('td', 'row-actions');

    const toggle = mkEl('button', 'ghost');
    toggle.type = 'button';
    toggle.textContent = user.role === 'teacher' ? 'เปลี่ยนเป็นนักศึกษา' : 'เปลี่ยนเป็นอาจารย์';
    toggle.disabled = user.id === state.user.id;
    toggle.title = user.id === state.user.id
      ? 'เปลี่ยนบทบาทของตัวเองไม่ได้'
      : 'สลับบทบาทบัญชีนี้';
    toggle.addEventListener('click', () =>
      patch(user, { role: user.role === 'teacher' ? 'student' : 'teacher' }, 'เปลี่ยนบทบาทแล้ว'));
    actions.appendChild(toggle);

    const rename = mkEl('button', 'ghost');
    rename.type = 'button';
    rename.textContent = 'แก้ชื่อที่แสดง';
    rename.addEventListener('click', () => {
      const value = prompt('ชื่อที่แสดงใหม่', user.display_name);
      if (value === null) return;
      const trimmed = value.trim();
      if (!trimmed || trimmed === user.display_name) return;
      patch(user, { display_name: trimmed }, 'แก้ชื่อที่แสดงแล้ว');
    });
    actions.appendChild(rename);

    const reset = mkEl('button', 'danger-btn small');
    reset.type = 'button';
    reset.textContent = 'รีเซ็ตรหัสผ่าน';
    reset.title = user.id === state.user.id
      ? 'ใช้ฟอร์ม “เปลี่ยนรหัสผ่านของฉัน” ด้านล่างแทน'
      : 'ตั้งรหัสผ่านใหม่ให้บัญชีนี้ (บัญชีนี้จะถูกออกจากระบบทุกอุปกรณ์)';
    reset.disabled = user.id === state.user.id;
    reset.addEventListener('click', () => {
      const value = prompt(`รหัสผ่านใหม่สำหรับ ${user.username} (อย่างน้อย 6 ตัวอักษร)`, '');
      if (value === null) return;
      if (value.length < 6) {
        toast('รหัสผ่านต้องยาวอย่างน้อย 6 ตัวอักษร', true);
        return;
      }
      patch(user, { password: value }, `รีเซ็ตรหัสผ่านของ ${user.username} แล้ว`);
    });
    actions.appendChild(reset);

    row.appendChild(actions);
    body.appendChild(row);
  }
  table.appendChild(body);
  el.table.appendChild(table);
}

async function load() {
  try {
    const data = await api('/api/users');
    state.users = data.users || [];
    render();
  } catch (err) {
    toast('โหลดรายชื่อบัญชีไม่สำเร็จ: ' + err.message, true);
  }
}

async function patch(user, payload, successText) {
  try {
    await api(`/api/users/${encodeURIComponent(user.id)}`, json('PATCH', payload));
    await load();
    toast(successText);
  } catch (err) {
    toast('บันทึกไม่สำเร็จ: ' + err.message, true);
  }
}

el.userForm.addEventListener('submit', async (e) => {
  e.preventDefault();
  try {
    await api('/api/users', json('POST', {
      username: el.username.value.trim(),
      display_name: el.display.value.trim(),
      role: el.role.value,
      password: el.password.value,
    }));
    el.username.value = '';
    el.display.value = '';
    el.password.value = '';
    await load();
    toast('สร้างบัญชีใหม่แล้ว');
  } catch (err) {
    toast('สร้างบัญชีไม่สำเร็จ: ' + err.message, true);
  }
});

el.selfForm.addEventListener('submit', async (e) => {
  e.preventDefault();
  if (el.selfPassword.value.length < 6) {
    toast('รหัสผ่านใหม่ต้องยาวอย่างน้อย 6 ตัวอักษร', true);
    return;
  }
  try {
    await api('/api/auth/password', json('POST', {
      current: el.selfCurrent.value,
      password: el.selfPassword.value,
    }));
    toast('เปลี่ยนรหัสผ่านแล้ว ต้องเข้าสู่ระบบใหม่');
    setTimeout(() => window.location.replace('/login.html'), 900);
  } catch (err) {
    toast('เปลี่ยนรหัสผ่านไม่สำเร็จ: ' + err.message, true);
  }
});

(async function boot() {
  state.user = await requireUser('teacher');
  if (!state.user) return;
  renderIdentity(state.user, el);
  await load();
})();
