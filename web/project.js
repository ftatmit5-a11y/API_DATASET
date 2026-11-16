'use strict';

const state = {
  id: '',
  project: null,
  currentUser: null,
  students: [],
  uploading: false,
  exporting: false,
};

const el = {
  name: document.getElementById('project-name'),
  subtitle: document.getElementById('project-subtitle'),
  owner: document.getElementById('project-owner'),
  input: document.getElementById('image-input'),
  uploadLabel: document.getElementById('upload-label'),
  makeData: document.getElementById('make-data'),
  statImages: document.getElementById('stat-images'),
  statBoxes: document.getElementById('stat-boxes'),
  statClasses: document.getElementById('stat-classes'),
  statPending: document.getElementById('stat-pending'),
  classChips: document.getElementById('class-chips'),
  openSwitch: document.getElementById('open-switch'),
  reviewLink: document.getElementById('review-link'),
  memberPicker: document.getElementById('member-picker'),
  memberCount: document.getElementById('member-count'),
  saveMembers: document.getElementById('save-members'),
  grid: document.getElementById('image-grid'),
  empty: document.getElementById('image-empty'),
  trashLink: document.getElementById('project-trash-link'),
  trashCount: document.getElementById('project-trash-count'),
  drop: document.getElementById('drop-zone'),
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
  }, 3400);
}

function base() {
  return `/api/projects/${encodeURIComponent(state.id)}`;
}

function encodeName(name) {
  return encodeURIComponent(name);
}

function annotateUrl(imageName) {
  return `annotate.html?project=${encodeURIComponent(state.id)}&image=${encodeName(imageName)}`;
}

function imageUrl(name) {
  return `${base()}/images/${encodeName(name)}`;
}

function isOwner() {
  return Boolean(
    state.project && state.currentUser && state.project.owner_id === state.currentUser.id,
  );
}

// Only the owner manages members, opens submissions, and imports photos.
function canManage() {
  return isOwner();
}

function canDeleteImage(image) {
  if (!state.currentUser) return false;
  return isOwner() || image.uploaded_by === state.currentUser.id;
}

function renderHeader() {
  const p = state.project;
  if (!p) return;
  document.title = p.name;
  el.name.textContent = p.name;
  el.subtitle.textContent = `สร้างเมื่อ ${new Date(p.created_at).toLocaleString('th-TH', {
    dateStyle: 'medium',
    timeStyle: 'short',
  })}`;
  el.owner.textContent = p.owner_name
    ? `เจ้าของ: ${p.owner_name} (@${p.owner_username})`
    : 'เจ้าของ: -';
  el.statImages.textContent = `${p.image_count} รูป`;
  el.statBoxes.textContent = `${p.box_count} boxes`;
  el.statClasses.textContent = `${p.class_count} classes`;
  el.statPending.textContent = `${p.pending_count || 0} รออนุมัติ`;

  el.classChips.innerHTML = '';
  const classes = p.classes || [];
  if (classes.length === 0) {
    el.classChips.appendChild(
      mkEl('span', 'empty-inline', 'ยังไม่มี class — อาจารย์กำหนดได้ที่หน้านี้ หรือเปิดหน้ารูปแล้วเพิ่ม'),
    );
  } else {
    classes.forEach((name, index) => {
      el.classChips.appendChild(mkEl('span', 'chip class-chip', `${index}: ${name}`));
    });
  }

  const hasImages = p.image_count > 0;
  el.makeData.disabled = !hasImages || state.exporting;
  el.makeData.title = hasImages
    ? 'รวมรูปและ label ทั้งหมดของ project นี้เป็น ZIP'
    : 'เพิ่มรูปก่อนจึงจะทำ data ได้';

  el.reviewLink.href = `pending.html?project=${encodeURIComponent(state.id)}`;
  el.uploadLabel.hidden = !canManage();
  el.drop.hidden = !canManage();
  el.openSwitch.disabled = !canManage();
  el.saveMembers.disabled = !canManage();
}

