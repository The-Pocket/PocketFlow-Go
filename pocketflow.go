package pocketflow

import (
	"fmt"
	"log"
	"maps" // Requires Go 1.21+, replace with loop copy for older versions
	"reflect"
	"time"
)

// SharedContext is the map passed between nodes.
type SharedContext = map[string]any

// DefaultAction is the action name used when a node's Post returns an empty string or nil action.
const DefaultAction = "default"

// PocketFlowError represents an error originating from the PocketFlow library.
type PocketFlowError struct {
	Message string
	Cause   error
}

func (e *PocketFlowError) Error() string {
	if e.Cause != nil {
		// Consider adding cause details depending on verbosity needs
		return fmt.Sprintf("PocketFlow error: %s (caused by: %v)", e.Message, e.Cause)
	}
	return fmt.Sprintf("PocketFlow error: %s", e.Message)
}

// Unwrap allows PocketFlowError to work with errors.Is and errors.As.
func (e *PocketFlowError) Unwrap() error {
	return e.Cause
}

func newPocketFlowError(msg string, cause error) error {
	return &PocketFlowError{Message: msg, Cause: cause}
}

func logWarn(format string, v ...any) {
	log.Printf("WARN: PocketFlow - "+format, v...)
}

// --- Base Node ---

// BaseNode defines the interface for all nodes in a workflow.
type BaseNode interface {
	// Prep prepares input for Exec using the shared context.
	// Returns the prepared data (can be nil) and an error.
	Prep(ctx SharedContext) (any, error)

	// Exec performs the main work using the result from Prep.
	// Returns the execution result (can be nil) and an error.
	Exec(prepResult any) (any, error)

	// Post processes results, updates context, and returns the next action string.
	// An empty string implies DefaultAction. Returns an error if post-processing fails.
	Post(ctx SharedContext, prepResult any, execResult any) (string, error)

	// SetParams sets node-specific parameters. Returns the node for chaining.
	SetParams(params map[string]any) BaseNode

	// GetParams returns the node's current parameters.
	GetParams() map[string]any

	// Next connects this node to another node for a specific action. Returns the *next* node for chaining.
	Next(action string, node BaseNode) BaseNode

	// GetSuccessors returns the map of action->node successors.
	GetSuccessors() map[string]BaseNode

	// GetNextNode retrieves the successor node for a given action (or DefaultAction).
	GetNextNode(action string) BaseNode

	// Run executes a single node's lifecycle (prep, exec, post). Useful for standalone execution.
	// Returns the resulting action and error.
	Run(ctx SharedContext) (string, error)

	// InternalRun is used by Flow orchestration to execute the node lifecycle.
	// Separated from Run to prevent potential issues if Run is overridden incorrectly.
	InternalRun(ctx SharedContext) (string, error)
}

// --- Common Node Implementation Details ---

type nodeCore struct {
	params     SharedContext
	successors map[string]BaseNode
}

func (n *nodeCore) initCore() {
	if n.params == nil {
		n.params = make(SharedContext)
	}
	if n.successors == nil {
		n.successors = make(map[string]BaseNode)
	}
}

func (n *nodeCore) SetParams(params map[string]any) {
	n.initCore()
	if params != nil {
		// Create a copy to avoid external modification issues
		n.params = maps.Clone(params) // Go 1.21+
		// For older Go:
		// n.params = make(SharedContext, len(params))
		// for k, v := range params {
		// 	n.params[k] = v
		// }
	} else {
		n.params = make(SharedContext)
	}
}

func (n *nodeCore) GetParams() map[string]any {
	n.initCore()
	// Return a copy to prevent modification? Or trust user? Let's return direct map for now.
	return n.params
}

