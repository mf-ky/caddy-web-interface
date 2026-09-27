package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mf-ky/caddy-web-interface/internal/caddyfile"
	"github.com/mf-ky/caddy-web-interface/internal/diff"
	"github.com/mf-ky/caddy-web-interface/internal/remote"
	"github.com/mf-ky/caddy-web-interface/internal/store"
)

// caddyErrorResponse sends a Caddy/agent failure to the UI, pointing at the
// card that contains the offending line when possible. For non-admins every
// secret value is scrubbed, and context lines are only shown from blocks
// without secrets.
func caddyErrorResponse(w http.ResponseWriter, ce *remote.CaddyError, text string, u *store.User) {
	admin := u == nil || u.Role == store.RoleAdmin
	var secrets []string
	if !admin && text != "" {
		secrets = caddyfile.SecretValues(text)
		c := *ce
		c.Message, c.Detail = caddyfile.Scrub(c.Message, secrets), caddyfile.Scrub(c.Detail, secrets)
		ce = &c
	}
	data := map[string]any{"caddy": ce}
	if ce.Line > 0 && text != "" {
		lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
		from, to := max(ce.Line-4, 1), min(ce.Line+3, len(lines))
		if doc, err := caddyfile.Parse(text); err == nil {
			for sg, rng := range doc.Lines() {
				if ce.Line >= rng[0] && ce.Line <= rng[1] {
					data["segmentKey"] = sg.Key()
					data["segmentLabel"] = label(sg)
					if !admin {
						// only show lines of this block, and only if it holds no secrets
						if caddyfile.RedactSegment(sg).Format("\t") != sg.Format("\t") {
							from, to = 1, 0
						} else {
							from, to = max(from, rng[0]), min(to, rng[1])
						}
					}
				}
			}
		} else if !admin {
			from, to = 1, 0
		}
		var ctx []map[string]any
		for i := from; i <= to && i <= len(lines); i++ {
			ctx = append(ctx, map[string]any{"n": i, "text": lines[i-1]})
		}
		if len(ctx) > 0 {
			data["context"] = ctx
		}
	}
	code := http.StatusUnprocessableEntity
	switch ce.Kind {
	case "conflict":
		code = http.StatusConflict
	case "connection", "hostkey", "notconfigured":
		code = http.StatusBadGateway
	case "notfound":
		code = http.StatusNotFound
	}
	msg := ce.Message
	switch ce.Kind {
	case "invalid":
		msg = "Caddy rejected the configuration: " + ce.Message
	case "reload":
		msg = "Caddy could not load the new configuration: " + ce.Message
	}
	writeErrData(w, code, msg, data)
}

// agentCtx is used for calls that change the server: they must finish even
// if the browser goes away, or the agent could be stopped half-way.
func agentCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Minute)
}

