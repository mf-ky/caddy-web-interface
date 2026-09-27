package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// This test drives the whole API against real Caddy instances through the
// real agent script (local mode). It is skipped when `caddy` is not on PATH.

type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("X-CaddyWeb", "1")
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func (c *client) ok(method, path string, body any) map[string]any {
	c.t.Helper()
	code, out := c.do(method, path, body)
	if code != 200 {
		c.t.Fatalf("%s %s: %d %v", method, path, code, out)
	}
	return out
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, http: &http.Client{Jar: jar}}
}

// startCaddy writes a Caddyfile and an agent wrapper for one fake server.
func startCaddy(t *testing.T, dir string, adminPort, sitePort int, body string) string {
	t.Helper()
	etc := filepath.Join(dir, "etc")
	os.MkdirAll(etc, 0o755)
	caddyfile := fmt.Sprintf("{\n\tadmin localhost:%d\n}\n\n# Test site\n:%d {\n\trespond %q\n}\n", adminPort, sitePort, body)
	os.WriteFile(filepath.Join(etc, "Caddyfile"), []byte(caddyfile), 0o644)
	conf := fmt.Sprintf("CADDYFILE=%s/Caddyfile\nBACKUP_DIR=%s/backups\nSTAGING_DIR=%s/staging\n", etc, etc, dir)
	os.WriteFile(filepath.Join(dir, "agent.conf"), []byte(conf), 0o644)
	agentSrc, _ := filepath.Abs("../../agent/caddyweb-agent")
	wrapper := fmt.Sprintf("#!/bin/sh\nexport CADDYWEB_AGENT_CONF=%s/agent.conf HOME=%s\nexec %s \"$@\"\n", dir, dir, agentSrc)
	wp := filepath.Join(dir, "agent.sh")
	os.WriteFile(wp, []byte(wrapper), 0o755)
	cmd := exec.Command("caddy", "run", "--config", filepath.Join(etc, "Caddyfile"), "--adapter", "caddyfile")
	cmd.Env = append(os.Environ(), "HOME="+dir)
	if err := cmd.Start(); err != nil {
		t.Fatalf("caddy run: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	get(t, fmt.Sprintf("http://localhost:%d", sitePort))
	return wp
}

func get(t *testing.T, url string) string {
	t.Helper()
	for i := 0; i < 50; i++ {
		res, err := http.Get(url)
		if err == nil {
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			return string(b)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("GET %s failed", url)
	return ""
}

func TestEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("caddy"); err != nil {
		t.Skip("caddy not installed")
	}
	tmp := t.TempDir()
	agentA := startCaddy(t, filepath.Join(tmp, "a"), 22019, 18081, "hello from A")
	agentB := startCaddy(t, filepath.Join(tmp, "b"), 22020, 18082, "hello from B")

	srv, err := New(Options{DataDir: filepath.Join(tmp, "data"), Version: "test", Static: fstest.MapFS{"index.html": {Data: []byte("ok")}}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	c := newClient(t, ts.URL)

	// first-run setup creates the admin
	if code, _ := c.do("GET", "/api/servers", nil); code != 401 {
		t.Fatalf("expected 401 before login, got %d", code)
	}
	c.ok("POST", "/api/setup", map[string]string{"username": "alex", "password": "correct-horse"})
	if code, _ := c.do("POST", "/api/setup", map[string]string{"username": "evil", "password": "12345678"}); code != 403 {
		t.Fatal("setup allowed twice")
	}

	// CSRF header is required
	req, _ := http.NewRequest("POST", ts.URL+"/api/servers", strings.NewReader("{}"))
	res, _ := c.http.Do(req)
	if res.StatusCode != 403 {
		t.Fatalf("missing X-CaddyWeb header accepted: %d", res.StatusCode)
	}

	a := c.ok("POST", "/api/servers", map[string]any{"name": "Pi A", "connection": map[string]any{"mode": "local", "agentPath": agentA}})
	b := c.ok("POST", "/api/servers", map[string]any{"name": "Pi B", "connection": map[string]any{"mode": "local", "agentPath": agentB}})
	ida, idb := a["id"].(string), b["id"].(string)
	if ida != "pi-a" || idb != "pi-b" {
		t.Fatalf("ids %s %s", ida, idb)
	}
	list := c.ok("GET", "/api/servers", nil)["servers"].([]any)
	if len(list) != 2 || !list[0].(map[string]any)["ok"].(bool) {
		t.Fatalf("servers: %v", list)
	}

	st := c.ok("GET", "/api/servers/"+ida+"/state", nil)
	segs := st["segments"].([]any)
	if len(segs) != 2 {
		t.Fatalf("segments: %v", segs)
	}
	rev := int64(st["rev"].(float64))

	// add a site card
	newSeg := map[string]any{"segment": map[string]any{"kind": "site", "comments": []string{"# Second"}, "header": []string{":18083"},
		"nodes": []any{map[string]any{"type": "directive", "tokens": []string{"respond", `"second site"`}}}}}
	st = c.ok("POST", fmt.Sprintf("/api/servers/%s/draft/segments?rev=%d", ida, rev), newSeg)
	if !st["hasChanges"].(bool) {
		t.Fatal("expected pending changes")
	}
	rev = int64(st["rev"].(float64))

	// stale rev is refused
	if code, _ := c.do("POST", fmt.Sprintf("/api/servers/%s/draft/segments?rev=%d", ida, rev-1), newSeg); code != 409 {
		t.Fatalf("stale rev accepted: %d", code)
	}

	// apply → live
	out := c.ok("POST", "/api/servers/"+ida+"/apply", map[string]any{"rev": rev})
	if out["backup"] == "" {
		t.Fatal("no backup name")
	}
	if got := get(t, "http://localhost:18083"); got != "second site" {
		t.Fatalf("new site not live: %q", got)
	}
	disk, _ := os.ReadFile(filepath.Join(tmp, "a", "etc", "Caddyfile"))
	if !strings.Contains(string(disk), "# Second\n:18083 {\n\trespond \"second site\"\n}\n") || !strings.Contains(string(disk), "# Test site\n:18081 {") {
		t.Fatalf("Caddyfile on disk:\n%s", disk)
	}

	// an invalid edit is rejected by Caddy with a pointer to the line
	st = c.ok("GET", "/api/servers/"+ida+"/state", nil)
	rev = int64(st["rev"].(float64))
	bad := map[string]any{"segment": map[string]any{"kind": "site", "header": []string{":18084"},
		"nodes": []any{map[string]any{"type": "directive", "tokens": []string{"reverse_prox", "localhost:1"}}}}}
	st = c.ok("POST", fmt.Sprintf("/api/servers/%s/draft/segments?rev=%d", ida, rev), bad)
	code, e := c.do("POST", "/api/servers/"+ida+"/apply", map[string]any{"rev": st["rev"]})
	if code != 422 || !strings.Contains(e["error"].(string), "unrecognized directive") || e["segmentKey"] != "site::18084" {
		t.Fatalf("bad config: %d %v", code, e)
	}
	if got := get(t, "http://localhost:18083"); got != "second site" {
		t.Fatal("live config changed after a failed apply")
	}
	c.ok("POST", fmt.Sprintf("/api/servers/%s/draft/discard?rev=%v", ida, st["rev"]), nil)

	// copy the new card to server B
	st = c.ok("GET", "/api/servers/"+ida+"/state", nil)
	var secondID float64 = -1
	for _, sg := range st["segments"].([]any) {
		m := sg.(map[string]any)
		if m["key"] == "site::18083" {
			secondID = m["id"].(float64)
		}
	}
	c.ok("POST", fmt.Sprintf("/api/servers/%s/draft/segments/%v/copy?key=site::18083", ida, secondID), map[string]string{"target": idb})
	stb := c.ok("GET", "/api/servers/"+idb+"/state", nil)
	if !stb["hasChanges"].(bool) {
		t.Fatal("copy did not reach server B's draft")
	}

	// backups and restore
	bk := c.ok("GET", "/api/servers/"+ida+"/backups", nil)["backups"].([]any)
	if len(bk) != 1 {
		t.Fatalf("backups: %v", bk)
	}
	name := bk[0].(map[string]any)["name"].(string)
	c.ok("POST", "/api/servers/"+ida+"/backups/"+name+"/restore", map[string]any{})
	time.Sleep(300 * time.Millisecond)
	if res, err := http.Get("http://localhost:18083"); err == nil {
		res.Body.Close()
		t.Fatal("restored config still serves the removed site")
	}
	if got := get(t, "http://localhost:18081"); got != "hello from A" {
		t.Fatalf("restore: %q", got)
	}

	// roles: a viewer can read but not change anything, and sees no secrets
	c.ok("POST", "/api/users", map[string]string{"username": "guest", "role": "viewer", "password": "guest-pass-1"})
	v := newClient(t, ts.URL)
	v.ok("POST", "/api/login", map[string]string{"username": "guest", "password": "guest-pass-1"})
	v.ok("GET", "/api/servers/"+ida+"/state", nil)
	if code, _ := v.do("POST", fmt.Sprintf("/api/servers/%s/draft/segments?rev=0", ida), newSeg); code != 403 {
		t.Fatalf("viewer could add: %d", code)
	}
	if code, _ := v.do("GET", "/api/users", nil); code != 403 {
		t.Fatal("viewer could list users")
	}

	// power user: may add, may not delete or apply
	c.ok("POST", "/api/users", map[string]string{"username": "power", "role": "power", "password": "power-pass-1"})
	p := newClient(t, ts.URL)
	p.ok("POST", "/api/login", map[string]string{"username": "power", "password": "power-pass-1"})
	st = p.ok("GET", "/api/servers/"+ida+"/state", nil)
	st = p.ok("POST", fmt.Sprintf("/api/servers/%s/draft/segments?rev=%v", ida, st["rev"]), newSeg)
	if code, _ := p.do("DELETE", fmt.Sprintf("/api/servers/%s/draft/segments/0?rev=%v", ida, st["rev"]), nil); code != 403 {
		t.Fatalf("power user could delete: %d", code)
	}
	if code, _ := p.do("POST", "/api/servers/"+ida+"/apply", map[string]any{"rev": st["rev"]}); code != 403 {
		t.Fatalf("power user could apply: %d", code)
	}
	// ...and may not edit a live card
	for _, sg := range st["segments"].([]any) {
		m := sg.(map[string]any)
		if m["key"] == "site::18081" && m["canEdit"].(bool) {
			t.Fatal("power user may edit a live card")
		}
	}

	// password reset from the "CLI" (store) logs the session out
	if err := srv.users.SetPassword("power", "another-pass-2"); err != nil {
		t.Fatal(err)
	}
	if code, _ := p.do("GET", "/api/servers", nil); code != 401 {
		t.Fatalf("session survived password reset: %d", code)
	}
}
