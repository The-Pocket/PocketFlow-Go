package pocketflow

import "time"

// Node runs prep/exec/post, retrying exec and optionally using a fallback.
// Configure fields and callbacks before starting any executions.
type Node struct {
	nodeCore
	MaxRetries       int
	WaitMilliseconds time.Duration
	PrepFunc         func(*PfContext, map[string]any) (any, error)
	ExecFunc         func(*PfContext, map[string]any, any) (any, error)
	PostFunc         func(*PfContext, map[string]any, any, any) (string, error)
	ExecFallbackFunc func(*PfContext, map[string]any, any, error) (any, error)
}

func NewNode() *Node { return &Node{nodeCore: nodeCore{params: cloneParams(nil)}, MaxRetries: 1} }

func (n *Node) Clone() BaseNode {
	clone := *n
	clone.nodeCore = n.nodeCore.clone()
	return &clone
}

func (n *Node) SetParams(params map[string]any) BaseNode {
	n.setParams(params)
	return n
}

func (n *Node) SetRetry(attempts int, wait time.Duration) *Node {
	validateRetry(attempts, wait)
	n.MaxRetries, n.WaitMilliseconds = attempts, wait
	return n
}

func (n *Node) SetPrep(f func(*PfContext, map[string]any) (any, error)) *Node {
	n.PrepFunc = f
	return n
}

func (n *Node) SetExec(f func(*PfContext, map[string]any, any) (any, error)) *Node {
	n.ExecFunc = f
	return n
}

func (n *Node) SetPost(f func(*PfContext, map[string]any, any, any) (string, error)) *Node {
	n.PostFunc = f
	return n
}

func (n *Node) SetFallback(f func(*PfContext, map[string]any, any, error) (any, error)) *Node {
	n.ExecFallbackFunc = f
	return n
}

func (n *Node) Prep(ctx *PfContext) (any, error) {
	if n.PrepFunc == nil {
		return nil, nil
	}
	return n.PrepFunc(ctx, n.params)
}

// Exec is a single attempt. Run applies the retry/fallback policy.
func (n *Node) Exec(ctx *PfContext, prep any) (any, error) {
	if n.ExecFunc == nil {
		return nil, nil
	}
	return n.ExecFunc(ctx, n.params, prep)
}

func (n *Node) Post(ctx *PfContext, prep, result any) (string, error) {
	if n.PostFunc == nil {
		return "", nil
	}
	return n.PostFunc(ctx, n.params, prep, result)
}

func (n *Node) Run(ctx *PfContext) (string, error) { return runStandalone(n, ctx, true) }

func (n *Node) InternalRun(ctx *PfContext) (string, error) {
	ctx = ctx.withExecution(n.params)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	prep, err := n.Prep(ctx)
	if err != nil {
		return "", phaseError("Prep", n, err)
	}
	var fallback func(*PfContext, error) (any, error)
	if n.ExecFallbackFunc != nil {
		fallback = func(ctx *PfContext, err error) (any, error) {
			return n.ExecFallbackFunc(ctx, n.params, prep, err)
		}
	}
	result, err := retryExec(ctx, n.MaxRetries, n.WaitMilliseconds,
		func(ctx *PfContext) (any, error) { return n.Exec(ctx, prep) }, fallback)
	if err != nil {
		return "", phaseError("Exec", n, err)
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

var _ CloneableNode = (*Node)(nil)
