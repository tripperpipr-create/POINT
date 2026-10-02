package domain

import (
	"encoding/json"
	"errors"
	"path"
	"strings"
)

// DependencyPlan is approved with the WorkOrder; it never creates a project.
// A nil plan preserves the digest and execution semantics of historical orders.
type DependencyPlan struct {
	Version  string              `json:"version"`
	Projects []DependencyProject `json:"projects"`
}
type DependencyProject struct {
	Cwd           string         `json:"cwd,omitempty"`
	Manager       string         `json:"manager"` // npm | go | composer
	Commands      []SetupCommand `json:"commands"`
	ManifestPaths []string       `json:"manifestPaths"`           // workspace-relative
	ExpectedPaths []string       `json:"expectedPaths,omitempty"` // workspace-relative
}

func NormalizeDependencyPlan(plan *DependencyPlan) *DependencyPlan {
	if plan == nil {
		return nil
	}
	raw, _ := json.Marshal(plan)
	var out DependencyPlan
	_ = json.Unmarshal(raw, &out)
	out.Version = strings.TrimSpace(out.Version)
	for i := range out.Projects {
		p := &out.Projects[i]
		p.Cwd = strings.ReplaceAll(strings.TrimSpace(p.Cwd), `\`, "/")
		if p.Cwd == "." {
			p.Cwd = ""
		}
		p.Manager = strings.TrimSpace(p.Manager)
		for j := range p.Commands {
			c := &p.Commands[j]
			c.Command = strings.TrimSpace(c.Command)
			c.Cwd = strings.ReplaceAll(strings.TrimSpace(c.Cwd), `\`, "/")
			if c.TimeoutSeconds <= 0 {
				c.TimeoutSeconds = 600
			}
		}
	}
	return &out
}

// Reject traversal on all hosts, including Windows paths read on Linux.
func DependencyPathValid(value string, rootAllowed bool) bool {
	value = strings.ReplaceAll(value, `\`, "/")
	if value == "" || value == "." {
		return rootAllowed
	}
	if strings.HasPrefix(value, "/") || strings.ContainsAny(value, ":\x00") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." || part == "" || strings.TrimSpace(part) != part {
			return false
		}
	}
	return true
}
func ValidateDependencyPlan(plan *DependencyPlan) error {
	if plan == nil {
		return nil
	}
	if plan.Version != "1" || len(plan.Projects) == 0 || len(plan.Projects) > 64 {
		return errors.New("dependency plan requires version 1 and 1..64 projects")
	}
	seen := map[string]bool{}
	for _, p := range plan.Projects {
		if !DependencyPathValid(p.Cwd, true) || seen[path.Clean(p.Cwd)+":"+p.Manager] {
			return errors.New("dependency plan contains an invalid or duplicate project")
		}
		seen[path.Clean(p.Cwd)+":"+p.Manager] = true
		if p.Manager != "npm" && p.Manager != "go" && p.Manager != "composer" {
			return errors.New("unsupported dependency manager")
		}
		if len(p.Commands) == 0 || len(p.Commands) > 8 || len(p.ManifestPaths) == 0 {
			return errors.New("dependency project requires commands and manifests")
		}
		for _, c := range p.Commands {
			if c.Command == "" || len(c.Command) > 4096 || c.TimeoutSeconds <= 0 || c.TimeoutSeconds > 600 || !DependencyPathValid(c.Cwd, true) {
				return errors.New("dependency preparation contains an invalid command")
			}
		}
		for _, paths := range [][]string{p.ManifestPaths, p.ExpectedPaths} {
			for _, path := range paths {
				if !DependencyPathValid(path, false) {
					return errors.New("dependency paths must stay inside the workspace")
				}
			}
		}
	}
	return nil
}
