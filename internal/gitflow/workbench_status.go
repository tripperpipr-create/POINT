package gitflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type Change struct {
	Path         string `json:"path"`
	OriginalPath string `json:"originalPath,omitempty"`
	Area         string `json:"area"`
	Code         string `json:"code"`
	Status       int    `json:"status"`
	Staged       bool   `json:"staged"`
}
type Remote struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	PushURL string `json:"pushUrl"`
}
type Snapshot struct {
	Root           string   `json:"root"`
	Branch         string   `json:"branch"`
	Head           string   `json:"head"`
	Revision       string   `json:"revision"`
	Detached       bool     `json:"detached"`
	Remote         string   `json:"remote"`
	Ahead          int      `json:"ahead"`
	Behind         int      `json:"behind"`
	PullConfigured bool     `json:"pullConfigured"`
	Operation      string   `json:"operation"`
	Changes        []Change `json:"changes"`
	Remotes        []Remote `json:"remotes"`
	Tags           []string `json:"tags"`
	LocalBranches  []string `json:"localBranches"`
	RemoteBranches []string `json:"remoteBranches"`
	Branches       []string `json:"branches"`
}

func ReadSnapshot(ctx context.Context, runner Runner, dir string) (Snapshot, error) {
	s := Snapshot{Changes: []Change{}, Remotes: []Remote{}, Branches: []string{}}
	root, err := run(ctx, runner, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return s, err
	}
	s.Root = root
	for _, key := range []string{"pull.rebase", "pull.ff"} {
		v, _ := run(ctx, runner, root, "config", "--get", key)
		s.PullConfigured = s.PullConfigured || v != ""
	}
	raw, err := runner.Run(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return s, err
	}
	records := strings.Split(string(raw), "\x00")
	for i := 0; i < len(records); i++ {
		row := records[i]
		if len(row) < 4 {
			continue
		}
		code, name, old := row[:2], row[3:], ""
		if strings.ContainsAny(code, "RC") && i+1 < len(records) {
			i++
			old = records[i]
		}
		if code == "??" {
			s.Changes = append(s.Changes, Change{Path: name, Area: "untracked", Code: "?", Status: 7})
			continue
		}
		if code == "UU" || code == "AA" || code == "DD" || strings.Contains(code, "U") {
			s.Changes = append(s.Changes, Change{Path: name, OriginalPath: old, Area: "conflict", Code: code, Status: 18})
			continue
		}
		for j, area := range []string{"staged", "working"} {
			if code[j] == ' ' {
				continue
			}
			status := 5
			if j == 0 {
				status = 0
			}
			switch code[j] {
			case 'A':
				if j == 0 {
					status = 1
				} else {
					status = 9
				}
			case 'D':
				if j == 0 {
					status = 2
				} else {
					status = 6
				}
			case 'R':
				if j == 0 {
					status = 3
				} else {
					status = 10
				}
			case 'C':
				status = 4
			}
			s.Changes = append(s.Changes, Change{Path: name, OriginalPath: old, Area: area, Code: string(code[j]), Status: status, Staged: j == 0})
		}
	}
	s.Head, _ = run(ctx, runner, root, "rev-parse", "--verify", "HEAD")
	s.Branch, _ = run(ctx, runner, root, "symbolic-ref", "--quiet", "--short", "HEAD")
	s.Detached = s.Branch == ""
	branchPull, _ := run(ctx, runner, root, "config", "--get", "branch."+s.Branch+".rebase")
	s.PullConfigured = s.PullConfigured || branchPull != ""
	s.Remote, _ = run(ctx, runner, root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if s.Remote != "" {
		counts, _ := run(ctx, runner, root, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
		fields := strings.Fields(counts)
		if len(fields) == 2 {
			s.Ahead = number(fields[0])
			s.Behind = number(fields[1])
		}
	}
	names, _ := run(ctx, runner, root, "remote")
	for _, name := range strings.Fields(names) {
		u, _ := run(ctx, runner, root, "remote", "get-url", name)
		p, _ := run(ctx, runner, root, "remote", "get-url", "--push", name)
		s.Remotes = append(s.Remotes, Remote{Name: name, URL: StripCredentials(u), PushURL: StripCredentials(p)})
	}
	refs, _ := run(ctx, runner, root, "for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/remotes", "refs/tags")
	if refs != "" {
		s.Branches = strings.Split(refs, "\n")
	}
	for ref, target := range map[string]*[]string{"refs/heads": &s.LocalBranches, "refs/remotes": &s.RemoteBranches, "refs/tags": &s.Tags} {
		out, _ := run(ctx, runner, root, "for-each-ref", "--format=%(refname:short)", ref)
		*target = []string{}
		if out != "" {
			*target = strings.Split(out, "\n")
		}
	}
	for _, op := range []struct{ file, name string }{{"rebase-merge", "rebase"}, {"rebase-apply", "rebase"}, {"MERGE_HEAD", "merge"}, {"CHERRY_PICK_HEAD", "cherry-pick"}, {"REVERT_HEAD", "revert"}} {
		p, _ := run(ctx, runner, root, "rev-parse", "--git-path", op.file)
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		if _, e := os.Stat(p); e == nil {
			s.Operation = op.name
			break
		}
	}
	hash := sha256.New()
	metadata, _ := json.Marshal([]any{s.Root, s.Branch, s.Remote, s.Operation, s.PullConfigured, s.Remotes})
	hash.Write(metadata)
	refState, _ := runner.Run(ctx, root, "for-each-ref", "--format=%(refname) %(objectname)")
	hash.Write(refState)
	hash.Write(raw)
	hash.Write([]byte(s.Head))
	index, _ := runner.Run(ctx, root, "ls-files", "--stage", "-z")
	hash.Write(index)
	// Content digests detect an editor save that leaves porcelain unchanged.
	seen := map[string]bool{}
	for _, c := range s.Changes {
		if seen[c.Path] {
			continue
		}
		seen[c.Path] = true
		p, e := SafePath(root, c.Path)
		if e != nil {
			return s, e
		}
		info, e := os.Lstat(p)
		if e != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			v, _ := os.Readlink(p)
			hash.Write([]byte(v))
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		file, e := os.Open(p)
		if e != nil {
			return s, e
		}
		_, e = copyDigest(hash, file)
		file.Close()
		if e != nil {
			return s, e
		}
	}
	s.Revision = hex.EncodeToString(hash.Sum(nil))
	return s, nil
}
func SafePath(root, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) || strings.ContainsRune(name, 0) {
		return "", errors.New("неверный путь Git")
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("путь вне репозитория")
	}
	if strings.EqualFold(strings.Split(filepath.ToSlash(clean), "/")[0], ".git") {
		return "", errors.New("служебный путь Git")
	}
	target := filepath.Join(root, clean)
	// Resolve parents, but allow the final symlink itself to be staged.
	parent := filepath.Dir(target)
	for {
		real, e := filepath.EvalSymlinks(parent)
		if e == nil {
			rel, e := filepath.Rel(root, real)
			if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return "", errors.New("путь выходит из репозитория")
			}
			break
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", e
		}
		parent = next
	}
	return target, nil
}
