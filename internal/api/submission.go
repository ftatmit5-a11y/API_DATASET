package api

import (
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"

	"yoloapi/internal/annotation"
)

// submissionContext resolves the project, its submission store, and the row in
// the path. access is what the caller may do in the project.
type submissionContext struct {
	access     annotation.ProjectAccess
	store      *annotation.SubmissionStore
	submission annotation.Submission
}

func (s *Server) submissionContext(r *http.Request) (submissionContext, error) {
	var zero submissionContext
	access, err := s.Projects.Access(r.PathValue("id"), currentUser(r).ID)
	if err != nil {
		return zero, err
	}
	if !access.IsOwner && !access.IsMember {
		return zero, fmt.Errorf("%w: you are not part of this project", annotation.ErrForbidden)
	}
	store, err := s.Projects.Submissions(access.Project.ID)
	if err != nil {
		return zero, err
	}
	sub, err := store.Get(r.PathValue("sid"))
	if err != nil {
		return zero, err
	}
	// Only the reviewer or the author may look at a submission.
	if !access.IsOwner && sub.StudentID != access.User.ID {
		return zero, fmt.Errorf("%w: this submission belongs to another student", annotation.ErrForbidden)
	}
	return submissionContext{access: access, store: store, submission: sub}, nil
}

// submissionReviewer requires the caller to own the project: approving and
// rejecting is the teacher's job.
func (s *Server) submissionReviewer(r *http.Request) (submissionContext, error) {
	ctx, err := s.submissionContext(r)
	if err != nil {
		return ctx, err
	}
	if !ctx.access.IsOwner {
		return ctx, fmt.Errorf("%w: only the project owner reviews submissions", annotation.ErrForbidden)
	}
	return ctx, nil
}

// submissionAuthor requires the caller to be the student who created it.
func (s *Server) submissionAuthor(r *http.Request) (submissionContext, error) {
	ctx, err := s.submissionContext(r)
	if err != nil {
		return ctx, err
	}
	if ctx.submission.StudentID != ctx.access.User.ID {
		return ctx, fmt.Errorf("%w: only the author can change this submission", annotation.ErrForbidden)
	}
	return ctx, nil
}

// submissionList: the owner sees the whole queue, students only their own work.
// ?status=draft,pending narrows it.
func (s *Server) submissionList(w http.ResponseWriter, r *http.Request) {
	ctxAccess, err := s.Projects.Access(r.PathValue("id"), currentUser(r).ID)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	if !ctxAccess.IsOwner && !ctxAccess.IsMember {
		writeAnnotationErr(w, fmt.Errorf("%w: you are not part of this project", annotation.ErrForbidden))
		return
	}
	store, err := s.Projects.Submissions(ctxAccess.Project.ID)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}

	statuses := splitStatuses(r.URL.Query().Get("status"))
	studentID := ctxAccess.User.ID
	if ctxAccess.IsOwner {
		// Owners may ask for one student's queue explicitly.
		if filter := strings.TrimSpace(r.URL.Query().Get("student")); filter != "" {
			studentID = filter
		} else {
			studentID = ""
		}
	}
	list, err := store.List(studentID, statuses...)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	counts, err := store.Counts()
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project_id":  ctxAccess.Project.ID,
		"submissions": list,
		"counts":      counts,
		"can_review":  ctxAccess.CanReview(),
		"can_submit":  ctxAccess.CanSubmit(),
	})
}

// submissionCreate stores student photos as drafts in the project staging area.
func (s *Server) submissionCreate(w http.ResponseWriter, r *http.Request) {
	access, err := s.Projects.Access(r.PathValue("id"), currentUser(r).ID)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	if !access.CanSubmit() {
		if !access.IsMember {
			err = fmt.Errorf("%w: you are not part of this project", annotation.ErrForbidden)
		} else if access.IsTeacher {
			err = fmt.Errorf("%w: teachers import photos from the project page", annotation.ErrForbidden)
		} else {
			err = fmt.Errorf("%w: this project is not accepting submissions", annotation.ErrForbidden)
		}
		writeAnnotationErr(w, err)
		return
	}
	store, err := s.Projects.Submissions(access.Project.ID)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.MaxUpload)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "upload exceeds size limit")
			return
		}
		writeError(w, http.StatusBadRequest, "expected multipart form with image files")
		return
	}

	var headers []*multipart.FileHeader
	if r.MultipartForm != nil {
		headers = append(headers, r.MultipartForm.File["files"]...)
		headers = append(headers, r.MultipartForm.File["file"]...)
	}
	if len(headers) == 0 {
		writeError(w, http.StatusBadRequest, "form field \"files\" (images) is required")
		return
	}
	note := ""
	if r.MultipartForm != nil && r.MultipartForm.Value["note"] != nil {
		note = r.MultipartForm.Value["note"][0]
	}

	created := []annotation.Submission{}
	for _, header := range headers {
		file, err := header.Open()
		if err != nil {
			writeError(w, http.StatusBadRequest, "could not read uploaded image")
			return
		}
		sub, err := store.Add(header.Filename, access.User.ID, note, file)
		file.Close()
		if err != nil {
			writeAnnotationErr(w, err)
			return
		}
		created = append(created, sub)
	}
	w.Header().Set("Location", fmt.Sprintf("%s/submissions/%s",
		strings.TrimSuffix(r.URL.Path, "/"), created[0].ID))
	writeJSON(w, http.StatusCreated, map[string]any{"submissions": created})
}

func (s *Server) submissionGet(w http.ResponseWriter, r *http.Request) {
	ctx, err := s.submissionContext(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"submission": ctx.submission,
		"can_review": ctx.access.CanReview(),
		"can_edit":   ctx.canEdit(),
	})
}

