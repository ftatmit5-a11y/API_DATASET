package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func (s *Server) projectPage(w http.ResponseWriter, r *http.Request) {
	target := "/project.html"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusTemporaryRedirect)
}

func (s *Server) trashPage(w http.ResponseWriter, r *http.Request) {
	target := "/trash.html"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusTemporaryRedirect)
}

func (s *Server) usersList(w http.ResponseWriter, _ *http.Request) {
	users, err := s.Projects.Users()
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) trashList(w http.ResponseWriter, _ *http.Request) {
	groups, err := s.Projects.Trash()
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": groups})
}

func (s *Server) projectsList(w http.ResponseWriter, _ *http.Request) {
	projects, err := s.Projects.List()
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

func (s *Server) projectCreate(w http.ResponseWriter, r *http.Request) {
	user, err := s.requestUser(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	info, err := s.Projects.CreateForOwner(req.Name, user.ID)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) projectGet(w http.ResponseWriter, r *http.Request) {
	store, id, err := s.projectStore(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	info, err := s.Projects.Info(id)
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
	writeJSON(w, http.StatusOK, annotation.ProjectState{
		ProjectInfo: info,
		Classes:     classes,
		Images:      images,
		Trash:       trash,
	})
}

func (s *Server) projectRename(w http.ResponseWriter, r *http.Request) {
	user, err := s.requestUser(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	id := r.PathValue("id")
	if err := s.Projects.CheckOwner(id, user.ID); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	info, err := s.Projects.Rename(id, req.Name)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) projectDelete(w http.ResponseWriter, r *http.Request) {
	user, err := s.requestUser(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	id := r.PathValue("id")
	if err := s.Projects.CheckOwner(id, user.ID); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	if err := s.Projects.Delete(id); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) projectUploadImages(w http.ResponseWriter, r *http.Request) {
	store, _, err := s.projectStore(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	user, err := s.requestUser(r)
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
		name, err := store.AddImage(header.Filename, user.ID, file)
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
	store, _, err := s.projectStore(r)
	if err != nil {
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

func (s *Server) projectTrashImage(w http.ResponseWriter, r *http.Request) {
	store, id, err := s.projectStore(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	img, err := s.imageForMutation(r, store, id)
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

func (s *Server) projectRestoreImage(w http.ResponseWriter, r *http.Request) {
	store, id, err := s.projectStore(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	img, err := s.imageForMutation(r, store, id)
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
	store, id, err := s.projectStore(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	user, err := s.requestUser(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	// Purging is destructive and cannot be undone, so only the project owner
	// may do it. Uploaders can still restore their own images.
	if err := s.Projects.CheckOwner(id, user.ID); err != nil {
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

// imageForMutation loads an image and checks that the current user is allowed
// to change it (project owner or the user who uploaded it).
func (s *Server) imageForMutation(r *http.Request, store *annotation.Store, projectID string) (annotation.ImageInfo, error) {
	user, err := s.requestUser(r)
	if err != nil {
		return annotation.ImageInfo{}, err
	}
	img, err := store.Image(r.PathValue("name"))
	if err != nil {
		return annotation.ImageInfo{}, err
	}
	info, err := s.Projects.Info(projectID)
	if err != nil {
		return annotation.ImageInfo{}, err
	}
	if info.OwnerID != user.ID && img.UploadedBy != user.ID {
		return annotation.ImageInfo{}, fmt.Errorf("%w: only the project owner or uploader can change this image", annotation.ErrForbidden)
	}
	return img, nil
}

func (s *Server) projectGetLabels(w http.ResponseWriter, r *http.Request) {
	store, _, err := s.projectStore(r)
	if err != nil {
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

func (s *Server) projectPutLabels(w http.ResponseWriter, r *http.Request) {
	store, _, err := s.projectStore(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	var req struct {
		Labels []annotation.Label `json:"labels"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
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

func (s *Server) projectAddClass(w http.ResponseWriter, r *http.Request) {
	store, id, err := s.projectStore(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	user, err := s.requestUser(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	if err := s.Projects.CheckOwner(id, user.ID); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
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
	store, id, err := s.projectStore(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	user, err := s.requestUser(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	if err := s.Projects.CheckOwner(id, user.ID); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	var req struct {
		Classes []string `json:"classes"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
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

func (s *Server) projectExport(w http.ResponseWriter, r *http.Request) {
	store, id, err := s.projectStore(r)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
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

func (s *Server) projectStore(r *http.Request) (*annotation.Store, string, error) {
	id := r.PathValue("id")
	store, err := s.Projects.Store(id)
	return store, id, err
}

// requestUser resolves the simulated account selected by the web UI. Requests
// without the header fall back to the seeded default user so existing scripts
// keep working.
func (s *Server) requestUser(r *http.Request) (annotation.User, error) {
	id := strings.TrimSpace(r.Header.Get("X-User-ID"))
	return s.Projects.User(id)
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
