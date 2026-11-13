package annotation

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestStoreRoundTripAndExport(t *testing.T) {
	store := newTestStore(t)
	if err := store.SaveClasses([]string{"fish", "leaf_disease"}); err != nil {
		t.Fatal(err)
	}

	name, err := store.AddImage("photo.jpg", seedOwnerID, strings.NewReader("fake-image"))
	if err != nil {
		t.Fatal(err)
	}
	if !validImageName(name) || !strings.HasSuffix(name, ".jpg") || len(name) != 40 {
		t.Fatalf("name = %q", name)
	}

	labels := []Label{
		{ClassID: 0, XCenter: 0.5, YCenter: 0.5, Width: 0.25, Height: 0.25},
		{ClassID: 1, XCenter: 0.2, YCenter: 0.3, Width: 0.1, Height: 0.2},
	}
	if err := store.SaveLabels(name, labels); err != nil {
		t.Fatal(err)
	}

	got, err := store.LoadLabels(name)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("labels = %+v", got)
	}
	if got[1].ClassID != 1 || got[1].XCenter != 0.2 || got[1].Height != 0.2 {
		t.Fatalf("labels = %+v", got)
	}

	state, err := store.State()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Classes) != 2 || state.Classes[1] != "leaf_disease" {
		t.Fatalf("classes = %#v", state.Classes)
	}
	if len(state.Images) != 1 || state.Images[0].OriginalName != "photo.jpg" ||
		state.Images[0].LabelCount != 2 || state.Images[0].UploadedBy != seedOwnerID {
		t.Fatalf("images = %+v", state.Images)
	}

	var buf bytes.Buffer
	if err := store.ExportZip(&buf); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, f := range zr.File {
		found[f.Name] = true
	}
	for _, want := range []string{"classes.txt", "images/photo.jpg", "labels/photo.txt"} {
		if !found[want] {
			t.Fatalf("export missing %q, got %v", want, found)
		}
	}
}

