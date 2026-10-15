// Package pocketflow provides composable nodes and sequential or parallel flows.
// Build a graph once, then run it with independent params and a shared context.
package pocketflow

import (
	"fmt"
	"log"
	"sort"
)

// DefaultAction is the successor key used when post returns an empty action.
const DefaultAction = "default"

// PocketFlowError identifies a failed lifecycle phase and preserves its cause.
type PocketFlowError struct {
	Message string
	Cause   error
}

func (e *PocketFlowError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("PocketFlow error: %s (caused by: %v)", e.Message, e.Cause)
	}
	return "PocketFlow error: " + e.Message
}

func (e *PocketFlowError) Unwrap() error { return e.Cause }

func newPocketFlowError(message string, cause error) error {
	return &PocketFlowError{Message: message, Cause: cause}
}

func phaseError(phase string, node BaseNode, err error) error {
	return newPocketFlowError(fmt.Sprintf("%s phase failed in %T", phase, node), err)
}

func logWarn(format string, args ...any) {
	log.Printf("WARN: PocketFlow - "+format, args...)
}

// BaseNode is a node or a flow. An empty post action selects the default edge.
// Run executes just this node's lifecycle; a Flow follows its successors.
type BaseNode interface {
	Prep(ctx *PfContext) (any, error)
	Exec(ctx *PfContext, prepResult any) (any, error)
	Post(ctx *PfContext, prepResult any, execResult any) (string, error)
	SetParams(params map[string]any) BaseNode
	GetParams() map[string]any
	Next(action string, node BaseNode) BaseNode
	GetSuccessors() map[string]BaseNode
	GetNextNode(action string) BaseNode
	Run(ctx *PfContext) (string, error)
	// InternalRun is the orchestration hook called on an execution-local clone.
	InternalRun(ctx *PfContext) (string, error)
}

// CloneableNode participates in flow execution without mutating graph state.
// Custom nodes must preserve their concrete type, configuration and successors.
// A clone needs a fresh top-level params map; application objects remain shared.
type CloneableNode interface {
	BaseNode
	Clone() BaseNode
}

type nodeCore struct {
	params     map[string]any
	successors map[string]BaseNode
}

func cloneParams(params map[string]any) map[string]any {
	result := make(map[string]any, len(params))
	for key, value := range params {
		result[key] = value
	}
	return result
}

func mergeParams(base, overrides map[string]any) map[string]any {
	result := cloneParams(base)
	for key, value := range overrides {
		result[key] = value
	}
	return result
}

func (n *nodeCore) clone() nodeCore {
	return nodeCore{params: cloneParams(n.params), successors: n.GetSuccessors()}
}

func (n *nodeCore) setParams(params map[string]any) { n.params = cloneParams(params) }

// setRunParams installs an already isolated map shared by sequential steps.
func (n *nodeCore) setRunParams(params map[string]any) { n.params = params }

// GetParams returns a shallow copy of configured params, never runtime results.
func (n *nodeCore) GetParams() map[string]any { return cloneParams(n.params) }

func (n *nodeCore) Next(action string, node BaseNode) BaseNode {
	if node == nil {
		panic("successor node cannot be nil")
	}
	if action == "" {
		action = DefaultAction
	}
	if n.successors == nil {
		n.successors = make(map[string]BaseNode)
	}
	if _, exists := n.successors[action]; exists {
		logWarn("Overwriting successor for action '%s'", action)
	}
	n.successors[action] = node
	return node
}

// GetSuccessors returns a copy; use Next to configure an edge.
func (n *nodeCore) GetSuccessors() map[string]BaseNode {
	result := make(map[string]BaseNode, len(n.successors))
	for action, node := range n.successors {
		result[action] = node
	}
	return result
}

func (n *nodeCore) GetNextNode(action string) BaseNode {
	key := action
	if key == "" {
		key = DefaultAction
	}
	next := n.successors[key]
	if next == nil && len(n.successors) > 0 {
		keys := make([]string, 0, len(n.successors))
		for successor := range n.successors {
			keys = append(keys, successor)
		}
		sort.Strings(keys)
		logWarn("Flow ends: '%s' not found in %v", key, keys)
	}
	return next
}

func cloneNode(node BaseNode) (BaseNode, error) {
	cloneable, ok := node.(CloneableNode)
	if !ok {
		return nil, newPocketFlowError(fmt.Sprintf("custom node %T must implement Clone() BaseNode for isolated execution", node), nil)
	}
	clone := cloneable.Clone()
	if clone == nil {
		return nil, newPocketFlowError(fmt.Sprintf("Clone of %T returned nil", node), nil)
	}
	return clone, nil
}

func runStandalone(node CloneableNode, ctx *PfContext, warn bool) (string, error) {
	if warn && len(node.GetSuccessors()) > 0 {
		logWarn("Node won't run successors. Use Flow.")
	}
	clone, err := cloneNode(node)
	if err != nil {
		return "", err
	}
	return clone.InternalRun(ctx)
}