func (n *nodeCore) Next(action string, node BaseNode) BaseNode {
	n.initCore()
	if node == nil {
		panic("Successor node cannot be nil") // Panic mirrors Java's NullPointerException
	}
	if action == "" {
		action = DefaultAction
	}
	if _, exists := n.successors[action]; exists {
		logWarn("Overwriting successor for action '%s' in node %T", action, n) // %T gives dynamic type
	}
	n.successors[action] = node
	return node // Return the next node for chaining
}

func (n *nodeCore) GetSuccessors() map[string]BaseNode {
	n.initCore()
	return n.successors
}

func (n *nodeCore) GetNextNode(action string) BaseNode {
	n.initCore()
	if action == "" {
		action = DefaultAction
	}
	nextNode, exists := n.successors[action]
	if !exists && len(n.successors) > 0 {
		keys := make([]string, 0, len(n.successors))
		for k := range n.successors {
			keys = append(keys, k)
		}
		logWarn("Flow might end: Action '%s' not found in successors %v of node %T", action, keys, n)
	}
	return nextNode
}

// --- Standard Node (with Retry) ---

// Node implements BaseNode with retry logic.
type Node struct {
	nodeCore
	MaxRetries       int
	WaitMilliseconds time.Duration // Use time.Duration for clarity

	// User-defined functions for node logic
	PrepFunc func(ctx SharedContext, params SharedContext) (any, error)
	ExecFunc func(prepResult any, params SharedContext) (any, error)
	PostFunc func(ctx SharedContext, prepResult any, execResult any, params SharedContext) (string, error)

	// Optional fallback function if all retries fail
	ExecFallbackFunc func(prepResult any, params SharedContext, lastErr error) (any, error)
}

// NewNode creates a new Node with default retry settings (1 try, 0 wait).
func NewNode() *Node {
	n := &Node{
		MaxRetries:       1,
		WaitMilliseconds: 0,
		PrepFunc:         func(ctx SharedContext, params SharedContext) (any, error) { return nil, nil }, // Default no-op
		ExecFunc:         func(prepResult any, params SharedContext) (any, error) { return nil, nil }, // Default no-op
		PostFunc: func(ctx SharedContext, prepResult any, execResult any, params SharedContext) (string, error) {
			return DefaultAction, nil // Default action
		},
	}
	n.initCore()
	return n
}

// SetRetry configures retry behaviour.
func (n *Node) SetRetry(maxRetries int, waitMilliseconds time.Duration) *Node {
	if maxRetries < 1 {
		panic("maxRetries must be at least 1")
	}
	if waitMilliseconds < 0 {
		panic("waitMilliseconds cannot be negative")
	}
	n.MaxRetries = maxRetries
	n.WaitMilliseconds = waitMilliseconds
	return n
}

// SetPrep sets the PrepFunc.
func (n *Node) SetPrep(f func(ctx SharedContext, params SharedContext) (any, error)) *Node {
	n.PrepFunc = f
	return n
}

// SetExec sets the ExecFunc.
func (n *Node) SetExec(f func(prepResult any, params SharedContext) (any, error)) *Node {
	n.ExecFunc = f
	return n
}

// SetPost sets the PostFunc.
func (n *Node) SetPost(f func(ctx SharedContext, prepResult any, execResult any, params SharedContext) (string, error)) *Node {
	n.PostFunc = f
	return n
}

// SetFallback sets the ExecFallbackFunc.
func (n *Node) SetFallback(f func(prepResult any, params SharedContext, lastErr error) (any, error)) *Node {
	n.ExecFallbackFunc = f
	return n
}

// --- BaseNode Implementation for Node ---

func (n *Node) SetParams(params map[string]any) BaseNode {
	n.nodeCore.SetParams(params)
	return n
}

func (n *Node) Next(action string, node BaseNode) BaseNode {
	return n.nodeCore.Next(action, node)
}

func (n *Node) Prep(ctx SharedContext) (any, error) {
	if n.PrepFunc == nil {
		return nil, nil // Default behavior
	}
	return n.PrepFunc(ctx, n.params)
}

