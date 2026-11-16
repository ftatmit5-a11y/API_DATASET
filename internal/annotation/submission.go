package annotation

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"yoloapi/internal/auth"
)

// Submission workflow states. A student uploads into "draft", draws boxes, then
// marks it "pending" for the teacher. The teacher answers "approved" (the photo
// joins the project) or "rejected" (the photo goes to the project trash).
const (
	SubmissionDraft    = "draft"
	SubmissionPending  = "pending"
	SubmissionApproved = "approved"
	SubmissionRejected = "rejected"
)

// MaxSubmissionNote caps the free-text fields students and teachers write.
const MaxSubmissionNote = 500

// Submission is one photo waiting for, or having received, a teacher decision.
type Submission struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"project_id"`
	StudentID    string    `json:"student_id"`
	StudentName  string    `json:"student_name"`
	StudentUser  string    `json:"student_username"`
	ImageName    string    `json:"image_name"`
	OriginalName string    `json:"original_name"`
	Note         string    `json:"note"`
	Status       string    `json:"status"`
	ReviewNote   string    `json:"review_note"`
	Labels       []Label   `json:"labels"`
	BoxCount     int       `json:"box_count"`
	CreatedAt    time.Time `json:"created_at"`
	SubmittedAt  time.Time `json:"submitted_at,omitempty"`
	ReviewedAt   time.Time `json:"reviewed_at,omitempty"`
	ReviewName   string    `json:"reviewer_name"`
	ImageID      string    `json:"image_id,omitempty"`
	ImageRef     string    `json:"image_ref,omitempty"`
}

// SubmissionStore writes submission files and rows for one project. Image files
// of pending work live under <project>/staging so they cannot be picked up by
// export until a teacher approves them.
type SubmissionStore struct {
	db          *sql.DB
	projectID   string
	dir         string
	imagesDir   string
	stagingDir  string
	cleanupFile func(string) error
}

func newSubmissionStore(db *sql.DB, projectID, dir string) (*SubmissionStore, error) {
	staging := filepath.Join(dir, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return nil, err
	}
	return &SubmissionStore{
		db:         db,
		projectID:  projectID,
		dir:        dir,
		imagesDir:  filepath.Join(dir, "images"),
		stagingDir: staging,
	}, nil
}

// StagingDir is where photos waiting for review are kept.
func (s *SubmissionStore) StagingDir() string { return s.stagingDir }

func (s *SubmissionStore) stagingPath(imageName string) (string, error) {
	if !validImageName(imageName) {
		return "", fmt.Errorf("%w: bad submission name", ErrInvalid)
	}
	return filepath.Join(s.stagingDir, imageName), nil
}

func safeImagePath(dir, name string) (string, error) {
	if !validImageName(name) {
		return "", fmt.Errorf("%w: bad image name", ErrInvalid)
	}
	return filepath.Join(dir, name), nil
}

// Add stores one uploaded photo as a draft submission and returns its row.
func (s *SubmissionStore) Add(originalName, studentID, note string, r io.Reader) (Submission, error) {
	if studentID == "" {
		return Submission{}, fmt.Errorf("%w: student is required", ErrInvalid)
	}
	note, err := cleanNote(note)
	if err != nil {
		return Submission{}, err
	}
	original := sanitizeFilename(originalName)
	ext := strings.ToLower(filepath.Ext(original))
	if _, ok := imageExts[ext]; !ok {
		return Submission{}, fmt.Errorf("%w: unsupported image type %q", ErrInvalid, ext)
	}
	id, err := newImageID()
	if err != nil {
		return Submission{}, err
	}
	name := id + ext

	target := filepath.Join(s.stagingDir, name)
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return Submission{}, err
	}
	written, copyErr := io.Copy(f, io.LimitReader(r, maxImageBytes+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(target)
		if copyErr != nil {
			return Submission{}, copyErr
		}
		return Submission{}, closeErr
	}
	if written > maxImageBytes {
		os.Remove(target)
		return Submission{}, fmt.Errorf("%w: image %s exceeds %d MiB", ErrInvalid, name, maxImageBytes>>20)
	}

	created := formatTime(time.Now().UTC())
	_, err = s.db.Exec(`INSERT INTO submissions
		(id, project_id, student_id, image_name, original_name, note, status, labels_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, '[]', ?)`,
		id, s.projectID, studentID, name, original, note, SubmissionDraft, created)
	if err != nil {
		os.Remove(target)
		return Submission{}, err
	}
	return s.Get(id)
}

// Get loads one submission of this project.
func (s *SubmissionStore) Get(id string) (Submission, error) {
	if !validSubmissionID(id) {
		return Submission{}, fmt.Errorf("%w: bad submission id", ErrInvalid)
	}
	row := s.db.QueryRow(submissionSelect+`WHERE sm.id = ? AND sm.project_id = ?`, id, s.projectID)
	sub, err := scanSubmission(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Submission{}, fmt.Errorf("%w: submission %s", ErrNotFound, id)
	}
	if err != nil {
		return Submission{}, err
	}
	return sub, nil
}

// List returns submissions of this project. studentID filters to one student's
// own work; an empty studentID returns everything (teacher view). statuses
// narrows the query, and an empty list means "any status".
func (s *SubmissionStore) List(studentID string, statuses ...string) ([]Submission, error) {
	query := submissionSelect
	args := []any{s.projectID}
	where := []string{"sm.project_id = ?"}
	if studentID != "" {
		where = append(where, "sm.student_id = ?")
		args = append(args, studentID)
	}
	if len(statuses) > 0 {
		marks := make([]string, 0, len(statuses))
		for _, status := range statuses {
			if !validSubmissionStatus(status) {
				return nil, fmt.Errorf("%w: unknown submission status %q", ErrInvalid, status)
			}
			marks = append(marks, "?")
			args = append(args, status)
		}
		where = append(where, "sm.status IN ("+strings.Join(marks, ",")+")")
	}
	query += "WHERE " + strings.Join(where, " AND ")
	query += ` ORDER BY sm.created_at DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Submission{}
	for rows.Next() {
		sub, err := scanSubmission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Counts reports how many submissions sit in each status.
func (s *SubmissionStore) Counts() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM submissions
		WHERE project_id = ? GROUP BY status`, s.projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{
		SubmissionDraft:    0,
		SubmissionPending:  0,
		SubmissionApproved: 0,
		SubmissionRejected: 0,
	}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ImagePath returns the staged file for a submission, for serving previews.
// Approved submissions point at the project images folder instead.
func (s *SubmissionStore) ImagePath(id string) (string, error) {
	sub, err := s.Get(id)
	if err != nil {
		return "", err
	}
	name := sub.ImageName
	dir := s.stagingDir
	if sub.ImageRef != "" {
		name = sub.ImageRef
		dir = s.imagesDir
	}
	path, err := safeImagePath(dir, name)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: submission image %s", ErrNotFound, name)
		}
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%w: submission image %s", ErrNotFound, name)
	}
	return path, nil
}

// SaveLabels replaces the boxes of a draft, pending, or rejected submission.
// The class list of the project is the only authority for validation, so a
// student cannot invent class ids.
func (s *SubmissionStore) SaveLabels(id string, labels []Label) error {
	sub, err := s.Get(id)
	if err != nil {
		return err
	}
	if sub.Status != SubmissionDraft && sub.Status != SubmissionPending && sub.Status != SubmissionRejected {
		return fmt.Errorf("%w: this submission was already approved", ErrForbidden)
	}
	classCount, err := s.classCount()
	if err != nil {
		return err
	}
	for i, l := range labels {
		if err := validateLabel(l, classCount); err != nil {
			return fmt.Errorf("%w: box %d: %v", ErrInvalid, i+1, err)
		}
	}
	encoded, err := json.Marshal(labels)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE submissions SET labels_json = ? WHERE id = ? AND project_id = ?`,
		string(encoded), id, s.projectID)
	return err
}

