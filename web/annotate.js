'use strict';

const PALETTE = [
  '#e63946', '#2a9d8f', '#e9c46a', '#457b9d', '#f4a261',
  '#9b5de5', '#00bbf9', '#00f5d4', '#ff6b6b', '#8ac926',
];

const state = {
  projects: [],
  project: null,
  currentUser: null,
  classes: [],
  images: [],
  activeClass: -1,
  currentImage: null,
  boxes: [],
  selected: -1,
  dirty: false,
  img: null,
  loadToken: 0,
  // Submission mode: the canvas edits a student photo that has not been
  // approved into the project yet.
  submission: null,
  queue: [],
};

const el = {
  projectForm: document.getElementById('project-form'),
  projectName: document.getElementById('project-name'),
  projectList: document.getElementById('project-list'),
  projectEmpty: document.getElementById('project-empty'),
  projectBadge: document.getElementById('project-badge'),
  classForm: document.getElementById('class-form'),
  className: document.getElementById('class-name'),
  classList: document.getElementById('class-list'),
  classOwnerHint: document.getElementById('class-owner-hint'),
  imageInput: document.getElementById('image-input'),
  imageList: document.getElementById('image-list'),
  imageEmpty: document.getElementById('image-empty'),
  currentImage: document.getElementById('current-image'),
  activeClass: document.getElementById('active-class'),
  dirty: document.getElementById('dirty-indicator'),
  saveBtn: document.getElementById('save-btn'),
  clearBoxes: document.getElementById('clear-boxes'),
  canvasWrap: document.getElementById('canvas-wrap'),
  canvas: document.getElementById('canvas'),
  placeholder: document.getElementById('canvas-placeholder'),
  boxCount: document.getElementById('box-count'),
  boxList: document.getElementById('box-list'),
  toast: document.getElementById('toast'),
  exportLink: document.getElementById('export-link'),
  trashLink: document.getElementById('trash-link'),
  identity: document.getElementById('identity'),
  logoutBtn: document.getElementById('logout-btn'),
  submissionBar: document.getElementById('submission-bar'),
  submissionStatus: document.getElementById('submission-status'),
  submissionMeta: document.getElementById('submission-meta'),
  submissionBack: document.getElementById('submission-back'),
  submissionSubmit: document.getElementById('submission-submit'),
  submissionWithdraw: document.getElementById('submission-withdraw'),
  submissionApprove: document.getElementById('submission-approve'),
  submissionReject: document.getElementById('submission-reject'),
};

const ctx = el.canvas.getContext('2d');

let mode = null; // 'draw' | 'move' | 'resize'
let dragStart = null;
let dragOriginal = null;
let dragMoved = false;
let saving = false;
let toastTimer = 0;

const JSON_HEADERS = { 'Content-Type': 'application/json' };

// ---------- helpers ----------

// api() comes from common.js: it adds the CSRF header and sends the browser to
// the login page when the session has expired.

function toast(text, isError = false) {
  el.toast.textContent = text;
  el.toast.className = 'toast show' + (isError ? ' error' : '');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    el.toast.className = 'toast';
  }, 3200);
}

function clamp(v, min, max) {
  return Math.min(max, Math.max(min, v));
}

function classColor(id) {
  if (id < 0) return '#888';
  return PALETTE[id % PALETTE.length];
}

function colorDot(color) {
  const span = document.createElement('span');
  span.className = 'dot';
  span.style.background = color;
  return span;
}

function projectId() {
  return state.project ? state.project.id : '';
}

function isProjectOwner(project) {
  return Boolean(
    state.currentUser && project && project.owner_id === state.currentUser.id,
  );
}

function canDeleteCurrentImage(img) {
  if (!state.currentUser) return false;
  return isProjectOwner(state.project) || img.uploaded_by === state.currentUser.id;
}

function projectUrl(suffix) {
  return `/api/projects/${encodeURIComponent(projectId())}${suffix}`;
}

function submissionUrl(suffix) {
  const sub = state.submission;
  if (!sub) return '';
  return `${projectUrl('/submissions')}/${encodeURIComponent(sub.id)}${suffix}`;
}

// inSubmissionMode is the single switch the canvas code checks: a submission is
// a student photo that has not been approved into the dataset yet.
function inSubmissionMode() {
  return Boolean(state.submission);
}

function isSubmissionEditable() {
  return Boolean(state.submission && state.submission.can_edit);
}

function updateUrl(projectID, imageName, submissionID) {
  const url = new URL(window.location.href);
  if (projectID) url.searchParams.set('project', projectID);
  else url.searchParams.delete('project');
  if (submissionID) {
    url.searchParams.set('submission', submissionID);
    url.searchParams.delete('image');
  } else {
    url.searchParams.delete('submission');
    if (imageName) url.searchParams.set('image', imageName);
    else url.searchParams.delete('image');
  }
  history.replaceState(null, '', url);
}

function jsonBody(value) {
  return JSON.stringify(value);
}

// ---------- state rendering ----------

function renderToolbar() {
  const hasProject = Boolean(state.project);
  el.projectBadge.textContent = hasProject ? state.project.name : 'ยังไม่ได้เลือก project';
  el.projectBadge.style.borderColor = hasProject ? 'var(--accent, #3b82f6)' : '';

  if (hasProject && state.activeClass >= 0 && state.classes[state.activeClass] !== undefined) {
    el.activeClass.textContent = `${state.activeClass}: ${state.classes[state.activeClass]}`;
    el.activeClass.style.borderColor = classColor(state.activeClass);
  } else {
    el.activeClass.textContent = 'ยังไม่ได้เลือก class';
    el.activeClass.style.borderColor = '';
  }

  const reviewing = inSubmissionMode();
  const editable = !reviewing || isSubmissionEditable();

  el.dirty.textContent = state.dirty ? 'ยังไม่บันทึก' : '';
  el.dirty.classList.toggle('visible', state.dirty);
  el.currentImage.textContent = reviewing
    ? (state.submission.original_name || 'กำลังโหลด...') + ' · ส่งเข้าตรวจ'
    : state.currentImage || 'ยังไม่ได้เลือกรูป';
  el.saveBtn.disabled =
    !hasProject || saving || !state.dirty || !editable || (reviewing ? false : !state.currentImage);
  el.clearBoxes.disabled = !hasProject || !editable || state.boxes.length === 0;

  const canManageClasses = hasProject && isProjectOwner(state.project) && !reviewing;

  el.projectName.disabled = !isTeacherAccount();
  el.projectForm.hidden = !isTeacherAccount();
  el.className.disabled = !canManageClasses;
  el.classForm.querySelector('button').disabled = !canManageClasses;
  el.classForm.hidden = !canManageClasses;
  el.classOwnerHint.hidden = !hasProject || canManageClasses;
  el.imageInput.disabled = !hasProject || reviewing || !isProjectOwner(state.project);

  if (hasProject && !reviewing) {
    el.exportLink.href = projectUrl('/export');
    el.exportLink.classList.remove('disabled');
    el.exportLink.removeAttribute('aria-disabled');
    el.trashLink.href = `trash.html?project=${encodeURIComponent(state.project.id)}`;
    el.trashLink.title = `ดูถังขยะของ project ${state.project.name}`;
  } else {
    el.exportLink.removeAttribute('href');
    el.exportLink.classList.add('disabled');
    el.exportLink.setAttribute('aria-disabled', 'true');
    el.trashLink.href = '/trash.html';
    el.trashLink.title = 'ดูถังขยะทุกรูปที่ถูกลบ';
  }
  renderSubmissionBar();
}

