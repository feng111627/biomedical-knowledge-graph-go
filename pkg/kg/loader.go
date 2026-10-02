package kg

import (
	"encoding/json"
	"fmt"
)

// 本地样例文件格式（JSON），不依赖任何外部服务或真实术语库：
//
//	{
//	  "concepts":  [{"id":"D001","name":"Diabetes","source":"snomed_demo","synonyms":["DM"]}],
//	  "hierarchy": [{"parent":"D010","child":"D001","source":"snomed_demo"}],
//	  "relations": [{"subject":"D020","relation":"treats","object":"D001","source":"snomed_demo"}],
//	  "mappings":  [{"target_id":"SNOMED:73211009","concept_id":"D001","source":"snomed_demo"}],
//	  "term_mappings": [{"term":"Diabetes","concept_id":"D001","source":"snomed_demo"}]
//	}

type conceptDTO struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Source   string   `json:"source"`
	Synonyms []string `json:"synonyms"`
}

type hierarchyDTO struct {
	Parent string `json:"parent"`
	Child  string `json:"child"`
	Source string `json:"source"`
}

type relationDTO struct {
	Subject  string `json:"subject"`
	Relation string `json:"relation"`
	Object   string `json:"object"`
	Source   string `json:"source"`
}

type mappingDTO struct {
	TargetID  string `json:"target_id"`
	ConceptID string `json:"concept_id"`
	Source    string `json:"source"`
}

type termMappingDTO struct {
	Term      string `json:"term"`
	ConceptID string `json:"concept_id"`
	Source    string `json:"source"`
}

type datasetDTO struct {
	Concepts     []conceptDTO     `json:"concepts"`
	Hierarchy    []hierarchyDTO   `json:"hierarchy"`
	Relations    []relationDTO    `json:"relations"`
	Mappings     []mappingDTO     `json:"mappings"`
	TermMappings []termMappingDTO `json:"term_mappings"`
}

// LoadDataset 从 JSON 字节加载数据集。
// 仅做结构性校验（字段非空）；跨记录的一致性校验由导入器负责。
func LoadDataset(raw []byte) (Dataset, error) {
	var dto datasetDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		return Dataset{}, fmt.Errorf("invalid dataset json: %w", err)
	}

	ds := Dataset{
		Concepts:  make([]Concept, 0, len(dto.Concepts)),
		Hierarchy: make([]HierarchyEdge, 0, len(dto.Hierarchy)),
		Relations: make([]EntityRelation, 0, len(dto.Relations)),
		Mappings:  make([]MappingAssignment, 0, len(dto.Mappings)),
	}

	for i, c := range dto.Concepts {
		if c.ID == "" {
			return Dataset{}, fmt.Errorf("concepts[%d]: id is required", i)
		}
		if c.Name == "" {
			return Dataset{}, fmt.Errorf("concepts[%d] (%s): name is required", i, c.ID)
		}
		if c.Source == "" {
			return Dataset{}, fmt.Errorf("concepts[%d] (%s): source is required", i, c.ID)
		}
		ds.Concepts = append(ds.Concepts, Concept{
			ID: c.ID, Name: c.Name, Source: c.Source,
			Synonyms: dedupSorted(c.Synonyms),
		})
	}
	for i, h := range dto.Hierarchy {
		if h.Parent == "" || h.Child == "" {
			return Dataset{}, fmt.Errorf("hierarchy[%d]: parent and child are required", i)
		}
		if h.Source == "" {
			return Dataset{}, fmt.Errorf("hierarchy[%d] (%s->%s): source is required", i, h.Parent, h.Child)
		}
		ds.Hierarchy = append(ds.Hierarchy, HierarchyEdge{Parent: h.Parent, Child: h.Child, Source: h.Source})
	}
	for i, r := range dto.Relations {
		if r.Subject == "" || r.Relation == "" || r.Object == "" {
			return Dataset{}, fmt.Errorf("relations[%d]: subject, relation and object are required", i)
		}
		if r.Source == "" {
			return Dataset{}, fmt.Errorf("relations[%d]: source is required", i)
		}
		ds.Relations = append(ds.Relations, EntityRelation{
			Subject: r.Subject, Relation: r.Relation, Object: r.Object, Source: r.Source,
		})
	}
	for i, m := range dto.Mappings {
		if m.TargetID == "" || m.ConceptID == "" {
			return Dataset{}, fmt.Errorf("mappings[%d]: target_id and concept_id are required", i)
		}
		if m.Source == "" {
			return Dataset{}, fmt.Errorf("mappings[%d]: source is required", i)
		}
		ds.Mappings = append(ds.Mappings, MappingAssignment{
			TargetID: m.TargetID, ConceptID: m.ConceptID, Source: m.Source,
		})
	}
	for i, tm := range dto.TermMappings {
		if tm.Term == "" || tm.ConceptID == "" {
			return Dataset{}, fmt.Errorf("term_mappings[%d]: term and concept_id are required", i)
		}
		if tm.Source == "" {
			return Dataset{}, fmt.Errorf("term_mappings[%d]: source is required", i)
		}
		if NormalizeTerm(tm.Term) == "" {
			return Dataset{}, fmt.Errorf("term_mappings[%d]: term normalizes to empty", i)
		}
		ds.TermMappings = append(ds.TermMappings, TermAssignment{
			Term: tm.Term, ConceptID: tm.ConceptID, Source: tm.Source,
		})
	}
	return ds, nil
}

// dedupSorted 去重并按字典序排序，保证同义词列表稳定。
func dedupSorted(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sortStrings(out)
	return out
}
