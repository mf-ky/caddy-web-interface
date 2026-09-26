package server

import (
	"context"
	"errors"
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
// card that contains the offending line when possible.
func caddyErrorResponse(w http.ResponseWriter, ce *remote.CaddyError, text string) {
	data := map[string]any{"caddy": ce}
	if ce.Line > 0 && text != "" {
		lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
		from, to := max(ce.Line-4, 1), min(ce.Line+3, len(lines))
		var ctx []map[string]any
		for i := from; i <= to; i++ {
			ctx = append(ctx, map[string]any{"n": i, "text": lines[i-1]})
		}
		data["context"] = ctx
		if doc, err := caddyfile.Parse(text); err == nil {
			for sg, rng := range doc.Lines() {
				if ce.Line >= rng[0] && ce.Line <= rng[1] {
					data["segmentKey"] = sg.Key()
					data["segmentLabel"] = label(sg)
				}
			}
		}
	}
	code := http.StatusUnprocessableEntity
	switch ce.Kind {
	case "conflict":
		code = http.StatusConflict
	case "connection", "hostkey", "notconfigured":
		code = http.StatusBadGateway
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

func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request, u *store.User) {
	s.draftMu.Lock()
	text := s.state.Draft().Text
	s.draftMu.Unlock()
	if strings.TrimSpace(text) == "" {
		writeErr(w, http.StatusBadRequest, "Nothing to validate.")
		return
	}
	if err := s.client.Validate(r.Context(), text); err != nil {
		caddyErrorResponse(w, remote.Explain(err), text)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "message": "Caddy says the configuration is valid."})
}

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		Rev   int64 `json:"rev"`
		Force bool  `json:"force"` // overwrite changes made on the server
	}
	if !readJSON(w, r, &in) {
		return
	}
	live, liveErr := s.syncLive(r.Context(), true)
	if liveErr != nil {
		caddyErrorResponse(w, liveErr, "")
		return
	}
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	d := s.currentDraft(live)
	if in.Rev != d.Rev {
		writeErrData(w, http.StatusConflict, "The draft changed since you reviewed it. Please review again.", map[string]any{"state": s.buildState(r.Context(), u, live, nil, d)})
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
	res, err := s.client.Apply(r.Context(), d.Text, expected)
	if err != nil {
		ce := remote.Explain(err)
		if ce.Kind == "conflict" {
			ce.Message = "Someone changed the Caddyfile on the server after you started editing. Review the server's changes, then either discard your draft or keep it and apply again."
		}
		_ = s.state.AddHistory(store.ApplyRecord{Time: time.Now(), User: u.Username, Action: "apply", OK: false, Error: ce.Message, Changes: d.Log})
		caddyErrorResponse(w, ce, d.Text)
		return
	}
	changes := d.Log
	newLive := store.Live{Text: d.Text, SHA: shaOf(d.Text), Fetched: time.Now()}
	_ = s.state.SetLive(newLive)
	d.BaseSHA, d.Log, d.Creators = newLive.SHA, nil, map[string]string{}
	d.Rev++
	_ = s.state.SetDraft(d)
	_ = s.state.AddHistory(store.ApplyRecord{Time: time.Now(), User: u.Username, Action: "apply", OK: true, Backup: res.Backup, Changes: changes})
	s.afterChange(res.Backup)
	st := s.buildState(r.Context(), u, newLive, nil, d)
	writeJSON(w, map[string]any{"ok": true, "backup": res.Backup, "state": st})
}

// afterChange prunes old backups, mirrors the new one and runs the offsite
// copy. It runs in the background so Apply returns quickly.
func (s *Server) afterChange(backup string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		keep := s.state.Settings().BackupRetention
		if err := s.client.Prune(ctx, keep); err != nil {
			log.Printf("pruning backups: %v", err)
		}
		if backup != "" {
			if err := s.mirrorBackup(ctx, backup); err != nil {
				log.Printf("mirroring backup %s: %v", backup, err)
			}
		}
		s.pruneMirror(keep)
		if s.state.Settings().Offsite.Enabled {
			s.runOffsite(ctx)
		}
	}()
}

func (s *Server) mirrorDir() string { return filepath.Join(s.dataDir, "backups") }

func (s *Server) mirrorBackup(ctx context.Context, name string) error {
	if !remote.ValidBackupName(name) {
		return errors.New("invalid backup name")
	}
	text, err := s.client.BackupRead(ctx, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.mirrorDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.mirrorDir(), name), []byte(text), 0o600)
}

