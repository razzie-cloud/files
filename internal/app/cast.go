package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/razzie-cloud/files/internal/logging"
	"github.com/razzie-cloud/files/internal/storage"
	"github.com/razzie-cloud/files/internal/store"
)

// Playback capabilities deliberately support fewer types than browser previews.
func castType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".mp4":
		return "video/mp4"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	}
	return ""
}

type castEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	URL  string `json:"url"`
	MIME string `json:"mime"`
}
type playback struct {
	Mode    string      `json:"mode"`
	Title   string      `json:"title"`
	Expires time.Time   `json:"expires"`
	Media   *castEntry  `json:"media,omitempty"`
	Entries []castEntry `json:"entries,omitempty"`
	Cleanup string      `json:"cleanup,omitempty"`
	ShareID string      `json:"share_id,omitempty"`
}
type playbackTicket struct {
	Version int    `json:"v"`
	Share   string `json:"share"`
	Path    string `json:"path"`
	Mode    string `json:"mode"`
	Expires int64  `json:"expires"`
}

func ticketMAC(v store.Share, payload string) []byte {
	mac := hmac.New(sha256.New, v.SessionKey)
	mac.Write([]byte("files-cast-playback-v1\x00" + v.ID + "\x00" + payload))
	return mac.Sum(nil)
}
func signPlayback(v store.Share, scope, mode string, expiry time.Time) string {
	data, _ := json.Marshal(playbackTicket{1, v.ID, scope, mode, expiry.Unix()})
	payload := base64.RawURLEncoding.EncodeToString(data)
	return payload + "." + base64.RawURLEncoding.EncodeToString(ticketMAC(v, payload))
}
func verifyPlayback(v store.Share, token string, now time.Time) (playbackTicket, error) {
	var t playbackTicket
	bad := errors.New("Invalid or expired playback ticket")
	if len(token) > 4096 || len(v.SessionKey) != 32 || v.Expired(now) {
		return t, bad
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return t, bad
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, ticketMAC(v, parts[0])) {
		return t, bad
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(data, &t) != nil {
		return t, bad
	}
	canonical, _ := json.Marshal(t)
	if !bytes.Equal(data, canonical) || t.Version != 1 || t.Share != v.ID || t.Expires <= now.Unix() || t.Expires > now.Add(24*time.Hour).Unix() || (v.ExpiresAt != nil && t.Expires > v.ExpiresAt.Unix()) {
		return t, bad
	}
	if t.Mode != "video" && t.Mode != "slideshow" {
		return t, bad
	}
	if _, err := sharedPath(v, t.Path); err != nil || (t.Mode == "slideshow" && !v.IsDirectory) {
		return t, bad
	}
	return t, nil
}
func (t playbackTicket) permits(relative, mode string) bool {
	if t.Mode != mode {
		return false
	}
	if relative == t.Path {
		return true
	}
	// A delegated slideshow grants only the chosen folder and its direct images.
	if mode != "slideshow" {
		return false
	}
	parent := path.Dir(relative)
	if parent == "." {
		parent = ""
	}
	return parent == t.Path && strings.HasPrefix(castType(relative), "image/")
}

func castJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(value)
}
func castError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": http.StatusText(status)})
}
func castStorageError(w http.ResponseWriter, err error) {
	code := http.StatusBadGateway
	switch {
	case errors.Is(err, storage.ErrNotFound), errors.Is(err, store.ErrNotFound):
		code = 404
	case errors.Is(err, storage.ErrInvalidPath):
		code = 422
	case errors.Is(err, storage.ErrTooLarge):
		code = 413
	case errors.Is(err, context.DeadlineExceeded):
		code = 504
	}
	castError(w, code)
}
func (s *Server) mediaURL(v store.Share, relative, ticket string) string {
	q := url.Values{"path": {relative}}
	if ticket != "" {
		q.Set("ticket", ticket)
	}
	return s.Config.ShareOrigin + "/s/" + v.ID + "/media?" + q.Encode()
}

