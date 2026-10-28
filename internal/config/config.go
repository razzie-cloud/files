package config

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port, Origin, PostgresURI, PostgresURIFrom, PasswordFile, SecretsDir string
	PrivateEndpoints                                                     string
	AllowHTTP                                                            bool
	MaxUploadBytes                                                       int64
	MaxUploads, MaxEntries                                               int
}

func Load(args []string, env func(string) string) (Config, error) {
	c := Config{}
	f := flag.NewFlagSet("files", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	str := func(p *string, name, variable, fallback string) {
		value := env(variable)
		if value == "" {
			value = fallback
		}
		f.StringVar(p, name, value, variable)
	}
	str(&c.Port, "port", "SERVICE_PORT", "8080")
	str(&c.Origin, "origin", "ORIGIN", "")
	str(&c.PostgresURI, "postgres-uri", "POSTGRES_URI", "")
	str(&c.PostgresURIFrom, "postgres-uri-from", "POSTGRES_URI_FROM", "")
	str(&c.PasswordFile, "postgres-password-file", "POSTGRES_PASSWORD_FILE", "")
	str(&c.SecretsDir, "secrets-dir", "SECRETS_DIR", "")
	str(&c.PrivateEndpoints, "s3-allowed-private-endpoints", "S3_ALLOWED_PRIVATE_ENDPOINTS", "")
	num := func(key string, fallback int64) (int64, error) {
		if v := env(key); v != "" {
			return strconv.ParseInt(v, 10, 64)
		}
		return fallback, nil
	}
	u, e := num("MAX_UPLOAD_BYTES", 1<<30)
	if e != nil {
		return c, errors.New("invalid MAX_UPLOAD_BYTES")
	}
	n, e := num("MAX_CONCURRENT_UPLOADS", 4)
	if e != nil {
		return c, errors.New("invalid MAX_CONCURRENT_UPLOADS")
	}
	l, e := num("MAX_LIST_ENTRIES", 10000)
	if e != nil {
		return c, errors.New("invalid MAX_LIST_ENTRIES")
	}
	h := false
	if v := env("S3_ALLOW_HTTP"); v != "" {
		h, e = strconv.ParseBool(v)
		if e != nil {
			return c, errors.New("invalid S3_ALLOW_HTTP")
		}
	}
	f.BoolVar(&c.AllowHTTP, "s3-allow-http", h, "allow HTTP for explicit private endpoint exceptions")
	f.Int64Var(&c.MaxUploadBytes, "max-upload-bytes", u, "maximum plaintext file size")
	f.IntVar(&c.MaxUploads, "max-concurrent-uploads", int(n), "concurrent uploads per process")
	f.IntVar(&c.MaxEntries, "max-list-entries", int(l), "maximum entries in one directory")
	if err := f.Parse(args); err != nil {
		return c, errors.New("invalid command-line configuration")
	}
	if f.NArg() != 0 {
		return c, errors.New("unexpected arguments")
	}
	p, e := strconv.Atoi(c.Port)
	if e != nil || p < 1 || p > 65535 {
		return c, errors.New("invalid service port")
	}
	origin, e := url.Parse(c.Origin)
	if e != nil || origin.Host == "" || (origin.Scheme != "http" && origin.Scheme != "https") || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" {
		return c, errors.New("ORIGIN must be an exact http(s) origin without a path")
	}
	if (c.PostgresURI == "") == (c.PostgresURIFrom == "") {
		return c, errors.New("set exactly one of POSTGRES_URI and POSTGRES_URI_FROM")
	}
	if c.SecretsDir == "" || c.MaxUploadBytes < 1 || c.MaxUploadBytes > 1<<40 || c.MaxUploads < 1 || c.MaxUploads > 64 || c.MaxEntries < 1 || c.MaxEntries > 100000 {
		return c, errors.New("set SECRETS_DIR and valid transfer/listing limits")
	}
	return c, nil
}

func (c Config) DatabaseURI(ctx context.Context) (string, error) {
	value := c.PostgresURI
	if c.PostgresURIFrom != "" {
		u, e := url.Parse(c.PostgresURIFrom)
		if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return "", errors.New("invalid broker discovery URL")
		}
		client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		for attempt := range 5 {
			req, e := http.NewRequestWithContext(ctx, http.MethodGet, c.PostgresURIFrom, nil)
			if e != nil {
				return "", errors.New("invalid broker request")
			}
			resp, e := client.Do(req)
			if e == nil {
				body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4097))
				resp.Body.Close()
				if resp.StatusCode == 200 && readErr == nil && len(body) <= 4096 {
					value = strings.TrimSpace(string(body))
					break
				}
				if resp.StatusCode == 200 {
					return "", errors.New("invalid broker response")
				}
				if resp.StatusCode >= 300 && resp.StatusCode < 500 {
					return "", errors.New("broker rejected database discovery")
				}
			}
			if attempt == 4 {
				return "", errors.New("database broker unavailable or invalid response")
			}
			timer := time.NewTimer(time.Duration(200*(1<<attempt)+rand.IntN(100)) * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", ctx.Err()
			case <-timer.C:
			}
		}
	}
	u, e := url.Parse(value)
	if e != nil || u.Host == "" || u.User == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Path == "" {
		return "", errors.New("invalid PostgreSQL URI")
	}
	if c.PasswordFile != "" {
		b, e := os.ReadFile(c.PasswordFile)
		if e != nil {
			return "", errors.New("cannot read PostgreSQL password file")
		}
		u.User = url.UserPassword(u.User.Username(), strings.TrimRight(string(b), "\r\n"))
	}
	return u.String(), nil
}

func (c Config) Address() string { return fmt.Sprintf(":%s", c.Port) }
