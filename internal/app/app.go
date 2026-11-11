package app

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/razzie-cloud/files/internal/config"
	"github.com/razzie-cloud/files/internal/gatewayidentity"
	"github.com/razzie-cloud/files/internal/health"
	"github.com/razzie-cloud/files/internal/identity"
	"github.com/razzie-cloud/files/internal/logging"
	"github.com/razzie-cloud/files/internal/secrets"
	"github.com/razzie-cloud/files/internal/storage"
	rclonestorage "github.com/razzie-cloud/files/internal/storage/rclone"
	"github.com/razzie-cloud/files/internal/store"
	"github.com/razzie-cloud/files/internal/web"
	"golang.org/x/time/rate"
)

type Server struct {
	identityVerifier *gatewayidentity.Verifier
	Config           config.Config
	Repo             store.Repository
	Secrets          *secrets.Store
	Storage          storage.Factory
	Policy           *rclonestorage.Policy
	templates        *template.Template
	uploads          chan struct{}
	health           *health.Monitor
	shares           shareCache
	passwordSlots    chan struct{}
	passwordRate     *rate.Limiter
}
type userKey struct{}
type crumb struct{ Name, URL string }
type view struct {
	LandingOrigin                               string
	Page, Message, Path, Parent, Next, Previous string
	User                                        identity.User
	Connections                                 []store.Connection
	Directories                                 []store.Directory
	Connection                                  *store.Connection
	ConnectionForm, ConnectionDialog            bool
	Directory                                   *store.Directory
	Entries                                     []storage.Entry
	Crumbs                                      []crumb
	Writable                                    bool
	UploadLimit                                 int64
	UploadFileLimit, UploadCompleted            int
	ReturnURL                                   string
	ShareEnabled                                bool
	Public                                      bool
	ShareDirectory                              bool
	Shares                                      []store.Share
	ShareURL                                    string
}

func New(cfg config.Config, repo store.Repository, secretStore *secrets.Store, factory storage.Factory, policy *rclonestorage.Policy) (*Server, error) {
	if cfg.CastShareTTL == 0 {
		cfg.CastShareTTL = 6 * time.Hour
	}
	if cfg.CastShareTTL <= 0 || cfg.CastShareTTL > 24*time.Hour {
		return nil, errors.New("invalid Cast share lifetime")
	}
	t, e := template.New("templates").Funcs(template.FuncMap{"size": size, "base": path.Base, "mediaKind": mediaKind, "castType": castType}).ParseFS(web.Assets, "templates.html")
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	verifier, e := gatewayidentity.New(ctx, cfg.AccessGatewayURL, cfg.Origin)
	if e != nil {
		return nil, e
	}
	return &Server{identityVerifier: verifier, Config: cfg, Repo: repo, Secrets: secretStore, Storage: factory, Policy: policy, templates: t, uploads: make(chan struct{}, cfg.MaxUploads),
		passwordSlots: make(chan struct{}, 2), passwordRate: rate.NewLimiter(2, 5),
		health: health.New(health.Check{Name: "postgres", Ping: func(ctx context.Context) error { return repo.Ping(ctx) }})}, nil
}

