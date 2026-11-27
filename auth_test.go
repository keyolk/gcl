package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// pickle builds the SHORT_BINUNICODE key/value pairs gcalcli's pickle holds.
func pickle(kv ...string) []byte {
	var b []byte
	for _, s := range kv {
		b = append(b, 0x8c, byte(len(s)))
		b = append(b, s...)
	}
	return b
}

func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("GCALCLI_OAUTH", "")
	t.Setenv("GCL_TOKEN", "")
	t.Setenv("GCL_CLIENT_ID", "")
	t.Setenv("GCL_CLIENT_SECRET", "")
	return home
}

func write(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// With no sign-in the error names both ways to sign in.
func TestNoSignInSaysHowToSignIn(t *testing.T) {
	isolate(t)
	_, err := loadOAuthCreds()
	if err == nil || !strings.Contains(err.Error(), "gcl login") || !strings.Contains(err.Error(), "warren auth") {
		t.Fatalf("err %v", err)
	}
}

// On Linux gcalcli keeps its sign-in under the XDG data dir; gcl read only
// the macOS path before, and refused to start in a workspace.
func TestGcalcliSignInOnThisOS(t *testing.T) {
	home := isolate(t)
	p := filepath.Join(home, ".local", "share", "gcalcli", "oauth")
	if runtime.GOOS == "darwin" {
		p = filepath.Join(home, "Library", "Application Support", "gcalcli", "oauth")
	}
	write(t, p, pickle("_client_id", "cid", "_client_secret", "csec", "_refresh_token", "rt"))
	c, err := loadOAuthCreds()
	if err != nil {
		t.Fatal(err)
	}
	if c.ClientID != "cid" || c.RefreshToken != "rt" || c.TokenURI != googleTokenURL {
		t.Fatalf("%+v", c)
	}
}

// gcl's own sign-in wins over gcalcli's.
func TestOwnSignInFirst(t *testing.T) {
	home := isolate(t)
	write(t, filepath.Join(home, ".gcalcli_oauth"), pickle("_client_id", "old", "_client_secret", "s", "_refresh_token", "old-rt"))
	if err := saveToken(storedToken{ClientID: "new", ClientSecret: "s2", RefreshToken: "new-rt"}); err != nil {
		t.Fatal(err)
	}
	c, err := loadOAuthCreds()
	if err != nil {
		t.Fatal(err)
	}
	if c.ClientID != "new" || c.RefreshToken != "new-rt" {
		t.Fatalf("%+v", c)
	}
	fi, err := os.Stat(tokenPath())
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token.json mode %v %v", fi.Mode(), err)
	}
}

// fakeGoogle answers the token exchange and checks the PKCE pair.
func fakeGoogle(t *testing.T, challenge *string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("code") != "the-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != *challenge ||
			r.Form.Get("client_id") != "cid" || r.Form.Get("grant_type") != "authorization_code" {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "at", "refresh_token": "fresh-rt"})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// browser plays the person: it follows the auth URL's redirect_uri with a
// code and the given state (the URL's own when empty).
func browser(t *testing.T, challenge *string, state string) func(string) error {
	return func(u string) error {
		pu, err := url.Parse(u)
		if err != nil {
			return err
		}
		q := pu.Query()
		*challenge = q.Get("code_challenge")
		if q.Get("code_challenge_method") != "S256" || q.Get("access_type") != "offline" || q.Get("scope") != calendarScope {
			t.Errorf("auth URL %s", u)
		}
		if state == "" {
			state = q.Get("state")
		}
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?code=the-code&state=" + url.QueryEscape(state))
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
}

func TestLoginSavesTheRefreshToken(t *testing.T) {
	isolate(t)
	var challenge string
	g := fakeGoogle(t, &challenge)
	err := login(context.Background(), loginConfig{
		clientID: "cid", clientSecret: "csec", authURL: "https://accounts.example/auth", tokenURL: g.URL,
		open: browser(t, &challenge, ""), out: io.Discard, timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadOAuthCreds()
	if err != nil {
		t.Fatal(err)
	}
	if c.RefreshToken != "fresh-rt" || c.ClientSecret != "csec" || c.TokenURI != g.URL {
		t.Fatalf("%+v", c)
	}
}

// A second login reuses the client of the first: no flags needed.
func TestLoginReusesTheClient(t *testing.T) {
	isolate(t)
	if err := saveToken(storedToken{ClientID: "cid", ClientSecret: "csec", RefreshToken: "old"}); err != nil {
		t.Fatal(err)
	}
	var challenge string
	g := fakeGoogle(t, &challenge)
	err := login(context.Background(), loginConfig{
		authURL: "https://accounts.example/auth", tokenURL: g.URL,
		open: browser(t, &challenge, ""), out: io.Discard, timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := loadOAuthCreds(); c.RefreshToken != "fresh-rt" {
		t.Fatalf("%+v", c)
	}
}

func TestLoginRefusesAForeignState(t *testing.T) {
	isolate(t)
	var challenge string
	g := fakeGoogle(t, &challenge)
	err := login(context.Background(), loginConfig{
		clientID: "cid", clientSecret: "csec", authURL: "https://accounts.example/auth", tokenURL: g.URL,
		open: browser(t, &challenge, "someone-elses"), out: io.Discard, timeout: 10 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "state") {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(tokenPath()); err == nil {
		t.Fatal("a token was saved")
	}
}

// With no client anywhere, login asks for one.
func TestLoginAsksForAClient(t *testing.T) {
	isolate(t)
	cfg := loginConfig{out: io.Discard, in: strings.NewReader("cid\ncsec\n")}
	if err := resolveClient(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.clientID != "cid" || cfg.clientSecret != "csec" {
		t.Fatalf("%+v", cfg)
	}
}
