package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mf-ky/caddy-web-interface/internal/caddyfile"
	"github.com/mf-ky/caddy-web-interface/internal/diff"
	"github.com/mf-ky/caddy-web-interface/internal/remote"
	"github.com/mf-ky/caddy-web-interface/internal/store"
)

func shaOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// syncLive reads the Caddyfile from the server (at most every few seconds
// unless force is set) and caches it.
func (s *Server) syncLive(ctx context.Context, force bool) (store.Live, *remote.CaddyError) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	cached := s.state.Live()
	if !s.client.Config().Configured() {
		return cached, &remote.CaddyError{Kind: "notconfigured", Message: "CaddyWeb is not connected to a Caddy server yet."}
	}
	if !force && time.Since(s.lastFetch) < 3*time.Second {
		return cached, s.liveErr
	}
	s.lastFetch = time.Now()
	text, err := s.client.Read(ctx)
	if err != nil {
		s.liveErr = remote.Explain(err)
		return cached, s.liveErr
	}
	live := store.Live{Text: text, SHA: shaOf(text), Fetched: time.Now()}
	_ = s.state.SetLive(live)
	s.liveErr = nil
	return live, nil
}

// currentDraft reconciles the saved draft with the live file. Callers must
// hold draftMu.
func (s *Server) currentDraft(live store.Live) store.Draft {
	d := s.state.Draft()
	if live.SHA == "" {
		return d
	}
	changed := false
	switch {
	case d.Text == "" && d.BaseSHA == "":
		// first run: start from the live file
		d.Text, d.BaseSHA, changed = live.Text, live.SHA, true
	case shaOf(d.Text) == live.SHA && (d.BaseSHA != live.SHA || len(d.Log) > 0 || len(d.Creators) > 0):
		// draft matches the server (applied, or edited to the same result)
		d.BaseSHA, changed = live.SHA, true
		d.Log, d.Creators = nil, map[string]string{}
	case d.BaseSHA != live.SHA && shaOf(d.Text) == d.BaseSHA:
		// no pending edits, but the file changed on the server (e.g. from the
		// CLI): follow it
		d.Text, d.BaseSHA, changed = live.Text, live.SHA, true
		d.Log, d.Creators = nil, map[string]string{}
	}
	if changed {
		d.Rev++
		_ = s.state.SetDraft(d)
	}
	return d
}

// ---- state view ----

type segView struct {
	ID            int                   `json:"id"`
	Key           string                `json:"key"`
	Kind          caddyfile.SegmentKind `json:"kind"`
	Comments      []string              `json:"comments"`
	Header        []string              `json:"header"`
	HeaderComment string                `json:"headerComment"`
	Nodes         []*caddyfile.Node     `json:"nodes"`
	Text          string                `json:"text"`
	Status        string                `json:"status"` // unchanged | new | modified | deleted
	CreatedBy     string                `json:"createdBy,omitempty"`
	StartLine     int                   `json:"startLine"`
	EndLine       int                   `json:"endLine"`
	CanEdit       bool                  `json:"canEdit"`
	CanDelete     bool                  `json:"canDelete"`
}

type connView struct {
	Configured bool                  `json:"configured"`
	OK         bool                  `json:"ok"`
	Mode       string                `json:"mode"`
	Host       string                `json:"host"`
	Error      *remote.CaddyError    `json:"error,omitempty"`
	Info       *remote.Info          `json:"info,omitempty"`
	Fetched    time.Time             `json:"fetched"`
}

type stateView struct {
	Rev        int64          `json:"rev"`
	Segments   []segView      `json:"segments"`
	Deleted    []segView      `json:"deleted"`
	HasChanges bool           `json:"hasChanges"`
	Conflict   bool           `json:"conflict"`
	ParseError string         `json:"parseError,omitempty"`
	Connection connView       `json:"connection"`
	Log        []store.Change `json:"log"`
	Domains    []string       `json:"domains"`
	Indent     string         `json:"indent"`
	Retention  int            `json:"retention"`
}

