const list = document.getElementById('list');
const msg = document.getElementById('msg');
const form = document.getElementById('upload-form');
const cards = document.getElementById('summary');
const sumDatasets = document.getElementById('sum-datasets');
const sumImages = document.getElementById('sum-images');
const sumSplits = document.getElementById('sum-splits');
const sumSkipped = document.getElementById('sum-skipped');

function setMsg(text, isError) {
  msg.textContent = text;
  msg.style.color = isError ? 'crimson' : 'green';
}

// สร้าง element แบบปลอดภัย (ใช้ textContent ไม่ใช้ innerHTML)
function cell(text) {
  const td = document.createElement('td');
  td.textContent = text ?? '';
  return td;
}

// รองรับทั้ง response แบบใหม่ { summary: {...} } และแบบแบนเดิม
function summaryOf(d) {
  return d.summary || d;
}

function pick(...values) {
  for (const v of values) {
    if (v !== undefined && v !== null && v !== '') return v;
  }
  return undefined;
}

function pct(v) {
  const n = Number(v);
  return Number.isFinite(n) ? Math.round(n * 100) + '%' : '';
}

function fmtDate(v) {
  if (!v) return '';
  const dt = new Date(v);
  if (Number.isNaN(dt.getTime())) return String(v);
  return dt.toLocaleString('th-TH', { dateStyle: 'medium', timeStyle: 'short' });
}

function describe(d) {
  const s = summaryOf(d);
  const parts = [];
  const total = pick(s.total, s.images, s.image_count);
  if (total !== undefined) parts.push(`${total} รูป`);
  const train = pick(s.train, s.train_count);
  const val = pick(s.val, s.val_count);
  const test = pick(s.test, s.test_count);
  if (train !== undefined && val !== undefined && test !== undefined) {
    parts.push(`train ${train} / val ${val} / test ${test}`);
  }
  const nc = pick(s.nc, Array.isArray(s.classes) ? s.classes.length : undefined);
  if (nc !== undefined) parts.push(`${nc} คลาส`);
  if (s.unlabeled_skipped > 0) parts.push(`ข้าม ${s.unlabeled_skipped} รูปที่ไม่มี label`);
  return parts.join(' · ');
}

function renderSummary(items) {
  let images = 0;
  let train = 0;
  let val = 0;
  let test = 0;
  let skipped = 0;
  for (const d of items) {
    const s = summaryOf(d);
    images += Number(pick(s.total, s.images, s.image_count)) || 0;
    train += Number(pick(s.train, s.train_count)) || 0;
    val += Number(pick(s.val, s.val_count)) || 0;
    test += Number(pick(s.test, s.test_count)) || 0;
    skipped += Number(s.unlabeled_skipped) || 0;
  }
  sumDatasets.textContent = String(items.length);
  sumImages.textContent = String(images);
  sumSplits.textContent = `${train} / ${val} / ${test}`;
  sumSkipped.textContent = String(skipped);
  cards.hidden = items.length === 0;
}

function splitCell(s) {
  const td = document.createElement('td');
  const train = pick(s.train, s.train_count);
  const val = pick(s.val, s.val_count);
  const test = pick(s.test, s.test_count);
  td.appendChild(document.createTextNode(`${train ?? '-'} / ${val ?? '-'} / ${test ?? '-'}`));

  const meta = [];
  if (s.train_ratio !== undefined && s.val_ratio !== undefined && s.test_ratio !== undefined) {
    meta.push(`${pct(s.train_ratio)} / ${pct(s.val_ratio)} / ${pct(s.test_ratio)}`);
  }
  if (s.seed !== undefined) meta.push(`seed ${s.seed}`);
  if (meta.length > 0) {
    const small = document.createElement('small');
    small.textContent = meta.join(' · ');
    td.appendChild(small);
  }
  return td;
}

function classCell(s) {
  const names = Array.isArray(s.classes) ? s.classes.filter(Boolean) : [];
  const nc = pick(s.nc, names.length || undefined);
  const td = document.createElement('td');
  td.className = 'classes';
  if (names.length > 0) {
    td.textContent = `${nc} · ${names.join(', ')}`;
    td.title = `${nc} คลาส\n${names.join('\n')}`;
  } else {
    td.textContent = nc !== undefined ? String(nc) : '-';
  }
  return td;
}

async function load() {
  try {
    const res = await fetch('/api/datasets');
    if (!res.ok) throw new Error(await res.text());
    const data = await res.json();

    // รองรับทั้งแบบ [ ... ] และแบบ { "datasets": [ ... ] }
    const items = Array.isArray(data) ? data : (data.datasets || []);

    renderSummary(items);
    list.innerHTML = '';
    if (items.length === 0) {
      const tr = document.createElement('tr');
      const td = cell('ยังไม่มี dataset');
      td.colSpan = 8;
      td.className = 'empty';
      tr.appendChild(td);
      list.appendChild(tr);
      return;
    }

    for (const d of items) {
      const s = summaryOf(d);
      const id = d.id ?? d.ID ?? '';

      const tr = document.createElement('tr');
      tr.appendChild(cell(id));

      const fileTd = cell(pick(d.filename, d.file, '-'));
      fileTd.title = pick(d.filename, d.file, '');
      tr.appendChild(fileTd);

      tr.appendChild(cell(pick(s.total, s.images, s.image_count, '-')));
      tr.appendChild(splitCell(s));
      tr.appendChild(classCell(s));
      tr.appendChild(cell(pick(s.unlabeled_skipped, 0)));
      tr.appendChild(cell(fmtDate(d.created_at ?? d.createdAt)));

      const actions = document.createElement('td');
      actions.className = 'actions';

      const dl = document.createElement('a');
      dl.href = `/api/datasets/${encodeURIComponent(id)}/download`;
      dl.textContent = 'ดาวน์โหลด';
      actions.appendChild(dl);

      const del = document.createElement('button');
      del.textContent = 'ลบ';
      del.dataset.id = id;
      actions.appendChild(del);

      tr.appendChild(actions);
      list.appendChild(tr);
    }
  } catch (err) {
    setMsg('โหลดรายการไม่สำเร็จ: ' + err.message, true);
  }
}

form.addEventListener('submit', async (e) => {
  e.preventDefault();
  setMsg('กำลังอัปโหลด...', false);
  try {
    const res = await fetch('/api/datasets', {
      method: 'POST',
      body: new FormData(form),
    });
    const text = await res.text();
    if (!res.ok) throw new Error(text);
    let created;
    try {
      created = JSON.parse(text);
    } catch {
      created = null;
    }
    const detail = created ? describe(created) : '';
    setMsg('อัปโหลดสำเร็จ' + (detail ? ' — ' + detail : ''), false);
    form.reset();
  } catch (err) {
    setMsg('อัปโหลดไม่สำเร็จ: ' + err.message, true);
  }
  load();
});

list.addEventListener('click', async (e) => {
  const id = e.target.dataset.id;
  if (!id) return;
  if (!confirm('ลบ dataset ' + id + ' ?')) return;
  try {
    const res = await fetch('/api/datasets/' + encodeURIComponent(id), { method: 'DELETE' });
    if (!res.ok) throw new Error(await res.text());
    setMsg('ลบแล้ว', false);
  } catch (err) {
    setMsg('ลบไม่สำเร็จ: ' + err.message, true);
  }
  load();
});

load();
