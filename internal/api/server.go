package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"yoloapi/internal/annotation"
	"yoloapi/internal/dataset"
	"yoloapi/web"
)

// Server serves the YOLO dataset split API.
type Server struct {
	DataDir   string
	MaxUpload int64
	Projects  *annotation.Manager
	mux       *http.ServeMux

	throttle    *loginThrottle
	sessionDone chan struct{}
	sessionOnce sync.Once
}

// New builds the HTTP routes.
func New(dataDir string, maxUpload int64) (*Server, error) {
	if maxUpload <= 0 {
		maxUpload = 512 << 20
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "jobs"), 0o755); err != nil {
		return nil, err
	}
	projects, err := annotation.NewManager(
		filepath.Join(dataDir, "annotations.db"),
		filepath.Join(dataDir, "projects"),
	)
	if err != nil {
		return nil, err
	}
	// Bring the old single-workspace annotations into a project on first run.
	if list, listErr := projects.List(); listErr == nil && len(list) == 0 {
		if _, imported, importErr := projects.ImportLegacy(filepath.Join(dataDir, "annotations"), "Default"); importErr != nil {
			log.Printf("could not import legacy annotations: %v", importErr)
		} else if imported {
			log.Printf("imported legacy annotations as project \"Default\"")
		}
	}
	s := &Server{
		DataDir:   dataDir,
		MaxUpload: maxUpload,
		Projects:  projects,
		mux:       http.NewServeMux(),
		throttle:  newLoginThrottle(),
	}
	s.routes()
	s.startSessionGC()
	return s, nil
}

// Handler is the root HTTP handler. The guard enforces login for the API while
// leaving pages, the endpoint index, and the login call public.
func (s *Server) Handler() http.Handler {
	return s.log(s.guard(s.mux))
}

// Close releases the SQLite database handle.
func (s *Server) Close() error {
	s.stopSessionGC()
	if s == nil || s.Projects == nil {
		return nil
	}
	return s.Projects.Close()
}

// protectedAPIPath reports whether a request path needs a signed-in session.
// Static pages are public shells: their data calls are protected, and the
// client redirects to the login page on 401.
func protectedAPIPath(path string) bool {
	if !strings.HasPrefix(path, "/api/") {
		return false
	}
	// The login call is the only way in, so it must stay reachable anonymous.
	return path != "/api/auth/login"
}

