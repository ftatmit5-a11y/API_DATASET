# YOLO Dataset API

API ภาษา Go รับชุดข้อมูล YOLO เป็นไฟล์ ZIP แล้วแบ่งเป็น **train / val / test** พร้อมสร้าง `data.yaml` สำหรับ Ultralytics

## สิ่งที่ API ทำ

1. รับไฟล์ ZIP ทาง `POST /api/datasets`
2. จับคู่รูปกับไฟล์ label (ชื่อเดียวกัน)
3. สุ่มแบ่งตามสัดส่วน (ค่าเริ่มต้น 70% / 20% / 10%)
4. สร้าง ZIP ผลลัพธ์ที่มี `data.yaml`

โครงสร้าง ZIP ที่รับได้:

```text
images/
  img_01.jpg
  group/img_02.png
labels/
  img_01.txt
  group/img_02.txt
classes.txt
```

รูปกับ label อยู่โฟลเดอร์เดียวกันก็ได้ (`photo.jpg` คู่กับ `photo.txt`)

`classes.txt` เป็นชื่อคลาสบรรทัดละชื่อ ถ้าไม่มีไฟล์นี้ API จะตั้งชื่อ `class_0`, `class_1` ตามเลขคลาสใน label  
รองรับ `obj.names` และ `names` ใน `data.yaml` / `dataset.yaml` ด้วย

รูปที่ไม่มี label จะถูกข้าม และจำนวนที่ข้ามอยู่ใน `unlabeled_skipped`

ถ้า ZIP มีโฟลเดอร์ห่ออยู่ชั้นเดียว เช่น `MyDataset/images/...` API จะตัดโฟลเดอร์นั้นออกให้ ผลลัพธ์จึงเป็น `train/images/...` โดยไม่มี `MyDataset` ซ้อนอยู่

ZIP ผลลัพธ์:

```text
data.yaml
train/images/...
train/labels/...
val/images/...
val/labels/...
test/images/...
test/labels/...
```

ตัวอย่าง `data.yaml`:

```yaml
path: .
train: train/images
val: val/images
test: test/images

nc: 2
names:
  0: cat
  1: dog
```

ถ้า `test_ratio` เป็น 0 จะไม่สร้างโฟลเดอร์ test และไม่ใส่คีย์ `test` ในไฟล์ yaml

## Projects และ Annotation Studio

หน้าแรกของงาน annotation คือ `http://localhost:8080/projects`

ขั้นตอน:

1. เลือก **ผู้ใช้จำลอง** ที่มุมขวาบน (`สมชาย` เป็นเจ้าของข้อมูลเดิม, `สมหญิง` เป็น user ธรรมดา)
2. กด **สร้าง project** (เช่น `งานวิจัยปลา`) — project จะแสดงเป็นการ์ดในหน้านี้ และผู้ใช้ที่สร้างเป็นเจ้าของ project
3. กดเข้าไปใน project จะเห็นรูปทั้งหมดที่อยู่ใน project พร้อมชื่อผู้อัปโหลด
4. กด **+ เพิ่มรูป** หรือลากไฟล์มาวาง เพื่อเพิ่มรูปได้เรื่อยๆ ไม่จำกัดจำนวน
5. คลิกรูปเพื่อเปิดหน้า Annotation Studio เฉพาะรูปนั้น
6. เพิ่ม class แล้ววาด bounding box กด **บันทึก** (หรือ `Ctrl+S`)
7. กลับไปหน้า project เมื่อได้จำนวนรูปที่พอใจแล้ว กด **ทำ data (ZIP)** เพื่อดาวน์โหลด `images/`, `labels/`, `classes.txt`, `manifest.csv` ทั้งหมดของ project

### ถังขยะ (soft delete)

- การกด `×` ที่รูป (ทั้งในหน้า project และหน้า annotate) จะ **ย้ายรูปไปถังขยะ** ไม่ได้ลบไฟล์ทันที
- ถังขยะมีหน้าแยกที่ `http://localhost:8080/trash` แสดงรูป **แยกราย project** พร้อมชื่อ project, เจ้าของ project,เวลาที่ลบ, ผู้อัปโหลด และจำนวน box ของรูปนั้น
- ถ้าเปิดจากใน project (ปุ่ม “ถังขยะของ project นี้”) จะกรองเหลือเฉพาะรูปของ project นั้น: `/trash?project=<project-id>`
- รูปในถังขยะจะไม่แสดงในรายการรูปปกติ ไม่ถูกนับในสถิติ และไม่ถูกใส่ใน ZIP ตอนทำ data (ยังกู้คืนได้ครบทั้งรูปและ label)
- สิทธิ์:
  - **ย้ายเข้าถังขยะ / กู้คืน** → เจ้าของ project หรือผู้อัปโหลดรูปนั้น
  - **ลบถาวร** → **เจ้าของ project เท่านั้น** (ปุ่มจะไม่แสดงสำหรับผู้ใช้คนอื่น และ API ตอบ `403 Forbidden`)

