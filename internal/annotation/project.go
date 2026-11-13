package annotation

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultUserID is the seeded user that owns data created before accounts
// existed. Requests without an X-User-ID header are treated as this user.
const DefaultUserID = seedOwnerID

// User is one simulated account. There is no password or login yet: the web UI
// stores the selected user locally and sends X-User-ID with each request.
type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// ProjectInfo is the metadata shown in the project list.
type ProjectInfo struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	CreatedAt     time.Time `json:"created_at"`
	OwnerID       string    `json:"owner_id"`
	OwnerUsername string    `json:"owner_username"`
	OwnerName     string    `json:"owner_name"`
	ImageCount    int       `json:"image_count"`
	BoxCount      int       `json:"box_count"`
	ClassCount    int       `json:"class_count"`
}

// ProjectState is one project plus its classes, active images, and trash.
type ProjectState struct {
	ProjectInfo
	Classes []string    `json:"classes"`
	Images  []ImageInfo `json:"images"`
	Trash   []ImageInfo `json:"trash"`
}

// Manager owns the projects root folder and the SQLite metadata database.
type Manager struct {
	db   *sql.DB
	root string
}

type projectMeta struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// NewManager opens the SQLite database and prepares the projects root. Existing
// folder-based projects are imported into the database on first start.
func NewManager(dbPath, root string) (*Manager, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	db, err := OpenDB(dbPath)
	if err != nil {
		return nil, err
	}
	m := &Manager{db: db, root: root}
	if err := m.migrateFromFolders(); err != nil {
		db.Close()
		return nil, err
	}
	return m, nil
}

// Users lists the simulated accounts, oldest first.
func (m *Manager) Users() ([]User, error) {
	rows, err := m.db.Query(`SELECT id, username, display_name FROM users ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// User returns one simulated account, or ErrNotFound.
func (m *Manager) User(id string) (User, error) {
	if id == "" {
		id = DefaultUserID
	}
	var u User
	err := m.db.QueryRow(`SELECT id, username, display_name FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Username, &u.DisplayName)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("%w: user %s", ErrNotFound, id)
	}
	if err != nil {
		return User{}, err
	}
	return u, nil
}

// CheckOwner returns ErrForbidden when userID is not the owner of the project.
func (m *Manager) CheckOwner(projectID, userID string) error {
	info, err := m.info(projectID)
	if err != nil {
		return err
	}
	if userID == "" || info.OwnerID != userID {
		return fmt.Errorf("%w: only the project owner can do this", ErrForbidden)
	}
	return nil
}