func TestSaveLabelsValidation(t *testing.T) {
	store := newTestStore(t)
	name, err := store.AddImage("photo.jpg", seedOwnerID, strings.NewReader("img"))
	if err != nil {
		t.Fatal(err)
	}

	// No classes yet.
	err = store.SaveLabels(name, []Label{{0, 0.5, 0.5, 0.2, 0.2}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid class error, got %v", err)
	}

	if err := store.SaveClasses([]string{"fish"}); err != nil {
		t.Fatal(err)
	}
	cases := []Label{
		{ClassID: 3, XCenter: 0.5, YCenter: 0.5, Width: 0.2, Height: 0.2},
		{ClassID: 0, XCenter: 0.5, YCenter: 0.5, Width: 0, Height: 0.2},
		{ClassID: 0, XCenter: 0.9, YCenter: 0.5, Width: 0.4, Height: 0.2},
		{ClassID: 0, XCenter: 1.5, YCenter: 0.5, Width: 0.2, Height: 0.2},
	}
	for _, c := range cases {
		if err := store.SaveLabels(name, []Label{c}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("label %+v: expected invalid, got %v", c, err)
		}
	}
}

func TestClassRemovalChecksLabels(t *testing.T) {
	store := newTestStore(t)
	if err := store.SaveClasses([]string{"a", "b", "c"}); err != nil {
		t.Fatal(err)
	}
	name, err := store.AddImage("photo.jpg", seedOwnerID, strings.NewReader("img"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLabels(name, []Label{{ClassID: 2, XCenter: 0.5, YCenter: 0.5, Width: 0.2, Height: 0.2}}); err != nil {
		t.Fatal(err)
	}

	if err := store.SaveClasses([]string{"a", "b"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("removing used trailing class should fail, got %v", err)
	}
	if err := store.SaveClasses([]string{"a", "c"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("removing class before a used id should fail, got %v", err)
	}
	if err := store.SaveClasses([]string{"a", "b", "c", "d"}); err != nil {
		t.Fatalf("appending a class should work: %v", err)
	}

	if err := store.DeleteImage(name); err != nil {
		t.Fatal(err)
	}
	var leftover int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM labels WHERE project_id = ?`, store.projectID).Scan(&leftover); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Fatalf("image delete left %d label rows behind", leftover)
	}
	if err := store.SaveClasses([]string{"a", "b"}); err != nil {
		t.Fatalf("removing unused class should work: %v", err)
	}
}

func TestImageNamesAreUniqueAndSafe(t *testing.T) {
	store := newTestStore(t)
	first, err := store.AddImage("photo.jpg", seedOwnerID, strings.NewReader("a"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AddImage("photo.png", seedOwnerID, strings.NewReader("b"))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("names must be unique, got %q twice", first)
	}
	if !strings.HasSuffix(first, ".jpg") || !strings.HasSuffix(second, ".png") {
		t.Fatalf("names = %q, %q", first, second)
	}

	images, err := store.Images()
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 || images[0].OriginalName != "photo.jpg" || images[1].OriginalName != "photo.png" {
		t.Fatalf("images = %+v", images)
	}
	if images[0].ID == "" || images[0].ID == images[1].ID {
		t.Fatalf("image ids are not unique: %+v", images)
	}

	traversal, err := store.AddImage("../../evil.jpg", seedOwnerID, strings.NewReader("c"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(traversal, `/\`) {
		t.Fatalf("unsafe name %q", traversal)
	}
	if _, err := store.ImagePath("../evil.jpg"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected traversal to be rejected, got %v", err)
	}
}

func TestDuplicateUploadsGetUniqueExportNames(t *testing.T) {
	store := newTestStore(t)
	if err := store.SaveClasses([]string{"fish"}); err != nil {
		t.Fatal(err)
	}
	first, err := store.AddImage("photo.jpg", seedOwnerID, strings.NewReader("a"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AddImage("photo.jpg", seedMemberID, strings.NewReader("b"))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("stored names must differ, got %q", first)
	}
	label := []Label{{ClassID: 0, XCenter: 0.5, YCenter: 0.5, Width: 0.2, Height: 0.2}}
	if err := store.SaveLabels(first, label); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLabels(second, label); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := store.ExportZip(&buf); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	images := map[string]bool{}
	labels := map[string]bool{}
	manifest := false
	for _, f := range zr.File {
		switch {
		case f.Name == "manifest.csv":
			manifest = true
		case strings.HasPrefix(f.Name, "images/"):
			images[f.Name] = true
		case strings.HasPrefix(f.Name, "labels/"):
			labels[f.Name] = true
		}
	}
	if !manifest {
		t.Fatal("export is missing manifest.csv")
	}
	if len(images) != 2 || len(labels) != 2 {
		t.Fatalf("images = %v, labels = %v", images, labels)
	}
	if _, ok := images["images/photo.jpg"]; !ok {
		t.Fatalf("first upload should keep the original name: %v", images)
	}
	if _, ok := labels["labels/photo.txt"]; !ok {
		t.Fatalf("first label should keep the original name: %v", labels)
	}
}

func TestSaveEmptyLabelsClearsBoxes(t *testing.T) {
	store := newTestStore(t)
	if err := store.SaveClasses([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	name, err := store.AddImage("photo.jpg", seedOwnerID, strings.NewReader("img"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLabels(name, []Label{{ClassID: 0, XCenter: 0.5, YCenter: 0.5, Width: 0.2, Height: 0.2}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLabels(name, nil); err != nil {
		t.Fatal(err)
	}
	labels, err := store.LoadLabels(name)
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 0 {
		t.Fatalf("labels = %+v", labels)
	}
	images, err := store.Images()
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || images[0].LabelCount != 0 {
		t.Fatalf("images = %+v", images)
	}
}

func TestTrashRestoreAndPurgeImage(t *testing.T) {
	store := newTestStore(t)
	if err := store.SaveClasses([]string{"fish"}); err != nil {
		t.Fatal(err)
	}
	name, err := store.AddImage("photo.jpg", seedOwnerID, strings.NewReader("img"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLabels(name, []Label{{ClassID: 0, XCenter: 0.5, YCenter: 0.5, Width: 0.2, Height: 0.2}}); err != nil {
		t.Fatal(err)
	}

	// Soft delete keeps the file and labels but hides the image from the
	// active list and from exports.
	if err := store.TrashImage(name); err != nil {
		t.Fatal(err)
	}
	images, err := store.Images()
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 0 {
		t.Fatalf("active images = %+v", images)
	}
	trash, err := store.TrashedImages()
	if err != nil {
		t.Fatal(err)
	}
	if len(trash) != 1 || trash[0].Name != name || trash[0].DeletedAt == nil {
		t.Fatalf("trash = %+v", trash)
	}
	if _, err := store.ImagePath(name); err != nil {
		t.Fatalf("trashed image file should still exist: %v", err)
	}
	var buf bytes.Buffer
	if err := store.ExportZip(&buf); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "images/") || strings.HasPrefix(f.Name, "labels/") {
			t.Fatalf("trashed image leaked into export: %s", f.Name)
		}
	}
	if _, err := store.LoadLabels(name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("labels of a trashed image should be protected, got %v", err)
	}

	// Restore makes the image and its labels active again.
	if err := store.RestoreImage(name); err != nil {
		t.Fatal(err)
	}
	images, err = store.Images()
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || images[0].LabelCount != 1 {
		t.Fatalf("restored images = %+v", images)
	}
	if _, err := store.LoadLabels(name); err != nil {
		t.Fatalf("labels should be readable after restore: %v", err)
	}

	// Purging from the trash removes the row, labels, and file.
	if err := store.TrashImage(name); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteImage(name); err != nil {
		t.Fatal(err)
	}
	trash, err = store.TrashedImages()
	if err != nil {
		t.Fatal(err)
	}
	if len(trash) != 0 {
		t.Fatalf("trash after purge = %+v", trash)
	}
	if _, err := store.ImagePath(name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purged image file should be gone, got %v", err)
	}
	var leftover int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM labels WHERE project_id = ?`, store.projectID).Scan(&leftover); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Fatalf("purge left %d label rows behind", leftover)
	}
}
