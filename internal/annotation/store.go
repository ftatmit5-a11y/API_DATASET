// Package annotation stores YOLO annotations. Projects, classes, image records,
// and bounding boxes live in SQLite; the image files themselves stay on disk.
package annotation

import (
	"archive/zip"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrInvalid is returned when input does not pass validation.
	ErrInvalid = errors.New("invalid input")
	// ErrNotFound is returned when an image or class list entry is missing.
	ErrNotFound = errors.New("not found")
	// ErrForbidden is returned when the current user is not allowed to change
	// a project or one of its images.
	ErrForbidden = errors.New("forbidden")
)

const (
	maxImageBytes = 64 << 20 // 64 MiB per image
	coordEpsilon  = 1e-6
	maxClasses    = 200 // a project class list stays short enough to click through
)

var imageExts = map[string]struct{}{
	".jpg":  {},
	".jpeg": {},
	".png":  {},
	".bmp":  {},
	".webp": {},
	".tif":  {},
	".tiff": {},
	".gif":  {},
}

// Label is one YOLO bounding box in normalized coordinates.
type Label struct {
	ClassID int     `json:"class_id"`
	XCenter float64 `json:"x_center"`
	YCenter float64 `json:"y_center"`
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
}

// ImageInfo describes one uploaded image, who uploaded it, and how many boxes
// it has. Name is the unique on-disk filename; OriginalName is what the user
// uploaded.
type ImageInfo struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	OriginalName   string     `json:"original_name"`
	UploadedBy     string     `json:"uploaded_by"`
	UploadedByName string     `json:"uploaded_by_name"`
	CreatedAt      time.Time  `json:"created_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
	LabelCount     int        `json:"label_count"`
}

// State is everything the annotation UI needs to render.
type State struct {
	Classes []string    `json:"classes"`
	Images  []ImageInfo `json:"images"`
	Trash   []ImageInfo `json:"trash"`
}

// Store keeps one project's classes, image records, and labels in SQLite while
// the image files live under <project>/images.
type Store struct {
	db        *sql.DB
	projectID string
	dir       string
}

func newStore(db *sql.DB, projectID, dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "images"), 0o755); err != nil {
		return nil, err
	}
	return &Store{db: db, projectID: projectID, dir: dir}, nil
}

// ImagesDir is where uploaded image files live.
func (s *Store) ImagesDir() string { return filepath.Join(s.dir, "images") }

// State returns the class list, active images, and trashed images.
func (s *Store) State() (State, error) {
	classes, err := s.Classes()
	if err != nil {
		return State{}, err
	}
	images, err := s.Images()
	if err != nil {
		return State{}, err
	}
	trash, err := s.TrashedImages()
	if err != nil {
		return State{}, err
	}
	return State{Classes: classes, Images: images, Trash: trash}, nil
}

// Images lists active (not trashed) images with their box counts, sorted by
// display name.
func (s *Store) Images() ([]ImageInfo, error) {
	rows, err := s.db.Query(`
		SELECT i.id, i.name, i.original_name, i.uploaded_by,
			COALESCE(u.display_name, ''), i.created_at, i.deleted_at,
			(SELECT COUNT(*) FROM labels l
			 WHERE l.project_id = i.project_id AND l.image_name = i.name)
		FROM images i
		LEFT JOIN users u ON u.id = i.uploaded_by
		WHERE i.project_id = ? AND i.deleted_at = ''
		ORDER BY lower(i.original_name), lower(i.name)`, s.projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ImageInfo{}
	for rows.Next() {
		img, err := scanImage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, img)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// TrashedImages lists every image in the trash, most recently deleted first.
func (s *Store) TrashedImages() ([]ImageInfo, error) {
	return s.TrashedImagesBy("")
}

// TrashedImagesBy lists trashed images. An uploader of "" means every account;
// otherwise only photos uploaded by that user are returned, which is what a
// student is allowed to see of a project's trash.
func (s *Store) TrashedImagesBy(uploader string) ([]ImageInfo, error) {
	query := `
		SELECT i.id, i.name, i.original_name, i.uploaded_by,
			COALESCE(u.display_name, ''), i.created_at, i.deleted_at,
			(SELECT COUNT(*) FROM labels l
			 WHERE l.project_id = i.project_id AND l.image_name = i.name)
		FROM images i
		LEFT JOIN users u ON u.id = i.uploaded_by
		WHERE i.project_id = ? AND i.deleted_at <> ''`
	args := []any{s.projectID}
	if uploader != "" {
		query += ` AND i.uploaded_by = ?`
		args = append(args, uploader)
	}
	query += ` ORDER BY i.deleted_at DESC, lower(i.original_name), lower(i.name)`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ImageInfo{}
	for rows.Next() {
		img, err := scanImage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, img)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Image returns one image record by its stored filename, whether it is active
// or in the trash.
func (s *Store) Image(name string) (ImageInfo, error) {
	if !validImageName(name) {
		return ImageInfo{}, fmt.Errorf("%w: bad image name", ErrInvalid)
	}
	row := s.db.QueryRow(`
		SELECT i.id, i.name, i.original_name, i.uploaded_by,
			COALESCE(u.display_name, ''), i.created_at, i.deleted_at,
			(SELECT COUNT(*) FROM labels l
			 WHERE l.project_id = i.project_id AND l.image_name = i.name)
		FROM images i
		LEFT JOIN users u ON u.id = i.uploaded_by
		WHERE i.project_id = ? AND i.name = ?`, s.projectID, name)
	img, err := scanImage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ImageInfo{}, fmt.Errorf("%w: image %s", ErrNotFound, name)
	}
	if err != nil {
		return ImageInfo{}, err
	}
	return img, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanImage(row scanner) (ImageInfo, error) {
	var (
		img     ImageInfo
		created string
		deleted string
	)
	if err := row.Scan(&img.ID, &img.Name, &img.OriginalName, &img.UploadedBy,
		&img.UploadedByName, &created, &deleted, &img.LabelCount); err != nil {
		return ImageInfo{}, err
	}
	img.CreatedAt = parseTime(created)
	if deleted != "" {
		if t := parseTime(deleted); !t.IsZero() {
			img.DeletedAt = &t
		}
	}
	return img, nil
}

// AddImage saves an uploaded image under a new UUID filename and records who
// uploaded it. The original filename is kept for display only.
func (s *Store) AddImage(filename, uploadedBy string, r io.Reader) (string, error) {
	original := sanitizeFilename(filename)
	ext := strings.ToLower(filepath.Ext(original))
	if _, ok := imageExts[ext]; !ok {
		return "", fmt.Errorf("%w: unsupported image type %q", ErrInvalid, ext)
	}
	if uploadedBy == "" {
		uploadedBy = DefaultUserID
	}
	id, err := newImageID()
	if err != nil {
		return "", err
	}
	name := id + ext
	if err := s.saveImage(id, name, original, uploadedBy, r); err != nil {
		return "", err
	}
	return name, nil
}

// importImage stores a file under its existing name. It is only used when
// migrating folder-based projects whose names are already unique.
func (s *Store) importImage(name, originalName, uploadedBy string, r io.Reader) (string, error) {
	name = sanitizeFilename(name)
	originalName = sanitizeFilename(originalName)
	ext := strings.ToLower(filepath.Ext(name))
	if _, ok := imageExts[ext]; !ok {
		return "", fmt.Errorf("%w: unsupported image type %q", ErrInvalid, ext)
	}
	if uploadedBy == "" {
		uploadedBy = DefaultUserID
	}
	id, err := newImageID()
	if err != nil {
		return "", err
	}
	if err := s.saveImage(id, name, originalName, uploadedBy, r); err != nil {
		return "", err
	}
	return name, nil
}

func (s *Store) saveImage(id, name, originalName, uploadedBy string, r io.Reader) error {
	target := filepath.Join(s.ImagesDir(), name)
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(f, io.LimitReader(r, maxImageBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(target)
		return copyErr
	}
	if closeErr != nil {
		os.Remove(target)
		return closeErr
	}
	if n > maxImageBytes {
		os.Remove(target)
		return fmt.Errorf("%w: image %s exceeds %d MiB", ErrInvalid, name, maxImageBytes>>20)
	}

	if _, err := s.db.Exec(`INSERT INTO images
		(id, project_id, name, original_name, size, created_at, uploaded_by)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, s.projectID, name, originalName, n, formatTime(time.Now().UTC()), uploadedBy); err != nil {
		os.Remove(target)
		return err
	}
	return nil
}

