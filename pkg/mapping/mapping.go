// Package mapping normalises multi-source identifiers and terms into concept
// mappings. Every resolved answer carries the list of evidence entries it was
// derived from, so conclusions are traceable to their sources.
//
// Conflicts are never auto-resolved by "picking one":
//
//   - same_source_conflicting_targets: within a single source, one identifier
//     maps to two or more different concept targets;
//   - mutually_exclusive_target: the identifier maps to different concepts
//     across sources (without any single source contradicting itself).
//
// Group iteration and evidence ordering are fully sorted, so repeated runs on
// identical input produce byte-identical results.
package mapping

import (
	"sort"
	"strings"

	"github.com/feng111627/biomedical-knowledge-graph-go/pkg/model"
)

// Resolution is the verdict for one queried identifier or term.
type Resolution struct {
	Query      string             `json:"query"`
	ConceptID  string             `json:"concept_id,omitempty"` // empty when conflicted
	Provenance []model.Provenance `json:"provenance,omitempty"`
	Conflict   *model.Conflict    `json:"conflict,omitempty"`
}

// IsConflicted reports whether the resolution was blocked by a conflict.
func (r Resolution) IsConflicted() bool { return r.Conflict != nil }

// termEvidence is one source-backed observation that a term denotes a concept.
type termEvidence struct {
	conceptID string
	source    string
	detail    string
}

// Resolver is the deterministic, read-only result of normalisation.
type Resolver struct {
	// idGroups: external identifier -> all entries mentioning it (sorted).
	idGroups map[string][]model.Mapping
	idAnswer map[string]Resolution
	// termGroups: normalized term -> observations (sorted by source, concept).
	termGroups map[string][]termEvidence
	termAnswer map[string]Resolution
	// Conflicts in deterministic order (by external id / term).
	Conflicts []model.Conflict
}

// New builds a Resolver from explicit mapping entries plus the concept labels
// and synonyms that should participate in term normalization.
func New(entries []model.Mapping, concepts []model.Concept, synonyms []model.Synonym) *Resolver {
	r := &Resolver{
		idGroups:   map[string][]model.Mapping{},
		idAnswer:   map[string]Resolution{},
		termGroups: map[string][]termEvidence{},
		termAnswer: map[string]Resolution{},
	}
	// Deterministic copies.
	ids := append([]model.Mapping(nil), entries...)
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].ExternalID != ids[j].ExternalID {
			return ids[i].ExternalID < ids[j].ExternalID
		}
		if ids[i].Source != ids[j].Source {
			return ids[i].Source < ids[j].Source
		}
		return ids[i].ConceptID < ids[j].ConceptID
	})
	for _, e := range ids {
		r.idGroups[e.ExternalID] = append(r.idGroups[e.ExternalID], e)
	}
	// Labels and synonym terms are normalized the same way as queries.
	for _, c := range concepts {
		if strings.TrimSpace(c.Label) != "" {
			r.termGroups[NormalizeTerm(c.Label)] = append(r.termGroups[NormalizeTerm(c.Label)], termEvidence{
				conceptID: c.ID, source: c.Source, detail: "concept label",
			})
		}
	}
	for _, s := range synonyms {
		r.termGroups[NormalizeTerm(s.Term)] = append(r.termGroups[NormalizeTerm(s.Term)], termEvidence{
			conceptID: s.ConceptID, source: s.Source, detail: "synonym",
		})
	}
	for k := range r.termGroups {
		ev := r.termGroups[k]
		sort.Slice(ev, func(i, j int) bool {
			if ev[i].source != ev[j].source {
				return ev[i].source < ev[j].source
			}
			return ev[i].conceptID < ev[j].conceptID
		})
		r.termGroups[k] = ev
	}

	// Evaluate identifier groups.
	keys := make([]string, 0, len(r.idGroups))
	for k := range r.idGroups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		group := r.idGroups[k]
		res := r.evaluateID(k, group)
		r.idAnswer[k] = res
		if res.Conflict != nil {
			r.Conflicts = append(r.Conflicts, *res.Conflict)
		}
	}
	// Evaluate term groups (deterministic key order).
	tkeys := make([]string, 0, len(r.termGroups))
	for k := range r.termGroups {
		tkeys = append(tkeys, k)
	}
	sort.Strings(tkeys)
	for _, k := range tkeys {
		res := r.evaluateTerm(k, r.termGroups[k])
		r.termAnswer[k] = res
		if res.Conflict != nil {
			// avoid duplicate listing if an identical conflict was already seen
			// via an external-id group; identifiers and terms are separate
			// namespaces, so they are listed on their own.
			r.Conflicts = append(r.Conflicts, *res.Conflict)
		}
	}
	return r
}

