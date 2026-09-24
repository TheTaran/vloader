package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"strings"
	"testing"
	"time"
)

func TestSMTPDisableTLSUsesPlainSMTP(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	type serverResult struct {
		message   string
		recipient string
		err       error
	}
	result := make(chan serverResult, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			result <- serverResult{err: err}
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		send := func(line string) error { _, err := fmt.Fprint(conn, line+"\r\n"); return err }
		var recipient string
		if err := send("220 test SMTP ready"); err != nil {
			result <- serverResult{err: err}
			return
		}
		var message strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				result <- serverResult{err: err}
				return
			}
			command := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(command, "EHLO "):
				if err := send("250-test"); err != nil {
					result <- serverResult{err: err}
					return
				}
				if err := send("250 SIZE 100000"); err != nil {
					result <- serverResult{err: err}
					return
				}
			case strings.HasPrefix(command, "MAIL FROM:") || strings.HasPrefix(command, "RCPT TO:"):
				if err := send("250 accepted"); err != nil {
					result <- serverResult{err: err}
					return
				}
				if strings.HasPrefix(command, "RCPT TO:") {
					recipient = strings.TrimPrefix(command, "RCPT TO:")
				}
			case command == "DATA":
				if err := send("354 continue"); err != nil {
					result <- serverResult{err: err}
					return
				}
				for {
					dataLine, err := reader.ReadString('\n')
					if err != nil {
						result <- serverResult{err: err}
						return
					}
					if dataLine == ".\r\n" {
						break
					}
					message.WriteString(dataLine)
				}
				if err := send("250 queued"); err != nil {
					result <- serverResult{err: err}
					return
				}
			case command == "QUIT":
				_ = send("221 bye")
				result <- serverResult{message: message.String(), recipient: recipient}
				return
			default:
				_ = send("500 unexpected command")
				result <- serverResult{err: fmt.Errorf("unexpected SMTP command: %s", command)}
				return
			}
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	cfg := Config{SMTPHost: "127.0.0.1", SMTPPort: port, SMTPFrom: "vloader@example.com", SMTPDisableTLS: true}
	wish := Wish{RequesterEmail: "viewer@example.com", Title: "Requested Film", Type: "Movie"}
	if err := sendWishAvailableNotification(cfg, "https://vloader.example.com", wish); err != nil {
		t.Fatalf("plain SMTP send failed: %v", err)
	}
	got := <-result
	if got.err != nil || got.recipient != "<viewer@example.com>" || !strings.Contains(got.message, "Requested Film") || !strings.Contains(got.message, "available in Emby") {
		t.Fatalf("plain SMTP server result = %#v", got)
	}
}