// isTeacherAccount reports whether the signed-in user is staff. Students get a
// read-only project sidebar and work through their submissions instead.
function isTeacherAccount() {
  return isTeacher(state.currentUser);
}

function renderProjects() {
  el.projectList.innerHTML = '';
  el.projectEmpty.hidden = state.projects.length > 0;

  state.projects.forEach((project) => {
    const active = state.project && state.project.id === project.id;
    const li = document.createElement('li');
    li.className = 'project-item' + (active ? ' active' : '');

    const open = document.createElement('button');
    open.type = 'button';
    open.className = 'project-open';
    open.textContent = project.name;
    open.title =
      `${project.name}\nเจ้าของ: ${project.owner_name || '-'}\n` +
      `${project.image_count} รูป · ${project.box_count} boxes · ` +
      `${project.class_count} classes` +
      (isProjectOwner(project) ? '\nดับเบิลคลิกเพื่อเปลี่ยนชื่อ' : '');
    open.addEventListener('click', () => selectProject(project.id));
    open.addEventListener('dblclick', (e) => {
      e.preventDefault();
      if (isProjectOwner(project)) renameProject(project);
    });
    li.appendChild(open);

    const badge = document.createElement('span');
    badge.className = 'badge';
    badge.textContent = String(project.image_count);
    badge.title = 'จำนวนรูปใน project';
    li.appendChild(badge);

    if (isProjectOwner(project)) {
      const del = document.createElement('button');
      del.type = 'button';
      del.className = 'icon-btn';
      del.textContent = '×';
      del.title = 'ลบ project';
      del.addEventListener('click', (e) => {
        e.stopPropagation();
        deleteProject(project);
      });
      li.appendChild(del);
    }

    el.projectList.appendChild(li);
  });
}

function markDirty() {
  state.dirty = true;
  renderToolbar();
}

function renderClasses() {
  el.classList.innerHTML = '';
  if (!state.project) {
    const li = document.createElement('li');
    li.className = 'empty-note';
    li.textContent = 'สร้างหรือเลือก project ก่อน';
    el.classList.appendChild(li);
    return;
  }
  if (state.classes.length === 0) {
    const li = document.createElement('li');
    li.className = 'empty-note';
    li.textContent = 'ยังไม่มี class';
    el.classList.appendChild(li);
    return;
  }
  const canManage = isProjectOwner(state.project);
  state.classes.forEach((name, index) => {
    const li = document.createElement('li');
    li.className = 'class-item' + (index === state.activeClass ? ' active' : '');
    li.appendChild(colorDot(classColor(index)));

    const input = document.createElement('input');
    input.type = 'text';
    input.className = 'class-name';
    input.value = name;
    input.readOnly = !canManage;
    input.title = canManage ? 'แก้ไขชื่อ class' : 'เฉพาะเจ้าของ project เท่านั้นที่แก้ไข class ได้';
    if (canManage) {
      input.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') input.blur();
      });
      input.addEventListener('change', () => renameClass(index, input.value));
    }
    li.appendChild(input);

    if (canManage) {
      const del = document.createElement('button');
      del.type = 'button';
      del.className = 'icon-btn';
      del.textContent = '×';
      del.title = 'ลบ class';
      del.addEventListener('click', (e) => {
        e.stopPropagation();
        deleteClass(index);
      });
      li.appendChild(del);
    }

    li.addEventListener('click', (e) => {
      if (e.target === input) return;
      selectClass(index);
    });
    el.classList.appendChild(li);
  });
}

function renderImages() {
  // In review mode the left list shows submissions instead of dataset images.
  if (inSubmissionMode()) {
    renderQueue();
    return;
  }
  el.imageList.innerHTML = '';
  el.imageEmpty.hidden = state.images.length > 0;
  el.imageEmpty.textContent = state.project
    ? 'ยังไม่มีรูป กด “อัปโหลด” เพื่อเริ่ม'
    : 'สร้างหรือเลือก project ก่อน';

  state.images.forEach((img) => {
    const label = img.original_name || img.name;
    const li = document.createElement('li');
    li.className = 'image-item' + (img.name === state.currentImage ? ' active' : '');

    const open = document.createElement('button');
    open.type = 'button';
    open.className = 'image-open';
    open.textContent = label;
    open.title =
      `${label}\nไฟล์ที่เก็บ: ${img.name}\n` +
      `อัปโหลดโดย: ${img.uploaded_by_name || '-'}`;
    open.addEventListener('click', () => selectImage(img.name));
    li.appendChild(open);

    const badge = document.createElement('span');
    badge.className = 'badge';
    badge.textContent = String(img.label_count);
    badge.title = 'จำนวน bounding box';
    li.appendChild(badge);

    if (canDeleteCurrentImage(img)) {
      const del = document.createElement('button');
      del.type = 'button';
      del.className = 'icon-btn';
      del.textContent = '×';
      del.title = 'ย้ายรูปนี้ไปถังขยะ';
      del.addEventListener('click', (e) => {
        e.stopPropagation();
        deleteImage(img.name);
      });
      li.appendChild(del);
    }
    el.imageList.appendChild(li);
  });
}