func (s *Server) routes() {
	// Public.
	s.mux.HandleFunc("GET /health", s.health)
	s.mux.HandleFunc("GET /api", s.index)
	s.mux.HandleFunc("POST /api/auth/login", s.login)
	s.mux.HandleFunc("GET /annotate", s.annotatePage)
	s.mux.HandleFunc("GET /projects", s.projectsPage)
	s.mux.HandleFunc("GET /project", s.projectPage)
	s.mux.HandleFunc("GET /trash", s.trashPage)
	s.mux.HandleFunc("GET /pending", s.pendingPage)
	s.mux.HandleFunc("GET /mywork", s.myWorkPage)
	s.mux.HandleFunc("GET /users", s.usersPage)
	s.mux.HandleFunc("GET /login", s.loginPage)
	s.mux.Handle("GET /", http.FileServer(http.FS(web.Files)))

	// Session-managed accounts.
	s.mux.HandleFunc("GET /api/auth/me", s.whoAmI)
	s.mux.HandleFunc("POST /api/auth/logout", s.logout)
	s.mux.HandleFunc("POST /api/auth/password", s.changePassword)
	s.mux.HandleFunc("GET /api/users", s.usersList)
	s.mux.HandleFunc("POST /api/users", s.userCreate)
	s.mux.HandleFunc("PATCH /api/users/{id}", s.userUpdate)

	// Dataset splitting: teacher-only, because it writes jobs to the server.
	s.mux.HandleFunc("POST /api/datasets", s.teacherOnly(s.upload))
	s.mux.HandleFunc("GET /api/datasets", s.teacherOnly(s.list))
	s.mux.HandleFunc("GET /api/datasets/{id}", s.teacherOnly(s.get))
	s.mux.HandleFunc("GET /api/datasets/{id}/download", s.teacherOnly(s.download))
	s.mux.HandleFunc("DELETE /api/datasets/{id}", s.teacherOnly(s.remove))

	// Projects.
	s.mux.HandleFunc("GET /api/projects", s.projectsList)
	s.mux.HandleFunc("POST /api/projects", s.projectCreate)
	s.mux.HandleFunc("GET /api/projects/{id}", s.projectGet)
	s.mux.HandleFunc("PATCH /api/projects/{id}", s.projectRename)
	s.mux.HandleFunc("DELETE /api/projects/{id}", s.projectDelete)
	s.mux.HandleFunc("PUT /api/projects/{id}/submissions-open", s.projectSubmissionsSwitch)
	s.mux.HandleFunc("GET /api/projects/{id}/members", s.projectMembersList)
	s.mux.HandleFunc("POST /api/projects/{id}/members", s.projectMembersAdd)
	s.mux.HandleFunc("DELETE /api/projects/{id}/members/{userId}", s.projectMemberRemove)

	// Images, labels, classes, export.
	s.mux.HandleFunc("POST /api/projects/{id}/images", s.projectUploadImages)
	s.mux.HandleFunc("GET /api/projects/{id}/images/{name}", s.projectServeImage)
	s.mux.HandleFunc("DELETE /api/projects/{id}/images/{name}", s.projectTrashImage)
	s.mux.HandleFunc("POST /api/projects/{id}/images/{name}/restore", s.projectRestoreImage)
	s.mux.HandleFunc("DELETE /api/projects/{id}/images/{name}/permanent", s.projectPurgeImage)
	s.mux.HandleFunc("GET /api/projects/{id}/images/{name}/labels", s.projectGetLabels)
	s.mux.HandleFunc("PUT /api/projects/{id}/images/{name}/labels", s.projectPutLabels)
	s.mux.HandleFunc("POST /api/projects/{id}/classes", s.projectAddClass)
	s.mux.HandleFunc("PUT /api/projects/{id}/classes", s.projectPutClasses)
	s.mux.HandleFunc("GET /api/projects/{id}/export", s.projectExport)

	// Trash and submissions.
	s.mux.HandleFunc("GET /api/trash", s.trashList)
	s.mux.HandleFunc("GET /api/pending", s.pendingList)
	s.mux.HandleFunc("GET /api/mywork", s.myWorkList)
	s.mux.HandleFunc("GET /api/projects/{id}/submissions", s.submissionList)
	s.mux.HandleFunc("POST /api/projects/{id}/submissions", s.submissionCreate)
	s.mux.HandleFunc("GET /api/projects/{id}/submissions/{sid}", s.submissionGet)
	s.mux.HandleFunc("GET /api/projects/{id}/submissions/{sid}/image", s.submissionImage)
	s.mux.HandleFunc("PUT /api/projects/{id}/submissions/{sid}/labels", s.submissionPutLabels)
	s.mux.HandleFunc("POST /api/projects/{id}/submissions/{sid}/submit", s.submissionSubmit)
	s.mux.HandleFunc("POST /api/projects/{id}/submissions/{sid}/withdraw", s.submissionWithdraw)
	s.mux.HandleFunc("DELETE /api/projects/{id}/submissions/{sid}", s.submissionDelete)
	s.mux.HandleFunc("POST /api/projects/{id}/submissions/{sid}/approve", s.submissionApprove)
	s.mux.HandleFunc("POST /api/projects/{id}/submissions/{sid}/reject", s.submissionReject)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {

	writeJSON(w, http.StatusOK, map[string]any{
		"service": "yolo-dataset-api",
		"auth":    "all endpoints below /api/ except login need a session cookie; writes also need an X-Requested-With header",
		"endpoints": []string{
			"GET /health",
			"POST /api/auth/login",
			"POST /api/auth/logout",
			"GET /api/auth/me",
			"POST /api/auth/password",
			"GET /api/users",
			"POST /api/users",
			"PATCH /api/users/{id}",
			"POST /api/datasets",
			"GET /api/datasets",
			"GET /api/datasets/{id}",
			"GET /api/datasets/{id}/download",
			"DELETE /api/datasets/{id}",
			"GET /api/projects",
			"POST /api/projects",
			"GET /api/trash",
			"GET /api/pending",
			"GET /api/mywork",
			"GET /api/projects/{id}",
			"PATCH /api/projects/{id}",
			"DELETE /api/projects/{id}",
			"PUT /api/projects/{id}/submissions-open",
			"GET /api/projects/{id}/members",
			"POST /api/projects/{id}/members",
			"DELETE /api/projects/{id}/members/{userId}",
			"POST /api/projects/{id}/images",
			"GET /api/projects/{id}/images/{name}",
			"DELETE /api/projects/{id}/images/{name}",
			"POST /api/projects/{id}/images/{name}/restore",
			"DELETE /api/projects/{id}/images/{name}/permanent",
			"GET /api/projects/{id}/images/{name}/labels",
			"PUT /api/projects/{id}/images/{name}/labels",
			"POST /api/projects/{id}/classes",
			"PUT /api/projects/{id}/classes",
			"GET /api/projects/{id}/export",
			"GET /api/projects/{id}/submissions",
			"POST /api/projects/{id}/submissions",
			"GET /api/projects/{id}/submissions/{sid}",
			"GET /api/projects/{id}/submissions/{sid}/image",
			"PUT /api/projects/{id}/submissions/{sid}/labels",
			"POST /api/projects/{id}/submissions/{sid}/submit",
			"POST /api/projects/{id}/submissions/{sid}/withdraw",
			"DELETE /api/projects/{id}/submissions/{sid}",
			"POST /api/projects/{id}/submissions/{sid}/approve",
			"POST /api/projects/{id}/submissions/{sid}/reject",
		},
	})
}

type job struct {
	ID          string          `json:"id"`
	Filename    string          `json:"filename"`
	CreatedAt   time.Time       `json:"created_at"`
	Summary     dataset.Summary `json:"summary"`
	DownloadURL string          `json:"download_url"`
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.MaxUpload)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "upload exceeds size limit")
			return
		}
		writeError(w, http.StatusBadRequest, "expected multipart form with a zip file")
		return
	}

	opt, err := parseOptions(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "form field \"file\" (the YOLO zip) is required")
		return
	}
	defer file.Close()

	name := header.Filename
	if name == "" {
		name = "dataset.zip"
	}
	if !strings.HasSuffix(strings.ToLower(name), ".zip") {
		writeError(w, http.StatusBadRequest, "file must be a .zip archive")
		return
	}

	id, err := newID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create job id")
		return
	}
	dir := s.jobDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create job directory")
		return
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(dir)
		}
	}()

	inPath := filepath.Join(dir, "input.zip")
	if err := saveUpload(inPath, file); err != nil {
		writeError(w, http.StatusInternalServerError, "could not store upload")
		return
	}
	outPath := filepath.Join(dir, "dataset.zip")
	summary, err := dataset.BuildZip(inPath, outPath, opt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	os.Remove(inPath)

	j := job{
		ID:          id,
		Filename:    filepath.Base(name),
		CreatedAt:   time.Now().UTC(),
		Summary:     summary,
		DownloadURL: "/api/datasets/" + id + "/download",
	}
	if err := writeJob(dir, j); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save job metadata")
		return
	}
	cleanup = false
	writeJSON(w, http.StatusCreated, j)
}

