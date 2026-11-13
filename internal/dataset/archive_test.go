package dataset

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnzipStripsWrapperFolder(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "wrapped.zip")
	writeZipFiles(t, zipPath, map[string]string{
		"MyData/classes.txt":  "cat\n",
		"MyData/images/a.jpg": "a",
		"MyData/labels/a.txt": "0 0.5 0.5 0.2 0.2\n",
	})

	dest := filepath.Join(dir, "out")
	if err := unzip(zipPath, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "images", "a.jpg")); err != nil {
		t.Fatalf("expected wrapper to be stripped, images/a.jpg missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "MyData")); !os.IsNotExist(err) {
		t.Fatalf("wrapper folder should have been removed, stat err = %v", err)
	}
}

func TestUnzipKeepsRootLevelLayout(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "flat.zip")
	writeZipFiles(t, zipPath, map[string]string{
		"classes.txt":  "cat\n",
		"images/a.jpg": "a",
		"labels/a.txt": "0 0.5 0.5 0.2 0.2\n",
	})

	dest := filepath.Join(dir, "out")
	if err := unzip(zipPath, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "images", "a.jpg")); err != nil {
		t.Fatalf("images/a.jpg missing: %v", err)
	}
}

func TestBuildZipDropsWrapperFolder(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "in.zip")
	writeZipFiles(t, zipPath, map[string]string{
		"GarbageData/classes.txt":  "cat\ndog\n",
		"GarbageData/images/a.jpg": "a",
		"GarbageData/labels/a.txt": "0 0.5 0.5 0.2 0.2\n",
		"GarbageData/images/b.jpg": "b",
		"GarbageData/labels/b.txt": "1 0.5 0.5 0.2 0.2\n",
	})

	outZip := filepath.Join(dir, "out.zip")
	if _, err := BuildZip(zipPath, outZip, Options{TrainRatio: 1, ValRatio: 0, TestRatio: 0, Seed: 1}); err != nil {
		t.Fatal(err)
	}

	r, err := zip.OpenReader(outZip)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	images := map[string]bool{}
	hasYAML := false
	for _, f := range r.File {
		if strings.Contains(f.Name, "GarbageData") {
			t.Fatalf("output still contains wrapper folder: %s", f.Name)
		}
		if f.Name == "data.yaml" {
			hasYAML = true
		}
		if strings.HasPrefix(f.Name, "train/images/") {
			rest := strings.TrimPrefix(f.Name, "train/images/")
			if strings.Contains(rest, "/") {
				t.Fatalf("image is still inside a sub folder: %s", f.Name)
			}
			images[rest] = true
		}
		if strings.HasPrefix(f.Name, "train/labels/") {
			rest := strings.TrimPrefix(f.Name, "train/labels/")
			if strings.Contains(rest, "/") {
				t.Fatalf("label is still inside a sub folder: %s", f.Name)
			}
		}
	}
	if !hasYAML {
		t.Fatal("result zip has no data.yaml")
	}
	if !images["a.jpg"] || !images["b.jpg"] {
		t.Fatalf("expected flat train/images/a.jpg and b.jpg, got %v", images)
	}
}

func writeZipFiles(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}