const SUBMISSION_CLASS = {
  draft: 'status-draft',
  pending: 'status-pending',
  approved: 'status-approved',
  rejected: 'status-rejected',
};

// renderQueue lists the submissions a teacher is waiting on, or the student's
// own photos, so the same canvas can walk through a queue by clicking.
function renderQueue() {
  el.imageList.innerHTML = '';
  el.imageEmpty.hidden = state.queue.length > 0;
  el.imageEmpty.textContent = state.queue.length
    ? ''
    : state.submission && state.submission.can_review
      ? 'ไม่มีรูปรออนุมัติใน project นี้'
      : 'คุณยังไม่ได้ส่งรูปเข้า project นี้';

  state.queue.forEach((sub) => {
    const li = document.createElement('li');
    li.className = 'image-item' + (state.submission && state.submission.id === sub.id ? ' active' : '');

    const open = document.createElement('button');
    open.type = 'button';
    open.className = 'image-open';
    open.textContent = sub.original_name || sub.image_name;
    open.title =
      `${sub.original_name || ''}\nสถานะ: ${SUBMISSION_LABELS[sub.status] || sub.status}\n` +
      `นักศึกษา: ${sub.student_name || '-'}\nboxes: ${sub.box_count}` +
      (sub.note ? `\nโน้ต: ${sub.note}` : '') +
      (sub.review_note ? `\nเหตุผลที่ไม่ผ่าน: ${sub.review_note}` : '');
    open.addEventListener('click', () => {
      if (state.dirty && !confirm('มีการแก้ไขที่ยังไม่บันทึก ต้องการเปลี่ยนรูปหรือไม่?')) return;
      openSubmission(projectId(), sub.id);
    });
    li.appendChild(open);

    const chip = document.createElement('span');
    chip.className = 'status-pill ' + (SUBMISSION_CLASS[sub.status] || '');
    chip.textContent = String(sub.box_count);
    chip.title = SUBMISSION_LABELS[sub.status] || sub.status;
    li.appendChild(chip);

    el.imageList.appendChild(li);
  });
}

// loadQueue fills the queue for whoever is signed in. renderImages decides
// whether the sidebar shows the queue or the dataset, so this is also safe to
// call while the teacher is browsing images.
async function loadQueue() {
  if (!state.project) {
    state.queue = [];
    renderImages();
    return;
  }
  try {
    const data = await api(projectUrl('/submissions'));
    state.queue = (data.submissions || []).filter(
      (sub) => sub.status === 'pending' || sub.status === 'draft',
    );
  } catch (err) {
    state.queue = [];
    toast('โหลดคิวไม่สำเร็จ: ' + err.message, true);
  }
  renderImages();
}

function boxRect(box) {
  const W = el.canvas.width;
  const H = el.canvas.height;
  const w = box.width * W;
  const h = box.height * H;
  const x = box.x_center * W - w / 2;
  const y = box.y_center * H - h / 2;
  return { x, y, w, h, x2: x + w, y2: y + h };
}

function setBoxFromPixels(box, x1, y1, x2, y2) {
  const W = el.canvas.width;
  const H = el.canvas.height;
  const left = Math.min(x1, x2);
  const top = Math.min(y1, y2);
  const width = Math.abs(x2 - x1);
  const height = Math.abs(y2 - y1);
  box.x_center = (left + width / 2) / W;
  box.y_center = (top + height / 2) / H;
  box.width = width / W;
  box.height = height / H;
}

function renderCanvas() {
  const W = el.canvas.width;
  const H = el.canvas.height;
  if (!W || !H || !state.img) return;
  ctx.clearRect(0, 0, W, H);
  ctx.drawImage(state.img, 0, 0, W, H);

  const baseLine = Math.max(2, Math.round(W / 500));
  const fontSize = Math.max(13, Math.round(W / 55));

  state.boxes.forEach((box, index) => {
    const r = boxRect(box);
    const color = classColor(box.class_id);
    const selected = index === state.selected;

    ctx.lineWidth = selected ? baseLine + 1.5 : baseLine;
    ctx.strokeStyle = color;
    ctx.strokeRect(r.x, r.y, r.w, r.h);

    if (selected) {
      const pad = 3;
      ctx.setLineDash([8, 5]);
      ctx.lineWidth = Math.max(1, baseLine / 2);
      ctx.strokeStyle = 'rgba(255,255,255,0.9)';
      ctx.strokeRect(r.x - pad, r.y - pad, r.w + pad * 2, r.h + pad * 2);
      ctx.setLineDash([]);

      const handle = Math.max(7, Math.round(W / 140));
      ctx.fillStyle = color;
      ctx.fillRect(r.x2 - handle / 2, r.y2 - handle / 2, handle, handle);
      ctx.strokeStyle = 'rgba(255,255,255,0.9)';
      ctx.lineWidth = 1;
      ctx.strokeRect(r.x2 - handle / 2, r.y2 - handle / 2, handle, handle);
    }

    const name = state.classes[box.class_id];
    const label = `${box.class_id}: ${name === undefined ? '?' : name}`;
    ctx.font = `600 ${fontSize}px system-ui, -apple-system, "Segoe UI", sans-serif`;
    const textWidth = ctx.measureText(label).width;
    const labelHeight = fontSize + 8;
    const labelY = r.y - labelHeight >= 0 ? r.y - labelHeight : r.y;

    ctx.fillStyle = color;
    ctx.fillRect(r.x, labelY, textWidth + 10, labelHeight);
    ctx.fillStyle = '#fff';
    ctx.textBaseline = 'middle';
    ctx.fillText(label, r.x + 5, labelY + labelHeight / 2 + 1);
  });
}

