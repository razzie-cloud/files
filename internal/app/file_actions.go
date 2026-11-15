package app

import (
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/razzie-cloud/files/internal/storage"
	"github.com/razzie-cloud/files/internal/store"
)

type fileActionView struct {
	Kind, Source, Name, Parent, Destination string
}
type folderNode struct {
	Name, Path, DirectoryID string
}

func fileParent(key string) string {
	parent := path.Dir(key)
	if parent == "." {
		return ""
	}
	return parent
}

func (s *Server) fileActionForm(w http.ResponseWriter, r *http.Request) {
	d, c, fs, err := s.open(r, true)
	if err != nil {
		s.operationError(w, r, err)
		return
	}
	defer fs.Close()
	source, err := storage.Path(r.URL.Query().Get("path"), false)
	if err == nil {
		_, err = fs.Stat(r.Context(), source)
	}
	if err != nil {
		s.operationError(w, r, err)
		return
	}
	kind := "rename"
	if strings.HasSuffix(r.URL.Path, "/move") {
		kind = "move"
	}
	v := view{Page: "file-action", Directory: &d, Connection: &c, Writable: true,
		FileAction: &fileActionView{Kind: kind, Source: source, Name: path.Base(source), Parent: fileParent(source), Destination: fileParent(source)}}
	if kind == "move" {
		v.Folders, err = listFolders(r, fs, "", d.ID)
		if err != nil {
			s.operationError(w, r, err)
			return
		}
	}
	s.render(w, r, v)
}

func listFolders(r *http.Request, fs storage.FS, parent, directoryID string) ([]folderNode, error) {
	entries, err := fs.List(r.Context(), parent)
	if errors.Is(err, storage.ErrNotFound) && parent == "" {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	nodes := []folderNode{}
	seen := map[string]bool{}
	for _, entry := range entries {
		if !entry.Directory || seen[entry.Path] {
			continue
		}
		// Reject unexpected recursive/out-of-root entries from a backend.
		if expected, err := storage.Child(parent, entry.Name); err != nil || entry.Path != expected {
			return nil, storage.ErrInvalidPath
		}
		seen[entry.Path] = true
		nodes = append(nodes, folderNode{Name: entry.Name, Path: entry.Path, DirectoryID: directoryID})
	}
	return nodes, nil
}

func (s *Server) folderChildren(w http.ResponseWriter, r *http.Request) {
	// Failures go beside the retained form, rather than replacing its tree.
	r.Header.Set("HX-Target", "file-action-feedback")
	d, _, fs, err := s.open(r, true)
	if err != nil {
		s.operationError(w, r, err)
		return
	}
	defer fs.Close()
	parent, err := storage.Path(r.URL.Query().Get("path"), true)
	if err != nil {
		s.fail(w, r, err, 422)
		return
	}
	nodes, err := listFolders(r, fs, parent, d.ID)
	if err != nil {
		s.operationError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "folder-children", view{Folders: nodes}); err != nil {
		s.fail(w, r, err, 500)
	}
}

func (s *Server) moveFile(w http.ResponseWriter, r *http.Request) {
	d, c, fs, err := s.open(r, true)
	if err != nil {
		s.operationError(w, r, err)
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
	source, err := storage.Path(r.FormValue("path"), false)
	if err != nil {
		s.fail(w, r, err, 422)
		return
	}
	parent := fileParent(source)
	var destination string
	if strings.HasSuffix(r.URL.Path, "/rename") {
		destination, err = storage.Child(parent, r.FormValue("name"))
	} else {
		var target string
		target, err = storage.Path(r.FormValue("destination"), true)
		if err == nil {
			destination, err = storage.Child(target, path.Base(source))
		}
	}
	if err != nil {
		s.fail(w, r, err, 422)
		return
	}
	if source == destination {
		s.fail(w, r, errors.New("Choose a different name or destination folder."), 422)
		return
	}
	keys := []string{c.ID + "/" + d.Bucket + "/" + fs.PhysicalKey(source), c.ID + "/" + d.Bucket + "/" + fs.PhysicalKey(destination)}
	unlock, err := s.Repo.LockMany(r.Context(), keys)
	if err == nil {
		unlock, err = s.checkWriteLock(r, d, c, unlock)
	}
	if err != nil {
		s.operationError(w, r, err)
		return
	}
	defer unlock()
	if err = fs.Move(r.Context(), source, destination); err != nil {
		s.operationError(w, r, err)
		return
	}
	result := "moved"
	if strings.HasSuffix(r.URL.Path, "/rename") {
		result = "renamed"
	}
	s.done(w, r, "/directories/"+d.ID+"?path="+url.QueryEscape(parent)+"&file_action="+result)
}

func (s *Server) createMoveFolder(w http.ResponseWriter, r *http.Request) {
	d, c, fs, err := s.open(r, true)
	if err != nil {
		s.operationError(w, r, err)
		return
	}
	defer fs.Close()
	if revision(r) != d.Revision {
		s.fail(w, r, store.ErrConflict, 409)
		return
	}
	parent, err := storage.Path(r.FormValue("destination"), true)
	var key string
	if err == nil {
		key, err = storage.Child(parent, r.FormValue("name"))
	}
	if err != nil {
		s.fail(w, r, err, 422)
		return
	}
	select {
	case s.uploads <- struct{}{}:
		defer func() { <-s.uploads }()
	default:
		s.fail(w, r, errors.New("All write slots are busy. Try again shortly."), 429)
		return
	}
	unlock, err := s.writeLock(r, d, c, fs, key)
	if err != nil {
		s.operationError(w, r, err)
		return
	}
	defer unlock()
	entries, err := fs.List(r.Context(), parent)
	if errors.Is(err, storage.ErrNotFound) && parent == "" {
		err = nil
	}
	for _, entry := range entries {
		if entry.Name == path.Base(key) {
			err = storage.ErrConflict
		}
	}
	if err == nil {
		err = fs.Mkdir(r.Context(), key)
	}
	if err != nil {
		s.operationError(w, r, err)
		return
	}
	if r.Header.Get("HX-Request") != "true" {
		s.done(w, r, "/directories/"+d.ID+"?path="+url.QueryEscape(parent))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("HX-Retarget", "#folder-create-result")
	if err := s.templates.ExecuteTemplate(w, "folder-created", folderNode{Name: path.Base(key), Path: key, DirectoryID: d.ID}); err != nil {
		s.fail(w, r, err, 500)
	}
}
