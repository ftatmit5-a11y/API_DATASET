package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Seeded logins from internal/annotation/db.go. Tests sign in instead of
// spoofing headers, which is the point of having real accounts.
const (
	teacherUser = "somchai"
	teacherPass = "teacher1234"
	studentUser = "somying"
	studentPass = "student1234"
)

// client is a cookie-aware HTTP client bound to one test server, so every call
// carries the session of exactly one account.
type client struct {
	t     *testing.T
	base  string
	http  *http.Client
	who   string
	token string
	id    string
	role  string
}

type testServer struct {
	t   *testing.T
	srv *Server
	ts  *httptest.Server
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	srv, err := New(t.TempDir(), 8<<20)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		// Give SQLite a moment to release the WAL file so TempDir cleanup on
		// Windows does not fail with a sharing violation.
		time.Sleep(50 * time.Millisecond)
		if err := srv.Close(); err != nil {
			t.Logf("close server: %v", err)
		}
	})
	return &testServer{t: t, srv: srv, ts: ts}
}

// login signs in and returns a client that keeps the session cookie.
func (s *testServer) login(username, password string) *client {
	s.t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		s.t.Fatalf("cookie jar: %v", err)
	}
	c := &client{
		t:     s.t,
		base:  s.ts.URL,
		http:  &http.Client{Jar: jar},
		who:   username,
		token: "test-" + username,
	}

	res := c.do(http.MethodPost, "/api/auth/login", strings.NewReader(
		`{"username":"`+username+`","password":"`+password+`"}`), "application/json")
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		s.t.Fatalf("login %s: status %d body %s", username, res.StatusCode, body)
	}
	var out struct {
		User struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"user"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		s.t.Fatalf("login decode: %v", err)
	}
	c.id = out.User.ID
	c.role = out.User.Role
	return c
}

// teacher and student return the two seeded accounts.
func (s *testServer) teacher() *client { return s.login(teacherUser, teacherPass) }
func (s *testServer) student() *client { return s.login(studentUser, studentPass) }

// do sends a request with the CSRF header that writes require.
func (c *client) do(method, path string, body io.Reader, contentType string) *http.Response {
	c.t.Helper()
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// Fetch in the browser always sets this; a write without it is a forgery.
	req.Header.Set("X-Requested-With", c.token)
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	return res
}

// get fetches a path and fails the test on a non-2xx status.
func (c *client) get(path string) *http.Response {
	c.t.Helper()
	res := c.do(http.MethodGet, path, nil, "")
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		c.t.Fatalf("GET %s: status %d body %s", path, res.StatusCode, body)
	}
	return res
}

// json sends a JSON body and returns the raw response.
func (c *client) json(method, path, payload string) *http.Response {
	c.t.Helper()
	return c.do(method, path, strings.NewReader(payload), "application/json")
}

// statusOf runs a request and returns only the status code, draining the body.
func (c *client) statusOf(method, path, payload string) int {
	c.t.Helper()
	var body io.Reader
	if payload != "" {
		body = strings.NewReader(payload)
	}
	res := c.do(method, path, body, contentTypeFor(method, payload))
	defer res.Body.Close()
	io.Copy(io.Discard, res.Body)
	return res.StatusCode
}

func contentTypeFor(method, payload string) string {
	if payload == "" {
		return ""
	}
	return "application/json"
}

// mustJSON decodes a successful JSON response.
func (c *client) mustJSON(path string, dst any) {
	c.t.Helper()
	res := c.get(path)
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(dst); err != nil {
		c.t.Fatalf("decode %s: %v", path, err)
	}
}

// raw returns the body of a successful GET, for zip downloads.
func (c *client) raw(path string) []byte {
	c.t.Helper()
	res := c.get(path)
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		c.t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// expect fails unless the call returns the wanted status.
func (c *client) expect(method, path, payload string, want int) {
	c.t.Helper()
	got := c.statusOf(method, path, payload)
	if got != want {
		c.t.Fatalf("%s %s: status %d want %d", method, path, got, want)
	}
}

// anonymous returns a client with no session cookie at all.
func (s *testServer) anonymous() *client {
	s.t.Helper()
	return &client{
		t:     s.t,
		base:  s.ts.URL,
		http:  &http.Client{},
		who:   "<anonymous>",
		token: "test-anon",
	}
}

// noCSRF returns a client that has a session but omits the write header,
// mimicking a cross-site form post.
func (s *testServer) noCSRF(username, password string) *client {
	c := s.login(username, password)
	c.token = ""
	return c
}

// doWithoutCSRF sends a request without the X-Requested-With header.
func (c *client) doWithoutCSRF(method, path, payload string) *http.Response {
	c.t.Helper()
	var body io.Reader
	if payload != "" {
		body = strings.NewReader(payload)
	}
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	if payload != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	io.Copy(io.Discard, res.Body)
	return res
}