function renderBoxList() {
  el.boxList.innerHTML = '';
  el.boxCount.textContent = String(state.boxes.length);

  if (state.boxes.length === 0) {
    const li = document.createElement('li');
    li.className = 'empty-note';
    li.textContent = 'ยังไม่มี bounding box';
    el.boxList.appendChild(li);
    return;
  }

  state.boxes.forEach((box, index) => {
    const li = document.createElement('li');
    li.className = 'box-item' + (index === state.selected ? ' active' : '');
    li.appendChild(colorDot(classColor(box.class_id)));

    const select = document.createElement('select');
    select.className = 'box-class';
    state.classes.forEach((name, id) => {
      const option = document.createElement('option');
      option.value = String(id);
      option.textContent = `${id}: ${name}`;
      select.appendChild(option);
    });
    select.value = String(box.class_id);
    select.addEventListener('change', () => {
      box.class_id = Number(select.value);
      markDirty();
      renderCanvas();
      renderBoxList();
    });
    li.appendChild(select);

    const coords = document.createElement('span');
    coords.className = 'box-coords';
    coords.textContent =
      `x ${box.x_center.toFixed(3)} · y ${box.y_center.toFixed(3)} · ` +
      `w ${box.width.toFixed(3)} · h ${box.height.toFixed(3)}`;
    li.appendChild(coords);

    const del = document.createElement('button');
    del.type = 'button';
    del.className = 'icon-btn';
    del.textContent = '×';
    del.title = 'ลบกรอบนี้';
    del.addEventListener('click', (e) => {
      e.stopPropagation();
      removeBox(index);
    });
    li.appendChild(del);

    li.addEventListener('click', (e) => {
      if (e.target === select || e.target === del) return;
      state.selected = index;
      renderCanvas();
      renderBoxList();
    });
    el.boxList.appendChild(li);
  });
}

// ---------- project actions ----------

// ---------- project actions ----------

// projectFromDetail rebuilds the sidebar's project object from an API answer.
// It lives in one place on purpose: dropping a field here used to break the
// owner checks in the UI, so both loaders must share it.
function projectFromDetail(data) {
  return {
    id: data.id,
    name: data.name,
    created_at: data.created_at,
    owner_id: data.owner_id,
    owner_username: data.owner_username,
    owner_name: data.owner_name,
    image_count: data.image_count,
    box_count: data.box_count,
    class_count: data.class_count,
    submissions_open: data.submissions_open,
    member_count: data.member_count,
    pending_count: data.pending_count,
    can_review: data.can_review,
    can_submit: data.can_submit,
  };
}

async function refreshProjects() {
  const data = await api('/api/projects');
  state.projects = data.projects || [];
  if (state.project) {
    const updated = state.projects.find((p) => p.id === state.project.id);
    if (updated) state.project = updated;
  }
  renderProjects();
  renderToolbar();
}

async function refreshProjectData() {
  if (!state.project) return;
  const id = state.project.id;
  const data = await api(`/api/projects/${encodeURIComponent(id)}`);
  if (!state.project || state.project.id !== id) return;

  state.project = projectFromDetail(data);
  state.classes = data.classes || [];
  state.images = data.images || [];

  if (state.activeClass >= state.classes.length) {
    state.activeClass = state.classes.length ? state.classes.length - 1 : -1;
  }
  if (state.activeClass < 0 && state.classes.length > 0) {
    state.activeClass = 0;
  }
  renderClasses();
  renderImages();
  renderToolbar();
}

function clearProject() {
  state.loadToken++;
  state.project = null;
  state.classes = [];
  state.images = [];
  state.activeClass = -1;
  state.currentImage = null;
  state.boxes = [];
  state.selected = -1;
  state.dirty = false;
  state.img = null;
  state.submission = null;
  state.queue = [];
  el.canvas.width = 0;
  el.canvas.height = 0;
  el.canvasWrap.classList.remove('has-image');
  el.placeholder.hidden = false;
  el.placeholder.textContent = state.projects.length
    ? 'เลือก project เพื่อเริ่มวาด'
    : 'สร้าง project ก่อนเพื่อเก็บรูปและ label';
  updateUrl('', '');
  renderProjects();
  renderClasses();
  renderImages();
  renderBoxList();
  renderToolbar();
}

async function createProject(name) {
  try {
    const info = await api('/api/projects', {
      method: 'POST',
      headers: JSON_HEADERS,
      body: jsonBody({ name }),
    });
    await refreshProjects();
    await selectProject(info.id, true);
    toast(`สร้าง project "${info.name}" แล้ว`);
  } catch (err) {
    toast('สร้าง project ไม่สำเร็จ: ' + err.message, true);
  }
}

async function renameProject(project) {
  if (!isProjectOwner(project)) return;
  const name = prompt('ชื่อ project ใหม่', project.name);
  if (name === null) return;
  const trimmed = name.trim();
  if (!trimmed || trimmed === project.name) return;
  try {
    const info = await api(`/api/projects/${encodeURIComponent(project.id)}`, {
      method: 'PATCH',
      headers: JSON_HEADERS,
      body: jsonBody({ name: trimmed }),
    });
    if (state.project && state.project.id === project.id) {
      state.project = { ...state.project, name: info.name };
    }
    await refreshProjects();
    toast('เปลี่ยนชื่อ project แล้ว');
  } catch (err) {
    toast('เปลี่ยนชื่อ project ไม่สำเร็จ: ' + err.message, true);
  }
}

async function deleteProject(project) {
  if (!isProjectOwner(project)) return;
  if (!confirm(`ลบ project "${project.name}" พร้อมรูปและ label ทั้งหมด?`)) return;
  const wasCurrent = state.project && state.project.id === project.id;
  try {
    await api(`/api/projects/${encodeURIComponent(project.id)}`, { method: 'DELETE' });
    if (wasCurrent) {
      state.project = null;
      state.dirty = false;
    }
    await refreshProjects();
    if (wasCurrent) {
      if (state.projects.length > 0) {
        await selectProject(state.projects[0].id, true);
      } else {
        clearProject();
      }
    }
    toast('ลบ project แล้ว');
  } catch (err) {
    toast('ลบ project ไม่สำเร็จ: ' + err.message, true);
  }
}

