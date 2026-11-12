package rclone

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/razzie-cloud/files/internal/secrets"
	"github.com/razzie-cloud/files/internal/store"
	"github.com/razzie-cloud/files/internal/testutil"
)

func TestSharedStoragePrefixesAndCrypt(t *testing.T) {
	ctx := context.Background()
	server, _ := testutil.S3(t, "shared-test")
	defer server.Close()
	endpoint, _ := url.Parse(server.URL)
	policy, err := NewPolicy(endpoint.Host, true)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := StartEgress(policy)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	factory := &Factory{ProxyURL: proxy.URL, Policy: policy, MaxEntries: 100}
	owners := []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}
	for _, prefix := range []bool{false, true} {
		for _, mode := range []string{"none", "rclone_crypt"} {
			t.Run(fmt.Sprintf("prefix=%v/%s", prefix, mode), func(t *testing.T) {
				c := store.Connection{Provider: "Other", Endpoint: server.URL, Region: "us-east-1", PathStyle: true, Shared: true, UserIDPrefix: prefix}
				d := store.Directory{Bucket: "shared-test", Prefix: fmt.Sprintf("%t/%s", prefix, mode), EncryptionMode: mode, FilenameEncryption: "standard", DirectoryEncryption: true, FilenameEncoding: "base32", Suffix: ".bin"}
				credentials := secrets.Value{AccessKey: "test", SecretKey: "test-secret"}
				crypt := secrets.Value{Password: "compatible shared fixture password"}
				for _, owner := range owners {
					d.Owner = owner
					fs, err := factory.Open(ctx, c, d, credentials, crypt)
					if err != nil {
						t.Fatal(err)
					}
					if err := fs.Put(ctx, "entry.txt", strings.NewReader(owner)); err != nil {
						t.Fatal(err)
					}
					physical := fs.PhysicalKey("entry.txt")
					expected := d.Prefix + "/"
					if prefix {
						expected = owner + "/" + expected
					}
					if !strings.HasPrefix(physical, expected) {
						t.Fatal("user root must precede crypt names", physical)
					}
					fs.Close()
				}
				d.Owner = owners[0]
				fs, err := factory.Open(ctx, c, d, credentials, crypt)
				if err != nil {
					t.Fatal(err)
				}
				defer fs.Close()
				in, err := fs.Open(ctx, "entry.txt", -1, -1)
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(in)
				in.Close()
				if err != nil {
					t.Fatal(err)
				}
				expected := owners[1]
				if prefix {
					expected = owners[0]
				}
				if string(data) != expected {
					t.Fatal("prefix policy did not govern isolation", string(data))
				}
				if prefix {
					d.Owner = "../another-user"
					if _, err := factory.Open(ctx, c, d, credentials, crypt); err == nil {
						t.Fatal("invalid owner accepted for user prefix")
					}
				}
			})
		}
	}
}
