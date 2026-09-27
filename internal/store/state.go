package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mf-ky/caddy-web-interface/internal/remote"
)

// Offsite is an optional extra copy of backups made with rsync.
type Offsite struct {
	Enabled bool   `json:"enabled"`
	Target  string `json:"target"` // e.g. user@nas:/backups/caddy/ or /mnt/backup/caddy/
	Port    int    `json:"port"`   // SSH port for remote targets (0 = 22)
	LastRun string `json:"lastRun,omitempty"`
	LastErr string `json:"lastError,omitempty"`
}

// Settings are the admin-editable options shared by all servers.
type Settings struct {
	BackupRetention int     `json:"backupRetention"`
	Offsite         Offsite `json:"offsite"`
	SessionHours    int     `json:"sessionHours"`
}

// ServerConfig is one managed Caddy server.
type ServerConfig struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Connection remote.Config `json:"connection"`
	Created    time.Time     `json:"created"`
}

// Change is one entry in a draft's change log.
type Change struct {
	Time   time.Time `json:"time"`
	User   string    `json:"user"`
	Action string    `json:"action"` // added | edited | deleted | reverted | raw-edit | copied
	Target string    `json:"target"`
}

// Draft is the working copy the UI edits before "Apply".
type Draft struct {
	Text     string            `json:"text"`
	BaseSHA  string            `json:"baseSha"` // SHA of the live file the draft started from
	Rev      int64             `json:"rev"`
	Creators map[string]string `json:"creators"` // segment key -> username, for cards added in this draft
	Log      []Change          `json:"log"`
}

// Live caches the most recently read server Caddyfile.
type Live struct {
	Text    string    `json:"text"`
	SHA     string    `json:"sha"`
	Fetched time.Time `json:"fetched"`
}

// ApplyRecord is one entry of the history page.
type ApplyRecord struct {
	Time    time.Time `json:"time"`
	User    string    `json:"user"`
	Action  string    `json:"action"` // apply | restore
	Backup  string    `json:"backup,omitempty"`
	OK      bool      `json:"ok"`
	Error   string    `json:"error,omitempty"`
	Changes []Change  `json:"changes,omitempty"`
	Note    string    `json:"note,omitempty"`
}

// State holds the global settings and the list of servers.
type State struct {
	dir string
	mu  sync.Mutex

	settings Settings
	servers  []ServerConfig
	per      map[string]*ServerState
}

// OpenState loads settings and servers from dir, migrating a single-server
// data directory from older versions if needed.
func OpenState(dir string) (*State, error) {
	s := &State{dir: dir, per: map[string]*ServerState{}}
	s.settings = Settings{BackupRetention: 20, SessionHours: 24 * 7}
	if err := readJSON(filepath.Join(dir, "settings.json"), &s.settings); err != nil {
		return nil, err
	}
	if s.settings.BackupRetention < 1 {
		s.settings.BackupRetention = 20
	}
	if s.settings.SessionHours < 1 {
		s.settings.SessionHours = 24 * 7
	}
	if err := readJSON(filepath.Join(dir, "servers.json"), &s.servers); err != nil {
		return nil, err
	}
	if err := s.migrateSingleServer(); err != nil {
		return nil, fmt.Errorf("migrating data directory: %w", err)
	}
	for _, sc := range s.servers {
		ps, err := openServerState(s.serverDir(sc.ID))
		if err != nil {
			return nil, err
		}
		s.per[sc.ID] = ps
	}
	return s, nil
}

// migrateSingleServer moves pre-multi-server files into servers/<id>/.
func (s *State) migrateSingleServer() error {
	var old struct {
		Connection *remote.Config `json:"connection"`
	}
	if err := readJSON(filepath.Join(s.dir, "settings.json"), &old); err != nil {
		return err
	}
	_, draftErr := os.Stat(filepath.Join(s.dir, "draft.json"))
	if len(s.servers) > 0 || (old.Connection == nil || !old.Connection.Configured()) && draftErr != nil {
		return nil
	}
	name := "Caddy server"
	cfg := remote.Config{Mode: "ssh", Port: 22, User: "caddyweb"}
	if old.Connection != nil {
		cfg = *old.Connection
		if cfg.Host != "" {
			name = cfg.Host
		}
	}
	sc := ServerConfig{ID: "default", Name: name, Connection: cfg, Created: time.Now()}
	dst := s.serverDir(sc.ID)
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	for _, f := range []string{"draft.json", "live.json", "history.json", "backups"} {
		if _, err := os.Stat(filepath.Join(s.dir, f)); err == nil {
			if err := os.Rename(filepath.Join(s.dir, f), filepath.Join(dst, f)); err != nil {
				return err
			}
		}
	}
	s.servers = []ServerConfig{sc}
	if err := writeJSON(filepath.Join(s.dir, "servers.json"), s.servers, 0o600); err != nil {
		return err
	}
	return writeJSON(filepath.Join(s.dir, "settings.json"), s.settings, 0o600)
}

func (s *State) serverDir(id string) string { return filepath.Join(s.dir, "servers", id) }

// ServerDir is where a server's draft, history and backup mirror live.
func (s *State) ServerDir(id string) string { return s.serverDir(id) }

