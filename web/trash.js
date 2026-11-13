'use strict';

const state = {
  groups: [],
  currentUser: null,
  filterProject: '',
  query: '',
};

const el = {
  total: document.getElementById('trash-total'),
  search: document.getElementById('trash-search'),
  groups: document.getElementById('trash-groups'),
  empty: document.getElementById('trash-empty'),
  toast: document.getElementById('toast'),
  userSelect: document.getElementById('user-select'),
};

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
  }, 3400);
}

function fmtDate(value) {
  if (!value) return '';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleString('th-TH', { dateStyle: 'medium', timeStyle: 'short' });
}

function encodeName(name) {
  return encodeURIComponent(name);
}

function projectBase(projectId) {
  return `/api/projects/${encodeURIComponent(projectId)}`;
}

function imageUrl(projectId, name) {
  return `${projectBase(projectId)}/images/${encodeName(name)}`;
}

function isOwner(project) {
  return Boolean(state.currentUser && project.owner_id === state.currentUser.id);
}

// Restore is allowed for the owner or the user who uploaded the image, matching
// the API rule. Purging is owner-only because it is irreversible.
function canRestore(project, image) {
  if (!state.currentUser) return false;
  return isOwner(project) || image.uploaded_by === state.currentUser.id;
}

function matchesQuery(project, image) {
  if (!state.query) return true;
  const needle = state.query.toLowerCase();
  const haystack = [
    project.name,
    image.original_name,
    image.name,
    image.uploaded_by_name,
  ]
    .filter(Boolean)
    .join(' ')
    .toLowerCase();
  return haystack.includes(needle);
}

function makeButton(label, className, onClick, title) {
  const button = document.createElement('button');
  button.type = 'button';
  button.className = className;
  button.textContent = label;
  if (title) button.title = title;
  button.addEventListener('click', onClick);
  return button;
}

function renderCard(project, image) {
  const card = document.createElement('article');
  card.className = 'trash-card';

  const img = document.createElement('img');
  img.className = 'trash-img';
  img.src = imageUrl(project.id, image.name);
  img.alt = image.original_name || image.name;
  img.loading = 'lazy';
  card.appendChild(img);

  const body = document.createElement('div');
  body.className = 'trash-body';

  const name = document.createElement('span');
  name.className = 'thumb-name';
  name.textContent = image.original_name || image.name;
  name.title = image.original_name
    ? `${image.original_name}\nไฟล์ที่เก็บ: ${image.name}`
    : image.name;
  body.appendChild(name);

  const when = document.createElement('p');
  when.className = 'trash-when';
  const who = image.uploaded_by_name ? ` · อัปโหลดโดย ${image.uploaded_by_name}` : '';
  when.textContent = image.deleted_at
    ? `ลบเมื่อ ${fmtDate(image.deleted_at)}${who}`
    : `อยู่ในถังขยะ${who}`;
  body.appendChild(when);

  const boxes = document.createElement('p');
  boxes.className = 'trash-when';
  boxes.textContent = `${image.label_count} bounding box`;
  body.appendChild(boxes);

  const actions = document.createElement('div');
  actions.className = 'trash-actions';

  if (canRestore(project, image)) {
    actions.appendChild(
      makeButton('กู้คืน', 'primary-btn small', () => restoreImage(project, image), 'นำรูปกลับเข้า project'),
    );
  }
  if (isOwner(project)) {
    actions.appendChild(
      makeButton('ลบถาวร', 'danger-btn small', () => purgeImage(project, image), 'ลบไฟล์และ label ถาวร (เจ้าของเท่านั้น)'),
    );
  } else {
    const note = document.createElement('span');
    note.className = 'empty-inline';
    note.textContent = 'ลบถาวร: เฉพาะเจ้าของ project';
    actions.appendChild(note);
  }

  body.appendChild(actions);
  card.appendChild(body);
  return card;
}

function render() {
  el.groups.innerHTML = '';
  let total = 0;

  for (const group of state.groups) {
    if (state.filterProject && group.project.id !== state.filterProject) continue;

    const project = group.project;
    const images = (group.images || []).filter((image) => matchesQuery(project, image));
    if (images.length === 0) continue;
    total += images.length;

    const section = document.createElement('section');
    section.className = 'trash-group';

    const head = document.createElement('div');
    head.className = 'trash-group-head';

    const title = document.createElement('h2');
    const link = document.createElement('a');
    link.href = `project.html?id=${encodeURIComponent(project.id)}`;
    link.textContent = project.name;
    link.title = `เปิด project ${project.name}`;
    title.appendChild(link);

    const count = document.createElement('span');
    count.className = 'badge';
    count.textContent = String(images.length);
    title.appendChild(count);
    head.appendChild(title);

    const meta = document.createElement('p');
    meta.className = 'trash-group-meta';
    const owner = project.owner_name
      ? `เจ้าของ: ${project.owner_name} (@${project.owner_username})`
      : 'เจ้าของ: -';
    meta.textContent = `${owner} · ใน project เหลือ ${project.image_count} รูป · ${
      isOwner(project) ? 'คุณเป็นเจ้าของ project นี้' : 'คุณไม่ใช่เจ้าของ project นี้'
    }`;
    head.appendChild(meta);

    section.appendChild(head);

    const grid = document.createElement('div');
    grid.className = 'trash-grid';
    for (const image of images) {
      grid.appendChild(renderCard(project, image));
    }
    section.appendChild(grid);

    el.groups.appendChild(section);
  }

  el.total.textContent = String(total);
  el.empty.hidden = total > 0;
}

async function load() {
  try {
    const data = await api('/api/trash');
    state.groups = data.projects || [];
    render();
  } catch (err) {
    state.groups = [];
    el.groups.innerHTML = '';
    el.total.textContent = '0';
    el.empty.hidden = false;
    toast('โหลดถังขยะไม่สำเร็จ: ' + err.message, true);
  }
}

async function restoreImage(project, image) {
  try {
    await api(`${projectBase(project.id)}/images/${encodeName(image.name)}/restore`, {
      method: 'POST',
    });
    await load();
    toast(`กู้คืน "${image.original_name || image.name}" ใน project ${project.name} แล้ว`);
  } catch (err) {
    toast('กู้คืนรูปไม่สำเร็จ: ' + err.message, true);
  }
}

async function purgeImage(project, image) {
  const label = image.original_name || image.name;
  if (!confirm(`ลบรูป "${label}" ของ project "${project.name}" ถาวร?\nไฟล์และ label จะหายไปและกู้คืนไม่ได้`)) {
    return;
  }
  try {
    await api(`${projectBase(project.id)}/images/${encodeName(image.name)}/permanent`, {
      method: 'DELETE',
    });
    await load();
    toast('ลบรูปถาวรแล้ว');
  } catch (err) {
    toast('ลบรูปถาวรไม่สำเร็จ: ' + err.message, true);
  }
}

el.search.addEventListener('input', () => {
  state.query = el.search.value.trim();
  render();
});

const params = new URLSearchParams(window.location.search);
state.filterProject = params.get('project') || '';

(async function boot() {
  state.currentUser = await initUserSelect(el.userSelect);
  if (state.filterProject) {
    const data = await api('/api/projects/' + encodeURIComponent(state.filterProject)).catch(() => null);
    if (data && data.name) {
      document.title = `Trash · ${data.name}`;
      el.search.placeholder = `ค้นหาใน project "${data.name}"`;
    }
  }
  await load();
})();
