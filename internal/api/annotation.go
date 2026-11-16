package api

import (
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	"yoloapi/internal/annotation"
)

func (s *Server) annotatePage(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/annotate.html", http.StatusTemporaryRedirect)
}

func (s *Server) projectsPage(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/projects.html", http.StatusTemporaryRedirect)
}

// pageRedirect forwards a short URL to its static page and keeps the query.
func pageRedirect(w http.ResponseWriter, r *http.Request, page string) {
	target := "/" + page + ".html"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusTemporaryRedirect)
}

func (s *Server) projectPage(w http.ResponseWriter, r *http.Request) {
	pageRedirect(w, r, "project")
}

func (s *Server) trashPage(w http.ResponseWriter, r *http.Request) {
	pageRedirect(w, r, "trash")
}

func (s *Server) pendingPage(w http.ResponseWriter, r *http.Request) {
	pageRedirect(w, r, "pending")
}

func (s *Server) myWorkPage(w http.ResponseWriter, r *http.Request) {
	pageRedirect(w, r, "mywork")
}

func (s *Server) usersPage(w http.ResponseWriter, r *http.Request) {
	pageRedirect(w, r, "users")
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	pageRedirect(w, r, "login")
}

// trashList shows only what the signed-in account may act on: a teacher sees
// the trash of the projects they own, a student sees only photos of theirs that
// were rejected or deleted.
func (s *Server) trashList(w http.ResponseWriter, r *http.Request) {
	groups, err := s.Projects.TrashForUser(currentUser(r).ID)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": groups})
}

func (s *Server) projectsList(w http.ResponseWriter, r *http.Request) {
	projects, err := s.Projects.ListVisible(currentUser(r).ID)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

func (s *Server) projectCreate(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if err := s.requireTeacher(r); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	var plan annotation.ProjectPlan
	if err := decodeJSON(r, &plan); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	info, err := s.Projects.CreateProject(plan, user.ID)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

// projectGet returns the project plus the parts the caller may see. A student
// gets the class list and only their own approved photos; the teacher who owns
// the project also gets every photo and the trash.
func (s *Server) projectGet(w http.ResponseWriter, r *http.Request) {
	access, store, err := s.projectAccess(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	classes, err := store.Classes()
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	images, err := store.Images()
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	trash, err := store.TrashedImages()
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}

	if !access.IsOwner {
		images = filterImagesByUploader(images, access.User.ID)
		trash = []annotation.ImageInfo{}
	}

	state := annotation.ProjectState{
		ProjectInfo: access.Project,
		Classes:     classes,
		Images:      images,
		Trash:       trash,
	}
	state.CanReview = access.CanReview()
	state.CanSubmit = access.CanSubmit()
	writeJSON(w, http.StatusOK, state)
}

func filterImagesByUploader(images []annotation.ImageInfo, userID string) []annotation.ImageInfo {
	out := make([]annotation.ImageInfo, 0, len(images))
	for _, img := range images {
		if img.UploadedBy == userID {
			out = append(out, img)
		}
	}
	return out
}

func (s *Server) projectRename(w http.ResponseWriter, r *http.Request) {
	if _, _, err := s.projectOwnerAccess(r); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	info, err := s.Projects.Rename(r.PathValue("id"), req.Name)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) projectDelete(w http.ResponseWriter, r *http.Request) {
	if _, _, err := s.projectOwnerAccess(r); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	id := r.PathValue("id")
	if err := s.Projects.Delete(id); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

// projectUploadImages is the teacher's own import path. Students reach the same
// photos through submissions, which keeps unreviewed work out of the dataset.
func (s *Server) projectUploadImages(w http.ResponseWriter, r *http.Request) {
	access, store, err := s.projectOwnerAccess(r)
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

	saved := make([]string, 0, len(headers))
	for _, header := range headers {
		file, err := header.Open()
		if err != nil {
			writeError(w, http.StatusBadRequest, "could not read uploaded image")
			return
		}
		name, err := store.AddImage(header.Filename, access.User.ID, file)
		file.Close()
		if err != nil {
			writeAnnotationErr(w, err)
			return
		}
		saved = append(saved, name)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"saved": saved})
}

func (s *Server) projectServeImage(w http.ResponseWriter, r *http.Request) {
	access, store, err := s.projectAccess(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	if _, err := s.visibleImage(r, access, store); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	p, err := store.ImagePath(r.PathValue("name"))
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	http.ServeFile(w, r, p)
}

// projectTrashImage soft-deletes one photo. The owner may trash any photo;
// a student may only remove their own.
func (s *Server) projectTrashImage(w http.ResponseWriter, r *http.Request) {
	access, store, err := s.projectAccess(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	img, err := s.imageForMutation(r, access, store)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	if err := store.TrashImage(img.Name); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"trashed": img.Name})
}

// projectRestoreImage brings a trashed photo back. Only the owner may do it:
// a student whose rejected submission was restored would skip the review step.
func (s *Server) projectRestoreImage(w http.ResponseWriter, r *http.Request) {
	_, store, err := s.projectOwnerAccess(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	img, err := s.imageForName(r, store)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	if err := store.RestoreImage(img.Name); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"restored": img.Name})
}

func (s *Server) projectPurgeImage(w http.ResponseWriter, r *http.Request) {
	_, store, err := s.projectOwnerAccess(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	name := r.PathValue("name")
	img, err := store.Image(name)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	if img.DeletedAt == nil {
		writeAnnotationErr(w, fmt.Errorf("%w: image %s is not in the trash", annotation.ErrInvalid, name))
		return
	}
	if err := store.DeleteImage(img.Name); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": img.Name})
}

// imageForName loads the image named in the path.
func (s *Server) imageForName(r *http.Request, store *annotation.Store) (annotation.ImageInfo, error) {
	img, err := store.Image(r.PathValue("name"))
	if err != nil {
		return annotation.ImageInfo{}, err
	}
	return img, nil
}

// imageForMutation loads the image named in the path and checks the caller may
// change it: the project owner always can, others only their own uploads.
func (s *Server) imageForMutation(r *http.Request, access annotation.ProjectAccess, store *annotation.Store) (annotation.ImageInfo, error) {
	img, err := s.imageForName(r, store)
	if err != nil {
		return annotation.ImageInfo{}, err
	}
	if !access.IsOwner && img.UploadedBy != access.User.ID {
		return annotation.ImageInfo{}, fmt.Errorf("%w: only the project owner or uploader can change this image", annotation.ErrForbidden)
	}
	return img, nil
}

// visibleImage enforces photo privacy for reads: teachers see everything, a
// student only the photos they uploaded themselves.
func (s *Server) visibleImage(r *http.Request, access annotation.ProjectAccess, store *annotation.Store) (annotation.ImageInfo, error) {
	img, err := s.imageForName(r, store)
	if err != nil {
		return annotation.ImageInfo{}, err
	}
	if !access.IsTeacher && img.UploadedBy != access.User.ID {
		return annotation.ImageInfo{}, fmt.Errorf("%w: this photo belongs to another student", annotation.ErrForbidden)
	}
	return img, nil
}

func (s *Server) projectGetLabels(w http.ResponseWriter, r *http.Request) {
	access, store, err := s.projectAccess(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	if _, err := s.visibleImage(r, access, store); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	name := r.PathValue("name")
	labels, err := store.LoadLabels(name)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"image": name, "labels": labels})
}

// projectPutLabels edits boxes of photos already inside the project. Keeping
// this with the owner means student work only enters through a decision.
func (s *Server) projectPutLabels(w http.ResponseWriter, r *http.Request) {
	_, store, err := s.projectOwnerAccess(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	var req struct {
		Labels []annotation.Label `json:"labels"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := r.PathValue("name")
	if err := store.SaveLabels(name, req.Labels); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"image": name, "saved": len(req.Labels)})
}

// projectAddClass appends one class. Classes belong to the teacher who created
// the project: students label against a fixed list.
func (s *Server) projectAddClass(w http.ResponseWriter, r *http.Request) {
	_, store, err := s.projectOwnerAccess(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	classes, err := store.Classes()
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	classes = append(classes, req.Name)
	if err := store.SaveClasses(classes); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	classes, err = store.Classes()
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"classes": classes})
}

func (s *Server) projectPutClasses(w http.ResponseWriter, r *http.Request) {
	_, store, err := s.projectOwnerAccess(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	var req struct {
		Classes []string `json:"classes"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := store.SaveClasses(req.Classes); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	classes, err := store.Classes()
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"classes": classes})
}

// projectExport builds the YOLO ZIP. Only the owner exports, so a student
// cannot pull the reviewed dataset of a course they are taking.
func (s *Server) projectExport(w http.ResponseWriter, r *http.Request) {
	_, store, err := s.projectOwnerAccess(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	id := r.PathValue("id")
	tmp, err := os.CreateTemp("", "yolo-annotations-*.zip")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create archive")
		return
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := store.ExportZip(tmp); err != nil {
		tmp.Close()
		writeError(w, http.StatusInternalServerError, "could not build archive")
		return
	}
	if err := tmp.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, "could not build archive")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "project-"+id+".zip"))
	http.ServeFile(w, r, name)
}