func (s *Server) pruneMirror(keep int) {
	entries, err := os.ReadDir(s.mirrorDir())
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if remote.ValidBackupName(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for i, n := range names {
		if i >= keep {
			_ = os.Remove(filepath.Join(s.mirrorDir(), n))
		}
	}
}

// runOffsite copies the mirrored backups somewhere else with rsync.
func (s *Server) runOffsite(ctx context.Context) error {
	set := s.state.Settings().Offsite
	if strings.TrimSpace(set.Target) == "" {
		return errors.New("no rsync destination set")
	}
	if _, err := exec.LookPath("rsync"); err != nil {
		return s.recordOffsite(errors.New("rsync is not installed on the CaddyWeb machine"))
	}
	if err := os.MkdirAll(s.mirrorDir(), 0o700); err != nil {
		return s.recordOffsite(err)
	}
	port := set.Port
	if port == 0 {
		port = 22
	}
	sshCmd := "ssh -i " + shellQuote(remote.KeyPath(filepath.Join(s.dataDir, "ssh"))) +
		" -p " + strconv.Itoa(port) +
		" -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=" + shellQuote(filepath.Join(s.dataDir, "ssh", "known_hosts_offsite"))
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "rsync", "-a", "--timeout=60", "-e", sshCmd, s.mirrorDir()+"/", set.Target)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return s.recordOffsite(errors.New(msg))
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

func (s *Server) handleOffsiteRun(w http.ResponseWriter, r *http.Request, u *store.User) {
	// make sure the local mirror has everything the server has
	if list, err := s.client.Backups(r.Context()); err == nil {
		for _, b := range list {
			if _, err := os.Stat(filepath.Join(s.mirrorDir(), b.Name)); err != nil {
				_ = s.mirrorBackup(r.Context(), b.Name)
			}
		}
	}
	if err := s.runOffsite(r.Context()); err != nil {
		writeErr(w, http.StatusBadGateway, "rsync failed: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "settings": s.state.Settings().Offsite})
}

// ---- backups ----

func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request, u *store.User) {
	list, err := s.client.Backups(r.Context())
	if err != nil {
		caddyErrorResponse(w, remote.Explain(err), "")
		return
	}
	who := map[string]store.ApplyRecord{}
	for _, h := range s.state.History() {
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
		if _, err := os.Stat(filepath.Join(s.mirrorDir(), b.Name)); err == nil {
			it.Mirrored = true
		}
		items = append(items, it)
	}
	dir := ""
	if info := s.cachedInfo(r.Context()); info != nil {
		dir = info.BackupDir
	}
	set := s.state.Settings()
	writeJSON(w, map[string]any{
		"backups": items, "retention": set.BackupRetention, "dir": dir,
		"mirrorDir": s.mirrorDir(), "offsite": set.Offsite,
	})
}

func (s *Server) handleBackupRead(w http.ResponseWriter, r *http.Request, u *store.User) {
	name := r.PathValue("name")
	text, err := s.client.BackupRead(r.Context(), name)
	if err != nil {
		caddyErrorResponse(w, remote.Explain(err), "")
		return
	}
	live, _ := s.syncLive(r.Context(), false)
	cur := live.Text
	if u.Role != store.RoleAdmin {
		text, cur = caddyfile.RedactText(text), caddyfile.RedactText(cur)
	}
	lines := diff.Lines(cur, text)
	add, rem := diff.Stats(lines)
	writeJSON(w, map[string]any{"name": name, "text": text, "diff": diff.Hunks(lines, 3), "added": add, "removed": rem})
}

func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request, u *store.User) {
	name := r.PathValue("name")
	text, err := s.client.BackupRead(r.Context(), name)
	if err != nil {
		writeErr(w, http.StatusBadGateway, remote.Explain(err).Message)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write([]byte(text))
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request, u *store.User) {
	name := r.PathValue("name")
	var in struct {
		DiscardDraft bool `json:"discardDraft"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	live, liveErr := s.syncLive(r.Context(), true)
	if liveErr != nil {
		caddyErrorResponse(w, liveErr, "")
		return
	}
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	d := s.currentDraft(live)
	if shaOf(d.Text) != live.SHA && !in.DiscardDraft {
		writeErrData(w, http.StatusConflict, "You have unapplied changes. Restoring a backup will discard them.", map[string]any{"needsDiscard": true})
		return
	}
	text, err := s.client.BackupRead(r.Context(), name)
	if err != nil {
		caddyErrorResponse(w, remote.Explain(err), "")
		return
	}
	res, err := s.client.Restore(r.Context(), name, live.SHA)
	if err != nil {
		ce := remote.Explain(err)
		_ = s.state.AddHistory(store.ApplyRecord{Time: time.Now(), User: u.Username, Action: "restore", OK: false, Error: ce.Message, Note: name})
		caddyErrorResponse(w, ce, text)
		return
	}
	newLive := store.Live{Text: text, SHA: shaOf(text), Fetched: time.Now()}
	_ = s.state.SetLive(newLive)
	d = store.Draft{Text: text, BaseSHA: newLive.SHA, Rev: d.Rev + 1, Creators: map[string]string{}}
	_ = s.state.SetDraft(d)
	_ = s.state.AddHistory(store.ApplyRecord{Time: time.Now(), User: u.Username, Action: "restore", OK: true, Backup: res.Backup, Note: name})
	s.afterChange(res.Backup)
	writeJSON(w, map[string]any{"ok": true, "backup": res.Backup, "state": s.buildState(r.Context(), u, newLive, nil, d)})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request, u *store.User) {
	writeJSON(w, map[string]any{"history": s.state.History()})
}

func (s *Server) handleDNSProviders(w http.ResponseWriter, r *http.Request, u *store.User) {
	s.cacheMu.Lock()
	if s.dns != nil && time.Since(s.dnsAt) < 5*time.Minute && r.URL.Query().Get("refresh") != "1" {
		list := s.dns
		s.cacheMu.Unlock()
		writeJSON(w, map[string]any{"installed": list})
		return
	}
	s.cacheMu.Unlock()
	list, err := s.client.DNSProviders(r.Context())
	if err != nil {
		writeJSON(w, map[string]any{"installed": []string{}, "error": remote.Explain(err).Message})
		return
	}
	if list == nil {
		list = []string{}
	}
	s.cacheMu.Lock()
	s.dns, s.dnsAt = list, time.Now()
	s.cacheMu.Unlock()
	writeJSON(w, map[string]any{"installed": list})
}