func (s *Server) cachedInfo(ctx context.Context) *remote.Info {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.info != nil && time.Since(s.infoAt) < time.Minute {
		return s.info
	}
	info, err := s.client.Info(ctx)
	if err == nil {
		s.info, s.infoAt = info, time.Now()
	}
	return s.info
}

func (s *Server) buildState(ctx context.Context, u *store.User, live store.Live, liveErr *remote.CaddyError, d store.Draft) stateView {
	cfg := s.client.Config()
	v := stateView{
		Rev: d.Rev, Log: d.Log, Segments: []segView{}, Deleted: []segView{}, Domains: []string{},
		Retention: s.state.Settings().BackupRetention,
		Connection: connView{
			Configured: cfg.Configured(), OK: liveErr == nil && live.SHA != "", Mode: cfg.Mode, Host: cfg.Host,
			Error: liveErr, Fetched: live.Fetched,
		},
	}
	if v.Log == nil {
		v.Log = []store.Change{}
	}
	if v.Connection.OK {
		v.Connection.Info = s.cachedInfo(ctx)
	}
	if d.Text == "" && live.SHA == "" {
		return v
	}
	v.HasChanges = shaOf(d.Text) != live.SHA && live.SHA != ""
	v.Conflict = v.HasChanges && d.BaseSHA != live.SHA

	doc, err := caddyfile.Parse(d.Text)
	if err != nil {
		v.ParseError = err.Error()
		return v
	}
	v.Indent = doc.Indent
	liveSegs := map[string]*caddyfile.Segment{}
	var liveOrder []*caddyfile.Segment
	if ldoc, err := caddyfile.Parse(live.Text); err == nil {
		for _, sg := range ldoc.Segments() {
			liveSegs[sg.Key()] = sg
			liveOrder = append(liveOrder, sg)
		}
	}
	lines := doc.Lines()
	isAdmin := u.Role == store.RoleAdmin
	present := map[string]bool{}
	domains := map[string]int{}
	for i, sg := range doc.Segments() {
		key := sg.Key()
		present[key] = true
		sv := s.segToView(sg, doc.Indent, isAdmin)
		sv.ID, sv.StartLine, sv.EndLine = i, lines[sg][0], lines[sg][1]
		sv.CreatedBy = d.Creators[key]
		if ls, ok := liveSegs[key]; !ok {
			sv.Status = "new"
		} else if ls.Text(doc.Indent) != sg.Text(doc.Indent) {
			sv.Status = "modified"
		} else {
			sv.Status = "unchanged"
		}
		if live.SHA == "" {
			sv.Status = "unchanged"
		}
		sv.CanEdit = isAdmin || (u.Role == store.RolePower && sv.Status == "new" && sv.CreatedBy == u.Username)
		sv.CanDelete = isAdmin
		v.Segments = append(v.Segments, sv)
		if sg.Kind == caddyfile.KindSite {
			for _, a := range sg.Addresses() {
				if b := baseDomain(a); b != "" {
					domains[b]++
				}
			}
		}
	}
	for _, ls := range liveOrder {
		if !present[ls.Key()] {
			sv := s.segToView(ls, doc.Indent, isAdmin)
			sv.Status, sv.ID = "deleted", -1
			v.Deleted = append(v.Deleted, sv)
		}
	}
	for dmn := range domains {
		v.Domains = append(v.Domains, dmn)
	}
	sort.Slice(v.Domains, func(i, j int) bool {
		if domains[v.Domains[i]] != domains[v.Domains[j]] {
			return domains[v.Domains[i]] > domains[v.Domains[j]]
		}
		return v.Domains[i] < v.Domains[j]
	})
	return v
}

