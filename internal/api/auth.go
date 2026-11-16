package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"yoloapi/internal/annotation"
	"yoloapi/internal/auth"
)

// sessionCookie holds the login token. The database stores only its hash, so a
// leaked database does not hand out working cookies.
const (
	sessionCookie    = "yolo_session"
	csrfHeader       = "X-Requested-With"
	maxLoginBodySize = 4 << 10
	sessionGCEvery   = 6 * time.Hour
)

type userContextKey struct{}

// currentUser returns the signed-in account, or the zero value when the request
// is anonymous.
func currentUser(r *http.Request) annotation.User {
	u, _ := r.Context().Value(userContextKey{}).(annotation.User)
	return u
}

// secureRequest detects HTTPS directly or behind a reverse proxy so the session
// cookie only travels over encrypted links in that case.
func secureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   secureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func sessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	return strings.TrimSpace(c.Value)
}

// guard enforces the session on every protected API path and stores the
// account on the request context. Handlers below it can assume a signed-in user.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !protectedAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		user, err := s.Projects.SessionUser(sessionToken(r))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "please sign in")
			return
		}
		// SameSite=Lax blocks cross-site form posts; requiring this custom
		// header on writes also blocks the fetches a hostile page could force,
		// because sending it needs a CORS preflight the page cannot pass.
		if !safeMethod(r.Method) && r.Header.Get(csrfHeader) == "" {
			writeError(w, http.StatusForbidden, "missing "+csrfHeader+" header")
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey{}, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func safeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

// decodeJSON reads a bounded JSON body.
func decodeJSON(r *http.Request, dst any) error {
	body := io.LimitReader(r.Body, 1<<20)
	return json.NewDecoder(body).Decode(dst)
}

// loginThrottle slows repeated failed sign-ins per account and address.
type loginThrottle struct {
	mu       sync.Mutex
	failures map[string]throttleState
}

type throttleState struct {
	count  int
	until  time.Time
	lastAt time.Time
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{failures: map[string]throttleState{}}
}

// delay reports how long this key must wait before its next attempt.
func (t *loginThrottle) delay(key string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.failures[key]
	if !state.until.IsZero() && time.Now().Before(state.until) {
		return time.Until(state.until)
	}
	return 0
}

func (t *loginThrottle) failed(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.failures[key]
	if !state.lastAt.IsZero() && time.Since(state.lastAt) > 15*time.Minute {
		state.count = 0
	}
	state.count++
	state.lastAt = time.Now()
	wait := time.Duration(state.count-1) * 2 * time.Second
	if wait > 30*time.Second {
		wait = 30 * time.Second
	}
	state.until = time.Now().Add(wait)
	t.failures[key] = state
}

func (t *loginThrottle) succeeded(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.failures, key)
}

// throttleKey is deliberately coarse: username plus the client's first address
// component, so a classroom behind one NAT is not locked out for long.
func throttleKey(r *http.Request, username string) string {
	host := r.RemoteAddr
	if comma := strings.IndexAny(host, ","); comma > 0 {
		host = host[:comma]
	}
	return strings.ToLower(strings.TrimSpace(username)) + "|" + host
}

// login starts a session. Failures always answer the same generic message so
// the endpoint cannot be used to enumerate accounts.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBodySize)
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	username := auth.NormalizeUsername(req.Username)
	key := throttleKey(r, username)
	if wait := s.throttle.delay(key); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "too many sign-in attempts, try again shortly")
		return
	}

	user, err := s.Projects.Authenticate(username, req.Password)
	if err != nil {
		if errors.Is(err, annotation.ErrBadCredentials) {
			s.throttle.failed(key)
			writeError(w, http.StatusUnauthorized, "invalid username or password")
			return
		}
		writeAnnotationErr(w, err)
		return
	}
	s.throttle.succeeded(key)

	token, expires, err := s.Projects.StartSession(user.ID, annotation.SessionTTL)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	s.setSessionCookie(w, r, token, expires)
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

// logout drops the session row and the cookie.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.Projects.EndSession(sessionToken(r)); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	s.clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"logged_out": "ok"})
}

// whoAmI lets the web UI decide whether to render a page or redirect to login.
func (s *Server) whoAmI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": currentUser(r)})
}

// changePassword is self-service: any signed-in account may rotate its own
// password after proving the old one. Sessions of that user are dropped so the
// change takes effect everywhere.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	self := currentUser(r)
	if _, err := s.Projects.Authenticate(self.Username, req.Current); err != nil {
		writeError(w, http.StatusUnauthorized, "current password is wrong")
		return
	}
	updated, err := s.Projects.UpdateUser(self.ID, nil, nil, &req.Password)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	s.clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"user": updated, "reauth_required": true})
}

// usersList is teacher-only: students must not enumerate the whole roster.
func (s *Server) usersList(w http.ResponseWriter, r *http.Request) {
	if err := s.requireTeacher(r); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	users, err := s.Projects.Users()
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

// userCreate lets teachers add accounts (usually students) with an initial
// password the new account should change from its own page.
func (s *Server) userCreate(w http.ResponseWriter, r *http.Request) {
	if err := s.requireTeacher(r); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	var req struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		Password    string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	user, err := s.Projects.NewUser(req.Username, req.DisplayName, req.Role, req.Password)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

// userUpdate lets teachers rename an account, change its role, or reset its
// password. The id comes from the path, never from the body.
func (s *Server) userUpdate(w http.ResponseWriter, r *http.Request) {
	if err := s.requireTeacher(r); err != nil {
		writeAnnotationErr(w, err)
		return
	}
	var req struct {
		DisplayName *string `json:"display_name"`
		Role        *string `json:"role"`
		Password    *string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	user, err := s.Projects.UpdateUser(r.PathValue("id"), req.DisplayName, req.Role, req.Password)
	if err != nil {
		writeAnnotationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) requireTeacher(r *http.Request) error {
	return s.Projects.CheckTeacher(currentUser(r).ID)
}

// teacherOnly wraps a handler so only teachers may reach it. Dataset building
// writes to the server disk, so it is not a student action.
func (s *Server) teacherOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.requireTeacher(r); err != nil {
			writeAnnotationErr(w, err)
			return
		}
		next(w, r)
	}
}

// pruneSessions removes expired rows so the sessions table cannot grow forever.
func (s *Server) pruneSessions() {
	n, err := s.Projects.DeleteExpiredSessions()
	if err != nil {
		// A busy database is not worth failing requests over: expiry is still
		// checked for every request.
		log.Printf("session cleanup: %v", err)
		return
	}
	if n > 0 {
		log.Printf("session cleanup: dropped %d expired sessions", n)
	}
}

// startSessionGC prunes at startup and then hourly until the server closes.
func (s *Server) startSessionGC() {
	s.sessionOnce.Do(func() {
		s.sessionDone = make(chan struct{})
		s.pruneSessions()
		go func() {
			ticker := time.NewTicker(sessionGCEvery)
			defer ticker.Stop()
			for {
				select {
				case <-s.sessionDone:
					return
				case <-ticker.C:
					s.pruneSessions()
				}
			}
		}()
	})
}

func (s *Server) stopSessionGC() {
	if s.sessionDone != nil {
		select {
		case <-s.sessionDone:
			// already closed
		default:
			close(s.sessionDone)
		}
		s.sessionDone = nil
	}
}