### ผู้ใช้จำลองและเจ้าของ project

- ยังไม่มีระบบ login — หน้าเว็บเก็บผู้ใช้ที่เลือกไว้ใน `localStorage` แล้วส่ง `X-User-ID` ไปกับทุก request
- เจ้าของ project เท่านั้นที่เปลี่ยนชื่อหรือลบ project ได้
- **เจ้าของ project เท่านั้นที่เพิ่ม แก้ไข หรือลบ class ได้** (ทั้งใน UI และ API จะตอบ `403 Forbidden` ให้ผู้ใช้ที่ไม่ใช่เจ้าของ)
- ผู้ใช้ทุกคนอัปโหลดรูปและวาด label ได้
- เจ้าของ project หรือคนที่อัปโหลดรูปนั้นสามารถย้ายรูปไปถังขยะและกู้คืนรูปนั้นได้
- การลบถาวรจากรูปในถังขยะ ทำได้เฉพาะเจ้าของ project เท่านั้น
- ทุกครั้งที่อัปโหลด ระบบบันทึก `uploaded_by` และเวลาไว้ในฐานข้อมูล

### ชื่อรูปซ้ำ

รูปใหม่ทุกไฟล์ได้ UUID v4 เป็นชื่อไฟล์จริงบนดิสก์ เช่น `3f2c...-....jpg` ทำให้อัปโหลดชื่อซ้ำกันได้โดยไม่ทับกัน ส่วนชื่อเดิมยังแสดงในหน้าเว็บและถูกเก็บใน `original_name`

ตอน export ระบบใช้ชื่อเดิมให้ก่อน ถ้าซ้ำจะเติม id สั้นๆ ต่อท้าย และสร้าง `manifest.csv` บอกชื่อไฟล์, ชื่อเดิม, ผู้อัปโหลด, และเวลา:

```text
image,original_name,uploaded_by,uploaded_by_name,uploaded_at
photo.jpg,photo.jpg,0000000000000002,สมหญิง,2026-11-12T10:00:00Z
photo_3f2c1a9b.jpg,photo.jpg,0000000000000001,สมชาย,2026-11-12T10:01:00Z
```

หน้า annotate รับพารามิเตอร์ `project` และ `image` เพื่อเปิดตรงรูปที่เลือก:

```text
/annotate.html?project=<project-id>&image=<image-name>
```

`image` คือชื่อไฟล์ที่เก็บจริง (UUID) ซึ่งได้จากรายการรูปใน project

ข้อมูลแยกตาม project ยังไม่แบ่ง train/val/test:

```text
data/
  annotations.db          # SQLite: users, projects, classes, images, labels
  projects/
    <project-id>/
      images/             # ไฟล์รูปจริง (ชื่อ UUID, ไม่มี label .txt แล้ว)
```

ถ้าเคยใช้หน้า annotation แบบ workspace เดียว ข้อมูลเดิมใน `data/annotations/` จะถูกคัดลอกเข้า project ชื่อ `Default` ให้อัตโนมัติเมื่อเริ่มเซิร์ฟเวอร์ครั้งแรก (ไฟล์เดิมยังอยู่เป็น backup) และข้อมูลเดิมจะเป็นของ `สมชาย`

## ฐานข้อมูล (SQLite)

- ใช้ `modernc.org/sqlite` ซึ่งเป็น pure Go จึงไม่ต้องติดตั้ง C compiler หรือเปิด CGO
- ไฟล์ฐานข้อมูลอยู่ที่ `data/annotations.db` (เปิด/สร้างอัตโนมัติเมื่อเริ่มเซิร์ฟเวอร์)
- ตารางหลัก: `users`, `projects`, `classes`, `images`, `labels`
- `projects.owner_id` บอกเจ้าของ project, `images.uploaded_by` บอกคนอัปโหลด, `images.id` เป็น UUID ของรูป
- `images.deleted_at` เก็บเวลาเมื่อรูปถูกย้ายไปถังขยะ (ค่าว่าง = รูปยังใช้งานอยู่) การลบรูปจึงเป็น soft delete
- รูปภาพยังเก็บเป็นไฟล์ใน `data/projects/<project-id>/images/` ส่วน class, metadata และ bounding box อยู่ใน SQLite
- ถ้ามี project แบบโฟลเดอร์เดิม (`project.json`, `classes.txt`, `labels/`) ระบบจะ import เข้า SQLite ให้อัตโนมัติเมื่อเริ่มเซิร์ฟเวอร์ และเก็บไฟล์เดิมไว้เป็น backup
- การ export ยังสร้าง ZIP แบบ YOLO จากข้อมูลในฐานข้อมูล: `classes.txt`, `images/`, `labels/`, `manifest.csv`

