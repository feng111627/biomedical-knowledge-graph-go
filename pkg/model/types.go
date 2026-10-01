// Package model defines the core data types of the biomedical knowledge graph:
// concepts, synonyms, hierarchy links, entity relations, multi-source
// identifier mappings, provenance and the machine-readable error / conflict
// categories used across import, reasoning and revision.
package model

// Concept is a node in the biomedical knowledge graph (e.g. a disease class).
type Concept struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Source      string `json:"source"`
	Description string `json:"description,omitempty"`
}

// Synonym is an alternative term for a concept, owned by a specific source.
type Synonym struct {
	ConceptID string `json:"concept_id"`
	Term      string `json:"term"`
	Source    string `json:"source"`
}

// ParentLink states that ConceptID is a subclass (child) of ParentID.
type ParentLink struct {
	ConceptID string `json:"concept_id"`
	ParentID  string `json:"parent_id"`
	Source    string `json:"source"`
}

// EntityRelation is a typed relation between two concrete concepts,
// e.g. concept A "treats" concept B.
type EntityRelation struct {
	ID        string `json:"id"`
	SubjectID string `json:"subject_id"`
	Predicate string `json:"predicate"`
	ObjectID  string `json:"object_id"`
	Source    string `json:"source"`
}

// Mapping associates an external identifier / term with one of our concepts.
// A single mapping entry never maps to more than one concept: such a case is
// represented by two Mapping entries sharing the same ExternalID, which the
// mapping table detects as a conflict.
type Mapping struct {
	Source     string `json:"source"`
	ExternalID string `json:"external_id"`
	ConceptID  string `json:"concept_id"`
	Term       string `json:"term,omitempty"`
}

// Dataset is a local, self-contained sample of a whole import batch.
type Dataset struct {
	Name      string           `json:"name"`
	Concepts  []Concept        `json:"concepts"`
	Synonyms  []Synonym        `json:"synonyms"`
	Parents   []ParentLink     `json:"parents"`
	Relations []EntityRelation `json:"relations"`
	Mappings  []Mapping        `json:"mappings"`
}

// Provenance identifies a single piece of evidence backing a conclusion:
// which source asserted it and which input record it came from.
type Provenance struct {
	Source   string `json:"source"`
	RecordID string `json:"record_id,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// ConflictKind enumerates the kinds of conflicts the system can detect.
type ConflictKind string

const (
	// ConflictNone means no conflict.
	ConflictNone ConflictKind = "none"
	// ConflictMutuallyExclusiveTarget: the same external identifier is mapped
	// by (possibly different) sources to mutually exclusive (different) concepts.
	ConflictMutuallyExclusiveTarget ConflictKind = "mutually_exclusive_target"
	// ConflictSameSourceTargets: within ONE source, the same identifier is
	// mapped to two different targets at once.
	ConflictSameSourceTargets ConflictKind = "same_source_conflicting_targets"
)

// Conflict describes one detected mapping conflict with all participating
// evidence, so callers never have to guess which entry was picked.
type Conflict struct {
	Kind     ConflictKind `json:"kind"`
	External string       `json:"external_id"`
	Entries  []Mapping    `json:"entries"`
	Reason   string       `json:"reason"`
}

// RejectKind enumerates why an import batch may be refused.
type RejectKind string

const (
	RejectNone              RejectKind = "none"
	RejectHierarchyCycle    RejectKind = "hierarchy_cycle"
	RejectDanglingReference RejectKind = "dangling_reference"
	RejectDuplicateConflict RejectKind = "duplicate_conflict"
)

// RejectError is a structured refusal: a batch is rejected wholesale with an
// explicit, explainable reason rather than silently dropping records.
type RejectError struct {
	Kind    RejectKind `json:"kind"`
	Message string     `json:"message"`
	// Paths carries cycle chains or other supporting details.
	Paths [][]string `json:"paths,omitempty"`
}

func (e *RejectError) Error() string { return string(e.Kind) + ": " + e.Message }
