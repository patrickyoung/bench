package contractexec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

const (
	maxInventory = 256
	maxEvidence  = 1 << 20
)

var ignored = map[string]bool{
	".bench": true, ".git": true, ".hg": true, ".svn": true,
	"node_modules": true, "vendor": true, ".next": true,
}

// Evidence is a bounded, deterministic, read-only view of what the compiler
// was allowed to use. These exact bytes are sent to Ask and therefore land in
// the session log; replay never depends on a later rescan of a changed tree.
func Evidence(root, input string) string {
	var paths []string
	truncated := false
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == root {
			return nil
		}
		name := entry.Name()
		if entry.IsDir() && ignored[name] {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if len(paths) >= maxInventory {
			truncated = true
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			rel += string(filepath.Separator)
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	slices.Sort(paths)
	var b strings.Builder
	b.WriteString("READ-ONLY WORKSPACE INVENTORY\n")
	fmt.Fprintf(&b, "root: %s\n", root)
	for _, path := range paths {
		fmt.Fprintf(&b, "- %s\n", path)
	}
	if truncated {
		fmt.Fprintf(&b, "- [inventory truncated after %d entries]\n", maxInventory)
	}
	if input != "" {
		b.WriteString("\nUSER-SUPPLIED INPUT\n")
		if len(input) <= maxEvidence {
			b.WriteString(input)
			if !strings.HasSuffix(input, "\n") {
				b.WriteByte('\n')
			}
		} else {
			sum := sha256.Sum256([]byte(input))
			fmt.Fprintf(&b, "[omitted from contract turn: %d bytes, sha256 %s; the worker receives the exact bytes]\n",
				len(input), hex.EncodeToString(sum[:]))
		}
	}
	return b.String()
}
