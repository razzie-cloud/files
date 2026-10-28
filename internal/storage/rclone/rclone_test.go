package rclone

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/razzie-cloud/files/internal/secrets"
	"github.com/razzie-cloud/files/internal/storage"
	"github.com/razzie-cloud/files/internal/store"
	"github.com/razzie-cloud/files/internal/testutil"
)

func TestS3AndCryptRoundTrip(t *testing.T) {
	ctx := context.Background()
	s3server, backend := testutil.S3(t, "files-test")
	defer s3server.Close()
	endpoint, _ := url.Parse(s3server.URL)
	policy, e := NewPolicy(endpoint.Host, true)
	if e != nil {
		t.Fatal(e)
	}
	proxy, e := StartEgress(policy)
	if e != nil {
		t.Fatal(e)
	}
	defer proxy.Close()
	factory := &Factory{ProxyURL: proxy.URL, Policy: policy, MaxEntries: 100}
	for _, mode := range []string{"none", "rclone_crypt"} {
		for _, names := range []string{"standard", "off"} {
			t.Run(mode+"/"+names, func(t *testing.T) {
				c := store.Connection{ID: uuid.NewString(), Provider: "Other", Endpoint: s3server.URL, Region: "us-east-1", PathStyle: true, AccessMode: "writable"}
				d := store.Directory{Bucket: "files-test", Prefix: mode + "/" + names, EncryptionMode: mode, FilenameEncryption: names, DirectoryEncryption: true, FilenameEncoding: "base32", Suffix: ".bin"}
				credentials := secrets.Value{AccessKey: "dummy", SecretKey: "dummy-secret"}
				keys := secrets.Value{Password: "temporary test encryption password", Password2: "temporary salt"}
				fs, e := factory.Open(ctx, c, d, credentials, keys)
				if e != nil {
					t.Fatal(e)
				}
				defer fs.Close()
				if e = fs.Mkdir(ctx, "empty"); e != nil {
					t.Fatal(e)
				}
				content := bytes.Repeat([]byte("private contents + %25 árvíz\n"), 5000)
				if e = fs.Put(ctx, "file + %25.txt", bytes.NewReader(content)); e != nil {
					t.Fatal(e)
				}
				if e = fs.Put(ctx, "zero", strings.NewReader("")); e != nil {
					t.Fatal(e)
				}
				entries, e := fs.List(ctx, "")
				if e != nil || len(entries) != 3 {
					_, raw := fs.(*handle).fs.List(ctx, "")
					t.Logf("raw listing error: %v", raw)
					if h := fs.(*handle); h.crypt != nil {
						es, _ := h.crypt.UnWrap().List(ctx, "")
						for _, v := range es {
							t.Logf("underlying %T %q size=%d", v, v.Remote(), v.Size())
						}
					}
					t.Fatalf("listing=%+v error=%v", entries, e)
				}
				stat, e := fs.Stat(ctx, "file + %25.txt")
				if e != nil || stat.Size != int64(len(content)) {
					t.Fatalf("stat=%+v error=%v", stat, e)
				}
				for _, span := range []struct{ start, end int64 }{{-1, -1}, {65000, 66000}} {
					in, e := fs.Open(ctx, "file + %25.txt", span.start, span.end)
					if e != nil {
						t.Fatal(e)
					}
					data, e := io.ReadAll(in)
					in.Close()
					want := content
					if span.start >= 0 {
						want = content[span.start : span.end+1]
					}
					if e != nil || !bytes.Equal(want, data) {
						t.Fatalf("download error=%v bytes=%d", e, len(data))
					}
				}
				if mode == "rclone_crypt" {
					physical := fs.PhysicalKey("file + %25.txt")
					raw, e := backend.GetObject(ctx, "files-test", physical, nil)
					if e != nil {
						t.Fatal(e)
					}
					data, e := io.ReadAll(raw.Contents)
					raw.Contents.Close()
					if e != nil || !bytes.HasPrefix(data, []byte("RCLONE\x00\x00")) || bytes.Contains(data, content[:64]) {
						t.Fatal("plaintext stored in encrypted prefix")
					}
					wrong := keys
					wrong.Password = "incorrect password"
					other, e := factory.Open(ctx, c, d, credentials, wrong)
					if e != nil {
						t.Fatal(e)
					}
					if names == "standard" {
						_, e = other.List(ctx, "")
						if !errors.Is(e, storage.ErrCrypto) {
							t.Fatalf("wrong password accepted: %v", e)
						}
					} else {
						in, e := other.Open(ctx, "file + %25.txt", -1, -1)
						if e == nil {
							_, e = io.ReadAll(in)
							in.Close()
						}
						if e == nil {
							t.Fatal("wrong content key accepted")
						}
					}
					other.Close()
				}
				if e = fs.Remove(ctx, "file + %25.txt", false); e != nil {
					t.Fatal(e)
				}
				if e = fs.Remove(ctx, "empty", true); e != nil {
					t.Fatal(e)
				}
				if e = fs.Remove(ctx, "", true); !errors.Is(e, storage.ErrInvalidPath) {
					t.Fatal("root removal accepted")
				}
			})
		}
	}
}

func TestPublicIPAndEndpointPolicy(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1", "::ffff:127.0.0.1", "fc00::1", "100.64.1.1", "192.0.2.1", "64:ff9b::a00:1"} {
		if public(netip.MustParseAddr(value)) {
			t.Fatalf("private/reserved IP allowed: %s", value)
		}
	}
	if !public(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public IP blocked")
	}
	p, _ := NewPolicy("127.0.0.1:9000", true)
	for _, raw := range []string{"http://127.0.0.1:9001", "https://user:pass@example.com", "https://example.com/path", "file:///etc/passwd"} {
		if p.Endpoint(raw) == nil {
			t.Fatalf("endpoint accepted %s", raw)
		}
	}
	if p.Endpoint("http://127.0.0.1:9000") != nil {
		t.Fatal("explicit test endpoint rejected")
	}
	if _, e := p.dial(context.Background(), "127.0.0.1:443"); e == nil {
		t.Fatal("loopback destination dialed without exception")
	}
}