func (n *Node) Exec(prepResult any) (any, error) {
	// This is the public Exec, usually called via InternalRun which handles retry
	if n.ExecFunc == nil {
		return nil, nil
	}
	return n.ExecFunc(prepResult, n.params)
}

func (n *Node) Post(ctx SharedContext, prepResult any, execResult any) (string, error) {
	if n.PostFunc == nil {
		return DefaultAction, nil
	}
	action, err := n.PostFunc(ctx, prepResult, execResult, n.params)
	if err == nil && action == "" {
		action = DefaultAction
	}
	return action, err
}

func (n *Node) Run(ctx SharedContext) (string, error) {
	if len(n.successors) > 0 {
		logWarn("Node %T has successors, but Run() was called directly. Successors won't be executed by this call. Use Flow.Run() for orchestration.", n)
	}
	return n.InternalRun(ctx)
}

func (n *Node) InternalRun(ctx SharedContext) (string, error) {
	prepRes, err := n.Prep(ctx)
	if err != nil {
		return "", newPocketFlowError(fmt.Sprintf("Prep phase failed in %T", n), err)
	}

	var execRes any
	var lastExecErr error
	currentRetry := 0

	for currentRetry = 0; currentRetry < n.MaxRetries; currentRetry++ {
		execRes, lastExecErr = n.Exec(prepRes) // Call the non-retry Exec
		if lastExecErr == nil {
			break // Success
		}
		if currentRetry < n.MaxRetries-1 && n.WaitMilliseconds > 0 {
			time.Sleep(n.WaitMilliseconds)
		}
	}

	// If all retries failed
	if lastExecErr != nil {
		if n.ExecFallbackFunc != nil {
			execRes, err = n.ExecFallbackFunc(prepRes, n.params, lastExecErr)
			if err != nil {
				// Wrap the fallback error, potentially including the original execution error
				return "", newPocketFlowError(fmt.Sprintf("ExecFallback phase failed in %T after %d retries", n, n.MaxRetries), err)
			}
			lastExecErr = nil // Fallback succeeded, clear the error
		} else {
			// No fallback, return the last execution error
			return "", newPocketFlowError(fmt.Sprintf("Exec phase failed in %T after %d retries", n, n.MaxRetries), lastExecErr)
		}
	}

	// Post phase
	action, err := n.Post(ctx, prepRes, execRes)
	if err != nil {
		return "", newPocketFlowError(fmt.Sprintf("Post phase failed in %T", n), err)
	}

	return action, nil
}

// --- Batch Node (Processes items individually) ---

// BatchNode implements BaseNode to process slices of items.
type BatchNode struct {
	nodeCore
	MaxRetries       int
	WaitMilliseconds time.Duration

	// User-defined functions
	// Prep returns a slice (or error)
	PrepFunc func(ctx SharedContext, params SharedContext) ([]any, error)
	// ExecItem operates on a single item from the Prep slice
	ExecItemFunc func(item any, params SharedContext) (any, error)
	// Post receives the original prep slice and the slice of exec results
	PostFunc func(ctx SharedContext, prepResult []any, execResult []any, params SharedContext) (string, error)

	// Optional fallback for individual item processing
	ExecItemFallbackFunc func(item any, params SharedContext, lastErr error) (any, error)
}

// NewBatchNode creates a new BatchNode with default settings.
func NewBatchNode() *BatchNode {
	bn := &BatchNode{
		MaxRetries:       1,
		WaitMilliseconds: 0,
		PrepFunc:         func(ctx SharedContext, params SharedContext) ([]any, error) { return nil, nil },
		ExecItemFunc:     func(item any, params SharedContext) (any, error) { return item, nil }, // Default pass-through
		PostFunc: func(ctx SharedContext, prepResult []any, execResult []any, params SharedContext) (string, error) {
			return DefaultAction, nil
		},
	}
	bn.initCore()
	return bn
}

