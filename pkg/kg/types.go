package kg

// Concept 是生物医学领域中的一个概念/本体类。
type Concept struct {
	// ID 是图谱内部稳定标识（如 D001）。
	ID string
	// Name 是规范名称。
	Name string
	// Source 是定义来源标识（如 snomed_demo）。
	Source string
	// Synonyms 是该概念在本来源中的同义词。
	Synonyms []string
}

// HierarchyEdge 是一条上下位关系：Child is-a Parent。
type HierarchyEdge struct {
	Parent string
	Child  string
	Source string
}

// EntityRelation 是两个概念之间的非层级关系（如 treats）。
type EntityRelation struct {
	Subject  string
	Relation string
	Object   string
	Source   string
}

// MappingAssignment 是一条外部标识到内部概念的映射归属。
type MappingAssignment struct {
	// TargetID 是外部术语库中的标识。
	TargetID string
	// ConceptID 是映射到的图谱内部概念。
	ConceptID string
	// Source 是给出该映射的来源。
	Source string
}

// Dataset 是一次导入的原始数据集合。
type Dataset struct {
	Concepts  []Concept
	Hierarchy []HierarchyEdge
	Relations []EntityRelation
	Mappings  []MappingAssignment
	// TermMappings 是原始文本术语到概念的显式归属。
	TermMappings []TermAssignment
}

// AncestorPath 记录一个概念的某个祖先及其传递推导路径。
type AncestorPath struct {
	ConceptID string
	Path      []string
}

// Evidence 是一条结论背后的来源依据。
type Evidence struct {
	Source string
	Kind   string
	Detail string
}

// ConceptView 是查询返回的概念完整视图（含同义词）。
type ConceptView struct {
	Concept   Concept
	Synonyms  []string
	Evidences []Evidence
}

// RelationView 是查询返回的关系视图。
type RelationView struct {
	Relation  EntityRelation
	Evidences []Evidence
}

// MappingView 是查询返回的映射视图。
type MappingView struct {
	Assignment MappingAssignment
	// Conflicted 表示该映射因冲突而不可作为确定结论使用。
	Conflicted bool
}