function renderImages() {
  const images = (state.project && state.project.images) || [];
  el.empty.hidden = images.length > 0;
  el.grid.innerHTML = '';

  for (const image of images) {
    const card = mkEl('article', 'thumb-card');

    const link = mkEl('a', 'thumb-link');
    link.href = annotateUrl(image.name);
    link.title = `เปิด ${image.name} เพื่อวาด bounding box`;

    const img = mkEl('img', 'thumb-img');
    img.src = imageUrl(image.name);
    img.alt = image.original_name || image.name;
    img.loading = 'lazy';
    link.appendChild(img);
    link.appendChild(mkEl('span', 'thumb-overlay', 'วาด label'));
    card.appendChild(link);

    const meta = mkEl('div', 'thumb-meta');
    const name = mkEl('span', 'thumb-name', image.original_name || image.name);
    name.title = image.original_name
      ? `${image.original_name}\nไฟล์ที่เก็บ: ${image.name}`
      : image.name;
    meta.appendChild(name);
    meta.appendChild(mkEl('span', 'badge', String(image.label_count)));

    if (canDeleteImage(image)) {
      const del = mkEl('button', 'icon-btn', '×');
      del.type = 'button';
      del.title = 'ย้ายรูปนี้ไปถังขยะ';
      del.addEventListener('click', () => deleteImage(image));
      meta.appendChild(del);
    }
    card.appendChild(meta);

    const uploader = mkEl('p', 'thumb-uploader');
    uploader.textContent = image.uploaded_by_name
      ? `อัปโหลดโดย ${image.uploaded_by_name}`
      : 'ไม่ทราบผู้อัปโหลด';
    uploader.title = image.created_at ? fmtDate(image.created_at) : '';
    card.appendChild(uploader);

    el.grid.appendChild(card);
  }
}

function renderTrashLink() {
  const trash = (state.project && state.project.trash) || [];
  el.trashCount.textContent = String(trash.length);
  el.trashLink.href = `trash.html?project=${encodeURIComponent(state.id)}`;
}

function renderClassForm() {
  // Teachers define the class list here so students always label against the
  // same fixed set.
  const panel = document.getElementById('class-editor');
  if (!panel) return;
  panel.innerHTML = '';

  const list = mkEl('div', 'class-edit-rows');
  const classes = (state.project && state.project.classes) || [];
  classes.forEach((name, index) => {
    const row = mkEl('div', 'class-row');
    row.appendChild(mkEl('span', 'class-row-index', String(index)));

    const input = mkEl('input', 'class-row-name');
    input.type = 'text';
    input.maxLength = 80;
    input.value = name;
    row.appendChild(input);

    const remove = mkEl('button', 'icon-btn', '×');
    remove.type = 'button';
    remove.title = 'ลบคลาสนี้';
    remove.addEventListener('click', () => row.remove());
    row.appendChild(remove);

    list.appendChild(row);
  });

  const add = mkEl('button', 'ghost', '+ เพิ่มคลาส');
  add.type = 'button';
  add.addEventListener('click', () => {
    const row = mkEl('div', 'class-row');
    row.appendChild(mkEl('span', 'class-row-index', String(list.children.length)));
    const input = mkEl('input', 'class-row-name');
    input.type = 'text';
    input.maxLength = 80;
    input.placeholder = 'ชื่อ class';
    row.appendChild(input);
    const remove = mkEl('button', 'icon-btn', '×');
    remove.type = 'button';
    remove.title = 'ลบคลาสนี้';
    remove.addEventListener('click', () => {
      row.remove();
      [...list.children].forEach((r, i) => {
        r.querySelector('.class-row-index').textContent = String(i);
      });
    });
    row.appendChild(remove);
    list.appendChild(row);
    input.focus();
    [...list.children].forEach((r, i) => {
      r.querySelector('.class-row-index').textContent = String(i);
    });
  });

  const save = mkEl('button', 'primary-btn small', 'บันทึกรายชื่อคลาส');
  save.type = 'button';
  save.addEventListener('click', () => saveClasses(list));

  panel.appendChild(list);
  const actions = mkEl('div', 'class-edit-actions');
  actions.appendChild(add);
  actions.appendChild(save);
  panel.appendChild(actions);

  panel.hidden = false;
}

