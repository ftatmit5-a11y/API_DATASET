'use strict';

const state = {
  user: null,
  groups: [],
  total: 0,
  filter: '',
  busy: new Set(),
};

const el = {
  total: document.getElementById('pending-total'),
  filter: document.getElementById('project-filter'),
  refresh: document.getElementById('refresh-btn'),
  groups: document.getElementById('pending-groups'),
  empty: document.getElementById('pending-empty'),
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

function submissionImageURL(group, sub) {
  return `${base(group.project.id)}/submissions/${encodeURIComponent(sub.id)}/image`;
}

function reviewURL(group, sub) {
  return `annotate.html?project=${encodeURIComponent(group.project.id)}&submission=${encodeURIComponent(sub.id)}`;
}

function projectURL(projectID) {
  return `project.html?id=${encodeURIComponent(projectID)}`;
}

function renderFilter() {
  const previous = el.filter.value;
  el.filter.innerHTML = '';
  el.filter.appendChild(el_opt('', 'ทุก project'));
  for (const group of state.groups) {
    el.filter.appendChild(el_opt(group.project.id, `${group.project.name} (${group.submissions.length})`));
  }
  el.filter.value = [...el.filter.options].some((o) => o.value === previous) ? previous : state.filter;
  el.filter.disabled = state.groups.length === 0;
}

function el_opt(value, label) {
  const option = document.createElement('option');
  option.value = value;
  option.textContent = label;
  return option;
}

function statusChip(label, kind) {
  const chip = mkEl('span', 'chip ' + kind, label);
  return chip;
}

function renderCard(group, sub) {
  const card = document.createElement('article');
  card.className = 'review-card';
  const busy = state.busy.has(sub.id);

  const figure = document.createElement('a');
  figure.className = 'review-figure';
  figure.href = reviewURL(group, sub);
  figure.title = 'เปิดตรวจพร้อมกรอบที่นักศึกษาวาด';

  const img = document.createElement('img');
  img.className = 'review-img';
  img.src = submissionImageURL(group, sub);
  img.alt = sub.original_name || sub.image_name;
  img.loading = 'lazy';
  figure.appendChild(img);

  const boxes = mkEl('span', 'review-boxes', `${sub.box_count} boxes`);
  figure.appendChild(boxes);
  card.appendChild(figure);

  const body = document.createElement('div');
  body.className = 'review-body';

  const name = mkEl('span', 'review-name', sub.original_name || sub.image_name);
  name.title = `${sub.original_name || ''}\nไฟล์ใน staging: ${sub.image_name}`;
  body.appendChild(name);

  const who = mkEl('p', 'review-who');
  who.textContent = `นักศึกษา: ${sub.student_name || '-'} (@${sub.student_username || '-'})`;
  body.appendChild(who);

  const when = mkEl('p', 'review-when');
  when.textContent = `ส่งเมื่อ ${fmtDate(sub.submitted_at || sub.created_at)}`;
  body.appendChild(when);

  if (sub.note) {
    const note = mkEl('p', 'review-note');
    note.textContent = `โน้ตจากนักศึกษา: ${sub.note}`;
    body.appendChild(note);
  } else {
    body.appendChild(mkEl('p', 'review-note muted', 'ไม่มีโน้ตจากนักศึกษา'));
  }

  const actions = document.createElement('div');
  actions.className = 'review-actions';

  const open = document.createElement('a');
  open.className = 'ghost';
  open.href = reviewURL(group, sub);
  open.textContent = 'ตรวจรูปนี้';
  actions.appendChild(open);

  const approve = document.createElement('button');
  approve.type = 'button';
  approve.className = 'primary-btn small';
  approve.textContent = 'อนุมัติ';
  approve.disabled = busy;
  approve.addEventListener('click', () => decide(group, sub, 'approve'));
  actions.appendChild(approve);

  const reject = document.createElement('button');
  reject.type = 'button';
  reject.className = 'danger-btn small';
  reject.textContent = 'ไม่ผ่าน';
  reject.disabled = busy;
  reject.addEventListener('click', () => decide(group, sub, 'reject'));
  actions.appendChild(reject);

  body.appendChild(actions);
  card.appendChild(body);
  return card;
}

function render() {
  el.groups.innerHTML = '';
  let shown = 0;

  for (const group of state.groups) {
    if (state.filter && group.project.id !== state.filter) continue;

    const section = document.createElement('section');
    section.className = 'trash-group review-group';

    const head = document.createElement('div');
    head.className = 'trash-group-head';

    const title = document.createElement('h2');
    const link = document.createElement('a');
    link.href = projectURL(group.project.id);
    link.textContent = group.project.name;
    link.title = `เปิด project ${group.project.name}`;
    title.appendChild(link);
    title.appendChild(mkEl('span', 'badge', String(group.submissions.length)));
    head.appendChild(title);

    const meta = document.createElement('p');
    meta.className = 'trash-group-meta';
    meta.textContent = `เจ้าของ: ${group.project.owner_name || '-'} · รูปใน project แล้ว ${
      group.project.image_count
    } · ${group.project.class_count} classes`;
    head.appendChild(meta);

    section.appendChild(head);

    const grid = document.createElement('div');
    grid.className = 'review-grid';
    for (const sub of group.submissions) {
      grid.appendChild(renderCard(group, sub));
      shown += 1;
    }
    section.appendChild(grid);
    el.groups.appendChild(section);
  }

  el.total.textContent = String(shown);
  el.empty.hidden = shown > 0;
  el.empty.textContent = state.groups.length === 0
    ? 'ไม่มีรูปรออนุมัติเลย — นักศึกษาส่งงานเข้ามาเมื่อไรจะแสดงที่นี่'
    : 'ไม่มีรูปในเลือกปัจจุบัน';
}

async function load() {
  try {
    const data = await api('/api/pending');
    state.groups = data.projects || [];
    state.total = data.total || 0;
    renderFilter();
    render();
  } catch (err) {
    toast('โหลดคิวรออนุมัติไม่สำเร็จ: ' + err.message, true);
  }
}

async function decide(group, sub, action) {
  let note = '';
  if (action === 'reject') {
    note = prompt('เหตุผลที่ไม่ผ่าน (นักศึกษาจะเห็นข้อความนี้)', '');
    if (note === null) return;
    note = note.trim();
    if (!note) {
      toast('ต้องระบุเหตุผลเมื่อไม่ผ่าน', true);
      return;
    }
  } else if (!confirm(`อนุมัติ "${sub.original_name || sub.image_name}" ของ ${sub.student_name || 'นักศึกษา'} เข้า project ${group.project.name}?`)) {
    return;
  }

  state.busy.add(sub.id);
  render();
  try {
    await api(`${base(group.project.id)}/submissions/${encodeURIComponent(sub.id)}/${action}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ note }),
    });
    toast(action === 'approve'
      ? 'อนุมัติแล้ว รูปเข้า project พร้อมกรอบที่นักศึกษาวาด'
      : 'ไม่ผ่าน รูปและกรอบถูกย้ายไปถังขยะของ project');
    await load();
  } catch (err) {
    toast('บันทึกผลไม่สำเร็จ: ' + err.message, true);
    state.busy.delete(sub.id);
    render();
  }
}

el.filter.addEventListener('change', () => {
  state.filter = el.filter.value;
  render();
});
el.refresh.addEventListener('click', load);

// A decision made in the review tab should refresh this tab when the user
// comes back to it, so the queue never shows stale rows.
window.addEventListener('focus', () => {
  if (!document.hidden) load();
});

(async function boot() {
  state.user = await requireUser('teacher');
  if (!state.user) return;
  renderIdentity(state.user, el);
  const params = new URLSearchParams(window.location.search);
  state.filter = params.get('project') || '';
  await load();
})();
