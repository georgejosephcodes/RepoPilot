package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"repopilot/api/internal/evalmetrics"
	"repopilot/api/internal/repos"
)

// EvalSet is docs/phase2/eval.json. worker/scripts/eval_labels.py checks it against the real clones; the checks
// here are the ones the harness itself depends on.
type EvalSet struct {
	Version      int        `json:"version"`
	LabelsFrozen bool       `json:"labels_frozen"`
	Repositories []RepoPin  `json:"repositories"`
	Questions    []Question `json:"questions"`
}

type RepoPin struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
}

type Question struct {
	ID       string              `json:"id"`
	Repo     string              `json:"repo"`
	Kind     string              `json:"kind"`
	Split    string              `json:"split"`
	Question string              `json:"question"`
	Relevant []evalmetrics.Label `json:"relevant"`
}

func (q Question) Answerable() bool { return q.Kind != "unanswerable" }

var validKinds = map[string]bool{
	"location": true, "flow": true, "identifier": true, "literal": true, "conceptual": true, "trap": true, "unanswerable": true,
}

func loadSet(data []byte) (EvalSet, error) {
	var s EvalSet
	if err := json.Unmarshal(data, &s); err != nil {
		return EvalSet{}, fmt.Errorf("read eval set: %w", err)
	}
	if problems := validateSet(s); len(problems) > 0 {
		return EvalSet{}, fmt.Errorf("eval set has %d problem(s):\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	return s, nil
}

func validateSet(s EvalSet) []string {
	var problems []string
	pinned := map[string]bool{}
	for _, r := range s.Repositories {
		if len(r.Commit) != 40 {
			problems = append(problems, fmt.Sprintf("repository %s: commit must be 40 characters", r.Name))
		}
		pinned[r.Name] = true
	}
	if len(s.Questions) == 0 {
		problems = append(problems, "no questions")
	}
	seen := map[string]bool{}
	for _, q := range s.Questions {
		switch {
		case q.ID == "":
			problems = append(problems, "a question has no id")
		case seen[q.ID]:
			problems = append(problems, q.ID+": duplicate id")
		}
		seen[q.ID] = true
		if !pinned[q.Repo] {
			problems = append(problems, fmt.Sprintf("%s: repository %q is not pinned", q.ID, q.Repo))
		}
		if !validKinds[q.Kind] {
			problems = append(problems, fmt.Sprintf("%s: unknown kind %q", q.ID, q.Kind))
		}
		if q.Split != "dev" && q.Split != "test" {
			problems = append(problems, fmt.Sprintf("%s: split must be dev or test", q.ID))
		}
		if strings.TrimSpace(q.Question) == "" {
			problems = append(problems, q.ID+": empty question")
		}
		grade2 := false
		for _, l := range q.Relevant {
			if l.Grade == 2 {
				grade2 = true
			}
			if l.File == "" || l.Start < 1 || l.End < l.Start || (l.Grade != 1 && l.Grade != 2) {
				problems = append(problems, fmt.Sprintf("%s: bad span %s:%d-%d grade %d", q.ID, l.File, l.Start, l.End, l.Grade))
			}
		}
		if q.Answerable() && !grade2 {
			problems = append(problems, q.ID+": answerable question without a grade-2 span")
		}
		if !q.Answerable() && len(q.Relevant) > 0 {
			problems = append(problems, q.ID+": unanswerable question with spans")
		}
	}
	return problems
}

// selectSplit returns the questions of one split ("dev", "test") or all of them ("all"), in file order.
func selectSplit(qs []Question, split string) []Question {
	var out []Question
	for _, q := range qs {
		if split == "all" || q.Split == split {
			out = append(out, q)
		}
	}
	return out
}

// checkRepos matches every pinned repository to an indexed one: present, ready, and indexed at the pinned
// commit. Anything else means labels and chunks would describe different code, so the run must not start.
func checkRepos(pins []RepoPin, indexed []repos.Repository) (map[string]int64, []string) {
	byName := map[string]repos.Repository{}
	for _, r := range indexed {
		byName[strings.ToLower(r.Owner+"/"+r.Name)] = r
	}
	ids := map[string]int64{}
	var problems []string
	for _, p := range pins {
		r, ok := byName[strings.ToLower(p.Name)]
		if !ok {
			problems = append(problems, p.Name+": not indexed (use 'add' in the UI)")
			continue
		}
		if r.Status != "ready" {
			problems = append(problems, fmt.Sprintf("%s: status is %s, not ready", p.Name, r.Status))
		}
		if r.CommitSHA == nil || *r.CommitSHA != p.Commit {
			got := "none"
			if r.CommitSHA != nil {
				got = (*r.CommitSHA)[:min(7, len(*r.CommitSHA))]
			}
			problems = append(problems, fmt.Sprintf("%s: indexed at %s but the eval set pins %s", p.Name, got, p.Commit[:min(7, len(p.Commit))]))
		}
		ids[p.Name] = r.ID
	}
	return ids, problems
}
