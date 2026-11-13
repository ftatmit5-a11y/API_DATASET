'use strict';

const state = {
  projects: [],
  currentUser: null,
};

const el = {
  form: document.getElementById('project-form'),
  name: document.getElementById('project-name'),
  grid: document.getElementById('project-grid'),
  count: document.getElementById('project-count'),
  empty: document.getElementById('project-empty'),
  toast: document.getElementById('toast'),
  userSelect: document.getElementById('user-select'),
};

const JSON_HEADERS = { 'Content-Type': 'application/json' };
let toastTimer = 0;

async function api(path, options) {
  const res = await userFetch(path, options);
  const text = await res.text();
  let data = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  if (!res.ok) {
    throw new Error((data && data.error) || text || res.statusText);
  }
  return data;
}

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

function statLine(project) {
  return `${project.image_count} รูป · ${project.box_count} boxes · ${project.class_count} classes`;
}

function projectMark(name) {
  const trimmed = (name || '').trim();
  return trimmed ? trimmed.charAt(0).toUpperCase() : 'P';
}

function isOwner(project) {
  return Boolean(state.currentUser && project.owner_id === state.currentUser.id);
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

    const cover = document.createElement('div');
    cover.className = 'project-cover';
    const mark = document.createElement('span');
    mark.className = 'project-cover-mark';
    mark.textContent = projectMark(project.name);
    cover.appendChild(mark);
    link.appendChild(cover);

    const body = document.createElement('div');
    body.className = 'project-body';

    const title = document.createElement('h3');
    title.className = 'project-title';
    title.textContent = project.name;
    body.appendChild(title);

    const stats = document.createElement('p');
    stats.className = 'project-stats';
    stats.textContent = statLine(project);
    body.appendChild(stats);

    const owner = document.createElement('p');
    owner.className = 'project-owner';
    owner.textContent = project.owner_name
      ? `เจ้าของ: ${project.owner_name} (@${project.owner_username})`
      : 'เจ้าของ: -';
    body.appendChild(owner);

    const date = document.createElement('p');
    date.className = 'project-date';
    date.textContent = project.created_at ? `สร้างเมื่อ ${fmtDate(project.created_at)}` : '';
    body.appendChild(date);

    link.appendChild(body);
    card.appendChild(link);

    const actions = document.createElement('div');
    actions.className = 'project-card-actions';

    const open = document.createElement('a');
    open.className = 'primary-btn small';
    open.href = projectUrl(project.id);
    open.textContent = 'เปิด project';
    actions.appendChild(open);

    const rename = document.createElement('button');
    rename.type = 'button';
    rename.className = 'ghost';
    rename.textContent = 'เปลี่ยนชื่อ';
    rename.addEventListener('click', () => renameProject(project));
    if (isOwner(project)) actions.appendChild(rename);

    const del = document.createElement('button');
    del.type = 'button';
    del.className = 'icon-btn';
    del.textContent = '×';
    del.title = 'ลบ project';
    del.addEventListener('click', () => deleteProject(project));
    if (isOwner(project)) actions.appendChild(del);

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

async function createProject(name) {
  try {
    const info = await api('/api/projects', {
      method: 'POST',
      headers: JSON_HEADERS,
      body: JSON.stringify({ name }),
    });
    await load();
    toast(`สร้าง project "${info.name}" แล้ว`);
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
  if (!confirm(`ลบ project "${project.name}" พร้อมรูปและ label ทั้งหมด?`)) return;
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
  const name = el.name.value.trim();
  if (!name) return;
  el.name.value = '';
  createProject(name);
});

(async function boot() {
  state.currentUser = await initUserSelect(el.userSelect);
  await load();
})();