func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request, u *store.User, sc *serverCtx) {
	live, _ := sc.syncLive(r.Context(), false)
	sc.draftMu.Lock()
	text := sc.currentDraft(live).Text
	sc.draftMu.Unlock()
	if strings.TrimSpace(text) == "" {
		writeErr(w, http.StatusBadRequest, "Nothing to validate.")
		return
	}
	if err := sc.client.Validate(r.Context(), text); err != nil {
		caddyErrorResponse(w, remote.Explain(err), text, u)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "message": "Caddy says the configuration is valid."})
}

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request, u *store.User, sc *serverCtx) {
	var in struct {
		Rev   int64 `json:"rev"`
		Force bool  `json:"force"` // overwrite changes made on the server
	}
	if !readJSON(w, r, &in) {
		return
	}
	live, liveErr := sc.syncLive(r.Context(), true)
	if liveErr != nil {
		caddyErrorResponse(w, liveErr, "", u)
		return
	}
	sc.draftMu.Lock()
	defer sc.draftMu.Unlock()
	// No re-reads of the live file while the agent is swapping it: a read
	// half-way could mistake a failed attempt for the new live version.
	sc.fetchMu.Lock()
	defer sc.fetchMu.Unlock()
	d := sc.currentDraft(live)
	if d.BaseSHA == "" {
		writeErr(w, http.StatusConflict, "This draft has no known starting point on the server; refresh and try again.")
		return
	}
	if in.Rev != d.Rev {
		writeErrData(w, http.StatusConflict, "The draft changed since you reviewed it. Please review again.", map[string]any{"state": sc.buildState(r.Context(), u, live, nil, d)})
		return
	}
	if shaOf(d.Text) == live.SHA {
		writeErr(w, http.StatusBadRequest, "There are no changes to apply.")
		return
	}
	expected := d.BaseSHA
	if in.Force {
		expected = live.SHA
	}
	actx, cancel := agentCtx(r)
	defer cancel()
	res, err := sc.client.Apply(actx, d.Text, expected)
	if err != nil {
		ce := remote.Explain(err)
		if ce.Kind == "conflict" {
			ce.Message = "Someone changed the Caddyfile on the server after you started editing. Review the server's changes, then either discard your draft or keep it and apply again."
		}
		_ = sc.st.AddHistory(store.ApplyRecord{Time: time.Now(), User: u.Username, Action: "apply", OK: false, Error: ce.Message, Changes: d.Log})
		caddyErrorResponse(w, ce, d.Text, u)
		return
	}
	changes := d.Log
	if in.Force {
		changes = append(changes, store.Change{Time: time.Now(), User: u.Username, Action: "overwrote changes made directly on the server", Target: "Caddyfile"})
	}
	newLive := store.Live{Text: d.Text, SHA: shaOf(d.Text), Fetched: time.Now()}
	_ = sc.st.SetLive(newLive)
	d.BaseSHA, d.Log, d.Creators = newLive.SHA, nil, map[string]string{}
	d.Rev++
	_ = sc.st.SetDraft(d)
	_ = sc.st.AddHistory(store.ApplyRecord{Time: time.Now(), User: u.Username, Action: "apply", OK: true, Backup: res.Backup, Changes: changes})
	s.afterChange(sc, res.Backup)
	st := sc.buildState(r.Context(), u, newLive, nil, d)
	writeJSON(w, map[string]any{"ok": true, "backup": res.Backup, "state": st})
}

// afterChange prunes old backups, mirrors the new one and runs the offsite
// copy. It runs in the background so Apply returns quickly.
func (s *Server) afterChange(sc *serverCtx, backup string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		keep := s.state.Settings().BackupRetention
		if err := sc.client.Prune(ctx, keep); err != nil {
			log.Printf("pruning backups: %v", err)
		}
		if backup != "" {
			if err := sc.mirrorBackup(ctx, backup); err != nil {
				log.Printf("mirroring backup %s: %v", backup, err)
			}
		}
		sc.pruneMirror(keep)
		if s.state.Settings().Offsite.Enabled {
			s.runOffsite(ctx)
		}
	}()
}

func (sc *serverCtx) mirrorDir() string { return filepath.Join(sc.st.Dir(), "backups") }