// SetRetry configures retry behaviour.
func (bn *BatchNode) SetRetry(maxRetries int, waitMilliseconds time.Duration) *BatchNode {
	if maxRetries < 1 {
		panic("maxRetries must be at least 1")
	}
	if waitMilliseconds < 0 {
		panic("waitMilliseconds cannot be negative")
	}
	bn.MaxRetries = maxRetries
	bn.WaitMilliseconds = waitMilliseconds
	return bn
}

// SetPrep sets the PrepFunc. Expects a function returning []any.
func (bn *BatchNode) SetPrep(f func(ctx SharedContext, params SharedContext) ([]any, error)) *BatchNode {
	bn.PrepFunc = f
	return bn
}

// SetExecItem sets the ExecItemFunc for processing individual items.
func (bn *BatchNode) SetExecItem(f func(item any, params SharedContext) (any, error)) *BatchNode {
	bn.ExecItemFunc = f
	return bn
}

// SetPost sets the PostFunc. Receives []any prep and []any exec results.
func (bn *BatchNode) SetPost(f func(ctx SharedContext, prepResult []any, execResult []any, params SharedContext) (string, error)) *BatchNode {
	bn.PostFunc = f
	return bn
}

// SetItemFallback sets the ExecItemFallbackFunc.
func (bn *BatchNode) SetItemFallback(f func(item any, params SharedContext, lastErr error) (any, error)) *BatchNode {
	bn.ExecItemFallbackFunc = f
	return bn
}

// --- BaseNode Implementation for BatchNode ---

func (bn *BatchNode) SetParams(params map[string]any) BaseNode {
	bn.nodeCore.SetParams(params)
	return bn
}

func (bn *BatchNode) Next(action string, node BaseNode) BaseNode {
	return bn.nodeCore.Next(action, node)
}

// Prep calls the user-defined PrepFunc.
func (bn *BatchNode) Prep(ctx SharedContext) (any, error) {
	if bn.PrepFunc == nil {
		return nil, nil
	}
	// Prep returns the slice directly (as 'any')
	return bn.PrepFunc(ctx, bn.params)
}

// Exec iterates through the prepResult slice, calling ExecItemFunc for each item with retries.
func (bn *BatchNode) Exec(prepResult any) (any, error) {
	if prepResult == nil {
		return []any{}, nil // Return empty slice if prep was nil
	}

	// Type assertion to get the slice from Prep result
	items, ok := prepResult.([]any)
	if !ok {
		return nil, newPocketFlowError(fmt.Sprintf("Prep phase of BatchNode %T did not return []any, got %T", bn, prepResult), nil)
	}

	if len(items) == 0 {
		return []any{}, nil // Return empty slice for empty input
	}

	results := make([]any, len(items))
	var itemResult any
	var lastItemErr error
	currentRetry := 0

	for i, item := range items {
		lastItemErr = nil // Reset error for each item
		itemSuccess := false
		for currentRetry = 0; currentRetry < bn.MaxRetries; currentRetry++ {
			itemResult, lastItemErr = bn.ExecItemFunc(item, bn.params)
			if lastItemErr == nil {
				itemSuccess = true
				break // Success for this item
			}
			if currentRetry < bn.MaxRetries-1 && bn.WaitMilliseconds > 0 {
				time.Sleep(bn.WaitMilliseconds)
			}
		}

		// If all retries failed for this item
		if !itemSuccess {
			if bn.ExecItemFallbackFunc != nil {
				fallbackResult, fallbackErr := bn.ExecItemFallbackFunc(item, bn.params, lastItemErr)
				if fallbackErr != nil {
					// Fallback failed, return error for the whole batch
					return nil, newPocketFlowError(fmt.Sprintf("ExecItemFallback failed for item %d (%v) in %T after %d retries", i, item, bn, bn.MaxRetries), fallbackErr)
				}
				itemResult = fallbackResult // Use fallback result
				lastItemErr = nil           // Mark as success via fallback
			} else {
				// No fallback, fail the whole batch
				return nil, newPocketFlowError(fmt.Sprintf("ExecItem failed for item %d (%v) in %T after %d retries", i, item, bn, bn.MaxRetries), lastItemErr)
			}
		}
		results[i] = itemResult
	}

	return results, nil // Return the slice of results
}

