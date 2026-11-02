package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/razzie-cloud/files/internal/storage"
	"github.com/razzie-cloud/files/internal/store"
	"github.com/razzie-cloud/files/internal/web"
	"golang.org/x/crypto/bcrypt"
)

const shareSessionTTL = 12 * time.Hour
const shareCookieName = "files_share_session"

func sameHost(host, origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(host, u.Host)
}

func shareExpiration(value string, now time.Time) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	minutes, err := strconv.ParseInt(value, 10, 64)
	if err != nil || minutes < 1 || minutes > int64((1<<63-1)/time.Minute) {
		return nil, errors.New("Enter a positive whole number of minutes, or leave expiration blank.")
	}
	expires := now.Add(time.Duration(minutes) * time.Minute)
	return &expires, nil
}
func (s *Server) shareForm(w http.ResponseWriter, r *http.Request) {
	if s.Config.ShareOrigin == "" {
		http.NotFound(w, r)
		return
	}
	d, err := s.Repo.Directory(r.Context(), user(r).ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err, 500)
		return
	}
	shares, err := s.Repo.Shares(r.Context(), d.Owner, d.ID)
	if err != nil {
		s.fail(w, r, err, 500)
		return
	}
	key, err := storage.Path(r.URL.Query().Get("path"), true)
	if err != nil {
		s.fail(w, r, err, 422)
		return
	}
	s.render(w, r, view{Page: "shares", Directory: &d, Shares: shares, Path: key, ShareURL: s.Config.ShareOrigin, ShareDirectory: r.URL.Query().Get("kind") != "file"})
}
func (s *Server) createShare(w http.ResponseWriter, r *http.Request) {
	if s.Config.ShareOrigin == "" {
		http.NotFound(w, r)
		return
	}
	expires, err := shareExpiration(r.FormValue("expires_in_minutes"), time.Now())
	if err != nil {
		s.fail(w, r, err, 422)
		return
	}
	d, _, fs, err := s.open(r, false)
	if err != nil {
		s.operationError(w, r, err)
		return
	}
	defer fs.Close()
	key, err := storage.Path(r.FormValue("path"), true)
	if err != nil {
		s.fail(w, r, err, 422)
		return
	}
	kind := r.FormValue("kind")
	if kind != "directory" && kind != "file" {
		s.fail(w, r, errors.New("Select a file or directory."), 422)
		return
	}
	v := store.Share{Owner: d.Owner, DirectoryID: d.ID, Path: key, IsDirectory: kind == "directory", ExpiresAt: expires}
	if v.IsDirectory {
		_, err = fs.List(r.Context(), key)
		if errors.Is(err, storage.ErrNotFound) && key == "" {
			err = nil
		}
	} else {
		var info storage.Entry
		info, err = fs.Stat(r.Context(), key)
		if err == nil && info.Directory {
			err = storage.ErrInvalidPath
		}
	}
	if err != nil {
		s.operationError(w, r, err)
		return
	}

	password := r.FormValue("password")
	if len(password) > 72 {
		s.fail(w, r, errors.New("Use a password of at most 72 bytes."), 422)
		return
	}
	if password != "" {
		select {
		case s.passwordSlots <- struct{}{}:
			defer func() { <-s.passwordSlots }()
		default:
			s.fail(w, r, errors.New("Password checks are busy. Try again shortly."), 429)
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			s.fail(w, r, err, 500)
			return
		}
		v.PasswordHash = string(hash)
	}
	if err := s.Repo.CreateShare(r.Context(), &v); err != nil {
		s.fail(w, r, err, 500)
		return
	}
	s.shares.invalidate()
	s.done(w, r, "/directories/"+d.ID+"/shares")
}
func (s *Server) removeShare(w http.ResponseWriter, r *http.Request) {
	if s.Config.ShareOrigin == "" {
		http.NotFound(w, r)
		return
	}
	d, err := s.Repo.Directory(r.Context(), user(r).ID, r.PathValue("id"))
	if err == nil {
		err = s.Repo.RemoveShare(r.Context(), d.Owner, d.ID, r.PathValue("share"))
	}
	if err != nil {
		s.fail(w, r, err, 500)
		return
	}
	s.shares.invalidate()
	s.done(w, r, "/directories/"+d.ID+"/shares")
}

func (s *Server) publicHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(web.Static())))
	mux.HandleFunc("GET /s/{share}", s.publicShare)
	mux.HandleFunc("GET /s/{share}/download", s.publicShare)
	mux.HandleFunc("POST /s/{share}/unlock", s.publicShare)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Neither proxy identity headers nor admin routes have meaning here.
		if r.Method == http.MethodPost {
			if len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.Config.ShareOrigin {
				http.Error(w, "Invalid request origin", 403)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			if err := r.ParseForm(); err != nil {
				http.Error(w, "Invalid form", 422)
				return
			}
		}
		if r.Method == http.MethodGet && !strings.HasSuffix(r.URL.Path, "/download") {
			ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
		}
		mux.ServeHTTP(w, r)
	})
}

