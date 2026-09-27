package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/mf-ky/caddy-web-interface/internal/caddyfile"
	"github.com/mf-ky/caddy-web-interface/internal/remote"
	"github.com/mf-ky/caddy-web-interface/internal/store"
)

// serverCtx is everything CaddyWeb keeps in memory for one Caddy server.
type serverCtx struct {
	id     string
	srv    *Server
	st     *store.ServerState
	client *remote.Client

	draftMu sync.Mutex // serialises draft edits, apply and restore
	fetchMu sync.Mutex // one read of the live Caddyfile at a time

	cacheMu   sync.Mutex // guards the fields below
	info      *remote.Info
	infoAt    time.Time
	dns       []string
	dnsAt     time.Time
	liveErr   *remote.CaddyError
	lastFetch time.Time
}

// serverCtx returns (creating on first use) the context for a server id,
// or nil if no such server is configured.
func (s *Server) serverCtx(id string) *serverCtx {
	cfg, ok := s.state.Server(id)
	if !ok {
		return nil
	}
	s.ctxMu.Lock()
	defer s.ctxMu.Unlock()
	if sc, ok := s.ctxs[id]; ok {
		return sc
	}
	sc := &serverCtx{id: id, srv: s, st: s.state.ServerState(id), client: remote.NewClient(cfg.Connection, s.signer)}
	s.ctxs[id] = sc
	return sc
}

func (s *Server) dropServerCtx(id string) {
	s.ctxMu.Lock()
	defer s.ctxMu.Unlock()
	if sc, ok := s.ctxs[id]; ok {
		sc.client.SetConfig(remote.Config{})
		delete(s.ctxs, id)
	}
}

// resetCaches forgets cached facts after the connection settings change.
func (sc *serverCtx) resetCaches() {
	sc.cacheMu.Lock()
	sc.info, sc.dns, sc.lastFetch, sc.liveErr = nil, nil, time.Time{}, nil
	sc.cacheMu.Unlock()
}

type serverHandler func(http.ResponseWriter, *http.Request, *store.User, *serverCtx)

// onServer resolves {sid} in the path, then checks the role.
func (s *Server) onServer(h serverHandler, roles ...store.Role) http.HandlerFunc {
	return s.require(func(w http.ResponseWriter, r *http.Request, u *store.User) {
		sc := s.serverCtx(r.PathValue("sid"))
		if sc == nil {
			writeErr(w, http.StatusNotFound, "That server doesn't exist (any more).")
			return
		}
		h(w, r, u, sc)
	}, roles...)
}

// ---- overview ----

type serverSummary struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Mode       string             `json:"mode"`
	Host       string             `json:"host"`
	Port       int                `json:"port"`
	User       string             `json:"user"`
	AgentPath  string             `json:"agentPath,omitempty"`
	Trusted    bool               `json:"trusted"`
	Configured bool               `json:"configured"`
	OK         bool               `json:"ok"`
	Error      *remote.CaddyError `json:"error,omitempty"`
	Info       *remote.Info       `json:"info,omitempty"`
	Sites      int                `json:"sites"`
	Pending    int                `json:"pending"`
	Conflict   bool               `json:"conflict"`
	LastApply  *store.ApplyRecord `json:"lastApply,omitempty"`
	Fetched    time.Time          `json:"fetched"`
	Checking   bool               `json:"checking,omitempty"` // still waiting for the server to answer
}

func (s *Server) summarize(ctx context.Context, cfg store.ServerConfig) serverSummary {
	sc := s.serverCtx(cfg.ID)
	sum := serverSummary{ID: cfg.ID, Name: cfg.Name, Mode: cfg.Connection.Mode, Host: cfg.Connection.Host, Port: cfg.Connection.Port,
		User: cfg.Connection.User, AgentPath: cfg.Connection.AgentPath, Trusted: cfg.Connection.HostKey != "", Configured: cfg.Connection.Configured()}
	if sc == nil {
		return sum
	}
	live, liveErr := sc.syncLive(ctx, false)
	sc.draftMu.Lock()
	d := sc.currentDraft(live)
	sc.draftMu.Unlock()
	sum.OK, sum.Error, sum.Fetched = liveErr == nil && live.SHA != "", liveErr, live.Fetched
	if sum.OK {
		sum.Info = sc.cachedInfo(ctx)
	}
	text := d.Text
	if text == "" {
		text = live.Text
	}
	if doc, err := caddyfile.Parse(text); err == nil {
		liveKeys := map[string]string{}
		if ldoc, err := caddyfile.Parse(live.Text); err == nil {
			for _, sg := range ldoc.Segments() {
				liveKeys[sg.Key()] = sg.Text(ldoc.Indent)
			}
		}
		seen := map[string]bool{}
		for _, sg := range doc.Segments() {
			if sg.Kind == caddyfile.KindSite {
				sum.Sites++
			}
			seen[sg.Key()] = true
			if live.SHA != "" {
				if lt, ok := liveKeys[sg.Key()]; !ok || lt != sg.Text(doc.Indent) {
					sum.Pending++
				}
			}
		}
		for k := range liveKeys {
			if !seen[k] {
				sum.Pending++
			}
		}
	}
	sum.Conflict = live.SHA != "" && shaOf(d.Text) != live.SHA && d.BaseSHA != live.SHA
	if h := sc.st.History(); len(h) > 0 {
		sum.LastApply = &h[0]
	}
	return sum
}