// Post calls the user-defined PostFunc.
func (bn *BatchNode) Post(ctx SharedContext, prepResult any, execResult any) (string, error) {
	// Type assertions needed as interface methods deal with 'any'
	prepSlice, okPrep := prepResult.([]any)
	if prepResult != nil && !okPrep { // Allow nil prepResult
		return "", newPocketFlowError(fmt.Sprintf("Internal error: prepResult in BatchNode %T Post was not []any (%T)", bn, prepResult), nil)
	}

	execSlice, okExec := execResult.([]any)
	if execResult != nil && !okExec { // Allow nil execResult (e.g., if prep was empty)
		return "", newPocketFlowError(fmt.Sprintf("Internal error: execResult in BatchNode %T Post was not []any (%T)", bn, execResult), nil)
	}
	// Ensure slices are not nil if they were originally nil/empty, matching Java behaviour somewhat
	if prepSlice == nil { prepSlice = []any{} }
	if execSlice == nil { execSlice = []any{} }


	if bn.PostFunc == nil {
		return DefaultAction, nil
	}
	action, err := bn.PostFunc(ctx, prepSlice, execSlice, bn.params)
    if err == nil && action == "" {
		action = DefaultAction
	}
	return action, err
}

func (bn *BatchNode) Run(ctx SharedContext) (string, error) {
	if len(bn.successors) > 0 {
		logWarn("Node %T has successors, but Run() was called directly. Successors won't be executed by this call. Use Flow.Run() for orchestration.", bn)
	}
	return bn.InternalRun(ctx)
}

// InternalRun implements the retry logic at the item level within Exec.
func (bn *BatchNode) InternalRun(ctx SharedContext) (string, error) {
	prepRes, err := bn.Prep(ctx) // prepRes should be []any
	if err != nil {
		return "", newPocketFlowError(fmt.Sprintf("Prep phase failed in %T", bn), err)
	}

	// Exec handles its own item-level retry/fallback
	execRes, err := bn.Exec(prepRes) // execRes should be []any
	if err != nil {
		// Error from Exec already includes context about retries/fallbacks
		return "", err // Don't wrap again
	}

	// Post phase
	action, err := bn.Post(ctx, prepRes, execRes) // Post expects []any, []any
	if err != nil {
		return "", newPocketFlowError(fmt.Sprintf("Post phase failed in %T", bn), err)
	}

	return action, nil
}

// --- Flow ---

// Flow orchestrates the execution of connected nodes.
type Flow struct {
	nodeCore  // Flow itself can have params, though less common for successors here
	startNode BaseNode
}

// NewFlow creates a new Flow, optionally with a starting node.
func NewFlow(startNode BaseNode) *Flow {
	f := &Flow{
		startNode: startNode,
	}
	f.initCore()
	return f
}

// Start sets the initial node for the flow. Returns the start node for chaining setup.
func (f *Flow) Start(node BaseNode) BaseNode {
	if node == nil {
		panic("Start node cannot be nil")
	}
	f.startNode = node
	return node
}

// --- BaseNode Implementation for Flow ---
// Most BaseNode methods are less relevant for Flow itself, focused on orchestration.

func (f *Flow) SetParams(params map[string]any) BaseNode {
	f.nodeCore.SetParams(params)
	return f
}

// Next for a Flow doesn't make logical sense in the standard execution model.
func (f *Flow) Next(action string, node BaseNode) BaseNode {
	logWarn("Calling Next() on a Flow is unusual. Successors set here are not used by standard Run() orchestration.")
	return f.nodeCore.Next(action, node)
}