async function selectProject(id, force = false, imageName = '') {
  if (!force && state.project && state.project.id === id) return;
  if (!force && state.dirty && !confirm('มีการแก้ไขที่ยังไม่บันทึก ต้องการเปลี่ยน project หรือไม่?')) {
    return;
  }
  const project = state.projects.find((p) => p.id === id);
  if (!project) return;

  const token = ++state.loadToken;
  state.project = project;
  state.classes = [];
  state.images = [];
  state.activeClass = -1;
  state.currentImage = null;
  state.boxes = [];
  state.selected = -1;
  state.dirty = false;
  state.img = null;
  state.submission = null;
  state.queue = [];
  localStorage.setItem('lastProject', id);
  updateUrl(id, '');

  el.canvas.width = 0;
  el.canvas.height = 0;
  el.canvasWrap.classList.remove('has-image');
  el.placeholder.hidden = false;
  el.placeholder.textContent = 'กำลังโหลด project...';
  renderProjects();
  renderClasses();
  renderImages();
  renderBoxList();
  renderToolbar();

  try {
    const data = await api(`/api/projects/${encodeURIComponent(id)}`);
    if (token !== state.loadToken) return;

    state.project = projectFromDetail(data);
    state.classes = data.classes || [];
    state.images = data.images || [];
    state.activeClass = state.classes.length > 0 ? 0 : -1;

    renderProjects();
    renderClasses();
    renderImages();
    renderToolbar();
    await loadQueue();

    if (state.images.length > 0) {
      const wanted =
        imageName && state.images.some((img) => img.name === imageName)
          ? imageName
          : state.images[0].name;
      await selectImage(wanted, true);
    } else {
      el.placeholder.hidden = false;
      el.placeholder.textContent = 'อัปโหลดรูปเพื่อเริ่มวาด';
    }
  } catch (err) {
    if (token !== state.loadToken) return;
    toast('โหลด project ไม่สำเร็จ: ' + err.message, true);
    state.project = null;
    clearProject();
  }
}

// ---------- class actions ----------

function selectClass(index) {
  state.activeClass = index;
  renderClasses();
  renderToolbar();
}

async function addClass(name) {
  if (!state.project) return;
  if (!isProjectOwner(state.project)) {
    toast('เฉพาะเจ้าของ project เท่านั้นที่เพิ่ม class ได้', true);
    return;
  }
  try {
    const data = await api(projectUrl('/classes'), {
      method: 'POST',
      headers: JSON_HEADERS,
      body: jsonBody({ name }),
    });
    await refreshProjectData();
    state.activeClass = (data.classes || []).length - 1;
    renderClasses();
    renderToolbar();
    toast(`เพิ่ม class "${name.trim()}" แล้ว`);
  } catch (err) {
    toast('เพิ่ม class ไม่สำเร็จ: ' + err.message, true);
  }
}

async function renameClass(index, value) {
  if (!state.project) return;
  if (!isProjectOwner(state.project)) {
    toast('เฉพาะเจ้าของ project เท่านั้นที่แก้ไข class ได้', true);
    renderClasses();
    return;
  }
  const name = value.trim();
  if (!name) {
    toast('ชื่อ class ต้องไม่ว่าง', true);
    renderClasses();
    return;
  }
  if (name === state.classes[index]) {
    renderClasses();
    return;
  }
  const next = [...state.classes];
  next[index] = name;
  try {
    await api(projectUrl('/classes'), {
      method: 'PUT',
      headers: JSON_HEADERS,
      body: jsonBody({ classes: next }),
    });
    await refreshProjectData();
    toast('เปลี่ยนชื่อ class แล้ว');
  } catch (err) {
    toast('เปลี่ยนชื่อไม่สำเร็จ: ' + err.message, true);
    renderClasses();
  }
}

async function deleteClass(index) {
  if (!state.project) return;
  if (!isProjectOwner(state.project)) {
    toast('เฉพาะเจ้าของ project เท่านั้นที่ลบ class ได้', true);
    return;
  }
  const name = state.classes[index];
  if (!confirm(`ลบ class "${name}" ?`)) return;
  const next = state.classes.filter((_, i) => i !== index);
  try {
    await api(projectUrl('/classes'), {
      method: 'PUT',
      headers: JSON_HEADERS,
      body: jsonBody({ classes: next }),
    });
    await refreshProjectData();
    toast('ลบ class แล้ว');
  } catch (err) {
    toast('ลบ class ไม่สำเร็จ: ' + err.message, true);
    renderClasses();
  }
}

// ---------- image + label actions ----------

async function deleteImage(name) {
  if (!state.project) return;
  const id = projectId();
  if (!confirm(`ย้ายรูป "${name}" ไปถังขยะ? (กู้คืนได้ในหน้า project)`)) return;
  try {
    await api(projectUrl(`/images/${encodeURIComponent(name)}`), { method: 'DELETE' });
    if (state.project && state.project.id === id && state.currentImage === name) {
      clearCanvas();
    }
    await refreshProjectData();
    toast('ย้ายรูปไปถังขยะแล้ว');
  } catch (err) {
    toast('ย้ายไปถังขยะไม่สำเร็จ: ' + err.message, true);
  }
}

function clearCanvas() {
  state.img = null;
  state.boxes = [];
  state.selected = -1;
  state.dirty = false;
  state.currentImage = null;
  el.canvas.width = 0;
  el.canvas.height = 0;
  el.canvasWrap.classList.remove('has-image');
  el.placeholder.hidden = false;
  el.placeholder.textContent = state.project
    ? 'อัปโหลดหรือเลือกรูปเพื่อเริ่มวาด'
    : 'สร้างหรือเลือก project ก่อน';
  if (!state.submission) {
    updateUrl(projectId(), '');
  }
  renderImages();
  renderBoxList();
  renderToolbar();
}

// loadImage takes a full URL so project images and staged submissions can share
// the canvas code.
function loadImage(url) {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => resolve(img);
    img.onerror = () => reject(new Error('โหลดรูปไม่สำเร็จ'));
    img.src = url;
  });
}

function imageURL(name) {
  return projectUrl(`/images/${encodeURIComponent(name)}`);
}

