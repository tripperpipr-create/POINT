package gitflow

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

type HistoryQuery struct {
	Search string `json:"search,omitempty"`
	Author string `json:"author,omitempty"`
	Path   string `json:"path,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Skip   int    `json:"skip,omitempty"`
}
type HistoryCommit struct {
	Hash    string   `json:"hash"`
	Parents []string `json:"parents"`
	Author  string   `json:"author"`
	Date    string   `json:"date"`
	Message string   `json:"message"`
	Refs    string   `json:"refs"`
}

func History(ctx context.Context, runner Runner, root string, q HistoryQuery) ([]HistoryCommit, error) {
	result := []HistoryCommit{}
	if q.Skip < 0 || q.Skip > 1000000 {
		return result, errors.New("неверная страница истории")
	}
	args := []string{"log", "--topo-order", "--date=iso-strict", "--max-count=100", "--skip=" + strconv.Itoa(q.Skip), "--format=%H%x00%P%x00%an%x00%aI%x00%s%x00%D%x00"}
	if q.Search != "" {
		args = append(args, "--fixed-strings", "--grep="+q.Search)
	}
	if q.Author != "" {
		args = append(args, "--author="+q.Author)
	}
	if q.Ref != "" {
		if strings.HasPrefix(q.Ref, "-") {
			return result, errors.New("неверная ревизия")
		}
		args = append(args, q.Ref)
	} else {
		args = append(args, "--all")
	}
	args = append(args, "--")
	if q.Path != "" {
		if _, e := SafePath(root, q.Path); e != nil {
			return result, e
		}
		args = append(args, q.Path)
	}
	out, e := runner.Run(ctx, root, args...)
	if e != nil {
		if head, _ := run(ctx, runner, root, "rev-parse", "--verify", "HEAD"); head == "" {
			return result, nil
		}
		return result, e
	}
	parts := strings.Split(string(out), "\x00")
	for len(parts) >= 6 {
		hash := strings.TrimSpace(parts[0])
		if hash != "" {
			result = append(result, HistoryCommit{Hash: hash, Parents: strings.Fields(parts[1]), Author: parts[2], Date: parts[3], Message: parts[4], Refs: parts[5]})
		}
		parts = parts[6:]
	}
	return result, nil
}
func Compare(ctx context.Context, runner Runner, root, base, head, path string) (string, error) {
	for _, ref := range []string{base, head} {
		if ref == "" || strings.HasPrefix(ref, "-") {
			return "", errors.New("выберите две ревизии")
		}
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", base, head, "--"}
	if path != "" {
		if _, e := SafePath(root, path); e != nil {
			return "", e
		}
		args = append(args, path)
	}
	out, e := runner.Run(ctx, root, args...)
	return string(out), e
}
func Blame(ctx context.Context, runner Runner, root, path string) (string, error) {
	if _, e := SafePath(root, path); e != nil {
		return "", e
	}
	out, e := runner.Run(ctx, root, "blame", "--line-porcelain", "--", path)
	return string(out), e
}
