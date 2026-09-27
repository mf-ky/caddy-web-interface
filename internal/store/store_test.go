package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUsers(t *testing.T) {
	dir := t.TempDir()
	u, err := OpenUsers(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Add("Alex", RoleAdmin, "short"); err == nil {
		t.Fatal("short password accepted")
	}
	if err := u.Add("Alex", RoleAdmin, "long-enough"); err != nil {
		t.Fatal(err)
	}
	if err := u.Add("alex", RoleViewer, "long-enough"); err == nil {
		t.Fatal("duplicate (case-insensitive) accepted")
	}
	if u.Verify("ALEX", "long-enough") == nil || u.Verify("alex", "wrong-pass") != nil {
		t.Fatal("verify")
	}
	if err := u.SetRole("alex", RoleViewer); err != ErrLastAdmin {
		t.Fatalf("demoting the last admin: %v", err)
	}
	if err := u.Delete("alex"); err != ErrLastAdmin {
		t.Fatalf("deleting the last admin: %v", err)
	}
	// A second process (the CLI) changing the file is picked up.
	stamp := u.Get("alex").Stamp()
	other, _ := OpenUsers(dir)
	if err := other.SetPassword("alex", "new-password"); err != nil {
		t.Fatal(err)
	}
	if u.Get("alex").Stamp() == stamp {
		t.Fatal("password change from another process not noticed")
	}
}

func TestMigrateSingleServer(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"connection":{"mode":"ssh","host":"192.168.0.10","port":22,"user":"caddyweb","hostKey":"SHA256:x"},"backupRetention":7}`), 0o600)
	os.WriteFile(filepath.Join(dir, "draft.json"), []byte(`{"text":"a.com {\n}\n","rev":3}`), 0o600)
	st, err := OpenState(dir)
	if err != nil {
		t.Fatal(err)
	}
	servers := st.Servers()
	if len(servers) != 1 || servers[0].ID != "default" || servers[0].Connection.Host != "192.168.0.10" || servers[0].Connection.HostKey != "SHA256:x" {
		t.Fatalf("servers: %+v", servers)
	}
	if st.Settings().BackupRetention != 7 {
		t.Fatal("settings lost")
	}
	if d := st.ServerState("default").Draft(); d.Rev != 3 {
		t.Fatalf("draft not migrated: %+v", d)
	}
	// ids come from names and never collide
	a, _ := st.AddServer("My Pi!", servers[0].Connection)
	b, _ := st.AddServer("my pi", servers[0].Connection)
	if a.ID != "my-pi" || b.ID != "my-pi-2" {
		t.Fatalf("ids %s %s", a.ID, b.ID)
	}
	if err := st.RemoveServer("my-pi"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "servers", "my-pi")); !os.IsNotExist(err) {
		t.Fatal("removed server's folder still in place")
	}
}
