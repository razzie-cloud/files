package storage

import (
	"testing"
)

func TestRelativePaths(t *testing.T) {
	for _, value := range []string{"/absolute", "a/../b", "a//b", "a/./b", "a/", "a\\b", "\x00", string([]byte{0xff})} {
		if _, e := Path(value, false); e == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	for _, value := range []string{"a + %25/árvíz.txt", "a:b", "file #1.txt"} {
		if got, e := Path(value, false); e != nil || got != value {
			t.Fatalf("rejected %q", value)
		}
	}
	if got, e := Child("folder", "../evil"); e == nil || got != "" {
		t.Fatal("child traversal")
	}
}
