// Package sandboxsync supplies byte-preserving manifests and safe portable transfers.
package sandboxsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"local-agent-workbench/internal/filepolicy"
)

type Entry struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory,omitempty"`
	Hash      string `json:"hash,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Mode      uint32 `json:"mode,omitempty"`
}
type Manifest struct {
	Rules   string  `json:"rules"`
	Entries []Entry `json:"entries"`
	Digest  string  `json:"digest"`
}
type Change struct {
	Entry
	Delete bool   `json:"delete,omitempty"`
	Data   []byte `json:"data,omitempty"`
}

func Scan(ctx context.Context, root, rules string) (Manifest, error) {
	if !filepolicy.ValidVersion(rules) {
		return Manifest{}, fmt.Errorf("unknown file rules %q", rules)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return Manifest{}, err
	}
	if !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return Manifest{}, fmt.Errorf("unsafe manifest root %q", root)
	}
	m := Manifest{Rules: rules, Entries: []Entry{}}
	seen := map[string]string{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() && filepolicy.SkipDirectoryPath(rules, rel, false) {
			return filepath.SkipDir
		}
		if d.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 || (!d.IsDir() && filepolicy.SkipFile(d.Name())) {
			return nil
		}
		if rules == filepolicy.Current {
			if err := filepolicy.ValidatePath(rel); err != nil {
				return err
			}
			key := filepolicy.Fold(rel)
			if previous, ok := seen[key]; ok && previous != rel {
				return fmt.Errorf("case-colliding paths %q and %q", previous, rel)
			}
			seen[key] = rel
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		entry := Entry{Path: rel, Directory: d.IsDir(), Mode: uint32(info.Mode().Perm())}
		if !d.IsDir() {
			if !info.Mode().IsRegular() {
				return nil
			}
			file, err := os.Open(p)
			if err != nil {
				return err
			}
			h := sha256.New()
			entry.Size, err = io.Copy(h, file)
			closeErr := file.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			entry.Hash = hex.EncodeToString(h.Sum(nil))
		}
		m.Entries = append(m.Entries, entry)
		return nil
	})
	if err != nil {
		return Manifest{}, err
	}
	m.Digest = Digest(m.Entries)
	return m, nil
}

// Digest preserves WalkDir's depth-first ordering, including empty directories.
func Digest(entries []Entry) string {
	ordered := append([]Entry(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool { return treeLess(ordered[i].Path, ordered[j].Path) })
	h := sha256.New()
	for _, e := range ordered {
		if e.Directory {
			h.Write([]byte("d\x00" + e.Path + "\x00"))
		} else {
			h.Write([]byte("f\x00" + e.Path + "\x00"))
			value, _ := hex.DecodeString(e.Hash)
			h.Write(value)
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
func treeLess(a, b string) bool {
	x, y := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return len(x) < len(y)
}

func Changes(before, after Manifest) []Change {
	old := map[string]Entry{}
	next := map[string]Entry{}
	for _, e := range before.Entries {
		old[e.Path] = e
	}
	for _, e := range after.Entries {
		next[e.Path] = e
	}
	changes := []Change{}
	for _, e := range before.Entries {
		if n, ok := next[e.Path]; !ok || n.Directory != e.Directory {
			changes = append(changes, Change{Entry: e, Delete: true})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return len(changes[i].Path) > len(changes[j].Path) })
	for _, e := range after.Entries {
		o, ok := old[e.Path]
		if !ok || o.Directory != e.Directory || o.Hash != e.Hash {
			changes = append(changes, Change{Entry: e})
		}
	}
	return changes
}

// Resolve checks every existing path component; caller must serialize mutations.
func Resolve(root, rel string) (string, error) {
	if err := filepolicy.ValidatePath(rel); err != nil {
		return "", err
	}
	p := root
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return "", fmt.Errorf("unsafe root %q", root)
	}
	for _, c := range strings.Split(rel, "/") {
		p = filepath.Join(p, c)
		info, err = os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return "", fmt.Errorf("link at %q", rel)
		}
	}
	return p, nil
}

func ReadChange(root string, change Change) (Change, error) {
	if change.Delete || change.Directory {
		return change, nil
	}
	p, err := Resolve(root, change.Path)
	if err != nil {
		return change, err
	}
	change.Data, err = os.ReadFile(p)
	if err != nil {
		return change, err
	}
	h := sha256.Sum256(change.Data)
	if hex.EncodeToString(h[:]) != change.Hash {
		return change, fmt.Errorf("file changed while transferring %s", change.Path)
	}
	return change, nil
}

func Apply(root string, changes []Change) error {
	return apply(root, changes, true)
}

// Seed applies to a newly created, unpublished copy. It retains safe paths,
// hashes and atomic file replacement, but leaves durability to the copy owner.
// The ordinary operation journal must use Apply instead.
func Seed(root string, changes []Change) error {
	return apply(root, changes, false)
}

func apply(root string, changes []Change, durable bool) error {
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	for _, c := range changes {
		if filepolicy.ExcludedPath(filepolicy.Current, c.Path, c.Directory) {
			return fmt.Errorf("excluded transfer path %s", c.Path)
		}
		p, err := Resolve(root, c.Path)
		if err != nil {
			return err
		}
		if c.Delete {
			err = os.Remove(p)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if c.Directory {
			if err = os.MkdirAll(p, 0755); err != nil {
				return err
			}
			continue
		}
		h := sha256.Sum256(c.Data)
		if hex.EncodeToString(h[:]) != c.Hash || int64(len(c.Data)) != c.Size {
			return fmt.Errorf("invalid transfer hash/size %s", c.Path)
		}
		if err = os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			return err
		}
		f, err := os.CreateTemp(filepath.Dir(p), ".point-transfer-")
		if err != nil {
			return err
		}
		tmp := f.Name()
		_, err = f.Write(c.Data)
		if err == nil && durable {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Chmod(tmp, fs.FileMode(c.Mode)&0777)
		}
		if err == nil {
			err = os.Rename(tmp, p)
		}
		if err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	return nil
}
