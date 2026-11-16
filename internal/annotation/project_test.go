package annotation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yoloapi/internal/auth"
)

func TestManagerProjects(t *testing.T) {
	manager := newTestManager(t)

	list, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("projects = %+v", list)
	}

	info, err := manager.Create("Fish study")
	if err != nil {
		t.Fatal(err)
	}
	if !validProjectID(info.ID) || info.Name != "Fish study" {
		t.Fatalf("project = %+v", info)
	}
	if info.ImageCount != 0 || info.BoxCount != 0 || info.ClassCount != 0 {
		t.Fatalf("counts = %+v", info)
	}

	store, err := manager.Store(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveClasses([]string{"fish", "leaf"}); err != nil {
		t.Fatal(err)
	}
	name, err := store.AddImage("a.jpg", seedOwnerID, strings.NewReader("img"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLabels(name, []Label{{ClassID: 1, XCenter: 0.5, YCenter: 0.5, Width: 0.2, Height: 0.2}}); err != nil {
		t.Fatal(err)
	}

	info, err = manager.Info(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info.ImageCount != 1 || info.BoxCount != 1 || info.ClassCount != 2 {
		t.Fatalf("counts = %+v", info)
	}

	renamed, err := manager.Rename(info.ID, "Fish 2026")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "Fish 2026" {
		t.Fatalf("renamed = %+v", renamed)
	}

	list, err = manager.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "Fish 2026" {
		t.Fatalf("projects = %+v", list)
	}

	if err := manager.Delete(info.ID); err != nil {
		t.Fatal(err)
	}
	var leftover int
	if err := manager.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM projects) +
		(SELECT COUNT(*) FROM classes) +
		(SELECT COUNT(*) FROM images) +
		(SELECT COUNT(*) FROM labels)`).Scan(&leftover); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Fatalf("project delete left %d rows behind", leftover)
	}
	if _, err := manager.Store(info.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	list, err = manager.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("projects after delete = %+v", list)
	}
}

func TestUsersOwnershipAndUploader(t *testing.T) {
	manager := newTestManager(t)

	users, err := manager.Users()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].ID != seedOwnerID || users[1].ID != seedMemberID {
		t.Fatalf("users = %+v", users)
	}

	member, err := manager.User(seedMemberID)
	if err != nil {
		t.Fatal(err)
	}
	if member.Username != seedMemberUsername || member.DisplayName != seedMemberName {
		t.Fatalf("member = %+v", member)
	}
	if member.Role != auth.RoleStudent {
		t.Fatalf("seeded member role = %q want student", member.Role)
	}
	if _, err := manager.User("00000000000000ff"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected unknown user to be not found, got %v", err)
	}

	// Students cannot create projects; teachers can.
	if _, err := manager.CreateForOwner("Member project", member.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("student creating a project: got %v want forbidden", err)
	}
	secondTeacher, err := manager.NewUser("teacher02", "อาจารย์ที่สอง", auth.RoleTeacher, "password123")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.CheckTeacher(member.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("student should fail the teacher check: %v", err)
	}
	if err := manager.CheckTeacher(secondTeacher.ID); err != nil {
		t.Fatalf("teacher check failed for a teacher: %v", err)
	}

	info, err := manager.CreateForOwner("Second teacher project", secondTeacher.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info.OwnerID != secondTeacher.ID || info.OwnerName != secondTeacher.DisplayName ||
		info.OwnerUsername != secondTeacher.Username {
		t.Fatalf("project owner = %+v", info)
	}
	if err := manager.CheckOwner(info.ID, seedOwnerID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner should be forbidden, got %v", err)
	}
	if err := manager.CheckOwner(info.ID, secondTeacher.ID); err != nil {
		t.Fatalf("owner should be allowed: %v", err)
	}

	store, err := manager.Store(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.AddImage("photo.jpg", seedOwnerID, strings.NewReader("a"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AddImage("photo.jpg", member.ID, strings.NewReader("b"))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("uploads with the same name must get different stored names: %q", first)
	}

	images, err := store.Images()
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 {
		t.Fatalf("images = %+v", images)
	}
	uploaders := map[string]string{}
	for _, img := range images {
		if img.OriginalName != "photo.jpg" || img.ID == "" {
			t.Fatalf("image = %+v", img)
		}
		uploaders[img.Name] = img.UploadedBy
	}
	if uploaders[first] != seedOwnerID || uploaders[second] != member.ID {
		t.Fatalf("uploaders = %v", uploaders)
	}
}

func TestManagerRejectsBadNamesAndIDs(t *testing.T) {
	manager := newTestManager(t)
	if _, err := manager.Create("  "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid name, got %v", err)
	}
	if _, err := manager.Store("../etc"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid id, got %v", err)
	}
	if _, err := manager.Store("0000000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestImportLegacyAnnotations(t *testing.T) {
	base := t.TempDir()
	legacyDir := filepath.Join(base, "annotations")
	writeFile(t, filepath.Join(legacyDir, "classes.txt"), "cat\ndog\n")
	writeFile(t, filepath.Join(legacyDir, "images", "a.jpg"), "img")
	writeFile(t, filepath.Join(legacyDir, "labels", "a.txt"), "1 0.5 0.4 0.2 0.3\n")

	manager, err := NewManager(filepath.Join(base, "annotations.db"), filepath.Join(base, "projects"))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	info, imported, err := manager.ImportLegacy(legacyDir, "Default")
	if err != nil {
		t.Fatal(err)
	}
	if !imported {
		t.Fatal("expected legacy data to be imported")
	}
	if info.ImageCount != 1 || info.BoxCount != 1 || info.ClassCount != 2 {
		t.Fatalf("info = %+v", info)
	}

	store, err := manager.Store(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	classes, err := store.Classes()
	if err != nil {
		t.Fatal(err)
	}
	if len(classes) != 2 || classes[1] != "dog" {
		t.Fatalf("classes = %#v", classes)
	}
	labels, err := store.LoadLabels("a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 1 || labels[0].ClassID != 1 {
		t.Fatalf("labels = %+v", labels)
	}

	// Legacy data is kept as a backup.
	if _, err := os.Stat(filepath.Join(legacyDir, "images", "a.jpg")); err != nil {
		t.Fatalf("legacy image should remain: %v", err)
	}
}

func TestImportLegacyEmpty(t *testing.T) {
	base := t.TempDir()
	legacyDir := filepath.Join(base, "annotations")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(filepath.Join(base, "annotations.db"), filepath.Join(base, "projects"))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	_, imported, err := manager.ImportLegacy(legacyDir, "Default")
	if err != nil {
		t.Fatal(err)
	}
	if imported {
		t.Fatal("empty legacy folder should not create a project")
	}
	list, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("projects = %+v", list)
	}
}

func TestMigrateFolderProjects(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "projects")
	id := "0123456789abcdef"
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(filepath.Join(dir, "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "labels"), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := fmt.Sprintf(`{"id":%q,"name":"Old project","created_at":"2026-01-02T03:04:05Z"}`, id)
	writeFile(t, filepath.Join(dir, "project.json"), meta)
	writeFile(t, filepath.Join(dir, "classes.txt"), "cat\ndog\n")
	writeFile(t, filepath.Join(dir, "images", "a.jpg"), "img")
	writeFile(t, filepath.Join(dir, "labels", "a.txt"), "1 0.5 0.4 0.2 0.3\n")

	manager, err := NewManager(filepath.Join(base, "annotations.db"), root)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	info, err := manager.Info(id)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "Old project" || info.ImageCount != 1 || info.BoxCount != 1 || info.ClassCount != 2 {
		t.Fatalf("info = %+v", info)
	}
	if info.CreatedAt.Year() != 2026 {
		t.Fatalf("created_at = %v", info.CreatedAt)
	}

	store, err := manager.Store(id)
	if err != nil {
		t.Fatal(err)
	}
	labels, err := store.LoadLabels("a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 1 || labels[0].ClassID != 1 {
		t.Fatalf("labels = %+v", labels)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
