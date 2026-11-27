package main

// gcl signs in to Google on its own. `gcl login` runs the installed-app OAuth
// flow (loopback redirect with PKCE) and keeps the result in
// ~/.config/gcl/token.json, so gcl runs on a host that has no gcalcli: a
// Linux workspace, a fresh laptop.
//
// gcalcli's stored sign-in is still read when gcl has none of its own, from
// the path gcalcli uses on this OS, so an existing gcalcli setup keeps
// working without `gcl login`. The macOS path was the only one before, which
// made gcl unusable on Linux.
//
// In a warren workspace the browser is on the laptop:
//
//	warren auth <ws> -- gcl login
//
// opens the URL there and carries the redirect back to the loopback port gcl
// listens on in the workspace.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	googleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"
	// The scope gcalcli asks for: gcl reads events, answers invitations and
	// queries free/busy.
	calendarScope = "https://www.googleapis.com/auth/calendar"
)

// storedToken is token.json: the OAuth client and the refresh token it got.
type storedToken struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
	TokenURI     string `json:"token_uri,omitempty"`
}

// tokenPath is gcl's own sign-in, beside its config.
func tokenPath() string {
	if v := strings.TrimSpace(os.Getenv("GCL_TOKEN")); v != "" {
		return v
	}
	return filepath.Join(xdgConfigHome(), appName, "token.json")
}

// gcalcliOAuthPaths are where gcalcli keeps its pickled sign-in, most likely
// first. gcalcli 4.4+ uses platformdirs' user data dir; older versions wrote
// ~/.gcalcli_oauth.
func gcalcliOAuthPaths() []string {
	if v := os.Getenv("GCALCLI_OAUTH"); v != "" {
		return []string{v}
	}
	home, _ := os.UserHomeDir()
	var dirs []string
	if runtime.GOOS == "darwin" {
		dirs = append(dirs, filepath.Join(home, "Library", "Application Support", "gcalcli"))
	}
	data := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	dirs = append(dirs, filepath.Join(data, "gcalcli"))
	var out []string
	for _, d := range dirs {
		out = append(out, filepath.Join(d, "oauth"))
	}
	return append(out, filepath.Join(home, ".gcalcli_oauth"))
}

// errNoSignIn says how to sign in, here and from a laptop into a workspace.
func errNoSignIn() error {
	return fmt.Errorf("no Google sign-in: run `%s login` (it keeps the sign-in in %s).\n"+
		"In a warren workspace, run from the laptop: warren auth <workspace> -- %s login",
		appName, tokenPath(), appName)
}

// loadOAuthCreds finds a sign-in: gcl's own first, then gcalcli's.
func loadOAuthCreds() (*oauthCreds, error) {
	if b, err := os.ReadFile(tokenPath()); err == nil {
		var t storedToken
		if err := json.Unmarshal(b, &t); err != nil {
			return nil, fmt.Errorf("%s: %w", tokenPath(), err)
		}
		if t.ClientID == "" || t.RefreshToken == "" {
			return nil, fmt.Errorf("%s has no client or refresh token; run `%s login` again", tokenPath(), appName)
		}
		c := &oauthCreds{ClientID: t.ClientID, ClientSecret: t.ClientSecret, RefreshToken: t.RefreshToken, TokenURI: t.TokenURI}
		if c.TokenURI == "" {
			c.TokenURI = googleTokenURL
		}
		return c, nil
	}
	for _, p := range gcalcliOAuthPaths() {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		c, err := gcalcliCreds(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		return c, nil
	}
	return nil, errNoSignIn()
}

// gcalcliClient is the OAuth client in gcalcli's sign-in, if there is one:
// `gcl login` reuses it so a gcalcli user needs no client of their own.
func gcalcliClient() (id, secret string) {
	for _, p := range gcalcliOAuthPaths() {
		if data, err := os.ReadFile(p); err == nil {
			return extractPickleString(data, "_client_id"), extractPickleString(data, "_client_secret")
		}
	}
	return "", ""
}

type loginConfig struct {
	clientID, clientSecret string
	authURL, tokenURL      string
	// open shows the URL to the person; the URL is always printed as well.
	open func(string) error
	out  io.Writer
	// in answers the client prompts when no client is known.
	in      io.Reader
	timeout time.Duration
}

const loginUsage = `usage: gcl login [-client-id ID -client-secret SECRET] [-no-browser]

Signs in to Google Calendar and keeps the sign-in in ~/.config/gcl/token.json.
The OAuth client comes from the flags, GCL_CLIENT_ID/GCL_CLIENT_SECRET, an
earlier gcl login, or gcalcli's sign-in; without any, gcl asks for it (a
"Desktop app" client from Google Cloud Console > APIs & Services >
Credentials, with the Calendar API enabled).

In a warren workspace, run it from the laptop so the browser opens there:
  warren auth <workspace> -- gcl login`

func cliLogin(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, loginUsage) }
	id := fs.String("client-id", "", "OAuth client ID")
	secret := fs.String("client-secret", "", "OAuth client secret")
	noBrowser := fs.Bool("no-browser", false, "only print the URL")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg := loginConfig{
		clientID: *id, clientSecret: *secret,
		authURL: googleAuthURL, tokenURL: googleTokenURL,
		open: openURL, out: stderr, in: os.Stdin, timeout: 5 * time.Minute,
	}
	if *noBrowser {
		cfg.open = func(string) error { return nil }
	}
	if err := login(context.Background(), cfg); err != nil {
		fmt.Fprintln(stderr, "gcl login:", err)
		return 1
	}
	fmt.Fprintf(stdout, "Signed in. gcl keeps the sign-in in %s.\n", tokenPath())
	return 0
}

