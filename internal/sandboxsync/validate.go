package sandboxsync

import (
	"encoding/hex"
	"fmt"
	"path"
	"strings"

	"local-agent-workbench/internal/filepolicy"
)

func ValidateManifest(m Manifest) error {
	if m.Rules != filepolicy.Current {
		return fmt.Errorf("unsupported manifest rules %q", m.Rules)
	}
	seen := map[string]Entry{}
	folded := map[string]string{}
	for i, e := range m.Entries {
		if err := filepolicy.ValidatePath(e.Path); err != nil {
			return err
		}
		if i > 0 && !treeLess(m.Entries[i-1].Path, e.Path) {
			return fmt.Errorf("manifest order or duplicate path %q", e.Path)
		}
		if filepolicy.ExcludedPath(m.Rules, e.Path, e.Directory) {
			return fmt.Errorf("excluded manifest path %q", e.Path)
		}
		key := filepolicy.Fold(e.Path)
		if previous, ok := folded[key]; ok {
			return fmt.Errorf("case-colliding paths %q and %q", previous, e.Path)
		}
		folded[key] = e.Path
		if parent := path.Dir(e.Path); parent != "." {
			if p, ok := seen[parent]; !ok || !p.Directory {
				return fmt.Errorf("missing directory for %q", e.Path)
			}
		}
		if e.Mode&^uint32(0777) != 0 || e.Size < 0 {
			return fmt.Errorf("invalid file metadata %q", e.Path)
		}
		if e.Directory {
			if e.Hash != "" || e.Size != 0 {
				return fmt.Errorf("invalid directory metadata %q", e.Path)
			}
		} else {
			hash, err := hex.DecodeString(e.Hash)
			if err != nil || len(hash) != 32 || strings.ToLower(e.Hash) != e.Hash {
				return fmt.Errorf("invalid file hash %q", e.Path)
			}
		}
		seen[e.Path] = e
	}
	if m.Digest != Digest(m.Entries) {
		return fmt.Errorf("manifest digest differs")
	}
	return nil
}
