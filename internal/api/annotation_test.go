package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"yoloapi/internal/annotation"
)

func TestProjectWorkflow(t *testing.T) {
	srv, err := New(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// No projects at first.
	var list struct {
		Projects []annotation.ProjectInfo `json:"projects"`
	}
	getJSON(t, ts.URL+"/api/projects", &list)
	if len(list.Projects) != 0 {
		t.Fatalf("projects = %+v", list.Projects)
	}
	// Create one project.
	res, err := http.Post(ts.URL+"/api/projects", "application/json", strings.NewReader(`{"name":"Fish study"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("create project: status %d body %s", res.StatusCode, raw)
	}
	var project annotation.ProjectInfo
	if err := json.NewDecoder(res.Body).Decode(&project); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if project.Name != "Fish study" {
		t.Fatalf("project = %+v", project)
	}
	base := ts.URL + "/api/projects/" + project.ID

	// Add two classes.
	for _, name := range []string{"fish", "leaf_disease"} {
		body := strings.NewReader(`{"name":"` + name + `"}`)
		res, err := http.Post(base+"/classes", "application/json", body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("add class %s: status %d", name, res.StatusCode)
		}
		res.Body.Close()
	}

	// Upload an image into the project.
	form, contentType := imageForm(t, "files", "fish1.jpg", []byte("fake-image"))
	res, err = http.Post(base+"/images", contentType, form)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("upload: status %d body %s", res.StatusCode, raw)
	}
	var uploaded struct {
		Saved []string `json:"saved"`
	}
	if err := json.NewDecoder(res.Body).Decode(&uploaded); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(uploaded.Saved) != 1 || len(uploaded.Saved[0]) != 40 || !strings.HasSuffix(uploaded.Saved[0], ".jpg") {
		t.Fatalf("saved = %#v", uploaded.Saved)
	}
	name := uploaded.Saved[0]

	// Project state should show the classes and the image.
	var state annotation.ProjectState
	getJSON(t, base, &state)
	if len(state.Classes) != 2 || state.Classes[0] != "fish" {
		t.Fatalf("classes = %#v", state.Classes)
	}
	if len(state.Images) != 1 || state.Images[0].LabelCount != 0 {
		t.Fatalf("images = %+v", state.Images)
	}
	if state.Images[0].Name != name || state.Images[0].OriginalName != "fish1.jpg" ||
		state.Images[0].UploadedByName == "" {
		t.Fatalf("image metadata = %+v", state.Images[0])
	}
	if state.ImageCount != 1 || state.ClassCount != 2 || state.BoxCount != 0 {
		t.Fatalf("counts = %+v", state.ProjectInfo)
	}
	if state.OwnerID != annotation.DefaultUserID || state.OwnerName == "" {
		t.Fatalf("owner = %+v", state.ProjectInfo)
	}

	// Save one box.
	put := `{"labels":[{"class_id":0,"x_center":0.5,"y_center":0.5,"width":0.25,"height":0.25}]}`
	req, _ := http.NewRequest(http.MethodPut, base+"/images/"+name+"/labels", strings.NewReader(put))
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("put labels: status %d body %s", res.StatusCode, raw)
	}
	res.Body.Close()

	var got struct {
		Labels []annotation.Label `json:"labels"`
	}
	getJSON(t, base+"/images/"+name+"/labels", &got)
	if len(got.Labels) != 1 || got.Labels[0].ClassID != 0 || got.Labels[0].Width != 0.25 {
		t.Fatalf("labels = %+v", got.Labels)
	}

	// Project list picks up the counts.
	getJSON(t, ts.URL+"/api/projects", &list)
	if len(list.Projects) != 1 || list.Projects[0].BoxCount != 1 || list.Projects[0].ImageCount != 1 {
		t.Fatalf("projects = %+v", list.Projects)
	}

	// Unknown class id is rejected.
	bad := strings.NewReader(`{"labels":[{"class_id":9,"x_center":0.5,"y_center":0.5,"width":0.2,"height":0.2}]}`)
	req, _ = http.NewRequest(http.MethodPut, base+"/images/"+name+"/labels", bad)
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad class: status %d", res.StatusCode)
	}
	res.Body.Close()

	// Export contains the flat images/labels/classes layout.
	res, err = http.Get(base + "/export")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("export status %d", res.StatusCode)
	}
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{"classes.txt", "images/fish1.jpg", "labels/fish1.txt"} {
		if !names[want] {
			t.Fatalf("export missing %q, got %v", want, names)
		}
	}

	// Rename the project.
	req, _ = http.NewRequest(http.MethodPatch, base, strings.NewReader(`{"name":"Fish 2026"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("rename status %d", res.StatusCode)
	}
	var renamed annotation.ProjectInfo
	if err := json.NewDecoder(res.Body).Decode(&renamed); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if renamed.Name != "Fish 2026" {
		t.Fatalf("renamed = %+v", renamed)
	}

	// Delete the project and confirm it is gone.
	req, _ = http.NewRequest(http.MethodDelete, base, nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("delete status %d", res.StatusCode)
	}
	res.Body.Close()
	getJSON(t, ts.URL+"/api/projects", &list)
	if len(list.Projects) != 0 {
		t.Fatalf("projects after delete = %+v", list.Projects)
	}

	// The annotation page is reachable.
	res, err = http.Get(ts.URL + "/annotate")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(page), "Annotation Studio") {
		t.Fatalf("annotate page status %d", res.StatusCode)
	}
}

func TestProjectPages(t *testing.T) {
	srv, err := New(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	pages := []struct {
		path string
		want string
	}{
		{"/projects", "projects.js"},
		{"/projects.html", "projects.js"},
		{"/project.html", "project.js"},
	}
	for _, page := range pages {
		res, err := http.Get(ts.URL + page.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || !strings.Contains(string(body), page.want) {
			t.Fatalf("%s: status %d, missing %q", page.path, res.StatusCode, page.want)
		}
	}

	// /project keeps the ?id= query when redirecting to the static page.
	res, err := http.Get(ts.URL + "/project?id=0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Request.URL.Path != "/project.html" {
		t.Fatalf("redirected to %q", res.Request.URL.Path)
	}
	if got := res.Request.URL.Query().Get("id"); got != "0123456789abcdef" {
		t.Fatalf("id query = %q", got)
	}
}

func TestProjectValidation(t *testing.T) {
	srv, err := New(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Empty project name is rejected.
	res, err := http.Post(ts.URL+"/api/projects", "application/json", strings.NewReader(`{"name":"  "}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty name: status %d", res.StatusCode)
	}
	res.Body.Close()

	// Unknown project id is not found.
	res, err = http.Get(ts.URL + "/api/projects/0000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown project: status %d", res.StatusCode)
	}
	res.Body.Close()

	// Bad project id is rejected before touching the filesystem.
	res, err = http.Get(ts.URL + "/api/projects/..%2Fetc")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusBadRequest && res.StatusCode != http.StatusNotFound {
		t.Fatalf("bad project id: status %d", res.StatusCode)
	}
	res.Body.Close()
}

func TestUsersOwnershipAndPermissions(t *testing.T) {
	srv, err := New(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var users struct {
		Users []annotation.User `json:"users"`
	}
	getJSON(t, ts.URL+"/api/users", &users)
	if len(users.Users) != 2 {
		t.Fatalf("users = %+v", users.Users)
	}
	owner := users.Users[0]
	member := users.Users[1]

	// The owner creates a project.
	res := doUserRequest(t, http.MethodPost, ts.URL+"/api/projects", owner.ID,
		strings.NewReader(`{"name":"Owner project"}`), "application/json")
	if res.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("create project: status %d body %s", res.StatusCode, raw)
	}
	var project annotation.ProjectInfo
	if err := json.NewDecoder(res.Body).Decode(&project); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if project.OwnerID != owner.ID || project.OwnerName != owner.DisplayName {
		t.Fatalf("project = %+v", project)
	}
	base := ts.URL + "/api/projects/" + project.ID

	// Only the owner may manage classes.
	res = doUserRequest(t, http.MethodPost, base+"/classes", member.ID,
		strings.NewReader(`{"name":"member-class"}`), "application/json")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("member add class: status %d", res.StatusCode)
	}
	res.Body.Close()

	res = doUserRequest(t, http.MethodPut, base+"/classes", member.ID,
		strings.NewReader(`{"classes":["member-class"]}`), "application/json")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("member put classes: status %d", res.StatusCode)
	}
	res.Body.Close()

	res = doUserRequest(t, http.MethodPost, base+"/classes", owner.ID,
		strings.NewReader(`{"name":"owner-class"}`), "application/json")
	if res.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("owner add class: status %d body %s", res.StatusCode, raw)
	}
	res.Body.Close()

	res = doUserRequest(t, http.MethodPut, base+"/classes", owner.ID,
		strings.NewReader(`{"classes":["owner-class","second"]}`), "application/json")
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("owner put classes: status %d body %s", res.StatusCode, raw)
	}
	res.Body.Close()

	var classState annotation.ProjectState
	getJSON(t, base, &classState)
	if got := strings.Join(classState.Classes, ","); got != "owner-class,second" {
		t.Fatalf("classes = %q", got)
	}

	// The same original filename can be uploaded twice; each gets a unique
	// stored name and records the member as uploader.
	saved := []string{}
	for i := 0; i < 2; i++ {
		form, contentType := imageForm(t, "files", "same.jpg", []byte("image"))
		res = doUserRequest(t, http.MethodPost, base+"/images", member.ID, form, contentType)
		if res.StatusCode != http.StatusCreated {
			raw, _ := io.ReadAll(res.Body)
			t.Fatalf("upload %d: status %d body %s", i, res.StatusCode, raw)
		}
		var uploaded struct {
			Saved []string `json:"saved"`
		}
		if err := json.NewDecoder(res.Body).Decode(&uploaded); err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		saved = append(saved, uploaded.Saved...)
	}
	if len(saved) != 2 || saved[0] == saved[1] {
		t.Fatalf("saved = %v", saved)
	}

	var state annotation.ProjectState
	getJSON(t, base, &state)
	if len(state.Images) != 2 {
		t.Fatalf("images = %+v", state.Images)
	}
	for _, img := range state.Images {
		if img.OriginalName != "same.jpg" || img.UploadedBy != member.ID || img.UploadedByName == "" {
			t.Fatalf("image metadata = %+v", img)
		}
	}

	// Only the owner may rename or delete the project.
	res = doUserRequest(t, http.MethodPatch, base, member.ID,
		strings.NewReader(`{"name":"Nope"}`), "application/json")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("member rename: status %d", res.StatusCode)
	}
	res.Body.Close()

	res = doUserRequest(t, http.MethodPatch, base, owner.ID,
		strings.NewReader(`{"name":"Owner renamed"}`), "application/json")
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("owner rename: status %d body %s", res.StatusCode, raw)
	}
	res.Body.Close()

	res = doUserRequest(t, http.MethodDelete, base, member.ID, nil, "")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("member delete: status %d", res.StatusCode)
	}
	res.Body.Close()

	// The uploader may delete their own image.
	res = doUserRequest(t, http.MethodDelete, base+"/images/"+saved[0], member.ID, nil, "")
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("member delete image: status %d body %s", res.StatusCode, raw)
	}
	res.Body.Close()

	res = doUserRequest(t, http.MethodDelete, base, owner.ID, nil, "")
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("owner delete project: status %d body %s", res.StatusCode, raw)
	}
	res.Body.Close()
}

func doUserRequest(t *testing.T, method, url, userID string, body io.Reader, contentType string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if userID != "" {
		req.Header.Set("X-User-ID", userID)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func imageForm(t *testing.T, field, filename string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func getJSON(t *testing.T, url string, dst any) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("GET %s: status %d body %s", url, res.StatusCode, raw)
	}
	if err := json.NewDecoder(res.Body).Decode(dst); err != nil {
		t.Fatal(err)
	}
}

func TestImageTrashRestoreAndPurge(t *testing.T) {
	srv, err := New(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var users struct {
		Users []annotation.User `json:"users"`
	}
	getJSON(t, ts.URL+"/api/users", &users)
	owner := users.Users[0]
	member := users.Users[1]

	res := doUserRequest(t, http.MethodPost, ts.URL+"/api/projects", owner.ID,
		strings.NewReader(`{"name":"Trash test"}`), "application/json")
	if res.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("create project: status %d body %s", res.StatusCode, raw)
	}
	var project annotation.ProjectInfo
	if err := json.NewDecoder(res.Body).Decode(&project); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	base := ts.URL + "/api/projects/" + project.ID

	form, contentType := imageForm(t, "files", "owner.jpg", []byte("image"))
	res = doUserRequest(t, http.MethodPost, base+"/images", owner.ID, form, contentType)
	if res.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("upload: status %d body %s", res.StatusCode, raw)
	}
	var uploaded struct {
		Saved []string `json:"saved"`
	}
	if err := json.NewDecoder(res.Body).Decode(&uploaded); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(uploaded.Saved) != 1 {
		t.Fatalf("saved = %v", uploaded.Saved)
	}
	name := uploaded.Saved[0]
	imageURL := base + "/images/" + name

	// A user who is neither owner nor uploader cannot trash the image.
	res = doUserRequest(t, http.MethodDelete, imageURL, member.ID, nil, "")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("non-owner trash: status %d", res.StatusCode)
	}
	res.Body.Close()

	// The owner trashes the image; it moves to the trash list but the file
	// can still be served for the trash thumbnail.
	res = doUserRequest(t, http.MethodDelete, imageURL, owner.ID, nil, "")
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("owner trash: status %d body %s", res.StatusCode, raw)
	}
	res.Body.Close()

	res = doUserRequest(t, http.MethodGet, base, owner.ID, nil, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("get project: status %d", res.StatusCode)
	}
	var state annotation.ProjectState
	if err := json.NewDecoder(res.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(state.Images) != 0 || len(state.Trash) != 1 || state.Trash[0].Name != name {
		t.Fatalf("state after trash: images=%+v trash=%+v", state.Images, state.Trash)
	}
	if state.ImageCount != 0 {
		t.Fatalf("image_count after trash = %d", state.ImageCount)
	}

	res = doUserRequest(t, http.MethodPost, imageURL+"/restore", member.ID, nil, "")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("non-owner restore: status %d", res.StatusCode)
	}
	res.Body.Close()

	res = doUserRequest(t, http.MethodPost, imageURL+"/restore", owner.ID, nil, "")
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("owner restore: status %d body %s", res.StatusCode, raw)
	}
	res.Body.Close()

	res = doUserRequest(t, http.MethodGet, base, owner.ID, nil, "")
	if err := json.NewDecoder(res.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(state.Images) != 1 || len(state.Trash) != 0 || state.Images[0].Name != name {
		t.Fatalf("state after restore: images=%+v trash=%+v", state.Images, state.Trash)
	}

	// Trash again, then purge permanently.
	res = doUserRequest(t, http.MethodDelete, imageURL, owner.ID, nil, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("second trash: status %d", res.StatusCode)
	}
	res.Body.Close()

	res = doUserRequest(t, http.MethodDelete, imageURL+"/permanent", owner.ID, nil, "")
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("owner purge: status %d body %s", res.StatusCode, raw)
	}
	res.Body.Close()

	res = doUserRequest(t, http.MethodGet, base, owner.ID, nil, "")
	if err := json.NewDecoder(res.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(state.Images) != 0 || len(state.Trash) != 0 {
		t.Fatalf("state after purge: images=%+v trash=%+v", state.Images, state.Trash)
	}

	res = doUserRequest(t, http.MethodGet, imageURL, owner.ID, nil, "")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("purged image file: status %d", res.StatusCode)
	}
	res.Body.Close()
}

func uploadProjectImage(t *testing.T, baseURL, projectID, userID, filename string) string {
	t.Helper()
	form, contentType := imageForm(t, "files", filename, []byte("image"))
	res := doUserRequest(t, http.MethodPost, baseURL+"/api/projects/"+projectID+"/images", userID, form, contentType)
	if res.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("upload %s: status %d body %s", filename, res.StatusCode, raw)
	}
	var uploaded struct {
		Saved []string `json:"saved"`
	}
	if err := json.NewDecoder(res.Body).Decode(&uploaded); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(uploaded.Saved) != 1 {
		t.Fatalf("saved = %v", uploaded.Saved)
	}
	return uploaded.Saved[0]
}

func createProjectFor(t *testing.T, baseURL, userID, name string) annotation.ProjectInfo {
	t.Helper()
	res := doUserRequest(t, http.MethodPost, baseURL+"/api/projects", userID,
		strings.NewReader(`{"name":"`+name+`"}`), "application/json")
	if res.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("create %s: status %d body %s", name, res.StatusCode, raw)
	}
	var project annotation.ProjectInfo
	if err := json.NewDecoder(res.Body).Decode(&project); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return project
}

func TestTrashGroupsByProject(t *testing.T) {
	srv, err := New(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var users struct {
		Users []annotation.User `json:"users"`
	}
	getJSON(t, ts.URL+"/api/users", &users)
	owner := users.Users[0]
	member := users.Users[1]

	// Nothing in the trash yet.
	var empty struct {
		Projects []annotation.ProjectTrash `json:"projects"`
	}
	getJSON(t, ts.URL+"/api/trash", &empty)
	if len(empty.Projects) != 0 {
		t.Fatalf("trash = %+v", empty.Projects)
	}

	first := createProjectFor(t, ts.URL, owner.ID, "Alpha")
	second := createProjectFor(t, ts.URL, member.ID, "Beta")

	firstImage := uploadProjectImage(t, ts.URL, first.ID, owner.ID, "one.jpg")
	secondImage := uploadProjectImage(t, ts.URL, second.ID, member.ID, "two.jpg")

	res := doUserRequest(t, http.MethodDelete, ts.URL+"/api/projects/"+first.ID+"/images/"+firstImage, owner.ID, nil, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("trash first: status %d", res.StatusCode)
	}
	res.Body.Close()

	res = doUserRequest(t, http.MethodDelete, ts.URL+"/api/projects/"+second.ID+"/images/"+secondImage, member.ID, nil, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("trash second: status %d", res.StatusCode)
	}
	res.Body.Close()

	var grouped struct {
		Projects []annotation.ProjectTrash `json:"projects"`
	}
	getJSON(t, ts.URL+"/api/trash", &grouped)
	if len(grouped.Projects) != 2 {
		t.Fatalf("groups = %+v", grouped.Projects)
	}

	names := map[string]string{}
	owners := map[string]string{}
	images := map[string][]string{}
	for _, group := range grouped.Projects {
		names[group.Project.ID] = group.Project.Name
		owners[group.Project.ID] = group.Project.OwnerID
		for _, img := range group.Images {
			if img.DeletedAt == nil {
				t.Fatalf("trashed image %+v has no deleted_at", img)
			}
			images[group.Project.ID] = append(images[group.Project.ID], img.Name)
		}
	}
	if names[first.ID] != "Alpha" || owners[first.ID] != owner.ID {
		t.Fatalf("first group = %+v", names)
	}
	if names[second.ID] != "Beta" || owners[second.ID] != member.ID {
		t.Fatalf("second group = %+v", names)
	}
	if len(images[first.ID]) != 1 || images[first.ID][0] != firstImage {
		t.Fatalf("first images = %v", images[first.ID])
	}
	if len(images[second.ID]) != 1 || images[second.ID][0] != secondImage {
		t.Fatalf("second images = %v", images[second.ID])
	}
}

func TestPurgeRequiresProjectOwner(t *testing.T) {
	srv, err := New(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var users struct {
		Users []annotation.User `json:"users"`
	}
	getJSON(t, ts.URL+"/api/users", &users)
	owner := users.Users[0]
	member := users.Users[1]

	project := createProjectFor(t, ts.URL, owner.ID, "Owner only")
	base := ts.URL + "/api/projects/" + project.ID
	name := uploadProjectImage(t, ts.URL, project.ID, member.ID, "member.jpg")
	imageURL := base + "/images/" + name

	// The uploader may soft-delete their own image.
	res := doUserRequest(t, http.MethodDelete, imageURL, member.ID, nil, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("uploader trash: status %d", res.StatusCode)
	}
	res.Body.Close()

	// ...and restore it, but not purge it.
	res = doUserRequest(t, http.MethodDelete, imageURL+"/permanent", member.ID, nil, "")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("uploader purge: status %d", res.StatusCode)
	}
	res.Body.Close()

	res = doUserRequest(t, http.MethodPost, imageURL+"/restore", member.ID, nil, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("uploader restore: status %d", res.StatusCode)
	}
	res.Body.Close()

	// Only the owner may purge.
	res = doUserRequest(t, http.MethodDelete, imageURL, owner.ID, nil, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("owner trash: status %d", res.StatusCode)
	}
	res.Body.Close()

	res = doUserRequest(t, http.MethodDelete, imageURL+"/permanent", owner.ID, nil, "")
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("owner purge: status %d body %s", res.StatusCode, raw)
	}
	res.Body.Close()

	res = doUserRequest(t, http.MethodGet, imageURL, owner.ID, nil, "")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("purged image: status %d", res.StatusCode)
	}
	res.Body.Close()
}

func TestTrashPage(t *testing.T) {
	srv, err := New(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for _, page := range []struct{ path, want string }{
		{"/trash", "trash.js"},
		{"/trash.html", "trash.js"},
	} {
		res, err := http.Get(ts.URL + page.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || !strings.Contains(string(body), page.want) {
			t.Fatalf("%s: status %d, missing %q", page.path, res.StatusCode, page.want)
		}
	}

	res, err := http.Get(ts.URL + "/trash?project=0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Request.URL.Path != "/trash.html" {
		t.Fatalf("redirected to %q", res.Request.URL.Path)
	}
	if got := res.Request.URL.Query().Get("project"); got != "0123456789abcdef" {
		t.Fatalf("project query = %q", got)
	}
}