// StartHealthChecks starts the background monitor; stop it before closing Repo.
func (s *Server) StartHealthChecks(ctx context.Context) func() {
	return s.health.Start(ctx)
}
func size(n int64) string {
	if n < 0 {
		return "—"
	}
	for _, unit := range []struct {
		n int64
		s string
	}{{1 << 30, "GiB"}, {1 << 20, "MiB"}, {1 << 10, "KiB"}} {
		if n >= unit.n {
			return strconv.FormatFloat(float64(n)/float64(unit.n), 'f', 1, 64) + " " + unit.s
		}
	}
	return strconv.FormatInt(n, 10) + " B"
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	handle := func(pattern string, handler http.HandlerFunc) {
		mux.Handle(pattern, logging.Route(pattern, handler))
	}
	mux.Handle("GET /static/", logging.Route("GET /static/", http.StripPrefix("/static/", http.FileServerFS(web.Static()))))
	handle("GET /{$}", s.home)
	handle("GET /connections", s.connections)
	handle("GET /connections/new", s.connections)
	handle("GET /connections/{id}/edit", s.connections)
	handle("POST /connections", s.saveConnection)
	handle("POST /connections/{id}/update", s.saveConnection)
	handle("POST /connections/{id}/test", s.testConnection)
	handle("POST /connections/{id}/remove", s.removeConnection)
	handle("GET /directories/new", s.directoryForm)
	handle("GET /directories/{id}/edit", s.directoryForm)
	handle("POST /directories", s.saveDirectory)
	handle("POST /directories/{id}/update", s.saveDirectory)
	handle("POST /directories/{id}/test", s.testDirectory)
	handle("POST /directories/{id}/remove", s.removeDirectory)
	handle("GET /directories/{id}", s.browse)
	handle("GET /directories/{id}/download", s.download)
	handle("POST /directories/{id}/upload", s.upload)
	handle("POST /directories/{id}/mkdir", s.mutate)
	handle("POST /directories/{id}/delete-file", s.mutate)
	handle("POST /directories/{id}/rmdir", s.mutate)
	handle("GET /directories/{id}/shares", s.shareForm)
	handle("POST /directories/{id}/shares", s.createShare)
	handle("POST /directories/{id}/cast", s.createCast)
	handle("POST /directories/{id}/shares/{share}/remove", s.removeShare)
	public := s.publicHandler()
	return logging.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.health.Handle(w, r) {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		// SDK execution is opt-in through capable views; these path-scoped
		// exceptions also allow an initial document to navigate there via HTMX.
		if s.Config.ShareOrigin != "" {
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://www.gstatic.com/cv/js/sender/ https://www.gstatic.com/cast/sdk/libs/sender/ https://www.gstatic.com/eureka/clank/; connect-src 'self' "+s.Config.ShareOrigin+"; style-src 'self'; img-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		}
		// Select public routes by the actual Host, never by forwarded identity.
		if s.Config.ShareOrigin != "" && sameHost(r.Host, s.Config.ShareOrigin) {
			public.ServeHTTP(w, r)
			return
		}
		// The management origin cannot serve public capabilities.
		if strings.HasPrefix(r.URL.Path, "/s/") {
			http.NotFound(w, r)
			return
		}
		if err := s.identityVerifier.Verify(r, false); err != nil {
			http.Error(w, "Verified gateway identity required", 401)
			return
		}
		user, e := identity.FromRequest(r)
		if e != nil {
			http.Error(w, "Verified proxy identity required", 401)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), userKey{}, user))
		logging.Fields(r.Context(), "owner_user_id", user.ID)
		if r.Method == "POST" {
			if len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.Config.Origin {
				http.Error(w, "Invalid request origin", 403)
				return
			}
			if !strings.HasSuffix(r.URL.Path, "/upload") {
				r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
				if e := r.ParseForm(); e != nil {
					s.fail(w, r, errors.New("invalid form"), 422)
					return
				}
			}
		}
		ctx := r.Context()
		if r.Method == "GET" && !strings.HasSuffix(r.URL.Path, "/download") {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
		}

		mux.ServeHTTP(w, r.WithContext(ctx))
	}))
}
func user(r *http.Request) identity.User {
	u, _ := r.Context().Value(userKey{}).(identity.User)
	return u
}
func (s *Server) render(w http.ResponseWriter, r *http.Request, v view) {
	v.LandingOrigin = s.Config.LandingOrigin
	if v.LandingOrigin == "" {
		v.LandingOrigin = "https://razzie.cloud"
	}
	v.User = user(r)
	v.UploadLimit = s.Config.MaxUploadBytes
	v.UploadFileLimit = maxUploadFiles
	v.ShareEnabled = s.Config.ShareOrigin != ""
	v.Public = v.ShareEnabled && sameHost(r.Host, s.Config.ShareOrigin)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	name := "page"
	if r.Header.Get("HX-Request") == "true" {
		name = "content"
		if v.Page == "connections" && r.Header.Get("HX-Target") == "connection-dialog-content" {
			name = "connection-dialog"
			v.ConnectionDialog = true
		}
	}
	if e := s.templates.ExecuteTemplate(w, name, v); e != nil {
		logging.Fields(r.Context(), "template", name)
		logging.Fail(r.Context(), slog.LevelError, "render_template", e)
	}
}
func (s *Server) fail(w http.ResponseWriter, r *http.Request, e error, status int) {
	s.failView(w, r, e, status, view{})
}
func (s *Server) failView(w http.ResponseWriter, r *http.Request, e error, status int, v view) {
	message := "Request failed. Check the storage configuration and try again."
	switch {
	case errors.Is(e, store.ErrNotFound), errors.Is(e, storage.ErrNotFound):
		status = 404
		message = "Not found."
	case errors.Is(e, store.ErrConflict), errors.Is(e, storage.ErrConflict):
		status = 409
		message = "Configuration changed, name already exists, or directory is not empty. Refresh and try again."
	case errors.Is(e, storage.ErrInvalidPath):
		status = 422
		message = "Invalid relative path."
	case errors.Is(e, storage.ErrTooLarge):
		status = 413
		message = "The upload or directory exceeds the configured limit."
	case errors.Is(e, storage.ErrCrypto):
		status = 422
		message = "Cannot decrypt this directory or file. Check the crypt settings."
	case errors.Is(e, context.DeadlineExceeded):
		status = 504
		message = "Storage request timed out."
	default:
		if status == 422 || status == 403 || status == 429 {
			message = e.Error()
		}
	}
	if status >= 500 {
		operation := "application"
		if status == 502 || status == 504 {
			operation = "storage"
		}
		logging.Fail(r.Context(), slog.LevelError, operation, e)
	} else if errors.Is(e, storage.ErrCrypto) {
		logging.FailKind(r.Context(), slog.LevelWarn, "storage", "crypto")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Header.Get("HX-Request") == "true" && (r.Header.Get("HX-Target") == "connection-feedback" || r.Header.Get("HX-Target") == "connection-dialog-content") {
		target := "#connection-feedback"
		if r.Header.Get("HX-Target") == "connection-dialog-content" {
			target = "#connection-list-feedback"
		}
		w.Header().Set("HX-Retarget", target)
		w.WriteHeader(status)
		if err := s.templates.ExecuteTemplate(w, "connection-error", view{Message: message}); err != nil {
			logging.Fail(r.Context(), slog.LevelError, "render_template", err)
		}
		return
	}
	w.WriteHeader(status)
	v.Page, v.Message = "error", message
	s.render(w, r, v)
}
func (s *Server) done(w http.ResponseWriter, r *http.Request, destination string) {
	if r.Header.Get("HX-Request") == "true" {
		location, _ := json.Marshal(map[string]string{"path": destination, "target": "#main", "swap": "innerHTML"})
		w.Header().Set("HX-Location", string(location))
		w.WriteHeader(200)
		return
	}
	http.Redirect(w, r, destination, 303)
}
func validName(name string) bool {
	if name != strings.TrimSpace(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 80 {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func revision(r *http.Request) int64 {
	v, _ := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	return v
}
func checked(r *http.Request, key string) bool { return r.FormValue(key) == "on" }

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	dirs, e := s.Repo.Directories(r.Context(), user(r).ID)
	if e != nil {
		s.fail(w, r, e, 500)
		return
	}
	s.render(w, r, view{Page: "home", Directories: dirs})
}
func (s *Server) connections(w http.ResponseWriter, r *http.Request) {
	all, e := s.Repo.Connections(r.Context(), user(r).ID)
	if e != nil {
		s.fail(w, r, e, 500)
		return
	}
	v := view{Page: "connections", Connections: all, ConnectionForm: r.URL.Path == "/connections/new", Connection: &store.Connection{Provider: "Other", PathStyle: true, AccessMode: "read_only"}}
	if id := r.PathValue("id"); id != "" {
		c, e := s.Repo.Connection(r.Context(), user(r).ID, id)
		if e != nil {
			s.fail(w, r, e, 500)
			return
		}
		v.Connection = &c
		v.ConnectionForm = true
	}
	s.render(w, r, v)
}
func (s *Server) saveConnection(w http.ResponseWriter, r *http.Request) {
	owner := user(r).ID
	c := store.Connection{Owner: owner}
	var credentials secrets.Value
	if id := r.PathValue("id"); id != "" {
		var e error
		c, e = s.Repo.Connection(r.Context(), owner, id)
		if e != nil {
			s.fail(w, r, e, 500)
			return
		}
		if c.Revision != revision(r) {
			s.fail(w, r, store.ErrConflict, 409)
			return
		}
		credentials, e = s.Secrets.Get(r.Context(), owner, c.SecretRef)
		if e != nil {
			s.fail(w, r, e, 500)
			return
		}
	}
	c.Name = r.FormValue("name")
	c.Provider = r.FormValue("provider")
	c.Endpoint = strings.TrimSpace(r.FormValue("endpoint"))
	c.Region = strings.TrimSpace(r.FormValue("region"))
	c.PathStyle = checked(r, "path_style")
	c.AccessMode = r.FormValue("access_mode")
	if !validName(c.Name) || (c.Provider != "AWS" && c.Provider != "Other" && c.Provider != "Backblaze") || (c.AccessMode != "read_only" && c.AccessMode != "writable") || len(c.Region) > 100 {
		s.fail(w, r, errors.New("Enter a name, provider, region, and access mode."), 422)
		return
	}
	if c.Endpoint != "" {
		if e := s.Policy.Endpoint(c.Endpoint); e != nil {
			s.fail(w, r, e, 422)
			return
		}
	} else if c.Provider != "AWS" {
		s.fail(w, r, errors.New("An S3 endpoint is required for this provider."), 422)
		return
	}
	if v := r.FormValue("access_key"); v != "" {
		credentials.AccessKey = v
	}
	if v := r.FormValue("secret_key"); v != "" {
		credentials.SecretKey = v
	}
	if v := r.FormValue("session_token"); v != "" {
		credentials.SessionToken = v
	}
	if checked(r, "clear_session_token") {
		credentials.SessionToken = ""
	}
	if credentials.AccessKey == "" || credentials.SecretKey == "" {
		s.fail(w, r, errors.New("Access key and secret key are required."), 422)
		return
	}
	ref, e := s.Secrets.Put(r.Context(), owner, credentials)
	if e != nil {
		s.fail(w, r, e, 500)
		return
	}
	c.SecretRef = ref
	if e = s.Repo.SaveConnection(r.Context(), &c); e != nil {
		s.fail(w, r, e, 500)
		return
	}
	action := "updated"
	if r.PathValue("id") == "" {
		action = "created"
	}
	logging.For(r.Context()).Info("Connection saved", "event", "connection.saved", "connection_id", c.ID, "action", action)
	s.shares.invalidate()
	s.done(w, r, "/connections")
}
func (s *Server) removeConnection(w http.ResponseWriter, r *http.Request) {
	c, e := s.Repo.Connection(r.Context(), user(r).ID, r.PathValue("id"))
	if e == nil {
		e = s.Repo.RemoveConnection(r.Context(), c.Owner, c.ID, revision(r))
	}
	if e != nil {
		s.fail(w, r, e, 500)
		return
	}
	logging.For(r.Context()).Info("Connection removed", "event", "connection.removed", "connection_id", c.ID)
	s.shares.invalidate()
	s.done(w, r, "/connections")
}
func (s *Server) directoryForm(w http.ResponseWriter, r *http.Request) {
	connections, e := s.Repo.Connections(r.Context(), user(r).ID)
	if e != nil {
		s.fail(w, r, e, 500)
		return
	}
	d := store.Directory{EncryptionMode: "none", FilenameEncryption: "standard", DirectoryEncryption: true, FilenameEncoding: "base32", Suffix: ".bin"}
	if id := r.PathValue("id"); id != "" {
		d, e = s.Repo.Directory(r.Context(), user(r).ID, id)
		if e != nil {
			s.fail(w, r, e, 500)
			return
		}
	}
	s.render(w, r, view{Page: "directory-form", Directory: &d, Connections: connections})
}
func (s *Server) saveDirectory(w http.ResponseWriter, r *http.Request) {
	owner := user(r).ID
	if id := r.PathValue("id"); id != "" {
		d, e := s.Repo.Directory(r.Context(), owner, id)
		if e != nil {
			s.fail(w, r, e, 500)
			return
		}
		if d.Revision != revision(r) {
			s.fail(w, r, store.ErrConflict, 409)
			return
		}
		d.Name = r.FormValue("name")
		d.ReadOnly = checked(r, "read_only")
		if !validName(d.Name) {
			s.fail(w, r, errors.New("Enter a directory name."), 422)
			return
		}
		if e = s.Repo.SaveDirectory(r.Context(), &d); e != nil {
			s.fail(w, r, e, 500)
			return
		}
		logging.For(r.Context()).Info("Directory updated", "event", "directory.updated", "directory_id", d.ID, "connection_id", d.ConnectionID)
		s.shares.invalidate()
		s.done(w, r, "/")
		return
	}
	d := store.Directory{Owner: owner, ConnectionID: r.FormValue("connection_id"), Name: r.FormValue("name"), Bucket: r.FormValue("bucket"), Prefix: strings.TrimSuffix(r.FormValue("prefix"), "/"), ReadOnly: checked(r, "read_only"), EncryptionMode: r.FormValue("encryption_mode"), FilenameEncryption: r.FormValue("filename_encryption"), DirectoryEncryption: checked(r, "directory_encryption"), FilenameEncoding: r.FormValue("filename_encoding"), Suffix: r.FormValue("suffix")}
	if !validName(d.Name) || !rclonestorage.ValidateBucket(d.Bucket) {
		s.fail(w, r, errors.New("Enter a directory name and valid S3 bucket name."), 422)
		return
	}
	if _, e := storage.Path(d.Prefix, true); e != nil {
		s.fail(w, r, e, 422)
		return
	}
	c, e := s.Repo.Connection(r.Context(), owner, d.ConnectionID)
	if e != nil {
		s.fail(w, r, e, 500)
		return
	}
	keys := secrets.Value{}
	if d.EncryptionMode == "rclone_crypt" {
		keys.Password = r.FormValue("crypt_password")
		keys.Password2 = r.FormValue("crypt_password2")
		if keys.Password == "" || (d.FilenameEncryption != "standard" && d.FilenameEncryption != "off") || (d.FilenameEncoding != "base32" && d.FilenameEncoding != "base64" && d.FilenameEncoding != "base32768") || !strings.HasPrefix(d.Suffix, ".") || strings.ContainsAny(d.Suffix, "/\\") || len(d.Suffix) > 20 {
			s.fail(w, r, errors.New("Enter a crypt password and valid crypt options."), 422)
			return
		}
		credentials, e := s.Secrets.Get(r.Context(), owner, c.SecretRef)
		if e != nil {
			s.fail(w, r, e, 500)
			return
		}
		fs, e := s.Storage.Open(r.Context(), c, d, credentials, keys)
		if e != nil {
			s.fail(w, r, e, 502)
			return
		}
		defer fs.Close()
		entries, e := fs.List(r.Context(), "")
		if e != nil && !errors.Is(e, storage.ErrNotFound) {
			s.fail(w, r, e, 502)
			return
		}
		if checked(r, "new_crypt_prefix") {
			if len(entries) > 0 {
				s.fail(w, r, errors.New("The crypt prefix contains data. Attach it using a known encrypted file instead."), 422)
				return
			}
		} else {
			key, e := storage.Path(r.FormValue("verify_path"), false)
			if e != nil {
				s.fail(w, r, errors.New("Enter a known decrypted file path to verify the crypt password, or initialize an empty prefix."), 422)
				return
			}
			info, statErr := fs.Stat(r.Context(), key)
			if statErr != nil {
				s.fail(w, r, storage.ErrCrypto, 422)
				return
			}
			if info.Size < 1 {
				s.fail(w, r, errors.New("Choose a non-empty encrypted file to verify the password."), 422)
				return
			}
			in, e := fs.Open(r.Context(), key, 0, 65535)
			if e == nil {
				_, e = io.Copy(io.Discard, in)
				in.Close()
			}
			if e != nil {
				s.fail(w, r, storage.ErrCrypto, 422)
				return
			}
		}
		ref, e := s.Secrets.Put(r.Context(), owner, keys)
		if e != nil {
			s.fail(w, r, e, 500)
			return
		}
		d.CryptSecretRef = ref
	} else if d.EncryptionMode != "none" {
		s.fail(w, r, errors.New("Select plain S3 or rclone crypt."), 422)
		return
	}
	if e = s.Repo.SaveDirectory(r.Context(), &d); e != nil {
		s.fail(w, r, e, 500)
		return
	}
	logging.For(r.Context()).Info("Directory created", "event", "directory.created", "directory_id", d.ID, "connection_id", d.ConnectionID)
	s.shares.invalidate()
	s.done(w, r, "/")
}
func (s *Server) removeDirectory(w http.ResponseWriter, r *http.Request) {
	d, e := s.Repo.Directory(r.Context(), user(r).ID, r.PathValue("id"))
	if e == nil {
		e = s.Repo.RemoveDirectory(r.Context(), d.Owner, d.ID, revision(r))
	}
	if e != nil {
		s.fail(w, r, e, 500)
		return
	}
	logging.For(r.Context()).Info("Directory removed", "event", "directory.removed", "directory_id", d.ID)
	s.shares.invalidate()
	s.done(w, r, "/")
}
func (s *Server) open(r *http.Request, write bool) (store.Directory, store.Connection, storage.FS, error) {
	d, e := s.Repo.Directory(r.Context(), user(r).ID, r.PathValue("id"))
	if e != nil {
		return d, store.Connection{}, nil, e
	}
	c, e := s.Repo.Connection(r.Context(), d.Owner, d.ConnectionID)
	if e != nil {
		return d, c, nil, e
	}
	if write && !d.Writable(c) {
		return d, c, nil, errReadOnly
	}
	logging.Fields(r.Context(), "directory_id", d.ID, "connection_id", c.ID)
	credentials, e := s.Secrets.Get(r.Context(), d.Owner, c.SecretRef)
	if e != nil {
		return d, c, nil, e
	}
	keys := secrets.Value{}
	if d.EncryptionMode == "rclone_crypt" {
		keys, e = s.Secrets.Get(r.Context(), d.Owner, d.CryptSecretRef)
		if e != nil {
			return d, c, nil, e
		}
	}
	fs, e := s.Storage.Open(r.Context(), c, d, credentials, keys)
	return d, c, fs, e
}

var errReadOnly = errors.New("This storage is read-only.")

func (s *Server) operationError(w http.ResponseWriter, r *http.Request, e error) {
	if errors.Is(e, errReadOnly) {
		s.fail(w, r, e, 403)
	} else {
		s.fail(w, r, e, 502)
	}
}
func (s *Server) browse(w http.ResponseWriter, r *http.Request) {
	d, c, fs, e := s.open(r, false)
	if e != nil {
		s.operationError(w, r, e)
		return
	}
	defer fs.Close()
	key, e := storage.Path(r.URL.Query().Get("path"), true)
	if e != nil {
		s.fail(w, r, e, 422)
		return
	}
	entries, e := fs.List(r.Context(), key)
	if errors.Is(e, storage.ErrNotFound) && key == "" {
		e = nil
	}
	if e != nil {
		s.operationError(w, r, e)
		return
	}
	v := view{Page: "browse", Directory: &d, Connection: &c, Path: key, Writable: d.Writable(c), Crumbs: []crumb{{d.Name, "/directories/" + d.ID}}}
	parent := ""
	for _, part := range strings.Split(key, "/") {
		if part == "" {
			continue
		}
		if parent != "" {
			parent += "/"
		}
		parent += part
		v.Crumbs = append(v.Crumbs, crumb{part, "/directories/" + d.ID + "?path=" + url.QueryEscape(parent)})
	}
	if key != "" {
		parent = path.Dir(key)
		if parent == "." {
			parent = ""
		}
		v.Parent = "/directories/" + d.ID + "?path=" + url.QueryEscape(parent)
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 0 {
		page = 0
	}
	if page > len(entries)/100 {
		page = 0
	}
	start := page * 100
	end := min(start+100, len(entries))
	v.Entries = entries[start:end]
	base := "/directories/" + d.ID + "?path=" + url.QueryEscape(key) + "&page="
	if page > 0 {
		v.Previous = base + strconv.Itoa(page-1)
	}
	if end < len(entries) {
		v.Next = base + strconv.Itoa(page+1)
	}
	s.render(w, r, v)
}
func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	c, e := s.Repo.Connection(r.Context(), user(r).ID, r.PathValue("id"))
	if e != nil {
		s.fail(w, r, e, 500)
		return
	}
	d := store.Directory{Bucket: r.FormValue("bucket"), Prefix: strings.TrimSuffix(r.FormValue("prefix"), "/"), EncryptionMode: "none"}
	if !rclonestorage.ValidateBucket(d.Bucket) {
		s.fail(w, r, errors.New("Enter a bucket to test."), 422)
		return
	}
	if _, e = storage.Path(d.Prefix, true); e != nil {
		s.fail(w, r, e, 422)
		return
	}
	credentials, e := s.Secrets.Get(r.Context(), c.Owner, c.SecretRef)
	if e != nil {
		s.fail(w, r, e, 500)
		return
	}
	fs, e := s.Storage.Open(r.Context(), c, d, credentials, secrets.Value{})
	if e != nil {
		s.operationError(w, r, e)
		return
	}
	defer fs.Close()
	_, e = fs.List(r.Context(), "")
	if e != nil {
		s.operationError(w, r, e)
		return
	}
	s.render(w, r, view{Page: "message", Message: "Listing succeeded. This does not verify upload or delete permissions."})
}
func (s *Server) testDirectory(w http.ResponseWriter, r *http.Request) {
	_, _, fs, e := s.open(r, false)
	if e != nil {
		s.operationError(w, r, e)
		return
	}
	defer fs.Close()
	_, e = fs.List(r.Context(), "")
	if e != nil {
		s.operationError(w, r, e)
		return
	}
	s.render(w, r, view{Page: "message", Message: "Directory listing succeeded."})
}

// writeLock rechecks persisted access and revisions after waiting for a lock.
func (s *Server) writeLock(r *http.Request, d store.Directory, c store.Connection, fs storage.FS, key string) (func(), error) {
	unlock, e := s.Repo.Lock(r.Context(), c.ID+"/"+d.Bucket+"/"+fs.PhysicalKey(key))
	if e != nil {
		return nil, e
	}
	current, e := s.Repo.Directory(r.Context(), d.Owner, d.ID)
	if e == nil {
		var conn store.Connection
		conn, e = s.Repo.Connection(r.Context(), d.Owner, c.ID)
		if e == nil {
			if !current.Writable(conn) {
				e = errReadOnly
			} else if current.Revision != d.Revision || conn.Revision != c.Revision {
				e = store.ErrConflict
			}
		}
	}
	if e != nil {
		unlock()
		return nil, e
	}
	return unlock, nil
}
func (s *Server) mutate(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	d, c, fs, e := s.open(r, true)
	if e != nil {
		s.operationError(w, r, e)
		return
	}
	defer fs.Close()
	select {
	case s.uploads <- struct{}{}:
		defer func() { <-s.uploads }()
	default:
		s.fail(w, r, errors.New("All write slots are busy. Try again shortly."), 429)
		return
	}
	if revision(r) != d.Revision {
		s.fail(w, r, store.ErrConflict, 409)
		return
	}
	key, e := storage.Path(r.FormValue("path"), false)
	if e != nil {
		s.fail(w, r, e, 422)
		return
	}
	unlock, e := s.writeLock(r, d, c, fs, key)
	if e != nil {
		s.operationError(w, r, e)
		return
	}
	defer unlock()
	switch {
	case strings.HasSuffix(r.URL.Path, "/mkdir"):
		e = fs.Mkdir(r.Context(), key)
	case strings.HasSuffix(r.URL.Path, "/delete-file"):
		if !checked(r, "confirm") {
			s.fail(w, r, errors.New("Confirm file deletion."), 422)
			return
		}
		e = fs.Remove(r.Context(), key, false)
	case strings.HasSuffix(r.URL.Path, "/rmdir"):
		if !checked(r, "confirm") {
			s.fail(w, r, errors.New("Confirm empty-folder removal."), 422)
			return
		}
		e = fs.Remove(r.Context(), key, true)
	}
	if e != nil {
		s.operationError(w, r, e)
		return
	}
	logging.For(r.Context()).Debug("Storage mutation completed", "event", "storage.mutation_completed", "operation", strings.TrimPrefix(r.Pattern, "POST /directories/{id}/"), "duration_ms", time.Since(start).Milliseconds())
	parent := path.Dir(key)
	if parent == "." {
		parent = ""
	}
	s.done(w, r, "/directories/"+d.ID+"?path="+url.QueryEscape(parent))
}

const maxUploadFiles = 100

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	d, c, fs, e := s.open(r, true)
	if e != nil {
		s.operationError(w, r, e)
		return
	}
	defer fs.Close()
	select {
	case s.uploads <- struct{}{}:
		defer func() { <-s.uploads }()
	default:
		s.fail(w, r, errors.New("All upload slots are busy. Try again shortly."), 429)
		return
	}
	query := r.URL.Query()
	fileLimit := maxUploadFiles
	if query.Get("replace") == "yes" {
		fileLimit = 1
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(fileLimit)*(s.Config.MaxUploadBytes+(64<<10)))
	reader, e := r.MultipartReader()
	if e != nil {
		s.fail(w, r, errors.New("Expected a multipart upload."), 422)
		return
	}
	// Destination and revision are URL form fields, so file-part order never
	// controls authorization and the body can stream without disk spooling.
	parent, e := storage.Path(query.Get("path"), true)
	if e != nil {
		s.fail(w, r, e, 422)
		return
	}
	rev, _ := strconv.ParseInt(query.Get("revision"), 10, 64)
	if rev != d.Revision {
		s.fail(w, r, store.ErrConflict, 409)
		return
	}
	destination := "/directories/" + d.ID + "?path=" + url.QueryEscape(parent)
	completed := 0
	var totalBytes int64
	fail := func(err error, status int) {
		s.failView(w, r, err, status, view{UploadCompleted: completed, ReturnURL: destination})
	}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var maxBytes *http.MaxBytesError
			if errors.As(err, &maxBytes) {
				fail(storage.ErrTooLarge, 413)
			} else {
				fail(errors.New("Incomplete or invalid multipart upload. Refresh the directory before retrying."), 422)
			}
			return
		}
		if completed == fileLimit {
			message := "Choose no more than " + strconv.Itoa(fileLimit) + " files per upload."
			if fileLimit == 1 {
				message = "Choose one replacement file."
			}
			fail(errors.New(message), 422)
			return
		}
		_, parameters, parseErr := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		if parseErr != nil || strings.ContainsAny(parameters["filename"], "/\\") {
			fail(storage.ErrInvalidPath, 422)
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			fail(errors.New("Choose files to upload."), 422)
			return
		}
		key, err := storage.Child(parent, part.FileName())
		if err != nil {
			fail(err, 422)
			return
		}
		limited := &limitReader{Reader: part, remaining: s.Config.MaxUploadBytes}
		err = s.uploadFile(r, d, c, fs, key, limited, query.Get("replace") == "yes")
		if limited.exceeded {
			err = storage.ErrTooLarge
		}
		if err != nil {
			status := 502
			if errors.Is(err, errReadOnly) {
				status = 403
			}
			fail(err, status)
			return
		}
		completed++
		totalBytes += s.Config.MaxUploadBytes - limited.remaining
	}
	if completed == 0 {
		fail(errors.New("Choose files to upload."), 422)
		return
	}
	logging.For(r.Context()).Debug("Upload completed", "event", "storage.upload_completed", "files", completed, "bytes", totalBytes, "duration_ms", time.Since(start).Milliseconds())
	s.done(w, r, destination)
}

