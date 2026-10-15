package pocketflow

// Flow executes connected nodes until the returned action has no successor.
// A Flow is also a node: nest it or branch on its last action in an outer Flow.
type Flow struct {
	nodeCore
	startNode BaseNode
	PrepFunc  func(*PfContext, map[string]any) (any, error)
	PostFunc  func(*PfContext, map[string]any, any, any) (string, error)
}

// NewFlow accepts nil for an empty flow; Start may configure its entry later.
func NewFlow(start BaseNode) *Flow {
	return &Flow{nodeCore: nodeCore{params: cloneParams(nil)}, startNode: start}
}

func (f *Flow) Clone() BaseNode {
	clone := *f
	clone.nodeCore = f.nodeCore.clone()
	return &clone
}

func (f *Flow) Start(node BaseNode) BaseNode {
	f.startNode = node
	return node
}

func (f *Flow) SetParams(params map[string]any) BaseNode {
	f.setParams(params)
	return f
}

func (f *Flow) SetPrep(fn func(*PfContext, map[string]any) (any, error)) *Flow {
	f.PrepFunc = fn
	return f
}

func (f *Flow) SetPost(fn func(*PfContext, map[string]any, any, any) (string, error)) *Flow {
	f.PostFunc = fn
	return f
}

func (f *Flow) Prep(ctx *PfContext) (any, error) {
	if f.PrepFunc == nil {
		return nil, nil
	}
	return f.PrepFunc(ctx, f.params)
}

func (f *Flow) Exec(ctx *PfContext, _ any) (any, error) {
	return f.orchestrate(ctx, cloneParams(f.params))
}

func (f *Flow) Post(ctx *PfContext, prep, result any) (string, error) {
	if f.PostFunc != nil {
		return f.PostFunc(ctx, f.params, prep, result)
	}
	action, _ := result.(string)
	return action, nil
}

func (f *Flow) Run(ctx *PfContext) (string, error) { return runStandalone(f, ctx, false) }

func (f *Flow) InternalRun(ctx *PfContext) (string, error) {
	ctx = ctx.withExecution(f.params)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	prep, err := f.Prep(ctx)
	if err != nil {
		return "", phaseError("Prep", f, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	result, err := f.Exec(ctx, prep)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	action, err := f.Post(ctx, prep, result)
	if err != nil {
		return "", phaseError("Post", f, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return action, nil
}

func (f *Flow) orchestrate(ctx *PfContext, params map[string]any) (string, error) {
	current, lastAction := f.startNode, ""
	// Sequential steps share this execution's params, as in Python's _orch.
	// Nested flows copy the map when starting their own orchestration.
	for current != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		execution, err := cloneNode(current)
		if err != nil {
			return "", err
		}
		// Limit the shared-map fast path to built-in types. An embedded Node
		// also promotes setRunParams, but must not hide a custom SetParams.
		switch receiver := execution.(type) {
		case *Node:
			receiver.setRunParams(params)
		case *BatchNode:
			receiver.setRunParams(params)
		case *Flow:
			receiver.setRunParams(params)
		case *BatchFlow:
			receiver.setRunParams(params)
		default:
			execution.SetParams(params)
		}
		lastAction, err = execution.InternalRun(ctx)
		if err != nil {
			return "", err
		}
		current = execution.GetNextNode(lastAction)
	}
	return lastAction, nil
}

var _ CloneableNode = (*Flow)(nil)