// Submit moves a draft or a rejected submission back into the teacher's queue.
func (s *SubmissionStore) Submit(id, studentID string) (Submission, error) {
	sub, err := s.Get(id)
	if err != nil {
		return Submission{}, err
	}
	if sub.StudentID != studentID {
		return Submission{}, fmt.Errorf("%w: only the author can submit this work", ErrForbidden)
	}
	if sub.Status != SubmissionDraft && sub.Status != SubmissionRejected {
		if sub.Status == SubmissionPending {
			return sub, nil
		}
		return Submission{}, fmt.Errorf("%w: this submission was already approved", ErrForbidden)
	}
	classCount, err := s.classCount()
	if err != nil {
		return Submission{}, err
	}
	if classCount == 0 {
		return Submission{}, fmt.Errorf("%w: the teacher has not defined classes for this project yet", ErrInvalid)
	}
	if _, err := s.db.Exec(`UPDATE submissions SET status = ?, submitted_at = ?, review_note = '', reviewed_at = '', reviewed_by = '', image_id = '', image_ref = ''
		WHERE id = ? AND project_id = ? AND student_id = ?`,
		SubmissionPending, formatTime(time.Now().UTC()), id, s.projectID, studentID); err != nil {
		return Submission{}, err
	}
	return s.Get(id)
}