async function saveClasses(list) {
  const names = [...list.querySelectorAll('.class-row-name')]
    .map((input) => input.value.trim())
    .filter(Boolean);
  const lower = names.map((n) => n.toLowerCase());
  if (new Set(lower).size !== lower.length) {
    toast('ชื่อ class ซ้ำกัน', true);
    return;
  }
  try {
    await api(`${base()}/classes`, {
      method: 'PUT',
      headers: JSON_HEADERS,
      body: JSON.stringify({ classes: names }),
    });
    await load();
    toast(`บันทึก ${names.length} classes แล้ว`);
  } catch (err) {
    toast('บันทึก class ไม่สำเร็จ: ' + err.message, true);
  }
}

async function load() {
  try {
    state.project = await api(base());
    document.body.classList.remove('load-error');
    renderHeader();
    renderImages();
    renderTrashLink();
    renderClassForm();
    await renderMembers();
  } catch (err) {
    document.body.classList.add('load-error');
    el.name.textContent = 'เปิด project ไม่ได้';
    el.subtitle.textContent = err.message;
    el.makeData.disabled = true;
    el.classChips.innerHTML = '';
    el.grid.innerHTML = '';
    el.empty.hidden = true;
    el.trashCount.textContent = '0';
    toast('โหลด project ไม่สำเร็จ: ' + err.message, true);
  }
}

async function loadStudents() {
  try {
    const data = await api('/api/users');
    state.students = (data.users || []).filter((u) => u.role === 'student');
  } catch {
    state.students = [];
  }
}

async function renderMembers() {
  let members = [];
  try {
    const data = await api(`${base()}/members`);
    members = data.members || [];
  } catch {
    members = [];
  }
  const chosen = new Set(members.map((m) => m.id));

  el.memberPicker.innerHTML = '';
  if (state.students.length === 0) {
    el.memberPicker.appendChild(
      mkEl('p', 'hint', 'ยังไม่มีบัญชีนักศึกษา — สร้างได้ที่หน้า “บัญชี”'),
    );
    el.memberCount.textContent = String(members.length);
    return;
  }

  for (const student of state.students) {
    const label = mkEl('label', 'picker-item');
    const box = mkEl('input', null);
    box.type = 'checkbox';
    box.value = student.id;
    box.className = 'member-check';
    box.checked = chosen.has(student.id);
    box.disabled = !canManage();
    label.appendChild(box);
    label.appendChild(mkEl('span', null, `${student.display_name} (@${student.username})`));
    el.memberPicker.appendChild(label);
  }
  el.memberCount.textContent = String(members.length);
}

async function saveMembers() {
  const picked = [...el.memberPicker.querySelectorAll('.member-check:checked')].map((b) => b.value);
  try {
    if (picked.length > 0) {
      await api(`${base()}/members`, {
        method: 'POST',
        headers: JSON_HEADERS,
        body: JSON.stringify({ user_ids: picked }),
      });
    }
    // Anyone ticked off gets removed, so the list matches the checkboxes.
    const current = await api(`${base()}/members`);
    for (const member of current.members || []) {
      if (!picked.includes(member.id)) {
        await api(`${base()}/members/${encodeURIComponent(member.id)}`, { method: 'DELETE' });
      }
    }
    await load();
    toast(`บันทึกนักศึกษา ${picked.length} คนแล้ว`);
  } catch (err) {
    toast('บันทึกนักศึกษาไม่สำเร็จ: ' + err.message, true);
  }
}

async function toggleOpen(event) {
  const open = event.target.checked;
  try {
    await api(`${base()}/submissions-open`, {
      method: 'PUT',
      headers: JSON_HEADERS,
      body: JSON.stringify({ open }),
    });
    await load();
    toast(open ? 'เปิดรับรูปจากนักศึกษาแล้ว' : 'ปิดรับรูปจากนักศึกษาแล้ว');
  } catch (err) {
    event.target.checked = !open;
    toast('เปลี่ยนสถานะไม่สำเร็จ: ' + err.message, true);
  }
}