// Settings returns a copy of the settings.
func (s *State) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// UpdateSettings applies fn and saves.
func (s *State) UpdateSettings(fn func(*Settings)) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.settings)
	return s.settings, writeJSON(filepath.Join(s.dir, "settings.json"), s.settings, 0o600)
}

// Servers returns the configured servers in creation order.
func (s *State) Servers() []ServerConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ServerConfig(nil), s.servers...)
}

// Server returns one server's config.
func (s *State) Server(id string) (ServerConfig, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sc := range s.servers {
		if sc.ID == id {
			return sc, true
		}
	}
	return ServerConfig{}, false
}

// ServerState returns the per-server state (draft, live, history).
func (s *State) ServerState(id string) *ServerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.per[id]
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	s := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(s) > 32 {
		s = strings.Trim(s[:32], "-")
	}
	if s == "" {
		s = "server"
	}
	return s
}

// AddServer creates a server with an ID derived from its name.
func (s *State) AddServer(name string, cfg remote.Config) (ServerConfig, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ServerConfig{}, errors.New("give the server a name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	base := slugify(name)
	id := base
	taken := func(id string) bool {
		for _, sc := range s.servers {
			if sc.ID == id {
				return true
			}
		}
		_, err := os.Stat(s.serverDir(id))
		return err == nil
	}
	for i := 2; taken(id); i++ {
		id = fmt.Sprintf("%s-%d", base, i)
	}
	sc := ServerConfig{ID: id, Name: name, Connection: cfg, Created: time.Now()}
	ps, err := openServerState(s.serverDir(id))
	if err != nil {
		return ServerConfig{}, err
	}
	s.servers = append(s.servers, sc)
	if err := writeJSON(filepath.Join(s.dir, "servers.json"), s.servers, 0o600); err != nil {
		s.servers = s.servers[:len(s.servers)-1]
		return ServerConfig{}, err
	}
	s.per[id] = ps
	return sc, nil
}

// UpdateServer changes a server's name or connection.
func (s *State) UpdateServer(id string, fn func(*ServerConfig)) (ServerConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.servers {
		if s.servers[i].ID == id {
			fn(&s.servers[i])
			s.servers[i].ID = id
			return s.servers[i], writeJSON(filepath.Join(s.dir, "servers.json"), s.servers, 0o600)
		}
	}
	return ServerConfig{}, fmt.Errorf("no server %q", id)
}

// RemoveServer forgets a server. Its local files (draft, history, backup
// mirror) are moved to servers/.removed/ rather than deleted.
func (s *State) RemoveServer(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, sc := range s.servers {
		if sc.ID == id {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("no server %q", id)
	}
	rest := append(append([]ServerConfig(nil), s.servers[:idx]...), s.servers[idx+1:]...)
	if err := writeJSON(filepath.Join(s.dir, "servers.json"), rest, 0o600); err != nil {
		return err
	}
	s.servers = rest
	delete(s.per, id)
	trash := filepath.Join(s.dir, "servers", ".removed")
	_ = os.MkdirAll(trash, 0o700)
	_ = os.Rename(s.serverDir(id), filepath.Join(trash, id+"-"+time.Now().Format("20060102-150405")))
	return nil
}

// ServerState is one server's draft, live cache and history.
type ServerState struct {
	dir string
	mu  sync.Mutex

	draft   Draft
	live    Live
	history []ApplyRecord
}

func openServerState(dir string) (*ServerState, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &ServerState{dir: dir}
	for _, f := range []struct {
		name string
		v    any
	}{{"draft.json", &s.draft}, {"live.json", &s.live}, {"history.json", &s.history}} {
		if err := readJSON(filepath.Join(dir, f.name), f.v); err != nil {
			return nil, err
		}
	}
	if s.draft.Creators == nil {
		s.draft.Creators = map[string]string{}
	}
	return s, nil
}

// Dir is the server's data directory.
func (s *ServerState) Dir() string { return s.dir }

// Draft returns a copy of the draft.
func (s *ServerState) Draft() Draft {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyDraft(s.draft)
}

func copyDraft(d Draft) Draft {
	c := d
	c.Creators = map[string]string{}
	for k, v := range d.Creators {
		c.Creators[k] = v
	}
	c.Log = append([]Change(nil), d.Log...)
	return c
}

// SetDraft saves a new draft.
func (s *ServerState) SetDraft(d Draft) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.draft = copyDraft(d)
	return writeJSON(filepath.Join(s.dir, "draft.json"), s.draft, 0o600)
}

// Live returns the cached live file.
func (s *ServerState) Live() Live {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live
}

// SetLive caches the live file.
func (s *ServerState) SetLive(l Live) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := l.SHA != s.live.SHA
	s.live = l
	if !changed {
		return nil
	}
	return writeJSON(filepath.Join(s.dir, "live.json"), s.live, 0o600)
}

// History returns apply history, newest first.
func (s *ServerState) History() []ApplyRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ApplyRecord(nil), s.history...)
}

// AddHistory records an apply/restore (keeps the latest 500).
func (s *ServerState) AddHistory(r ApplyRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append([]ApplyRecord{r}, s.history...)
	if len(s.history) > 500 {
		s.history = s.history[:500]
	}
	return writeJSON(filepath.Join(s.dir, "history.json"), s.history, 0o600)
}