async function selectImage(name, force = false) {
  if (!state.project) return;
  if (name === state.currentImage && !state.submission) return;
  if (!force && state.dirty && !confirm('มีการแก้ไขที่ยังไม่บันทึก ต้องการเปลี่ยนรูปหรือไม่?')) {
    return;
  }
  // Switching to a dataset image leaves review mode behind.
  state.submission = null;
  renderSubmissionBar();

  const pid = projectId();
  const token = ++state.loadToken;
  state.currentImage = name;
  state.dirty = false;
  state.selected = -1;
  state.boxes = [];
  state.img = null;
  updateUrl(pid, name);
  el.canvasWrap.classList.remove('has-image');
  el.placeholder.hidden = false;
  el.placeholder.textContent = 'กำลังโหลดรูป...';
  renderImages();
  renderBoxList();
  renderToolbar();

  try {
    const img = await loadImage(imageURL(name));
    if (token !== state.loadToken || projectId() !== pid) return;
    state.img = img;
    el.canvas.width = img.naturalWidth;
    el.canvas.height = img.naturalHeight;
    el.canvasWrap.classList.add('has-image');
    el.placeholder.hidden = true;

    const data = await api(projectUrl(`/images/${encodeURIComponent(name)}/labels`));
    if (token !== state.loadToken || projectId() !== pid) return;
    state.boxes = (data.labels || []).map((l) => ({
      class_id: l.class_id,
      x_center: l.x_center,
      y_center: l.y_center,
      width: l.width,
      height: l.height,
    }));
    state.selected = -1;
    renderBoxList();
    renderCanvas();
    renderToolbar();
  } catch (err) {
    if (token !== state.loadToken || projectId() !== pid) return;
    toast('โหลดรูปไม่สำเร็จ: ' + err.message, true);
    clearCanvas();
  }
}

// saveLabels stores boxes either on a dataset image (teacher) or on a pending
// submission (student draft/pending). It returns true when the server accepted
// the boxes, which the submit action relies on.
async function saveLabels(quiet = false) {
  if (saving) return false;
  if (inSubmissionMode()) {
    if (!isSubmissionEditable()) {
      toast('รูปนี้ตรวจแล้ว แก้ไม่ได้', true);
      return false;
    }
  } else if (!state.project || !state.currentImage) {
    return false;
  }

  const pid = projectId();
  const name = state.currentImage;
  const subID = state.submission ? state.submission.id : '';
  saving = true;
  renderToolbar();
  el.saveBtn.textContent = 'กำลังบันทึก...';

  const payload = {
    labels: state.boxes.map((b) => ({
      class_id: b.class_id,
      x_center: b.x_center,
      y_center: b.y_center,
      width: b.width,
      height: b.height,
    })),
  };

  try {
    const url = subID
      ? submissionUrl('/labels')
      : projectUrl(`/images/${encodeURIComponent(name)}/labels`);
    await api(url, {
      method: 'PUT',
      headers: JSON_HEADERS,
      body: jsonBody(payload),
    });
    if (subID) {
      if (state.submission && state.submission.id === subID) {
        state.dirty = false;
      }
    } else if (state.project && state.project.id === pid && state.currentImage === name) {
      state.dirty = false;
    }
    if (!quiet) {
      toast(subID ? 'บันทึกกรอบของ submission แล้ว' : 'บันทึก labels แล้ว');
    }
    if (!subID && !quiet) {
      await refreshProjectData();
    }
    return true;
  } catch (err) {
    toast('บันทึกไม่สำเร็จ: ' + err.message, true);
    return false;
  } finally {
    saving = false;
    el.saveBtn.textContent = 'บันทึก';
    renderToolbar();
  }
}

function removeBox(index) {
  if (index < 0 || index >= state.boxes.length) return;
  state.boxes.splice(index, 1);
  if (state.selected === index) {
    state.selected = -1;
  } else if (state.selected > index) {
    state.selected--;
  }
  markDirty();
  renderBoxList();
  renderCanvas();
}

// ---------- submission review mode ----------

const SUBMISSION_LABELS = {
  draft: 'ฉบับร่าง',
  pending: 'รออนุมัติ',
  approved: 'อนุมัติแล้ว',
  rejected: 'ไม่ผ่าน',
};

// openSubmission loads a student photo for annotation (author) or review
// (teacher). The boxes live on the submission row until the teacher decides.
async function openSubmission(projectID, sid, force = false) {
  if (!force && state.submission && state.submission.id === sid) return;
  if (!force && state.dirty && !confirm('มีการแก้ไขที่ยังไม่บันทึก ต้องการเปลี่ยนรูปหรือไม่?')) {
    return;
  }

  const token = ++state.loadToken;
  state.submission = null;
  state.currentImage = null;
  state.boxes = [];
  state.selected = -1;
  state.dirty = false;
  state.img = null;
  updateUrl(projectID, '', sid);
  el.canvasWrap.classList.remove('has-image');
  el.placeholder.hidden = false;
  el.placeholder.textContent = 'กำลังโหลดรูปที่ต้องตรวจ...';
  renderSubmissionBar();
  renderToolbar();
  renderImages();

  try {
    const detail = await api(`/api/projects/${encodeURIComponent(projectID)}/submissions/${encodeURIComponent(sid)}`);
    if (token !== state.loadToken) return;
    const sub = detail.submission || {};
    state.submission = {
      id: sub.id,
      status: sub.status,
      note: sub.note || '',
      original_name: sub.original_name || sub.image_name,
      student_name: sub.student_name || '',
      submitted_at: sub.submitted_at,
      review_note: sub.review_note || '',
      reviewer_name: sub.reviewer_name || '',
      can_edit: Boolean(detail.can_edit),
      can_review: Boolean(detail.can_review),
    };

    const img = await loadImage(submissionUrl('/image'));
    if (token !== state.loadToken) return;
    state.img = img;
    el.canvas.width = img.naturalWidth;
    el.canvas.height = img.naturalHeight;
    el.canvasWrap.classList.add('has-image');
    el.placeholder.hidden = true;
    state.boxes = (sub.labels || []).map((l) => ({ ...l }));
    renderCanvas();
    renderBoxList();
    renderSubmissionBar();
    renderToolbar();
    await loadQueue();
  } catch (err) {
    if (token !== state.loadToken) return;
    toast('เปิด submission ไม่สำเร็จ: ' + err.message, true);
    clearCanvas();
    renderSubmissionBar();
  }
}

// leaveSubmissionMode returns the canvas to normal project images.
function leaveSubmissionMode() {
  if (!state.submission) return;
  state.submission = null;
  updateUrl(projectId(), '');
  el.canvasWrap.classList.remove('has-image');
  el.placeholder.hidden = false;
  el.placeholder.textContent = 'เลือกรูปจาก list ทางซ้าย';
  renderSubmissionBar();
  renderImages();
}