// Validate at most 64 KiB, including for HEAD and range requests. This checks
// container/header identity, not codec compatibility or full-file integrity.
func validateCastMedia(ctx context.Context, fs storage.FS, key string) (storage.Entry, error) {
	info, err := fs.Stat(ctx, key)
	if err != nil {
		return info, err
	}
	kind := castType(key)
	if info.Directory || kind == "" || info.Size < 12 {
		return info, errCastMedia
	}
	in, err := fs.Open(ctx, key, 0, min(info.Size-1, 65535))
	if err != nil {
		return info, err
	}
	defer in.Close()
	if in.Size != info.Size {
		return info, store.ErrConflict
	}
	data, err := io.ReadAll(io.LimitReader(in, 65536))
	if err != nil {
		return info, err
	}
	if kind == "video/mp4" {
		if len(data) < 16 || string(data[4:8]) != "ftyp" {
			return info, errCastMedia
		}
		size := int(binary.BigEndian.Uint32(data[:4]))
		if size < 16 || size > len(data) || size%4 != 0 {
			return info, errCastMedia
		}
		allowed := false
		for offset := 8; offset < size; offset += 4 {
			if offset == 12 {
				continue
			} // minor version
			switch string(data[offset : offset+4]) {
			case "isom", "iso2", "iso3", "iso4", "iso5", "iso6", "mp41", "mp42", "avc1", "hvc1", "hev1":
				allowed = true
			case "avif", "avis", "heic", "heix", "mif1", "msf1":
				return info, errCastMedia
			}
		}
		if !allowed || info.Size < int64(size+8) || len(data) < size+8 {
			return info, errCastMedia
		}
		// Require a plausible next ISO BMFF box as well as ftyp. Payloads
		// and later boxes remain streamed, so this is not a full-file parser.
		next := uint64(binary.BigEndian.Uint32(data[size : size+4]))
		header := uint64(8)
		if next == 1 {
			if len(data) < size+16 {
				return info, errCastMedia
			}
			next = binary.BigEndian.Uint64(data[size+8 : size+16])
			header = 16
		}
		if next != 0 && (next < header || next > uint64(info.Size-int64(size))) {
			return info, errCastMedia
		}
		switch string(data[size+4 : size+8]) {
		case "moov", "mdat", "free", "skip", "wide", "uuid", "pdin", "sidx", "moof":
		default:
			return info, errCastMedia
		}
	} else {
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		expected := "jpeg"
		if kind == "image/png" {
			expected = "png"
		}
		if err != nil || format != expected || cfg.Width < 1 || cfg.Height < 1 {
			return info, errCastMedia
		}
	}
	return info, nil
}

var errCastMedia = errors.New("Unsupported or malformed Cast media")

