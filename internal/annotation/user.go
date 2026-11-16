package annotation

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"yoloapi/internal/auth"
)

// ErrBadCredentials is returned when a login attempt fails. The message is
// intentionally identical for unknown users and wrong passwords.
var ErrBadCredentials = errors.New("invalid username or password")

// User is one real account. Teachers own projects and review submissions;
// students submit photos into projects they were added to.
type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

// SessionTTL is how long a login stays valid without re-authenticating.
const SessionTTL = 7 * 24 * time.Hour

// ProjectAccess describes what the signed-in user may do in one project.
type ProjectAccess struct {
	Project   ProjectInfo
	User      User
	IsOwner   bool // the teacher who created the project
	IsMember  bool // explicitly added, so students may submit here
	IsTeacher bool
}

// CanReview reports whether this user approves or rejects submissions.
func (a ProjectAccess) CanReview() bool { return a.IsOwner }

// CanSubmit reports whether this user may send photos for review.
func (a ProjectAccess) CanSubmit() bool {
	return !a.IsTeacher && a.IsMember && a.Project.SubmissionsOpen
}

// fillUser scans one user row.
func scanUser(row scanner) (User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role); err != nil {
		return User{}, err
	}
	if !auth.ValidRole(u.Role) {
		u.Role = auth.RoleTeacher
	}
	return u, nil
}

const userColumns = `id, username, display_name, role`

// Authenticate verifies credentials and returns the account.
func (m *Manager) Authenticate(username, password string) (User, error) {
	name := auth.NormalizeUsername(username)
	if name == "" {
		return User{}, ErrBadCredentials
	}
	var (
		u      User
		hashed string
	)
	err := m.db.QueryRow(`SELECT `+userColumns+`, password_hash FROM users WHERE username = ?`, name).
		Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &hashed)
	if errors.Is(err, sql.ErrNoRows) {
		// Still burn time comparing so probing does not learn which names exist.
		auth.VerifyPassword("pbkdf2-sha256$1000$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", password)
		return User{}, ErrBadCredentials
	}
	if err != nil {
		return User{}, err
	}
	if hashed == "" || !auth.VerifyPassword(hashed, password) {
		return User{}, ErrBadCredentials
	}
	if !auth.ValidRole(u.Role) {
		u.Role = auth.RoleTeacher
	}
	return u, nil
}