// Withdraw pulls a pending submission back to draft before it is reviewed.
func (s *SubmissionStore) Withdraw(id, studentID string) (Submission, error) {
	sub, err := s.Get(id)
	if err != nil {
		return Submission{}, err
	}
	if sub.Status == SubmissionDraft {
		return sub, nil
	}
	if sub.Status != SubmissionPending {
		return Submission{}, fmt.Errorf("%w: this submission was already reviewed", ErrForbidden)
	}
	if _, err := s.db.Exec(`UPDATE submissions SET status = ?, submitted_at = ''
		WHERE id = ? AND project_id = ? AND student_id = ?`,
		SubmissionDraft, id, s.projectID, studentID); err != nil {
		return Submission{}, err
	}
	return s.Get(id)
}

// Delete removes a student's own draft or rejected submission, including its
// staged file. Pending or approved work must not disappear silently.
func (s *SubmissionStore) Delete(id, studentID string) error {
	sub, err := s.Get(id)
	if err != nil {
		return err
	}
	if sub.Status != SubmissionDraft && sub.Status != SubmissionRejected {
		return fmt.Errorf("%w: only drafts or rejected work can be deleted", ErrInvalid)
	}
	res, err := s.db.Exec(`DELETE FROM submissions WHERE id = ? AND project_id = ? AND student_id = ?`,
		id, s.projectID, studentID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: submission %s", ErrNotFound, id)
	}
	return s.removeStaged(sub.ImageName)
}

// Approve moves the photo into the project images, writes its boxes, and records
// the decision. The student keeps credit as the uploader.
func (s *SubmissionStore) Approve(id, reviewerID, note string) (Submission, ImageInfo, error) {
	note, err := cleanNote(note)
	if err != nil {
		return Submission{}, ImageInfo{}, err
	}
	var img ImageInfo
	err = s.decide(id, reviewerID, SubmissionApproved, note, func(sub Submission) error {
		created, err := s.commit(sub, false, reviewerID)
		img = created
		return err
	})
	if err != nil {
		return Submission{}, ImageInfo{}, err
	}
	out, err := s.Get(id)
	return out, img, err
}

// Reject records the teacher's decision and reason. The photo stays in the
// staging area so the student can read the feedback, fix the boxes, and resubmit.
func (s *SubmissionStore) Reject(id, reviewerID, note string) (Submission, ImageInfo, error) {
	note, err := cleanNote(note)
	if err != nil {
		return Submission{}, ImageInfo{}, err
	}
	if note == "" {
		return Submission{}, ImageInfo{}, fmt.Errorf("%w: a reason is required when rejecting", ErrInvalid)
	}
	err = s.decide(id, reviewerID, SubmissionRejected, note, func(sub Submission) error {
		return nil
	})
	if err != nil {
		return Submission{}, ImageInfo{}, err
	}
	out, err := s.Get(id)
	return out, ImageInfo{}, err
}

func (s *SubmissionStore) decide(id, reviewerID, status, note string, apply func(Submission) error) error {
	sub, err := s.Get(id)
	if err != nil {
		return err
	}
	if sub.Status != SubmissionPending {
		if sub.Status == SubmissionDraft {
			return fmt.Errorf("%w: the student has not submitted this photo yet", ErrInvalid)
		}
		return fmt.Errorf("%w: already reviewed as %s", ErrInvalid, sub.Status)
	}
	if err := apply(sub); err != nil {
		return err
	}
	reviewed := formatTime(time.Now().UTC())
	res, err := s.db.Exec(`UPDATE submissions SET status = ?, review_note = ?, reviewed_at = ?, reviewed_by = ?
		WHERE id = ? AND project_id = ? AND status = ?`,
		status, note, reviewed, reviewerID, id, s.projectID, SubmissionPending)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: submission %s changed while reviewing", ErrNotFound, id)
	}
	return nil
}

