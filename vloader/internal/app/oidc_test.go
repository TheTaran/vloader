package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"golang.org/x/oauth2"
)

func TestOIDCFlowValidation(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	signer, e := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name, subject, nonce, audience string
		groups                         []string
		allowedGroups, adminGroups     string
		role                           string
		expired, missingCookie         bool
		status                         int
	}{
		{name: "allowed subject", subject: "allowed-sub", nonce: "nonce", audience: "client", status: 303},
		{name: "denied subject", subject: "other-sub", nonce: "nonce", audience: "client", status: 403},
		{name: "allowed Pocket ID group", subject: "group-user", nonce: "nonce", audience: "client", groups: []string{"vloader-users"}, allowedGroups: "vloader-users", role: "user", status: 303},
		{name: "denied group", subject: "other-user", nonce: "nonce", audience: "client", groups: []string{"other-group"}, allowedGroups: "vloader-users", status: 403},
		{name: "admin Pocket ID group", subject: "admin-user", nonce: "nonce", audience: "client", groups: []string{"vloader-admins"}, adminGroups: "vloader-admins", role: "admin", status: 303},
		{name: "nonce mismatch", subject: "allowed-sub", nonce: "wrong", audience: "client", status: 401},
		{name: "audience mismatch", subject: "allowed-sub", nonce: "nonce", audience: "other-client", status: 401},
		{name: "expired token", subject: "allowed-sub", nonce: "nonce", audience: "client", expired: true, status: 401},
		{name: "browser state binding", subject: "allowed-sub", nonce: "nonce", audience: "client", missingCookie: true, status: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			a.cfg.OIDCAllowedSubjects = "allowed-sub"
			a.cfg.OIDCGroupsClaim = "groups"
			a.cfg.OIDCAllowedGroups = tc.allowedGroups
			a.cfg.OIDCAdminGroups = tc.adminGroups
			var issuer string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/keys":
					jsonOut(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, Algorithm: "RS256", Use: "sig"}}})
				case "/token":
					if e := r.ParseForm(); e != nil {
						t.Error(e)
					}
					if r.Form.Get("code_verifier") != "verifier" {
						t.Error("PKCE verifier missing")
					}
					exp := time.Now().Add(time.Hour)
					if tc.expired {
						exp = time.Now().Add(-time.Hour)
					}
					token, e := jwt.Signed(signer).Claims(map[string]any{"iss": issuer, "sub": tc.subject, "aud": tc.audience, "exp": exp.Unix(), "iat": time.Now().Add(-2 * time.Hour).Unix(), "nonce": tc.nonce, "groups": tc.groups}).Serialize()
					if e != nil {
						t.Error(e)
					}
					jsonOut(w, map[string]any{"access_token": "test-access", "token_type": "Bearer", "id_token": token})
				default:
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			issuer = server.URL
			a.oauth = &oauth2.Config{ClientID: "client", Endpoint: oauth2.Endpoint{AuthURL: issuer + "/authorize", TokenURL: issuer + "/token", AuthStyle: oauth2.AuthStyleInParams}, RedirectURL: a.origin + "/auth/callback"}
			a.verifier = oidc.NewVerifier(issuer, oidc.NewRemoteKeySet(context.Background(), issuer+"/keys"), &oidc.Config{ClientID: "client"})
			a.flows["state"] = flow{"nonce", "verifier", time.Now().Add(time.Minute)}
			r := httptest.NewRequest("GET", "/auth/callback?state=state&code=code", nil)
			if !tc.missingCookie {
				r.AddCookie(&http.Cookie{Name: "oidc_state", Value: "state"})
			}
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if tc.status == 303 {
				if len(w.Result().Cookies()) == 0 {
					t.Fatal("no session")
				}
				if tc.role != "" {
					login := httptest.NewRequest("GET", "/", nil)
					login.AddCookie(w.Result().Cookies()[0])
					if got := a.role(login); got != tc.role {
						t.Fatalf("role = %q, want %q", got, tc.role)
					}
				}
				again := httptest.NewRecorder()
				a.Handler().ServeHTTP(again, r)
				if again.Code != 403 {
					t.Fatal("state replay accepted")
				}
			}
		})
	}
}

func TestOIDCStartUsesPKCEAndNonce(t *testing.T) {
	a := testApp(t)
	a.oauth = &oauth2.Config{ClientID: "client", Endpoint: oauth2.Endpoint{AuthURL: "https://id.example/authorize"}, RedirectURL: a.origin + "/auth/callback", Scopes: []string{"openid", "groups"}}
	w := request(a, "GET", "/auth/oidc", "", "", "")
	if w.Code != 302 {
		t.Fatal(w.Code)
	}
	u, e := url.Parse(w.Header().Get("Location"))
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" || q.Get("state") == "" || !strings.Contains(q.Get("scope"), "groups") {
		t.Fatal("OIDC safeguards missing")
	}
	if len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].Value != q.Get("state") {
		t.Fatal("missing browser binding")
	}
	b, _ := json.Marshal(a.flows)
	if len(b) == 0 {
		t.Fatal("missing flow")
	}
}

func TestOIDCDisplayNamePrefersDisplayNameThenName(t *testing.T) {
	for _, tc := range []struct {
		name, claims, want string
	}{
		{name: "display_name", claims: `{"display_name":"Alex Display","name":"Alex Name"}`, want: "Alex Display"},
		{name: "name fallback", claims: `{"name":"Alex Name"}`, want: "Alex Name"},
		{name: "subject fallback", claims: `{}`, want: "stable-subject"},
		{name: "blank display name", claims: `{"display_name":"  ","name":"Alex Name"}`, want: "Alex Name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var claims map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.claims), &claims); err != nil {
				t.Fatal(err)
			}
			if got := oidcDisplayName(claims, "stable-subject"); got != tc.want {
				t.Fatalf("oidcDisplayName() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOIDCVerifiedEmailRequiresVerifiedValidAddress(t *testing.T) {
	for _, tc := range []struct{ claims, want string }{
		{`{"email":"viewer@example.com","email_verified":true}`, "viewer@example.com"},
		{`{"email":"viewer@example.com","email_verified":false}`, ""},
		{`{"email":"viewer@example.com"}`, ""},
		{`{"email":"bad\r\nBcc:attacker@example.com","email_verified":true}`, ""},
	} {
		var claims map[string]json.RawMessage
		if err := json.Unmarshal([]byte(tc.claims), &claims); err != nil {
			t.Fatal(err)
		}
		if got := oidcVerifiedEmail(claims); got != tc.want {
			t.Errorf("oidcVerifiedEmail(%s) = %q, want %q", tc.claims, got, tc.want)
		}
	}
}
