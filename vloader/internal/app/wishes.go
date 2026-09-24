package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

var imdbIDPattern = regexp.MustCompile(`^tt[0-9]{5,12}$`)
var tmdbIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,11}$`)

type WishMetadata struct {
	Title       string   `json:"title"`
	Overview    string   `json:"overview,omitempty"`
	PosterURL   string   `json:"posterUrl,omitempty"`
	ReleaseDate string   `json:"releaseDate,omitempty"`
	Genres      []string `json:"genres,omitempty"`
	Rating      float64  `json:"rating,omitempty"`
	Runtime     int      `json:"runtime,omitempty"`
}

type wishMetadataCache struct {
	Metadata WishMetadata
	Expires  time.Time
}

func (a *App) listWishes(w http.ResponseWriter, r *http.Request) {
	user, admin := a.user(r), a.role(r) == "admin"
	a.mu.RLock()
	defer a.mu.RUnlock()
	displayNames := make(map[string]string, len(a.sessions))
	for _, session := range a.sessions {
		if session.User != "" && session.DisplayName != "" && time.Now().Before(session.Expiry) {
			displayNames[session.User] = session.DisplayName
		}
	}
	items := make([]Wish, 0, len(a.wishes))
	for _, wish := range a.wishes {
		if admin || wish.Requester == user {
			if wish.RequesterName == "" {
				wish.RequesterName = displayNames[wish.Requester]
			}
			items = append(items, wish)
		}
	}
	jsonOut(w, map[string]any{"items": items, "admin": admin, "metadataEnabled": a.cfg.TMDBAPIKey != ""})
}

func (a *App) getWishMetadata(w http.ResponseWriter, r *http.Request) {
	user, admin := a.user(r), a.role(r) == "admin"
	var wish Wish
	a.mu.RLock()
	found := false
	for _, candidate := range a.wishes {
		if candidate.ID == r.PathValue("id") && (admin || candidate.Requester == user) {
			wish, found = candidate, true
			break
		}
	}
	a.mu.RUnlock()
	if !found {
		fail(w, http.StatusNotFound, "Request not found")
		return
	}
	if wish.Source != "imdb" && wish.Source != "tmdb" {
		fail(w, http.StatusNotFound, "No external title reference for this request")
		return
	}
	cfg := a.config()
	if cfg.TMDBAPIKey == "" {
		fail(w, http.StatusFailedDependency, "Configure a TMDb API Read Access Token in Settings, Metadata to load title details")
		return
	}
	a.mu.RLock()
	cached, ok := a.wishMetadata[wish.ID]
	a.mu.RUnlock()
	if ok && time.Now().Before(cached.Expires) {
		jsonOut(w, cached.Metadata)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	metadata, err := fetchWishMetadata(ctx, a.client, cfg.TMDBAPIKey, wish)
	if err != nil {
		log.Printf("vloader: TMDb metadata lookup failed for request %s: %v", wish.ID, err)
		fail(w, http.StatusBadGateway, "Could not load external title details; verify the TMDb API Read Access Token")
		return
	}
	a.mu.Lock()
	a.wishMetadata[wish.ID] = wishMetadataCache{Metadata: metadata, Expires: time.Now().Add(6 * time.Hour)}
	a.mu.Unlock()
	jsonOut(w, metadata)
}

type tmdbSearchResult struct {
	ID          int      `json:"id"`
	Title       string   `json:"title"`
	Name        string   `json:"name"`
	Overview    string   `json:"overview"`
	PosterPath  string   `json:"poster_path"`
	ReleaseDate string   `json:"release_date"`
	FirstAir    string   `json:"first_air_date"`
	Rating      float64  `json:"vote_average"`
	Genres      []string `json:"-"`
}

func fetchWishMetadata(ctx context.Context, baseClient *http.Client, apiKey string, wish Wish) (WishMetadata, error) {
	client := *baseClient
	client.Timeout = 8 * time.Second
	requestJSON := func(endpoint string, target any) error {
		u := url.URL{Scheme: "https", Host: "api.themoviedb.org", Path: "/3/" + strings.TrimLeft(endpoint, "/")}
		q := u.Query()
		if wish.Source == "imdb" && strings.HasPrefix(endpoint, "find/") {
			q.Set("external_source", "imdb_id")
		}
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("TMDb returned HTTP %d", res.StatusCode)
		}
		if err := json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(target); err != nil {
			return fmt.Errorf("invalid TMDb response")
		}
		return nil
	}
	var result tmdbSearchResult
	if wish.Source == "imdb" {
		var matches struct {
			Movies []tmdbSearchResult `json:"movie_results"`
			TV     []tmdbSearchResult `json:"tv_results"`
		}
		if err := requestJSON("find/"+url.PathEscape(wish.ExternalID), &matches); err != nil {
			return WishMetadata{}, err
		}
		if wish.Type == "Movie" && len(matches.Movies) > 0 {
			result = matches.Movies[0]
		} else if wish.Type == "Series" && len(matches.TV) > 0 {
			result = matches.TV[0]
		} else {
			return WishMetadata{}, fmt.Errorf("no matching %s title found", wish.Type)
		}
	} else {
		kind := "movie"
		if wish.Type == "Series" {
			kind = "tv"
		}
		if err := requestJSON(kind+"/"+url.PathEscape(wish.ExternalID), &result); err != nil {
			return WishMetadata{}, err
		}
	}

	kind := "movie"
	metadata := WishMetadata{Title: result.Title, Overview: result.Overview, ReleaseDate: result.ReleaseDate, Rating: result.Rating}
	if wish.Type == "Series" {
		kind, metadata.Title, metadata.ReleaseDate = "tv", result.Name, result.FirstAir
	}
	if result.PosterPath != "" && strings.HasPrefix(result.PosterPath, "/") && !strings.Contains(result.PosterPath, "..") {
		metadata.PosterURL = "https://image.tmdb.org/t/p/w342" + result.PosterPath
	}
	if result.ID > 0 {
		var details struct {
			Genres []struct {
				Name string `json:"name"`
			} `json:"genres"`
			Runtime        int   `json:"runtime"`
			EpisodeRuntime []int `json:"episode_run_time"`
		}
		if err := requestJSON(fmt.Sprintf("%s/%d", kind, result.ID), &details); err == nil {
			for _, genre := range details.Genres {
				metadata.Genres = append(metadata.Genres, genre.Name)
			}
			metadata.Runtime = details.Runtime
			if metadata.Runtime == 0 && len(details.EpisodeRuntime) > 0 {
				metadata.Runtime = details.EpisodeRuntime[0]
			}
		}
	}
	if strings.TrimSpace(metadata.Title) == "" {
		return WishMetadata{}, fmt.Errorf("external title has no display name")
	}
	return metadata, nil
}

func (a *App) createWish(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title      string `json:"title"`
		Type       string `json:"type"`
		Source     string `json:"source"`
		ExternalID string `json:"externalId"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Type = strings.TrimSpace(in.Type)
	in.Source = strings.ToLower(strings.TrimSpace(in.Source))
	in.ExternalID = strings.TrimSpace(in.ExternalID)
	if len([]rune(in.Title)) < 1 || len([]rune(in.Title)) > 180 || (in.Type != "Movie" && in.Type != "Series") {
		fail(w, http.StatusBadRequest, "Enter a title (up to 180 characters) and choose Movie or Series")
		return
	}
	if in.Source == "" {
		in.Source = "manual"
	}
	ref, sourceURL, ok := normalizeWishReference(in.Source, in.ExternalID, in.Type)
	if !ok {
		fail(w, http.StatusBadRequest, "Enter a valid IMDb or TMDb ID or URL, or choose Manual")
		return
	}
	if in.Source == "manual" && in.ExternalID != "" {
		fail(w, http.StatusBadRequest, "Manual requests do not use an external ID")
		return
	}
	now := time.Now().UTC()
	requester := a.user(r)
	wish := Wish{ID: random(), Requester: requester, RequesterName: a.displayNameForUser(requester), Title: in.Title, Type: in.Type, Source: in.Source, ExternalID: ref, SourceURL: sourceURL, Status: "pending", CreatedAt: now, UpdatedAt: now}
	a.mu.Lock()
	if len(a.wishes) >= 5000 {
		a.mu.Unlock()
		fail(w, http.StatusServiceUnavailable, "Wish list is full")
		return
	}
	for _, existing := range a.wishes {
		if existing.Requester == wish.Requester && existing.Type == wish.Type && normalizedTitle(existing.Title) == normalizedTitle(wish.Title) && existing.Status != "rejected" {
			a.mu.Unlock()
			fail(w, http.StatusConflict, "You already requested this title")
			return
		}
	}
	next := append(append([]Wish(nil), a.wishes...), wish)
	if err := persist(a.data+"/wishes.json", next); err != nil {
		a.mu.Unlock()
		fail(w, http.StatusInternalServerError, "Could not save the request")
		return
	}
	a.wishes = next
	a.mu.Unlock()
	select {
	case a.notifications <- wish:
	default:
		log.Printf("vloader: email notification queue is full; request %s was saved without an email", wish.ID)
	}
	jsonOut(w, wish)
}

