package pocketflow

import "fmt"

// BatchFlow runs a complete flow for each parameter map prepared by its hook.
type BatchFlow struct {
	Flow
	PrepBatchFunc func(*PfContext, map[string]any) ([]map[string]any, error)
	PostBatchFunc func(*PfContext, map[string]any, []map[string]any) (string, error)
	parallel      bool
	concurrency   int
}

func NewBatchFlow(start BaseNode) *BatchFlow {
	return &BatchFlow{Flow: *NewFlow(start)}
}

// ParallelBatchFlow executes the parameter groups concurrently.
type ParallelBatchFlow = BatchFlow

func NewParallelBatchFlow(start BaseNode) *ParallelBatchFlow {
	flow := NewBatchFlow(start)
	flow.parallel = true
	return flow
}

func (f *BatchFlow) Clone() BaseNode {
	clone := *f
	clone.nodeCore = f.nodeCore.clone()
	return &clone
}

func (f *BatchFlow) SetParams(params map[string]any) BaseNode {
	f.setParams(params)
	return f
}

func (f *BatchFlow) SetPrepBatch(fn func(*PfContext, map[string]any) ([]map[string]any, error)) *BatchFlow {
	f.PrepBatchFunc = fn
	f.PrepFunc = nil
	return f
}

func (f *BatchFlow) SetPostBatch(fn func(*PfContext, map[string]any, []map[string]any) (string, error)) *BatchFlow {
	f.PostBatchFunc = fn
	f.PostFunc = nil
	return f
}

// SetPrep offers the generic Flow hook. Its result must be []map[string]any.
func (f *BatchFlow) SetPrep(fn func(*PfContext, map[string]any) (any, error)) *BatchFlow {
	f.PrepFunc = fn
	f.PrepBatchFunc = nil
	return f
}

// SetPost offers the generic Flow hook; execResult is nil for a batch flow.
func (f *BatchFlow) SetPost(fn func(*PfContext, map[string]any, any, any) (string, error)) *BatchFlow {
	f.PostFunc = fn
	f.PostBatchFunc = nil
	return f
}

// SetConcurrency limits parallel flow executions; zero means unlimited.
func (f *BatchFlow) SetConcurrency(limit int) *BatchFlow {
	if limit < 0 {
		panic("concurrency cannot be negative")
	}
	f.concurrency = limit
	return f
}

func (f *BatchFlow) Prep(ctx *PfContext) (any, error) {
	if f.PrepBatchFunc == nil {
		return f.Flow.Prep(ctx)
	}
	return f.PrepBatchFunc(ctx, f.params)
}

func batchParams(value any) ([]map[string]any, error) {
	if value == nil {
		return []map[string]any{}, nil
	}
	params, ok := value.([]map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected []map[string]any, got %T", value)
	}
	if params == nil {
		params = []map[string]any{}
	}
	return params, nil
}

func (f *BatchFlow) Exec(ctx *PfContext, prep any) (any, error) {
	groups, err := batchParams(prep)
	if err != nil {
		return nil, err
	}
	run := func(index int) error {
		params := mergeParams(f.params, groups[index])
		_, err := f.orchestrate(ctx.withExecution(params), params)
		if err != nil {
			return newPocketFlowError(fmt.Sprintf("Orchestration failed for batch item %d", index), err)
		}
		return nil
	}
	if f.parallel {
		err = gather(ctx, len(groups), f.concurrency, run)
	} else {
		for index := range groups {
			if err = ctx.Err(); err != nil {
				break
			}
			if err = run(index); err != nil {
				break
			}
		}
	}
	return nil, err
}

func (f *BatchFlow) Post(ctx *PfContext, prep, result any) (string, error) {
	groups, err := batchParams(prep)
	if err != nil {
		return "", err
	}
	if f.PostBatchFunc != nil {
		return f.PostBatchFunc(ctx, f.params, groups)
	}
	return f.Flow.Post(ctx, prep, result)
}

func (f *BatchFlow) Run(ctx *PfContext) (string, error) { return runStandalone(f, ctx, false) }

func (f *BatchFlow) InternalRun(ctx *PfContext) (string, error) {
	ctx = ctx.withExecution(f.params)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	prep, err := f.Prep(ctx)
	if err != nil {
		return "", phaseError("PrepBatch", f, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	result, err := f.Exec(ctx, prep)
	if err != nil {
		return "", phaseError("ExecBatch", f, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	action, err := f.Post(ctx, prep, result)
	if err != nil {
		return "", phaseError("PostBatch", f, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return action, nil
}

var _ CloneableNode = (*BatchFlow)(nil)