async function uploadFiles(files) {
  if (!state.project || state.uploading) return;
  const imageName = /\.(jpe?g|png|bmp|webp|tiff?|gif)$/i;
  const list = files.filter(
    (file) => file && (file.type.startsWith('image/') || imageName.test(file.name)),
  );
  if (list.length === 0) {
    toast('เลือกไฟล์รูปภาพก่อน', true);
    return;
  }

  const form = new FormData();
  list.forEach((file) => form.append('files', file));

  state.uploading = true;
  el.uploadLabel.classList.add('disabled');
  toast(`กำลังอัปโหลด ${list.length} รูป...`);
  try {
    await api(`${base()}/images`, { method: 'POST', body: form });
    await load();
    toast(`อัปโหลด ${list.length} รูปแล้ว`);
  } catch (err) {
    toast('อัปโหลดไม่สำเร็จ: ' + err.message, true);
  } finally {
    state.uploading = false;
    el.uploadLabel.classList.remove('disabled');
    el.input.value = '';
  }
}

async function deleteImage(image) {
  if (!confirm(`ย้ายรูป "${image.original_name || image.name}" ไปถังขยะ?`)) return;
  try {
    await api(`${base()}/images/${encodeName(image.name)}`, { method: 'DELETE' });
    await load();
    toast('ย้ายรูปไปถังขยะแล้ว — กู้คืนได้ในหน้าถังขยะ');
  } catch (err) {
    toast('ย้ายไปถังขยะไม่สำเร็จ: ' + err.message, true);
  }
}

async function renameProject() {
  if (!state.project || !isOwner()) return;
  const name = prompt('ชื่อ project ใหม่', state.project.name);
  if (name === null) return;
  const trimmed = name.trim();
  if (!trimmed || trimmed === state.project.name) return;
  try {
    await api(base(), {
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

function exportData() {
  if (!state.project || state.project.image_count === 0 || state.exporting) return;
  if (
    state.project.class_count === 0 &&
    !confirm('project นี้ยังไม่มี class — data ที่ได้จะไม่มี label ต้องการทำต่อหรือไม่?')
  ) {
    return;
  }
  state.exporting = true;
  renderHeader();
  toast('กำลังสร้าง ZIP...');
  window.location.href = `${base()}/export`;
  setTimeout(() => {
    state.exporting = false;
    renderHeader();
  }, 2500);
}

el.input.addEventListener('change', () => {
  uploadFiles(Array.from(el.input.files || []));
});

el.makeData.addEventListener('click', exportData);
el.openSwitch.addEventListener('change', toggleOpen);
el.saveMembers.addEventListener('click', saveMembers);

el.name.addEventListener('dblclick', renameProject);
el.name.title = 'ดับเบิลคลิกเพื่อเปลี่ยนชื่อ project (เฉพาะเจ้าของ)';

['dragenter', 'dragover'].forEach((event) =>
  el.drop.addEventListener(event, (e) => {
    e.preventDefault();
    el.drop.classList.add('dragover');
  }),
);
el.drop.addEventListener('dragleave', () => el.drop.classList.remove('dragover'));
el.drop.addEventListener('drop', (e) => {
  e.preventDefault();
  el.drop.classList.remove('dragover');
  uploadFiles(Array.from(e.dataTransfer.files || []));
});

window.addEventListener('dragover', (e) => e.preventDefault());
window.addEventListener('drop', (e) => e.preventDefault());

const params = new URLSearchParams(window.location.search);
state.id = params.get('id') || '';

(async function boot() {
  if (!state.id) {
    toast('ไม่พบ project ที่เลือก', true);
    el.makeData.disabled = true;
    el.input.disabled = true;
    return;
  }
  state.currentUser = await requireUser('teacher');
  if (!state.currentUser) return;
  renderIdentity(state.currentUser, el);
  await Promise.all([load(), loadStudents()]);
})();