พิกัดที่บันทึกเป็นแบบ normalized:

```text
<class_id> <x_center> <y_center> <width> <height>
```

โดยแปลงจากพิกัด pixel บนหน้าจอ:

```text
x_center = (x_min + x_max) / (2 * W)
y_center = (y_min + y_max) / (2 * H)
width    = (x_max - x_min) / W
height   = (y_max - y_min) / H
```

API ของ Annotation Studio:

- `GET /api/users` รายชื่อผู้ใช้จำลอง
- `GET /api/trash` รูปที่อยู่ในถังขยะทั้งหมด จัดกลุ่มตาม project
- `GET /api/projects` รายการ project พร้อมจำนวนรูป/box/class
- `POST /api/projects` สร้าง project (`{"name": "..."}`)
- `GET /api/projects/{id}` ข้อมูล project, class และรูปทั้งหมด
- `PATCH /api/projects/{id}` เปลี่ยนชื่อ project (เจ้าของเท่านั้น)
- `DELETE /api/projects/{id}` ลบ project ทั้งหมด (เจ้าของเท่านั้น)
- `POST /api/projects/{id}/images` อัปโหลดรูป (`multipart` ฟิลด์ `files`)
- `GET /api/projects/{id}/images/{name}` ดูรูป
- `DELETE /api/projects/{id}/images/{name}` ย้ายรูปและ label ไปถังขยะ (soft delete, เจ้าของหรือผู้อัปโหลด)
- `POST /api/projects/{id}/images/{name}/restore` กู้คืนรูปจากถังขยะ (เจ้าของหรือผู้อัปโหลด)
- `DELETE /api/projects/{id}/images/{name}/permanent` ลบรูปและ label ถาวร (**เจ้าของ project เท่านั้น**)
- `GET /api/projects/{id}/images/{name}/labels` อ่าน label
- `PUT /api/projects/{id}/images/{name}/labels` บันทึก label
- `POST /api/projects/{id}/classes` เพิ่ม class (เจ้าของเท่านั้น)
- `PUT /api/projects/{id}/classes` แก้ไขรายการ class (เจ้าของเท่านั้น)
- `GET /api/projects/{id}/export` ดาวน์โหลด ZIP (`images/`, `labels/`, `classes.txt`)

## รันเซิร์ฟเวอร์

Go ติดตั้งผ่าน Scoop อยู่ที่ `%USERPROFILE%\scoop\apps\go\current\bin` ถ้าคำสั่ง `go` ยังไม่เจอ ให้เติม PATH ก่อน:

```powershell
$env:Path = "$env:USERPROFILE\scoop\apps\go\current\bin;" + $env:Path
go run ./cmd/server
```

ค่าเริ่มต้นฟังที่ `http://localhost:8080` ผลลัพธ์เก็บในโฟลเดอร์ `data/jobs/`

ตัวแปรสภาพแวดล้อม:

| ตัวแปร | ค่าเริ่มต้น | ความหมาย |
| --- | --- | --- |
| `ADDR` | `:8080` | พอร์ตที่ฟัง |
| `DATA_DIR` | `data` | ที่เก็บงานที่แบ่งแล้ว |
| `MAX_UPLOAD_MB` | `512` | ขนาด ZIP สูงสุด |

ไฟล์ `.env` ถูก gitignore ไว้ ใช้ `.env.example` เป็นตัวอย่างค่าที่ `docker compose` อ่านอัตโนมัติ

## รันด้วย Docker

```powershell
docker compose up --build -d     # เปิด http://localhost:8080
docker compose logs -f yoloapi   # ดู log
docker compose down              # หยุด (ข้อมูลยังอยู่ใน volume)
docker compose down -v           # หยุดและลบ volume (ข้อมูลหาย)
```

- image build แบบ multi-stage: ใช้ `golang:1.23-alpine` 컴ไพล์ด้วย `CGO_ENABLED=0` (SQLite เป็น pure Go ผ่าน `modernc.org/sqlite` จึงไม่ต้องมี C toolchain) แล้วรันบน `alpine:3.21` ด้วย user non-root (`uid 10001`)
- ข้อมูลทั้งหมดอยู่ที่ `DATA_DIR=/data` ซึ่ง mount เป็น volume ชื่อ `yolo-api_yolo-data` (SQLite + รูปของแต่ละ project + งาน dataset ที่สร้างแล้ว) **ข้อมูลไม่หายเมื่อ restart container แต่หายเมื่อ `down -v`**
- มี `HEALTHCHECK` ชี้ที่ `/health` และ expose พอร์ต 8080
- จะใช้ `docker build` เองก็ได้:

```powershell
docker build -t yoloapi:dev .
docker run --rm -p 8080:8080 -v yolo-data:/data yoloapi:dev
```

