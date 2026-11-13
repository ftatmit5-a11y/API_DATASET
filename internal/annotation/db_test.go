package annotation

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLitePersistsAcrossRestart(t *testing.T) {
	base := t.TempDir()
	dbPath := filepath.Join(base, "annotations.db")
	root := filepath.Join(base, "projects")

	manager, err := NewManager(dbPath, root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := manager.Create("Persistent")
	if err != nil {
		t.Fatal(err)
	}
	store, err := manager.Store(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveClasses([]string{"fish", "weed"}); err != nil {
		t.Fatal(err)
	}
	name, err := store.AddImage("photo.jpg", seedOwnerID, strings.NewReader("img"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLabels(name, []Label{{ClassID: 1, XCenter: 0.4, YCenter: 0.6, Width: 0.2, Height: 0.3}}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewManager(dbPath, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	got, err := reopened.Info(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Persistent" || got.ImageCount != 1 || got.BoxCount != 1 || got.ClassCount != 2 {
		t.Fatalf("info = %+v", got)
	}

	store, err = reopened.Store(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	classes, err := store.Classes()
	if err != nil {
		t.Fatal(err)
	}
	if len(classes) != 2 || classes[1] != "weed" {
		t.Fatalf("classes = %#v", classes)
	}
	labels, err := store.LoadLabels(name)
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 1 || labels[0].ClassID != 1 || labels[0].Width != 0.2 {
		t.Fatalf("labels = %+v", labels)
	}
}