function renderSubmissionBar() {
  const sub = state.submission;
  el.submissionBar.hidden = !sub;
  if (!sub) {
    el.submissionSubmit.hidden = true;
    el.submissionWithdraw.hidden = true;
    el.submissionApprove.hidden = true;
    el.submissionReject.hidden = true;
    return;
  }

  const label = SUBMISSION_LABELS[sub.status] || sub.status;
  el.submissionStatus.textContent = label;
  el.submissionStatus.className = 'chip status-' + sub.status;

  const bits = [sub.original_name];
  if (sub.student_name) bits.push('นักศึกษา: ' + sub.student_name);
  if (sub.note) bits.push('โน้ต: ' + sub.note);
  if (sub.status === 'rejected') bits.push('เหตุผล: ' + (sub.review_note || '-'));
  el.submissionMeta.textContent = bits.join(' · ');

  const authorSide = !sub.can_review;
  const canSend = authorSide && sub.can_edit && (sub.status === 'draft' || sub.status === 'rejected');
  el.submissionSubmit.hidden = !canSend;
  el.submissionSubmit.textContent = sub.status === 'rejected' ? 'ส่งใหม่อีกครั้ง' : 'ส่งอนุมัติ';
  el.submissionWithdraw.hidden = !(authorSide && sub.status === 'pending');
  el.submissionApprove.hidden = !(sub.can_review && sub.status === 'pending');
  el.submissionReject.hidden = !(sub.can_review && sub.status === 'pending');

  el.submissionBack.href = sub.can_review
    ? `/pending.html?project=${encodeURIComponent(projectId())}`
    : '/mywork.html';
  el.submissionBack.textContent = sub.can_review ? '← กลับไปคิวตรวจ' : '← กลับไปงานของฉัน';

  if (!sub.can_edit && sub.status === 'approved') {
    el.placeholder.textContent = 'รูปนี้ตรวจแล้ว อ่านอย่างเดียว';
  }
}

async function submissionAction(kind) {
  const sub = state.submission;
  if (!sub) return;

  if (kind === 'reject') {
    const note = prompt('เหตุผลที่ไม่ผ่าน (นักศึกษาจะเห็นข้อความนี้)', '');
    if (note === null) return;
    if (!note.trim()) {
      toast('ต้องระบุเหตุผลเมื่อไม่ผ่าน', true);
      return;
    }
    await runSubmission(kind, note.trim());
    return;
  }
  if (kind === 'approve') {
    if (!confirm(`อนุมัติ "${sub.original_name}" เข้า dataset ของ project นี้?`)) return;
    await runSubmission(kind, '');
    return;
  }

  // Draft edits must be on the server before the photo can be sent.
  if (kind === 'submit' && state.dirty) {
    const saved = await saveLabels(true);
    if (!saved) return;
  }
  await runSubmission(kind, '');
}

async function runSubmission(kind, note) {
  const sub = state.submission;
  try {
    await api(submissionUrl('/' + kind), {
      method: 'POST',
      headers: note === undefined ? undefined : JSON_HEADERS,
      body: note === undefined ? undefined : jsonBody({ note }),
    });
    const messages = {
      submit: 'ส่งให้อาจารย์ตรวจแล้ว',
      withdraw: 'ดึงกลับมาเป็นฉบับร่างแล้ว',
      approve: 'อนุมัติแล้ว รูปเข้า project พร้อมกรอบที่วาดไว้',
      reject: 'ไม่ผ่าน รูปและกรอบถูกย้ายไปถังขยะของ project',
    };
    toast(messages[kind] || 'เรียบร้อย');
    if (kind === 'approve' || kind === 'reject') {
      await refreshProjectData();
      leaveSubmissionMode();
      if (state.project) {
        await loadQueue();
      }
      return;
    }
    await openSubmission(projectId(), sub.id, true);
  } catch (err) {
    toast('ทำไม่สำเร็จ: ' + err.message, true);
  }
}

// ---------- canvas interaction ----------

function eventPoint(e) {
  const rect = el.canvas.getBoundingClientRect();
  if (!rect.width || !rect.height) return { x: 0, y: 0 };
  return {
    x: (e.clientX - rect.left) * (el.canvas.width / rect.width),
    y: (e.clientY - rect.top) * (el.canvas.height / rect.height),
  };
}

function hitTest(p) {
  for (let i = state.boxes.length - 1; i >= 0; i--) {
    const r = boxRect(state.boxes[i]);
    if (p.x >= r.x && p.x <= r.x2 && p.y >= r.y && p.y <= r.y2) {
      return i;
    }
  }
  return -1;
}

function onPointerDown(e) {
  if (!state.project) {
    toast('สร้างหรือเลือก project ก่อน', true);
    return;
  }
  if (inSubmissionMode() && !isSubmissionEditable()) {
    toast('รูปนี้ถูกตรวจไปแล้ว จึงแก้กรอบไม่ได้', true);
    return;
  }
  if (!state.img || e.button !== 0) return;
  if (state.activeClass < 0) {
    toast('เพิ่มและเลือก class ก่อนวาดกรอบ', true);
    return;
  }

  const p = eventPoint(e);
  const hit = hitTest(p);
  const rect = el.canvas.getBoundingClientRect();
  const scale = rect.width ? el.canvas.width / rect.width : 1;
  const handlePad = 10 * scale;

  if (hit >= 0) {
    state.selected = hit;
    const r = boxRect(state.boxes[hit]);
    const nearHandle =
      Math.abs(p.x - r.x2) <= handlePad && Math.abs(p.y - r.y2) <= handlePad;
    mode = nearHandle ? 'resize' : 'move';
    dragStart = p;
    dragOriginal = { ...state.boxes[hit] };
    dragMoved = false;
    renderCanvas();
    renderBoxList();
    e.preventDefault();
    return;
  }

  mode = 'draw';
  dragStart = p;
  dragMoved = false;
  state.boxes.push({
    class_id: state.activeClass,
    x_center: p.x / el.canvas.width,
    y_center: p.y / el.canvas.height,
    width: 0,
    height: 0,
  });
  state.selected = state.boxes.length - 1;
  renderCanvas();
  renderBoxList();
  e.preventDefault();
}