// projectAccess resolves the signed-in account's relationship to the project in
// the path and opens its store. Being the owner or a listed member is enough.
func (s *Server) projectAccess(r *http.Request) (annotation.ProjectAccess, *annotation.Store, error) {
	access, err := s.Projects.Access(r.PathValue("id"), currentUser(r).ID)
	if err != nil {
		return access, nil, err
	}
	if !access.IsOwner && !access.IsMember {
		return access, nil, fmt.Errorf("%w: you are not part of this project", annotation.ErrForbidden)
	}
	store, err := s.Projects.Store(access.Project.ID)
	if err != nil {
		return access, nil, err
	}
	return access, store, nil
}

// projectOwnerAccess is the stricter form used for classes, imports, exports,
// label edits, restoring, and purging.
func (s *Server) projectOwnerAccess(r *http.Request) (annotation.ProjectAccess, *annotation.Store, error) {
	access, store, err := s.projectAccess(r)
	if err != nil {
		return access, store, err
	}
	if !access.IsOwner {
		return access, store, fmt.Errorf("%w: only the project owner can do this", annotation.ErrForbidden)
	}
	return access, store, nil
}

func writeAnnotationErr(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, annotation.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, annotation.ErrInvalid):
		code = http.StatusBadRequest
	case errors.Is(err, annotation.ErrForbidden):
		code = http.StatusForbidden
	}
	msg := err.Error()
	msg = strings.TrimPrefix(msg, annotation.ErrInvalid.Error()+": ")
	msg = strings.TrimPrefix(msg, annotation.ErrNotFound.Error()+": ")
	msg = strings.TrimPrefix(msg, annotation.ErrForbidden.Error()+": ")
	writeError(w, code, msg)
}
