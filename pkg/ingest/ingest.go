// Package ingest loads local sample datasets (JSON files) and validates them.
//
// A dataset is accepted only when ALL of the following hold:
//
//   - no duplicate concept id with a conflicting definition;
//   - every referenced concept exists (no dangling references);
//   - the subclass hierarchy is acyclic (no cycle, including self loops).
//
// Any failure aborts the whole import with a structured *model.RejectError;
// records are never silently dropped and import never hangs on cyclic input
// (cycle detection is a bounded DFS with explicit coloring).
package ingest

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/model"
)

// LoadFile reads and parses a local JSON dataset file.
func LoadFile(path string) (*model.Dataset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset %q: %w", path, err)
	}
	var d model.Dataset
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("parse dataset %q: %w", path, err)
	}
	if err := Validate(&d); err != nil {
		return nil, err
	}
	return &d, nil
}

// Validate checks a dataset and returns a structured *model.RejectError on the
// first violated rule. Checks run in a fixed order so the verdict for the same
// input is deterministic.
func Validate(d *model.Dataset) error {
	if err := validateConcepts(d); err != nil {
		return err
	}
	if err := validateReferences(d); err != nil {
		return err
	}
	if err := validateHierarchy(d); err != nil {
		return err
	}
	return nil
}

func validateConcepts(d *model.Dataset) error {
	seen := map[string]model.Concept{}
	for _, c := range d.Concepts {
		if strings.TrimSpace(c.ID) == "" {
			return &model.RejectError{
				Kind:    model.RejectDuplicateConflict,
				Message: "concept with empty id is not allowed",
			}
		}
		if prev, ok := seen[c.ID]; ok {
			// Identical re-assertion is idempotent and allowed; a conflicting
			// definition for the same id is explicitly rejected.
			if prev.Label != c.Label {
				return &model.RejectError{
					Kind: model.RejectDuplicateConflict,
					Message: fmt.Sprintf(
						"concept %q defined more than once with conflicting labels: %q (source %q) vs %q (source %q)",
						c.ID, prev.Label, prev.Source, c.Label, c.Source),
				}
			}
		} else {
			seen[c.ID] = c
		}
	}
	relIDs := map[string]model.EntityRelation{}
	for _, r := range d.Relations {
		if strings.TrimSpace(r.ID) == "" {
			return &model.RejectError{
				Kind:    model.RejectDuplicateConflict,
				Message: fmt.Sprintf("relation for subject %q has empty id", r.SubjectID),
			}
		}
		if prev, ok := relIDs[r.ID]; ok {
			if prev.SubjectID != r.SubjectID || prev.Predicate != r.Predicate || prev.ObjectID != r.ObjectID {
				return &model.RejectError{
					Kind: model.RejectDuplicateConflict,
					Message: fmt.Sprintf(
						"relation id %q reused with conflicting definitions: (%s %s %s) vs (%s %s %s)",
						r.ID, prev.SubjectID, prev.Predicate, prev.ObjectID,
						r.SubjectID, r.Predicate, r.ObjectID),
				}
			}
		} else {
			relIDs[r.ID] = r
		}
	}
	return nil
}

// validateReferences verifies that every concept id referenced anywhere is
// actually defined. Missing concepts are reported in sorted, deterministic
// order instead of being silently ignored.
func validateReferences(d *model.Dataset) error {
	exists := map[string]bool{}
	for _, c := range d.Concepts {
		exists[c.ID] = true
	}
	missing := map[string]string{} // missing id -> first human explanation
	add := func(id, detail string) {
		if !exists[id] && strings.TrimSpace(id) != "" {
			if _, ok := missing[id]; !ok {
				missing[id] = detail
			}
		}
	}
	for _, s := range d.Synonyms {
		add(s.ConceptID, fmt.Sprintf("synonym %q (source %q)", s.Term, s.Source))
	}
	for _, p := range d.Parents {
		add(p.ConceptID, fmt.Sprintf("parent link child %q -> %q (source %q)", p.ConceptID, p.ParentID, p.Source))
		add(p.ParentID, fmt.Sprintf("parent link %q -> parent %q (source %q)", p.ConceptID, p.ParentID, p.Source))
	}
	for _, r := range d.Relations {
		add(r.SubjectID, fmt.Sprintf("relation %q subject", r.ID))
		add(r.ObjectID, fmt.Sprintf("relation %q object", r.ID))
	}
	for _, m := range d.Mappings {
		add(m.ConceptID, fmt.Sprintf("mapping %q from source %q", m.ExternalID, m.Source))
	}
	if len(missing) > 0 {
		ids := make([]string, 0, len(missing))
		for id := range missing {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		details := make([]string, 0, len(ids))
		for _, id := range ids {
			details = append(details, fmt.Sprintf("%q referenced by %s", id, missing[id]))
		}
		return &model.RejectError{
			Kind:    model.RejectDanglingReference,
			Message: "dangling reference(s) to undefined concepts: " + strings.Join(details, "; "),
		}
	}
	return nil
}

// validateHierarchy runs an iterative, bounded DFS over the child->parent
// graph. White=0, gray=1, black=2. A gray node reached again means a cycle;
// the chain of gray nodes on the current stack is returned as evidence.
func validateHierarchy(d *model.Dataset) error {
	parents := map[string][]string{}
	for _, p := range d.Parents {
		parents[p.ConceptID] = append(parents[p.ConceptID], p.ParentID)
	}
	for id := range parents {
		sort.Strings(parents[id])
	}
	ids := make([]string, 0, len(d.Concepts))
	for _, c := range d.Concepts {
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)

	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	stack := []string{}

	var dfs func(id string) []string
	dfs = func(id string) []string {
		color[id] = gray
		stack = append(stack, id)
		for _, pid := range parents[id] {
			if pid == id {
				return []string{id, id} // explicit self loop
			}
			switch color[pid] {
			case gray:
				// Extract the cycle: position of pid on the stack ... end, pid.
				start := 0
				for i, s := range stack {
					if s == pid {
						start = i
						break
					}
				}
				cycle := append([]string(nil), stack[start:]...)
				cycle = append(cycle, pid)
				return cycle
			case white:
				if cyc := dfs(pid); cyc != nil {
					return cyc
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return nil
	}

	for _, id := range ids {
		if color[id] == white {
			if cyc := dfs(id); cyc != nil {
				return &model.RejectError{
					Kind: model.RejectHierarchyCycle,
					Message: fmt.Sprintf(
						"cycle detected in subclass hierarchy: %s (each -> means is-a parent)",
						strings.Join(cyc, " -> ")),
					Paths: [][]string{cyc},
				}
			}
		}
	}
	return nil
}
