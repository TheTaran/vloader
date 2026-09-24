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
	BannerURL   string   `json:"bannerUrl,omitempty"`
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
			wish.RequesterEmail = ""
			wish.AvailabilityEmailSent = false
			items = append(items, wish)
		}
	}
	jsonOut(w, map[string]any{"items": items, "admin": admin, "metadataEnabled": a.cfg.TMDBAPIKey != "" || a.cfg.TVDBAPIKey != "", "tmdbEnabled": a.cfg.TMDBAPIKey != "", "tvdbEnabled": a.cfg.TVDBAPIKey != ""})
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
	cfg := a.config()
	if cfg.TMDBAPIKey == "" && cfg.TVDBAPIKey == "" {
		fail(w, http.StatusFailedDependency, "Configure a TMDb or TVDB API key in Settings, Metadata to load request artwork")
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
	metadata := WishMetadata{Title: wish.Title}
	if cfg.TMDBAPIKey != "" && (wish.Source == "imdb" || wish.Source == "tmdb") {
		tmdb, err := fetchWishMetadata(ctx, a.client, cfg.TMDBAPIKey, wish)
		if err == nil {
			metadata = tmdb
		} else {
			log.Printf("vloader: TMDb metadata lookup failed for request %s: %v", wish.ID, err)
		}
	}
	if cfg.TVDBAPIKey != "" {
		token, err := a.tvdbAccessToken(ctx, cfg)
		if err == nil {
			var banner string
			banner, err = fetchWishTVDBBannerWithToken(ctx, a.client, token, wish)
			if err == nil {
				metadata.BannerURL = banner
			}
		}
		if err != nil {
			log.Printf("vloader: TVDB banner lookup failed for request %s: %v", wish.ID, err)
		}
	}
	if metadata.Title == "" {
		metadata.Title = wish.Title
	}
	a.mu.Lock()
	a.wishMetadata[wish.ID] = wishMetadataCache{Metadata: metadata, Expires: time.Now().Add(6 * time.Hour)}
	a.mu.Unlock()
	jsonOut(w, metadata)
}