// evaluateID applies the two conflict rules to one external-id group.
func (r *Resolver) evaluateID(external string, group []model.Mapping) Resolution {
	res := Resolution{Query: external}
	targets := map[string]bool{}
	bySource := map[string]map[string]bool{}
	for _, e := range group {
		targets[e.ConceptID] = true
		if bySource[e.Source] == nil {
			bySource[e.Source] = map[string]bool{}
		}
		bySource[e.Source][e.ConceptID] = true
		res.Provenance = append(res.Provenance, model.Provenance{
			Source: e.Source,
			Detail: "mapping " + external + " -> " + e.ConceptID,
		})
	}
	if len(targets) <= 1 {
		for c := range targets {
			res.ConceptID = c
		}
		return res
	}
	// Conflicting targets: check the stronger "one source, many targets" rule.
	var selfContradictingSources []string
	for src, ts := range bySource {
		if len(ts) > 1 {
			selfContradictingSources = append(selfContradictingSources, src)
		}
	}
	sort.Strings(selfContradictingSources)
	if len(selfContradictingSources) > 0 {
		res.ConceptID = ""
		res.Conflict = &model.Conflict{
			Kind:     model.ConflictSameSourceTargets,
			External: external,
			Entries:  append([]model.Mapping(nil), group...),
			Reason: "source(s) " + strings.Join(selfContradictingSources, ", ") +
				" map identifier " + external + " to multiple different concepts within the same source",
		}
		return res
	}
	res.Conflict = &model.Conflict{
		Kind:     model.ConflictMutuallyExclusiveTarget,
		External: external,
		Entries:  append([]model.Mapping(nil), group...),
		Reason:   "identifier " + external + " is mapped to mutually exclusive concepts across different sources",
	}
	res.ConceptID = ""
	return res
}

func (r *Resolver) evaluateTerm(term string, ev []termEvidence) Resolution {
	res := Resolution{Query: term}
	targets := map[string]bool{}
	bySource := map[string]map[string]bool{}
	for _, e := range ev {
		targets[e.conceptID] = true
		if bySource[e.source] == nil {
			bySource[e.source] = map[string]bool{}
		}
		bySource[e.source][e.conceptID] = true
		res.Provenance = append(res.Provenance, model.Provenance{
			Source: e.source,
			Detail: e.detail + ": " + term + " -> " + e.conceptID,
		})
	}
	if len(targets) <= 1 {
		for c := range targets {
			res.ConceptID = c
		}
		return res
	}
	var selfSrc []string
	for src, ts := range bySource {
		if len(ts) > 1 {
			selfSrc = append(selfSrc, src)
		}
	}
	sort.Strings(selfSrc)
	entries := make([]model.Mapping, 0, len(ev))
	for _, e := range ev {
		entries = append(entries, model.Mapping{
			Source: e.source, ExternalID: "term:" + term,
			ConceptID: e.conceptID, Term: term,
		})
	}
	if len(selfSrc) > 0 {
		res.Conflict = &model.Conflict{
			Kind:     model.ConflictSameSourceTargets,
			External: "term:" + term,
			Entries:  entries,
			Reason:   "term \"" + term + "\" denotes multiple concepts within the same source(s) " + strings.Join(selfSrc, ", "),
		}
		return res
	}
	res.Conflict = &model.Conflict{
		Kind:     model.ConflictMutuallyExclusiveTarget,
		External: "term:" + term,
		Entries:  entries,
		Reason:   "term \"" + term + "\" denotes mutually exclusive concepts across different sources",
	}
	return res
}

// ResolveID returns the verdict for an external identifier.
func (r *Resolver) ResolveID(external string) Resolution {
	if res, ok := r.idAnswer[external]; ok {
		return res
	}
	return Resolution{Query: external}
}

// ResolveTerm normalizes the term and returns its verdict.
func (r *Resolver) ResolveTerm(term string) Resolution {
	if res, ok := r.termAnswer[NormalizeTerm(term)]; ok {
		return res
	}
	return Resolution{Query: term}
}

// UnambiguousCount returns the number of identifiers resolved without conflict.
func (r *Resolver) UnambiguousCount() int {
	n := 0
	for _, res := range r.idAnswer {
		if !res.IsConflicted() && res.ConceptID != "" {
			n++
		}
	}
	return n
}

// NormalizeTerm lower-cases and collapses internal whitespace, so
// "Aspirin  Tablet" and "aspirin tablet" normalize identically.
func NormalizeTerm(t string) string {
	fields := strings.Fields(strings.TrimSpace(t))
	return strings.ToLower(strings.Join(fields, " "))
}