- ถ้าอยากให้ container ใช้โฟลเดอร์ `data/` เดิมที่มีอยู่แล้ว ให้สร้าง `docker-compose.override.yml` (ไม่ถูก commit) ตามคอมเมนต์ใน `docker-compose.yml`

## Git และ CI

ไฟล์ที่เตรียมไว้สำหรับขึ้น Git:

| ไฟล์ | หน้าที่ |
| --- | --- |
| `.gitignore` | ไม่เก็บ `data/` (ฐานข้อมูล SQLite, รูป, ZIP ที่สร้าง), `bin/`, `coverage.out`, `.env` |
| `.gitattributes` | บังคับ line ending เป็น LF (สำคัญมาก เพราะ `gofmt` ปฏิเสธ CRLF และ Windows มักตั้ง `core.autocrlf=true`) |
| `.env.example` | ค่าที่ `docker compose` อ่าน (`PORT`, `MAX_UPLOAD_MB`, `VERSION`, `COMMIT`) ไว้คัดลอกเป็น `.env` |
| `.dockerignore` | ตัด `.git`, `data/`, เอกสารออกจากการ build |
| `Dockerfile` | image หลาย stage รันด้วย non-root |
| `docker-compose.yml` | รัน local/volume พร้อม healthcheck |
| `.github/workflows/ci.yml` | ทุก push/PR: gofmt + `go vet` + `go test -race` (Linux และ Windows), ตรวจไฟล์เว็บ, build image แล้ว smoke test |
| `.github/workflows/release.yml` | เมื่อ push tag `v*`: build multi-arch (`amd64`, `arm64`) และ push ไป GHCR |
| `scripts/check-web.mjs` | ตรวจว่า `go:embed` มีไฟล์ครบ, `getElementById` ตรงกับ HTML, และ `common.js` ถูกลoad ก่อนใช้งาน |

ตั้งค่าครั้งแรกและดันขึ้น GitHub:

```powershell
git init
git add .
git commit -m "YOLO dataset API and annotation workspace"
git branch -M main
git remote add origin https://github.com/<user>/<repo>.git
git push -u origin main
```

ออก release image:

```powershell
git tag v1.0.0
git push origin v1.0.0     # ได้ ghcr.io/<owner>/<repo>:1.0.0 และ :latest
```

ตรวจงานทั้งหมดก่อน commit (เหมือนที่ CI ทำ):

```powershell
gofmt -l .
go vet ./...
go test -race ./...
node scripts/check-web.mjs
```

## เรียก API

`POST /api/datasets` แบบ `multipart/form-data`

| ฟิลด์ | บังคับ | ความหมาย |
| --- | --- | --- |
| `file` | ใช่ | ไฟล์ `.zip` |
| `train_ratio` | ไม่ | สัดส่วน train ต้องส่งคู่กับ val และ test |
| `val_ratio` | ไม่ | สัดส่วน val |
| `test_ratio` | ไม่ | สัดส่วน test ผลรวมของสามค่าต้องเท่ากับ 1 |
| `seed` | ไม่ | จำนวนเต็ม ใช้สุ่มซ้ำผลเดิมได้ |
| `names` | ไม่ | ชื่อคลาสคั่นด้วยจุลภาค เช่น `cat,dog` ทับชื่อใน ZIP |

ตัวอย่างด้วย curl:

```powershell
curl.exe -F "file=@bruno/yolo-dataset/sample/sample_dataset.zip" -F "train_ratio=0.7" -F "val_ratio=0.2" -F "test_ratio=0.1" -F "seed=42" http://localhost:8080/api/datasets
```

คำตอบเป็น JSON มี `id`, จำนวนแต่ละชุด, รายชื่อคลาส และ `download_url`

เส้นทางอื่น:

- `GET /health`
- `GET /api/datasets` รายการงานที่แบ่งแล้ว
- `GET /api/datasets/{id}`
- `GET /api/datasets/{id}/download` ดาวน์โหลด ZIP
- `DELETE /api/datasets/{id}`

## Bruno

1. เปิด Bruno แล้วเลือก **Open Collection**
2. เลือกโฟลเดอร์ `bruno/yolo-dataset`
3. เลือก environment **Local** (`baseUrl` = `http://localhost:8080`)
4. รัน **Upload Dataset** ก่อน มันจะเก็บ `datasetId` ให้อัตโนมัติ
5. จากนั้นรัน Get, Download หรือ Delete ได้

ชุดตัวอย่างอยู่ใน `bruno/yolo-dataset/sample/sample_dataset.zip` (รูป 10 ใบ, คลาส `cat` กับ `dog`)  
สร้างใหม่ได้ด้วย:

```powershell
go run ./cmd/sample
```

## เทสต์

```powershell
go test ./...
```