func (sc *serverCtx) mirrorBackup(ctx context.Context, name string) error {
	if !remote.ValidBackupName(name) {
		return errors.New("invalid backup name")
	}
	text, err := sc.client.BackupRead(ctx, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(sc.mirrorDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(sc.mirrorDir(), name), []byte(text), 0o600)
}

func (sc *serverCtx) pruneMirror(keep int) {
	entries, err := os.ReadDir(sc.mirrorDir())
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if remote.ValidBackupName(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool { return remote.NewerBackup(names[i], names[j]) })
	for i, n := range names {
		if i >= keep {
			_ = os.Remove(filepath.Join(sc.mirrorDir(), n))
		}
	}
}

// runOffsite copies every server's mirrored backups somewhere else with
// rsync, into one sub-folder per server.
func (s *Server) runOffsite(ctx context.Context) error {
	set := s.state.Settings().Offsite
	target := strings.TrimSpace(set.Target)
	if target == "" {
		return errors.New("no rsync destination set")
	}
	if _, err := exec.LookPath("rsync"); err != nil {
		return s.recordOffsite(errors.New("rsync is not installed on the CaddyWeb machine"))
	}
	port := set.Port
	if port == 0 {
		port = 22
	}
	sshCmd := "ssh -i " + shellQuote(remote.KeyPath(filepath.Join(s.dataDir, "ssh"))) +
		" -p " + strconv.Itoa(port) +
		" -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=" + shellQuote(filepath.Join(s.dataDir, "ssh", "known_hosts_offsite"))
	if !strings.HasSuffix(target, "/") {
		target += "/"
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for _, cfg := range s.state.Servers() {
		sc := s.serverCtx(cfg.ID)
		if sc == nil {
			continue
		}
		if err := os.MkdirAll(sc.mirrorDir(), 0o700); err != nil {
			return s.recordOffsite(err)
		}
		cmd := exec.CommandContext(ctx, "rsync", "-a", "--timeout=60", "-e", sshCmd, sc.mirrorDir()+"/", target+cfg.ID+"/")
		out, err := cmd.CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return s.recordOffsite(fmt.Errorf("%s: %s", cfg.Name, msg))
		}
	}
	return s.recordOffsite(nil)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (s *Server) recordOffsite(err error) error {
	_, _ = s.state.UpdateSettings(func(st *store.Settings) {
		st.Offsite.LastRun = time.Now().Format(time.RFC3339)
		st.Offsite.LastErr = ""
		if err != nil {
			st.Offsite.LastErr = err.Error()
		}
	})
	if err != nil {
		log.Printf("offsite backup copy: %v", err)
	}
	return err
}

// handleOffsiteRun fills in any missing local backup copies, then rsyncs.
func (s *Server) handleOffsiteRun(w http.ResponseWriter, r *http.Request, u *store.User) {
	for _, cfg := range s.state.Servers() {
		sc := s.serverCtx(cfg.ID)
		if sc == nil || !sc.client.Config().Configured() {
			continue
		}
		if list, err := sc.client.Backups(r.Context()); err == nil {
			for _, b := range list {
				if _, err := os.Stat(filepath.Join(sc.mirrorDir(), b.Name)); err != nil {
					_ = sc.mirrorBackup(r.Context(), b.Name)
				}
			}
		}
	}
	if err := s.runOffsite(r.Context()); err != nil {
		writeErr(w, http.StatusBadGateway, "rsync failed: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "offsite": s.state.Settings().Offsite})
}

// ---- backups ----

func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request, u *store.User, sc *serverCtx) {
	list, err := sc.client.Backups(r.Context())
	if err != nil {
		caddyErrorResponse(w, remote.Explain(err), "", u)
		return
	}
	who := map[string]store.ApplyRecord{}
	for _, h := range sc.st.History() {
		if h.Backup != "" {
			who[h.Backup] = h
		}
	}
	type item struct {
		remote.Backup
		ReplacedBy string `json:"replacedBy,omitempty"` // who applied the change that created this backup
		Action     string `json:"action,omitempty"`
		Mirrored   bool   `json:"mirrored"`
	}
	items := []item{}
	for _, b := range list {
		it := item{Backup: b}
		if h, ok := who[b.Name]; ok {
			it.ReplacedBy, it.Action = h.User, h.Action
		}
		if _, err := os.Stat(filepath.Join(sc.mirrorDir(), b.Name)); err == nil {
			it.Mirrored = true
		}
		items = append(items, it)
	}
	dir := ""
	if info := sc.cachedInfo(r.Context()); info != nil {
		dir = info.BackupDir
	}
	set := s.state.Settings()
	out := map[string]any{"backups": items, "retention": set.BackupRetention, "dir": dir}
	if u.Role == store.RoleAdmin {
		out["mirrorDir"], out["offsite"] = sc.mirrorDir(), set.Offsite
	}
	writeJSON(w, out)
}

func (s *Server) handleBackupRead(w http.ResponseWriter, r *http.Request, u *store.User, sc *serverCtx) {
	name := r.PathValue("name")
	if !remote.ValidBackupName(name) {
		writeErr(w, http.StatusBadRequest, "That is not a valid backup name.")
		return
	}
	text, err := sc.client.BackupRead(r.Context(), name)
	if err != nil {
		caddyErrorResponse(w, remote.Explain(err), "", u)
		return
	}
	live, _ := sc.syncLive(r.Context(), false)
	cur := live.Text
	if u.Role != store.RoleAdmin {
		text, cur = caddyfile.RedactText(text), caddyfile.RedactText(cur)
	}
	lines := diff.Lines(cur, text)
	add, rem := diff.Stats(lines)
	writeJSON(w, map[string]any{"name": name, "text": text, "diff": diff.Hunks(lines, 3), "added": add, "removed": rem})
}

func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request, u *store.User, sc *serverCtx) {
	name := r.PathValue("name")
	if !remote.ValidBackupName(name) {
		writeErr(w, http.StatusBadRequest, "That is not a valid backup name.")
		return
	}
	text, err := sc.client.BackupRead(r.Context(), name)
	if err != nil {
		writeErr(w, http.StatusBadGateway, remote.Explain(err).Message)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write([]byte(text))
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request, u *store.User, sc *serverCtx) {
	name := r.PathValue("name")
	if !remote.ValidBackupName(name) {
		writeErr(w, http.StatusBadRequest, "That is not a valid backup name.")
		return
	}
	var in struct {
		DiscardDraft bool `json:"discardDraft"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	live, liveErr := sc.syncLive(r.Context(), true)
	if liveErr != nil {
		caddyErrorResponse(w, liveErr, "", u)
		return
	}
	sc.draftMu.Lock()
	defer sc.draftMu.Unlock()
	sc.fetchMu.Lock()
	defer sc.fetchMu.Unlock()
	d := sc.currentDraft(live)
	if shaOf(d.Text) != live.SHA && !in.DiscardDraft {
		writeErrData(w, http.StatusConflict, "You have unapplied changes. Restoring a backup will discard them.", map[string]any{"needsDiscard": true})
		return
	}
	text, err := sc.client.BackupRead(r.Context(), name)
	if err != nil {
		caddyErrorResponse(w, remote.Explain(err), "", u)
		return
	}
	actx, cancel := agentCtx(r)
	defer cancel()
	res, err := sc.client.Restore(actx, name, live.SHA)
	if err != nil {
		ce := remote.Explain(err)
		_ = sc.st.AddHistory(store.ApplyRecord{Time: time.Now(), User: u.Username, Action: "restore", OK: false, Error: ce.Message, Note: name})
		caddyErrorResponse(w, ce, text, u)
		return
	}
	newLive := store.Live{Text: text, SHA: shaOf(text), Fetched: time.Now()}
	_ = sc.st.SetLive(newLive)
	d = store.Draft{Text: text, BaseSHA: newLive.SHA, Rev: d.Rev + 1, Creators: map[string]string{}}
	_ = sc.st.SetDraft(d)
	_ = sc.st.AddHistory(store.ApplyRecord{Time: time.Now(), User: u.Username, Action: "restore", OK: true, Backup: res.Backup, Note: name})
	s.afterChange(sc, res.Backup)
	writeJSON(w, map[string]any{"ok": true, "backup": res.Backup, "state": sc.buildState(r.Context(), u, newLive, nil, d)})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request, u *store.User, sc *serverCtx) {
	h := sc.st.History()
	if h == nil {
		h = []store.ApplyRecord{}
	}
	if u.Role != store.RoleAdmin {
		secrets := append(caddyfile.SecretValues(sc.st.Live().Text), caddyfile.SecretValues(sc.st.Draft().Text)...)
		for i := range h {
			h[i].Error = caddyfile.Scrub(h[i].Error, secrets)
		}
	}
	writeJSON(w, map[string]any{"history": h})
}

func (s *Server) handleDNSProviders(w http.ResponseWriter, r *http.Request, u *store.User, sc *serverCtx) {
	sc.cacheMu.Lock()
	if sc.dns != nil && time.Since(sc.dnsAt) < 5*time.Minute && r.URL.Query().Get("refresh") != "1" {
		list := sc.dns
		sc.cacheMu.Unlock()
		writeJSON(w, map[string]any{"installed": list})
		return
	}
	sc.cacheMu.Unlock()
	list, err := sc.client.DNSProviders(r.Context())
	if err != nil {
		writeJSON(w, map[string]any{"installed": []string{}, "error": remote.Explain(err).Message})
		return
	}
	if list == nil {
		list = []string{}
	}
	sc.cacheMu.Lock()
	sc.dns, sc.dnsAt = list, time.Now()
	sc.cacheMu.Unlock()
	writeJSON(w, map[string]any{"installed": list})
}