// Prep for the Flow itself. Default is no-op. Can be overridden if needed.
func (f *Flow) Prep(ctx SharedContext) (any, error) {
	// Typically Flow prep is about setting up the context before orchestration starts
	return nil, nil
}

// Exec for the Flow initiates the orchestration. Should not be called directly by user.
func (f *Flow) Exec(prepResult any) (any, error) {
	// This is called internally by InternalRun after Flow's Prep.
	// The 'prepResult' here is the result of Flow.Prep, not a node's prep.
	// The 'execResult' of a Flow is the final action string from orchestration.
	finalAction, err := f.orchestrate(prepResult.(SharedContext), nil) // Run orchestration
	if err != nil {
		return "", err // Return error, action is irrelevant if orchestration failed
	}
	return finalAction, nil // Return the final action as the result
}

// Post for the Flow runs after orchestration completes. Default returns the final action.
func (f *Flow) Post(ctx SharedContext, prepResult any, execResult any) (string, error) {
	// prepResult is from Flow.Prep, execResult is the final action string from Exec/orchestrate.
	finalAction, _ := execResult.(string) // Ignore error, default to "" if cast fails
    if finalAction == "" {
        finalAction = DefaultAction // Or maybe keep it empty? Let's default.
    }
	return finalAction, nil
}

// Run starts the flow execution.
func (f *Flow) Run(ctx SharedContext) (string, error) {
	// Use InternalRun to perform the standard Flow lifecycle (Prep, Exec(orchestrate), Post)
	return f.InternalRun(ctx)
}

// InternalRun executes the flow's lifecycle: Prep, Orchestrate (via Exec), Post.
func (f *Flow) InternalRun(ctx SharedContext) (string, error) {
	// 1. Run Flow's Prep phase
	flowPrepResult, err := f.Prep(ctx)
	if err != nil {
		return "", newPocketFlowError(fmt.Sprintf("Prep phase failed for Flow %T", f), err)
	}

	// 2. Run Flow's Exec phase (which triggers orchestration)
	// Pass the *original* shared context to orchestration.
	// Flow's prepResult isn't typically used directly in orchestration nodes.
	flowExecResult, err := f.Exec(ctx) // Exec calls orchestrate
	if err != nil {
		// Error likely came from a node within orchestrate
		return "", err // Don't wrap again, error should be informative
	}

	// 3. Run Flow's Post phase
	finalAction, err := f.Post(ctx, flowPrepResult, flowExecResult)
	if err != nil {
		return "", newPocketFlowError(fmt.Sprintf("Post phase failed for Flow %T", f), err)
	}

	return finalAction, nil
}

