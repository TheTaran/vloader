package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
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
		message string
		err     error
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
				result <- serverResult{message: message.String()}
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
	if err := sendSMTPMessage(cfg, "admin@example.com", "test", "plain transport test\r\n"); err != nil {
		t.Fatalf("plain SMTP send failed: %v", err)
	}
	got := <-result
	if got.err != nil || !strings.Contains(got.message, "plain transport test") {
		t.Fatalf("plain SMTP server result = %#v", got)
	}
}

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