// commit copies the staged photo into the project images folder and writes the
// boxes that came with it. When trashed is true the image lands in the project
// trash instead of the active list.
func (s *SubmissionStore) commit(sub Submission, trashed bool, reviewerID string) (ImageInfo, error) {
	ext := strings.ToLower(filepath.Ext(sub.ImageName))
	if _, ok := imageExts[ext]; !ok {
		return ImageInfo{}, fmt.Errorf("%w: unsupported image type %q", ErrInvalid, ext)
	}
	imageID, err := newImageID()
	if err != nil {
		return ImageInfo{}, err
	}
	stored := imageID + ext

	src, err := s.stagingPath(sub.ImageName)
	if err != nil {
		return ImageInfo{}, err
	}
	dst := filepath.Join(s.imagesDir, stored)
	if err := moveFile(src, dst); err != nil {
		return ImageInfo{}, err
	}

	info, err := os.Stat(dst)
	if err != nil {
		return ImageInfo{}, err
	}

	created := formatTime(time.Now().UTC())
	deleted := ""
	if trashed {
		deleted = created
	}
	if _, err := s.db.Exec(`INSERT INTO images
		(id, project_id, name, original_name, size, created_at, uploaded_by, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		imageID, s.projectID, stored, sub.OriginalName, info.Size(), created, sub.StudentID, deleted); err != nil {
		// Put the file back where it came from so the submission is not lost.
		_ = moveFile(dst, src)
		return ImageInfo{}, err
	}

	if len(sub.Labels) > 0 {
		tx, err := s.db.Begin()
		if err != nil {
			return ImageInfo{}, err
		}
		for i, l := range sub.Labels {
			if _, err := tx.Exec(`INSERT INTO labels
				(project_id, image_name, seq, class_id, x_center, y_center, width, height)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				s.projectID, stored, i, l.ClassID, l.XCenter, l.YCenter, l.Width, l.Height); err != nil {
				tx.Rollback()
				_, _ = s.db.Exec(`DELETE FROM images WHERE project_id = ? AND name = ?`, s.projectID, stored)
				_ = moveFile(dst, src)
				return ImageInfo{}, err
			}
		}
		if err := tx.Commit(); err != nil {
			return ImageInfo{}, err
		}
	}

	if _, err := s.db.Exec(`UPDATE submissions SET image_id = ?, image_ref = ? WHERE id = ?`,
		imageID, stored, sub.ID); err != nil {
		return ImageInfo{}, err
	}
	return ImageInfo{
		ID:             imageID,
		Name:           stored,
		OriginalName:   sub.OriginalName,
		UploadedBy:     sub.StudentID,
		UploadedByName: sub.StudentName,
		CreatedAt:      parseTime(created),
		LabelCount:     len(sub.Labels),
	}, nil
}