func (s *Server) handleServers(w http.ResponseWriter, r *http.Request, u *store.User) {
	cfgs := s.state.Servers()
	out := make([]serverSummary, len(cfgs))
	type result struct {
		i   int
		sum serverSummary
	}
	results := make(chan result, len(cfgs))
	for i, cfg := range cfgs {
		go func(i int, cfg store.ServerConfig) {
			results <- result{i, s.summarize(context.WithoutCancel(r.Context()), cfg)}
		}(i, cfg)
	}
	// Answer quickly: servers that are slow to respond are reported as
	// "checking" and the page asks again a moment later.
	got := make([]bool, len(cfgs))
	deadline := time.After(2500 * time.Millisecond)
wait:
	for n := 0; n < len(cfgs); n++ {
		select {
		case res := <-results:
			out[res.i], got[res.i] = res.sum, true
		case <-deadline:
			break wait
		}
	}
	for i, cfg := range cfgs {
		if !got[i] {
			out[i] = serverSummary{ID: cfg.ID, Name: cfg.Name, Mode: cfg.Connection.Mode, Host: cfg.Connection.Host, Port: cfg.Connection.Port,
				User: cfg.Connection.User, Trusted: cfg.Connection.HostKey != "", Configured: cfg.Connection.Configured(), Checking: true}
		}
		if u.Role != store.RoleAdmin {
			out[i].AgentPath = ""
		}
	}
	writeJSON(w, map[string]any{"servers": out})
}

func (s *Server) handleServer(w http.ResponseWriter, r *http.Request, u *store.User, sc *serverCtx) {
	cfg, _ := s.state.Server(sc.id)
	writeJSON(w, s.summarize(r.Context(), cfg))
}

// ---- add / edit / remove ----

type serverInput struct {
	Name       string        `json:"name"`
	Connection remote.Config `json:"connection"`
}

func cleanConnection(c remote.Config) (remote.Config, error) {
	c.Host = strings.TrimSpace(c.Host)
	c.User = strings.TrimSpace(c.User)
	c.AgentPath = strings.TrimSpace(c.AgentPath)
	if c.Mode != "local" {
		c.Mode = "ssh"
		c.AgentPath = ""
	}
	if c.Port == 0 {
		c.Port = 22
	}
	if c.Port < 1 || c.Port > 65535 {
		return c, errors.New("port must be between 1 and 65535")
	}
	if c.User == "" {
		c.User = "caddyweb"
	}
	if strings.ContainsAny(c.Host, " /@") {
		return c, errors.New("host should be just a name or IP address, like 192.168.0.10")
	}
	if c.Mode == "ssh" && c.Host == "" {
		return c, errors.New("enter the Caddy server's IP address or host name")
	}
	if c.AgentPath != "" && !strings.HasPrefix(c.AgentPath, "/") {
		return c, errors.New("agent path must be absolute")
	}
	return c, nil
}

func (s *Server) handleAddServer(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in serverInput
	if !readJSON(w, r, &in) {
		return
	}
	c, err := cleanConnection(in.Connection)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	c.HostKey = ""
	cfg, err := s.state.AddServer(in.Name, c)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, s.summarize(r.Context(), cfg))
}

