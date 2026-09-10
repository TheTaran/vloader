package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
)

//go:embed web/*
var assets embed.FS

type Config struct {
	EmbyURL      string
	APIKey       string
	SourcePrefix string
	SourceMode   string
}
type Item struct {
	Metadata          json.RawMessage `json:"Metadata,omitempty"`
	ID                string          `json:"Id"`
	Name              string
	Type              string
	Path              string
	Overview          string
	ProductionYear    int
	CommunityRating   float64
	RunTimeTicks      int64
	Genres            []string
	ImageTags         map[string]string
	BackdropImageTags []string
	ParentID          string `json:"ParentId"`
	LibraryID         string `json:"LibraryId"`
	IsFolder          bool
	MediaSources      []struct {
		Path      string
		Container string
		Size      int64
	}
}

// UnmarshalJSON retains all original Emby fields, including provider IDs and media details.
func (i *Item) UnmarshalJSON(data []byte) error {
	type plain Item
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if len(v.Metadata) == 0 {
		v.Metadata = append(json.RawMessage(nil), data...)
	}
	*i = Item(v)
	return nil
}

type Catalog struct {
	SourceURL string
	Libraries []Item
	Items     []Item
	Updated   time.Time
}
type session struct {
	User   string
	Expiry time.Time
}
type flow struct {
	Nonce, Verifier string
	Expiry          time.Time
}
type App struct {
	mu       sync.RWMutex
	syncMu   sync.Mutex
	cfg      Config
	catalog  Catalog
	sessions map[string]session
	flows    map[string]flow
	attempts map[string]time.Time
	password []byte
	origin   string
	data     string
	client   *http.Client
	oauth    *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

func random() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func env(k, d string) string {
	if s := os.Getenv(k); s != "" {
		return s
	}
	return d
}
func Run() {
	a, e := New()
	if e != nil {
		log.Fatal(e)
	}
	s := &http.Server{Addr: ":8080", Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	log.Print("vloader listening on :8080")
	log.Fatal(s.ListenAndServe())
}
func New() (*App, error) {
	a := &App{data: env("DATA_DIR", "/data"), origin: env("APP_URL", "http://localhost:8090"), sessions: map[string]session{}, flows: map[string]flow{}, attempts: map[string]time.Time{}, client: &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second, MaxIdleConns: 20}, CheckRedirect: func(r *http.Request, v []*http.Request) error { return http.ErrUseLastResponse }}}
	u, e := url.Parse(a.origin)
	if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.Path != "" {
		return nil, errors.New("APP_URL must be an origin without trailing slash")
	}
	if e = os.MkdirAll(a.data, 0700); e != nil {
		return nil, e
	}
	a.cfg = Config{EmbyURL: os.Getenv("EMBY_URL"), APIKey: os.Getenv("EMBY_API_KEY"), SourcePrefix: env("SOURCE_PREFIX", "/media"), SourceMode: env("SOURCE_MODE", "emby")}
	if b, e := os.ReadFile(a.data + "/settings.json"); e == nil {
		if e = json.Unmarshal(b, &a.cfg); e != nil {
			return nil, e
		}
	}
	if b, e := os.ReadFile(a.data + "/catalog.json"); e == nil {
		if e = json.Unmarshal(b, &a.catalog); e != nil {
			return nil, e
		}
	}
	p := os.Getenv("ADMIN_PASSWORD")
	if len(p) < 16 {
		return nil, errors.New("ADMIN_PASSWORD must contain at least 16 characters")
	}
	a.password, e = bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	if e != nil {
		return nil, e
	}
	if issuer := os.Getenv("OIDC_ISSUER"); issuer != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		provider, e := oidc.NewProvider(ctx, issuer)
		if e != nil {
			return nil, errors.New("OIDC discovery failed")
		}
		if os.Getenv("OIDC_CLIENT_ID") == "" || os.Getenv("OIDC_ALLOWED_SUBJECTS") == "" {
			return nil, errors.New("OIDC_CLIENT_ID and OIDC_ALLOWED_SUBJECTS required")
		}
		a.oauth = &oauth2.Config{ClientID: os.Getenv("OIDC_CLIENT_ID"), ClientSecret: os.Getenv("OIDC_CLIENT_SECRET"), Endpoint: provider.Endpoint(), RedirectURL: a.origin + "/auth/callback", Scopes: []string{oidc.ScopeOpenID, "profile"}}
		a.verifier = provider.Verifier(&oidc.Config{ClientID: a.oauth.ClientID})
	}
	return a, nil
}
func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	jsonOut(w, map[string]string{"error": msg})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		fail(w, 400, "Ungültige Eingabe")
		return false
	}
	return true
}
func (a *App) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { jsonOut(w, map[string]string{"status": "ok"}) })
	m.HandleFunc("GET /api/session", a.me)
	m.HandleFunc("POST /auth/login", a.login)
	m.HandleFunc("POST /auth/logout", a.logout)
	m.HandleFunc("GET /auth/oidc", a.oidcStart)
	m.HandleFunc("GET /auth/callback", a.oidcCallback)
	m.Handle("GET /api/catalog", a.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.RLock()
		defer a.mu.RUnlock()
		if a.catalog.SourceURL != a.cfg.EmbyURL {
			jsonOut(w, Catalog{})
			return
		}
		jsonOut(w, a.catalog)
	})))
	m.Handle("GET /api/settings", a.guard(http.HandlerFunc(a.settings)))
	m.Handle("POST /api/settings", a.guard(http.HandlerFunc(a.saveSettings)))
	m.Handle("POST /api/sync", a.guard(http.HandlerFunc(a.syncCatalog)))
	m.Handle("GET /api/images/{id}/{kind}", a.guard(http.HandlerFunc(a.image)))
	m.Handle("GET /api/download/{id}", a.guard(http.HandlerFunc(a.download)))
	sub, _ := fs.Sub(assets, "web")
	m.Handle("/", http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self'; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("Origin") != a.origin {
			fail(w, 403, "Ungültiger Ursprung")
			return
		}
		m.ServeHTTP(w, r)
	})
}
func (a *App) user(r *http.Request) string {
	c, e := r.Cookie("vloader_session")
	if e != nil {
		return ""
	}
	a.mu.RLock()
	s := a.sessions[c.Value]
	a.mu.RUnlock()
	if time.Now().After(s.Expiry) {
		return ""
	}
	return s.User
}
func (a *App) guard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.user(r) == "" {
			fail(w, 401, "Anmeldung erforderlich")
			return
		}
		h.ServeHTTP(w, r)
	})
}
func (a *App) me(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, map[string]any{"user": a.user(r), "oidc": a.oauth != nil})
}
func (a *App) issue(w http.ResponseWriter, r *http.Request, user string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, s := range a.sessions {
		if time.Now().After(s.Expiry) {
			delete(a.sessions, k)
		}
	}
	if len(a.sessions) >= 1000 {
		fail(w, 503, "Zu viele Sitzungen")
		return
	}
	if c, e := r.Cookie("vloader_session"); e == nil {
		delete(a.sessions, c.Value)
	}
	key := random()
	a.sessions[key] = session{user, time.Now().Add(12 * time.Hour)}
	http.SetCookie(w, &http.Cookie{Name: "vloader_session", Value: key, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(a.origin, "https:"), SameSite: http.SameSiteLaxMode, MaxAge: 43200})
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Password string }
	if !decode(w, r, &in) {
		return
	}
	a.mu.Lock()
	until := a.attempts["local"]
	if time.Now().Before(until) {
		a.mu.Unlock()
		fail(w, 429, "Bitte kurz warten")
		return
	}
	a.attempts["local"] = time.Now().Add(time.Second)
	a.mu.Unlock()
	e := bcrypt.CompareHashAndPassword(a.password, []byte(in.Password))
	if e != nil || subtle.ConstantTimeCompare([]byte(in.Username), []byte(env("ADMIN_USERNAME", "admin"))) != 1 {
		fail(w, 401, "Anmeldung fehlgeschlagen")
		return
	}
	a.issue(w, r, in.Username)
	jsonOut(w, map[string]bool{"ok": true})
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, e := r.Cookie("vloader_session"); e == nil {
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "vloader_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(a.origin, "https:"), SameSite: http.SameSiteLaxMode})
	jsonOut(w, map[string]bool{"ok": true})
}
func (a *App) oidcStart(w http.ResponseWriter, r *http.Request) {
	if a.oauth == nil {
		fail(w, 404, "OIDC nicht eingerichtet")
		return
	}
	state, nonce, v := random(), random(), oauth2.GenerateVerifier()
	a.mu.Lock()
	for k, f := range a.flows {
		if time.Now().After(f.Expiry) {
			delete(a.flows, k)
		}
	}
	if len(a.flows) >= 1000 {
		a.mu.Unlock()
		fail(w, 429, "Bitte später erneut versuchen")
		return
	}
	a.flows[state] = flow{nonce, v, time.Now().Add(5 * time.Minute)}
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "oidc_state", Value: state, Path: "/auth", MaxAge: 300, HttpOnly: true, Secure: strings.HasPrefix(a.origin, "https:"), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, a.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(v)), 302)
}
func (a *App) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if a.oauth == nil {
		fail(w, 404, "OIDC nicht eingerichtet")
		return
	}
	state := r.URL.Query().Get("state")
	c, e := r.Cookie("oidc_state")
	if e != nil || state == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(state)) != 1 {
		fail(w, 403, "Ungültiger Anmeldevorgang")
		return
	}
	a.mu.Lock()
	f, ok := a.flows[state]
	delete(a.flows, state)
	a.mu.Unlock()
	if !ok || time.Now().After(f.Expiry) {
		fail(w, 403, "Anmeldung abgelaufen")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	tok, e := a.oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(f.Verifier))
	if e != nil {
		fail(w, 401, "OIDC Anmeldung fehlgeschlagen")
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	id, e := a.verifier.Verify(ctx, raw)
	if e != nil || id.Nonce != f.Nonce {
		fail(w, 401, "Ungültiges ID-Token")
		return
	}
	allowed := false
	for _, sub := range strings.Split(os.Getenv("OIDC_ALLOWED_SUBJECTS"), ",") {
		if strings.TrimSpace(sub) == id.Subject {
			allowed = true
		}
	}
	if !allowed {
		fail(w, 403, "Konto nicht freigegeben")
		return
	}
	a.issue(w, r, id.Subject)
	http.Redirect(w, r, "/", 303)
}
func (a *App) config() Config { a.mu.RLock(); defer a.mu.RUnlock(); return a.cfg }
func (a *App) settings(w http.ResponseWriter, r *http.Request) {
	c := a.config()
	jsonOut(w, map[string]any{"EmbyURL": c.EmbyURL, "HasAPIKey": c.APIKey != "", "SourcePrefix": c.SourcePrefix, "SourceMode": c.SourceMode, "OIDC": a.oauth != nil})
}
func persist(file string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	tmp := file + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, file)
}
func (a *App) saveSettings(w http.ResponseWriter, r *http.Request) {
	var c Config
	if !decode(w, r, &c) {
		return
	}
	u, e := url.Parse(c.EmbyURL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") || (c.SourceMode != "emby" && c.SourceMode != "mount") || !strings.HasPrefix(c.SourcePrefix, "/") {
		fail(w, 400, "URL, Quellpfad oder Modus ungültig")
		return
	}
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	old := a.config()
	if c.APIKey == "" {
		if c.EmbyURL != old.EmbyURL {
			fail(w, 400, "Bei Serverwechsel API-Schlüssel erneut eingeben")
			return
		}
		c.APIKey = old.APIKey
	}
	if c.APIKey == "" {
		fail(w, 400, "API-Schlüssel fehlt")
		return
	}
	if e := persist(a.data+"/settings.json", c); e != nil {
		fail(w, 500, "Speichern fehlgeschlagen")
		return
	}
	a.mu.Lock()
	a.cfg = c
	a.mu.Unlock()
	jsonOut(w, map[string]bool{"ok": true})
}
func (a *App) emby(ctx context.Context, c Config, endpoint string) (*http.Response, error) {
	if c.EmbyURL == "" || c.APIKey == "" {
		return nil, errors.New("Emby nicht eingerichtet")
	}
	req, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(c.EmbyURL, "/")+endpoint, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("X-Emby-Token", c.APIKey)
	client := *a.client
	if strings.HasSuffix(endpoint, "/Download") {
		client.Timeout = 0

	}
	res, e := client.Do(req)
	if e != nil {
		return nil, errors.New("Emby nicht erreichbar")
	}
	if res.StatusCode != 200 {
		res.Body.Close()
		return nil, fmt.Errorf("Emby antwortet mit HTTP %d", res.StatusCode)
	}
	return res, nil
}
func (a *App) fetch(ctx context.Context, c Config, endpoint string, out any) error {
	res, e := a.emby(ctx, c, endpoint)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	return json.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(out)
}
func (a *App) syncCatalog(w http.ResponseWriter, r *http.Request) {
	if !a.syncMu.TryLock() {
		fail(w, 409, "Synchronisierung läuft bereits")
		return
	}
	defer a.syncMu.Unlock()
	c := a.config()
	var libs struct{ Items []Item }
	if e := a.fetch(r.Context(), c, "/Library/MediaFolders", &libs); e != nil {
		fail(w, 502, e.Error())
		return
	}
	next := Catalog{SourceURL: c.EmbyURL, Libraries: libs.Items, Items: []Item{}, Updated: time.Now()}
	for _, lib := range libs.Items {
		for start := 0; ; {
			var page struct {
				Items            []Item
				TotalRecordCount int
			}
			q := url.Values{"ParentId": {lib.ID}, "Recursive": {"true"}, "Fields": {"Overview,Path,Genres,MediaSources,ParentId"}, "StartIndex": {fmt.Sprint(start)}, "Limit": {"500"}}
			if e := a.fetch(r.Context(), c, "/Items?"+q.Encode(), &page); e != nil {
				fail(w, 502, e.Error())
				return
			}
			for _, item := range page.Items {
				item.LibraryID = lib.ID
				next.Items = append(next.Items, item)
			}
			start += len(page.Items)
			if start >= page.TotalRecordCount || len(page.Items) == 0 {
				break
			}
			if start > 100000 {
				fail(w, 422, "Bibliothek überschreitet 100.000 Einträge")
				return
			}
		}
	}
	if e := persist(a.data+"/catalog.json", next); e != nil {
		fail(w, 500, "Katalog konnte nicht gespeichert werden")
		return
	}
	a.mu.Lock()
	a.catalog = next
	a.mu.Unlock()
	jsonOut(w, next)
}
func (a *App) sourceItem(id string) (Item, Config, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.catalog.SourceURL != a.cfg.EmbyURL {
		return Item{}, a.cfg, false
	}
	for _, v := range a.catalog.Items {
		if v.ID == id {
			return v, a.cfg, true
		}
	}
	return Item{}, a.cfg, false
}
func (a *App) item(id string) (Item, bool) { i, _, ok := a.sourceItem(id); return i, ok }
func (a *App) image(w http.ResponseWriter, r *http.Request) {
	id, kind := r.PathValue("id"), r.PathValue("kind")
	_, c, ok := a.sourceItem(id)
	if !ok || (kind != "Primary" && kind != "Backdrop") {
		fail(w, 404, "Bild nicht gefunden")
		return
	}
	res, e := a.emby(r.Context(), c, "/Items/"+url.PathEscape(id)+"/Images/"+kind+"?MaxWidth=1280&Quality=85")
	if e != nil {
		fail(w, 502, "Bild nicht verfügbar")
		return
	}
	defer res.Body.Close()
	ct := res.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") || strings.Contains(ct, "svg") {
		fail(w, 502, "Ungültiges Bildformat")
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	io.Copy(w, io.LimitReader(res.Body, 15<<20))
}
func relativeSource(prefix, p string) (string, error) {
	prefix = path.Clean(prefix)
	p = path.Clean(p)
	if prefix == "/" || !strings.HasPrefix(p, prefix+"/") {
		return "", errors.New("Pfad außerhalb der Quelle")
	}
	rel := strings.TrimPrefix(p, prefix+"/")
	if !fs.ValidPath(rel) {
		return "", errors.New("Ungültiger Quellpfad")
	}
	return rel, nil
}
func (a *App) download(w http.ResponseWriter, r *http.Request) {
	item, c, ok := a.sourceItem(r.PathValue("id"))
	if !ok || item.IsFolder {
		fail(w, 404, "Datei nicht gefunden")
		return
	}
	if c.SourceMode == "mount" {
		rel, e := relativeSource(c.SourcePrefix, item.Path)
		if e != nil {
			fail(w, 403, e.Error())
			return
		}
		root, e := os.OpenRoot(env("MEDIA_ROOT", "/media"))
		if e != nil {
			fail(w, 503, "Freigabe nicht verfügbar")
			return
		}
		defer root.Close()
		file, e := root.Open(rel)
		if e != nil {
			fail(w, 404, "Quelldatei nicht verfügbar")
			return
		}
		defer file.Close()
		info, e := file.Stat()
		if e != nil || !info.Mode().IsRegular() {
			fail(w, 404, "Keine reguläre Datei")
			return
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": info.Name()}))
		http.ServeContent(w, r, info.Name(), info.ModTime(), file)
		return
	}
	res, e := a.emby(r.Context(), c, "/Items/"+url.PathEscape(item.ID)+"/Download")
	if e != nil {
		fail(w, 502, "Emby-Download nicht verfügbar")
		return
	}
	defer res.Body.Close()
	name := path.Base(item.Path)
	if name == "." || name == "/" || name == "" {
		name = item.Name
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Content-Type", "application/octet-stream")
	if n := res.Header.Get("Content-Length"); n != "" {
		w.Header().Set("Content-Length", n)
	}
	io.Copy(w, res.Body)
}