// Release each object's lock before reading the next part, and recheck access
// and configuration for every file in the selection.
func (s *Server) uploadFile(r *http.Request, d store.Directory, c store.Connection, fs storage.FS, key string, in io.Reader, replace bool) error {
	unlock, e := s.writeLock(r, d, c, fs, key)
	if e != nil {
		return e
	}
	defer unlock()
	_, e = fs.Stat(r.Context(), key)
	if e == nil && !replace {
		return store.ErrConflict
	}
	if e != nil && !errors.Is(e, storage.ErrNotFound) {
		return e
	}
	return fs.Put(r.Context(), key, in)
}

type limitReader struct {
	io.Reader
	remaining int64
	exceeded  bool
}

func (l *limitReader) Read(p []byte) (int, error) {
	if l.remaining == 0 {
		var b [1]byte
		n, e := l.Reader.Read(b[:])
		if n > 0 {
			l.exceeded = true
			return 0, storage.ErrTooLarge
		}
		return 0, e
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, e := l.Reader.Read(p)
	var maxBytes *http.MaxBytesError
	if errors.As(e, &maxBytes) {
		l.exceeded = true
	}
	l.remaining -= int64(n)
	return n, e
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	_, _, fs, e := s.open(r, false)
	if e != nil {
		s.operationError(w, r, e)
		return
	}
	defer fs.Close()
	key, e := storage.Path(r.URL.Query().Get("path"), false)
	if e != nil {
		s.fail(w, r, e, 422)
		return
	}
	s.serveDownload(w, r, fs, key)
}

func (s *Server) serveDownload(w http.ResponseWriter, r *http.Request, fs storage.FS, key string) {
	contentType, disposition := "application/octet-stream", "attachment"
	if r.URL.Query().Get("inline") == "1" {
		contentType = mediaType(key)
		if contentType == "" {
			http.Error(w, "This file type cannot be previewed.", http.StatusUnsupportedMediaType)
			return
		}
		if contentType == "text/plain" || contentType == "text/markdown" {
			s.serveDocument(w, r, fs, key, contentType == "text/markdown")
			return
		}
		disposition = "inline"
		// A directly opened media document needs to load its own URL. SVG is
		// an active document format: deny scripts and all subresources there.
		policy := "default-src 'none'; media-src 'self'; img-src 'self'; sandbox allow-same-origin; frame-ancestors 'none'"
		if contentType == "image/svg+xml" {
			policy = "default-src 'none'; sandbox; frame-ancestors 'none'"
		}
		if contentType == "application/pdf" {
			// Native PDF viewers cannot run in a sandboxed plugin frame. The
			// browser isolates its viewer; permit embedding only by this origin.
			policy = "default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
		}
		w.Header().Set("Content-Security-Policy", policy)
	}
	info, e := fs.Stat(r.Context(), key)
	if e != nil {
		s.operationError(w, r, e)
		return
	}
	s.serveFile(w, r, fs, key, info, contentType, disposition)
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, fs storage.FS, key string, info storage.Entry, contentType, disposition string) {
	start, end, partial, e := parseRange(r.Header.Get("Range"), info.Size)
	if e != nil {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(info.Size, 10))
		w.WriteHeader(416)
		return
	}
	openStart := int64(-1)
	if partial {
		openStart = start
	}
	in, e := fs.Open(r.Context(), key, openStart, end)
	if e != nil {
		s.operationError(w, r, e)
		return
	}
	defer in.Close()
	if in.Size != info.Size {
		s.fail(w, r, store.ErrConflict, 409)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": path.Base(key)}))
	w.Header().Set("Accept-Ranges", "bytes")
	length := info.Size
	if partial {
		length = end - start + 1
		w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10)+"/"+strconv.FormatInt(info.Size, 10))
	}
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	if partial {
		w.WriteHeader(206)
	}
	if r.Method == http.MethodHead {
		return
	}
	if _, e = io.CopyN(w, in, length); e != nil {
		logging.Interrupted(r.Context(), e)
		panic(http.ErrAbortHandler)
	}
}
func parseRange(value string, size int64) (int64, int64, bool, error) {
	if value == "" {
		return 0, size - 1, false, nil
	}
	bad := errors.New("invalid range")
	if !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") || size == 0 {
		return 0, 0, false, bad
	}
	parts := strings.Split(strings.TrimPrefix(value, "bytes="), "-")
	if len(parts) != 2 {
		return 0, 0, false, bad
	}
	if parts[0] == "" {
		suffix, e := strconv.ParseInt(parts[1], 10, 64)
		if e != nil || suffix <= 0 {
			return 0, 0, false, bad
		}
		return max(0, size-suffix), size - 1, true, nil
	}
	start, e := strconv.ParseInt(parts[0], 10, 64)
	if e != nil || start < 0 || start >= size {
		return 0, 0, false, bad
	}
	end := size - 1
	if parts[1] != "" {
		end, e = strconv.ParseInt(parts[1], 10, 64)
		if e != nil || end < start {
			return 0, 0, false, bad
		}
		end = min(end, size-1)
	}
	return start, end, true, nil
}
