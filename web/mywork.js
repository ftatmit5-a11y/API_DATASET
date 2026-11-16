'use strict';

const STATUS = {
  draft: { label: 'ฉบับร่าง', kind: 'status-draft' },
  pending: { label: 'รออนุมัติ', kind: 'status-pending' },
  approved: { label: 'อนุมัติแล้ว', kind: 'status-approved' },
  rejected: { label: 'ไม่ผ่าน', kind: 'status-rejected' },
};

const state = {
  user: null,
  groups: [],
  uploading: false,
};

const el = {
  total: document.getElementById('work-total'),
  counts: {
    draft: document.getElementById('count-draft'),
    pending: document.getElementById('count-pending'),
    approved: document.getElementById('count-approved'),
    rejected: document.getElementById('count-rejected'),
  },
  refresh: document.getElementById('refresh-btn'),
  groups: document.getElementById('work-groups'),
  empty: document.getElementById('work-empty'),
  toast: document.getElementById('toast'),
  identity: document.getElementById('identity'),
  logout: document.getElementById('logout-btn'),
};

let toastTimer = 0;

function toast(text, isError = false) {
  el.toast.textContent = text;
  el.toast.className = 'toast show' + (isError ? ' error' : '');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    el.toast.className = 'toast';
  }, 3400);
}

function base(projectID) {
  return `/api/projects/${encodeURIComponent(projectID)}`;
}

function submissionImageURL(projectID, sub) {
  return `${base(projectID)}/submissions/${encodeURIComponent(sub.id)}/image`;
}

