package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type arrMovieLookup struct {
	Title     string     `json:"title"`
	TMDBID    int        `json:"tmdbId"`
	IMDBID    string     `json:"imdbId"`
	Year      int        `json:"year"`
	Overview  string     `json:"overview"`
	Genres    []string   `json:"genres"`
	InCinemas string     `json:"inCinemas"`
	Images    []arrImage `json:"images"`
}

type arrSeriesLookup struct {
	Title      string     `json:"title"`
	TVDBID     int        `json:"tvdbId"`
	IMDBID     string     `json:"imdbId"`
	Year       int        `json:"year"`
	Overview   string     `json:"overview"`
	Genres     []string   `json:"genres"`
	FirstAired string     `json:"firstAired"`
	Images     []arrImage `json:"images"`
}

type arrImage struct {
	CoverType string `json:"coverType"`
	RemoteURL string `json:"remoteUrl"`
}

type arrSearchResult struct {
	Title      string `json:"title"`
	Type       string `json:"type"`
	Year       int    `json:"year,omitempty"`
	Overview   string `json:"overview,omitempty"`
	Source     string `json:"source"`
	ExternalID string `json:"externalId"`
	ImageURL   string `json:"imageUrl,omitempty"`
}

type arrRootFolder struct {
	Path string `json:"path"`
}

type arrProfile struct {
	ID int `json:"id"`
}

type arrDefaults struct {
	RootFolder string
	QualityID  int
	LanguageID int
}

func boundedSearchText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return value
}

func safeArrImageURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host != "image.tmdb.org" && host != "artworks.thetvdb.com" {
		return ""
	}
	return u.String()
}

func selectArrPoster(images []arrImage) string {
	for _, kind := range []string{"poster", "banner", "fanart"} {
		for _, image := range images {
			if strings.EqualFold(image.CoverType, kind) {
				if safe := safeArrImageURL(image.RemoteURL); safe != "" {
					return "/api/request-image?url=" + url.QueryEscape(safe)
				}
			}
		}
	}
	return ""
}

func arrEndpoint(base, endpoint string) string {
	return strings.TrimRight(base, "/") + "/api/v3/" + strings.TrimLeft(endpoint, "/")
}

