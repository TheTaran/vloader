package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		expired, missingCookie         bool
		status                         int
	}{
		{"allowed", "allowed-sub", "nonce", "client", false, false, 303},
		{"denied subject", "other-sub", "nonce", "client", false, false, 403},
		{"nonce mismatch", "allowed-sub", "wrong", "client", false, false, 401},
		{"audience mismatch", "allowed-sub", "nonce", "other-client", false, false, 401},
		{"expired token", "allowed-sub", "nonce", "client", true, false, 401},
		{"browser state binding", "allowed-sub", "nonce", "client", false, true, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			a.cfg.OIDCAllowedSubjects = "allowed-sub"
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
					token, e := jwt.Signed(signer).Claims(map[string]any{"iss": issuer, "sub": tc.subject, "aud": tc.audience, "exp": exp.Unix(), "iat": time.Now().Add(-2 * time.Hour).Unix(), "nonce": tc.nonce}).Serialize()
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
	a.oauth = &oauth2.Config{ClientID: "client", Endpoint: oauth2.Endpoint{AuthURL: "https://id.example/authorize"}, RedirectURL: a.origin + "/auth/callback", Scopes: []string{"openid"}}
	w := request(a, "GET", "/auth/oidc", "", "", "")
	if w.Code != 302 {
		t.Fatal(w.Code)
	}
	u, e := url.Parse(w.Header().Get("Location"))
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" || q.Get("state") == "" {
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
