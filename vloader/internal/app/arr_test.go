package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDispatchWishToRadarr(t *testing.T) {
	a := testApp(t)
	var added map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "radarr-secret" {
			t.Errorf("missing Radarr API key")
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie/lookup" {
			if r.URL.Query().Get("term") != "tmdb:123" {
				t.Errorf("unexpected lookup term %q", r.URL.Query().Get("term"))
			}
			json.NewEncoder(w).Encode([]arrMovieLookup{{Title: "Example Movie", TMDBID: 123, Year: 2025}})
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v3/rootfolder" {
			json.NewEncoder(w).Encode([]arrRootFolder{{Path: "/media/movies"}})
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v3/qualityprofile" {
			json.NewEncoder(w).Encode([]arrProfile{{ID: 4}})
			return
		}
		if r.URL.Path != "/api/v3/movie" {
			t.Fatalf("unexpected Radarr add path %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&added)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	a.cfg.RadarrURL, a.cfg.RadarrAPIKey = server.URL, "radarr-secret"
	if err := a.dispatchWish(t.Context(), Wish{Title: "Example Movie", Type: "Movie", Source: "tmdb", ExternalID: "123"}); err != nil {
		t.Fatal(err)
	}
	if added["tmdbId"] != float64(123) || added["qualityProfileId"] != float64(4) || added["rootFolderPath"] != "/media/movies" {
		t.Fatalf("unexpected Radarr payload: %#v", added)
	}
}

func TestDispatchWishToSonarr(t *testing.T) {
	a := testApp(t)
	var added map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "sonarr-secret" {
			t.Errorf("missing Sonarr API key")
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v3/series/lookup" {
			if r.URL.Query().Get("term") != "imdb:tt1234567" {
				t.Fatalf("unexpected Sonarr lookup: %s?%s", r.URL.Path, r.URL.RawQuery)
			}
			json.NewEncoder(w).Encode([]arrSeriesLookup{{Title: "Example Series", TVDBID: 456, IMDBID: "tt1234567", Year: 2024}})
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v3/rootfolder" {
			json.NewEncoder(w).Encode([]arrRootFolder{{Path: "/media/series"}})
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v3/qualityprofile" {
			json.NewEncoder(w).Encode([]arrProfile{{ID: 2}})
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v3/languageprofile" {
			json.NewEncoder(w).Encode([]arrProfile{{ID: 1}})
			return
		}
		if r.URL.Path != "/api/v3/series" {
			t.Fatalf("unexpected Sonarr add path %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&added)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	a.cfg.SonarrURL, a.cfg.SonarrAPIKey = server.URL, "sonarr-secret"
	if err := a.dispatchWish(t.Context(), Wish{Title: "Example Series", Type: "Series", Source: "imdb", ExternalID: "tt1234567"}); err != nil {
		t.Fatal(err)
	}
	if added["tvdbId"] != float64(456) || added["qualityProfileId"] != float64(2) || added["languageProfileId"] != float64(1) || added["rootFolderPath"] != "/media/series" {
		t.Fatalf("unexpected Sonarr payload: %#v", added)
	}
}

func TestUpdateWishRequiresAutomationBeforeApproval(t *testing.T) {
	a := testApp(t)
	a.sessions["admin-session"] = session{User: "admin", Expiry: time.Now().Add(time.Hour)}
	a.wishes = []Wish{{ID: "wish-1", Title: "Example", Type: "Movie", Status: "pending"}}
	w := request(a, http.MethodPost, "/api/wishes/wish-1", `{"status":"approved"}`, a.origin, "admin-session")
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "automation settings") {
		t.Fatalf("approval without Radarr should fail clearly: %d %s", w.Code, w.Body.String())
	}
	if a.wishes[0].Status != "pending" {
		t.Fatalf("failed automation changed request status: %+v", a.wishes[0])
	}
}

func TestSaveAutomationSettingsValidatesAndPersists(t *testing.T) {
	a := testApp(t)
	a.sessions["admin-session"] = session{User: "admin", Expiry: time.Now().Add(time.Hour)}
	// Older cached web clients still submit the now hidden profile fields. Keep
	// accepting them while choosing the service defaults during approval.
	body := `{"RadarrURL":"https://radarr.example.test","RadarrAPIKey":"radarr-key","RadarrRootFolder":"/old/movies","RadarrQualityID":7,"SonarrURL":"https://sonarr.example.test","SonarrAPIKey":"sonarr-key","SonarrRootFolder":"/old/series","SonarrQualityID":8,"SonarrLanguageID":9}`
	if w := request(a, http.MethodPost, "/api/settings/automation", body, a.origin, "admin-session"); w.Code != http.StatusOK {
		t.Fatalf("save automation settings: %d %s", w.Code, w.Body.String())
	}
	if a.cfg.RadarrURL == "" || a.cfg.SonarrURL == "" {
		t.Fatalf("automation settings were not applied: %+v", a.cfg)
	}
	settings := request(a, http.MethodGet, "/api/settings", "", "", "admin-session")
	if !strings.Contains(settings.Body.String(), `"HasRadarrAPIKey":true`) || strings.Contains(settings.Body.String(), "radarr-key") {
		t.Fatalf("settings response leaked or omitted Radarr key: %s", settings.Body.String())
	}
}

func TestRemoveLegacyAutomationAndMetadataSettings(t *testing.T) {
	file := filepath.Join(t.TempDir(), "settings.json")
	legacy := `{"EmbyURL":"https://emby.example.test","RadarrURL":"https://radarr.example.test","RadarrAPIKey":"radarr-key","RadarrRootFolder":"/movies","RadarrQualityID":7,"SonarrURL":"https://sonarr.example.test","SonarrAPIKey":"sonarr-key","SonarrRootFolder":"/series","SonarrQualityID":8,"SonarrLanguageID":9,"TMDBAPIKey":"tmdb-key","TVDBAPIKey":"tvdb-key","TVDBPIN":"tvdb-pin"}`
	if err := os.WriteFile(file, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := loadState(file, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := removeLegacySettings(file, cfg); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, obsolete := range []string{"RadarrRootFolder", "RadarrQualityID", "SonarrRootFolder", "SonarrQualityID", "SonarrLanguageID", "TMDBAPIKey", "TVDBAPIKey", "TVDBPIN"} {
		if bytes.Contains(b, []byte(obsolete)) {
			t.Fatalf("obsolete setting %s was not removed: %s", obsolete, b)
		}
	}
	if !bytes.Contains(b, []byte(`"RadarrAPIKey":"radarr-key"`)) || !bytes.Contains(b, []byte(`"SonarrAPIKey":"sonarr-key"`)) {
		t.Fatalf("active automation settings were not preserved: %s", b)
	}
}

func TestDispatchWishUsesFirstRadarrDefaults(t *testing.T) {
	a := testApp(t)
	var added map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v3/movie/lookup":
			json.NewEncoder(w).Encode([]arrMovieLookup{{Title: "Default Movie", TMDBID: 321, Year: 2026}})
		case "GET /api/v3/rootfolder":
			json.NewEncoder(w).Encode([]arrRootFolder{{Path: "/radarr/movies"}})
		case "GET /api/v3/qualityprofile":
			json.NewEncoder(w).Encode([]arrProfile{{ID: 7}})
		case "POST /api/v3/movie":
			json.NewDecoder(r.Body).Decode(&added)
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("unexpected Radarr request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	a.cfg.RadarrURL, a.cfg.RadarrAPIKey = server.URL, "radarr-secret"
	if err := a.dispatchWish(t.Context(), Wish{Title: "Default Movie", Type: "Movie", Source: "tmdb", ExternalID: "321"}); err != nil {
		t.Fatal(err)
	}
	if added["rootFolderPath"] != "/radarr/movies" || added["qualityProfileId"] != float64(7) {
		t.Fatalf("Radarr defaults were not used: %#v", added)
	}
}

func TestDispatchWishUsesFirstSonarrDefaults(t *testing.T) {
	a := testApp(t)
	var added map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v3/series/lookup":
			json.NewEncoder(w).Encode([]arrSeriesLookup{{Title: "Default Series", TVDBID: 654, Year: 2026}})
		case "GET /api/v3/rootfolder":
			json.NewEncoder(w).Encode([]arrRootFolder{{Path: "/sonarr/series"}})
		case "GET /api/v3/qualityprofile":
			json.NewEncoder(w).Encode([]arrProfile{{ID: 8}})
		case "GET /api/v3/languageprofile":
			json.NewEncoder(w).Encode([]arrProfile{{ID: 2}})
		case "POST /api/v3/series":
			json.NewDecoder(r.Body).Decode(&added)
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("unexpected Sonarr request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	a.cfg.SonarrURL, a.cfg.SonarrAPIKey = server.URL, "sonarr-secret"
	if err := a.dispatchWish(t.Context(), Wish{Title: "Default Series", Type: "Series", Source: "tvdb", ExternalID: "654"}); err != nil {
		t.Fatal(err)
	}
	if added["rootFolderPath"] != "/sonarr/series" || added["qualityProfileId"] != float64(8) || added["languageProfileId"] != float64(2) {
		t.Fatalf("Sonarr defaults were not used: %#v", added)
	}
}

func TestUserSearchesRadarrAndSonarrWithoutAddingTitles(t *testing.T) {
	var posts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			posts++
			w.WriteHeader(http.StatusCreated)
			return
		}
		switch r.URL.Path {
		case "/api/v3/movie/lookup":
			json.NewEncoder(w).Encode([]arrMovieLookup{{Title: "Wanted Movie", TMDBID: 123, Year: 2026, Overview: "Movie overview", Images: []arrImage{{CoverType: "poster", RemoteURL: "https://image.tmdb.org/t/p/original/poster.jpg"}, {CoverType: "fanart", RemoteURL: "https://image.tmdb.org/t/p/original/banner.jpg"}}}})
		case "/api/v3/series/lookup":
			json.NewEncoder(w).Encode([]arrSeriesLookup{{Title: "Wanted Series", TVDBID: 456, Year: 2025, Overview: "Series overview", Images: []arrImage{{CoverType: "banner", RemoteURL: "https://artworks.thetvdb.com/banners/series.jpg"}}}})
		default:
			t.Fatalf("unexpected search path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	a := testApp(t)
	a.sessions["user-session"] = session{User: "viewer", Expiry: time.Now().Add(time.Hour)}
	a.cfg.RadarrURL, a.cfg.RadarrAPIKey = server.URL, "radarr-secret"
	a.cfg.SonarrURL, a.cfg.SonarrAPIKey = server.URL, "sonarr-secret"

	movie := request(a, http.MethodGet, "/api/wishes/search?type=Movie&q=Wanted", "", "", "user-session")
	if movie.Code != http.StatusOK || !strings.Contains(movie.Body.String(), `"source":"tmdb"`) || !strings.Contains(movie.Body.String(), `"externalId":"123"`) || !strings.Contains(movie.Body.String(), `image.tmdb.org`) || !strings.Contains(movie.Body.String(), `poster.jpg`) {
		t.Fatalf("unexpected Radarr search response: %d %s", movie.Code, movie.Body.String())
	}
	series := request(a, http.MethodGet, "/api/wishes/search?type=Series&q=Wanted", "", "", "user-session")
	if series.Code != http.StatusOK || !strings.Contains(series.Body.String(), `"source":"tvdb"`) || !strings.Contains(series.Body.String(), `"externalId":"456"`) || !strings.Contains(series.Body.String(), `artworks.thetvdb.com`) {
		t.Fatalf("unexpected Sonarr search response: %d %s", series.Code, series.Body.String())
	}
	if posts != 0 || len(a.wishes) != 0 {
		t.Fatalf("search added a title before approval: posts=%d wishes=%d", posts, len(a.wishes))
	}
}

func TestSelectArrPosterPrefersPosterAndRejectsUntrustedHosts(t *testing.T) {
	images := []arrImage{
		{CoverType: "banner", RemoteURL: "https://evil.example/banner.jpg"},
		{CoverType: "fanart", RemoteURL: "https://image.tmdb.org/t/p/original/fanart.jpg"},
		{CoverType: "poster", RemoteURL: "https://image.tmdb.org/t/p/original/poster.jpg"},
	}
	got := selectArrPoster(images)
	if !strings.HasPrefix(got, "/api/request-image?url=") || !strings.Contains(got, "poster.jpg") {
		t.Fatalf("safe poster was not selected: %q", got)
	}
	if safeArrImageURL("http://image.tmdb.org/t/p/original/poster.jpg") != "" || safeArrImageURL("https://evil.example/banner.jpg") != "" {
		t.Fatal("unsafe request image URL accepted")
	}
}

func TestRequestSearchRequiresConfiguredTool(t *testing.T) {
	a := testApp(t)
	a.sessions["user-session"] = session{User: "viewer", Expiry: time.Now().Add(time.Hour)}
	w := request(a, http.MethodGet, "/api/wishes/search?type=Movie&q=Example", "", "", "user-session")
	if w.Code != http.StatusFailedDependency || !strings.Contains(w.Body.String(), "Radarr") {
		t.Fatalf("unconfigured Radarr search response: %d %s", w.Code, w.Body.String())
	}
}

func TestRequestMetadataComesFromArrLookup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/movie/lookup" || r.URL.Query().Get("term") != "tmdb:123" {
			t.Fatalf("unexpected metadata request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		json.NewEncoder(w).Encode([]arrMovieLookup{{Title: "Arr Movie", TMDBID: 123, Overview: "From Radarr", Genres: []string{"Drama"}, InCinemas: "2026-02-03"}})
	}))
	defer server.Close()
	a := testApp(t)
	metadata, err := a.fetchArrWishMetadata(t.Context(), Config{RadarrURL: server.URL, RadarrAPIKey: "secret"}, Wish{Title: "Arr Movie", Type: "Movie", Source: "tmdb", ExternalID: "123"})
	if err != nil || metadata.Title != "Arr Movie" || metadata.Overview != "From Radarr" || metadata.ReleaseDate != "2026-02-03" || len(metadata.Genres) != 1 {
		t.Fatalf("unexpected Arr metadata: %+v err=%v", metadata, err)
	}
}

func TestSonarrSearchRequestIsDispatchedOnlyAfterAdminApproval(t *testing.T) {
	var posts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v3/series/lookup" {
			json.NewEncoder(w).Encode([]arrSeriesLookup{{Title: "Approval Series", TVDBID: 789, Year: 2026}})
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v3/rootfolder" {
			json.NewEncoder(w).Encode([]arrRootFolder{{Path: "/media/series"}})
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v3/qualityprofile" {
			json.NewEncoder(w).Encode([]arrProfile{{ID: 2}})
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v3/languageprofile" {
			json.NewEncoder(w).Encode([]arrProfile{{ID: 1}})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v3/series" {
			posts++
			w.WriteHeader(http.StatusCreated)
			return
		}
		t.Fatalf("unexpected Sonarr request %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	a := testApp(t)
	a.sessions["user-session"] = session{User: "viewer", Expiry: time.Now().Add(time.Hour)}
	a.sessions["admin-session"] = session{User: "admin", Expiry: time.Now().Add(time.Hour)}
	a.cfg.SonarrURL, a.cfg.SonarrAPIKey = server.URL, "sonarr-secret"

	created := request(a, http.MethodPost, "/api/wishes", `{"title":"Approval Series","type":"Series","source":"tvdb","externalId":"789"}`, a.origin, "user-session")
	if created.Code != http.StatusOK || posts != 0 {
		t.Fatalf("request creation dispatched prematurely: status=%d posts=%d body=%s", created.Code, posts, created.Body.String())
	}
	var wish Wish
	if err := json.Unmarshal(created.Body.Bytes(), &wish); err != nil {
		t.Fatal(err)
	}
	approved := request(a, http.MethodPost, "/api/wishes/"+wish.ID, `{"status":"approved"}`, a.origin, "admin-session")
	if approved.Code != http.StatusOK || posts != 1 || a.wishes[0].Status != "approved" {
		t.Fatalf("approval did not dispatch once: status=%d posts=%d wish=%+v body=%s", approved.Code, posts, a.wishes[0], approved.Body.String())
	}
}