func (a *App) notificationWorker() {
	for wish := range a.notifications {
		if err := sendWishNotification(a.config(), a.origin, wish); err != nil {
			log.Printf("vloader: could not email admin about request %s: %v", wish.ID, err)
		}
	}
}

func sendWishNotification(cfg Config, appURL string, wish Wish) error {
	if cfg.AdminEmail == "" || cfg.SMTPHost == "" || cfg.SMTPPort == 0 || cfg.SMTPFrom == "" {
		return nil
	}
	title := strings.NewReplacer("\r", " ", "\n", " ").Replace(wish.Title)
	requester := wish.RequesterName
	if requester == "" {
		requester = wish.Requester
	}
	requester = strings.NewReplacer("\r", " ", "\n", " ").Replace(requester)
	body := fmt.Sprintf("A new vloader request was submitted.\r\n\r\nTitle: %s\r\nType: %s\r\nRequested by: %s\r\nSource: %s %s\r\n\r\nReview requests: %s\r\n", title, wish.Type, requester, wish.Source, wish.ExternalID, strings.TrimRight(appURL, "/")+"/")
	return sendSMTPMessage(cfg, cfg.AdminEmail, "[vloader] New "+wish.Type+" request", body)
}

func sendTestEmail(cfg Config) error {
	body := "This is a test email from vloader.\r\n\r\nYour SMTP settings are working.\r\n"
	return sendSMTPMessage(cfg, cfg.AdminEmail, "[vloader] SMTP test", body)
}

