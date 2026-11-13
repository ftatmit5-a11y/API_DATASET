package dataset

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSplitsPairsAndWritesYAML(t *testing.T) {
	root := t.TempDir()
	writePair(t, root, "images/a.jpg", "labels/a.txt", "0 0.5 0.5 0.2 0.2\n")
	writePair(t, root, "images/sub/b.png", "labels/sub/b.txt", "1 0.4 0.4 0.1 0.1\n")
	writePair(t, root, "images/c.jpg", "labels/c.txt", "0 0.2 0.2 0.2 0.2\n")
	writePair(t, root, "images/nolabel.jpg", "", "")
	if err := os.WriteFile(filepath.Join(root, "classes.txt"), []byte("cat\ndog\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	summary, err := Build(root, out, Options{TrainRatio: 0.7, ValRatio: 0.2, TestRatio: 0.1, Seed: 42})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 3 {
		t.Fatalf("total = %d, want 3", summary.Total)
	}
	if summary.UnlabeledSkipped != 1 {
		t.Fatalf("skipped = %d, want 1", summary.UnlabeledSkipped)
	}
	if summary.Train+summary.Val+summary.Test != 3 {
		t.Fatalf("counts %+v do not sum to 3", summary)
	}
	if summary.Train < 1 {
		t.Fatal("train bucket is empty")
	}
	if summary.NC != 2 || summary.Classes[0] != "cat" || summary.Classes[1] != "dog" {
		t.Fatalf("classes = %#v", summary.Classes)
	}

	yamlBytes, err := os.ReadFile(filepath.Join(out, "data.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(yamlBytes)
	for _, want := range []string{"train: train/images", "val: val/images", "nc: 2", "0: cat", "1: dog"} {
		if !strings.Contains(yaml, want) {
			t.Fatalf("data.yaml missing %q:\n%s", want, yaml)
		}
	}

	var placed int
	for _, split := range []string{"train", "val", "test"} {
		n, err := countFiles(filepath.Join(out, split, "images"))
		if err != nil {
			t.Fatal(err)
		}
		labels, err := countFiles(filepath.Join(out, split, "labels"))
		if err != nil {
			t.Fatal(err)
		}
		if n != labels {
			t.Fatalf("%s images %d != labels %d", split, n, labels)
		}
		placed += n
	}
	if placed != 3 {
		t.Fatalf("placed %d files", placed)
	}
	if _, err := os.Stat(filepath.Join(out, "train", "images", "sub", "b.png")); err != nil {
		// b.png may land in val or test depending on seed; just ensure the sub path exists somewhere.
		found := false
		for _, split := range []string{"train", "val", "test"} {
			if _, err := os.Stat(filepath.Join(out, split, "images", "sub", "b.png")); err == nil {
				found = true
			}
		}
		if !found {
			t.Fatal("nested image path was not preserved")
		}
	}
}

func TestBuildZipRoundTrip(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "in.zip")
	writeSampleZip(t, zipPath)
	outZip := filepath.Join(dir, "out", "dataset.zip")
	summary, err := BuildZip(zipPath, outZip, Options{TrainRatio: 0.8, ValRatio: 0.2, TestRatio: 0, Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 4 || summary.Test != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	r, err := zip.OpenReader(outZip)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	hasYAML := false
	for _, f := range r.File {
		if f.Name == "data.yaml" {
			hasYAML = true
		}
		if strings.Contains(f.Name, "test/") {
			t.Fatalf("test split should be omitted, found %s", f.Name)
		}
	}
	if !hasYAML {
		t.Fatal("result zip has no data.yaml")
	}
}

func TestRejectZipSlip(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "bad.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("../escape.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("nope")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	err = unzip(zipPath, filepath.Join(dir, "out"))
	if err == nil {
		t.Fatal("expected zip slip to be rejected")
	}
}

func TestParseNamesYAML(t *testing.T) {
	list, ok := parseNamesYAML("path: .\nnames:\n  - cat\n  - dog\n")
	if !ok || len(list) != 2 || list[1] != "dog" {
		t.Fatalf("list parse %#v ok=%v", list, ok)
	}
	mapped, ok := parseNamesYAML("names:\n  0: person\n  1: bike\n")
	if !ok || mapped[0] != "person" || mapped[1] != "bike" {
		t.Fatalf("map parse %#v", mapped)
	}
	inline, ok := parseNamesYAML("names: [cat, dog]\n")
	if !ok || inline[0] != "cat" {
		t.Fatalf("inline %#v", inline)
	}
}

func TestValidateRatios(t *testing.T) {
	if err := ValidateRatios(0.7, 0.2, 0.1); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRatios(0.5, 0.5, 0.5); err == nil {
		t.Fatal("expected sum error")
	}
}

func writePair(t *testing.T, root, imageRel, labelRel, label string) {
	t.Helper()
	img := filepath.Join(root, filepath.FromSlash(imageRel))
	if err := os.MkdirAll(filepath.Dir(img), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(img, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	if labelRel == "" {
		return
	}
	lb := filepath.Join(root, filepath.FromSlash(labelRel))
	if err := os.MkdirAll(filepath.Dir(lb), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lb, []byte(label), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSampleZip(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	files := map[string]string{
		"classes.txt":  "alpha\nbeta\n",
		"images/1.jpg": "a",
		"labels/1.txt": "0 0.5 0.5 0.2 0.2\n",
		"images/2.jpg": "b",
		"labels/2.txt": "1 0.5 0.5 0.2 0.2\n",
		"images/3.jpg": "c",
		"labels/3.txt": "0 0.1 0.1 0.1 0.1\n",
		"images/4.jpg": "d",
		"labels/4.txt": "1 0.2 0.2 0.2 0.2\n",
	}
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

func countFiles(dir string) (int, error) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return 0, nil
	}
	n := 0
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d != nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n, err
}