func (s *SubmissionStore) removeStaged(imageName string) error {
	path, err := s.stagingPath(imageName)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RemoveProjectSubmissions deletes staged files that belong to a project being
// removed. The rows themselves disappear with the foreign key cascade.
func (m *Manager) removeStagedFiles(projectID string) error {
	dir, err := m.projectDir(projectID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	staging := filepath.Join(dir, "staging")
	entries, err := os.ReadDir(staging)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(staging, e.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// PendingAcrossProjects summarises the review queue for one teacher, so the
// pending page can group submissions by project without a query per project.
type PendingGroup struct {
	Project     ProjectInfo  `json:"project"`
	Submissions []Submission `json:"submissions"`
}

// PendingSubmissions returns pending submissions of every project the account
// may review. Teachers get their own projects; students get nothing here.
func (m *Manager) PendingSubmissions(userID string) ([]PendingGroup, error) {
	u, err := m.User(userID)
	if err != nil {
		return nil, err
	}
	if u.Role != auth.RoleTeacher {
		return []PendingGroup{}, nil
	}
	projects, err := m.ownedProjects(userID)
	if err != nil {
		return nil, err
	}
	out := []PendingGroup{}
	for _, info := range projects {
		store, err := m.submissions(info.ID)
		if err != nil {
			return nil, err
		}
		list, err := store.List("", SubmissionPending)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			continue
		}
		out = append(out, PendingGroup{Project: info, Submissions: list})
	}
	return out, nil
}

// Submissions returns the submission store of one project.
func (m *Manager) Submissions(id string) (*SubmissionStore, error) {
	return m.submissions(id)
}

func (m *Manager) submissions(id string) (*SubmissionStore, error) {
	dir, err := m.projectDir(id)
	if err != nil {
		return nil, err
	}
	return newSubmissionStore(m.db, id, dir)
}

// MySubmissions lists one student's submissions across every project they are in.
type MyGroup struct {
	Project     ProjectInfo  `json:"project"`
	Submissions []Submission `json:"submissions"`
}

// MySubmissions powers the student page: their own drafts, submissions waiting
// for review, and decisions with the teacher's reason.
func (m *Manager) MySubmissions(studentID string) ([]MyGroup, error) {
	var one int
	if err := m.db.QueryRow(`SELECT 1 FROM users WHERE id = ? AND role = ?`,
		studentID, auth.RoleStudent).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return []MyGroup{}, nil
	} else if err != nil {
		return nil, err
	}

	projects, err := m.memberProjects(studentID)
	if err != nil {
		return nil, err
	}

	out := []MyGroup{}
	for _, info := range projects {
		store, err := m.submissions(info.ID)
		if err != nil {
			return nil, err
		}
		list, err := store.List(studentID)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 && !info.SubmissionsOpen {
			continue
		}
		out = append(out, MyGroup{Project: info, Submissions: list})
	}
	return out, nil
}

func (s *SubmissionStore) classCount() (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM classes WHERE project_id = ?`, s.projectID).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

const submissionSelect = `
	SELECT sm.id, sm.project_id, sm.student_id,
		COALESCE(su.display_name, ''), COALESCE(su.username, ''),
		sm.image_name, sm.original_name, sm.note, sm.status, sm.review_note,
		sm.labels_json, sm.created_at, sm.submitted_at, sm.reviewed_at,
		COALESCE(ru.display_name, ''), sm.image_id, sm.image_ref
	FROM submissions sm
	LEFT JOIN users su ON su.id = sm.student_id
	LEFT JOIN users ru ON ru.id = sm.reviewed_by
	`

func scanSubmission(row scanner) (Submission, error) {
	var (
		sub        Submission
		labelsJSON string
		created    string
		submitted  string
		reviewed   string
	)
	err := row.Scan(
		&sub.ID, &sub.ProjectID, &sub.StudentID, &sub.StudentName, &sub.StudentUser,
		&sub.ImageName, &sub.OriginalName, &sub.Note, &sub.Status, &sub.ReviewNote,
		&labelsJSON, &created, &submitted, &reviewed, &sub.ReviewName, &sub.ImageID, &sub.ImageRef,
	)
	if err != nil {
		return Submission{}, err
	}
	sub.CreatedAt = parseTime(created)
	sub.SubmittedAt = parseTime(submitted)
	sub.ReviewedAt = parseTime(reviewed)
	sub.Labels = []Label{}
	if labelsJSON != "" && labelsJSON != "[]" {
		if err := json.Unmarshal([]byte(labelsJSON), &sub.Labels); err != nil {
			return Submission{}, fmt.Errorf("%w: stored boxes are corrupt: %v", ErrInvalid, err)
		}
	}
	sub.BoxCount = len(sub.Labels)
	return sub, nil
}

func cleanNote(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len([]rune(raw)) > MaxSubmissionNote {
		return "", fmt.Errorf("%w: note is longer than %d characters", ErrInvalid, MaxSubmissionNote)
	}
	return raw, nil
}

func validSubmissionID(id string) bool {
	if len(id) == 0 || id != filepath.Base(id) {
		return false
	}
	for _, r := range id {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		case r == '-':
		default:
			return false
		}
	}
	return true
}

func validSubmissionStatus(status string) bool {
	switch status {
	case SubmissionDraft, SubmissionPending, SubmissionApproved, SubmissionRejected:
		return true
	}
	return false
}

// moveFile renames across filesystems by copying when a plain rename fails.
func moveFile(from, to string) error {
	if err := os.Rename(from, to); err == nil {
		return nil
	}
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(to)
		return err
	}
	if err := dst.Close(); err != nil {
		os.Remove(to)
		return err
	}
	if err := src.Close(); err != nil {
		return err
	}
	return os.Remove(from)
}
