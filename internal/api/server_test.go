package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadListDownloadDelete(t *testing.T) {
	srv, err := New(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body, contentType := zipForm(t, map[string]string{
		"train_ratio": "0.7",
		"val_ratio":   "0.2",
		"test_ratio":  "0.1",
		"seed":        "42",
	})
	res, err := http.Post(ts.URL+"/api/datasets", contentType, body)
	if err != nil {
		t.Fatal(err)
	}
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

	got, err := http.Get(ts.URL + "/api/datasets/" + created.ID)
	if err != nil {
		t.Fatal(err)
	}
	got.Body.Close()
	if got.StatusCode != http.StatusOK {
		t.Fatalf("get status %d", got.StatusCode)
	}

	dl, err := http.Get(ts.URL + created.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer dl.Body.Close()
	if dl.StatusCode != http.StatusOK {
		t.Fatalf("download status %d", dl.StatusCode)
	}
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

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/datasets/"+created.ID, nil)
	del, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	del.Body.Close()
	if del.StatusCode != http.StatusOK {
		t.Fatalf("delete status %d", del.StatusCode)
	}
}

func TestRejectBadRatio(t *testing.T) {
	srv, err := New(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	body, contentType := zipForm(t, map[string]string{
		"train_ratio": "0.5",
		"val_ratio":   "0.5",
		"test_ratio":  "0.5",
	})
	res, err := http.Post(ts.URL+"/api/datasets", contentType, body)
	if err != nil {
		t.Fatal(err)
	}
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
