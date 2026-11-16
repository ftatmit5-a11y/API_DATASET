'use strict';

const state = {
  projects: [],
  students: [],
  currentUser: null,
};

const el = {
  form: document.getElementById('project-form'),
  name: document.getElementById('project-name'),
  classRows: document.getElementById('class-rows'),
  classCount: document.getElementById('class-count'),
  addClassRow: document.getElementById('add-class-row'),
  studentPicker: document.getElementById('student-picker'),
  openNow: document.getElementById('open-now'),
  grid: document.getElementById('project-grid'),
  count: document.getElementById('project-count'),
  empty: document.getElementById('project-empty'),
  toast: document.getElementById('toast'),
  identity: document.getElementById('identity'),
  logout: document.getElementById('logout-btn'),
};

const JSON_HEADERS = { 'Content-Type': 'application/json' };
let toastTimer = 0;

// api() lives in common.js: it adds the CSRF header and redirects to the login
// page when the session expires.

function toast(text, isError = false) {
  el.toast.textContent = text;
  el.toast.className = 'toast show' + (isError ? ' error' : '');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    el.toast.className = 'toast';
  }, 3200);
}

function fmtDate(value) {
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleString('th-TH', { dateStyle: 'medium', timeStyle: 'short' });
}

function projectUrl(id) {
  return `project.html?id=${encodeURIComponent(id)}`;
}

function pendingUrl(id) {
  return `pending.html?project=${encodeURIComponent(id)}`;
}

function projectMark(name) {
  const trimmed = (name || '').trim();
  return trimmed ? trimmed.charAt(0).toUpperCase() : 'P';
}

function isOwner(project) {
  return Boolean(state.currentUser && project.owner_id === state.currentUser.id);
}

// ---------- create form ----------

function addClassRow(value = '') {
  const row = document.createElement('div');
  row.className = 'class-row';

  const index = mkEl('span', 'class-row-index', String(el.classRows.children.length));
  const input = document.createElement('input');
  input.type = 'text';
  input.maxLength = 80;
  input.placeholder = 'ชื่อ class เช่น roach';
  input.className = 'class-row-name';
  input.value = value;
  input.addEventListener('input', () => {
    refreshClassIndexes();
    updateCounts();
  });

  const remove = mkEl('button', 'icon-btn', '×');
  remove.type = 'button';
  remove.title = 'ลบช่องนี้';
  remove.addEventListener('click', () => {
    row.remove();
    refreshClassIndexes();
    updateCounts();
  });

  row.appendChild(index);
  row.appendChild(input);
  row.appendChild(remove);
  el.classRows.appendChild(row);
  refreshClassIndexes();
  updateCounts();
  input.focus();
  return input;
}

function refreshClassIndexes() {
  [...el.classRows.children].forEach((row, i) => {
    const badge = row.querySelector('.class-row-index');
    if (badge) badge.textContent = String(i);
  });
}

function classNames() {
  return [...el.classRows.querySelectorAll('.class-row-name')]
    .map((input) => input.value.trim())
    .filter(Boolean);
}

function updateCounts() {
  el.classCount.textContent = String(classNames().length);
}

function renderStudentPicker() {
  el.studentPicker.innerHTML = '';
  if (state.students.length === 0) {
    el.studentPicker.appendChild(
      mkEl('p', 'hint', 'ยังไม่มีบัญชีนักศึกษา — สร้างได้ที่หน้า “บัญชี”'),
    );
    return;
  }
  for (const student of state.students) {
    const label = document.createElement('label');
    label.className = 'picker-item';

    const box = document.createElement('input');
    box.type = 'checkbox';
    box.value = student.id;
    box.className = 'student-check';

    label.appendChild(box);
    label.appendChild(mkEl('span', null, `${student.display_name} (@${student.username})`));
    el.studentPicker.appendChild(label);
  }
}

function pickedStudents() {
  return [...el.studentPicker.querySelectorAll('.student-check:checked')].map((box) => box.value);
}

function resetForm() {
  el.name.value = '';
  el.classRows.innerHTML = '';
  addClassRow();
  addClassRow();
  [...el.studentPicker.querySelectorAll('.student-check')].forEach((box) => {
    box.checked = false;
  });
  el.openNow.checked = true;
  updateCounts();
}

async function createProject() {
  const name = el.name.value.trim();
  if (!name) {
    toast('ใส่ชื่อ project ก่อน', true);
    return;
  }
  const classes = classNames();
  if (new Set(classes.map((c) => c.toLowerCase())).size !== classes.length) {
    toast('ชื่อ class ซ้ำกัน (ไม่สนตัวเล็กใหญ่)', true);
    return;
  }
  try {
    const info = await api('/api/projects', {
      method: 'POST',
      headers: JSON_HEADERS,
      body: JSON.stringify({
        name,
        classes,
        members: pickedStudents(),
        submissions_open: el.openNow.checked,
      }),
    });
    resetForm();
    await load();
    toast(`สร้าง project "${info.name}" พร้อม ${info.class_count} classes`);
    const card = el.grid.querySelector(`[data-id="${info.id}"]`);
    if (card) {
      card.classList.add('just-created');
      card.scrollIntoView({ behavior: 'smooth', block: 'center' });
      setTimeout(() => card.classList.remove('just-created'), 1600);
    }
  } catch (err) {
    toast('สร้าง project ไม่สำเร็จ: ' + err.message, true);
  }
}

