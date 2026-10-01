package kg

import (
	"context"
	"sort"
	"sync"
)

// Store 是支持版本化、回滚与并发读写的图谱存储。
// 提交是原子的：要么完整生效产生新版本，要么完全不生效；
// 查询永远基于某个已提交的不可变快照，不会读到半更新状态。
type Store struct {
	mu      sync.RWMutex
	current *snapshotData
	// history 保存全部仍可回滚的已提交快照，按版本升序。
	history []*snapshotData
	// nextVersion 单调递增，回滚后新版本号也不会复用。
	nextVersion int64
}

// NewStore 创建一个空存储（初始版本 0）。
func NewStore() *Store {
	empty := newSnapshotData(0)
	return &Store{current: empty, history: []*snapshotData{empty}, nextVersion: 1}
}

// ImportResult 是一次导入的结果。
type ImportResult struct {
	Version   int64
	Conflicts []MappingConflict
}

// Import 原子地全量导入一个数据集，产生新版本。
// 若数据存在环/悬空引用/重复冲突定义，则整体拒绝且当前版本不变。
// 映射冲突不导致拒绝，而是以冲突标记形式返回并体现在快照中。
func (s *Store) Import(ctx context.Context, ds Dataset) (*ImportResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	version := s.nextVersion
	candidate, buildErrs, conflicts := buildFromDataset(newSnapshotData(version), version, ds)
	buildErrs = append(buildErrs, validateData(candidate)...)
	if len(buildErrs) > 0 {
		return nil, &RejectError{Errors: sortErrors(buildErrs)}
	}
	s.commit(candidate)
	return &ImportResult{Version: version, Conflicts: conflicts}, nil
}

// Revision 表示对当前图谱的一次增量修订。
type Revision struct {
	// BaseVersion 是调用方读取时依据的版本号，用于乐观并发控制。
	BaseVersion int64
	// UpsertConcepts 新增或更新的概念定义。
	UpsertConcepts []Concept
	// DeleteConcepts 要删除的概念 ID（级联删除相关边、关系与映射）。
	DeleteConcepts []string
	// RenameConcepts 将概念改名（ID 不变，规范名变更）。
	RenameConcepts []Rename
	// UpsertHierarchy / DeleteHierarchy 层级边增量。
	UpsertHierarchy []HierarchyEdge
	DeleteHierarchy []HierarchyEdge
	// UpsertRelations / DeleteRelations 实体关系增量。
	UpsertRelations []EntityRelation
	DeleteRelations []EntityRelation
	// UpsertMappings / DeleteMappings 映射增量。
	UpsertMappings []MappingAssignment
	DeleteMappings []MappingAssignment
}

// Rename 表示概念改名（ID 保持不变，规范名变更）。
type Rename struct {
	ID      string
	NewName string
	Source  string
}

// RevisionResult 是一次修订的结果。
type RevisionResult struct {
	Version   int64
	Conflicts []MappingConflict
}

// ApplyRevision 在当前快照上原子地应用增量修订。
// 若 BaseVersion 与当前版本不一致（已被其他写入抢先），返回 stale_revision。
// 若修订破坏一致性（环/悬空/重复冲突），整体拒绝，当前版本保持不变。
func (s *Store) ApplyRevision(ctx context.Context, rev Revision) (*RevisionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if rev.BaseVersion != s.current.version {
		return nil, &RejectError{Errors: []*ValidationError{{
			Reason: RejectStaleRevision,
			Message: "revision based on version " + itoa64(rev.BaseVersion) +
				" but current version is " + itoa64(s.current.version),
		}}}
	}

	version := s.nextVersion
	candidate, buildErrs, conflicts := buildFromRevision(s.current, version, rev)
	buildErrs = append(buildErrs, validateData(candidate)...)
	if len(buildErrs) > 0 {
		return nil, &RejectError{Errors: sortErrors(buildErrs)}
	}
	s.commit(candidate)
	return &RevisionResult{Version: version, Conflicts: conflicts}, nil
}

func (s *Store) commit(d *snapshotData) {
	s.current = d
	s.history = append(s.history, d)
	s.nextVersion = d.version + 1
}

// Rollback 将存储整体回滚到指定版本：该版本之后的提交被整体丢弃。
// 之后新提交使用全新的、更大的版本号。
func (s *Store) Rollback(ctx context.Context, version int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	idx := -1
	for i, d := range s.history {
		if d.version == version {
			idx = i
			break
		}
	}
	if idx < 0 {
		return &ValidationError{
			Reason:  RejectStaleRevision,
			Message: "cannot rollback to unknown version " + itoa64(version),
		}
	}
	s.history = s.history[:idx+1]
	s.current = s.history[idx]
	s.nextVersion = s.maxVersionSeen() + 1
	return nil
}

func (s *Store) maxVersionSeen() int64 {
	max := int64(0)
	for _, d := range s.history {
		if d.version > max {
			max = d.version
		}
	}
	if s.nextVersion-1 > max {
		max = s.nextVersion - 1
	}
	return max
}

// Current 返回当前不可变快照（并发安全；返回后读取无需持锁）。
func (s *Store) Current() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return &Snapshot{data: s.current}
}

// Versions 返回仍可回滚到的全部历史版本号（升序）。
func (s *Store) Versions() []int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]int64, len(s.history))
	for i, d := range s.history {
		out[i] = d.version
	}
	return out
}

func sortErrors(errs []*ValidationError) []*ValidationError {
	sort.SliceStable(errs, func(i, j int) bool {
		if errs[i].Reason != errs[j].Reason {
			return errs[i].Reason < errs[j].Reason
		}
		return joinEntities(errs[i]) < joinEntities(errs[j])
	})
	return errs
}

func joinEntities(e *ValidationError) string {
	out := ""
	for i, x := range e.Entities {
		if i > 0 {
			out += "|"
		}
		out += x
	}
	return out
}

func itoa64(n int64) string {
	return int64ToString(n)
}

func int64ToString(n int64) string {
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