func (s *Server) list(w http.ResponseWriter, _ *http.Request) {
	entries, err := os.ReadDir(filepath.Join(s.DataDir, "jobs"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list jobs")
		return
	}
	jobs := make([]job, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		j, err := readJob(s.jobDir(e.Name()))
		if err != nil {
			continue
		}
		jobs = append(jobs, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"datasets": jobs})
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	j, err := s.load(r.PathValue("id"))
	if err != nil {
		writeJobErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.load(id); err != nil {
		writeJobErr(w, err)
		return
	}
	path := filepath.Join(s.jobDir(id), "dataset.zip")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "dataset-"+id+".zip"))
	http.ServeFile(w, r, path)
}

func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.load(id); err != nil {
		writeJobErr(w, err)
		return
	}
	if err := os.RemoveAll(s.jobDir(id)); err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete dataset")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) load(id string) (job, error) {
	if !validID(id) {
		return job{}, errNotFound
	}
	return readJob(s.jobDir(id))
}

func (s *Server) jobDir(id string) string {
	return filepath.Join(s.DataDir, "jobs", id)
}

var errNotFound = errors.New("not found")

func writeJobErr(w http.ResponseWriter, err error) {
	if errors.Is(err, errNotFound) || os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "dataset not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "could not read dataset")
}

func parseOptions(r *http.Request) (dataset.Options, error) {
	trainStr := strings.TrimSpace(r.FormValue("train_ratio"))
	valStr := strings.TrimSpace(r.FormValue("val_ratio"))
	testStr := strings.TrimSpace(r.FormValue("test_ratio"))
	train, val, test := dataset.DefaultRatios()
	if trainStr != "" || valStr != "" || testStr != "" {
		if trainStr == "" || valStr == "" || testStr == "" {
			return dataset.Options{}, fmt.Errorf("set train_ratio, val_ratio, and test_ratio together")
		}
		var err error
		train, err = parseRatio("train_ratio", trainStr)
		if err != nil {
			return dataset.Options{}, err
		}
		val, err = parseRatio("val_ratio", valStr)
		if err != nil {
			return dataset.Options{}, err
		}
		test, err = parseRatio("test_ratio", testStr)
		if err != nil {
			return dataset.Options{}, err
		}
	}
	if err := dataset.ValidateRatios(train, val, test); err != nil {
		return dataset.Options{}, err
	}

	seed := time.Now().UnixNano()
	if raw := strings.TrimSpace(r.FormValue("seed")); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return dataset.Options{}, fmt.Errorf("seed must be an integer")
		}
		seed = n
	}

	var names []string
	if raw := strings.TrimSpace(r.FormValue("names")); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				names = append(names, part)
			}
		}
		if len(names) == 0 {
			return dataset.Options{}, fmt.Errorf("names must be a comma-separated class list")
		}
	}
	return dataset.Options{
		TrainRatio: train,
		ValRatio:   val,
		TestRatio:  test,
		Names:      names,
		Seed:       seed,
	}, nil
}

func parseRatio(field, raw string) (float64, error) {
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number", field)
	}
	return n, nil
}

func saveUpload(path string, r io.Reader) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return err
	}
	return f.Close()
}

func writeJob(dir string, j job) error {
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "meta.json"), data, 0o644)
}

func readJob(dir string) (job, error) {
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return job{}, err
	}
	var j job
	if err := json.Unmarshal(data, &j); err != nil {
		return job{}, err
	}
	return j, nil
}

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func validID(id string) bool {
	if len(id) != 16 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) log(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