// ImagePath validates name and returns the absolute path of the image.
func (s *Store) ImagePath(name string) (string, error) {
	if !validImageName(name) {
		return "", fmt.Errorf("%w: bad image name", ErrInvalid)
	}
	p := filepath.Join(s.ImagesDir(), name)
	info, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: image %s", ErrNotFound, name)
		}
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%w: image %s", ErrNotFound, name)
	}
	return p, nil
}

// TrashImage soft-deletes an image by marking it as deleted. The file and its
// labels are kept so the image can be restored later.
func (s *Store) TrashImage(name string) error {
	if _, err := s.Image(name); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE images SET deleted_at = ? WHERE project_id = ? AND name = ?`,
		formatTime(time.Now().UTC()), s.projectID, name)
	return err
}

// RestoreImage brings a trashed image back into the active list.
func (s *Store) RestoreImage(name string) error {
	img, err := s.Image(name)
	if err != nil {
		return err
	}
	if img.DeletedAt == nil {
		return nil
	}
	_, err = s.db.Exec(`UPDATE images SET deleted_at = '' WHERE project_id = ? AND name = ?`,
		s.projectID, name)
	return err
}

// DeleteImage permanently removes the image file and its label rows. It is used
// when an image is purged from the trash.
func (s *Store) DeleteImage(name string) error {
	if _, err := s.ImagePath(name); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM labels WHERE project_id = ? AND image_name = ?`, s.projectID, name); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM images WHERE project_id = ? AND name = ?`, s.projectID, name); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(s.ImagesDir(), name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// requireActiveImage makes sure name exists and is not in the trash. Label
// editing is only allowed on active images.
func (s *Store) requireActiveImage(name string) error {
	if _, err := s.ImagePath(name); err != nil {
		return err
	}
	var deleted string
	err := s.db.QueryRow(`SELECT deleted_at FROM images WHERE project_id = ? AND name = ?`,
		s.projectID, name).Scan(&deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: image %s", ErrNotFound, name)
	}
	if err != nil {
		return err
	}
	if deleted != "" {
		return fmt.Errorf("%w: image %s is in the trash", ErrNotFound, name)
	}
	return nil
}

// LoadLabels reads the bounding boxes stored for an active image.
func (s *Store) LoadLabels(name string) ([]Label, error) {
	if err := s.requireActiveImage(name); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT class_id, x_center, y_center, width, height
		FROM labels
		WHERE project_id = ? AND image_name = ?
		ORDER BY seq`, s.projectID, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	labels := []Label{}
	for rows.Next() {
		var l Label
		if err := rows.Scan(&l.ClassID, &l.XCenter, &l.YCenter, &l.Width, &l.Height); err != nil {
			return nil, err
		}
		labels = append(labels, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return labels, nil
}

// SaveLabels validates and replaces the bounding boxes for an active image.
func (s *Store) SaveLabels(name string, labels []Label) error {
	if err := s.requireActiveImage(name); err != nil {
		return err
	}
	classes, err := s.Classes()
	if err != nil {
		return err
	}
	// Validate everything before touching the database.
	for i, l := range labels {
		if err := validateLabel(l, len(classes)); err != nil {
			return fmt.Errorf("%w: box %d: %v", ErrInvalid, i+1, err)
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM labels WHERE project_id = ? AND image_name = ?`, s.projectID, name); err != nil {
		return err
	}
	for i, l := range labels {
		if _, err := tx.Exec(`INSERT INTO labels
			(project_id, image_name, seq, class_id, x_center, y_center, width, height)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			s.projectID, name, i, l.ClassID, l.XCenter, l.YCenter, l.Width, l.Height); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Classes returns the project class list ordered by class id.
func (s *Store) Classes() ([]string, error) {
	rows, err := s.db.Query(`SELECT name FROM classes WHERE project_id = ? ORDER BY class_id`, s.projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

// cleanClassNames trims and validates a class list, rejecting blanks and
// case-insensitive duplicates. Class ids are positions in the returned slice.
func cleanClassNames(names []string) ([]string, error) {
	cleaned := make([]string, 0, len(names))
	seen := map[string]struct{}{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			return nil, fmt.Errorf("%w: class name must not be empty", ErrInvalid)
		}
		if strings.ContainsAny(n, "\r\n") {
			return nil, fmt.Errorf("%w: class name must be one line", ErrInvalid)
		}
		if len([]rune(n)) > 80 {
			return nil, fmt.Errorf("%w: class name %q is too long", ErrInvalid, n)
		}
		key := strings.ToLower(n)
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("%w: duplicate class name %q", ErrInvalid, n)
		}
		seen[key] = struct{}{}
		cleaned = append(cleaned, n)
	}
	return cleaned, nil
}

// SaveClasses replaces the class list after checking that existing labels still
// reference valid class ids.
func (s *Store) SaveClasses(names []string) error {
	cleaned, err := cleanClassNames(names)
	if err != nil {
		return err
	}

	old, err := s.Classes()
	if err != nil {
		return err
	}
	used, err := s.usedClassIDs()
	if err != nil {
		return err
	}
	prefix := commonPrefixLen(old, cleaned)
	for id := range used {
		if id < 0 {
			continue
		}
		if id >= len(cleaned) {
			return fmt.Errorf("%w: class %d is still used by labels", ErrInvalid, id)
		}
		if len(cleaned) < len(old) && id >= prefix {
			return fmt.Errorf("%w: cannot remove class %d while labels use it", ErrInvalid, id)
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM classes WHERE project_id = ?`, s.projectID); err != nil {
		return err
	}
	for id, name := range cleaned {
		if _, err := tx.Exec(`INSERT INTO classes (project_id, class_id, name) VALUES (?, ?, ?)`,
			s.projectID, id, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ExportZip writes images/, labels/, classes.txt, and manifest.csv into a zip
// archive. Image files are given readable names derived from the original
// upload; a short image id is appended only when two uploads share a name.
func (s *Store) ExportZip(w io.Writer) error {
	zw := zip.NewWriter(w)
	classes, err := s.Classes()
	if err != nil {
		zw.Close()
		return err
	}
	cf, err := zw.Create("classes.txt")
	if err != nil {
		zw.Close()
		return err
	}
	for _, name := range classes {
		if _, err := fmt.Fprintln(cf, name); err != nil {
			zw.Close()
			return err
		}
	}

	images, err := s.Images()
	if err != nil {
		zw.Close()
		return err
	}
	exported := exportFileNames(images)

	mf, err := zw.Create("manifest.csv")
	if err != nil {
		zw.Close()
		return err
	}
	cw := csv.NewWriter(mf)
	if err := cw.Write([]string{"image", "original_name", "uploaded_by", "uploaded_by_name", "uploaded_at"}); err != nil {
		zw.Close()
		return err
	}
	for _, img := range images {
		uploadedAt := ""
		if !img.CreatedAt.IsZero() {
			uploadedAt = img.CreatedAt.Format(time.RFC3339)
		}
		if err := cw.Write([]string{
			exported[img.Name], img.OriginalName, img.UploadedBy, img.UploadedByName, uploadedAt,
		}); err != nil {
			zw.Close()
			return err
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		zw.Close()
		return err
	}

	for _, img := range images {
		exportName := exported[img.Name]
		imgPath := filepath.Join(s.ImagesDir(), img.Name)
		if err := addFileToZip(zw, "images/"+exportName, imgPath); err != nil {
			zw.Close()
			return err
		}
		labels, err := s.LoadLabels(img.Name)
		if err != nil {
			zw.Close()
			return err
		}
		if len(labels) == 0 {
			continue
		}
		lf, err := zw.Create("labels/" + labelFileName(exportName))
		if err != nil {
			zw.Close()
			return err
		}
		for _, l := range labels {
			if _, err := fmt.Fprintf(lf, "%d %s %s %s %s\n",
				l.ClassID, formatCoord(l.XCenter), formatCoord(l.YCenter),
				formatCoord(l.Width), formatCoord(l.Height)); err != nil {
				zw.Close()
				return err
			}
		}
	}
	return zw.Close()
}

// exportFileNames maps stored filenames to unique, human-readable names. The
// first upload of "photo.jpg" keeps that name; later duplicates get a short
// image id suffix.
func exportFileNames(images []ImageInfo) map[string]string {
	used := map[string]struct{}{}
	out := make(map[string]string, len(images))
	for _, img := range images {
		base := sanitizeFilename(img.OriginalName)
		if base == "" || !isImage(base) {
			base = img.Name
		}
		ext := filepath.Ext(base)
		stem := strings.TrimSuffix(base, ext)
		candidate := base
		if _, taken := used[strings.ToLower(stem)]; taken {
			suffix := strings.ReplaceAll(img.ID, "-", "")
			if len(suffix) > 8 {
				suffix = suffix[:8]
			}
			if suffix == "" {
				suffix = "dup"
			}
			for i := 1; ; i++ {
				if i == 1 {
					candidate = fmt.Sprintf("%s_%s%s", stem, suffix, ext)
				} else {
					candidate = fmt.Sprintf("%s_%s_%d%s", stem, suffix, i, ext)
				}
				key := strings.ToLower(strings.TrimSuffix(candidate, ext))
				if _, taken := used[key]; !taken {
					break
				}
			}
		}
		used[strings.ToLower(strings.TrimSuffix(candidate, ext))] = struct{}{}
		out[img.Name] = candidate
	}
	return out
}

func (s *Store) usedClassIDs() (map[int]struct{}, error) {
	rows, err := s.db.Query(`SELECT DISTINCT class_id FROM labels WHERE project_id = ?`, s.projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	used := map[int]struct{}{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if id >= 0 {
			used[id] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return used, nil
}

func commonPrefixLen(a, b []string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

func parseLabelLine(text string) (Label, error) {
	fields := strings.Fields(text)
	if len(fields) != 5 {
		return Label{}, fmt.Errorf("expected 5 fields, got %d", len(fields))
	}
	id, err := strconv.Atoi(fields[0])
	if err != nil {
		return Label{}, fmt.Errorf("class id is not an integer")
	}
	values := make([]float64, 4)
	for i := 0; i < 4; i++ {
		v, err := strconv.ParseFloat(fields[i+1], 64)
		if err != nil {
			return Label{}, fmt.Errorf("coordinate %q is not a number", fields[i+1])
		}
		values[i] = v
	}
	return Label{
		ClassID: id,
		XCenter: values[0],
		YCenter: values[1],
		Width:   values[2],
		Height:  values[3],
	}, nil
}

func validateLabel(l Label, classCount int) error {
	if l.ClassID < 0 || l.ClassID >= classCount {
		return fmt.Errorf("class_id %d is outside the class list", l.ClassID)
	}
	for name, v := range map[string]float64{
		"x_center": l.XCenter,
		"y_center": l.YCenter,
	} {
		if v < -coordEpsilon || v > 1+coordEpsilon {
			return fmt.Errorf("%s must be between 0 and 1", name)
		}
	}
	if l.Width <= 0 || l.Width > 1+coordEpsilon {
		return fmt.Errorf("width must be between 0 and 1")
	}
	if l.Height <= 0 || l.Height > 1+coordEpsilon {
		return fmt.Errorf("height must be between 0 and 1")
	}
	if l.XCenter-l.Width/2 < -coordEpsilon || l.XCenter+l.Width/2 > 1+coordEpsilon {
		return fmt.Errorf("box is outside the image horizontally")
	}
	if l.YCenter-l.Height/2 < -coordEpsilon || l.YCenter+l.Height/2 > 1+coordEpsilon {
		return fmt.Errorf("box is outside the image vertically")
	}
	return nil
}

func formatCoord(v float64) string {
	if v < 0 && v > -coordEpsilon {
		v = 0
	}
	s := strconv.FormatFloat(v, 'f', 6, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

func sanitizeFilename(raw string) string {
	raw = strings.ReplaceAll(raw, "\\", "/")
	raw = path.Base(raw)
	raw = strings.Map(func(r rune) rune {
		switch r {
		case '<', '>', ':', '"', '|', '?', '*', 0:
			return '_'
		}
		return r
	}, raw)
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, ".")
	if raw == "" {
		return "image"
	}
	return raw
}

func validImageName(name string) bool {
	if name == "" || name != filepath.Base(name) {
		return false
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return false
	}
	return isImage(name)
}

func isImage(name string) bool {
	_, ok := imageExts[strings.ToLower(filepath.Ext(name))]
	return ok
}

func addFileToZip(zw *zip.Writer, name, source string) error {
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	w, err := zw.CreateHeader(&zip.FileHeader{
		Name:   name,
		Method: zip.Deflate,
	})
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, f); err != nil {
		return err
	}
	return nil
}