func (s *Server) describePlayback(ctx context.Context, fs storage.FS, v store.Share, relative, mode, ticket string, expires time.Time) (playback, error) {
	out := playback{Mode: mode, Title: path.Base(v.Path), Expires: expires}
	key, err := sharedPath(v, relative)
	if err != nil {
		return out, err
	}
	if key == "" {
		out.Title = "Slideshow"
	} else {
		out.Title = path.Base(key)
	}
	if mode == "video" {
		if castType(key) != "video/mp4" {
			return out, errCastMedia
		}
		if _, err := validateCastMedia(ctx, fs, key); err != nil {
			return out, err
		}
		out.Media = &castEntry{Name: path.Base(key), Path: relative, URL: s.mediaURL(v, relative, ticket), MIME: "video/mp4"}
		return out, nil
	}
	if mode != "slideshow" || !v.IsDirectory {
		return out, storage.ErrInvalidPath
	}
	entries, err := fs.List(ctx, key)
	if errors.Is(err, storage.ErrNotFound) && key == "" {
		err = nil
	}
	if err != nil {
		return out, err
	}
	if s.Config.MaxEntries > 0 && len(entries) > s.Config.MaxEntries {
		return out, storage.ErrTooLarge
	}
	out.Entries = []castEntry{}
	for _, entry := range entries {
		child, err := storage.Child(relative, entry.Name)
		if err != nil || entry.Directory || !strings.HasPrefix(castType(entry.Name), "image/") {
			continue
		}
		// Validate combined paths too; never use a backend's Path field.
		if _, err := sharedPath(v, child); err != nil {
			continue
		}
		out.Entries = append(out.Entries, castEntry{Name: entry.Name, Path: child, URL: s.mediaURL(v, child, ticket), MIME: castType(entry.Name)})
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Name < out.Entries[j].Name })
	return out, nil
}
func playbackError(w http.ResponseWriter, err error) {
	if errors.Is(err, errCastMedia) {
		castError(w, 415)
	} else {
		castStorageError(w, err)
	}
}
func (s *Server) createCast(w http.ResponseWriter, r *http.Request) {
	if s.Config.ShareOrigin == "" {
		castError(w, 404)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	d, _, fs, err := s.open(r, false)
	if err != nil {
		castStorageError(w, err)
		return
	}
	defer fs.Close()
	key, err := storage.Path(r.PostForm.Get("path"), true)
	mode := r.PostForm.Get("mode")
	if err != nil || (mode != "video" && mode != "slideshow") {
		castError(w, 422)
		return
	}
	expires := time.Now().Add(s.Config.CastShareTTL)
	v := store.Share{Owner: d.Owner, DirectoryID: d.ID, Path: key, IsDirectory: mode == "slideshow", ExpiresAt: &expires}
	// Validate existence and listing before persisting a capability.
	out, err := s.describePlayback(ctx, fs, v, "", mode, "", expires)
	if err != nil {
		playbackError(w, err)
		return
	}
	if mode == "slideshow" && len(out.Entries) == 0 {
		castError(w, 422)
		return
	}
	if err := s.Repo.CreateShare(ctx, &v); err != nil {
		castStorageError(w, err)
		return
	}
	s.shares.invalidate()
	logging.For(r.Context()).Info("Share created", "event", "share.created", "expires_at", v.ExpiresAt, "password_protected", false)
	// Rebuild URLs after persistence assigned the random ID. No further I/O can
	// fail between creation and the response (a lost response falls back to TTL).
	if out.Media != nil {
		out.Media.URL = s.mediaURL(v, "", "")
	}
	for i := range out.Entries {
		out.Entries[i].URL = s.mediaURL(v, out.Entries[i].Path, "")
	}
	out.ShareID = v.ID
	out.Cleanup = "/directories/" + d.ID + "/shares/" + v.ID + "/remove"
	castJSON(w, out)
}

func (s *Server) castSnapshot(w http.ResponseWriter, r *http.Request) (shareSnapshot, bool) {
	id := r.PathValue("share")
	if !store.ValidShareID(id) {
		castError(w, 404)
		return shareSnapshot{}, false
	}
	snapshot, err := s.shares.get(r.Context(), id, s.loadShare)
	if err != nil {
		castStorageError(w, err)
		return snapshot, false
	}
	if snapshot.Share.Expired(time.Now()) {
		castError(w, 404)
		return snapshot, false
	}
	return snapshot, true
}
func (s *Server) publicCast(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	snapshot, ok := s.castSnapshot(w, r)
	if !ok {
		return
	}
	v := snapshot.Share
	now := time.Now()
	if v.PasswordHash != "" && !validShareSession(r, v, now) {
		castError(w, 403)
		return
	}
	relative, err := storage.Path(r.PostForm.Get("path"), true)
	mode := r.PostForm.Get("mode")
	if err != nil || (mode != "video" && mode != "slideshow") {
		castError(w, 422)
		return
	}
	if _, err := sharedPath(v, relative); err != nil || (mode == "slideshow" && !v.IsDirectory) {
		castError(w, 404)
		return
	}
	expires := now.Add(s.Config.CastShareTTL)
	if v.ExpiresAt != nil && v.ExpiresAt.Before(expires) {
		expires = *v.ExpiresAt
	}
	ticket := ""
	if v.PasswordHash != "" {
		cookie, _ := r.Cookie(shareCookieName) // already verified
		value, _, _ := strings.Cut(cookie.Value, ".")
		seconds, _ := strconv.ParseInt(value, 10, 64)
		sessionEnd := time.Unix(seconds, 0)
		if sessionEnd.Before(expires) {
			expires = sessionEnd
		}
		ticket = signPlayback(v, relative, mode, expires)
	}
	fs, err := s.Storage.Open(ctx, snapshot.Connection, snapshot.Directory, snapshot.Credentials, snapshot.Crypt)
	if err != nil {
		castStorageError(w, err)
		return
	}
	defer fs.Close()
	out, err := s.describePlayback(ctx, fs, v, relative, mode, ticket, expires)
	if err != nil {
		playbackError(w, err)
		return
	}
	castJSON(w, out)
}

// GET/HEAD APIs never turn missing authorization into a password HTML page.
func authorizePlayback(w http.ResponseWriter, r *http.Request, v store.Share, relative, mode string, listing bool) bool {
	token := r.URL.Query().Get("ticket")
	if token != "" {
		ticket, err := verifyPlayback(v, token, time.Now())
		if err != nil || !ticket.permits(relative, mode) || (listing && relative != ticket.Path) {
			castError(w, 403)
			return false
		}
	} else if v.PasswordHash != "" {
		castError(w, 403)
		return false
	}
	return true
}
func (s *Server) publicSlideshow(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := s.castSnapshot(w, r)
	if !ok {
		return
	}
	v := snapshot.Share
	relative, err := storage.Path(r.URL.Query().Get("path"), true)
	if err != nil {
		castError(w, 422)
		return
	}
	if !authorizePlayback(w, r, v, relative, "slideshow", true) {
		return
	}
	fs, err := s.Storage.Open(r.Context(), snapshot.Connection, snapshot.Directory, snapshot.Credentials, snapshot.Crypt)
	if err != nil {
		castStorageError(w, err)
		return
	}
	defer fs.Close()
	expires := time.Now().Add(s.Config.CastShareTTL)
	if v.ExpiresAt != nil && v.ExpiresAt.Before(expires) {
		expires = *v.ExpiresAt
	}
	if token := r.URL.Query().Get("ticket"); token != "" {
		ticket, _ := verifyPlayback(v, token, time.Now())
		expires = time.Unix(ticket.Expires, 0)
	}
	out, err := s.describePlayback(r.Context(), fs, v, relative, "slideshow", r.URL.Query().Get("ticket"), expires)
	if err != nil {
		playbackError(w, err)
		return
	}
	castJSON(w, out)
}
func (s *Server) publicMedia(w http.ResponseWriter, r *http.Request) {
	// Default receiver and same-deployment sender HEAD checks use explicit,
	// noncredentialed CORS. Capability requests without Origin remain valid.
	w.Header().Add("Vary", "Origin")
	origin := r.Header.Get("Origin")
	if origin == "https://www.gstatic.com" || origin == s.Config.Origin || origin == s.Config.ShareOrigin {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}
	w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges")
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	snapshot, ok := s.castSnapshot(w, r.WithContext(ctx))
	if !ok {
		return
	}
	v := snapshot.Share
	relative, err := storage.Path(r.URL.Query().Get("path"), true)
	if err != nil {
		castError(w, 422)
		return
	}
	key, err := sharedPath(v, relative)
	if err != nil {
		castError(w, 404)
		return
	}
	mode := "video"
	if strings.HasPrefix(castType(key), "image/") {
		mode = "slideshow"
	}
	if !authorizePlayback(w, r, v, relative, mode, false) {
		return
	}
	fs, err := s.Storage.Open(ctx, snapshot.Connection, snapshot.Directory, snapshot.Credentials, snapshot.Crypt)
	if err != nil {
		castStorageError(w, err)
		return
	}
	defer fs.Close()
	info, err := validateCastMedia(ctx, fs, key)
	if err != nil {
		playbackError(w, err)
		return
	}
	cancel() // Only authorization, storage setup and header checks are bounded.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox; frame-ancestors 'none'")
	s.serveFile(w, r, fs, key, info, castType(key), "inline")
}