func (a *App) arrRequest(ctx context.Context, base, apiKey, method, endpoint string, body any, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, arrEndpoint(base, endpoint), reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-Api-Key", apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := *a.client
	client.Timeout = 15 * time.Second
	res, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("connection failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return res.StatusCode, fmt.Errorf("API returned HTTP %d", res.StatusCode)
	}
	if out == nil {
		return res.StatusCode, nil
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(out); err != nil {
		return res.StatusCode, fmt.Errorf("invalid API response: %w", err)
	}
	return res.StatusCode, nil
}

func arrLookupTerm(wish Wish) string {
	if wish.ExternalID != "" && (wish.Source == "imdb" || wish.Source == "tmdb" || wish.Source == "tvdb") {
		return wish.Source + ":" + wish.ExternalID
	}
	return wish.Title
}

func (a *App) fetchArrWishMetadata(ctx context.Context, cfg Config, wish Wish) (WishMetadata, error) {
	query := url.Values{"term": {arrLookupTerm(wish)}}
	if wish.Type == "Movie" {
		var matches []arrMovieLookup
		if _, err := a.arrRequest(ctx, cfg.RadarrURL, cfg.RadarrAPIKey, http.MethodGet, "movie/lookup?"+query.Encode(), nil, &matches); err != nil {
			return WishMetadata{}, err
		}
		for _, match := range matches {
			if wish.ExternalID == "" || fmt.Sprint(match.TMDBID) == wish.ExternalID {
				return WishMetadata{Title: match.Title, Overview: boundedSearchText(match.Overview, 2000), PosterURL: selectArrPoster(match.Images), ReleaseDate: match.InCinemas, Genres: match.Genres}, nil
			}
		}
	} else if wish.Type == "Series" {
		var matches []arrSeriesLookup
		if _, err := a.arrRequest(ctx, cfg.SonarrURL, cfg.SonarrAPIKey, http.MethodGet, "series/lookup?"+query.Encode(), nil, &matches); err != nil {
			return WishMetadata{}, err
		}
		for _, match := range matches {
			if wish.ExternalID == "" || fmt.Sprint(match.TVDBID) == wish.ExternalID {
				return WishMetadata{Title: match.Title, Overview: boundedSearchText(match.Overview, 2000), PosterURL: selectArrPoster(match.Images), ReleaseDate: match.FirstAired, Genres: match.Genres}, nil
			}
		}
	}
	return WishMetadata{}, fmt.Errorf("Arr did not find request metadata")
}

func (a *App) searchWishes(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	mediaType := strings.TrimSpace(r.URL.Query().Get("type"))
	if len([]rune(query)) < 2 || len([]rune(query)) > 100 || (mediaType != "Movie" && mediaType != "Series") {
		fail(w, http.StatusBadRequest, "Enter 2 to 100 characters and choose Movie or Series")
		return
	}
	cfg := a.config()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	values := url.Values{"term": {query}}
	results := make([]arrSearchResult, 0, 20)
	if mediaType == "Movie" {
		if cfg.RadarrURL == "" || cfg.RadarrAPIKey == "" {
			fail(w, http.StatusFailedDependency, "Radarr search is not configured")
			return
		}
		var matches []arrMovieLookup
		if _, err := a.arrRequest(ctx, cfg.RadarrURL, cfg.RadarrAPIKey, http.MethodGet, "movie/lookup?"+values.Encode(), nil, &matches); err != nil {
			log.Printf("vloader: Radarr request search failed: %v", err)
			fail(w, http.StatusBadGateway, "Radarr search failed")
			return
		}
		for _, match := range matches {
			title := strings.TrimSpace(match.Title)
			if match.TMDBID < 1 || title == "" || len([]rune(title)) > 180 {
				continue
			}
			results = append(results, arrSearchResult{Title: title, Type: "Movie", Year: match.Year, Overview: boundedSearchText(match.Overview, 500), Source: "tmdb", ExternalID: fmt.Sprint(match.TMDBID), ImageURL: selectArrPoster(match.Images)})
			if len(results) == 20 {
				break
			}
		}
	} else {
		if cfg.SonarrURL == "" || cfg.SonarrAPIKey == "" {
			fail(w, http.StatusFailedDependency, "Sonarr search is not configured")
			return
		}
		var matches []arrSeriesLookup
		if _, err := a.arrRequest(ctx, cfg.SonarrURL, cfg.SonarrAPIKey, http.MethodGet, "series/lookup?"+values.Encode(), nil, &matches); err != nil {
			log.Printf("vloader: Sonarr request search failed: %v", err)
			fail(w, http.StatusBadGateway, "Sonarr search failed")
			return
		}
		for _, match := range matches {
			title := strings.TrimSpace(match.Title)
			if match.TVDBID < 1 || title == "" || len([]rune(title)) > 180 {
				continue
			}
			results = append(results, arrSearchResult{Title: title, Type: "Series", Year: match.Year, Overview: boundedSearchText(match.Overview, 500), Source: "tvdb", ExternalID: fmt.Sprint(match.TVDBID), ImageURL: selectArrPoster(match.Images)})
			if len(results) == 20 {
				break
			}
		}
	}
	jsonOut(w, map[string]any{"items": results})
}

func (a *App) requestImage(w http.ResponseWriter, r *http.Request) {
	remote := safeArrImageURL(r.URL.Query().Get("url"))
	if remote == "" {
		fail(w, http.StatusBadRequest, "Invalid request image")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remote, nil)
	if err != nil {
		fail(w, http.StatusBadRequest, "Invalid request image")
		return
	}
	client := *a.client
	client.Timeout = 15 * time.Second
	res, err := client.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		if res != nil {
			res.Body.Close()
		}
		fail(w, http.StatusBadGateway, "Request image unavailable")
		return
	}
	defer res.Body.Close()
	contentType := res.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		fail(w, http.StatusBadGateway, "Invalid request image response")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = io.Copy(w, io.LimitReader(res.Body, 10<<20))
}

