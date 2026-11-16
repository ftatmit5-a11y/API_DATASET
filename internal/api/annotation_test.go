package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"yoloapi/internal/annotation"
)

// imageForm builds a multipart body with one photo under the "files" field.
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

// uploadFiles posts photos to a multipart endpoint and returns the "saved"
// names (project images) or submission ids, depending on the response shape.
func (c *client) uploadFiles(path, filename string, data []byte) (string, string) {
	c.t.Helper()
	form, contentType := imageForm(c.t, "files", filename, data)
	res := c.do(http.MethodPost, path, form, contentType)
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(res.Body)
		c.t.Fatalf("POST %s: status %d body %s", path, res.StatusCode, body)
	}
	var out struct {
		Saved       []string                `json:"saved"`
		Submissions []annotation.Submission `json:"submissions"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		c.t.Fatalf("decode upload response: %v", err)
	}
	switch {
	case len(out.Saved) == 1:
		return out.Saved[0], ""
	case len(out.Submissions) == 1:
		return out.Submissions[0].ID, ""
	default:
		c.t.Fatalf("unexpected upload response: %+v", out)
		return "", ""
	}
}

// userIDs loads the roster (teacher only) and maps username -> account.
func roster(t *testing.T, c *client) map[string]annotation.User {
	t.Helper()
	var out struct {
		Users []annotation.User `json:"users"`
	}
	c.mustJSON("/api/users", &out)
	by := map[string]annotation.User{}
	for _, u := range out.Users {
		by[u.Username] = u
	}
	return by
}

// newStudent lets a teacher create a student account for a test.
func (c *client) newStudent(username, password string) annotation.User {
	c.t.Helper()
	res := c.json(http.MethodPost, "/api/users",
		`{"username":"`+username+`","display_name":"`+username+` Student","role":"student","password":"`+password+`"}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(res.Body)
		c.t.Fatalf("create student %s: status %d body %s", username, res.StatusCode, body)
	}
	var u annotation.User
	if err := json.NewDecoder(res.Body).Decode(&u); err != nil {
		c.t.Fatal(err)
	}
	return u
}

func (c *client) createProject(payload string) annotation.ProjectInfo {
	c.t.Helper()
	res := c.json(http.MethodPost, "/api/projects", payload)
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(res.Body)
		c.t.Fatalf("create project: status %d body %s", res.StatusCode, body)
	}
	var info annotation.ProjectInfo
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		c.t.Fatal(err)
	}
	return info
}

const onePixel = "fake-image-bytes"

// ---------------------------------------------------------------------------
// authentication
// ---------------------------------------------------------------------------

