package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadListDownloadDelete(t *testing.T) {
	s := newTestServer(t)
	teacher := s.teacher()

	body, contentType := zipForm(t, map[string]string{
		"train_ratio": "0.7",
		"val_ratio":   "0.2",
		"test_ratio":  "0.1",
		"seed":        "42",
	})
	res := teacher.do(http.MethodPost, "/api/datasets", body, contentType)
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d body %s", res.StatusCode, b)
	}
	var created job
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Summary.Total != 4 || created.Summary.Classes[0] != "alpha" {
		t.Fatalf("summary = %+v", created.Summary)
	}

	// Splitting a dataset is teacher work, and students are signed out of it.
	s.student().expect(http.MethodGet, "/api/datasets", "", http.StatusForbidden)
	s.anonymous().expect(http.MethodGet, "/api/datasets", "", http.StatusUnauthorized)

	if got := teacher.statusOf(http.MethodGet, "/api/datasets/"+created.ID, ""); got != http.StatusOK {
		t.Fatalf("get status %d", got)
	}

	dl := teacher.get(created.DownloadURL)
	defer dl.Body.Close()
	tmp := filepath.Join(t.TempDir(), "out.zip")
	f, err := os.Create(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(f, dl.Body); err != nil {
		t.Fatal(err)
	}
	f.Close()
	zr, err := zip.OpenReader(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	found := false
	for _, file := range zr.File {
		if file.Name == "data.yaml" {
			found = true
		}
	}
	if !found {
		t.Fatal("downloaded zip missing data.yaml")
	}

	teacher.expect(http.MethodDelete, "/api/datasets/"+created.ID, "", http.StatusOK)
	if got := teacher.statusOf(http.MethodGet, "/api/datasets/"+created.ID, ""); got != http.StatusNotFound {
		t.Fatalf("deleted dataset: status %d", got)
	}
}

func TestRejectBadRatio(t *testing.T) {
	s := newTestServer(t)
	body, contentType := zipForm(t, map[string]string{
		"train_ratio": "0.5",
		"val_ratio":   "0.5",
		"test_ratio":  "0.5",
	})
	res := s.teacher().do(http.MethodPost, "/api/datasets", body, contentType)
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func zipForm(t *testing.T, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	part, err := w.CreateFormFile("file", "dataset.zip")
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(part)
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
		fw, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}