func (a *App) automationRoots(w http.ResponseWriter, r *http.Request) {
	cfg := a.config()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	result := map[string][]string{"Movie": {}, "Series": {}}
	for mediaType, service := range map[string]struct{ base, key string }{
		"Movie": {cfg.RadarrURL, cfg.RadarrAPIKey}, "Series": {cfg.SonarrURL, cfg.SonarrAPIKey},
	} {
		if service.base == "" || service.key == "" {
			continue
		}
		var roots []arrRootFolder
		if _, err := a.arrRequest(ctx, service.base, service.key, http.MethodGet, "rootfolder", nil, &roots); err != nil {
			log.Printf("vloader: %s root folder lookup failed: %v", mediaType, err)
			fail(w, http.StatusBadGateway, "Could not load destination folders")
			return
		}
		seen := map[string]bool{}
		for _, root := range roots {
			folder := strings.TrimSpace(root.Path)
			if folder != "" && !seen[folder] {
				result[mediaType] = append(result[mediaType], folder)
				seen[folder] = true
			}
		}
	}
	jsonOut(w, result)
}

func (a *App) dispatchWish(ctx context.Context, wish Wish, rootFolder string) error {
	cfg := a.config()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	switch wish.Type {
	case "Movie":
		if cfg.RadarrURL == "" {
			return fmt.Errorf("Radarr is not configured")
		}
		return a.dispatchRadarr(ctx, cfg, wish, rootFolder)
	case "Series":
		if cfg.SonarrURL == "" {
			return fmt.Errorf("Sonarr is not configured")
		}
		return a.dispatchSonarr(ctx, cfg, wish, rootFolder)
	default:
		return fmt.Errorf("unsupported request type %q", wish.Type)
	}
}

func (a *App) dispatchRadarr(ctx context.Context, cfg Config, wish Wish, rootFolder string) error {
	var matches []arrMovieLookup
	query := url.Values{"term": {arrLookupTerm(wish)}}
	if _, err := a.arrRequest(ctx, cfg.RadarrURL, cfg.RadarrAPIKey, http.MethodGet, "movie/lookup?"+query.Encode(), nil, &matches); err != nil {
		return fmt.Errorf("Radarr lookup failed: %w", err)
	}
	var selected *arrMovieLookup
	for i := range matches {
		candidate := &matches[i]
		if wish.ExternalID != "" {
			matchesID := wish.Source == "imdb" && strings.EqualFold(candidate.IMDBID, wish.ExternalID) || wish.Source != "imdb" && fmt.Sprint(candidate.TMDBID) == wish.ExternalID
			if matchesID {
				selected = candidate
				break
			}
			continue
		}
		if normalizedTitle(candidate.Title) == normalizedTitle(wish.Title) {
			selected = candidate
			break
		}
		if selected == nil {
			selected = candidate
		}
	}
	if selected == nil || selected.TMDBID < 1 {
		return fmt.Errorf("Radarr did not find a matching movie")
	}
	defaults, err := a.resolveRadarrDefaults(ctx, cfg, rootFolder)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"title": selected.Title, "tmdbId": selected.TMDBID, "year": selected.Year,
		"qualityProfileId": defaults.QualityID, "rootFolderPath": defaults.RootFolder,
		"monitored": true, "addOptions": map[string]any{"searchForMovie": true},
	}
	status, err := a.arrRequest(ctx, cfg.RadarrURL, cfg.RadarrAPIKey, http.MethodPost, "movie", payload, nil)
	if err != nil && status != http.StatusConflict {
		return fmt.Errorf("Radarr add failed: %w", err)
	}
	return nil
}