// orchestrate executes the node chain starting from startNode.
// initialParams are merged with the flow's own params for the *first* node.
// Returns the last action string and any error encountered.
func (f *Flow) orchestrate(ctx SharedContext, initialParams SharedContext) (string, error) {
	if f.startNode == nil {
		logWarn("Flow started with no start node.")
		return "", nil // No error, just nothing to run
	}

	currentNode := f.startNode
	lastAction := ""
	var err error

	// Prepare initial parameters for the first node run
	// Combine Flow's params and any specific initialParams for this run
	combinedParams := maps.Clone(f.params) // Start with flow's base params (Go 1.21+)
	// For older Go: loop copy f.params
	if initialParams != nil {
		maps.Copy(combinedParams, initialParams) // Merge initialParams, overwriting base (Go 1.21+)
        // For older Go: loop copy initialParams into combinedParams
	}


	for currentNode != nil {
		// Set the combined params *before* running the node
		// Subsequent nodes will inherit params via the nodeCore logic if not explicitly set otherwise
		currentNode.SetParams(combinedParams)
		// Clear combinedParams after first use to avoid reapplying initialParams accidentally
		combinedParams = nil

		// Execute the node's full lifecycle (Prep, Exec, Post)
		lastAction, err = currentNode.InternalRun(ctx)
		if err != nil {
			// Error occurred within the node's execution
			return "", err // Return the error immediately
		}

		// Get the next node based on the action returned by Post
		currentNode = currentNode.GetNextNode(lastAction)

		// If we are moving to a next node, ensure its params are potentially updated
		// (e.g., if Flow params changed or if we want nodes to inherit params)
        // For now, node params are set once at the start of orchestrate,
        // or individually if nodes modify their own params. Revisit if complex
        // parameter propagation is needed between nodes.
		if currentNode != nil {
             // Option 1: Inherit Flow's base params if node params are empty? (Maybe too complex)
             // Option 2: Let nodes manage their own params set via SetParams before InternalRun. (Current behaviour)
             // Option 3: Explicitly copy params from previous node? (Seems unintuitive)
             // Sticking with Option 2 for simplicity, matches Java more closely.
             // If combinedParams was used, we need ensure subsequent nodes get *some* params.
             // Let's ensure subsequent nodes get at least the Flow's base params if theirs are unset.
             if len(currentNode.GetParams()) == 0 && len(f.params) > 0 {
                  currentNode.SetParams(f.params) // Give it the flow's base params if it has none
             }
		}

	}

	// Orchestration finished successfully, return the last action determined
	return lastAction, nil
}

// --- Batch Flow ---

// BatchFlow runs the entire flow sequence for each parameter set generated by PrepBatch.
type BatchFlow struct {
	Flow // Embed Flow to inherit its structure and orchestration logic

	// User-defined functions for batch behavior
	PrepBatchFunc func(ctx SharedContext, params SharedContext) ([]SharedContext, error)
	PostBatchFunc func(ctx SharedContext, batchPrepResult []SharedContext, params SharedContext) (string, error)
}

// NewBatchFlow creates a new BatchFlow.
func NewBatchFlow(startNode BaseNode) *BatchFlow {
	bf := &BatchFlow{
		Flow: Flow{ // Initialize embedded Flow
			startNode: startNode,
		},
		// Provide sensible defaults?
		PrepBatchFunc: func(ctx SharedContext, params SharedContext) ([]SharedContext, error) { return nil, nil },
		PostBatchFunc: func(ctx SharedContext, batchPrepResult []SharedContext, params SharedContext) (string, error) { return DefaultAction, nil },
	}
	bf.initCore() // Initialize nodeCore for the BatchFlow itself
	bf.Flow.initCore() // Ensure embedded Flow's core is also initialized
	return bf
}

// SetPrepBatch sets the function to generate batch parameters.
func (bf *BatchFlow) SetPrepBatch(f func(ctx SharedContext, params SharedContext) ([]SharedContext, error)) *BatchFlow {
	bf.PrepBatchFunc = f
	return bf
}

// SetPostBatch sets the function to run after all batches complete.
func (bf *BatchFlow) SetPostBatch(f func(ctx SharedContext, batchPrepResult []SharedContext, params SharedContext) (string, error)) *BatchFlow {
	bf.PostBatchFunc = f
	return bf
}


// --- BaseNode Implementation Overrides for BatchFlow ---

// Prep for BatchFlow runs its PrepBatchFunc.
func (bf *BatchFlow) Prep(ctx SharedContext) (any, error) {
	if bf.PrepBatchFunc == nil {
		return nil, nil
	}
	// Returns []SharedContext
	return bf.PrepBatchFunc(ctx, bf.params)
}