func shareMAC(v store.Share, expiry string) string {
	mac := hmac.New(sha256.New, v.SessionKey)
	mac.Write([]byte(v.ID + ":" + expiry))
	return hex.EncodeToString(mac.Sum(nil))
}
func validShareSession(r *http.Request, v store.Share, now time.Time) bool {
	if v.Expired(now) {
		return false
	}
	c, err := r.Cookie(shareCookieName)
	if err != nil || len(c.Value) > 128 || len(v.SessionKey) != 32 {
		return false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 2 {
		return false
	}
	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || expiry <= now.Unix() || expiry > now.Add(shareSessionTTL).Unix() {
		return false
	}
	return hmac.Equal([]byte(parts[1]), []byte(shareMAC(v, parts[0])))
}

// sharedPath accepts only a relative path beneath this immutable share root.
// File shares ignore no parameters: any nonempty relative path is rejected.
func sharedPath(v store.Share, relative string) (string, error) {
	if _, err := storage.Path(v.Path, v.IsDirectory); err != nil {
		return "", err
	}
	if _, err := storage.Path(relative, true); err != nil {
		return "", err
	}
	if !v.IsDirectory && relative != "" {
		return "", store.ErrNotFound
	}
	if relative == "" {
		return v.Path, nil
	}
	if v.Path == "" {
		return relative, nil
	}
	return storage.Path(v.Path+"/"+relative, false)
}

func (s *Server) publicShare(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("share")
	if !store.ValidShareID(id) {
		http.NotFound(w, r)
		return
	}
	snapshot, err := s.shares.get(r.Context(), id, s.loadShare)
	if err != nil {
		s.fail(w, r, err, 503)
		return
	}
	v := snapshot.Share
	if v.Expired(time.Now()) {
		s.fail(w, r, store.ErrNotFound, 404)
		return
	}
	base := "/s/" + v.ID
	if r.Method == http.MethodPost {
		if v.PasswordHash == "" {
			s.done(w, r, base)
			return
		}
		if !s.passwordRate.Allow() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Try again shortly", 429)
			return
		}
		select {
		case s.passwordSlots <- struct{}{}:
			defer func() { <-s.passwordSlots }()
		default:
			http.Error(w, "Try again shortly", 429)
			return
		}
		password := r.PostForm.Get("password")
		if len(password) > 72 || bcrypt.CompareHashAndPassword([]byte(v.PasswordHash), []byte(password)) != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			s.render(w, r, view{Page: "share-password", ShareURL: base, Message: "Incorrect password."})
			return
		}
		now := time.Now()
		sessionEnd := now.Add(shareSessionTTL)
		if v.ExpiresAt != nil && v.ExpiresAt.Before(sessionEnd) {
			sessionEnd = *v.ExpiresAt
		}
		if !now.Before(sessionEnd) {
			s.fail(w, r, store.ErrNotFound, 404)
			return
		}
		expiry := strconv.FormatInt(sessionEnd.Unix(), 10)
		http.SetCookie(w, &http.Cookie{Name: shareCookieName, Value: expiry + "." + shareMAC(v, expiry), Path: base, Secure: strings.HasPrefix(s.Config.ShareOrigin, "https://"), HttpOnly: true, SameSite: http.SameSiteLaxMode})
		s.done(w, r, base)
		return
	}
	if v.PasswordHash != "" && !validShareSession(r, v, time.Now()) {
		s.render(w, r, view{Page: "share-password", ShareURL: base})
		return
	}
	relative, err := storage.Path(r.URL.Query().Get("path"), true)
	if err != nil {
		s.fail(w, r, err, 422)
		return
	}
	key, err := sharedPath(v, relative)
	if err != nil {
		s.fail(w, r, err, 404)
		return
	}
	fs, err := s.Storage.Open(r.Context(), snapshot.Connection, snapshot.Directory, snapshot.Credentials, snapshot.Crypt)
	if err != nil {
		s.operationError(w, r, err)
		return
	}
	defer fs.Close()
	if strings.HasSuffix(r.URL.Path, "/download") {
		s.serveDownload(w, r, fs, key)
		return
	}
	name := path.Base(v.Path)
	if v.Path == "" {
		name = snapshot.Directory.Name
	}
	page := view{Page: "shared", ShareURL: base, Path: relative, Message: name}
	if v.IsDirectory {
		entries, err := fs.List(r.Context(), key)
		if errors.Is(err, storage.ErrNotFound) && key == "" {
			err = nil
		}
		if err != nil {
			s.operationError(w, r, err)
			return
		}
		// Rebuild links from names; never trust a backend path to escape share scope.
		for _, entry := range entries {
			child, err := storage.Child(relative, entry.Name)
			if err != nil {
				continue
			}
			entry.Path = child
			page.Entries = append(page.Entries, entry)
		}
		index, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if index < 0 || index > len(page.Entries)/100 {
			index = 0
		}
		start := index * 100
		end := min(start+100, len(page.Entries))
		paging := base + "?path=" + url.QueryEscape(relative) + "&page="
		if index > 0 {
			page.Previous = paging + strconv.Itoa(index-1)
		}
		if end < len(page.Entries) {
			page.Next = paging + strconv.Itoa(index+1)
		}
		page.Entries = page.Entries[start:end]
		if relative != "" {
			parent := path.Dir(relative)
			if parent == "." {
				parent = ""
			}
			page.Parent = base + "?path=" + url.QueryEscape(parent)
		}
		page.Crumbs = []crumb{{name, base}}
	} else {
		info, err := fs.Stat(r.Context(), key)
		if err != nil {
			s.operationError(w, r, err)
			return
		}
		if info.Directory {
			s.fail(w, r, store.ErrNotFound, 404)
			return
		}
		info.Path = ""
		info.Name = name
		page.Entries = []storage.Entry{info}
	}
	s.render(w, r, page)
}