function drawURL(projectID, sub) {
  return `annotate.html?project=${encodeURIComponent(projectID)}&submission=${encodeURIComponent(sub.id)}`;
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

function renderUploadPanel(group) {
  const panel = document.createElement('div');
  panel.className = 'work-upload';

  if (!group.project.submissions_open) {
    panel.appendChild(
      mkEl('p', 'hint', 'project นี้ยังไม่เปิดรับรูป — รออาจารย์เปิด “รับรูปจากนักศึกษา”'),
    );
    return panel;
  }

  const row = document.createElement('div');
  row.className = 'work-upload-row';

  const input = document.createElement('input');
  input.type = 'file';
  input.accept = 'image/*';
  input.multiple = true;
  input.id = 'upload-' + group.project.id;
  input.hidden = true;

  const pick = mkEl('label', 'upload-btn');
  pick.textContent = '+ เลือกรูปส่ง';
  pick.htmlFor = input.id;
  pick.appendChild(input);

  const note = document.createElement('input');
  note.type = 'text';
  note.maxLength = 500;
  note.placeholder = 'โน้ตถึงอาจารย์ (ไม่บังคับ) เช่น ปลาที่ถ่ายเมื่อเช้า';

  const send = makeButton('อัปโหลด', 'primary-btn', () => {
    upload(group, Array.from(input.files || []), note.value);
  });

  row.appendChild(pick);
  row.appendChild(note);
  row.appendChild(send);
  panel.appendChild(row);

  const drop = document.createElement('div');
  drop.className = 'drop-zone work-drop';
  drop.innerHTML = '';
  drop.appendChild(mkEl('strong', null, 'ลากไฟล์รูปมาวางที่นี่'));
  drop.appendChild(mkEl('span', null, `class ที่ใช้อนุญาต: ${(group.classes || []).join(', ') || 'ยังไม่มี'}`));

  ['dragenter', 'dragover'].forEach((event) =>
    drop.addEventListener(event, (e) => {
      e.preventDefault();
      drop.classList.add('dragover');
    }),
  );
  drop.addEventListener('dragleave', () => drop.classList.remove('dragover'));
  drop.addEventListener('drop', (e) => {
    e.preventDefault();
    drop.classList.remove('dragover');
    upload(group, Array.from(e.dataTransfer.files || []), note.value);
  });
  panel.appendChild(drop);

  return panel;
}

const IMAGE_NAME = /\.(jpe?g|png|bmp|webp|tiff?|gif)$/i;

async function upload(group, files, note) {
  const list = files.filter((f) => f && (f.type.startsWith('image/') || IMAGE_NAME.test(f.name)));
  if (list.length === 0) {
    toast('เลือกไฟล์รูปก่อน', true);
    return;
  }
  if (state.uploading) return;

  const form = new FormData();
  list.forEach((f) => form.append('files', f));
  if (note && note.trim()) form.append('note', note.trim());

  state.uploading = true;
  toast(`กำลังส่ง ${list.length} รูปเข้า ${group.project.name}...`);
  try {
    await api(`${base(group.project.id)}/submissions`, { method: 'POST', body: form });
    await load();
    toast('อัปโหลดแล้ว — กด “วาดกรอบ” เพื่อทำ label แล้วค่อยกดส่งอนุมัติ');
  } catch (err) {
    toast('ส่งรูปไม่สำเร็จ: ' + err.message, true);
  } finally {
    state.uploading = false;
  }
}

function renderCard(projectID, sub, classes) {
  const card = document.createElement('article');
  card.className = 'work-card';

  const figure = document.createElement('a');
  figure.className = 'work-figure';
  figure.href = drawURL(projectID, sub);
  figure.title = 'เปิดเพื่อวาด/แก้ bounding box';

  const img = document.createElement('img');
  img.className = 'work-img';
  img.src = submissionImageURL(projectID, sub);
  img.alt = sub.original_name || sub.image_name;
  img.loading = 'lazy';
  figure.appendChild(img);
  figure.appendChild(mkEl('span', 'work-status ' + (STATUS[sub.status] || STATUS.draft).kind,
    (STATUS[sub.status] || STATUS.draft).label));
  card.appendChild(figure);

  const body = document.createElement('div');
  body.className = 'work-body';

  body.appendChild(mkEl('span', 'review-name', sub.original_name || sub.image_name));

  const meta = mkEl('p', 'work-when');
  meta.textContent = `${sub.box_count} boxes · สร้างเมื่อ ${fmtDate(sub.created_at)}`;
  body.appendChild(meta);

  if (sub.note) {
    body.appendChild(mkEl('p', 'work-note', `โน้ตของเรา: ${sub.note}`));
  }
  if (sub.status === 'rejected') {
    const reason = mkEl('p', 'work-reason');
    reason.textContent = `อาจารย์ไม่ผ่าน${sub.reviewer_name ? ' (' + sub.reviewer_name + ')' : ''}: ${
      sub.review_note || 'ไม่ระบุเหตุผล'
    }`;
    body.appendChild(reason);
  }
  if (sub.status === 'approved') {
    body.appendChild(mkEl('p', 'work-approved', 'อาจารย์อนุมัติแล้ว รูปอยู่ใน dataset ของ project'));
  }
  if (sub.status === 'pending') {
    body.appendChild(mkEl('p', 'work-pending', `ส่งเมื่อ ${fmtDate(sub.submitted_at)} · รออาจารย์ตรวจ`));
  }

  const actions = document.createElement('div');
  actions.className = 'work-actions';

  const draw = document.createElement('a');
  draw.className = 'ghost';
  draw.href = drawURL(projectID, sub);
  draw.textContent = sub.status === 'draft' || sub.status === 'rejected' ? 'วาด/แก้กรอบ' : 'ดูกรอบ';
  actions.appendChild(draw);

  if (sub.status === 'draft') {
    if (classes.length === 0) {
      actions.appendChild(mkEl('span', 'empty-inline', 'รออาจารย์กำหนด class ก่อน'));
    } else {
      actions.appendChild(makeButton('ส่งอนุมัติ', 'primary-btn small', () => send(projectID, sub),
        'ส่งรูปนี้ให้อาจารย์ตรวจ'));
    }
    actions.appendChild(makeButton('ลบ', 'danger-btn small', () => remove(projectID, sub),
      'ลบฉบับร่างและไฟล์ที่อัปโหลดไว้'));
  }
  if (sub.status === 'pending') {
    actions.appendChild(makeButton('ดึงกลับ', 'ghost', () => withdraw(projectID, sub),
      'ดึงกลับมาแก้ก่อนอาจารย์ตรวจ'));
  }
  if (sub.status === 'rejected') {
    actions.appendChild(makeButton('ส่งใหม่', 'primary-btn small', () => send(projectID, sub),
      'ส่งรูปนี้ให้อาจารย์ตรวจอีกครั้ง'));
    actions.appendChild(makeButton('ลบงานนี้', 'danger-btn small', () => remove(projectID, sub),
      'ลบงานที่ถูกตีกลับออกจากหน้างานของฉัน'));
  }

  body.appendChild(actions);
  card.appendChild(body);
  return card;
}

function render() {
  el.groups.innerHTML = '';
  const counts = { draft: 0, pending: 0, approved: 0, rejected: 0 };
  let total = 0;

  for (const group of state.groups) {
    const submissions = group.submissions || [];
    for (const sub of submissions) {
      if (counts[sub.status] !== undefined) counts[sub.status] += 1;
    }
    total += submissions.length;

    if (submissions.length === 0 && !group.project.submissions_open) continue;

    const section = document.createElement('section');
    section.className = 'trash-group';

    const head = document.createElement('div');
    head.className = 'trash-group-head';

    const title = document.createElement('h2');
    title.appendChild(mkEl('span', null, group.project.name));
    title.appendChild(mkEl('span', 'badge', String(submissions.length)));
    head.appendChild(title);

    const meta = document.createElement('p');
    meta.className = 'trash-group-meta';
    meta.textContent = `อาจารย์ผู้สอน: ${group.project.owner_name || '-'} · ${
      group.project.submissions_open ? 'เปิดรับรูป' : 'ปิดรับรูป'
    } · class: ${(group.classes || []).join(', ') || 'ยังไม่มี'}`;
    head.appendChild(meta);

    section.appendChild(head);
    section.appendChild(renderUploadPanel(group));

    if (submissions.length > 0) {
      const grid = document.createElement('div');
      grid.className = 'work-grid';
      for (const sub of submissions) {
        grid.appendChild(renderCard(group.project.id, sub, group.classes || []));
      }
      section.appendChild(grid);
    }

    el.groups.appendChild(section);
  }

  el.total.textContent = String(total);
  el.counts.draft.textContent = `${counts.draft} ฉบับร่าง`;
  el.counts.pending.textContent = `${counts.pending} รออนุมัติ`;
  el.counts.approved.textContent = `${counts.approved} อนุมัติแล้ว`;
  el.counts.rejected.textContent = `${counts.rejected} ไม่ผ่าน`;
  el.empty.hidden = total > 0 || state.groups.some((g) => g.project.submissions_open);
}

async function load() {
  try {
    const data = await api('/api/mywork');
    const groups = data.projects || [];
    // The class list per project comes from the project endpoint; students may
    // read it but never edit it.
    for (const group of groups) {
      const detail = await api(base(group.project.id)).catch(() => null);
      group.classes = detail ? detail.classes || [] : [];
    }
    state.groups = groups;
    render();
  } catch (err) {
    toast('โหลดงานไม่สำเร็จ: ' + err.message, true);
  }
}

async function send(projectID, sub) {
  try {
    await api(`${base(projectID)}/submissions/${encodeURIComponent(sub.id)}/submit`, { method: 'POST' });
    await load();
    toast('ส่งให้อาจารย์ตรวจแล้ว');
  } catch (err) {
    toast('ส่งไม่สำเร็จ: ' + err.message, true);
  }
}

async function withdraw(projectID, sub) {
  try {
    await api(`${base(projectID)}/submissions/${encodeURIComponent(sub.id)}/withdraw`, { method: 'POST' });
    await load();
    toast('ดึงกลับมาเป็นฉบับร่างแล้ว');
  } catch (err) {
    toast('ดึงกลับไม่สำเร็จ: ' + err.message, true);
  }
}

async function remove(projectID, sub) {
  if (!confirm(`ลบงาน "${sub.original_name || sub.image_name}" พร้อมไฟล์ที่อัปโหลดไว้ออกจากหน้างานของฉัน?`)) return;
  try {
    await api(`${base(projectID)}/submissions/${encodeURIComponent(sub.id)}`, { method: 'DELETE' });
    await load();
    toast('ลบงานแล้ว');
  } catch (err) {
    toast('ลบไม่สำเร็จ: ' + err.message, true);
  }
}

el.refresh.addEventListener('click', load);
window.addEventListener('focus', () => {
  if (!document.hidden) load();
});

(async function boot() {
  state.user = await requireUser('student');
  if (!state.user) return;
  renderIdentity(state.user, el);
  await load();
})();
