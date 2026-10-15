package pocketflow

import (
	"fmt"
	"time"
)

// BatchNode prepares items, retries each item, then posts their ordered results.
type BatchNode struct {
	nodeCore
	MaxRetries           int
	WaitMilliseconds     time.Duration
	PrepFunc             func(*PfContext, map[string]any) ([]any, error)
	ExecItemFunc         func(*PfContext, map[string]any, any) (any, error)
	PostFunc             func(*PfContext, map[string]any, []any, []any) (string, error)
	ExecItemFallbackFunc func(*PfContext, map[string]any, any, error) (any, error)
	parallel             bool
	concurrency          int
}

func NewBatchNode() *BatchNode {
	return &BatchNode{nodeCore: nodeCore{params: cloneParams(nil)}, MaxRetries: 1}
}

// ParallelBatchNode shares the batch API, with concurrent exec for its items.
type ParallelBatchNode = BatchNode

func NewParallelBatchNode() *ParallelBatchNode {
	node := NewBatchNode()
	node.parallel = true
	return node
}

func (n *BatchNode) Clone() BaseNode {
	clone := *n
	clone.nodeCore = n.nodeCore.clone()
	return &clone
}

func (n *BatchNode) SetParams(params map[string]any) BaseNode {
	n.setParams(params)
	return n
}

func (n *BatchNode) SetRetry(attempts int, wait time.Duration) *BatchNode {
	validateRetry(attempts, wait)
	n.MaxRetries, n.WaitMilliseconds = attempts, wait
	return n
}

// SetConcurrency limits parallel item executions; zero means unlimited.
func (n *BatchNode) SetConcurrency(limit int) *BatchNode {
	if limit < 0 {
		panic("concurrency cannot be negative")
	}
	n.concurrency = limit
	return n
}

func (n *BatchNode) SetPrep(f func(*PfContext, map[string]any) ([]any, error)) *BatchNode {
	n.PrepFunc = f
	return n
}

func (n *BatchNode) SetExecItem(f func(*PfContext, map[string]any, any) (any, error)) *BatchNode {
	n.ExecItemFunc = f
	return n
}

func (n *BatchNode) SetPost(f func(*PfContext, map[string]any, []any, []any) (string, error)) *BatchNode {
	n.PostFunc = f
	return n
}

func (n *BatchNode) SetItemFallback(f func(*PfContext, map[string]any, any, error) (any, error)) *BatchNode {
	n.ExecItemFallbackFunc = f
	return n
}

func (n *BatchNode) Prep(ctx *PfContext) (any, error) {
	if n.PrepFunc == nil {
		return nil, nil
	}
	return n.PrepFunc(ctx, n.params)
}

func batchItems(value any) ([]any, error) {
	if value == nil {
		return []any{}, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("expected []any, got %T", value)
	}
	if items == nil {
		items = []any{}
	}
	return items, nil
}

func (n *BatchNode) execItem(ctx *PfContext, params map[string]any, item any) (any, error) {
	var fallback func(*PfContext, error) (any, error)
	if n.ExecItemFallbackFunc != nil {
		fallback = func(ctx *PfContext, err error) (any, error) {
			return n.ExecItemFallbackFunc(ctx, params, item, err)
		}
	}
	return retryExec(ctx, n.MaxRetries, n.WaitMilliseconds,
		func(ctx *PfContext) (any, error) {
			if n.ExecItemFunc == nil {
				return nil, nil
			}
			return n.ExecItemFunc(ctx, params, item)
		}, fallback)
}

func (n *BatchNode) Exec(ctx *PfContext, prep any) (any, error) {
	items, err := batchItems(prep)
	if err != nil {
		return nil, err
	}
	results := make([]any, len(items))
	if n.parallel {
		err = gather(ctx, len(items), n.concurrency, func(index int) error {
			params := cloneParams(n.params)
			itemCtx := ctx.withExecution(params)
			result, err := n.execItem(itemCtx, params, items[index])
			if err != nil {
				return newPocketFlowError(fmt.Sprintf("ExecItem failed for item %d", index), err)
			}
			results[index] = result
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		for index, item := range items {
			result, err := n.execItem(ctx, n.params, item)
			if err != nil {
				return nil, newPocketFlowError(fmt.Sprintf("ExecItem failed for item %d", index), err)
			}
			results[index] = result
		}
	}
	return results, nil
}

func (n *BatchNode) Post(ctx *PfContext, prep, result any) (string, error) {
	items, err := batchItems(prep)
	if err != nil {
		return "", err
	}
	results, err := batchItems(result)
	if err != nil {
		return "", err
	}
	if n.PostFunc == nil {
		return "", nil
	}
	return n.PostFunc(ctx, n.params, items, results)
}

func (n *BatchNode) Run(ctx *PfContext) (string, error) { return runStandalone(n, ctx, true) }

func (n *BatchNode) InternalRun(ctx *PfContext) (string, error) {
	ctx = ctx.withExecution(n.params)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	prep, err := n.Prep(ctx)
	if err != nil {
		return "", phaseError("Prep", n, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	result, err := n.Exec(ctx, prep)
	if err != nil {
		return "", phaseError("Exec", n, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	action, err := n.Post(ctx, prep, result)
	if err != nil {
		return "", phaseError("Post", n, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return action, nil
}

var _ CloneableNode = (*BatchNode)(nil)