// canEdit reports whether the caller may still change the boxes of a submission.
func (c submissionContext) canEdit() bool {
	if c.submission.Status == annotation.SubmissionApproved {
		return false
	}
	return c.submission.StudentID == c.access.User.ID || c.access.IsOwner
}

// submissionImage serves the staged photo so the review page can show it.
func (s *Server) submissionImage(w http.ResponseWriter, r *http.Request) {
	ctx, err := s.submissionContext(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	path, err := ctx.store.ImagePath(ctx.submission.ID)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	http.ServeFile(w, r, path)
}

// submissionPutLabels saves the boxes a student drew while the work is still
// under review.
func (s *Server) submissionPutLabels(w http.ResponseWriter, r *http.Request) {
	ctx, err := s.submissionContext(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	if !ctx.canEdit() {
		if ctx.access.IsOwner && ctx.submission.StudentID != ctx.access.User.ID {
			writeAnnotationErr(w, fmt.Errorf("%w: only the author can draw boxes on their submission", annotation.ErrForbidden))
			return
		}
		writeAnnotationErr(w, fmt.Errorf("%w: this submission was already reviewed", annotation.ErrForbidden))
		return
	}
	var req struct {
		Labels []annotation.Label `json:"labels"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := ctx.store.SaveLabels(ctx.submission.ID, req.Labels); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"submission_id": ctx.submission.ID, "saved": len(req.Labels)})
}

// submissionSubmit moves a draft into the teacher's queue.
func (s *Server) submissionSubmit(w http.ResponseWriter, r *http.Request) {
	ctx, err := s.submissionAuthor(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	sub, err := ctx.store.Submit(ctx.submission.ID, ctx.access.User.ID)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

// submissionWithdraw pulls pending work back to draft before it is reviewed.
func (s *Server) submissionWithdraw(w http.ResponseWriter, r *http.Request) {
	ctx, err := s.submissionAuthor(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	sub, err := ctx.store.Withdraw(ctx.submission.ID, ctx.access.User.ID)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

// submissionDelete removes the caller's own draft and its staged file.
func (s *Server) submissionDelete(w http.ResponseWriter, r *http.Request) {
	ctx, err := s.submissionAuthor(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	if err := ctx.store.Delete(ctx.submission.ID, ctx.access.User.ID); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": ctx.submission.ID})
}

// submissionApprove moves the photo and its boxes into the project for good.
func (s *Server) submissionApprove(w http.ResponseWriter, r *http.Request) {
	s.decideSubmission(w, r, true)
}

// submissionReject sends the photo to the project trash with a required reason.
func (s *Server) submissionReject(w http.ResponseWriter, r *http.Request) {
	s.decideSubmission(w, r, false)
}

func (s *Server) decideSubmission(w http.ResponseWriter, r *http.Request, approve bool) {
	ctx, err := s.submissionReviewer(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}

	var (
		sub annotation.Submission
		img annotation.ImageInfo
	)
	if approve {
		sub, img, err = ctx.store.Approve(ctx.submission.ID, ctx.access.User.ID, req.Note)
	} else {
		sub, img, err = ctx.store.Reject(ctx.submission.ID, ctx.access.User.ID, req.Note)
	}
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"submission": sub,
		"image":      img,
		"in_trash":   !approve,
	})
}

// pendingList is the teacher's review queue across their own projects.
func (s *Server) pendingList(w http.ResponseWriter, r *http.Request) {
	groups, err := s.Projects.PendingSubmissions(currentUser(r).ID)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	total := 0
	for _, g := range groups {
		total += len(g.Submissions)
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": groups, "total": total})
}

// myWorkList is the student page: every project they belong to plus their own
// submissions and the teacher's decisions.
func (s *Server) myWorkList(w http.ResponseWriter, r *http.Request) {
	groups, err := s.Projects.MySubmissions(currentUser(r).ID)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": groups})
}

// projectSubmissionsSwitch opens or closes student submissions.
func (s *Server) projectSubmissionsSwitch(w http.ResponseWriter, r *http.Request) {
	_, _, err := s.projectOwnerAccess(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	var req struct {
		Open *bool `json:"open"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Open == nil {
		writeError(w, http.StatusBadRequest, `expected {"open": true|false}`)
		return
	}
	info, err := s.Projects.SetSubmissionsOpen(r.PathValue("id"), *req.Open)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// projectMembersList shows who may submit to the project.
func (s *Server) projectMembersList(w http.ResponseWriter, r *http.Request) {
	_, _, err := s.projectOwnerAccess(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	members, err := s.Projects.Members(r.PathValue("id"))
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// projectMembersAdd enrols students. Body is {"user_ids": ["..."]}.
func (s *Server) projectMembersAdd(w http.ResponseWriter, r *http.Request) {
	access, _, err := s.projectOwnerAccess(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	var req struct {
		UserIDs []string `json:"user_ids"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(req.UserIDs) == 0 {
		writeError(w, http.StatusBadRequest, "user_ids must list at least one student")
		return
	}
	if err := s.Projects.AddMembers(access.Project.ID, access.User.ID, req.UserIDs); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	members, err := s.Projects.Members(access.Project.ID)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// projectMemberRemove drops a student. Their submissions stay for the record.
func (s *Server) projectMemberRemove(w http.ResponseWriter, r *http.Request) {
	_, _, err := s.projectOwnerAccess(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	if err := s.Projects.RemoveMember(r.PathValue("id"), r.PathValue("userId")); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"removed": r.PathValue("userId")})
}

func splitStatuses(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
