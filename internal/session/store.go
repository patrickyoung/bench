// Package session discovers bench-owned ask sessions without interpreting
// their contents. ask remains responsible for validating and rendering them.
package session

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Info is filesystem metadata safe to show in the session picker.
type Info struct {
	Path     string
	Name     string
	Modified time.Time
	Size     int64
}

// Discover lists regular JSONL files newest first. A missing directory is an
// empty history, not an error, so merely opening bench creates nothing.
func Discover(dir string) ([]Info, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var found []Info
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		stat, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !stat.Mode().IsRegular() {
			continue
		}
		found = append(found, Info{
			Path:     filepath.Join(dir, entry.Name()),
			Name:     strings.TrimSuffix(entry.Name(), ".jsonl"),
			Modified: stat.ModTime(),
			Size:     stat.Size(),
		})
	}
	slices.SortFunc(found, func(a, b Info) int {
		if n := b.Modified.Compare(a.Modified); n != 0 {
			return n
		}
		return strings.Compare(a.Name, b.Name)
	})
	return found, nil
}

// Resolve accepts either an explicit path or an id within dir. It never picks
// a newest session implicitly.
func Resolve(dir, value string) string {
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) || strings.ContainsRune(value, os.PathSeparator) {
		return value
	}
	return filepath.Join(dir, strings.TrimSuffix(value, ".jsonl")+".jsonl")
}
