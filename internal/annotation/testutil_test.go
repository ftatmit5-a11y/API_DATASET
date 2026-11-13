package annotation

import (
	"path/filepath"
	"testing"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	base := t.TempDir()
	manager, err := NewManager(
		filepath.Join(base, "annotations.db"),
		filepath.Join(base, "projects"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Errorf("close manager: %v", err)
		}
	})
	return manager
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	manager := newTestManager(t)
	info, err := manager.Create("test project")
	if err != nil {
		t.Fatal(err)
	}
	store, err := manager.Store(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	return store
}
