package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"gopkg.in/yaml.v3"

	"github.com/Eve-Industry/eve-industry-discord-bot/internal/config"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/settings"
	"github.com/Eve-Industry/eve-industry-discord-bot/internal/store"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

const testKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

type env struct {
	t     *testing.T
	srv   *Server
	http  *httptest.Server
	store *store.Store
	clock time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	live, err := settings.Load(context.Background(), st, config.Settings{Channels: config.Channels{General: 11}}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := ConfigFromEnv(func(k string) string {
		return map[string]string{"ADMIN_PORT": "8443", "ADMIN_TLS_CERT": "c", "ADMIN_TLS_KEY": "k", "ADMIN_ENCRYPTION_KEY": testKey}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(cfg, st, live, quiet)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, srv: srv, store: st, clock: time.Unix(1_800_000_000, 0)}
	srv.now = func() time.Time { return e.clock }
	e.http = httptest.NewTLSServer(srv.Handler())
	t.Cleanup(e.http.Close)
	return e
}

// browser is one signed-in (or not) browser with its own cookies.
type browser struct {
	e      *env
	client *http.Client
	csrf   string
}

func (e *env) browser() *browser {
	jar, _ := cookiejar.New(nil)
	c := *e.http.Client() // a copy, so each browser keeps its own cookies
	c.Jar = jar
	return &browser{e: e, client: &c}
}

func (b *browser) do(method, path string, body any) (int, map[string]any) {
	b.e.t.Helper()
	var r io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		r = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, b.e.http.URL+path, r)
	req.Header.Set("Origin", b.e.http.URL)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if b.csrf != "" {
		req.Header.Set(csrfHeader, b.csrf)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if c, ok := out["csrf"].(string); ok {
		b.csrf = c
	}
	return resp.StatusCode, out
}

func (b *browser) want(status int, method, path string, body any) map[string]any {
	b.e.t.Helper()
	got, out := b.do(method, path, body)
	if got != status {
		b.e.t.Fatalf("%s %s = %d %v, want %d", method, path, got, out, status)
	}
	return out
}

func (e *env) code(secret string) string {
	e.t.Helper()
	c, err := totp.GenerateCode(secret, e.clock)
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *env) invite(email string) string {
	e.t.Helper()
	link, _, err := Invite(context.Background(), e.store, "https://admin.example", email, "test", e.clock)
	if err != nil {
		e.t.Fatal(err)
	}
	return link[strings.Index(link, "#")+1:]
}

// onboard accepts an invite and sets up an authenticator, returning the
// signed-in browser, the TOTP secret and the recovery codes.
func (e *env) onboard(email, password string) (*browser, string, []string) {
	e.t.Helper()
	b := e.browser()
	token := e.invite(email)
	b.want(200, "POST", "/auth/invite/accept", map[string]any{"token": token, "password": password})
	setup := b.want(200, "POST", "/auth/totp/setup", nil)
	secret := setup["secret"].(string)
	out := b.want(200, "POST", "/auth/totp/confirm", map[string]any{"code": e.code(secret)})
	var codes []string
	for _, c := range out["recovery_codes"].([]any) {
		codes = append(codes, c.(string))
	}
	return b, secret, codes
}

const goodPassword = "veldspar to the horizon"

func TestInviteSetupAndSettings(t *testing.T) {
	e := newEnv(t)
	b := e.browser()
	token := e.invite("Pilot@Example.com")

	out := b.want(200, "POST", "/auth/invite/check", map[string]any{"token": token})
	if out["email"] != "Pilot@Example.com" {
		t.Errorf("invite email = %v", out["email"])
	}
	b.want(400, "POST", "/auth/invite/accept", map[string]any{"token": token, "password": "short"})
	out = b.want(200, "POST", "/auth/invite/accept", map[string]any{"token": token, "password": goodPassword})
	if out["state"] != store.SessionSetup2FA {
		t.Fatalf("state after invite = %v", out["state"])
	}
	b.want(404, "POST", "/auth/invite/accept", map[string]any{"token": token, "password": goodPassword})

	// Nothing else works before the authenticator is set up.
	if out := b.want(403, "GET", "/api/v1/settings", nil); out["code"] != "totp_setup_required" {
		t.Errorf("settings before setup: %v", out)
	}

	setup := b.want(200, "POST", "/auth/totp/setup", nil)
	if !strings.HasPrefix(setup["qr_code"].(string), "data:image/png;base64,") {
		t.Error("no QR code")
	}
	if out := b.want(401, "POST", "/auth/totp/confirm", map[string]any{"code": "000000"}); out["code"] != "wrong_code" {
		t.Errorf("wrong code: %v", out)
	}
	out = b.want(200, "POST", "/auth/totp/confirm", map[string]any{"code": e.code(setup["secret"].(string))})
	if out["state"] != store.SessionFull || len(out["recovery_codes"].([]any)) != 10 {
		t.Fatalf("confirm = %v", out)
	}

	doc := b.want(200, "GET", "/api/v1/settings", nil)
	if doc["version"].(float64) != 1 {
		t.Fatalf("settings = %v", doc)
	}
	s := doc["settings"].(map[string]any)
	s["welcome"] = map[string]any{"message": "o7"}
	saved := b.want(200, "PUT", "/api/v1/settings", map[string]any{"version": 1, "settings": s})
	if saved["version"].(float64) != 2 || e.srv.settings.Settings().Welcome.Message != "o7" {
		t.Errorf("save didn't apply: %v", saved)
	}
	conflict := b.want(409, "PUT", "/api/v1/settings", map[string]any{"version": 1, "settings": s})
	if conflict["current"].(map[string]any)["version"].(float64) != 2 {
		t.Errorf("conflict = %v", conflict)
	}

	log := b.want(200, "GET", "/api/v1/audit", nil)["entries"].([]any)
	if last := log[0].(map[string]any); last["action"] != "settings.update" || last["actor"] != "Pilot@Example.com" {
		t.Errorf("latest audit entry = %v", last)
	}
}

func TestLoginNeedsSecondFactorOnce(t *testing.T) {
	e := newEnv(t)
	first, secret, codes := e.onboard("pilot@example.com", goodPassword)
	first.want(204, "POST", "/auth/logout", nil)
	first.want(401, "GET", "/api/v1/me", nil)

	b := e.browser()
	if out := b.want(401, "POST", "/auth/login", map[string]any{"email": "pilot@example.com", "password": "wrong password here"}); out["code"] != "bad_credentials" {
		t.Errorf("wrong password: %v", out)
	}
	if out := b.want(401, "POST", "/auth/login", map[string]any{"email": "nobody@example.com", "password": goodPassword}); out["error"] != "Wrong email or password." {
		t.Errorf("unknown email: %v", out)
	}
	out := b.want(200, "POST", "/auth/login", map[string]any{"email": "PILOT@example.com", "password": goodPassword})
	if out["state"] != store.SessionPending2FA {
		t.Fatalf("state after password = %v", out["state"])
	}
	if out := b.want(403, "GET", "/api/v1/settings", nil); out["code"] != "second_factor_required" {
		t.Errorf("settings before 2FA: %v", out)
	}

	// The code used during setup can't be replayed.
	b.want(401, "POST", "/auth/verify", map[string]any{"code": e.code(secret)})

	out = b.want(200, "POST", "/auth/verify", map[string]any{"recovery_code": strings.ToUpper(codes[0])})
	if out["state"] != store.SessionFull || out["recovery_codes_left"].(float64) != 9 {
		t.Fatalf("verify with recovery code = %v", out)
	}
	b.want(200, "GET", "/api/v1/settings", nil)

	// A recovery code works once.
	b2 := e.browser()
	b2.want(200, "POST", "/auth/login", map[string]any{"email": "pilot@example.com", "password": goodPassword})
	b2.want(401, "POST", "/auth/verify", map[string]any{"recovery_code": codes[0]})
	e.clock = e.clock.Add(time.Minute)
	b2.want(200, "POST", "/auth/verify", map[string]any{"code": e.code(secret)})
}

func TestCSRFAndOrigin(t *testing.T) {
	e := newEnv(t)
	b, _, _ := e.onboard("pilot@example.com", goodPassword)

	token := b.csrf
	b.csrf = "wrong"
	if out := b.want(403, "PUT", "/api/v1/settings", map[string]any{}); out["code"] != "csrf" {
		t.Errorf("wrong CSRF token: %v", out)
	}
	b.csrf = token

	req, _ := http.NewRequest("POST", e.http.URL+"/auth/logout", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set(csrfHeader, token)
	resp, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("cross-origin POST = %d, want 403", resp.StatusCode)
	}
	b.want(200, "GET", "/api/v1/me", nil)
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	resp, err := e.http.Client().Get(e.http.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, h := range []string{"Content-Security-Policy", "Strict-Transport-Security", "X-Content-Type-Options", "Referrer-Policy"} {
		if resp.Header.Get(h) == "" {
			t.Errorf("missing %s", h)
		}
	}
}

func TestStepUpAndAdmins(t *testing.T) {
	e := newEnv(t)
	b, secret, _ := e.onboard("lead@example.com", goodPassword)

	out := b.want(201, "POST", "/api/v1/admins/invites", map[string]any{"email": "second@example.com"})
	if !strings.Contains(out["link"].(string), "/invite#") {
		t.Errorf("invite link = %v", out["link"])
	}
	b.want(409, "POST", "/api/v1/admins/invites", map[string]any{"email": "lead@example.com"})

	// Six minutes on, admin changes need a fresh code.
	e.clock = e.clock.Add(6 * time.Minute)
	if out := b.want(403, "POST", "/api/v1/admins/invites", map[string]any{"email": "third@example.com"}); out["code"] != "step_up_required" {
		t.Fatalf("stale step-up: %v", out)
	}
	b.want(200, "POST", "/auth/verify", map[string]any{"code": e.code(secret)})
	b.want(201, "POST", "/api/v1/admins/invites", map[string]any{"email": "third@example.com"})

	other, _, _ := e.onboard("other@example.com", goodPassword)
	admins := b.want(200, "GET", "/api/v1/admins", nil)["admins"].([]any)
	if len(admins) != 2 {
		t.Fatalf("admins = %v", admins)
	}
	otherID := admins[1].(map[string]any)["id"].(float64)
	selfID := admins[0].(map[string]any)["id"].(float64)

	b.want(400, "DELETE", "/api/v1/admins/"+ftoa(selfID), nil)
	out = b.want(200, "POST", "/api/v1/admins/"+ftoa(otherID)+"/reset", nil)
	if !strings.Contains(out["link"].(string), "/invite#") {
		t.Errorf("reset link = %v", out)
	}
	other.want(401, "GET", "/api/v1/me", nil) // reset signs them out
	b.want(204, "DELETE", "/api/v1/admins/"+ftoa(otherID), nil)
}

func TestLimits(t *testing.T) {
	e := newEnv(t)
	b, _, _ := e.onboard("pilot@example.com", goodPassword)
	b.want(204, "POST", "/auth/logout", nil)

	for range 5 {
		b.want(401, "POST", "/auth/login", map[string]any{"email": "pilot@example.com", "password": "not the password"})
	}
	b.want(429, "POST", "/auth/login", map[string]any{"email": "pilot@example.com", "password": goodPassword})
	e.clock = e.clock.Add(16 * time.Minute)

	// Five wrong codes end the session; ten in an hour lock the account.
	b.want(200, "POST", "/auth/login", map[string]any{"email": "pilot@example.com", "password": goodPassword})
	for range 4 {
		b.want(401, "POST", "/auth/verify", map[string]any{"code": "000000"})
	}
	if out := b.want(401, "POST", "/auth/verify", map[string]any{"code": "000000"}); out["code"] != "unauthorized" {
		t.Fatalf("fifth wrong code: %v", out)
	}
	b.want(200, "POST", "/auth/login", map[string]any{"email": "pilot@example.com", "password": goodPassword})
	for range 4 {
		b.want(401, "POST", "/auth/verify", map[string]any{"code": "000000"})
	}
	if out := b.want(403, "POST", "/auth/verify", map[string]any{"code": "000000"}); out["code"] != "locked" {
		t.Fatalf("tenth wrong code: %v", out)
	}
	if out := b.want(403, "POST", "/auth/login", map[string]any{"email": "pilot@example.com", "password": goodPassword}); out["code"] != "locked" {
		t.Errorf("login while locked: %v", out)
	}
}

func TestSessionsExpire(t *testing.T) {
	e := newEnv(t)
	b, _, _ := e.onboard("pilot@example.com", goodPassword)
	e.clock = e.clock.Add(31 * time.Minute)
	b.want(401, "GET", "/api/v1/me", nil)
}

func TestConfigFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	if _, on, _ := ConfigFromEnv(env(nil)); on {
		t.Error("dashboard on without ADMIN_PORT")
	}
	cfg, _, err := ConfigFromEnv(env(map[string]string{"ADMIN_PORT": "9000"}))
	if err != nil || cfg.Addr() != "127.0.0.1:9000" || cfg.BaseURL() != "http://localhost:9000" {
		t.Errorf("plain HTTP: %+v, %v; want loopback only", cfg, err)
	}
	if _, _, err := ConfigFromEnv(env(map[string]string{"ADMIN_PORT": "9000", "ADMIN_HOST": "0.0.0.0"})); err == nil {
		t.Error("plain HTTP on all interfaces accepted")
	}
	cfg, _, err = ConfigFromEnv(env(map[string]string{"ADMIN_PORT": "8080", "ADMIN_PUBLIC_URL": "https://admin.example/"}))
	if err != nil || cfg.Addr() != ":8080" || cfg.BaseURL() != "https://admin.example" || !cfg.proxied() {
		t.Errorf("behind a proxy: %+v, %v", cfg, err)
	}
	if _, _, err := ConfigFromEnv(env(map[string]string{"ADMIN_PORT": "80000"})); err == nil {
		t.Error("bad port accepted")
	}
	if _, _, err := ConfigFromEnv(env(map[string]string{"ADMIN_PORT": "443", "ADMIN_TLS_CERT": "c"})); err == nil {
		t.Error("cert without key accepted")
	}
	if _, _, err := ConfigFromEnv(env(map[string]string{"ADMIN_PORT": "443", "ADMIN_ENCRYPTION_KEY": "short"})); err == nil {
		t.Error("bad encryption key accepted")
	}
}

// TestOpenAPICoversRoutes keeps api/openapi.yaml in step with the server.
func TestOpenAPICoversRoutes(t *testing.T) {
	data, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	var documented []string
	for path, ops := range spec.Paths {
		for method := range ops {
			documented = append(documented, strings.ToUpper(method)+" "+path)
		}
	}
	e := newEnv(t)
	served := slices.Clone(e.srv.patterns)
	slices.Sort(served)
	slices.Sort(documented)
	if !slices.Equal(served, documented) {
		t.Errorf("routes and api/openapi.yaml differ\nserved:     %v\ndocumented: %v", served, documented)
	}
}

func ftoa(f float64) string { return strconv.Itoa(int(f)) }
