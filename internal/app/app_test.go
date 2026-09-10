package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("APP_URL", "http://localhost:8090")
	t.Setenv("ADMIN_PASSWORD", "test-password-strong-123")
	t.Setenv("OIDC_ISSUER", "")
	a, e := New()
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func request(a *App, method, target, body, origin, cookie string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: "vloader_session", Value: cookie})
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func TestAuthenticationAndCSRF(t *testing.T) {
	a := testApp(t)
	for _, route := range []string{"/api/catalog", "/api/settings", "/api/download/1", "/api/images/1/Primary"} {
		if w := request(a, "GET", route, "", "", ""); w.Code != 401 {
			t.Fatalf("%s: %d", route, w.Code)
		}
	}
	body := `{"Username":"admin","Password":"test-password-strong-123"}`
	if w := request(a, "POST", "/auth/login", body, "https://evil.invalid", ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
	w := request(a, "POST", "/auth/login", body, a.origin, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("cookie flags")
	}
	key := cookies[0].Value
	if w := request(a, "GET", "/api/catalog", "", "", key); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(a, "POST", "/auth/logout", "{}", a.origin, key); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(a, "GET", "/api/catalog", "", "", key); w.Code != 401 {
		t.Fatal("session survived logout")
	}
}
func TestLoginDenialAndExpiry(t *testing.T) {
	a := testApp(t)
	w := request(a, "POST", "/auth/login", `{"Username":"admin","Password":"wrong"}`, a.origin, "")
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	w = request(a, "POST", "/auth/login", `{"Username":"admin","Password":"wrong"}`, a.origin, "")
	if w.Code != 429 {
		t.Fatal(w.Code)
	}
	a.sessions["expired"] = session{"admin", time.Now().Add(-time.Second)}
	if w := request(a, "GET", "/api/catalog", "", "", "expired"); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
func TestRelativeSource(t *testing.T) {
	for _, tc := range []struct {
		p  string
		ok bool
	}{{"/source/Film/movie.mkv", true}, {"/source-other/secret", false}, {"/source/../secret", false}, {"/source", false}, {"/etc/passwd", false}} {
		_, e := relativeSource("/source", tc.p)
		if (e == nil) != tc.ok {
			t.Errorf("%s: %v", tc.p, e)
		}
	}
}
func TestMountDownloadAndSymlinkEscape(t *testing.T) {
	a := testApp(t)
	root := t.TempDir()
	t.Setenv("MEDIA_ROOT", root)
	if e := os.WriteFile(filepath.Join(root, "movie.mkv"), []byte("movie-data"), 0600); e != nil {
		t.Fatal(e)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret"), 0600)
	os.Symlink(outside, filepath.Join(root, "escape.mkv"))
	a.cfg.SourceMode = "mount"
	a.cfg.SourcePrefix = "/source"
	a.catalog = Catalog{SourceURL: a.cfg.EmbyURL, Items: []Item{{ID: "1", Path: "/source/movie.mkv"}, {ID: "2", Path: "/source/escape.mkv"}, {ID: "3", Path: "/etc/passwd"}}}
	a.sessions["key"] = session{"admin", time.Now().Add(time.Hour)}
	w := request(a, "GET", "/api/download/1", "", "", "key")
	if w.Code != 200 || w.Body.String() != "movie-data" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, id := range []string{"2", "3"} {
		w = request(a, "GET", "/api/download/"+id, "", "", "key")
		if w.Code == 200 || strings.Contains(w.Body.String(), "secret") {
			t.Fatal("escaped root")
		}
	}
	r := httptest.NewRequest("GET", "/api/download/1", nil)
	r.Header.Set("Range", "bytes=0-4")
	r.AddCookie(&http.Cookie{Name: "vloader_session", Value: "key"})
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "movie" {
		t.Fatal("range download", w.Code)
	}
}
func TestEmbySyncPaginationAtomicityAndDownloads(t *testing.T) {
	a := testApp(t)
	broken := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Emby-Token") != "test-key" {
			t.Error("missing API auth")
		}
		switch r.URL.Path {
		case "/Library/MediaFolders":
			jsonOut(w, map[string]any{"Items": []Item{{ID: "lib", Name: "Movies"}}})
		case "/Items":
			if broken {
				w.WriteHeader(503)
				return
			}
			id := "1"
			if r.URL.Query().Get("StartIndex") == "1" {
				id = "2"
			}
			jsonOut(w, map[string]any{"Items": []Item{{ID: id, Name: "Movie " + id, Path: "/source/a.mkv", Overview: "Full overview", ParentID: "original-parent", Genres: []string{"Drama"}}}, "TotalRecordCount": 2})
		case "/Items/1/Download":
			io.WriteString(w, "movie")
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	a.cfg = Config{EmbyURL: server.URL, APIKey: "test-key", SourceMode: "emby"}
	a.sessions["key"] = session{"admin", time.Now().Add(time.Hour)}
	w := request(a, "POST", "/api/sync", "{}", a.origin, "key")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(a.catalog.Items) != 2 || a.catalog.Items[0].Overview != "Full overview" || a.catalog.Items[0].ParentID != "original-parent" || a.catalog.Items[0].LibraryID != "lib" {
		t.Fatal(a.catalog)
	}
	b, e := os.ReadFile(a.data + "/catalog.json")
	if e != nil || !json.Valid(b) {
		t.Fatal("catalog not persisted")
	}
	w = request(a, "GET", "/api/download/1", "", "", "key")
	if w.Code != 200 || w.Body.String() != "movie" {
		t.Fatal(w.Code)
	}
	broken = true
	w = request(a, "POST", "/api/sync", "{}", a.origin, "key")
	if w.Code != 502 || len(a.catalog.Items) != 2 {
		t.Fatal("failed sync changed catalog")
	}
}
func TestRedirectDoesNotLeakToken(t *testing.T) {
	a := testApp(t)
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer source.Close()
	_, e := a.emby(context.Background(), Config{EmbyURL: source.URL, APIKey: "secret"}, "/Items")
	if e == nil || called {
		t.Fatal("redirect followed")
	}
}
func TestSettingsRedactionAndServerChange(t *testing.T) {
	a := testApp(t)
	a.cfg.APIKey = "top-secret"
	a.cfg.EmbyURL = "http://old.invalid"
	a.catalog = Catalog{SourceURL: a.cfg.EmbyURL, Items: []Item{{ID: "old"}}}
	a.sessions["key"] = session{"admin", time.Now().Add(time.Hour)}
	w := request(a, "GET", "/api/settings", "", "", "key")
	if strings.Contains(w.Body.String(), "top-secret") {
		t.Fatal("secret disclosed")
	}
	w = request(a, "POST", "/api/settings", `{"EmbyURL":"http://new.invalid","SourceMode":"emby","SourcePrefix":"/source"}`, a.origin, "key")
	if w.Code != 400 {
		t.Fatal("server change reused secret")
	}
	w = request(a, "POST", "/api/settings", `{"EmbyURL":"http://new.invalid","APIKey":"new-key","SourceMode":"emby","SourcePrefix":"/source"}`, a.origin, "key")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, ok := a.item("old"); ok {
		t.Fatal("old catalog accessible on new server")
	}
	w = request(a, "GET", "/api/catalog", "", "", "key")
	if strings.Contains(w.Body.String(), "old") {
		t.Fatal("stale catalog exposed")
	}
}
func TestOIDCDisabledAndInvalidState(t *testing.T) {
	a := testApp(t)
	for _, p := range []string{"/auth/oidc", "/auth/callback?state=invalid"} {
		if w := request(a, "GET", p, "", "", ""); w.Code != 404 {
			t.Fatal(w.Code)
		}
	}
}

func TestOriginalMetadataSurvivesSnapshot(t *testing.T) {
	var i Item
	original := `{"Id":"a","Name":"Title","ProviderIds":{"Imdb":"tt123"},"People":[{"Name":"Director"}],"CustomField":"preserve-me"}`
	if err := json.Unmarshal([]byte(original), &i); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(i)
	if err != nil {
		t.Fatal(err)
	}
	var again Item
	if err = json.Unmarshal(b, &again); err != nil {
		t.Fatal(err)
	}
	if string(again.Metadata) != string(i.Metadata) || !strings.Contains(string(again.Metadata), "preserve-me") {
		t.Fatal("metadata lost or recursively nested")
	}
}