func TestWishAccessPersistenceAndAdminReview(t *testing.T) {
	a := testApp(t)
	a.sessions["admin-session"] = session{User: "admin", Expiry: time.Now().Add(time.Hour)}
	a.sessions["user-session"] = session{User: "viewer", DisplayName: "Viewer Name", Email: "viewer@example.com", Expiry: time.Now().Add(time.Hour)}
	sessionRoles.Store("user-session", "user")
	t.Cleanup(func() { sessionRoles.Delete("user-session") })

	created := request(a, http.MethodPost, "/api/wishes", `{"title":"A New Film","type":"Movie","source":"manual"}`, a.origin, "user-session")
	if created.Code != http.StatusOK {
		t.Fatalf("create wish: %d %s", created.Code, created.Body.String())
	}
	if strings.Contains(created.Body.String(), "viewer@example.com") {
		t.Fatal("request response exposed the requester's email address")
	}
	var wish Wish
	if err := json.Unmarshal(created.Body.Bytes(), &wish); err != nil || wish.Requester != "viewer" || wish.RequesterName != "Viewer Name" || wish.Status != "pending" || a.wishes[0].RequesterEmail != "viewer@example.com" {
		t.Fatalf("invalid created wish: %+v err=%v", wish, err)
	}
	if len(a.notifications) != 1 {
		t.Fatal("saved request was not queued for notification")
	}
	for _, tc := range []struct {
		cookie string
		count  int
	}{{"user-session", 1}, {"admin-session", 1}} {
		listed := request(a, http.MethodGet, "/api/wishes", "", "", tc.cookie)
		var response struct {
			Items []Wish `json:"items"`
		}
		if err := json.Unmarshal(listed.Body.Bytes(), &response); err != nil || len(response.Items) != tc.count {
			t.Fatalf("list for %s: count=%d err=%v", tc.cookie, len(response.Items), err)
		}
		if strings.Contains(listed.Body.String(), "viewer@example.com") {
			t.Fatal("request list exposed the requester's email address")
		}
		if tc.cookie == "admin-session" && response.Items[0].RequesterName != "Viewer Name" {
			t.Fatalf("admin sees requester name %q, want display name", response.Items[0].RequesterName)
		}
	}
	body := `{"status":"approved"}`
	if updated := request(a, http.MethodPost, "/api/wishes/"+wish.ID, body, a.origin, "user-session"); updated.Code != http.StatusForbidden {
		t.Fatalf("non-admin changed request: %d", updated.Code)
	}
	if updated := request(a, http.MethodPost, "/api/wishes/"+wish.ID, body, a.origin, "admin-session"); updated.Code != http.StatusOK {
		t.Fatalf("admin review failed: %d %s", updated.Code, updated.Body.String())
	}
	duplicate := request(a, http.MethodPost, "/api/wishes", `{"title":"A New Film!","type":"Movie","source":"manual"}`, a.origin, "user-session")
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate wish was accepted: %d %s", duplicate.Code, duplicate.Body.String())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchWishMetadataFromTMDB(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Has("api_key") || r.Header.Get("Authorization") != "Bearer test-read-token" {
			t.Fatalf("TMDb credential handling is wrong: url=%s auth=%q", r.URL, r.Header.Get("Authorization"))
		}
		var body string
		switch path.Clean(r.URL.Path) {
		case "/3/find/tt1234567":
			body = `{"movie_results":[{"id":123,"title":"Example Movie","overview":"Synopsis","poster_path":"/poster.jpg","release_date":"2024-01-02","vote_average":7.5}]}`
		case "/3/movie/123":
			body = `{"genres":[{"name":"Drama"}],"runtime":98}`
		default:
			t.Fatalf("unexpected TMDb path: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	wish := Wish{Source: "imdb", ExternalID: "tt1234567", Type: "Movie"}
	got, err := fetchWishMetadata(t.Context(), client, "test-read-token", wish)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Example Movie" || got.PosterURL != "https://image.tmdb.org/t/p/w342/poster.jpg" || got.ReleaseDate != "2024-01-02" || got.Rating != 7.5 || got.Runtime != 98 || len(got.Genres) != 1 || got.Genres[0] != "Drama" {
		t.Fatalf("unexpected TMDb metadata: %+v", got)
	}
}

func TestFetchWishTVDBBannerForMovieAndSeries(t *testing.T) {
	for _, tc := range []struct{ kind, endpoint, source string }{
		{"Series", "/series/123/extended", "imdb"},
		{"Movie", "/movies/456/extended", "manual"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				response := func(status int, body string) (*http.Response, error) {
					return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
				}
				switch {
				case r.URL.Path == "/v4/login":
					var credentials map[string]string
					if err := json.NewDecoder(r.Body).Decode(&credentials); err != nil || credentials["apikey"] != "tvdb-key" || credentials["pin"] != "subscriber-pin" {
						t.Fatalf("TVDB credentials not sent correctly: %#v err=%v", credentials, err)
					}
					return response(200, `{"data":{"token":"session-token"}}`)
				case r.URL.Path == "/v4/search":
					if r.Header.Get("Authorization") != "Bearer session-token" || r.URL.Query().Get("type") != strings.ToLower(tc.kind) {
						t.Fatalf("TVDB search request invalid: %s auth=%q", r.URL, r.Header.Get("Authorization"))
					}
					if tc.source == "imdb" && r.URL.Query().Get("remote_id") != "tt1234567" || tc.source == "manual" && r.URL.Query().Get("query") != "Example Title" {
						t.Fatalf("TVDB search did not use the intended request reference: %s", r.URL)
					}
					return response(200, `{"data":[{"id":"`+map[string]string{"Series": "123", "Movie": "456"}[tc.kind]+`","tvdb_id":"`+map[string]string{"Series": "123", "Movie": "456"}[tc.kind]+`","name":"Example Title","type":"`+strings.ToLower(tc.kind)+`"}]}`)
				case r.URL.Path == "/v4"+tc.endpoint:
					return response(200, `{"data":{"artworks":[{"image":"https://artworks.thetvdb.com/wide-fanart.jpg","type":1,"width":1920,"height":1080},{"image":"https://artworks.thetvdb.com/poster.jpg","type":2,"width":500,"height":750}]}}`)
				default:
					t.Fatalf("unexpected TVDB request: %s", r.URL)
					return nil, fmt.Errorf("unexpected TVDB request")
				}
			})}
			got, err := fetchWishTVDBBanner(t.Context(), client, "tvdb-key", "subscriber-pin", Wish{Type: tc.kind, Source: tc.source, ExternalID: "tt1234567", Title: "Example Title"})
			if err != nil || got != "https://artworks.thetvdb.com/wide-fanart.jpg" {
				t.Fatalf("TVDB banner = %q err=%v", got, err)
			}
		})
	}
}