func (s *Server) segToView(sg *caddyfile.Segment, indent string, isAdmin bool) segView {
	shown := sg
	if !isAdmin {
		shown = caddyfile.RedactSegment(sg)
	}
	text := shown.Text(indent)
	return segView{
		Key: sg.Key(), Kind: sg.Kind, Comments: nonNil(sg.Comments), Header: nonNil(sg.Header),
		HeaderComment: sg.HeaderComment, Nodes: shown.Nodes, Text: text,
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// baseDomain turns "https://music.example.com:443" into "example.com".
func baseDomain(addr string) string {
	a := addr
	if i := strings.Index(a, "://"); i >= 0 {
		a = a[i+3:]
	}
	if i := strings.IndexAny(a, "/"); i >= 0 {
		a = a[:i]
	}
	if h, _, err := net.SplitHostPort(a); err == nil {
		a = h
	}
	a = strings.TrimPrefix(a, "*.")
	if a == "" || net.ParseIP(a) != nil || !strings.Contains(a, ".") || strings.ContainsAny(a, "{}") {
		return ""
	}
	parts := strings.Split(a, ".")
	return strings.Join(parts[len(parts)-2:], ".")
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request, u *store.User) {
	force := r.URL.Query().Get("refresh") == "1"
	live, liveErr := s.syncLive(r.Context(), force)
	s.draftMu.Lock()
	d := s.currentDraft(live)
	s.draftMu.Unlock()
	writeJSON(w, s.buildState(r.Context(), u, live, liveErr, d))
}

// ---- mutations ----

type httpError struct {
	code int
	msg  string
	data map[string]any
}

func (e *httpError) Error() string { return e.msg }

func badRequest(format string, a ...any) error {
	return &httpError{code: http.StatusBadRequest, msg: fmt.Sprintf(format, a...)}
}

func forbidden(msg string) error { return &httpError{code: http.StatusForbidden, msg: msg} }

// mutate runs fn against a parsed copy of the draft and saves the result.
func (s *Server) mutate(w http.ResponseWriter, r *http.Request, u *store.User, fn func(doc *caddyfile.Document, d *store.Draft, live store.Live) (store.Change, error)) {
	rev, _ := strconv.ParseInt(r.URL.Query().Get("rev"), 10, 64)
	live, liveErr := s.syncLive(r.Context(), false)

	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	d := s.currentDraft(live)
	if rev != d.Rev {
		writeErrData(w, http.StatusConflict, "The draft was changed somewhere else (another user or browser tab). Your view has been refreshed — please try again.", map[string]any{"state": s.buildState(r.Context(), u, live, liveErr, d)})
		return
	}
	doc, err := caddyfile.Parse(d.Text)
	if err != nil {
		writeErr(w, http.StatusConflict, "The draft Caddyfile can't be read ("+err.Error()+"). An admin can fix it on the Caddyfile page.")
		return
	}
	change, err := fn(doc, &d, live)
	if err != nil {
		var he *httpError
		if errors.As(err, &he) {
			writeErrData(w, he.code, he.msg, he.data)
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d.Text = doc.String()
	d.Rev++
	change.Time, change.User = time.Now(), u.Username
	if change.Action != "" {
		d.Log = append(d.Log, change)
	}
	if shaOf(d.Text) == live.SHA {
		d.Log, d.Creators = nil, map[string]string{}
	}
	if d.Creators == nil {
		d.Creators = map[string]string{}
	}
	if err := s.state.SetDraft(d); err != nil {
		writeErr(w, http.StatusInternalServerError, "saving draft: "+err.Error())
		return
	}
	writeJSON(w, s.buildState(r.Context(), u, live, liveErr, d))
}

func findSegment(doc *caddyfile.Document, r *http.Request) (*caddyfile.Segment, error) {
	id, err := strconv.Atoi(r.PathValue("id"))
	segs := doc.Segments()
	if err != nil || id < 0 || id >= len(segs) {
		return nil, &httpError{code: http.StatusConflict, msg: "That card no longer exists. Please refresh."}
	}
	if key := r.URL.Query().Get("key"); key != "" && segs[id].Key() != key {
		return nil, &httpError{code: http.StatusConflict, msg: "The cards changed while you were editing. Please refresh."}
	}
	return segs[id], nil
}

// label names a segment for the change log.
func label(sg *caddyfile.Segment) string {
	switch sg.Kind {
	case caddyfile.KindGlobal:
		return "Global options"
	case caddyfile.KindSite:
		return strings.Join(sg.Addresses(), ", ")
	}
	return strings.Join(sg.Header, " ")
}

// checkDuplicates refuses two site blocks claiming the same address.
func checkDuplicates(doc *caddyfile.Document, sg *caddyfile.Segment, except *caddyfile.Segment) error {
	if sg.Kind != caddyfile.KindSite && sg.Kind != caddyfile.KindSnippet {
		return nil
	}
	mine := map[string]bool{}
	for _, a := range sg.Addresses() {
		mine[strings.ToLower(a)] = true
	}
	for _, other := range doc.Segments() {
		if other == except || other.Kind != sg.Kind {
			continue
		}
		for _, a := range other.Addresses() {
			if mine[strings.ToLower(a)] {
				return badRequest("%s is already used by another card (%s).", a, label(other))
			}
		}
	}
	return nil
}

type segmentInput struct {
	Segment caddyfile.Segment `json:"segment"`
	Text    string            `json:"text"`
}

func (s *Server) handleAddSegment(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in segmentInput
	if !readJSON(w, r, &in) {
		return
	}
	s.mutate(w, r, u, func(doc *caddyfile.Document, d *store.Draft, live store.Live) (store.Change, error) {
		sg := &in.Segment
		if in.Text != "" {
			parsed, err := caddyfile.ParseSegment(in.Text)
			if err != nil {
				return store.Change{}, badRequest("%v", err)
			}
			sg = parsed
		}
		if u.Role != store.RoleAdmin && sg.Kind != caddyfile.KindSite {
			return store.Change{}, forbidden("Power users can only add site cards.")
		}
		if err := caddyfile.CheckSegment(sg); err != nil {
			return store.Change{}, badRequest("%v", err)
		}
		if err := checkDuplicates(doc, sg, nil); err != nil {
			return store.Change{}, err
		}
		sg.MarkDirty()
		if sg.Kind == caddyfile.KindGlobal {
			if doc.Global() != nil {
				return store.Change{}, badRequest("There is already a global options block.")
			}
			doc.Prepend(sg)
		} else {
			doc.Append(sg)
		}
		d.Creators[sg.Key()] = u.Username
		return store.Change{Action: "added", Target: label(sg)}, nil
	})
}

// canEdit enforces "power users may only touch cards they added in this draft".
func canEdit(u *store.User, sg *caddyfile.Segment, d *store.Draft, live store.Live) error {
	if u.Role == store.RoleAdmin {
		return nil
	}
	if d.Creators[sg.Key()] != u.Username {
		return forbidden("Power users can only edit cards they added themselves, until an admin applies them.")
	}
	if ldoc, err := caddyfile.Parse(live.Text); err == nil {
		for _, ls := range ldoc.Segments() {
			if ls.Key() == sg.Key() {
				return forbidden("This card is already live; only an admin can change it.")
			}
		}
	}
	return nil
}

func (s *Server) replaceSegment(doc *caddyfile.Document, d *store.Draft, u *store.User, old, sg *caddyfile.Segment) (store.Change, error) {
	if sg.Kind != old.Kind && u.Role != store.RoleAdmin {
		return store.Change{}, forbidden("You can't change the type of this block.")
	}
	if u.Role != store.RoleAdmin && sg.Kind != caddyfile.KindSite {
		return store.Change{}, forbidden("Power users can only edit site cards.")
	}
	if sg.Kind == caddyfile.KindGlobal && old.Kind != caddyfile.KindGlobal {
		return store.Change{}, badRequest("Global options must be the first block; edit the existing one instead.")
	}
	if err := checkDuplicates(doc, sg, old); err != nil {
		return store.Change{}, err
	}
	if sg.Format(doc.Indent) == old.Format(doc.Indent) && old.Braces {
		return store.Change{}, nil // nothing changed: keep the original bytes
	}
	sg.MarkDirty()
	doc.Replace(old, sg)
	if creator, ok := d.Creators[old.Key()]; ok {
		delete(d.Creators, old.Key())
		d.Creators[sg.Key()] = creator
	}
	return store.Change{Action: "edited", Target: label(sg)}, nil
}

func (s *Server) handleUpdateSegment(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in segmentInput
	if !readJSON(w, r, &in) {
		return
	}
	s.mutate(w, r, u, func(doc *caddyfile.Document, d *store.Draft, live store.Live) (store.Change, error) {
		old, err := findSegment(doc, r)
		if err != nil {
			return store.Change{}, err
		}
		if err := canEdit(u, old, d, live); err != nil {
			return store.Change{}, err
		}
		sg := &in.Segment
		if err := caddyfile.CheckSegment(sg); err != nil {
			return store.Change{}, badRequest("%v", err)
		}
		return s.replaceSegment(doc, d, u, old, sg)
	})
}

func (s *Server) handleUpdateSegmentRaw(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in segmentInput
	if !readJSON(w, r, &in) {
		return
	}
	s.mutate(w, r, u, func(doc *caddyfile.Document, d *store.Draft, live store.Live) (store.Change, error) {
		old, err := findSegment(doc, r)
		if err != nil {
			return store.Change{}, err
		}
		if err := canEdit(u, old, d, live); err != nil {
			return store.Change{}, err
		}
		sg, err := caddyfile.ParseSegment(in.Text)
		if err != nil {
			return store.Change{}, badRequest("%v", err)
		}
		if old.Kind == caddyfile.KindGlobal && sg.Kind != caddyfile.KindGlobal {
			return store.Change{}, badRequest("The global options block must start with '{' on its own.")
		}
		return s.replaceSegment(doc, d, u, old, sg)
	})
}

func (s *Server) handleDeleteSegment(w http.ResponseWriter, r *http.Request, u *store.User) {
	s.mutate(w, r, u, func(doc *caddyfile.Document, d *store.Draft, live store.Live) (store.Change, error) {
		sg, err := findSegment(doc, r)
		if err != nil {
			return store.Change{}, err
		}
		doc.Remove(sg)
		delete(d.Creators, sg.Key())
		return store.Change{Action: "deleted", Target: label(sg)}, nil
	})
}

func liveSegment(live store.Live, key string) (*caddyfile.Segment, *caddyfile.Document) {
	ldoc, err := caddyfile.Parse(live.Text)
	if err != nil {
		return nil, nil
	}
	for _, ls := range ldoc.Segments() {
		if ls.Key() == key {
			return ls, ldoc
		}
	}
	return nil, ldoc
}

func (s *Server) handleRevertSegment(w http.ResponseWriter, r *http.Request, u *store.User) {
	s.mutate(w, r, u, func(doc *caddyfile.Document, d *store.Draft, live store.Live) (store.Change, error) {
		sg, err := findSegment(doc, r)
		if err != nil {
			return store.Change{}, err
		}
		ls, _ := liveSegment(live, sg.Key())
		if ls == nil {
			doc.Remove(sg)
			delete(d.Creators, sg.Key())
		} else {
			doc.Replace(sg, ls)
		}
		return store.Change{Action: "reverted", Target: label(sg)}, nil
	})
}

func (s *Server) handleRestoreDeleted(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		Key string `json:"key"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	s.mutate(w, r, u, func(doc *caddyfile.Document, d *store.Draft, live store.Live) (store.Change, error) {
		ls, ldoc := liveSegment(live, in.Key)
		if ls == nil {
			return store.Change{}, badRequest("That block is not in the live Caddyfile.")
		}
		for _, sg := range doc.Segments() {
			if sg.Key() == in.Key {
				return store.Change{}, badRequest("That block is already in the draft.")
			}
		}
		if ls.Kind == caddyfile.KindGlobal {
			doc.Prepend(ls)
			return store.Change{Action: "restored", Target: label(ls)}, nil
		}
		// put it back after the block that preceded it on the server
		var after *caddyfile.Segment
		draftByKey := map[string]*caddyfile.Segment{}
		for _, sg := range doc.Segments() {
			draftByKey[sg.Key()] = sg
		}
		for _, prev := range ldoc.Segments() {
			if prev == ls {
				break
			}
			if sg, ok := draftByKey[prev.Key()]; ok {
				after = sg
			}
		}
		doc.InsertAfter(after, ls)
		return store.Change{Action: "restored", Target: label(ls)}, nil
	})
}

func (s *Server) handleDraftRaw(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in segmentInput
	if !readJSON(w, r, &in) {
		return
	}
	s.mutate(w, r, u, func(doc *caddyfile.Document, d *store.Draft, live store.Live) (store.Change, error) {
		if strings.TrimSpace(in.Text) == "" {
			return store.Change{}, badRequest("The Caddyfile can't be empty.")
		}
		nd, err := caddyfile.Parse(in.Text)
		if err != nil {
			line := 0
			var se *caddyfile.SyntaxError
			if errors.As(err, &se) {
				line = se.Line
			}
			return store.Change{}, &httpError{code: http.StatusBadRequest, msg: err.Error(), data: map[string]any{"line": line}}
		}
		*doc = *nd
		d.Creators = map[string]string{}
		return store.Change{Action: "raw-edit", Target: "Caddyfile"}, nil
	})
}

func (s *Server) handleDiscard(w http.ResponseWriter, r *http.Request, u *store.User) {
	s.mutate(w, r, u, func(doc *caddyfile.Document, d *store.Draft, live store.Live) (store.Change, error) {
		if live.SHA == "" {
			return store.Change{}, badRequest("Can't discard without a connection to the Caddy server.")
		}
		nd, err := caddyfile.Parse(live.Text)
		if err != nil {
			return store.Change{}, badRequest("The live Caddyfile can't be parsed: %v", err)
		}
		*doc = *nd
		d.BaseSHA = live.SHA
		d.Log, d.Creators = nil, map[string]string{}
		return store.Change{}, nil
	})
}

// handleRebase keeps the draft even though the server file changed, so the
// next Apply replaces the server's version.
func (s *Server) handleRebase(w http.ResponseWriter, r *http.Request, u *store.User) {
	s.mutate(w, r, u, func(doc *caddyfile.Document, d *store.Draft, live store.Live) (store.Change, error) {
		d.BaseSHA = live.SHA
		return store.Change{Action: "kept draft over server changes", Target: "Caddyfile"}, nil
	})
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in segmentInput
	if !readJSON(w, r, &in) {
		return
	}
	indent := "\t"
	if doc, err := caddyfile.Parse(s.state.Draft().Text); err == nil {
		indent = doc.Indent
	}
	sg := &in.Segment
	if in.Text != "" {
		parsed, err := caddyfile.ParseSegment(in.Text)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		sg = parsed
	}
	if err := caddyfile.CheckSegment(sg); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out := sg
	if u.Role != store.RoleAdmin {
		out = caddyfile.RedactSegment(sg)
	}
	writeJSON(w, map[string]any{"text": out.Format(indent), "segment": out})
}

func (s *Server) handleDraftDiff(w http.ResponseWriter, r *http.Request, u *store.User) {
	live, _ := s.syncLive(r.Context(), false)
	s.draftMu.Lock()
	d := s.currentDraft(live)
	s.draftMu.Unlock()
	a, b := live.Text, d.Text
	if u.Role != store.RoleAdmin {
		a, b = caddyfile.RedactText(a), caddyfile.RedactText(b)
	}
	lines := diff.Lines(a, b)
	add, rem := diff.Stats(lines)
	writeJSON(w, map[string]any{"lines": diff.Hunks(lines, 3), "added": add, "removed": rem})
}

func (s *Server) handleCaddyfileText(w http.ResponseWriter, r *http.Request, u *store.User) {
	live, liveErr := s.syncLive(r.Context(), false)
	s.draftMu.Lock()
	d := s.currentDraft(live)
	s.draftMu.Unlock()
	text := d.Text
	if r.URL.Query().Get("which") == "live" {
		text = live.Text
	}
	if u.Role != store.RoleAdmin {
		text = caddyfile.RedactText(text)
	}
	writeJSON(w, map[string]any{"text": text, "rev": d.Rev, "error": liveErr, "path": s.infoPath(r.Context())})
}

func (s *Server) infoPath(ctx context.Context) string {
	if info := s.cachedInfo(ctx); info != nil {
		return info.Caddyfile
	}
	return ""
}
