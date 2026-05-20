package coord

import (
	"fmt"
	"time"

	"github.com/Yiling-J/theine-go"
	"github.com/mohitkumar/composer/pkg/model"
)

const (
	defaultCacheTTL     = 5 * time.Minute
	defaultCacheMaxSize = 1000 // max cached workflow versions (cost=1 each)
)

// workflowCache wraps a theine cache for WorkflowDAG instances.
// W-TinyLFU admission policy gives better hit rates than a plain LRU/TTL map
// under skewed access patterns (a few hot workflows hit much more than others).
type workflowCache struct {
	c   *theine.Cache[string, *model.WorkflowDAG]
	ttl time.Duration
}

func newWorkflowCache(ttl time.Duration) (*workflowCache, error) {
	if ttl <= 0 {
		ttl = defaultCacheTTL
	}
	c, err := theine.NewBuilder[string, *model.WorkflowDAG](defaultCacheMaxSize).Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create workflow cache: %w", err)
	}
	return &workflowCache{c: c, ttl: ttl}, nil
}

func wfCacheKey(workflowID string, version int) string {
	return fmt.Sprintf("%s@v%d", workflowID, version)
}

func (w *workflowCache) get(workflowID string, version int) (*model.WorkflowDAG, bool) {
	return w.c.Get(wfCacheKey(workflowID, version))
}

func (w *workflowCache) set(workflowID string, version int, dag *model.WorkflowDAG) {
	w.c.SetWithTTL(wfCacheKey(workflowID, version), dag, 1, w.ttl)
}

func (w *workflowCache) evict(workflowID string, version int) {
	w.c.Delete(wfCacheKey(workflowID, version))
}

func (w *workflowCache) close() {
	w.c.Close()
}
