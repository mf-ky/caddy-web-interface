package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Role controls what a user may do.
type Role string

const (
	RoleAdmin  Role = "admin"  // everything
	RolePower  Role = "power"  // view everything, add new cards; no edits of existing cards, no deletes, no apply
	RoleViewer Role = "viewer" // read only
)

// ValidRole reports whether r is a known role.
func ValidRole(r Role) bool { return r == RoleAdmin || r == RolePower || r == RoleViewer }

// RoleLabel is the name shown in the UI.
func RoleLabel(r Role) string {
	switch r {
	case RoleAdmin:
		return "Admin"
	case RolePower:
		return "Power User"
	}
	return "User"
}

// User is one account.
type User struct {
	Username     string    `json:"username"`
	Role         Role      `json:"role"`
	PasswordHash string    `json:"passwordHash"`
	Created      time.Time `json:"created"`
	PasswordSet  time.Time `json:"passwordSet"`
	LastLogin    time.Time `json:"lastLogin,omitempty"`
}

// Stamp changes whenever the password changes; sessions embed it so that a
// password reset (including from the CLI) logs out old sessions.
func (u *User) Stamp() string {
	h := sha256.Sum256([]byte(u.PasswordHash + string(u.Role)))
	return hex.EncodeToString(h[:6])
}

// Users is the account store (users.json). The CLI edits the same file while
// the server runs, so every read checks whether the file changed on disk.
type Users struct {
	path  string
	mu    sync.Mutex
	users map[string]*User
	mtime time.Time
}

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

// ErrLastAdmin protects against locking everyone out.
var ErrLastAdmin = errors.New("there must always be at least one admin")

// OpenUsers loads users.json from dir.
func OpenUsers(dir string) (*Users, error) {
	u := &Users{path: dir + "/users.json"}
	u.mu.Lock()
	defer u.mu.Unlock()
	return u, u.reload(true)
}

func (s *Users) reload(force bool) error {
	st, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		if s.users == nil {
			s.users = map[string]*User{}
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !force && st.ModTime().Equal(s.mtime) && s.users != nil {
		return nil
	}
	var list []*User
	if err := readJSON(s.path, &list); err != nil {
		return fmt.Errorf("reading %s: %w", s.path, err)
	}
	s.users = map[string]*User{}
	for _, u := range list {
		s.users[strings.ToLower(u.Username)] = u
	}
	s.mtime = st.ModTime()
	return nil
}

func (s *Users) save() error {
	list := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		list = append(list, u)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Username < list[j].Username })
	if err := writeJSON(s.path, list, 0o600); err != nil {
		return err
	}
	if st, err := os.Stat(s.path); err == nil {
		s.mtime = st.ModTime()
	}
	return nil
}

func copyUser(u *User) *User {
	c := *u
	return &c
}

// List returns all users sorted by name.
func (s *Users) List() []*User {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload(false)
	out := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, copyUser(u))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

// Count returns how many users exist.
func (s *Users) Count() int { return len(s.List()) }

// Get finds a user by name (case-insensitive).
func (s *Users) Get(name string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload(false)
	if u, ok := s.users[strings.ToLower(name)]; ok {
		return copyUser(u)
	}
	return nil
}

// CheckPassword enforces a minimal password policy.
func CheckPassword(pw string) error {
	if len(pw) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	if len(pw) > 72 {
		return errors.New("password must be at most 72 characters")
	}
	return nil
}

// Add creates a user.
func (s *Users) Add(name string, role Role, password string) error {
	if !usernameRe.MatchString(name) {
		return errors.New("username may only contain letters, numbers, dot, dash and underscore (max 32)")
	}
	if !ValidRole(role) {
		return fmt.Errorf("unknown role %q (use admin, power or viewer)", role)
	}
	if err := CheckPassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload(false)
	if _, ok := s.users[strings.ToLower(name)]; ok {
		return fmt.Errorf("user %q already exists", name)
	}
	now := time.Now()
	s.users[strings.ToLower(name)] = &User{Username: name, Role: role, PasswordHash: string(hash), Created: now, PasswordSet: now}
	return s.save()
}

// SetPassword changes a password.
func (s *Users) SetPassword(name, password string) error {
	if err := CheckPassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload(false)
	u, ok := s.users[strings.ToLower(name)]
	if !ok {
		return fmt.Errorf("no user %q", name)
	}
	u.PasswordHash = string(hash)
	u.PasswordSet = time.Now()
	return s.save()
}

func (s *Users) adminCount() int {
	n := 0
	for _, u := range s.users {
		if u.Role == RoleAdmin {
			n++
		}
	}
	return n
}

// SetRole changes a role.
func (s *Users) SetRole(name string, role Role) error {
	if !ValidRole(role) {
		return fmt.Errorf("unknown role %q (use admin, power or viewer)", role)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload(false)
	u, ok := s.users[strings.ToLower(name)]
	if !ok {
		return fmt.Errorf("no user %q", name)
	}
	if u.Role == RoleAdmin && role != RoleAdmin && s.adminCount() == 1 {
		return ErrLastAdmin
	}
	u.Role = role
	return s.save()
}

// Delete removes a user.
func (s *Users) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload(false)
	u, ok := s.users[strings.ToLower(name)]
	if !ok {
		return fmt.Errorf("no user %q", name)
	}
	if u.Role == RoleAdmin && s.adminCount() == 1 {
		return ErrLastAdmin
	}
	delete(s.users, strings.ToLower(name))
	return s.save()
}

// dummyHash keeps login timing the same for unknown users.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("caddyweb-timing"), bcrypt.DefaultCost)

// Verify checks a login and records the time of the last login.
func (s *Users) Verify(name, password string) *User {
	u := s.Get(name)
	if u == nil {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return nil
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil
	}
	s.mu.Lock()
	if cur, ok := s.users[strings.ToLower(name)]; ok {
		cur.LastLogin = time.Now()
		_ = s.save()
	}
	s.mu.Unlock()
	return u
}
