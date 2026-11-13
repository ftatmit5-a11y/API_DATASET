'use strict';

const state = {
  id: '',
  project: null,
  currentUser: null,
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
  classChips: document.getElementById('class-chips'),
  grid: document.getElementById('image-grid'),
  empty: document.getElementById('image-empty'),
  trashLink: document.getElementById('project-trash-link'),
  trashCount: document.getElementById('project-trash-count'),
  drop: document.getElementById('drop-zone'),
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

  el.classChips.innerHTML = '';
  const classes = p.classes || [];
  if (classes.length === 0) {
    const note = document.createElement('span');
    note.className = 'empty-inline';
    note.textContent = 'ยังไม่มี class — เปิดรูปใดรูปหนึ่งเพื่อเพิ่ม class ก่อนวาด';
    el.classChips.appendChild(note);
  } else {
    classes.forEach((name, index) => {
      const chip = document.createElement('span');
      chip.className = 'chip class-chip';
      chip.textContent = `${index}: ${name}`;
      el.classChips.appendChild(chip);
    });
  }

  const hasImages = p.image_count > 0;
  el.makeData.disabled = !hasImages || state.exporting;
  el.makeData.title = hasImages
    ? 'รวมรูปและ label ทั้งหมดของ project นี้เป็น ZIP'
    : 'เพิ่มรูปก่อนจึงจะทำ data ได้';
}

function renderImages() {
  const images = (state.project && state.project.images) || [];
  el.empty.hidden = images.length > 0;
  el.grid.innerHTML = '';

  for (const image of images) {
    const card = document.createElement('article');
    card.className = 'thumb-card';

    const link = document.createElement('a');
    link.className = 'thumb-link';
    link.href = annotateUrl(image.name);
    link.title = `เปิด ${image.name} เพื่อวาด bounding box`;

    const img = document.createElement('img');
    img.className = 'thumb-img';
    img.src = imageUrl(image.name);
    img.alt = image.name;
    img.loading = 'lazy';
    link.appendChild(img);

    const overlay = document.createElement('span');
    overlay.className = 'thumb-overlay';
    overlay.textContent = 'วาด label';
    link.appendChild(overlay);

    card.appendChild(link);

    const meta = document.createElement('div');
    meta.className = 'thumb-meta';

    const name = document.createElement('span');
    name.className = 'thumb-name';
    name.textContent = image.original_name || image.name;
    name.title = image.original_name
      ? `${image.original_name}\nไฟล์ที่เก็บ: ${image.name}`
      : image.name;
    meta.appendChild(name);

    const badge = document.createElement('span');
    badge.className = 'badge';
    badge.textContent = String(image.label_count);
    badge.title = `${image.label_count} bounding box`;
    meta.appendChild(badge);

    if (canDeleteImage(image)) {
      const del = document.createElement('button');
      del.type = 'button';
      del.className = 'icon-btn';
      del.textContent = '×';
      del.title = 'ย้ายรูปนี้ไปถังขยะ';
      del.addEventListener('click', () => deleteImage(image));
      meta.appendChild(del);
    }

    card.appendChild(meta);

    const uploader = document.createElement('p');
    uploader.className = 'thumb-uploader';
    uploader.textContent = image.uploaded_by_name
      ? `อัปโหลดโดย ${image.uploaded_by_name}`
      : 'ไม่ทราบผู้อัปโหลด';
    uploader.title = image.created_at
      ? new Date(image.created_at).toLocaleString('th-TH', {
          dateStyle: 'medium',
          timeStyle: 'short',
        })
      : '';
    card.appendChild(uploader);

    el.grid.appendChild(card);
  }
}

function renderTrashLink() {
  const trash = (state.project && state.project.trash) || [];
  el.trashCount.textContent = String(trash.length);
  el.trashLink.href = `trash.html?project=${encodeURIComponent(state.id)}`;
}

async function load() {
  try {
    state.project = await api(base());
    document.body.classList.remove('load-error');
    renderHeader();
    renderImages();
    renderTrashLink();
  } catch (err) {
    document.body.classList.add('load-error');
    el.name.textContent = 'ไม่พบ project';
    el.subtitle.textContent = err.message;
    el.makeData.disabled = true;
    el.classChips.innerHTML = '';
    el.grid.innerHTML = '';
    el.empty.hidden = true;
    el.trashCount.textContent = '0';
    toast('โหลด project ไม่สำเร็จ: ' + err.message, true);
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
    const hasClasses = state.project && state.project.class_count > 0;
    toast(
      hasClasses
        ? `อัปโหลด ${list.length} รูปแล้ว`
        : `อัปโหลด ${list.length} รูปแล้ว — เปิดรูปเพื่อเพิ่ม class ก่อนวาด`,
    );
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

el.name.addEventListener('dblclick', renameProject);
el.name.title = 'ดับเบิลคลิกเพื่อเปลี่ยนชื่อ project (เฉพาะเจ้าของ)';

el.drop.addEventListener('dragenter', (e) => {
  e.preventDefault();
  el.drop.classList.add('dragover');
});
el.drop.addEventListener('dragover', (e) => {
  e.preventDefault();
  el.drop.classList.add('dragover');
});
el.drop.addEventListener('dragleave', () => {
  el.drop.classList.remove('dragover');
});
el.drop.addEventListener('drop', (e) => {
  e.preventDefault();
  el.drop.classList.remove('dragover');
  uploadFiles(Array.from(e.dataTransfer.files || []));
});

window.addEventListener('dragover', (e) => e.preventDefault());
window.addEventListener('drop', (e) => e.preventDefault());

const params = new URLSearchParams(window.location.search);
state.id = params.get('id') || '';

if (!state.id) {
  toast('ไม่พบ project ที่เลือก', true);
  el.makeData.disabled = true;
  el.input.disabled = true;
} else {
  (async function boot() {
    state.currentUser = await initUserSelect(el.userSelect);
    await load();
  })();
}