func TestAuthRequiredAndCSRF(t *testing.T) {
	s := newTestServer(t)

	// Anonymous callers cannot read project data at all.
	anon := s.anonymous()
	anon.expect(http.MethodGet, "/api/projects", "", http.StatusUnauthorized)
	anon.expect(http.MethodGet, "/api/users", "", http.StatusUnauthorized)
	anon.expect(http.MethodGet, "/api/trash", "", http.StatusUnauthorized)

	// Health and the endpoint index stay public, and pages are shells.
	if res := anon.do(http.MethodGet, "/health", nil, ""); res.StatusCode != http.StatusOK {
		t.Fatalf("health: %d", res.StatusCode)
	} else {
		res.Body.Close()
	}
	for _, page := range []string{"/projects.html", "/login.html", "/pending.html", "/mywork.html", "/users.html", "/trash.html"} {
		res := anon.do(http.MethodGet, page, nil, "")
		if res.StatusCode != http.StatusOK {
			t.Fatalf("page %s: %d", page, res.StatusCode)
		}
		res.Body.Close()
	}

	// A signed-in user without the CSRF header cannot write.
	noCSRF := s.noCSRF(teacherUser, teacherPass)
	res := noCSRF.doWithoutCSRF(http.MethodPost, "/api/projects", `{"name":"forged"}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site write: status %d", res.StatusCode)
	}
	// Reads do not need the header.
	if res := noCSRF.doWithoutCSRF(http.MethodGet, "/api/projects", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("read without csrf header: %d", res.StatusCode)
	}
}

func TestLoginRejectsBadPasswords(t *testing.T) {
	s := newTestServer(t)

	for _, attempt := range []struct{ username, password, why string }{
		{teacherUser, "wrong-password", "wrong password"},
		{"nosuchuser", "teacher1234", "unknown account"},
		{"", "anything", "empty username"},
	} {
		res := s.anonymous().json(http.MethodPost, "/api/auth/login",
			`{"username":"`+attempt.username+`","password":"`+attempt.password+`"}`)
		if res.StatusCode != http.StatusUnauthorized {
			res.Body.Close()
			t.Fatalf("%s: status %d want 401", attempt.why, res.StatusCode)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		// The message must not reveal whether the account exists.
		if !strings.Contains(string(body), "invalid username or password") {
			t.Fatalf("%s: body = %s", attempt.why, body)
		}
	}
}

func TestSessionLifecycle(t *testing.T) {
	s := newTestServer(t)
	teacher := s.teacher()

	var me struct {
		User annotation.User `json:"user"`
	}
	teacher.mustJSON("/api/auth/me", &me)
	if me.User.Role != "teacher" || me.User.Username != teacherUser {
		t.Fatalf("me = %+v", me.User)
	}
	if me.User.ID == "" {
		t.Fatal("me has no id")
	}

	res := teacher.json(http.MethodPost, "/api/auth/logout", "")
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("logout: %d", res.StatusCode)
	}
	teacher.expect(http.MethodGet, "/api/auth/me", "", http.StatusUnauthorized)
}

func TestChangePasswordInvalidatesSessions(t *testing.T) {
	s := newTestServer(t)
	teacher := s.teacher()

	// The old password must be proven.
	teacher.expect(http.MethodPost, "/api/auth/password",
		`{"current":"not-it","password":"brand-new-pass"}`, http.StatusUnauthorized)

	res := teacher.json(http.MethodPost, "/api/auth/password",
		`{"current":"`+teacherPass+`","password":"brand-new-pass"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("change password: %d", res.StatusCode)
	}
	// The session was dropped, so the previous cookie no longer works.
	teacher.expect(http.MethodGet, "/api/projects", "", http.StatusUnauthorized)

	// The new password signs in successfully.
	fresh := s.login(teacherUser, "brand-new-pass")
	fresh.expect(http.MethodGet, "/api/projects", "", http.StatusOK)
	// And the old password is really gone.
	res = s.anonymous().json(http.MethodPost, "/api/auth/login",
		`{"username":"`+teacherUser+`","password":"`+teacherPass+`"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password still works: %d", res.StatusCode)
	}
}

func TestStudentsCannotManageAccounts(t *testing.T) {
	s := newTestServer(t)
	teacher := s.teacher()
	student := s.student()

	if len(roster(t, teacher)) < 2 {
		t.Fatal("seeded accounts are missing")
	}
	student.expect(http.MethodGet, "/api/users", "", http.StatusForbidden)
	student.expect(http.MethodPost, "/api/users",
		`{"username":"sneaky","display_name":"Sneaky","role":"teacher","password":"secret123"}`,
		http.StatusForbidden)
	student.expect(http.MethodPatch, "/api/users/"+student.id, `{"role":"teacher"}`, http.StatusForbidden)

	// Teachers may add accounts and reset passwords.
	created := teacher.newStudent("student01", "password123")
	if created.Role != "student" || created.ID == "" {
		t.Fatalf("created = %+v", created)
	}
	res := teacher.json(http.MethodPatch, "/api/users/"+created.ID, `{"password":"another-pass"}`)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("reset password: %d", res.StatusCode)
	}
	if res := s.anonymous().json(http.MethodPost, "/api/auth/login",
		`{"username":"student01","password":"another-pass"}`); res.StatusCode != http.StatusOK {
		t.Fatalf("login with reset password: %d", res.StatusCode)
	} else {
		res.Body.Close()
	}
}

func TestUsernameValidation(t *testing.T) {
	s := newTestServer(t)
	teacher := s.teacher()

	bad := []string{
		`{"username":"ab","display_name":"Too Short","role":"student","password":"password123"}`,
		`{"username":"has space","display_name":"Space","role":"student","password":"password123"}`,
		`{"username":"ok.name","display_name":"No Role","role":"","password":"password123"}`,
		`{"username":"goodname","display_name":"Weak","role":"student","password":"123"}`,
		`{"username":"goodname","display_name":"","role":"student","password":"password123"}`,
	}
	for _, payload := range bad {
		teacher.expect(http.MethodPost, "/api/users", payload, http.StatusBadRequest)
	}
	// Duplicate usernames are rejected.
	teacher.newStudent("dupe", "password123")
	teacher.expect(http.MethodPost, "/api/users",
		`{"username":"dupe","display_name":"Dupe Again","role":"student","password":"password123"}`,
		http.StatusBadRequest)
}

// ---------------------------------------------------------------------------
// teachers create projects with their classes
// ---------------------------------------------------------------------------

func TestProjectPlanDefinesClassesUpFront(t *testing.T) {
	s := newTestServer(t)
	teacher := s.teacher()

	info := teacher.createProject(`{"name":"ปลาหน้าดิน","classes":["roach","catfish","snakehead"],"submissions_open":true}`)
	base := "/api/projects/" + info.ID

	if info.ClassCount != 3 || !info.SubmissionsOpen {
		t.Fatalf("info = %+v", info)
	}

	var state annotation.ProjectState
	teacher.mustJSON(base, &state)
	if got := strings.Join(state.Classes, ","); got != "roach,catfish,snakehead" {
		t.Fatalf("classes = %q", got)
	}
	if !state.CanReview {
		t.Fatal("owner should be able to review")
	}

	// Empty names and duplicates are refused, and nothing is created.
	teacher.expect(http.MethodPost, "/api/projects",
		`{"name":"Broken","classes":["a","a"]}`, http.StatusBadRequest)
	teacher.expect(http.MethodPost, "/api/projects",
		`{"name":"Broken","classes":["a",""]}`, http.StatusBadRequest)
	var list struct {
		Projects []annotation.ProjectInfo `json:"projects"`
	}
	teacher.mustJSON("/api/projects", &list)
	for _, p := range list.Projects {
		if p.Name == "Broken" {
			t.Fatalf("invalid plan still created project %+v", p)
		}
	}
}

func TestOnlyTeachersCreateProjects(t *testing.T) {
	s := newTestServer(t)
	teacher := s.teacher()
	student := s.student()

	student.expect(http.MethodPost, "/api/projects", `{"name":"student project"}`, http.StatusForbidden)
	if res := teacher.json(http.MethodPost, "/api/projects", `{"name":"ok"}`); res.StatusCode != http.StatusCreated {
		t.Fatalf("teacher create: %d", res.StatusCode)
	} else {
		res.Body.Close()
	}
}

// ---------------------------------------------------------------------------
// the full review flow
// ---------------------------------------------------------------------------

func TestSubmissionApproveFlow(t *testing.T) {
	s := newTestServer(t)
	teacher := s.teacher()
	students := roster(t, teacher)
	somying := students[studentUser]

	info := teacher.createProject(`{"name":"Fish study","classes":["roach","catfish"]}`)
	base := "/api/projects/" + info.ID

	// Closed project: a student cannot submit yet.
	student := s.login(studentUser, studentPass)
	student.expect(http.MethodGet, base, "", http.StatusForbidden)

	// Joining the project and opening submissions lets the student work.
	teacher.expect(http.MethodPost, base+"/members", `{"user_ids":["`+somying.ID+`"]}`, http.StatusOK)
	teacher.expect(http.MethodPut, base+"/submissions-open", `{"open":true}`, http.StatusOK)

	var opened annotation.ProjectState
	student.mustJSON(base, &opened)
	if !opened.CanSubmit || opened.CanReview {
		t.Fatalf("student flags = %+v", opened)
	}

	// A student may not import straight into the dataset.
	student.uploadFail(base+"/images", "photo.jpg")

	// Upload a photo: it becomes a draft the student can still draw on.
	sid, _ := student.uploadFiles(base+"/submissions", "photo.jpg", []byte(onePixel))

	var detail struct {
		Submission annotation.Submission `json:"submission"`
		CanReview  bool                  `json:"can_review"`
		CanEdit    bool                  `json:"can_edit"`
	}
	student.mustJSON(base+"/submissions/"+sid, &detail)
	if detail.Submission.ID != sid || detail.Submission.Status != "draft" {
		t.Fatalf("submission detail = %+v", detail.Submission)
	}
	if detail.CanReview {
		t.Fatal("a student must not get review rights")
	}
	if !detail.CanEdit {
		t.Fatal("a draft must stay editable by its author")
	}

	// Boxes drawn by the student, class id 1 = catfish.
	student.expect(http.MethodPut, base+"/submissions/"+sid+"/labels",
		`{"labels":[{"class_id":1,"x_center":0.5,"y_center":0.5,"width":0.2,"height":0.3}]}`, http.StatusOK)

	// A class id outside the teacher's list is refused.
	student.expect(http.MethodPut, base+"/submissions/"+sid+"/labels",
		`{"labels":[{"class_id":5,"x_center":0.5,"y_center":0.5,"width":0.2,"height":0.2}]}`,
		http.StatusBadRequest)

	// Until submitted the teacher's queue is empty.
	var pending struct {
		Projects []annotation.PendingGroup `json:"projects"`
		Total    int                       `json:"total"`
	}
	teacher.mustJSON("/api/pending", &pending)
	if pending.Total != 0 {
		t.Fatalf("pending before submit = %+v", pending)
	}
	student.expect(http.MethodPost, base+"/submissions/"+sid+"/submit", "", http.StatusOK)

	teacher.mustJSON("/api/pending", &pending)
	if pending.Total != 1 || len(pending.Projects) != 1 {
		t.Fatalf("pending after submit = %+v", pending)
	}
	if pending.Projects[0].Project.Name != "Fish study" {
		t.Fatalf("group = %+v", pending.Projects[0])
	}
	got := pending.Projects[0].Submissions[0]
	if got.ID != sid || got.Status != "pending" || got.BoxCount != 1 || got.StudentName == "" {
		t.Fatalf("submission = %+v", got)
	}

	// The photo is not part of the dataset yet.
	var before annotation.ProjectState
	teacher.mustJSON(base, &before)
	if len(before.Images) != 0 {
		t.Fatalf("images before approve = %+v", before.Images)
	}

	// Only the owner reviews.
	student.expect(http.MethodPost, base+"/submissions/"+sid+"/approve", "", http.StatusForbidden)
	teacher.expect(http.MethodPost, base+"/submissions/"+sid+"/approve", `{"note":"ชัดดี"}`, http.StatusOK)

	var after annotation.ProjectState
	teacher.mustJSON(base, &after)
	if len(after.Images) != 1 {
		t.Fatalf("images after approve = %+v", after.Images)
	}
	approved := after.Images[0]
	if approved.OriginalName != "photo.jpg" || approved.UploadedBy != somying.ID {
		t.Fatalf("approved image should credit the student: %+v", approved)
	}
	if approved.LabelCount != 1 {
		t.Fatalf("approved boxes = %+v", approved)
	}
	var labels struct {
		Labels []annotation.Label `json:"labels"`
	}
	teacher.mustJSON(base+"/images/"+approved.Name+"/labels", &labels)
	if len(labels.Labels) != 1 || labels.Labels[0].ClassID != 1 {
		t.Fatalf("labels = %+v", labels.Labels)
	}

	// Export contains exactly the approved photo.
	names := zipNames(t, teacher.raw(base+"/export"))
	if !names["images/photo.jpg"] || !names["labels/photo.txt"] {
		t.Fatalf("export = %v", names)
	}

	// A decided submission is locked for the student.
	student.expect(http.MethodPut, base+"/submissions/"+sid+"/labels", `{"labels":[]}`, http.StatusForbidden)
	student.expect(http.MethodPost, base+"/submissions/"+sid+"/withdraw", "", http.StatusForbidden)

	// The student's own page shows the decision.
	var mine struct {
		Projects []annotation.MyGroup `json:"projects"`
	}
	student.mustJSON("/api/mywork", &mine)
	if len(mine.Projects) != 1 {
		t.Fatalf("mywork = %+v", mine.Projects)
	}
	mineSub := mine.Projects[0].Submissions[0]
	if mineSub.Status != "approved" || mineSub.ReviewName == "" {
		t.Fatalf("student should see the approval: %+v", mineSub)
	}
}

func TestSubmissionRejectAndResubmit(t *testing.T) {
	s := newTestServer(t)
	teacher := s.teacher()
	somying := roster(t, teacher)[studentUser]

	info := teacher.createProject(`{"name":"Reject me","classes":["a"],"submissions_open":true}`)
	base := "/api/projects/" + info.ID
	teacher.expect(http.MethodPost, base+"/members", `{"user_ids":["`+somying.ID+`"]}`, http.StatusOK)

	student := s.login(studentUser, studentPass)
	sid, _ := student.uploadFiles(base+"/submissions", "blurry.jpg", []byte(onePixel))
	student.expect(http.MethodPut, base+"/submissions/"+sid+"/labels",
		`{"labels":[{"class_id":0,"x_center":0.5,"y_center":0.5,"width":0.2,"height":0.2}]}`, http.StatusOK)
	student.expect(http.MethodPost, base+"/submissions/"+sid+"/submit", "", http.StatusOK)

	// A rejection must carry a reason the student can read.
	teacher.expect(http.MethodPost, base+"/submissions/"+sid+"/reject", `{"note":""}`, http.StatusBadRequest)
	teacher.expect(http.MethodPost, base+"/submissions/"+sid+"/reject",
		`{"note":"ภาพเบลอ วาดกรอบไม่ตรง"}`, http.StatusOK)

	// The photo is not approved yet, so it does not appear in the active dataset.
	var state annotation.ProjectState
	teacher.mustJSON(base, &state)
	if len(state.Images) != 0 {
		t.Fatalf("rejected image should not be active: %+v", state.Images)
	}
	// Rejected submissions stay in the staging area, not in the trash.
	if len(state.Trash) != 0 {
		t.Fatalf("rejected submission should not go to trash: %+v", state.Trash)
	}

	// The student sees the reason and can edit the boxes, then resubmit.
	var mine struct {
		Projects []annotation.MyGroup `json:"projects"`
	}
	student.mustJSON("/api/mywork", &mine)
	sub := mine.Projects[0].Submissions[0]
	if sub.Status != "rejected" || sub.ReviewNote != "ภาพเบลอ วาดกรอบไม่ตรง" {
		t.Fatalf("student view = %+v", sub)
	}

	var detail struct {
		Submission annotation.Submission `json:"submission"`
		CanEdit    bool                  `json:"can_edit"`
	}
	student.mustJSON(base+"/submissions/"+sid, &detail)
	if !detail.CanEdit || detail.Submission.Status != "rejected" {
		t.Fatalf("rejected submission must stay editable: %+v", detail)
	}

	student.expect(http.MethodPut, base+"/submissions/"+sid+"/labels",
		`{"labels":[{"class_id":0,"x_center":0.6,"y_center":0.6,"width":0.3,"height":0.3}]}`, http.StatusOK)
	student.expect(http.MethodPost, base+"/submissions/"+sid+"/submit", "", http.StatusOK)

	student.mustJSON(base+"/submissions/"+sid, &detail)
	if detail.Submission.Status != "pending" || detail.Submission.ReviewNote != "" {
		t.Fatalf("resubmitted submission should be pending with reason cleared: %+v", detail.Submission)
	}

	// Teacher can now approve the resubmitted work.
	teacher.expect(http.MethodPost, base+"/submissions/"+sid+"/approve", `{"note":"ดีขึ้นแล้ว"}`, http.StatusOK)
	teacher.mustJSON(base, &state)
	if len(state.Images) != 1 || state.Images[0].LabelCount != 1 {
		t.Fatalf("approved image = %+v", state.Images)
	}

	// A second rejection can be approved after resubmitting too.
	sid2, _ := student.uploadFiles(base+"/submissions", "second.jpg", []byte(onePixel))
	student.expect(http.MethodPost, base+"/submissions/"+sid2+"/submit", "", http.StatusOK)
	teacher.expect(http.MethodPost, base+"/submissions/"+sid2+"/reject", `{"note":"แสงน้อย"}`, http.StatusOK)
	student.expect(http.MethodPost, base+"/submissions/"+sid2+"/submit", "", http.StatusOK)
	teacher.expect(http.MethodPost, base+"/submissions/"+sid2+"/approve", "", http.StatusOK)
	teacher.mustJSON(base, &state)
	if len(state.Images) != 2 {
		t.Fatalf("after second approve = %+v", state.Images)
	}

	// A rejected photo can be removed from the student's work page.
	sid3, _ := student.uploadFiles(base+"/submissions", "remove-me.jpg", []byte(onePixel))
	student.expect(http.MethodPost, base+"/submissions/"+sid3+"/submit", "", http.StatusOK)
	teacher.expect(http.MethodPost, base+"/submissions/"+sid3+"/reject", `{"note":"ใช้ไม่ได้"}`, http.StatusOK)
	student.expect(http.MethodDelete, base+"/submissions/"+sid3, "", http.StatusOK)
	student.expect(http.MethodGet, base+"/submissions/"+sid3, "", http.StatusNotFound)
}

func TestSubmissionGuards(t *testing.T) {
	s := newTestServer(t)
	teacher := s.teacher()
	somying := roster(t, teacher)[studentUser]
	another := teacher.newStudent("other01", "password123")

	info := teacher.createProject(`{"name":"Privacy","classes":["a"],"submissions_open":true}`)
	base := "/api/projects/" + info.ID
	teacher.expect(http.MethodPost, base+"/members",
		`{"user_ids":["`+somying.ID+`","`+another.ID+`"]}`, http.StatusOK)

	first := s.login(studentUser, studentPass)
	sid, _ := first.uploadFiles(base+"/submissions", "mine.jpg", []byte(onePixel))

	second := s.login("other01", "password123")
	second.expect(http.MethodGet, base+"/submissions/"+sid, "", http.StatusForbidden)
	second.expect(http.MethodGet, base+"/submissions/"+sid+"/image", "", http.StatusForbidden)
	second.expect(http.MethodPut, base+"/submissions/"+sid+"/labels", `{"labels":[]}`, http.StatusForbidden)
	second.expect(http.MethodPost, base+"/submissions/"+sid+"/submit", "", http.StatusForbidden)
	second.expect(http.MethodDelete, base+"/submissions/"+sid, "", http.StatusForbidden)

	// Each student only sees their own queue.
	var list struct {
		Submissions []annotation.Submission `json:"submissions"`
	}
	second.mustJSON(base+"/submissions", &list)
	if len(list.Submissions) != 0 {
		t.Fatalf("other student sees %+v", list.Submissions)
	}
	first.mustJSON(base+"/submissions", &list)
	if len(list.Submissions) != 1 {
		t.Fatalf("author sees %+v", list.Submissions)
	}

	// Drafts and rejected work can be deleted by their author; pending or
	// approved work cannot disappear silently.
	draftID, _ := first.uploadFiles(base+"/submissions", "temp.jpg", []byte(onePixel))
	first.expect(http.MethodDelete, base+"/submissions/"+draftID, "", http.StatusOK)
	first.expect(http.MethodGet, base+"/submissions/"+draftID, "", http.StatusNotFound)

	first.expect(http.MethodPost, base+"/submissions/"+sid+"/submit", "", http.StatusOK)
	first.expect(http.MethodDelete, base+"/submissions/"+sid, "", http.StatusBadRequest)

	// A student who was removed can no longer open the project.
	teacher.expect(http.MethodDelete, base+"/members/"+somying.ID, "", http.StatusOK)
	first.expect(http.MethodGet, base, "", http.StatusForbidden)
	first.expect(http.MethodGet, base+"/submissions/"+sid, "", http.StatusForbidden)
	// The teacher still sees the row for the record.
	var after annotation.ProjectState
	teacher.mustJSON(base, &after)
	if after.MemberCount != 1 {
		t.Fatalf("members = %+v", after)
	}
}

func TestWithdrawReturnsDraftToStudent(t *testing.T) {
	s := newTestServer(t)
	teacher := s.teacher()
	somying := roster(t, teacher)[studentUser]

	info := teacher.createProject(`{"name":"Withdraw","classes":["a"],"submissions_open":true}`)
	base := "/api/projects/" + info.ID
	teacher.expect(http.MethodPost, base+"/members", `{"user_ids":["`+somying.ID+`"]}`, http.StatusOK)

	student := s.login(studentUser, studentPass)
	sid, _ := student.uploadFiles(base+"/submissions", "w.jpg", []byte(onePixel))
	student.expect(http.MethodPost, base+"/submissions/"+sid+"/submit", "", http.StatusOK)

	var pending struct {
		Total int `json:"total"`
	}
	teacher.mustJSON("/api/pending", &pending)
	if pending.Total != 1 {
		t.Fatalf("pending = %+v", pending)
	}

	student.expect(http.MethodPost, base+"/submissions/"+sid+"/withdraw", "", http.StatusOK)
	teacher.mustJSON("/api/pending", &pending)
	if pending.Total != 0 {
		t.Fatalf("pending after withdraw = %+v", pending)
	}
	// Still a draft, so the student can keep drawing.
	student.expect(http.MethodPut, base+"/submissions/"+sid+"/labels",
		`{"labels":[{"class_id":0,"x_center":0.4,"y_center":0.4,"width":0.2,"height":0.2}]}`, http.StatusOK)
}

func TestSubmitNeedsClasses(t *testing.T) {
	s := newTestServer(t)
	teacher := s.teacher()
	somying := roster(t, teacher)[studentUser]

	info := teacher.createProject(`{"name":"No classes","submissions_open":true}`)
	base := "/api/projects/" + info.ID
	teacher.expect(http.MethodPost, base+"/members", `{"user_ids":["`+somying.ID+`"]}`, http.StatusOK)

	student := s.login(studentUser, studentPass)
	sid, _ := student.uploadFiles(base+"/submissions", "n.jpg", []byte(onePixel))
	// The class list is empty, so sending it in is refused.
	student.expect(http.MethodPost, base+"/submissions/"+sid+"/submit", "", http.StatusBadRequest)
}

// ---------------------------------------------------------------------------
// visibility and roles
// ---------------------------------------------------------------------------

func TestStudentsSeeOnlyTheirOwnPhotos(t *testing.T) {
	s := newTestServer(t)
	teacher := s.teacher()
	somying := roster(t, teacher)[studentUser]

	info := teacher.createProject(`{"name":"Shared","classes":["a"],"submissions_open":true}`)
	base := "/api/projects/" + info.ID
	teacher.expect(http.MethodPost, base+"/members", `{"user_ids":["`+somying.ID+`"]}`, http.StatusOK)

	// Teacher's own import plus an approved student photo.
	teacherPhoto, _ := teacher.uploadFiles(base+"/images", "teacher.jpg", []byte(onePixel))

	student := s.login(studentUser, studentPass)
	sid, _ := student.uploadFiles(base+"/submissions", "student.jpg", []byte(onePixel))
	student.expect(http.MethodPost, base+"/submissions/"+sid+"/submit", "", http.StatusOK)
	teacher.expect(http.MethodPost, base+"/submissions/"+sid+"/approve", "", http.StatusOK)

	var mine annotation.ProjectState
	student.mustJSON(base, &mine)
	if len(mine.Images) != 1 || mine.Images[0].OriginalName != "student.jpg" {
		t.Fatalf("student image list = %+v", mine.Images)
	}
	if mine.CanReview || !mine.CanSubmit {
		t.Fatalf("flags = %+v", mine)
	}
	if len(mine.Trash) != 0 {
		t.Fatalf("student should not browse project trash: %+v", mine.Trash)
	}

	// Another student's photo is not readable by this student.
	student.expect(http.MethodGet, base+"/images/"+teacherPhoto, "", http.StatusForbidden)
	student.expect(http.MethodGet, base+"/images/"+teacherPhoto+"/labels", "", http.StatusForbidden)
	student.expect(http.MethodDelete, base+"/images/"+teacherPhoto, "", http.StatusForbidden)
	teacher.expect(http.MethodDelete, base+"/images/"+teacherPhoto, "", http.StatusOK)

	// And class lists are still teacher-only.
	student.expect(http.MethodPost, base+"/classes", `{"name":"sneaky"}`, http.StatusForbidden)
	student.expect(http.MethodPut, base+"/classes", `{"classes":["a","b"]}`, http.StatusForbidden)
	student.expect(http.MethodGet, base+"/export", "", http.StatusForbidden)

	var teacherState annotation.ProjectState
	teacher.mustJSON(base, &teacherState)
	if len(teacherState.Images) != 1 {
		t.Fatalf("teacher sees trashed-only list? %+v", teacherState.Images)
	}
	if len(teacherState.Trash) != 1 {
		t.Fatalf("teacher trash = %+v", teacherState.Trash)
	}
}

func TestProjectListScoping(t *testing.T) {
	s := newTestServer(t)
	teacher := s.teacher()
	somying := roster(t, teacher)[studentUser]
	other := teacher.newStudent("other02", "password123")

	mineInfo := teacher.createProject(`{"name":"Course A","classes":["a"],"submissions_open":true}`)
	hidden := teacher.createProject(`{"name":"Course B","classes":["a"]}`)

	base := "/api/projects/" + mineInfo.ID
	teacher.expect(http.MethodPost, base+"/members",
		`{"user_ids":["`+somying.ID+`","`+other.ID+`"]}`, http.StatusOK)
	teacher.expect(http.MethodPut, base+"/submissions-open", `{"open":true}`, http.StatusOK)

	names := func(c *client) []string {
		var list struct {
			Projects []annotation.ProjectInfo `json:"projects"`
		}
		c.mustJSON("/api/projects", &list)
		out := []string{}
		for _, p := range list.Projects {
			out = append(out, p.Name)
		}
		return out
	}

	joined := s.login(studentUser, studentPass)
	got := names(joined)
	if strings.Join(got, ",") != "Course A" {
		t.Fatalf("student sees %v want [Course A]", got)
	}
	outsider := s.login("other02", "password123")
	if strings.Join(names(outsider), ",") != "Course A" {
		t.Fatalf("other student sees wrong list")
	}

	// Teachers see everything.
	all := names(teacher)
	if len(all) != 2 {
		t.Fatalf("teacher sees %v", all)
	}
	joined.expect(http.MethodGet, "/api/projects/"+hidden.ID, "", http.StatusForbidden)

	// Pending counts reach the list so the UI can badge it.
	var list struct {
		Projects []annotation.ProjectInfo `json:"projects"`
	}
	teacher.mustJSON("/api/projects", &list)
	for _, p := range list.Projects {
		if p.Name == "Course A" && p.MemberCount != 2 {
			t.Fatalf("member count = %+v", p)
		}
	}
}

func TestTrashListScoping(t *testing.T) {
	s := newTestServer(t)
	teacher := s.teacher()
	somying := roster(t, teacher)[studentUser]
	other := teacher.newStudent("other03", "password123")

	info := teacher.createProject(`{"name":"Trashed","classes":["a"],"submissions_open":true}`)
	base := "/api/projects/" + info.ID
	teacher.expect(http.MethodPost, base+"/members",
		`{"user_ids":["`+somying.ID+`","`+other.ID+`"]}`, http.StatusOK)

	// Only manually-deleted project images appear in the trash now;
	// rejected submissions stay in the staging area for the student to fix.
	name, _ := teacher.uploadFiles(base+"/images", "deleted.jpg", []byte(onePixel))
	teacher.expect(http.MethodDelete, base+"/images/"+name, "", http.StatusOK)

	// The owner sees the project trash.
	var groups struct {
		Projects []annotation.ProjectTrash `json:"projects"`
	}
	teacher.mustJSON("/api/trash", &groups)
	if len(groups.Projects) != 1 || len(groups.Projects[0].Images) != 1 {
		t.Fatalf("teacher trash = %+v", groups.Projects)
	}
	if groups.Projects[0].Project.Name != "Trashed" {
		t.Fatalf("group must name the project: %+v", groups.Projects[0].Project)
	}

	// Students do not see the teacher's deleted images.
	student := s.login(studentUser, studentPass)
	student.mustJSON("/api/trash", &groups)
	if len(groups.Projects) != 0 {
		t.Fatalf("student trash = %+v", groups.Projects)
	}
	outsider := s.login("other03", "password123")
	outsider.mustJSON("/api/trash", &groups)
	if len(groups.Projects) != 0 {
		t.Fatalf("unrelated student sees trash: %+v", groups.Projects)
	}
}

// ---------------------------------------------------------------------------
// basic project workflow, kept from before the review flow
// ---------------------------------------------------------------------------

func TestProjectWorkflow(t *testing.T) {
	s := newTestServer(t)
	teacher := s.teacher()

	var list struct {
		Projects []annotation.ProjectInfo `json:"projects"`
	}
	teacher.mustJSON("/api/projects", &list)
	if len(list.Projects) != 0 {
		t.Fatalf("projects = %+v", list.Projects)
	}

	info := teacher.createProject(`{"name":"Fish study","classes":["fish","leaf_disease"]}`)
	base := "/api/projects/" + info.ID
	teacher.expect(http.MethodPost, "/api/projects", `{"name":"   "}`, http.StatusBadRequest)
	teacher.expect(http.MethodGet, "/api/projects/0000000000000000", "", http.StatusNotFound)

	// Images and boxes.
	name, _ := teacher.uploadFiles(base+"/images", "fish1.jpg", []byte("image-bytes"))
	teacher.expect(http.MethodPut, base+"/images/"+name+"/labels",
		`{"labels":[{"class_id":0,"x_center":0.5,"y_center":0.5,"width":0.2,"height":0.2},`+
			`{"class_id":1,"x_center":0.2,"y_center":0.3,"width":0.1,"height":0.2}]}`, http.StatusOK)
	teacher.expect(http.MethodPut, base+"/images/"+name+"/labels",
		`{"labels":[{"class_id":9,"x_center":0.5,"y_center":0.5,"width":0.2,"height":0.2}]}`,
		http.StatusBadRequest)

	var state annotation.ProjectState
	teacher.mustJSON(base, &state)
	if len(state.Classes) != 2 || state.Classes[0] != "fish" {
		t.Fatalf("classes = %+v", state.Classes)
	}
	if len(state.Images) != 1 || state.Images[0].LabelCount != 2 {
		t.Fatalf("images = %+v", state.Images)
	}
	if state.ImageCount != 1 || state.BoxCount != 2 {
		t.Fatalf("counts = %+v", state)
	}
	if state.Images[0].UploadedBy != teacher.id {
		t.Fatalf("uploader = %+v", state.Images[0])
	}

	// Export layout.
	names := zipNames(t, teacher.raw(base+"/export"))
	for _, want := range []string{"classes.txt", "images/fish1.jpg", "labels/fish1.txt", "manifest.csv"} {
		if !names[want] {
			t.Fatalf("export missing %q: %v", want, names)
		}
	}

	// Renaming and deleting stay owner-only.
	teacher.expect(http.MethodPatch, base, `{"name":"Renamed study"}`, http.StatusOK)
	s.student().expect(http.MethodPatch, base, `{"name":"hijack"}`, http.StatusForbidden)
	teacher.expect(http.MethodDelete, base, "", http.StatusOK)
	teacher.expect(http.MethodGet, base, "", http.StatusNotFound)
}

func TestProjectPagesAndRedirects(t *testing.T) {
	s := newTestServer(t)
	anon := s.anonymous()

	for path, want := range map[string]string{
		"/projects":      "projects.js",
		"/project":       "project.js",
		"/trash":         "trash.js",
		"/pending":       "pending.js",
		"/mywork":        "mywork.js",
		"/users":         "users.js",
		"/login":         "login.js",
		"/annotate":      "annotate.js",
		"/projects.html": "projects.js",
	} {
		res := anon.do(http.MethodGet, path, nil, "")
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", path, res.StatusCode)
		}
		if !strings.Contains(string(body), want) {
			t.Fatalf("%s: page does not load %s", path, want)
		}
	}

	// Query strings survive the redirect so ?id= keeps working.
	res := anon.do(http.MethodGet, "/project?id=0123456789abcdef", nil, "")
	res.Body.Close()
	if got := res.Request.URL.Path; got != "/project.html" {
		t.Fatalf("redirect path = %s", got)
	}
	if got := res.Request.URL.Query().Get("id"); got != "0123456789abcdef" {
		t.Fatalf("redirect query = %s", got)
	}
}

func TestDatasetAPIRequiresLogin(t *testing.T) {
	s := newTestServer(t)
	s.anonymous().expect(http.MethodGet, "/api/datasets", "", http.StatusUnauthorized)
	s.anonymous().expect(http.MethodPost, "/api/datasets", "", http.StatusUnauthorized)

	teacher := s.teacher()
	teacher.expect(http.MethodGet, "/api/datasets", "", http.StatusOK)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// uploadFail asserts a student cannot use a teacher-only multipart route.
func (c *client) uploadFail(path, filename string) {
	c.t.Helper()
	form, contentType := imageForm(c.t, "files", filename, []byte(onePixel))
	res := c.do(http.MethodPost, path, form, contentType)
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(res.Body)
		c.t.Fatalf("POST %s should be forbidden: status %d body %s", path, res.StatusCode, body)
	}
}

func zipNames(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	found := map[string]bool{}
	for _, f := range zr.File {
		found[f.Name] = true
	}
	return found
}