func sendSMTPMessage(cfg Config, recipient, subject, body string) error {
	host := net.JoinHostPort(cfg.SMTPHost, fmt.Sprint(cfg.SMTPPort))
	conn, err := net.DialTimeout("tcp", host, 8*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(12 * time.Second))
	c, err := smtp.NewClient(conn, cfg.SMTPHost)
	if err != nil {
		return err
	}
	defer c.Quit()
	if !cfg.SMTPDisableTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return fmt.Errorf("SMTP server does not support STARTTLS")
		}
		if err = c.StartTLS(&tls.Config{ServerName: cfg.SMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if cfg.SMTPUsername != "" {
		var auth smtp.Auth = smtp.PlainAuth("", cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPHost)
		if cfg.SMTPDisableTLS {
			auth = plainSMTPAuth{username: cfg.SMTPUsername, password: cfg.SMTPPassword}
		}
		if err = c.Auth(auth); err != nil {
			return err
		}
	}
	if err = c.Mail(cfg.SMTPFrom); err != nil {
		return err
	}
	if err = c.Rcpt(recipient); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s", cfg.SMTPFrom, recipient, subject, body)
	if err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

// plainSMTPAuth is only selected after an administrator explicitly disables TLS.
// SMTP AUTH PLAIN is then sent unencrypted and must be clearly disclosed in the UI.
type plainSMTPAuth struct{ username, password string }

func (a plainSMTPAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.username + "\x00" + a.password), nil
}

func (plainSMTPAuth) Next([]byte, bool) ([]byte, error) { return nil, nil }

func (a *App) updateWish(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status string `json:"status"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Status != "approved" && in.Status != "rejected" {
		fail(w, http.StatusBadRequest, "Status must be approved or rejected")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	next := append([]Wish(nil), a.wishes...)
	found := false
	for i := range next {
		if next[i].ID == r.PathValue("id") {
			if next[i].Status == "available" {
				fail(w, http.StatusConflict, "This title is already available")
				return
			}
			next[i].Status = in.Status
			next[i].UpdatedAt = time.Now().UTC()
			found = true
			break
		}
	}
	if !found {
		fail(w, http.StatusNotFound, "Request not found")
		return
	}
	if err := persist(a.data+"/wishes.json", next); err != nil {
		fail(w, http.StatusInternalServerError, "Could not update the request")
		return
	}
	a.wishes = next
	jsonOut(w, map[string]bool{"ok": true})
}

func normalizeWishReference(source, value, mediaType string) (string, string, bool) {
	if source == "manual" {
		return "", "", value == ""
	}
	if source != "imdb" && source != "tmdb" {
		return "", "", false
	}
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return "", "", false
		}
		host := strings.ToLower(u.Hostname())
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if source == "imdb" {
			if (host != "imdb.com" && !strings.HasSuffix(host, ".imdb.com")) || len(parts) < 2 || parts[0] != "title" {
				return "", "", false
			}
			value = parts[1]
		} else {
			if host != "themoviedb.org" && !strings.HasSuffix(host, ".themoviedb.org") {
				return "", "", false
			}
			want := "movie"
			if mediaType == "Series" {
				want = "tv"
			}
			if len(parts) < 2 || parts[0] != want {
				return "", "", false
			}
			value = parts[1]
		}
	}
	if source == "imdb" && imdbIDPattern.MatchString(value) {
		return value, "https://www.imdb.com/title/" + value + "/", true
	}
	if source == "tmdb" && tmdbIDPattern.MatchString(value) {
		kind := "movie"
		if mediaType == "Series" {
			kind = "tv"
		}
		return value, fmt.Sprintf("https://www.themoviedb.org/%s/%s", kind, value), true
	}
	return "", "", false
}

func normalizedTitle(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func wishMatches(wish Wish, item Item) bool {
	if item.Type != wish.Type {
		return false
	}
	if wish.ExternalID != "" {
		provider := "Imdb"
		if wish.Source == "tmdb" {
			provider = "Tmdb"
		}
		for key, id := range item.ProviderIDs {
			if strings.EqualFold(key, provider) && strings.EqualFold(id, wish.ExternalID) {
				return true
			}
		}
		return false
	}
	return normalizedTitle(wish.Title) != "" && normalizedTitle(wish.Title) == normalizedTitle(item.Name)
}

// resolveWishesLocked checks newly synchronized Emby items and marks matched wishes available.
// The caller must hold a.mu for writing.
func (a *App) resolveWishesLocked(catalog Catalog) bool {
	changed := false
	for i := range a.wishes {
		wish := &a.wishes[i]
		if wish.Status != "pending" && wish.Status != "approved" {
			continue
		}
		for _, item := range catalog.Items {
			if wishMatches(*wish, item) {
				wish.Status = "available"
				wish.ItemID = item.ID
				wish.UpdatedAt = catalog.Updated.UTC()
				changed = true
				break
			}
		}
	}
	return changed
}

func (a *App) persistWishesLocked() error {
	return persist(a.data+"/wishes.json", a.wishes)
}