// resolveClient picks the OAuth client: flags, environment, an earlier gcl
// login, gcalcli's sign-in, then the person.
func resolveClient(cfg *loginConfig) error {
	if cfg.clientID == "" {
		cfg.clientID, cfg.clientSecret = os.Getenv("GCL_CLIENT_ID"), os.Getenv("GCL_CLIENT_SECRET")
	}
	if cfg.clientID == "" {
		if b, err := os.ReadFile(tokenPath()); err == nil {
			var t storedToken
			if json.Unmarshal(b, &t) == nil {
				cfg.clientID, cfg.clientSecret = t.ClientID, t.ClientSecret
			}
		}
	}
	if cfg.clientID == "" {
		cfg.clientID, cfg.clientSecret = gcalcliClient()
	}
	if cfg.clientID != "" {
		return nil
	}
	if cfg.in == nil {
		return errors.New("no OAuth client: pass -client-id and -client-secret")
	}
	r := bufio.NewReader(cfg.in)
	ask := func(q string) string {
		fmt.Fprint(cfg.out, q)
		s, _ := r.ReadString('\n')
		return strings.TrimSpace(s)
	}
	fmt.Fprintln(cfg.out, "No OAuth client yet. Use a \"Desktop app\" client from Google Cloud Console (APIs & Services > Credentials) with the Calendar API enabled.")
	cfg.clientID = ask("Client ID: ")
	cfg.clientSecret = ask("Client secret: ")
	if cfg.clientID == "" {
		return errors.New("no OAuth client")
	}
	return nil
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// login runs the loopback flow and saves token.json.
func login(ctx context.Context, cfg loginConfig) error {
	if err := resolveClient(&cfg); err != nil {
		return err
	}
	verifier, err := randomString(48)
	if err != nil {
		return err
	}
	state, err := randomString(16)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer ln.Close()
	redirect := fmt.Sprintf("http://127.0.0.1:%d/", ln.Addr().(*net.TCPAddr).Port)

	type result struct {
		code string
		err  error
	}
	got := make(chan result, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") == "" && q.Get("code") == "" && q.Get("error") == "" {
			http.NotFound(w, r) // a favicon or a stray request
			return
		}
		var res result
		switch {
		case q.Get("state") != state:
			res.err = errors.New("the redirect's state does not match this login")
		case q.Get("error") != "":
			res.err = fmt.Errorf("Google refused: %s", q.Get("error"))
		default:
			res.code = q.Get("code")
		}
		msg := "gcl is signed in. You can close this tab."
		if res.err != nil {
			msg = "gcl login failed: " + res.err.Error()
		}
		fmt.Fprintf(w, "<!doctype html><title>gcl</title><p>%s</p>", html.EscapeString(msg))
		select {
		case got <- res:
		default:
		}
	})}
	go srv.Serve(ln) //nolint:errcheck // ends with Shutdown below
	defer srv.Shutdown(context.Background())

	v := url.Values{
		"client_id":             {cfg.clientID},
		"redirect_uri":          {redirect},
		"response_type":         {"code"},
		"scope":                 {calendarScope},
		"access_type":           {"offline"},
		"prompt":                {"consent"},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	authURL := cfg.authURL + "?" + v.Encode()
	fmt.Fprintf(cfg.out, "Open this URL to sign in:\n\n  %s\n\n", authURL)
	if err := cfg.open(authURL); err != nil {
		fmt.Fprintf(cfg.out, "(could not open a browser: %v)\n", err)
	}

	tctx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()
	var res result
	select {
	case res = <-got:
	case <-tctx.Done():
		return errors.New("no redirect came back in time")
	}
	if res.err != nil {
		return res.err
	}

	form := url.Values{
		"code":          {res.code},
		"client_id":     {cfg.clientID},
		"client_secret": {cfg.clientSecret},
		"redirect_uri":  {redirect},
		"grant_type":    {"authorization_code"},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(tctx, http.MethodPost, cfg.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var tok struct {
		RefreshToken string `json:"refresh_token"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return fmt.Errorf("token response: %w", err)
	}
	if tok.RefreshToken == "" {
		return fmt.Errorf("no refresh token in the answer: %s %s", tok.Error, tok.ErrorDesc)
	}
	return saveToken(storedToken{ClientID: cfg.clientID, ClientSecret: cfg.clientSecret, RefreshToken: tok.RefreshToken, TokenURI: cfg.tokenURL})
}

// saveToken writes token.json readable by the owner only, replacing it
// whole so a crash never leaves half a file.
func saveToken(t storedToken) error {
	p := tokenPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(t, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// openURL opens a URL in the person's browser: $BROWSER (which `warren auth`
// points at the laptop), else the OS opener.
func openURL(u string) error {
	if b := os.Getenv("BROWSER"); b != "" {
		return exec.Command(b, u).Start()
	}
	if runtime.GOOS == "darwin" {
		return exec.Command("open", u).Start()
	}
	return exec.Command("xdg-open", u).Start()
}