// ---------- project cards ----------

function projectBadges(project) {
  const wrap = mkEl('div', 'project-badges');
  wrap.appendChild(
    mkEl('span', 'chip ' + (project.submissions_open ? 'chip-open' : 'chip-closed'),
      project.submissions_open ? 'เปิดรับรูป' : 'ปิดรับรูป'),
  );
  if (project.pending_count > 0) {
    const pending = mkEl('a', 'chip chip-pending', `รออนุมัติ ${project.pending_count}`);
    pending.href = pendingUrl(project.id);
    wrap.appendChild(pending);
  }
  if (project.member_count > 0) {
    wrap.appendChild(mkEl('span', 'chip', `นักศึกษา ${project.member_count} คน`));
  }
  return wrap;
}

function render() {
  el.count.textContent = String(state.projects.length);
  el.empty.hidden = state.projects.length > 0;
  el.grid.innerHTML = '';

  for (const project of state.projects) {
    const card = document.createElement('article');
    card.className = 'project-card';
    card.dataset.id = project.id;

    const link = document.createElement('a');
    link.className = 'project-card-link';
    link.href = projectUrl(project.id);
    link.title = `เปิด project ${project.name}`;

    const cover = mkEl('div', 'project-cover');
    cover.appendChild(mkEl('span', 'project-cover-mark', projectMark(project.name)));
    link.appendChild(cover);

    const body = mkEl('div', 'project-body');
    body.appendChild(mkEl('h3', 'project-title', project.name));
    body.appendChild(
      mkEl('p', 'project-stats',
        `${project.image_count} รูป · ${project.box_count} boxes · ${project.class_count} classes`),
    );
    body.appendChild(projectBadges(project));
    body.appendChild(
      mkEl('p', 'project-owner',
        project.owner_name ? `เจ้าของ: ${project.owner_name} (@${project.owner_username})` : 'เจ้าของ: -'),
    );
    if (project.created_at) {
      body.appendChild(mkEl('p', 'project-date', `สร้างเมื่อ ${fmtDate(project.created_at)}`));
    }
    link.appendChild(body);
    card.appendChild(link);

    const actions = mkEl('div', 'project-card-actions');
    const open = mkEl('a', 'primary-btn small', 'เปิด project');
    open.href = projectUrl(project.id);
    actions.appendChild(open);

    if (project.pending_count > 0) {
      const review = mkEl('a', 'ghost', `ตรวจ ${project.pending_count}`);
      review.href = pendingUrl(project.id);
      actions.appendChild(review);
    }

    if (isOwner(project)) {
      const rename = mkEl('button', 'ghost', 'เปลี่ยนชื่อ');
      rename.type = 'button';
      rename.addEventListener('click', () => renameProject(project));
      actions.appendChild(rename);

      const del = mkEl('button', 'icon-btn', '×');
      del.type = 'button';
      del.title = 'ลบ project';
      del.addEventListener('click', () => deleteProject(project));
      actions.appendChild(del);
    }

    card.appendChild(actions);
    el.grid.appendChild(card);
  }
}

async function load() {
  try {
    const data = await api('/api/projects');
    state.projects = data.projects || [];
    render();
  } catch (err) {
    toast('โหลดรายการ project ไม่สำเร็จ: ' + err.message, true);
  }
}

async function loadStudents() {
  try {
    const data = await api('/api/users');
    state.students = (data.users || []).filter((u) => u.role === 'student');
  } catch {
    state.students = [];
  }
  renderStudentPicker();
}

async function renameProject(project) {
  const name = prompt('ชื่อ project ใหม่', project.name);
  if (name === null) return;
  const trimmed = name.trim();
  if (!trimmed || trimmed === project.name) return;
  try {
    await api(`/api/projects/${encodeURIComponent(project.id)}`, {
      method: 'PATCH',
      headers: JSON_HEADERS,
      body: JSON.stringify({ name: trimmed }),
    });
    await load();
    toast('เปลี่ยนชื่อ project แล้ว');
  } catch (err) {
    toast('เปลี่ยนชื่อ project ไม่สำเร็จ: ' + err.message, true);
  }
}

async function deleteProject(project) {
  if (!confirm(`ลบ project "${project.name}" พร้อมรูป,label และ submission ทั้งหมด?`)) return;
  try {
    await api(`/api/projects/${encodeURIComponent(project.id)}`, { method: 'DELETE' });
    await load();
    toast('ลบ project แล้ว');
  } catch (err) {
    toast('ลบ project ไม่สำเร็จ: ' + err.message, true);
  }
}

el.form.addEventListener('submit', (e) => {
  e.preventDefault();
  createProject();
});
el.addClassRow.addEventListener('click', () => addClassRow());

(async function boot() {
  state.currentUser = await requireUser('teacher');
  if (!state.currentUser) return;
  renderIdentity(state.currentUser, el);
  resetForm();
  await Promise.all([load(), loadStudents()]);
})();