func TestSafeTVDBImageURLRejectsExternalHosts(t *testing.T) {
	if got := safeTVDBImageURL("https://evil.example/image.jpg"); got != "" {
		t.Fatalf("accepted external artwork host: %q", got)
	}
	if got := safeTVDBImageURL("http://artworks.thetvdb.com/image.jpg"); got != "" {
		t.Fatalf("accepted insecure artwork url: %q", got)
	}
}

func TestRequesterEmailIsRedactedFromNotificationErrors(t *testing.T) {
	got := redactRequesterEmail("550 mailbox viewer@example.com unavailable", "viewer@example.com")
	if strings.Contains(got, "viewer@example.com") || !strings.Contains(got, "[email redacted]") {
		t.Fatalf("requester email was not redacted from SMTP diagnostics: %q", got)
	}
}

func TestSaveMetadataSettingsIsAdminOnlyAndSecretIsRedacted(t *testing.T) {
	a := testApp(t)
	a.sessions["admin-session"] = session{User: "admin", Expiry: time.Now().Add(time.Hour)}
	a.sessions["user-session"] = session{User: "viewer", Expiry: time.Now().Add(time.Hour)}
	sessionRoles.Store("user-session", "user")
	t.Cleanup(func() { sessionRoles.Delete("user-session") })
	body := `{"TMDBAPIKey":"private-read-token","TVDBAPIKey":"private-tvdb-key","TVDBPIN":"private-pin"}`
	if got := request(a, http.MethodPost, "/api/settings/metadata", body, a.origin, "user-session"); got.Code != http.StatusForbidden {
		t.Fatalf("regular user changed metadata settings: %d", got.Code)
	}
	if got := request(a, http.MethodPost, "/api/settings/metadata", body, a.origin, "admin-session"); got.Code != http.StatusOK {
		t.Fatalf("admin could not save metadata settings: %d %s", got.Code, got.Body.String())
	}
	settings := request(a, http.MethodGet, "/api/settings", "", "", "admin-session")
	if !strings.Contains(settings.Body.String(), `"HasTMDBAPIKey":true`) || !strings.Contains(settings.Body.String(), `"HasTVDBAPIKey":true`) || !strings.Contains(settings.Body.String(), `"HasTVDBPIN":true`) || strings.Contains(settings.Body.String(), "private-read-token") || strings.Contains(settings.Body.String(), "private-tvdb-key") || strings.Contains(settings.Body.String(), "private-pin") {
		t.Fatal("metadata settings did not persist safely or exposed the API token")
	}
}

func TestWishMatchingUsesProviderIDAndMediaType(t *testing.T) {
	imdbWish := Wish{Title: "Movie", Type: "Movie", Source: "imdb", ExternalID: "tt1234567"}
	if !wishMatches(imdbWish, Item{Type: "Movie", ProviderIDs: map[string]string{"Imdb": "tt1234567"}}) {
		t.Fatal("IMDb provider ID did not match")
	}
	if wishMatches(imdbWish, Item{Type: "Series", ProviderIDs: map[string]string{"Imdb": "tt1234567"}}) {
		t.Fatal("movie request matched a series")
	}
	manual := Wish{Title: "A New Film", Type: "Movie"}
	if !wishMatches(manual, Item{Type: "Movie", Name: "A New-Film"}) {
		t.Fatal("normalized manual title did not match")
	}
	if wishMatches(manual, Item{Type: "Movie", Name: "A New Film Extended"}) {
		t.Fatal("partial manual title matched")
	}
	for source, value := range map[string]string{"imdb": "tt1234567", "tmdb": "55"} {
		if _, _, ok := normalizeWishReference(source, value, "Movie"); !ok {
			t.Errorf("valid %s reference rejected", source)
		}
	}
	if _, _, ok := normalizeWishReference("imdb", "tt1234567\r\nBcc: bad@example.com", "Movie"); ok {
		t.Fatal("invalid reference accepted")
	}
}