// Exec for BatchFlow runs the orchestration for each batch item.
// The 'prepResult' here is the []SharedContext from BatchFlow.Prep.
func (bf *BatchFlow) Exec(prepResult any) (any, error) {
    batchParamsList, ok := prepResult.([]SharedContext)
    if prepResult != nil && !ok {
        return "", newPocketFlowError(fmt.Sprintf("Internal error: prepResult in BatchFlow %T Exec was not []SharedContext (%T)", bf, prepResult), nil)
    }
     if batchParamsList == nil {
		 batchParamsList = []SharedContext{}
	 }


    for i, batchParams := range batchParamsList {
        // Run the embedded Flow's orchestration logic for each parameter set.
        // Pass current batchParams as initialParams for this specific run.
        _, err := bf.Flow.orchestrate(ctx, batchParams) // Use the SAME shared context for all runs
        if err != nil {
            // If one batch run fails, fail the whole BatchFlow execution
            return "", newPocketFlowError(fmt.Sprintf("Orchestration failed for batch item %d in %T", i, bf), err)
        }
        // Result (lastAction) of individual orchestrate runs is ignored here; side effects matter.
    }

    // The overall 'result' of BatchFlow's Exec phase is considered the original batch params list
    // This is needed for the PostBatchFunc signature.
    return batchParamsList, nil
}


// Post for BatchFlow runs its PostBatchFunc.
func (bf *BatchFlow) Post(ctx SharedContext, prepResult any, execResult any) (string, error) {
	// prepResult is the result of BatchFlow.Prep ([]SharedContext)
	// execResult is the result of BatchFlow.Exec (which we defined as the same []SharedContext)

    batchPrepResult, okPrep := prepResult.([]SharedContext)
	if prepResult != nil && !okPrep {
        return "", newPocketFlowError(fmt.Sprintf("Internal error: prepResult in BatchFlow %T Post was not []SharedContext (%T)", bf, prepResult), nil)
    }
     if batchPrepResult == nil {
		 batchPrepResult = []SharedContext{}
	 }

	if bf.PostBatchFunc == nil {
		return DefaultAction, nil
	}

	action, err := bf.PostBatchFunc(ctx, batchPrepResult, bf.params)
    if err == nil && action == "" {
		action = DefaultAction
	}
	return action, err
}


// Run starts the BatchFlow execution.
func (bf *BatchFlow) Run(ctx SharedContext) (string, error) {
	// Use InternalRun to perform the standard lifecycle (PrepBatch, Exec Batches, PostBatch)
	return bf.InternalRun(ctx)
}

// InternalRun executes the BatchFlow lifecycle: PrepBatch, Exec(orchestrate per batch), PostBatch.
func (bf *BatchFlow) InternalRun(ctx SharedContext) (string, error) {
	// 1. Run BatchFlow's Prep phase (PrepBatchFunc)
	// Should return []SharedContext
	prepBatchResult, err := bf.Prep(ctx)
	if err != nil {
		return "", newPocketFlowError(fmt.Sprintf("PrepBatch phase failed for BatchFlow %T", bf), err)
	}

	// 2. Run BatchFlow's Exec phase (Orchestrates for each item in prepBatchResult)
	// Exec returns the original prepBatchResult list after successful runs
	execResult, err := bf.Exec(prepBatchResult) // Exec calls orchestrate multiple times
	if err != nil {
		// Error likely came from a node within one of the orchestrate runs
		return "", err // Don't wrap again
	}

	// 3. Run BatchFlow's Post phase (PostBatchFunc)
	finalAction, err := bf.Post(ctx, prepBatchResult, execResult) // Pass prep and exec results
	if err != nil {
		return "", newPocketFlowError(fmt.Sprintf("PostBatch phase failed for BatchFlow %T", bf), err)
	}

	return finalAction, nil
}

// --- Type checking helper (optional but potentially useful) ---
// Example: Check if a result from 'any' is actually an int
func expectInt(val any, nodeType string, phase string) (int, error) {
	i, ok := val.(int)
	if !ok {
		return 0, newPocketFlowError(fmt.Sprintf("%s phase in %s expected int, got %T", phase, nodeType, val), nil)
	}
	return i, nil
}