// NewUser creates an account with a password.
func (m *Manager) NewUser(username, displayName, role, password string) (User, error) {
	name, err := auth.ValidateUsername(username)
	if err != nil {
		return User{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return User{}, fmt.Errorf("%w: display name must not be empty", ErrInvalid)
	}
	if len([]rune(displayName)) > 80 {
		return User{}, fmt.Errorf("%w: display name is too long", ErrInvalid)
	}
	if !auth.ValidRole(role) {
		return User{}, fmt.Errorf("%w: role must be teacher or student", ErrInvalid)
	}
	if err := auth.ValidatePassword(password); err != nil {
		return User{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	hashed, err := auth.HashPassword(password)
	if err != nil {
		return User{}, err
	}
	id, err := newUserID()
	if err != nil {
		return User{}, err
	}
	res, err := m.db.Exec(`INSERT INTO users (id, username, display_name, created_at, role, password_hash)
		VALUES (?, ?, ?, ?, ?, ?)`, id, name, displayName, formatTime(time.Now().UTC()), role, hashed)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return User{}, fmt.Errorf("%w: username %s is taken", ErrInvalid, name)
		}
		return User{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return User{}, fmt.Errorf("%w: user was not created", ErrInvalid)
	}
	return m.User(id)
}

// UpdateUser changes display name, role, and optionally the password. Nil
// fields are left untouched.
func (m *Manager) UpdateUser(id string, displayName, role, password *string) (User, error) {
	current, err := m.User(id)
	if err != nil {
		return User{}, err
	}
	next := current
	if displayName != nil {
		name := strings.TrimSpace(*displayName)
		if name == "" {
			return User{}, fmt.Errorf("%w: display name must not be empty", ErrInvalid)
		}
		if len([]rune(name)) > 80 {
			return User{}, fmt.Errorf("%w: display name is too long", ErrInvalid)
		}
		next.DisplayName = name
	}
	if role != nil {
		if !auth.ValidRole(*role) {
			return User{}, fmt.Errorf("%w: role must be teacher or student", ErrInvalid)
		}
		next.Role = *role
	}

	if _, err := m.db.Exec(`UPDATE users SET display_name = ?, role = ? WHERE id = ?`,
		next.DisplayName, next.Role, id); err != nil {
		return User{}, err
	}
	if password != nil {
		if err := auth.ValidatePassword(*password); err != nil {
			return User{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		hashed, err := auth.HashPassword(*password)
		if err != nil {
			return User{}, err
		}
		if _, err := m.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hashed, id); err != nil {
			return User{}, err
		}
		// A changed password invalidates every existing session of that user.
		if err := m.DeleteSessions(id); err != nil {
			return User{}, err
		}
	}
	return m.User(id)
}

// Users lists every account, teachers first then alphabetically.
func (m *Manager) Users() ([]User, error) {
	rows, err := m.db.Query(`SELECT ` + userColumns + ` FROM users
		ORDER BY CASE role WHEN 'teacher' THEN 0 ELSE 1 END, lower(display_name), username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// User returns one account, or ErrNotFound.
func (m *Manager) User(id string) (User, error) {
	if id == "" {
		id = DefaultUserID
	}
	row := m.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE id = ?`, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("%w: user %s", ErrNotFound, id)
	}
	if err != nil {
		return User{}, err
	}
	return u, nil
}

// CheckTeacher returns ErrForbidden unless userID belongs to a teacher.
func (m *Manager) CheckTeacher(userID string) error {
	u, err := m.User(userID)
	if err != nil {
		return err
	}
	if u.Role != auth.RoleTeacher {
		return fmt.Errorf("%w: teachers only", ErrForbidden)
	}
	return nil
}

// StartSession creates a login session and returns the raw token. Only its
// SHA-256 hash is stored, so a database leak does not hand out valid cookies.
func (m *Manager) StartSession(userID string, ttl time.Duration) (string, time.Time, error) {
	if _, err := m.User(userID); err != nil {
		return "", time.Time{}, err
	}
	if ttl <= 0 {
		ttl = SessionTTL
	}
	raw, err := auth.NewToken()
	if err != nil {
		return "", time.Time{}, err
	}
	now := time.Now().UTC()
	expires := now.Add(ttl)
	if _, err := m.db.Exec(`INSERT INTO sessions (token_hash, user_id, created_at, expires_at)
		VALUES (?, ?, ?, ?)`, auth.HashToken(raw), userID, formatTime(now), formatTime(expires)); err != nil {
		return "", time.Time{}, err
	}
	return raw, expires, nil
}

// SessionUser resolves a token to its account, or ErrNotFound when the session
// is missing or expired. Expired rows are cleaned up as they are hit.
func (m *Manager) SessionUser(rawToken string) (User, error) {
	if strings.TrimSpace(rawToken) == "" {
		return User{}, fmt.Errorf("%w: session", ErrNotFound)
	}
	var (
		userID  string
		expires string
	)
	err := m.db.QueryRow(`SELECT user_id, expires_at FROM sessions WHERE token_hash = ?`,
		auth.HashToken(rawToken)).Scan(&userID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("%w: session", ErrNotFound)
	}
	if err != nil {
		return User{}, err
	}
	expiry := parseTime(expires)
	if !expiry.IsZero() && time.Now().UTC().After(expiry) {
		_, _ = m.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, auth.HashToken(rawToken))
		return User{}, fmt.Errorf("%w: session expired", ErrNotFound)
	}
	return m.User(userID)
}

// EndSession signs the current token out.
func (m *Manager) EndSession(rawToken string) error {
	if strings.TrimSpace(rawToken) == "" {
		return nil
	}
	_, err := m.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, auth.HashToken(rawToken))
	return err
}

// DeleteSessions drops every session of one user, used after a password change.
func (m *Manager) DeleteSessions(userID string) error {
	_, err := m.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// DeleteExpiredSessions prunes old rows and reports how many were removed.
func (m *Manager) DeleteExpiredSessions() (int, error) {
	res, err := m.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, formatTime(time.Now().UTC()))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// AddMembers adds students to a project. Unknown ids fail the whole call so a
// typo cannot silently skip a student.
func (m *Manager) AddMembers(projectID, actorID string, userIDs []string) error {
	if _, err := m.info(projectID); err != nil {
		return err
	}
	seen := map[string]struct{}{}
	resolved := make([]string, 0, len(userIDs))
	for _, id := range userIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return fmt.Errorf("%w: member id must not be empty", ErrInvalid)
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		u, err := m.User(id)
		if err != nil {
			return err
		}
		if u.Role != auth.RoleStudent {
			return fmt.Errorf("%w: only students can join a project", ErrInvalid)
		}
		resolved = append(resolved, id)
	}

	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := formatTime(time.Now().UTC())
	for _, id := range resolved {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO project_members (project_id, user_id, added_by, created_at)
			VALUES (?, ?, ?, ?)`, projectID, id, actorID, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RemoveMember drops one student from a project. Their submissions stay.
func (m *Manager) RemoveMember(projectID, userID string) error {
	res, err := m.db.Exec(`DELETE FROM project_members WHERE project_id = ? AND user_id = ?`,
		projectID, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: member %s", ErrNotFound, userID)
	}
	return nil
}

// Members lists the students who may submit to a project.
func (m *Manager) Members(projectID string) ([]User, error) {
	if _, err := m.info(projectID); err != nil {
		return nil, err
	}
	rows, err := m.db.Query(`SELECT u.id, u.username, u.display_name, u.role FROM project_members pm
		JOIN users u ON u.id = pm.user_id
		WHERE pm.project_id = ?
		ORDER BY lower(u.display_name), u.username`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Access resolves what userID may do inside projectID.
func (m *Manager) Access(projectID, userID string) (ProjectAccess, error) {
	info, err := m.info(projectID)
	if err != nil {
		return ProjectAccess{}, err
	}
	u, err := m.User(userID)
	if err != nil {
		return ProjectAccess{}, err
	}
	access := ProjectAccess{
		Project:   info,
		User:      u,
		IsOwner:   info.OwnerID == u.ID,
		IsTeacher: u.Role == auth.RoleTeacher,
	}
	var one int
	err = m.db.QueryRow(`SELECT 1 FROM project_members WHERE project_id = ? AND user_id = ?`,
		projectID, u.ID).Scan(&one)
	switch {
	case err == nil:
		access.IsMember = true
	case errors.Is(err, sql.ErrNoRows):
	default:
		return ProjectAccess{}, err
	}
	// A teacher who is not the owner may browse but not review.
	return access, nil
}

func newUserID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
