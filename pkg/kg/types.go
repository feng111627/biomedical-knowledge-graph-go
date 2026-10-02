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

// TermAssignment 是一条原始文本术语到内部概念的术语归属。
// 与 MappingAssignment 的区别：映射以精确的外部标识为键，术语以
// NormalizeTerm 归一后的文本为键，可容忍大小写与空白差异。
type TermAssignment struct {
	// Term 是登记时的原始术语文本（原样保留，用于审计与展示）。
	Term string
	// Normalized 是归一化后的术语匹配键（由 NormalizeTerm 计算）。
	Normalized string
	// ConceptID 是术语指向的图谱内部概念。
	ConceptID string
	// Source 是给出该术语归属的来源。
	Source string
}

// Dataset 是一次导入的原始数据集合。
type Dataset struct {
	Concepts  []Concept
	Hierarchy []HierarchyEdge
	Relations []EntityRelation
	Mappings  []MappingAssignment
	// Terms 是原始文本术语 -> 内部概念的来源归属。
	Terms []TermAssignment
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

// TermView 是一条术语解析候选的视图。
type TermView struct {
	Assignment TermAssignment
	// Conflicted 表示该术语候选因冲突而不可作为确定结论使用。
	Conflicted bool
}

// TermResolution 是按原始文本解析术语的完整结果。
// 无命中时为 nil；命中时给出归一化前后的对应、全部候选与冲突（如有）。
type TermResolution struct {
	// Query 是调用方输入的原始文本。
	Query string
	// NormalizedQuery 是归一规则作用后的匹配键。
	NormalizedQuery string
	// MatchedTerm 是实际命中的登记术语（归一化键）。
	MatchedTerm string
	// Candidates 是该归一化术语下的全部来源候选，顺序确定。
	Candidates []TermView
	// Conflict 在无冲突时为 nil；冲突时给出类型、来源（同源冲突）与全部候选。
	Conflict *TermConflict
}

// TermConflict 描述一条术语映射冲突（只标记、不拒绝、不替调用方挑选）。
type TermConflict struct {
	Type    ConflictType
	Source  string
	Term    string
	Targets []TermAssignment
}