type tvdbArtwork struct {
	Image  string `json:"image"`
	Type   int    `json:"type"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type tvdbSearchRecord struct {
	ID       string `json:"id"`
	TVDBID   string `json:"tvdb_id"`
	Name     string `json:"name"`
	Title    string `json:"title"`
	ImageURL string `json:"image_url"`
	Overview string `json:"overview"`
	Type     string `json:"type"`
}

// fetchWishTVDBBanner uses the official TVDB v4 API. It deliberately returns
// only artwork URLs from TVDB's own image host, never a URL supplied by users.
func fetchWishTVDBBanner(ctx context.Context, baseClient *http.Client, apiKey, pin string, wish Wish) (string, error) {
	token, err := fetchTVDBAccessToken(ctx, baseClient, apiKey, pin)
	if err != nil {
		return "", err
	}
	return fetchWishTVDBBannerWithToken(ctx, baseClient, token, wish)
}

func (a *App) tvdbAccessToken(ctx context.Context, cfg Config) (string, error) {
	a.mu.RLock()
	if a.tvdbToken != "" && a.tvdbKey == cfg.TVDBAPIKey && a.tvdbPIN == cfg.TVDBPIN && time.Now().Before(a.tvdbExpires) {
		token := a.tvdbToken
		a.mu.RUnlock()
		return token, nil
	}
	a.mu.RUnlock()
	token, err := fetchTVDBAccessToken(ctx, a.client, cfg.TVDBAPIKey, cfg.TVDBPIN)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.cfg.TVDBAPIKey == cfg.TVDBAPIKey && a.cfg.TVDBPIN == cfg.TVDBPIN {
		a.tvdbToken, a.tvdbKey, a.tvdbPIN = token, cfg.TVDBAPIKey, cfg.TVDBPIN
		a.tvdbExpires = time.Now().Add(29 * 24 * time.Hour)
	}
	a.mu.Unlock()
	return token, nil
}

func fetchTVDBAccessToken(ctx context.Context, baseClient *http.Client, apiKey, pin string) (string, error) {
	client := *baseClient
	client.Timeout = 8 * time.Second
	login := map[string]string{"apikey": apiKey}
	if pin != "" {
		login["pin"] = pin
	}
	body, _ := json.Marshal(login)
	var auth struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := tvdbJSONRequest(ctx, &client, http.MethodPost, "/login", "", strings.NewReader(string(body)), &auth); err != nil {
		return "", err
	}
	if auth.Data.Token == "" {
		return "", fmt.Errorf("TVDB login response did not contain a token")
	}
	return auth.Data.Token, nil
}

func fetchWishTVDBBannerWithToken(ctx context.Context, baseClient *http.Client, token string, wish Wish) (string, error) {
	client := *baseClient
	client.Timeout = 8 * time.Second
	kind := "series"
	if wish.Type == "Movie" {
		kind = "movie"
	}
	query := url.Values{}
	query.Set("type", kind)
	if wish.Source == "imdb" && imdbIDPattern.MatchString(wish.ExternalID) {
		query.Set("remote_id", wish.ExternalID)
	} else {
		query.Set("query", wish.Title)
	}
	var search struct {
		Data []tvdbSearchRecord `json:"data"`
	}
	if err := tvdbJSONRequest(ctx, &client, http.MethodGet, "/search?"+query.Encode(), token, nil, &search); err != nil {
		return "", err
	}
	var record *tvdbSearchRecord
	for i := range search.Data {
		candidate := &search.Data[i]
		if strings.EqualFold(candidate.Type, kind) || candidate.Type == "" {
			name := candidate.Name
			if name == "" {
				name = candidate.Title
			}
			if normalizedTitle(name) == normalizedTitle(wish.Title) {
				record = candidate
				break
			}
			if record == nil {
				record = candidate
			}
		}
	}
	if record == nil {
		return "", nil
	}
	id := record.TVDBID
	if id == "" {
		id = record.ID
	}
	id = strings.TrimPrefix(id, kind+"-")
	if id == "" || strings.Trim(id, "0123456789") != "" {
		return "", nil
	}
	var details struct {
		Data struct {
			Artworks []tvdbArtwork `json:"artworks"`
		} `json:"data"`
	}
	endpoint := "/series/" + id + "/extended"
	if kind == "movie" {
		endpoint = "/movies/" + id + "/extended"
	}
	if err := tvdbJSONRequest(ctx, &client, http.MethodGet, endpoint, token, nil, &details); err != nil {
		return "", err
	}
	return selectTVDBBanner(details.Data.Artworks), nil
}

func tvdbJSONRequest(ctx context.Context, client *http.Client, method, endpoint, token string, body io.Reader, target any) error {
	req, err := http.NewRequestWithContext(ctx, method, "https://api4.thetvdb.com/v4"+endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("TVDB returned HTTP %d", res.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(target); err != nil {
		return fmt.Errorf("invalid TVDB response")
	}
	return nil
}

func selectTVDBBanner(artworks []tvdbArtwork) string {
	bestURL, bestScore := "", 0.0
	for _, artwork := range artworks {
		image := safeTVDBImageURL(artwork.Image)
		if image == "" {
			continue
		}
		ratio := float64(artwork.Width) / float64(max(artwork.Height, 1))
		if ratio < 1.3 {
			continue
		}
		score := ratio
		if ratio >= 1.3 && ratio <= 2.2 {
			score += 10
		} // prefer a wide fanart/background over a very thin logo strip
		if score > bestScore {
			bestURL, bestScore = image, score
		}
	}
	return bestURL
}

func safeTVDBImageURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || !(u.Host == "thetvdb.com" || strings.HasSuffix(u.Host, ".thetvdb.com")) {
		return ""
	}
	return u.String()
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
	wish := Wish{ID: random(), Requester: requester, RequesterName: a.displayNameForUser(requester), RequesterEmail: a.emailForUser(requester), Title: in.Title, Type: in.Type, Source: in.Source, ExternalID: ref, SourceURL: sourceURL, Status: "pending", CreatedAt: now, UpdatedAt: now}
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
	wish.RequesterEmail = ""
	jsonOut(w, wish)
}

func (a *App) notificationWorker() {
	for wish := range a.notifications {
		if wish.Status == "available" {
			err := sendWishAvailableNotification(a.config(), a.origin, wish)
			if err != nil {
				log.Printf("vloader: could not email requester about available request %s: %s", wish.ID, redactRequesterEmail(err.Error(), wish.RequesterEmail))
			}
			a.finishAvailabilityNotice(wish.ID, err == nil)
			continue
		}
		if err := sendWishNotification(a.config(), a.origin, wish); err != nil {
			log.Printf("vloader: could not email admin about request %s: %v", wish.ID, err)
		}
	}
}

func redactRequesterEmail(message, email string) string {
	if email == "" {
		return message
	}
	return strings.ReplaceAll(message, email, "[email redacted]")
}

func (a *App) enqueueAvailabilityNotice(wish Wish) {
	select {
	case a.notifications <- wish:
	default:
		a.mu.Lock()
		delete(a.availabilityEmailQueued, wish.ID)
		a.mu.Unlock()
		log.Printf("vloader: email notification queue is full; availability notice for request %s was not queued", wish.ID)
	}
}

func (a *App) finishAvailabilityNotice(id string, sent bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.availabilityEmailQueued, id)
	if !sent {
		return
	}
	for i := range a.wishes {
		if a.wishes[i].ID != id || a.wishes[i].AvailabilityEmailSent {
			continue
		}
		a.wishes[i].AvailabilityEmailSent = true
		if err := a.persistWishesLocked(); err != nil {
			a.wishes[i].AvailabilityEmailSent = false
			log.Printf("vloader: could not save availability email status for request %s: %v", id, err)
		}
		return
	}
}

func sendWishAvailableNotification(cfg Config, appURL string, wish Wish) error {
	if !validEmail(wish.RequesterEmail) {
		return fmt.Errorf("request has no verified requester email address")
	}
	if cfg.SMTPHost == "" || cfg.SMTPPort == 0 || cfg.SMTPFrom == "" || (cfg.SMTPUsername != "" && cfg.SMTPPassword == "") {
		return fmt.Errorf("SMTP notification settings are incomplete")
	}
	title := strings.NewReplacer("\r", " ", "\n", " ").Replace(wish.Title)
	body := fmt.Sprintf("Your requested title is now available in Emby.\r\n\r\nTitle: %s\r\nType: %s\r\n\r\nOpen your vloader library: %s\r\n", title, wish.Type, strings.TrimRight(appURL, "/")+"/")
	return sendSMTPMessage(cfg, wish.RequesterEmail, "[vloader] Now available: "+title, body)
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
func (a *App) resolveWishesLocked(catalog Catalog) ([]Wish, bool) {
	var notices []Wish
	changed := false
	for i := range a.wishes {
		wish := &a.wishes[i]
		if wish.Status == "pending" || wish.Status == "approved" {
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
		if wish.Status == "available" && !wish.AvailabilityEmailSent && validEmail(wish.RequesterEmail) && !a.availabilityEmailQueued[wish.ID] {
			a.availabilityEmailQueued[wish.ID] = true
			notices = append(notices, *wish)
		}
	}
	return notices, changed
}

func (a *App) persistWishesLocked() error {
	return persist(a.data+"/wishes.json", a.wishes)
}