func (s *Server) handleUpdateServer(w http.ResponseWriter, r *http.Request, u *store.User, sc *serverCtx) {
	var in serverInput
	if !readJSON(w, r, &in) {
		return
	}
	c, err := cleanConnection(in.Connection)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg, err := s.state.UpdateServer(sc.id, func(cur *store.ServerConfig) {
		if name := strings.TrimSpace(in.Name); name != "" {
			cur.Name = name
		}
		hostKey := cur.Connection.HostKey
		if c.Host != cur.Connection.Host || c.Port != cur.Connection.Port || c.Mode != cur.Connection.Mode {
			hostKey = "" // new target: its key must be trusted again
		}
		c.HostKey = hostKey
		cur.Connection = c
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	sc.client.SetConfig(cfg.Connection)
	sc.resetCaches()
	writeJSON(w, s.summarize(r.Context(), cfg))
}

func (s *Server) handleDeleteServer(w http.ResponseWriter, r *http.Request, u *store.User, sc *serverCtx) {
	if err := s.state.RemoveServer(sc.id); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.dropServerCtx(sc.id)
	writeJSON(w, map[string]bool{"ok": true})
}

// handleConnTest checks the connection and reports the host key when it
// still needs to be trusted.
func (s *Server) handleConnTest(w http.ResponseWriter, r *http.Request, u *store.User, sc *serverCtx) {
	if !sc.client.Config().Configured() {
		writeErr(w, http.StatusBadRequest, "Enter the Caddy server's address first.")
		return
	}
	info, err := sc.client.Info(r.Context())
	if err != nil {
		var hk *remote.HostKeyError
		if errors.As(err, &hk) {
			writeJSON(w, map[string]any{"ok": false, "needsTrust": true, "fingerprint": hk.Fingerprint, "changed": hk.Expected != "", "message": hk.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": false, "message": remote.Explain(err).Message})
		return
	}
	sc.cacheMu.Lock()
	sc.info, sc.infoAt, sc.lastFetch = info, time.Now(), time.Time{}
	sc.cacheMu.Unlock()
	msg := "Connected to " + info.Hostname + " (Caddy " + info.CaddyVersion + ")."
	if !info.Writable {
		msg += " Warning: the agent can't write " + info.Caddyfile + " — run the installer again on that server."
	}
	writeJSON(w, map[string]any{"ok": true, "info": info, "message": msg})
}

func (s *Server) handleConnTrust(w http.ResponseWriter, r *http.Request, u *store.User, sc *serverCtx) {
	var in struct {
		Fingerprint string `json:"fingerprint"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !strings.HasPrefix(in.Fingerprint, "SHA256:") {
		writeErr(w, http.StatusBadRequest, "invalid fingerprint")
		return
	}
	cfg, err := s.state.UpdateServer(sc.id, func(cur *store.ServerConfig) { cur.Connection.HostKey = in.Fingerprint })
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	sc.client.SetConfig(cfg.Connection)
	sc.resetCaches()
	s.handleConnTest(w, r, u, sc)
}

// ---- copy a card to another server ----

func (s *Server) handleCopySegment(w http.ResponseWriter, r *http.Request, u *store.User, sc *serverCtx) {
	var in struct {
		Target string `json:"target"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	target := s.serverCtx(in.Target)
	if target == nil {
		writeErr(w, http.StatusBadRequest, "Choose a server to copy to.")
		return
	}
	if target == sc {
		writeErr(w, http.StatusBadRequest, "That is the same server.")
		return
	}
	// Read the card from the source draft.
	live, _ := sc.syncLive(r.Context(), false)
	sc.draftMu.Lock()
	d := sc.currentDraft(live)
	sc.draftMu.Unlock()
	doc, err := caddyfile.Parse(d.Text)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	src, err := findSegment(doc, r)
	if err != nil {
		var he *httpError
		if errors.As(err, &he) {
			writeErr(w, he.code, he.msg)
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if u.Role != store.RoleAdmin && src.Kind != caddyfile.KindSite {
		writeErr(w, http.StatusForbidden, "Power users can only copy site cards.")
		return
	}
	if src.Kind == caddyfile.KindGlobal {
		writeErr(w, http.StatusBadRequest, "Global settings can't be copied; each server has its own.")
		return
	}
	cp := src.Clone()
	cp.MarkDirty()

	// Add it to the target server's draft.
	tlive, _ := target.syncLive(r.Context(), false)
	target.draftMu.Lock()
	defer target.draftMu.Unlock()
	td := target.currentDraft(tlive)
	tdoc, err := caddyfile.Parse(td.Text)
	if err != nil {
		writeErr(w, http.StatusConflict, "The other server's draft can't be read: "+err.Error())
		return
	}
	if err := checkDuplicates(tdoc, cp, nil); err != nil {
		writeErr(w, http.StatusBadRequest, "Not copied: "+err.Error())
		return
	}
	tdoc.Append(cp)
	td.Text = tdoc.String()
	td.Rev++
	if td.Creators == nil {
		td.Creators = map[string]string{}
	}
	td.Creators[cp.Key()] = u.Username
	td.Log = append(td.Log, store.Change{Time: time.Now(), User: u.Username, Action: "copied", Target: label(cp)})
	if err := target.st.SetDraft(td); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	tcfg, _ := s.state.Server(target.id)
	writeJSON(w, map[string]any{"ok": true, "target": tcfg.Name})
}

// ---- tools ----

// handleHashPassword makes a bcrypt hash for basic_auth (Caddy accepts bcrypt).
func (s *Server) handleHashPassword(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Password == "" || len(in.Password) > 72 {
		writeErr(w, http.StatusBadRequest, "Password must be 1–72 characters.")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), 14)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"hash": string(hash)})
}
