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
	"net/http/httptest"
	"net/mail"
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
	EmbyURL             string
	APIKey              string
	SourcePrefix        string
	SourceMode          string
	OIDCIssuer          string
	OIDCClientID        string
	OIDCClientSecret    string
	OIDCAllowedSubjects string
	OIDCAdminSubjects   string
	OIDCGroupsClaim     string
	OIDCAllowedGroups   string
	OIDCAdminGroups     string
	UpdateOIDC          bool
	SyncEnabled         bool
	SyncIntervalMinutes int
	AdminEmail          string
	SMTPHost            string
	SMTPPort            int
	SMTPDisableTLS      bool
	SMTPUsername        string
	SMTPPassword        string
	SMTPFrom            string
	TMDBAPIKey          string
	TVDBAPIKey          string
	TVDBPIN             string
}
type Item struct {
	Metadata          json.RawMessage `json:"Metadata,omitempty"`
	ID                string          `json:"Id"`
	Name              string
	Type              string
	CollectionType    string
	Path              string
	Overview          string
	PremiereDate      string
	ProductionYear    int
	CommunityRating   float64
	RunTimeTicks      int64
	Genres            []string
	ProviderIDs       map[string]string `json:"ProviderIds"`
	ImageTags         map[string]string
	BackdropImageTags []string
	ParentID          string `json:"ParentId"`
	LibraryID         string `json:"LibraryId"`
	SeriesID          string `json:"SeriesId"`
	SeasonID          string `json:"SeasonId"`
	IndexNumber       int
	ParentIndexNumber int
	IsFolder          bool
	MediaSources      []struct {
		Path      string
		Container string
		Size      int64
	}
	MediaStreams []struct {
		Type         string
		Codec        string
		Language     string
		DisplayTitle string
		IsDefault    bool
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
type Wish struct {
	ID            string    `json:"id"`
	Requester     string    `json:"requester"`
	RequesterName string    `json:"requesterName,omitempty"`
	Title         string    `json:"title"`
	Type          string    `json:"type"`
	Source        string    `json:"source"`
	ExternalID    string    `json:"externalId,omitempty"`
	SourceURL     string    `json:"sourceUrl,omitempty"`
	Status        string    `json:"status"`
	ItemID        string    `json:"itemId,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}
type session struct {
	User        string
	DisplayName string
	Expiry      time.Time
}

var sessionRoles sync.Map

type flow struct {
	Nonce, Verifier string
	Expiry          time.Time
}
type App struct {
	mu            sync.RWMutex
	syncMu        sync.Mutex
	cfg           Config
	catalog       Catalog
	wishes        []Wish
	wishMetadata  map[string]wishMetadataCache
	tvdbToken     string
	tvdbExpires   time.Time
	tvdbKey       string
	tvdbPIN       string
	notifications chan Wish
	sessions      map[string]session
	roles         map[string]string
	flows         map[string]flow
	attempts      map[string]time.Time
	password      []byte
	localAuth     bool
	origin        string
	data          string
	client        *http.Client
	mediaRoot     string
	oauth         *oauth2.Config
	verifier      *oidc.IDTokenVerifier
	version       string
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
	go a.notificationWorker()
	s := &http.Server{Addr: ":8080", Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	log.Print("vloader listening on :8080")
	log.Fatal(s.ListenAndServe())
}
func New() (*App, error) {
	a := &App{data: path.Clean(env("DATA_DIR", "/data")), mediaRoot: "/media", origin: env("APP_URL", "http://localhost:8090"), version: env("APP_VERSION", "dev"), sessions: map[string]session{}, flows: map[string]flow{}, attempts: map[string]time.Time{}, wishMetadata: map[string]wishMetadataCache{}, notifications: make(chan Wish, 64), client: &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second, MaxIdleConns: 20}, CheckRedirect: func(r *http.Request, v []*http.Request) error { return http.ErrUseLastResponse }}}
	if !path.IsAbs(a.data) || a.data == "/" || a.data == a.mediaRoot || strings.HasPrefix(a.data, a.mediaRoot+"/") || strings.HasPrefix(a.mediaRoot, a.data+"/") {
		return nil, errors.New("DATA_DIR must be an absolute path separate from the /media source mount")
	}
	u, e := url.Parse(a.origin)
	if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.Path != "" {
		return nil, errors.New("APP_URL must be an origin without trailing slash")
	}
	if e = os.MkdirAll(a.data, 0700); e != nil {
		return nil, e
	}
	a.cfg = Config{EmbyURL: os.Getenv("EMBY_URL"), APIKey: os.Getenv("EMBY_API_KEY"), SourcePrefix: env("SOURCE_PREFIX", "/media"), SourceMode: env("SOURCE_MODE", "emby"), OIDCIssuer: os.Getenv("OIDC_ISSUER"), OIDCClientID: os.Getenv("OIDC_CLIENT_ID"), OIDCClientSecret: os.Getenv("OIDC_CLIENT_SECRET"), OIDCAllowedSubjects: os.Getenv("OIDC_ALLOWED_SUBJECTS"), OIDCAdminSubjects: os.Getenv("OIDC_ADMIN_SUBJECTS"), OIDCGroupsClaim: env("OIDC_GROUPS_CLAIM", "groups"), OIDCAllowedGroups: os.Getenv("OIDC_ALLOWED_GROUPS"), OIDCAdminGroups: os.Getenv("OIDC_ADMIN_GROUPS")}
	if e := loadState(a.data+"/settings.json", &a.cfg); e != nil {
		return nil, e
	}
	if a.cfg.SyncIntervalMinutes <= 0 {
		a.cfg.SyncIntervalMinutes = 60
		a.cfg.SyncEnabled = true
	}
	if e := loadState(a.data+"/catalog.json", &a.catalog); e != nil {
		return nil, e
	}
	if e := loadState(a.data+"/wishes.json", &a.wishes); e != nil {
		return nil, e
	}
	a.localAuth = env("LOCAL_AUTH_ENABLED", "true") == "true"
	if a.localAuth {
		p := os.Getenv("ADMIN_PASSWORD")
		if len(p) < 16 {
			return nil, errors.New("ADMIN_PASSWORD must contain at least 16 characters when local authentication is enabled")
		}
		a.password, e = bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
		if e != nil {
			return nil, e
		}
	}
	if e = a.configureOIDC(a.cfg); e != nil {
		return nil, e
	}
	if !a.localAuth && a.oauth == nil {
		return nil, errors.New("enable local authentication or configure OIDC")
	}
	if os.Getenv("VLOADER_DISABLE_SCHEDULER") != "true" {
		go a.scheduler()
	}
	return a, nil
}
func (a *App) scheduler() {
	for {
		c := a.config()
		minutes := c.SyncIntervalMinutes
		if minutes < 5 {
			minutes = 60
		}
		t := time.NewTimer(time.Duration(minutes) * time.Minute)
		<-t.C
		if a.config().SyncEnabled && a.config().EmbyURL != "" {
			a.syncBackground()
		}
	}
}
func (a *App) syncBackground() {
	r := httptest.NewRequest(http.MethodPost, "/api/sync", nil)
	w := httptest.NewRecorder()
	a.syncCatalog(w, r)
	if w.Code >= 300 {
		log.Printf("scheduled sync failed: %s", w.Body.String())
	}
}
func (a *App) configureOIDC(c Config) error {
	if issuer := c.OIDCIssuer; issuer != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		provider, e := oidc.NewProvider(ctx, issuer)
		if e != nil {
			log.Print("vloader: OIDC provider discovery failed")
			return errors.New("OIDC discovery failed")
		}
		if c.OIDCClientID == "" || (c.OIDCAllowedSubjects == "" && c.OIDCAllowedGroups == "" && c.OIDCAdminGroups == "") {
			return errors.New("OIDC_CLIENT_ID and at least one allowed subject or group required")
		}
		scopes := []string{oidc.ScopeOpenID, "profile"}
		if c.OIDCAllowedGroups != "" || c.OIDCAdminGroups != "" {
			scopes = append(scopes, "groups")
		}
		a.oauth = &oauth2.Config{ClientID: c.OIDCClientID, ClientSecret: c.OIDCClientSecret, Endpoint: provider.Endpoint(), RedirectURL: a.origin + "/auth/callback", Scopes: scopes}
		a.verifier = provider.Verifier(&oidc.Config{ClientID: a.oauth.ClientID})
	} else {
		a.oauth, a.verifier = nil, nil
	}
	return nil
}
func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	log.Printf("vloader: request error status=%d reason=%q", status, msg)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	jsonOut(w, map[string]string{"error": msg})
}

type logResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *logResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *logResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *logResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func requestLogger(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, route := mux.Handler(r)
		if route == "" {
			route = "unmatched route"
		}
		started := time.Now()
		tracked := &logResponseWriter{ResponseWriter: w}
		next.ServeHTTP(tracked, r)
		status := tracked.status
		if status == 0 {
			status = http.StatusOK
		}
		// Avoid logging routine image traffic, static assets, health probes and
		// polling. Log failures and state-changing/download operations instead.
		if status >= 400 || strings.HasPrefix(route, "POST /api/") ||
			strings.HasPrefix(route, "POST /auth/") || route == "GET /api/download/{id}" {
			log.Printf("vloader: http request method=%s route=%q status=%d duration=%s", r.Method, route, status, time.Since(started).Round(time.Millisecond))
		}
	})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		fail(w, 400, "Invalid input")
		return false
	}
	return true
}
func (a *App) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { jsonOut(w, map[string]string{"status": "ok"}) })
	m.HandleFunc("GET /api/session", a.me)
	m.HandleFunc("GET /api/version", a.versionStatus)
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
	m.Handle("GET /api/wishes", a.guard(http.HandlerFunc(a.listWishes)))
	m.Handle("POST /api/wishes", a.guard(http.HandlerFunc(a.createWish)))
	m.Handle("GET /api/wishes/{id}/metadata", a.guard(http.HandlerFunc(a.getWishMetadata)))
	m.Handle("POST /api/wishes/{id}", a.adminGuard(http.HandlerFunc(a.updateWish)))
	m.Handle("GET /api/settings", a.adminGuard(http.HandlerFunc(a.settings)))
	m.Handle("POST /api/settings", a.adminGuard(http.HandlerFunc(a.saveSettings)))
	m.Handle("POST /api/settings/notifications", a.adminGuard(http.HandlerFunc(a.saveNotificationSettings)))
	m.Handle("POST /api/settings/metadata", a.adminGuard(http.HandlerFunc(a.saveMetadataSettings)))
	m.Handle("POST /api/settings/notifications/test", a.adminGuard(http.HandlerFunc(a.testNotificationSettings)))
	m.Handle("POST /api/sync", a.adminGuard(http.HandlerFunc(a.syncCatalog)))
	m.Handle("GET /api/images/{id}/{kind}", a.guard(http.HandlerFunc(a.image)))
	m.Handle("GET /api/download/{id}", a.guard(http.HandlerFunc(a.download)))
	sub, _ := fs.Sub(assets, "web")
	m.Handle("/", http.FileServer(http.FS(sub)))
	secure := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self'; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("Origin") != a.origin {
			fail(w, 403, "Invalid request origin")
			return
		}
		m.ServeHTTP(w, r)
	})
	return requestLogger(m, secure)
}
func (a *App) versionStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/TheTaran/vloader/releases/latest", nil)
	if err != nil {
		jsonOut(w, map[string]any{"currentVersion": a.version, "available": false})
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "vloader-version-check")
	res, err := a.client.Do(req)
	if err != nil {
		jsonOut(w, map[string]any{"currentVersion": a.version, "available": false})
		return
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		jsonOut(w, map[string]any{"currentVersion": a.version, "available": false})
		return
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&release); err != nil || release.TagName == "" {
		jsonOut(w, map[string]any{"currentVersion": a.version, "available": false})
		return
	}
	normalize := func(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }
	jsonOut(w, map[string]any{
		"currentVersion":  a.version,
		"latestVersion":   release.TagName,
		"available":       true,
		"updateAvailable": normalize(a.version) != normalize(release.TagName),
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
func (a *App) displayNameForUser(user string) string {
	if user == "" {
		return ""
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, s := range a.sessions {
		if s.User == user && time.Now().Before(s.Expiry) && s.DisplayName != "" {
			return s.DisplayName
		}
	}
	return user
}
func (a *App) role(r *http.Request) string {
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
	role, _ := sessionRoles.Load(c.Value)
	if role == nil {
		return "admin"
	}
	return role.(string)
}
func (a *App) guard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.user(r) == "" {
			fail(w, 401, "Sign-in required")
			return
		}
		h.ServeHTTP(w, r)
	})
}
func (a *App) adminGuard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.user(r) == "" {
			fail(w, 401, "Sign-in required")
			return
		}
		if a.role(r) != "admin" {
			fail(w, 403, "Administrator access required")
			return
		}
		h.ServeHTTP(w, r)
	})
}
func (a *App) me(w http.ResponseWriter, r *http.Request) {
	name := a.displayNameForUser(a.user(r))
	jsonOut(w, map[string]any{"user": name, "role": a.role(r), "admin": a.role(r) == "admin", "oidc": a.oauth != nil, "localAuth": a.localAuth})
}
func (a *App) issue(w http.ResponseWriter, r *http.Request, user, role string, displayNames ...string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, s := range a.sessions {
		if time.Now().After(s.Expiry) {
			delete(a.sessions, k)
		}
	}
	if len(a.sessions) >= 1000 {
		fail(w, 503, "Too many sessions")
		return
	}
	if c, e := r.Cookie("vloader_session"); e == nil {
		delete(a.sessions, c.Value)
	}
	key := random()
	displayName := user
	if len(displayNames) > 0 && strings.TrimSpace(displayNames[0]) != "" {
		displayName = strings.TrimSpace(displayNames[0])
	}
	a.sessions[key] = session{User: user, DisplayName: displayName, Expiry: time.Now().Add(12 * time.Hour)}
	sessionRoles.Store(key, role)
	http.SetCookie(w, &http.Cookie{Name: "vloader_session", Value: key, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(a.origin, "https:"), SameSite: http.SameSiteLaxMode, MaxAge: 43200})
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if !a.localAuth {
		fail(w, 404, "Local authentication is disabled")
		return
	}
	var in struct{ Username, Password string }
	if !decode(w, r, &in) {
		return
	}
	a.mu.Lock()
	until := a.attempts["local"]
	if time.Now().Before(until) {
		a.mu.Unlock()
		fail(w, 429, "Please wait a moment")
		return
	}
	a.attempts["local"] = time.Now().Add(time.Second)
	a.mu.Unlock()
	e := bcrypt.CompareHashAndPassword(a.password, []byte(in.Password))
	if e != nil || subtle.ConstantTimeCompare([]byte(in.Username), []byte(env("ADMIN_USERNAME", "admin"))) != 1 {
		fail(w, 401, "Sign-in failed")
		return
	}
	a.issue(w, r, in.Username, "admin")
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
		fail(w, 404, "OIDC not configured")
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
		fail(w, 429, "Please try again later")
		return
	}
	a.flows[state] = flow{nonce, v, time.Now().Add(5 * time.Minute)}
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "oidc_state", Value: state, Path: "/auth", MaxAge: 300, HttpOnly: true, Secure: strings.HasPrefix(a.origin, "https:"), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, a.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(v)), 302)
}

func oidcClaimValues(raw json.RawMessage) []string {
	var values []string
	if len(raw) == 0 {
		return values
	}
	if err := json.Unmarshal(raw, &values); err == nil {
		return values
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil && single != "" {
		return []string{single}
	}
	return values
}

func oidcDisplayName(claims map[string]json.RawMessage, subject string) string {
	for _, key := range []string{"display_name", "name"} {
		var value string
		if json.Unmarshal(claims[key], &value) == nil && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return subject
}

func hasExactCSVValue(csv, value string) bool {
	for _, candidate := range strings.Split(csv, ",") {
		if candidate = strings.TrimSpace(candidate); candidate != "" && candidate == value {
			return true
		}
	}
	return false
}

func hasAnyCSVValue(csv string, values []string) bool {
	for _, value := range values {
		if hasExactCSVValue(csv, value) {
			return true
		}
	}
	return false
}

func (a *App) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if a.oauth == nil {
		fail(w, 404, "OIDC not configured")
		return
	}
	state := r.URL.Query().Get("state")
	c, e := r.Cookie("oidc_state")
	if e != nil || state == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(state)) != 1 {
		fail(w, 403, "Invalid sign-in request")
		return
	}
	a.mu.Lock()
	f, ok := a.flows[state]
	delete(a.flows, state)
	a.mu.Unlock()
	if !ok || time.Now().After(f.Expiry) {
		fail(w, 403, "Sign-in request expired")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	tok, e := a.oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(f.Verifier))
	if e != nil {
		log.Print("vloader: OIDC authorization code exchange failed")
		fail(w, 401, "OIDC sign-in failed")
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	id, e := a.verifier.Verify(ctx, raw)
	if e != nil {
		log.Print("vloader: OIDC ID token verification failed")
		fail(w, 401, "Invalid ID token")
		return
	}
	if id.Nonce != f.Nonce {
		log.Print("vloader: OIDC ID token nonce mismatch")
		fail(w, 401, "Invalid ID token")
		return
	}
	cfg := a.config()
	claimName := strings.TrimSpace(cfg.OIDCGroupsClaim)
	if claimName == "" {
		claimName = "groups"
	}
	var tokenClaims map[string]json.RawMessage
	if e := id.Claims(&tokenClaims); e != nil {
		log.Print("vloader: OIDC ID token claims could not be decoded")
		fail(w, 401, "Invalid ID token")
		return
	}
	groups := oidcClaimValues(tokenClaims[claimName])
	allowed := hasExactCSVValue(cfg.OIDCAllowedSubjects, id.Subject) ||
		hasAnyCSVValue(cfg.OIDCAllowedGroups, groups) ||
		hasAnyCSVValue(cfg.OIDCAdminGroups, groups)
	if !allowed {
		fail(w, 403, "Account not authorized")
		return
	}
	role := "user"
	if hasExactCSVValue(cfg.OIDCAdminSubjects, id.Subject) || hasAnyCSVValue(cfg.OIDCAdminGroups, groups) {
		role = "admin"
	}
	a.issue(w, r, id.Subject, role, oidcDisplayName(tokenClaims, id.Subject))
	http.Redirect(w, r, "/", 303)
}
func (a *App) config() Config { a.mu.RLock(); defer a.mu.RUnlock(); return a.cfg }
func (a *App) settings(w http.ResponseWriter, r *http.Request) {
	c := a.config()
	claim := c.OIDCGroupsClaim
	if claim == "" {
		claim = "groups"
	}
	jsonOut(w, map[string]any{"EmbyURL": c.EmbyURL, "HasAPIKey": c.APIKey != "", "SourcePrefix": c.SourcePrefix, "SourceMode": c.SourceMode, "OIDCIssuer": c.OIDCIssuer, "OIDCClientID": c.OIDCClientID, "OIDCAllowedSubjects": c.OIDCAllowedSubjects, "OIDCAdminSubjects": c.OIDCAdminSubjects, "OIDCGroupsClaim": claim, "OIDCAllowedGroups": c.OIDCAllowedGroups, "OIDCAdminGroups": c.OIDCAdminGroups, "HasOIDCClientSecret": c.OIDCClientSecret != "", "OIDC": a.oauth != nil, "OIDCCallbackURL": strings.TrimRight(a.origin, "/") + "/auth/callback", "LocalAuth": a.localAuth, "SyncEnabled": c.SyncEnabled, "SyncIntervalMinutes": c.SyncIntervalMinutes, "AdminEmail": c.AdminEmail, "SMTPHost": c.SMTPHost, "SMTPPort": c.SMTPPort, "SMTPDisableTLS": c.SMTPDisableTLS, "SMTPUsername": c.SMTPUsername, "SMTPFrom": c.SMTPFrom, "HasSMTPPassword": c.SMTPPassword != "", "HasTMDBAPIKey": c.TMDBAPIKey != "", "HasTVDBAPIKey": c.TVDBAPIKey != "", "HasTVDBPIN": c.TVDBPIN != ""})
}

func (a *App) saveMetadataSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TMDBAPIKey string `json:"TMDBAPIKey"`
		TVDBAPIKey string `json:"TVDBAPIKey"`
		TVDBPIN    string `json:"TVDBPIN"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.TMDBAPIKey = strings.TrimSpace(in.TMDBAPIKey)
	if len(in.TMDBAPIKey) > 512 || strings.ContainsAny(in.TMDBAPIKey, "\r\n \t") {
		fail(w, http.StatusBadRequest, "Enter a valid TMDb API Read Access Token")
		return
	}
	in.TVDBAPIKey = strings.TrimSpace(in.TVDBAPIKey)
	in.TVDBPIN = strings.TrimSpace(in.TVDBPIN)
	if len(in.TVDBAPIKey) > 512 || strings.ContainsAny(in.TVDBAPIKey, "\r\n \t") || len(in.TVDBPIN) > 512 || strings.ContainsAny(in.TVDBPIN, "\r\n \t") {
		fail(w, http.StatusBadRequest, "Enter a valid TVDB API key and subscriber PIN")
		return
	}
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	c := a.config()
	if in.TMDBAPIKey != "" {
		c.TMDBAPIKey = in.TMDBAPIKey
	}
	if in.TVDBAPIKey != "" {
		c.TVDBAPIKey = in.TVDBAPIKey
	}
	if in.TVDBPIN != "" {
		c.TVDBPIN = in.TVDBPIN
	}
	if err := persist(a.data+"/settings.json", c); err != nil {
		fail(w, http.StatusInternalServerError, "Failed to save metadata settings")
		return
	}
	a.mu.Lock()
	a.cfg = c
	a.wishMetadata = map[string]wishMetadataCache{}
	a.tvdbToken, a.tvdbKey, a.tvdbPIN = "", "", ""
	a.tvdbExpires = time.Time{}
	a.mu.Unlock()
	jsonOut(w, map[string]bool{"ok": true})
}

func (a *App) saveNotificationSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AdminEmail     string `json:"AdminEmail"`
		SMTPHost       string `json:"SMTPHost"`
		SMTPPort       int    `json:"SMTPPort"`
		SMTPDisableTLS bool   `json:"SMTPDisableTLS"`
		SMTPUsername   string `json:"SMTPUsername"`
		SMTPPassword   string `json:"SMTPPassword"`
		SMTPFrom       string `json:"SMTPFrom"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.AdminEmail = strings.TrimSpace(in.AdminEmail)
	in.SMTPHost = strings.TrimSpace(in.SMTPHost)
	in.SMTPUsername = strings.TrimSpace(in.SMTPUsername)
	in.SMTPFrom = strings.TrimSpace(in.SMTPFrom)
	if in.AdminEmail == "" || !validEmail(in.AdminEmail) || in.SMTPHost == "" || strings.ContainsAny(in.SMTPHost, " /\\\r\n:@") || in.SMTPPort < 1 || in.SMTPPort > 65535 || !validEmail(in.SMTPFrom) || strings.ContainsAny(in.SMTPUsername, "\r\n") || strings.ContainsAny(in.SMTPPassword, "\r\n") {
		fail(w, http.StatusBadRequest, "Enter a valid admin email, SMTP host and port, and sender email")
		return
	}
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	c := a.config()
	if in.SMTPPassword == "" {
		in.SMTPPassword = c.SMTPPassword
	}
	if in.SMTPUsername != "" && in.SMTPPassword == "" {
		fail(w, http.StatusBadRequest, "Enter the SMTP password when SMTP authentication is enabled")
		return
	}
	c.AdminEmail, c.SMTPHost, c.SMTPPort, c.SMTPDisableTLS = in.AdminEmail, in.SMTPHost, in.SMTPPort, in.SMTPDisableTLS
	c.SMTPUsername, c.SMTPPassword, c.SMTPFrom = in.SMTPUsername, in.SMTPPassword, in.SMTPFrom
	if err := persist(a.data+"/settings.json", c); err != nil {
		fail(w, http.StatusInternalServerError, "Failed to save notification settings")
		return
	}
	a.mu.Lock()
	a.cfg = c
	a.mu.Unlock()
	jsonOut(w, map[string]bool{"ok": true})
}

func (a *App) testNotificationSettings(w http.ResponseWriter, r *http.Request) {
	c := a.config()
	if c.AdminEmail == "" || c.SMTPHost == "" || c.SMTPPort < 1 || c.SMTPFrom == "" || (c.SMTPUsername != "" && c.SMTPPassword == "") {
		fail(w, http.StatusBadRequest, "Save complete email notification settings before sending a test")
		return
	}
	if err := sendTestEmail(c); err != nil {
		log.Printf("vloader: SMTP test email failed host=%q: %v", c.SMTPHost, err)
		fail(w, http.StatusBadGateway, "Test email failed; check the SMTP settings and vloader logs")
		return
	}
	log.Printf("vloader: SMTP test email sent successfully")
	jsonOut(w, map[string]bool{"ok": true})
}

func validEmail(value string) bool {
	addr, err := mail.ParseAddress(value)
	return err == nil && addr.Address == value
}
func persist(file string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		log.Printf("vloader: unable to encode persisted state file=%s: %v", path.Base(file), e)
		return e
	}
	tmp := file + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		log.Printf("vloader: unable to write persisted state file=%s: %v", path.Base(file), e)
		return e
	}
	if e = os.Rename(tmp, file); e != nil {
		log.Printf("vloader: unable to replace persisted state file=%s: %v", path.Base(file), e)
		return e
	}
	return nil
}

func loadState(file string, dst any) error {
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read persisted state file %s: %w", path.Base(file), err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("decode persisted state file %s: %w", path.Base(file), err)
	}
	return nil
}
func (a *App) saveSettings(w http.ResponseWriter, r *http.Request) {
	var c Config
	if !decode(w, r, &c) {
		return
	}
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	old := a.config()
	if c.UpdateOIDC {
		// Authentication has its own form; do not make it depend on Emby/source
		// values sent by the browser. Preserve those settings exactly as stored.
		c.EmbyURL, c.APIKey, c.SourceMode, c.SourcePrefix = old.EmbyURL, old.APIKey, old.SourceMode, old.SourcePrefix
		c.SyncEnabled, c.SyncIntervalMinutes = old.SyncEnabled, old.SyncIntervalMinutes
	} else {
		u, err := url.Parse(c.EmbyURL)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") || (c.SourceMode != "emby" && c.SourceMode != "mount") || !strings.HasPrefix(c.SourcePrefix, "/") {
			fail(w, 400, "Invalid URL, source path or transfer mode")
			return
		}
	}
	if c.SyncIntervalMinutes == 0 {
		c.SyncIntervalMinutes = old.SyncIntervalMinutes
	}
	if c.SyncIntervalMinutes == 0 {
		c.SyncIntervalMinutes = 60
	}
	if c.SyncIntervalMinutes < 5 || c.SyncIntervalMinutes > 10080 {
		fail(w, 400, "Sync interval must be between 5 minutes and 7 days")
		return
	}
	if !c.UpdateOIDC {
		c.OIDCIssuer, c.OIDCClientID, c.OIDCClientSecret, c.OIDCAllowedSubjects, c.OIDCAdminSubjects = old.OIDCIssuer, old.OIDCClientID, old.OIDCClientSecret, old.OIDCAllowedSubjects, old.OIDCAdminSubjects
		c.OIDCGroupsClaim, c.OIDCAllowedGroups, c.OIDCAdminGroups = old.OIDCGroupsClaim, old.OIDCAllowedGroups, old.OIDCAdminGroups
	}
	// Admin subject IDs remain environment-managed; the GUI edits group-based roles.
	c.OIDCAdminSubjects = old.OIDCAdminSubjects
	// Notifications are edited through their own admin-only form and must survive
	// saves from the connection and authentication forms.
	c.AdminEmail, c.SMTPHost, c.SMTPPort, c.SMTPDisableTLS = old.AdminEmail, old.SMTPHost, old.SMTPPort, old.SMTPDisableTLS
	c.SMTPUsername, c.SMTPPassword, c.SMTPFrom = old.SMTPUsername, old.SMTPPassword, old.SMTPFrom
	c.TMDBAPIKey = old.TMDBAPIKey
	c.TVDBAPIKey, c.TVDBPIN = old.TVDBAPIKey, old.TVDBPIN
	if !c.UpdateOIDC && c.APIKey == "" {
		if c.EmbyURL != old.EmbyURL {
			fail(w, 400, "Enter the API key again when switching servers")
			return
		}
		c.APIKey = old.APIKey
	}
	if !c.UpdateOIDC && c.APIKey == "" {
		fail(w, 400, "API key is required")
		return
	}
	if c.OIDCClientSecret == "" {
		c.OIDCClientSecret = old.OIDCClientSecret
	}
	if c.OIDCGroupsClaim == "" {
		c.OIDCGroupsClaim = "groups"
	}
	if c.OIDCIssuer != "" && (c.OIDCClientID == "" || (c.OIDCAllowedSubjects == "" && c.OIDCAllowedGroups == "" && c.OIDCAdminGroups == "")) {
		fail(w, 400, "OIDC client ID and at least one allowed subject or group are required")
		return
	}
	if !a.localAuth && c.OIDCIssuer == "" {
		fail(w, 400, "OIDC cannot be removed while local authentication is disabled")
		return
	}
	if e := a.configureOIDC(c); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if !a.localAuth && a.oauth == nil {
		fail(w, 400, "Configure OIDC before disabling local authentication")
		return
	}
	if e := persist(a.data+"/settings.json", c); e != nil {
		fail(w, 500, "Failed to save settings; check that the mounted data directory is writable by UID 10001")
		return
	}
	a.mu.Lock()
	a.cfg = c
	a.mu.Unlock()
	if c.EmbyURL != "" && (old.EmbyURL == "" || old.APIKey == "" || a.catalog.Updated.IsZero()) {
		go a.syncBackground()
	}
	jsonOut(w, map[string]bool{"ok": true})
}
func (a *App) emby(ctx context.Context, c Config, endpoint string) (*http.Response, error) {
	if c.EmbyURL == "" || c.APIKey == "" {
		return nil, errors.New("Emby not configured")
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
		if host := req.URL.Hostname(); host != "" {
			log.Printf("vloader: Emby request failed host=%q: %v", host, e)
		} else {
			log.Printf("vloader: Emby request failed: %v", e)
		}
		return nil, errors.New("Unable to connect to Emby")
	}
	if res.StatusCode != 200 {
		log.Printf("vloader: Emby request returned HTTP %d host=%q", res.StatusCode, req.URL.Hostname())
		res.Body.Close()
		return nil, fmt.Errorf("Emby returned HTTP %d", res.StatusCode)
	}
	return res, nil
}
func (a *App) fetch(ctx context.Context, c Config, endpoint string, out any) error {
	res, e := a.emby(ctx, c, endpoint)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if err := json.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(out); err != nil {
		log.Printf("vloader: unable to decode Emby response: %v", err)
		return err
	}
	return nil
}
func (a *App) syncCatalog(w http.ResponseWriter, r *http.Request) {
	if !a.syncMu.TryLock() {
		fail(w, 409, "Sync already in progress")
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
			q := url.Values{"ParentId": {lib.ID}, "Recursive": {"true"}, "Fields": {"Overview,Path,Genres,ProviderIds,MediaSources,MediaStreams,PremiereDate,ParentId,SeriesId,SeasonId,IndexNumber"}, "StartIndex": {fmt.Sprint(start)}, "Limit": {"500"}}
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
				fail(w, 422, "Library exceeds 100,000 items")
				return
			}
		}
	}
	if e := persist(a.data+"/catalog.json", next); e != nil {
		fail(w, 500, "Failed to save the catalog")
		return
	}
	a.mu.Lock()
	a.catalog = next
	if a.resolveWishesLocked(next) {
		if err := a.persistWishesLocked(); err != nil {
			log.Printf("failed to persist wish availability: %v", err)
		}
	}
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
		fail(w, 404, "Image not found")
		return
	}
	res, e := a.emby(r.Context(), c, "/Items/"+url.PathEscape(id)+"/Images/"+kind+"?MaxWidth=1280&Quality=85")
	if e != nil {
		fail(w, 502, "Image unavailable")
		return
	}
	defer res.Body.Close()
	ct := res.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") || strings.Contains(ct, "svg") {
		fail(w, 502, "Invalid image format")
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if _, err := io.Copy(w, io.LimitReader(res.Body, 15<<20)); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("vloader: image transfer failed: %v", err)
	}
}
func relativeSource(prefix, p string) (string, error) {
	prefix = path.Clean(prefix)
	p = path.Clean(p)
	if prefix == "/" || !strings.HasPrefix(p, prefix+"/") {
		return "", errors.New("Path is outside the source")
	}
	rel := strings.TrimPrefix(p, prefix+"/")
	if !fs.ValidPath(rel) {
		return "", errors.New("Invalid source path")
	}
	return rel, nil
}
func (a *App) download(w http.ResponseWriter, r *http.Request) {
	item, c, ok := a.sourceItem(r.PathValue("id"))
	if !ok || item.IsFolder {
		fail(w, 404, "File not found")
		return
	}
	if c.SourceMode == "mount" {
		rel, e := relativeSource(c.SourcePrefix, item.Path)
		if e != nil {
			fail(w, 403, e.Error())
			return
		}
		root, e := os.OpenRoot(a.mediaRoot)
		if e != nil {
			log.Printf("vloader: source root unavailable: %v", e)
			fail(w, 503, "Share unavailable")
			return
		}
		defer root.Close()
		file, e := root.Open(rel)
		if e != nil {
			log.Printf("vloader: source file open failed not_found=%t permission_denied=%t", errors.Is(e, os.ErrNotExist), errors.Is(e, os.ErrPermission))
			fail(w, 404, "Source file unavailable")
			return
		}
		defer file.Close()
		info, e := file.Stat()
		if e != nil || !info.Mode().IsRegular() {
			fail(w, 404, "Not a regular file")
			return
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": info.Name()}))
		http.ServeContent(w, r, info.Name(), info.ModTime(), file)
		return
	}
	res, e := a.emby(r.Context(), c, "/Items/"+url.PathEscape(item.ID)+"/Download")
	if e != nil {
		fail(w, 502, "Emby download unavailable")
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
	if _, err := io.Copy(w, res.Body); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("vloader: download transfer failed: %v", err)
	}
}