// List returns every project, newest first.
func (m *Manager) List() ([]ProjectInfo, error) {
	rows, err := m.db.Query(`
		SELECT p.id, p.name, p.created_at, p.owner_id,
			COALESCE(u.username, ''), COALESCE(u.display_name, ''),
			(SELECT COUNT(*) FROM images i WHERE i.project_id = p.id AND i.deleted_at = ''),
			(SELECT COUNT(*) FROM labels l
			 JOIN images i2 ON i2.project_id = l.project_id AND i2.name = l.image_name
			 WHERE l.project_id = p.id AND i2.deleted_at = ''),
			(SELECT COUNT(*) FROM classes c WHERE c.project_id = p.id)
		FROM projects p
		LEFT JOIN users u ON u.id = p.owner_id
		ORDER BY p.created_at DESC, p.name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ProjectInfo{}
	for rows.Next() {
		var info ProjectInfo
		var created string
		if err := rows.Scan(
			&info.ID, &info.Name, &created, &info.OwnerID,
			&info.OwnerUsername, &info.OwnerName,
			&info.ImageCount, &info.BoxCount, &info.ClassCount,
		); err != nil {
			return nil, err
		}
		info.CreatedAt = parseTime(created)
		out = append(out, info)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Create makes a new project owned by the default seeded user.
func (m *Manager) Create(name string) (ProjectInfo, error) {
	return m.CreateForOwner(name, DefaultUserID)
}

// CreateForOwner makes a new project and records its owner.
func (m *Manager) CreateForOwner(name, ownerID string) (ProjectInfo, error) {
	name, err := validateProjectName(name)
	if err != nil {
		return ProjectInfo{}, err
	}
	if ownerID == "" {
		ownerID = DefaultUserID
	}
	if _, err := m.User(ownerID); err != nil {
		return ProjectInfo{}, err
	}
	id, err := newProjectID()
	if err != nil {
		return ProjectInfo{}, err
	}
	dir := filepath.Join(m.root, id)
	if err := os.MkdirAll(filepath.Join(dir, "images"), 0o755); err != nil {
		return ProjectInfo{}, err
	}
	created := time.Now().UTC()
	if _, err := m.db.Exec(`INSERT INTO projects (id, name, created_at, owner_id) VALUES (?, ?, ?, ?)`,
		id, name, formatTime(created), ownerID); err != nil {
		os.RemoveAll(dir)
		return ProjectInfo{}, err
	}
	return m.info(id)
}

// Store opens the image/label/class store for one project.
func (m *Manager) Store(id string) (*Store, error) {
	dir, err := m.projectDir(id)
	if err != nil {
		return nil, err
	}
	return newStore(m.db, id, dir)
}

// Rename changes a project's display name.
func (m *Manager) Rename(id, name string) (ProjectInfo, error) {
	name, err := validateProjectName(name)
	if err != nil {
		return ProjectInfo{}, err
	}
	if _, err := m.projectDir(id); err != nil {
		return ProjectInfo{}, err
	}
	if _, err := m.db.Exec(`UPDATE projects SET name = ? WHERE id = ?`, name, id); err != nil {
		return ProjectInfo{}, err
	}
	return m.info(id)
}

// Delete removes a project, its rows, and all of its image files.
func (m *Manager) Delete(id string) error {
	dir, err := m.projectDir(id)
	if err != nil {
		return err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`DELETE FROM labels WHERE project_id = ?`,
		`DELETE FROM images WHERE project_id = ?`,
		`DELETE FROM classes WHERE project_id = ?`,
		`DELETE FROM projects WHERE id = ?`,
	} {
		if _, err := tx.Exec(stmt, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// Info returns one project's metadata, or ErrNotFound.
func (m *Manager) Info(id string) (ProjectInfo, error) {
	return m.info(id)
}

// ProjectTrash is the trash of one project.
type ProjectTrash struct {
	Project ProjectInfo `json:"project"`
	Images  []ImageInfo `json:"images"`
}

// Trash lists trashed images grouped by project, newest project first. Every
// group carries the project metadata so the UI can show which project an image
// came from and whether the current user owns it.
func (m *Manager) Trash() ([]ProjectTrash, error) {
	rows, err := m.db.Query(`
		SELECT p.id
		FROM projects p
		WHERE EXISTS (
			SELECT 1 FROM images i WHERE i.project_id = p.id AND i.deleted_at <> ''
		)
		ORDER BY p.created_at DESC, p.name ASC`)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	groups := []ProjectTrash{}
	for _, id := range ids {
		info, err := m.info(id)
		if err != nil {
			return nil, err
		}
		store, err := m.Store(id)
		if err != nil {
			return nil, err
		}
		images, err := store.TrashedImages()
		if err != nil {
			return nil, err
		}
		groups = append(groups, ProjectTrash{Project: info, Images: images})
	}
	return groups, nil
}

// ImportLegacy copies the old single-workspace folder into a new project.
// It returns false when there is nothing to import.
func (m *Manager) ImportLegacy(legacyDir, name string) (ProjectInfo, bool, error) {
	classes, err := readClassesFile(filepath.Join(legacyDir, "classes.txt"))
	if err != nil {
		return ProjectInfo{}, false, err
	}
	images, err := listImageFiles(filepath.Join(legacyDir, "images"))
	if err != nil {
		return ProjectInfo{}, false, err
	}
	if len(classes) == 0 && len(images) == 0 {
		return ProjectInfo{}, false, nil
	}

	type legacyImage struct {
		name   string
		labels []Label
	}
	loaded := make([]legacyImage, 0, len(images))
	maxClass := -1
	for _, name := range images {
		labels, err := readLabelsFile(filepath.Join(legacyDir, "labels", labelFileName(name)))
		if err != nil {
			return ProjectInfo{}, false, err
		}
		for _, l := range labels {
			if l.ClassID > maxClass {
				maxClass = l.ClassID
			}
		}
		loaded = append(loaded, legacyImage{name: name, labels: labels})
	}
	for len(classes) <= maxClass {
		classes = append(classes, fmt.Sprintf("class_%d", len(classes)))
	}

	info, err := m.Create(name)
	if err != nil {
		return ProjectInfo{}, false, err
	}
	store, err := m.Store(info.ID)
	if err != nil {
		return ProjectInfo{}, false, err
	}
	if err := store.SaveClasses(classes); err != nil {
		return ProjectInfo{}, false, err
	}
	for _, img := range loaded {
		f, err := os.Open(filepath.Join(legacyDir, "images", img.name))
		if err != nil {
			return ProjectInfo{}, false, err
		}
		newName, addErr := store.importImage(img.name, img.name, DefaultUserID, f)
		f.Close()
		if addErr != nil {
			return ProjectInfo{}, false, addErr
		}
		if len(img.labels) > 0 {
			if err := store.SaveLabels(newName, img.labels); err != nil {
				return ProjectInfo{}, false, err
			}
		}
	}
	result, err := m.info(info.ID)
	if err != nil {
		return ProjectInfo{}, false, err
	}
	return result, true, nil
}

func (m *Manager) info(id string) (ProjectInfo, error) {
	var info ProjectInfo
	var created string
	err := m.db.QueryRow(`
		SELECT p.id, p.name, p.created_at, p.owner_id,
			COALESCE(u.username, ''), COALESCE(u.display_name, ''),
			(SELECT COUNT(*) FROM images i WHERE i.project_id = p.id AND i.deleted_at = ''),
			(SELECT COUNT(*) FROM labels l
			 JOIN images i2 ON i2.project_id = l.project_id AND i2.name = l.image_name
			 WHERE l.project_id = p.id AND i2.deleted_at = ''),
			(SELECT COUNT(*) FROM classes c WHERE c.project_id = p.id)
		FROM projects p
		LEFT JOIN users u ON u.id = p.owner_id
		WHERE p.id = ?`, id).
		Scan(&info.ID, &info.Name, &created, &info.OwnerID,
			&info.OwnerUsername, &info.OwnerName,
			&info.ImageCount, &info.BoxCount, &info.ClassCount)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectInfo{}, fmt.Errorf("%w: project %s", ErrNotFound, id)
	}
	if err != nil {
		return ProjectInfo{}, err
	}
	info.CreatedAt = parseTime(created)
	return info, nil
}

func (m *Manager) projectDir(id string) (string, error) {
	if !validProjectID(id) {
		return "", fmt.Errorf("%w: bad project id", ErrInvalid)
	}
	var exists int
	err := m.db.QueryRow(`SELECT 1 FROM projects WHERE id = ?`, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: project %s", ErrNotFound, id)
	}
	if err != nil {
		return "", err
	}
	dir := filepath.Join(m.root, id)
	if err := os.MkdirAll(filepath.Join(dir, "images"), 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func validateProjectName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("%w: project name must not be empty", ErrInvalid)
	}
	if len([]rune(name)) > 80 {
		return "", fmt.Errorf("%w: project name is too long", ErrInvalid)
	}
	return name, nil
}

// migrateFromFolders imports projects created before SQLite was introduced.
// Folder data is left in place as a backup.
func (m *Manager) migrateFromFolders() error {
	entries, err := os.ReadDir(m.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && validProjectID(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)

	for _, id := range ids {
		var exists int
		err := m.db.QueryRow(`SELECT 1 FROM projects WHERE id = ?`, id).Scan(&exists)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		dir := filepath.Join(m.root, id)
		meta, err := readMetaFile(dir)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return err
		}
		if err := m.importProjectFolder(id, dir, meta); err != nil {
			return err
		}
		log.Printf("imported project %q from folder into SQLite", meta.Name)
	}
	return nil
}

func (m *Manager) importProjectFolder(id, dir string, meta projectMeta) error {
	classes, err := readClassesFile(filepath.Join(dir, "classes.txt"))
	if err != nil {
		return err
	}
	images, err := listImageFiles(filepath.Join(dir, "images"))
	if err != nil {
		return err
	}

	type pendingImage struct {
		name      string
		size      int64
		createdAt time.Time
		labels    []Label
	}
	pending := make([]pendingImage, 0, len(images))
	maxClass := -1
	for _, name := range images {
		info, err := os.Stat(filepath.Join(dir, "images", name))
		if err != nil {
			return err
		}
		labels, err := readLabelsFile(filepath.Join(dir, "labels", labelFileName(name)))
		if err != nil {
			log.Printf("project %s: ignoring label file for %s: %v", id, name, err)
			labels = nil
		}
		for _, l := range labels {
			if l.ClassID > maxClass {
				maxClass = l.ClassID
			}
		}
		pending = append(pending, pendingImage{
			name:      name,
			size:      info.Size(),
			createdAt: info.ModTime().UTC(),
			labels:    labels,
		})
	}
	for len(classes) <= maxClass {
		classes = append(classes, fmt.Sprintf("class_%d", len(classes)))
	}

	created := meta.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	projectName := strings.TrimSpace(meta.Name)
	if projectName == "" {
		projectName = "Default"
	}

	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`INSERT INTO projects (id, name, created_at, owner_id) VALUES (?, ?, ?, ?)`,
		id, projectName, formatTime(created), DefaultUserID); err != nil {
		return err
	}
	for classID, name := range classes {
		if _, err := tx.Exec(`INSERT INTO classes (project_id, class_id, name) VALUES (?, ?, ?)`,
			id, classID, name); err != nil {
			return err
		}
	}
	for _, img := range pending {
		imageID, err := newImageID()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO images
			(id, project_id, name, original_name, size, created_at, uploaded_by)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			imageID, id, img.name, img.name, img.size, formatTime(img.createdAt), DefaultUserID); err != nil {
			return err
		}
		for seq, l := range img.labels {
			if _, err := tx.Exec(`INSERT INTO labels
				(project_id, image_name, seq, class_id, x_center, y_center, width, height)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				id, img.name, seq, l.ClassID, l.XCenter, l.YCenter, l.Width, l.Height); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func readMetaFile(dir string) (projectMeta, error) {
	data, err := os.ReadFile(filepath.Join(dir, "project.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return projectMeta{}, fmt.Errorf("%w: project metadata", ErrNotFound)
		}
		return projectMeta{}, err
	}
	var meta projectMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return projectMeta{}, err
	}
	return meta, nil
}

func newProjectID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// newImageID returns a random UUID v4. It is stored in images.id and used as
// the on-disk filename so two uploads with the same original name can never
// collide.
func newImageID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	s := hex.EncodeToString(b[:])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}

func validProjectID(id string) bool {
	if len(id) != 16 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}