func (a *App) dispatchSonarr(ctx context.Context, cfg Config, wish Wish, rootFolder string) error {
	var matches []arrSeriesLookup
	query := url.Values{"term": {arrLookupTerm(wish)}}
	if _, err := a.arrRequest(ctx, cfg.SonarrURL, cfg.SonarrAPIKey, http.MethodGet, "series/lookup?"+query.Encode(), nil, &matches); err != nil {
		return fmt.Errorf("Sonarr lookup failed: %w", err)
	}
	var selected *arrSeriesLookup
	for i := range matches {
		candidate := &matches[i]
		if wish.ExternalID != "" {
			matchesID := wish.Source == "imdb" && strings.EqualFold(candidate.IMDBID, wish.ExternalID) || wish.Source != "imdb" && fmt.Sprint(candidate.TVDBID) == wish.ExternalID
			if matchesID {
				selected = candidate
				break
			}
			continue
		}
		if normalizedTitle(candidate.Title) == normalizedTitle(wish.Title) {
			selected = candidate
			break
		}
		if selected == nil {
			selected = candidate
		}
	}
	if selected == nil || selected.TVDBID < 1 {
		return fmt.Errorf("Sonarr did not find a matching series")
	}
	defaults, err := a.resolveSonarrDefaults(ctx, cfg, rootFolder)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"title": selected.Title, "tvdbId": selected.TVDBID, "year": selected.Year,
		"qualityProfileId": defaults.QualityID,
		"rootFolderPath":   defaults.RootFolder, "monitored": true, "seasonFolder": true,
		"seriesType": "standard", "addOptions": map[string]any{"monitor": "all", "searchForMissingEpisodes": true},
	}
	if defaults.LanguageID > 0 {
		payload["languageProfileId"] = defaults.LanguageID
	}
	status, err := a.arrRequest(ctx, cfg.SonarrURL, cfg.SonarrAPIKey, http.MethodPost, "series", payload, nil)
	if err != nil && status != http.StatusConflict {
		return fmt.Errorf("Sonarr add failed: %w", err)
	}
	return nil
}

func selectRootFolder(roots []arrRootFolder, selected string) (string, bool) {
	selected = strings.TrimSpace(selected)
	if selected == "" {
		return "", false
	}
	for _, root := range roots {
		if strings.TrimSpace(root.Path) == selected {
			return selected, true
		}
	}
	return "", false
}

func (a *App) resolveRadarrDefaults(ctx context.Context, cfg Config, selectedRoot string) (arrDefaults, error) {
	var roots []arrRootFolder
	if _, err := a.arrRequest(ctx, cfg.RadarrURL, cfg.RadarrAPIKey, http.MethodGet, "rootfolder", nil, &roots); err != nil {
		return arrDefaults{}, fmt.Errorf("Radarr has no usable root folder")
	}
	rootFolder, ok := selectRootFolder(roots, selectedRoot)
	if !ok {
		return arrDefaults{}, fmt.Errorf("select a valid Radarr root folder")
	}
	var profiles []arrProfile
	if _, err := a.arrRequest(ctx, cfg.RadarrURL, cfg.RadarrAPIKey, http.MethodGet, "qualityprofile", nil, &profiles); err != nil || len(profiles) == 0 || profiles[0].ID < 1 {
		return arrDefaults{}, fmt.Errorf("Radarr has no usable quality profile")
	}
	return arrDefaults{RootFolder: rootFolder, QualityID: profiles[0].ID}, nil
}

func (a *App) resolveSonarrDefaults(ctx context.Context, cfg Config, selectedRoot string) (arrDefaults, error) {
	var roots []arrRootFolder
	if _, err := a.arrRequest(ctx, cfg.SonarrURL, cfg.SonarrAPIKey, http.MethodGet, "rootfolder", nil, &roots); err != nil {
		return arrDefaults{}, fmt.Errorf("Sonarr has no usable root folder")
	}
	rootFolder, ok := selectRootFolder(roots, selectedRoot)
	if !ok {
		return arrDefaults{}, fmt.Errorf("select a valid Sonarr root folder")
	}
	var profiles []arrProfile
	if _, err := a.arrRequest(ctx, cfg.SonarrURL, cfg.SonarrAPIKey, http.MethodGet, "qualityprofile", nil, &profiles); err != nil || len(profiles) == 0 || profiles[0].ID < 1 {
		return arrDefaults{}, fmt.Errorf("Sonarr has no usable quality profile")
	}
	defaults := arrDefaults{RootFolder: rootFolder, QualityID: profiles[0].ID}
	var languages []arrProfile
	status, err := a.arrRequest(ctx, cfg.SonarrURL, cfg.SonarrAPIKey, http.MethodGet, "languageprofile", nil, &languages)
	if err == nil && len(languages) > 0 && languages[0].ID > 0 {
		defaults.LanguageID = languages[0].ID
	} else if err != nil && status != http.StatusNotFound {
		return arrDefaults{}, fmt.Errorf("Sonarr language profile lookup failed: %w", err)
	}
	return defaults, nil
}
