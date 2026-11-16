package annotation

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"yoloapi/internal/auth"
)

// Seeded demo accounts. The first one is the default owner for data created
// before accounts existed. These passwords are placeholders on purpose: they
// are printed at first start so the teacher can sign in and change them.
const (
	seedOwnerID        = "0000000000000001"
	seedOwnerUsername  = "somchai"
	seedOwnerName      = "สมชาย"
	seedOwnerPassword  = "teacher1234"
	seedMemberID       = "0000000000000002"
	seedMemberUsername = "somying"
	seedMemberName     = "สมหญิง"
	seedMemberPassword = "student1234"
)

// OpenDB opens (or creates) the SQLite database used for annotation metadata.
// Images stay on disk; projects, users, classes, image records, and boxes live
// in SQLite.
func OpenDB(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer at a time; a single connection keeps behaviour
	// predictable and avoids "database is locked" errors.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	for _, stmt := range schemaStatements {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("apply schema: %w", err)
		}
	}
	if err := prepareDB(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS users (
		id           TEXT PRIMARY KEY,
		username     TEXT NOT NULL UNIQUE,
		display_name TEXT NOT NULL,
		created_at   TEXT NOT NULL,
		role         TEXT NOT NULL DEFAULT 'teacher',
		password_hash TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE IF NOT EXISTS sessions (
		token_hash TEXT PRIMARY KEY,
		user_id    TEXT NOT NULL,
		created_at TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS projects (
		id         TEXT PRIMARY KEY,
		name       TEXT NOT NULL,
		created_at TEXT NOT NULL,
		owner_id   TEXT NOT NULL DEFAULT '',
		submissions_open INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS project_members (
		project_id TEXT NOT NULL,
		user_id    TEXT NOT NULL,
		added_by   TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		PRIMARY KEY (project_id, user_id),
		FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS classes (
		project_id TEXT NOT NULL,
		class_id   INTEGER NOT NULL,
		name       TEXT NOT NULL,
		PRIMARY KEY (project_id, class_id),
		FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS images (
		id            TEXT NOT NULL DEFAULT '',
		project_id    TEXT NOT NULL,
		name          TEXT NOT NULL,
		original_name TEXT NOT NULL DEFAULT '',
		size          INTEGER NOT NULL DEFAULT 0,
		created_at    TEXT NOT NULL,
		uploaded_by   TEXT NOT NULL DEFAULT '',
		deleted_at    TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (project_id, name),
		FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS labels (
		project_id TEXT NOT NULL,
		image_name TEXT NOT NULL,
		seq        INTEGER NOT NULL,
		class_id   INTEGER NOT NULL,
		x_center   REAL NOT NULL,
		y_center   REAL NOT NULL,
		width      REAL NOT NULL,
		height     REAL NOT NULL,
		PRIMARY KEY (project_id, image_name, seq),
		FOREIGN KEY (project_id, image_name) REFERENCES images(project_id, name) ON DELETE CASCADE
	)`,
	`CREATE INDEX IF NOT EXISTS labels_by_image ON labels(project_id, image_name)`,
	`CREATE TABLE IF NOT EXISTS submissions (
		id            TEXT PRIMARY KEY,
		project_id    TEXT NOT NULL,
		student_id    TEXT NOT NULL,
		image_name    TEXT NOT NULL,
		original_name TEXT NOT NULL DEFAULT '',
		note          TEXT NOT NULL DEFAULT '',
		status        TEXT NOT NULL DEFAULT 'draft',
		review_note   TEXT NOT NULL DEFAULT '',
		labels_json   TEXT NOT NULL DEFAULT '[]',
		image_id      TEXT NOT NULL DEFAULT '',
		image_ref     TEXT NOT NULL DEFAULT '',
		created_at    TEXT NOT NULL,
		submitted_at  TEXT NOT NULL DEFAULT '',
		reviewed_at   TEXT NOT NULL DEFAULT '',
		reviewed_by   TEXT NOT NULL DEFAULT '',
		FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
		FOREIGN KEY (student_id) REFERENCES users(id) ON DELETE CASCADE
	)`,
	`CREATE INDEX IF NOT EXISTS submissions_by_project_status ON submissions(project_id, status)`,
	`CREATE INDEX IF NOT EXISTS submissions_by_student ON submissions(student_id, status)`,
	`CREATE INDEX IF NOT EXISTS sessions_by_user ON sessions(user_id)`,
}

// prepareDB upgrades databases created before users and upload tracking existed.
func prepareDB(db *sql.DB) error {
	columns := []struct {
		table string
		name  string
		ddl   string
	}{
		{"projects", "owner_id", `ALTER TABLE projects ADD COLUMN owner_id TEXT NOT NULL DEFAULT ''`},
		{"projects", "submissions_open", `ALTER TABLE projects ADD COLUMN submissions_open INTEGER NOT NULL DEFAULT 0`},
		{"images", "id", `ALTER TABLE images ADD COLUMN id TEXT NOT NULL DEFAULT ''`},
		{"images", "original_name", `ALTER TABLE images ADD COLUMN original_name TEXT NOT NULL DEFAULT ''`},
		{"images", "uploaded_by", `ALTER TABLE images ADD COLUMN uploaded_by TEXT NOT NULL DEFAULT ''`},
		{"images", "deleted_at", `ALTER TABLE images ADD COLUMN deleted_at TEXT NOT NULL DEFAULT ''`},
		{"users", "role", `ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'teacher'`},
		{"users", "password_hash", `ALTER TABLE users ADD COLUMN password_hash TEXT NOT NULL DEFAULT ''`},
	}
	for _, col := range columns {
		exists, err := columnExists(db, col.table, col.name)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := db.Exec(col.ddl); err != nil {
				return err
			}
		}
	}

	now := formatTime(time.Now().UTC())
	for _, user := range []struct {
		id       string
		username string
		name     string
		role     string
		password string
	}{
		{seedOwnerID, seedOwnerUsername, seedOwnerName, auth.RoleTeacher, seedOwnerPassword},
		{seedMemberID, seedMemberUsername, seedMemberName, auth.RoleStudent, seedMemberPassword},
	} {
		hashed, err := auth.HashPassword(user.password)
		if err != nil {
			return err
		}
		if _, err := db.Exec(`INSERT OR IGNORE INTO users
			(id, username, display_name, created_at, role, password_hash)
			VALUES (?, ?, ?, ?, ?, ?)`,
			user.id, user.username, user.name, now, user.role, hashed); err != nil {
			return err
		}
		// Accounts created before logins existed get their demo password and
		// role once, when password_hash is still empty. Accounts the teacher has
		// already touched are never overwritten.
		res, err := db.Exec(`UPDATE users SET password_hash = ?, role = ?
			WHERE username = ? AND password_hash = ''`, hashed, user.role, user.username)
		if err != nil {
			return err
		}
		if touched, _ := res.RowsAffected(); touched > 0 {
			log.Printf("seeded login for %q (role %s) - change this password after signing in",
				user.username, user.role)
		}
	}

	// Existing projects and images belong to the seeded default user.
	if _, err := db.Exec(`UPDATE projects SET owner_id = ? WHERE owner_id = ''`, seedOwnerID); err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE images SET uploaded_by = ? WHERE uploaded_by = ''`, seedOwnerID); err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE images SET original_name = name WHERE original_name = ''`); err != nil {
		return err
	}
	if err := backfillImageIDs(db); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS images_by_id ON images(id)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS images_by_uploader ON images(project_id, uploaded_by)`); err != nil {
		return err
	}
	return nil
}

func columnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var (
			cid        int
			name       string
			ctype      string
			notNull    int
			defaultV   sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &defaultV, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return found, nil
}

func backfillImageIDs(db *sql.DB) error {
	rows, err := db.Query(`SELECT rowid FROM images WHERE id = ''`)
	if err != nil {
		return err
	}
	rowIDs := []int64{}
	for rows.Next() {
		var rowID int64
		if err := rows.Scan(&rowID); err != nil {
			rows.Close()
			return err
		}
		rowIDs = append(rowIDs, rowID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(rowIDs) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, rowID := range rowIDs {
		id, err := newImageID()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE images SET id = ? WHERE rowid = ?`, id, rowID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// Close releases the database handle.
func (m *Manager) Close() error {
	if m == nil || m.db == nil {
		return nil
	}
	return m.db.Close()
}
