package app

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestWishAccessPersistenceAndAdminReview(t *testing.T) {
	a := testApp(t)
	a.sessions["admin-session"] = session{User: "admin", Expiry: time.Now().Add(time.Hour)}
	a.sessions["user-session"] = session{User: "viewer", Expiry: time.Now().Add(time.Hour)}
	sessionRoles.Store("user-session", "user")
	t.Cleanup(func() { sessionRoles.Delete("user-session") })

	created := request(a, http.MethodPost, "/api/wishes", `{"title":"A New Film","type":"Movie","source":"manual"}`, a.origin, "user-session")
	if created.Code != http.StatusOK {
		t.Fatalf("create wish: %d %s", created.Code, created.Body.String())
	}
	var wish Wish
	if err := json.Unmarshal(created.Body.Bytes(), &wish); err != nil || wish.Requester != "viewer" || wish.Status != "pending" {
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
