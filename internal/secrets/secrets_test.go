package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOwnerScopedSecretLifecycle(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	owner := "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa"
	ref, e := s.Put(owner, Value{SecretKey: "secret", Password: "crypt password"})
	if e != nil {
		t.Fatal(e)
	}
	value, e := s.Get(owner, ref)
	if e != nil || value.Password != "crypt password" {
		t.Fatal(e)
	}
	info, e := os.Stat(filepath.Join(dir, ref))
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("secret file permissions")
	}
	for _, test := range []struct{ owner, ref string }{{"bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb", ref}, {owner, "../escape"}, {owner, ""}} {
		if _, e = s.Get(test.owner, test.ref); e == nil {
			t.Fatal("secret reference escaped owner")
		}
	}
	s.Close()
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.Get(owner, ref); e != nil {
		t.Fatal("secret did not survive restart")
	}
}
