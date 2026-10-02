package kg

import "strings"

// RejectReason 是导入/修订被拒绝的一致性原因类别。
type RejectReason string

const (
	// RejectCycle 表示概念上下位层级中存在环。
	RejectCycle RejectReason = "cycle"
	// RejectDangling 表示层级边或关系引用了不存在的概念。
	RejectDangling RejectReason = "dangling_reference"
	// RejectDuplicateConflict 表示同一概念被重复定义且定义互相冲突。
	RejectDuplicateConflict RejectReason = "duplicate_conflict"
	// RejectStaleRevision 表示修订基于的版本已过期。
	RejectStaleRevision RejectReason = "stale_revision"
)

// ConflictType 是多来源标识映射冲突的类别。
type ConflictType string

const (
	// ConflictSameSource 表示同一来源内给出了互相冲突的映射目标。
	ConflictSameSource ConflictType = "same_source_conflict"
	// ConflictCrossSource 表示不同来源把同一外部标识映射到互斥概念。
	ConflictCrossSource ConflictType = "cross_source_conflict"
	// ConflictTermSameSource 表示同一来源把同一（归一后）术语指向多个不同概念。
	ConflictTermSameSource ConflictType = "term_same_source_conflict"
	// ConflictTermCrossSource 表示不同来源把同一（归一后）术语指向互斥概念。
	ConflictTermCrossSource ConflictType = "term_cross_source_conflict"
)

// ValidationError 描述一条被拒绝的导入/修订及其原因。
type ValidationError struct {
	Reason RejectReason
	// Message 是可读的拒绝说明。
	Message string
	// Entities 是与问题相关的概念/边标识，保证顺序稳定。
	Entities []string
}

func (e *ValidationError) Error() string {
	if len(e.Entities) == 0 {
		return string(e.Reason) + ": " + e.Message
	}
	return string(e.Reason) + ": " + e.Message + " [" + strings.Join(e.Entities, " -> ") + "]"
}

// RejectError 聚合一次导入/修订中发现的全部拒绝原因。
type RejectError struct {
	Errors []*ValidationError
}

func (e *RejectError) Error() string {
	if e == nil || len(e.Errors) == 0 {
		return "rejected with no details"
	}
	parts := make([]string, len(e.Errors))
	for i, ve := range e.Errors {
		parts[i] = ve.Error()
	}
	return "graph revision rejected (" + itoa(len(e.Errors)) + " issue(s)): " + strings.Join(parts, "; ")
}

// MappingConflict 描述一条外部标识映射冲突（不导致整体拒绝，只做标记）。
type MappingConflict struct {
	Type     ConflictType
	Source   string
	TargetID string
	Targets  []MappingAssignment
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