function onPointerMove(e) {
  if (!mode) return;
  if (e.buttons === 0) {
    onPointerUp();
    return;
  }
  e.preventDefault();
  const p = eventPoint(e);
  const W = el.canvas.width;
  const H = el.canvas.height;
  const box = state.boxes[state.selected];
  if (!box) return;

  if (mode === 'draw') {
    const x1 = clamp(dragStart.x, 0, W);
    const y1 = clamp(dragStart.y, 0, H);
    const x2 = clamp(p.x, 0, W);
    const y2 = clamp(p.y, 0, H);
    setBoxFromPixels(box, x1, y1, x2, y2);
  } else if (mode === 'move') {
    const dx = (p.x - dragStart.x) / W;
    const dy = (p.y - dragStart.y) / H;
    if (Math.abs(p.x - dragStart.x) > 0.5 || Math.abs(p.y - dragStart.y) > 0.5) {
      dragMoved = true;
    }
    box.x_center = clamp(dragOriginal.x_center + dx, box.width / 2, 1 - box.width / 2);
    box.y_center = clamp(dragOriginal.y_center + dy, box.height / 2, 1 - box.height / 2);
  } else if (mode === 'resize') {
    dragMoved = true;
    const left = (dragOriginal.x_center - dragOriginal.width / 2) * W;
    const top = (dragOriginal.y_center - dragOriginal.height / 2) * H;
    const minSize = 3;
    const x2 = clamp(p.x, left + minSize, W);
    const y2 = clamp(p.y, top + minSize, H);
    setBoxFromPixels(box, left, top, x2, y2);
  }
  renderCanvas();
}

function onPointerUp() {
  if (!mode) return;
  const box = state.boxes[state.selected];

  if (mode === 'draw') {
    if (!box || box.width * el.canvas.width < 4 || box.height * el.canvas.height < 4) {
      if (state.selected >= 0) {
        state.boxes.splice(state.selected, 1);
      }
      state.selected = -1;
    } else {
      markDirty();
    }
  } else if (dragMoved) {
    markDirty();
  }

  mode = null;
  dragStart = null;
  dragOriginal = null;
  dragMoved = false;
  renderBoxList();
  renderCanvas();
  renderToolbar();
}

// ---------- events ----------

el.projectForm.addEventListener('submit', (e) => {
  e.preventDefault();
  const name = el.projectName.value.trim();
  if (!name) return;
  el.projectName.value = '';
  createProject(name);
});

el.classForm.addEventListener('submit', (e) => {
  e.preventDefault();
  const name = el.className.value.trim();
  if (!name) return;
  el.className.value = '';
  addClass(name);
});

el.imageInput.addEventListener('change', async () => {
  if (!state.project) return;
  const files = Array.from(el.imageInput.files || []);
  if (files.length === 0) return;
  const form = new FormData();
  files.forEach((file) => form.append('files', file));

  try {
    const data = await api(projectUrl('/images'), { method: 'POST', body: form });
    await refreshProjectData();
    const saved = data && data.saved ? data.saved : [];
    if (saved.length > 0 && state.project) {
      await selectImage(saved[0]);
    }
    toast(`อัปโหลด ${saved.length} รูปแล้ว`);
  } catch (err) {
    toast('อัปโหลดไม่สำเร็จ: ' + err.message, true);
  } finally {
    el.imageInput.value = '';
  }
});

el.saveBtn.addEventListener('click', saveLabels);

el.clearBoxes.addEventListener('click', () => {
  if (state.boxes.length === 0) return;
  if (!confirm('ล้าง bounding box ทั้งหมดของรูปนี้?')) return;
  state.boxes = [];
  state.selected = -1;
  markDirty();
  renderBoxList();
  renderCanvas();
});

el.canvas.addEventListener('pointerdown', onPointerDown);
window.addEventListener('pointermove', onPointerMove);
window.addEventListener('pointerup', onPointerUp);

window.addEventListener('keydown', (e) => {
  const tag = e.target && e.target.tagName;
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;

  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
    e.preventDefault();
    if (state.dirty) saveLabels();
    return;
  }
  if ((e.key === 'Delete' || e.key === 'Backspace') && state.selected >= 0) {
    e.preventDefault();
    removeBox(state.selected);
    return;
  }
  if (e.key === 'Escape') {
    state.selected = -1;
    renderBoxList();
    renderCanvas();
  }
});

window.addEventListener('beforeunload', (e) => {
  if (!state.dirty) return;
  e.preventDefault();
  e.returnValue = '';
});

// ---------- boot ----------

function wireSubmissionBar() {
  el.submissionSubmit.addEventListener('click', () => submissionAction('submit'));
  el.submissionWithdraw.addEventListener('click', () => submissionAction('withdraw'));
  el.submissionApprove.addEventListener('click', () => submissionAction('approve'));
  el.submissionReject.addEventListener('click', () => submissionAction('reject'));
}

// boot resolves the session first, because what this page may do depends on
// the role: teachers edit dataset images and review, students draw on their own
// submissions.
(async function boot() {
  try {
    state.currentUser = await fetchMe();
    if (!state.currentUser) return;
    renderIdentity(state.currentUser, el);
    wireSubmissionBar();

    await refreshProjects();
    const params = new URLSearchParams(window.location.search);
    const wantedProject = params.get('project') || '';
    const wantedImage = params.get('image') || '';
    const wantedSubmission = params.get('submission') || '';
    const last = localStorage.getItem('lastProject');
    const project =
      state.projects.find((p) => p.id === wantedProject) ||
      state.projects.find((p) => p.id === last) ||
      state.projects[0];

    if (!project) {
      clearProject();
      el.placeholder.textContent = isStudent(state.currentUser)
        ? 'ยังไม่มี project ที่เปิดรับรูปจากคุณ ให้อาจารย์เพิ่มคุณเข้า project ก่อน'
        : 'ยังไม่มี project สร้างอันแรกเพื่อเก็บรูปและ label ไว้ด้วยกัน';
      return;
    }

    await selectProject(project.id, true, wantedSubmission ? '' : wantedImage);
    if (wantedSubmission) {
      await openSubmission(project.id, wantedSubmission, true);
      return;
    }
    if (isStudent(state.currentUser)) {
      // Students only ever draw on submissions, and every entry point passes a
      // submission id. Without one, their own page is the right destination.
      window.location.replace('/mywork.html');
    }
  } catch (err) {
    toast('โหลดข้อมูลไม่สำเร็จ: ' + err.message, true);
    clearProject();
  }
})();
