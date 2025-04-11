package pocketflow_test

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pf "github.com/The-Pocket/PocketFlow-Go" // Assuming module path
)

// --- Test Node Implementations using Functional Style ---

// setNumberNode creates a node that sets a number in the context.
func setNumberNode(number int) pf.BaseNode {
	n := pf.NewNode().
		SetExec(func(prepResult any, params pf.SharedContext) (any, error) {
			multiplier := 1
			if m, ok := params["multiplier"].(int); ok {
				multiplier = m
			}
			return number * multiplier, nil // Exec result is the number
		}).
		SetPost(func(ctx pf.SharedContext, prepResult any, execResult any, params pf.SharedContext) (string, error) {
			num := execResult.(int) // Assume execResult is int
			ctx["currentValue"] = num
			if num > 20 {
				return "over_20", nil
			}
			return pf.DefaultAction, nil
		})
	return n
}

// addNumberNode creates a node that adds a number based on context.
func addNumberNode(numberToAdd int) pf.BaseNode {
	n := pf.NewNode().
		SetPrep(func(ctx pf.SharedContext, params pf.SharedContext) (any, error) {
			current, ok := ctx["currentValue"].(int)
			if !ok {
				return nil, fmt.Errorf("currentValue not found or not an int in context")
			}
			return current, nil // Prep result is the current value
		}).
		SetExec(func(prepResult any, params pf.SharedContext) (any, error) {
			current := prepResult.(int) // Assume prepResult is int
			return current + numberToAdd, nil
		}).
		SetPost(func(ctx pf.SharedContext, prepResult any, execResult any, params pf.SharedContext) (string, error) {
			num := execResult.(int) // Assume execResult is int
			ctx["currentValue"] = num
			return "added", nil // Action to trigger next node
		})
	return n
}

// resultCaptureNode creates a node that captures the context value into its own params.
func resultCaptureNode() pf.BaseNode {
	n := pf.NewNode().
		SetPrep(func(ctx pf.SharedContext, params pf.SharedContext) (any, error) {
			val, ok := ctx["currentValue"]
			if !ok {
				// Provide a default if not found, mirroring Java test
				return -999, nil
			}
			return val.(int), nil
		}).
		SetExec(func(prepResult any, params pf.SharedContext) (any, error) {
			capturedVal := prepResult.(int)
			params["capturedValue"] = capturedVal // Store in node's *own* params
			return nil, nil                      // No meaningful exec result needed
		})
		// Default Post is sufficient (returns DefaultAction)
	return n
}

// simpleLogNode creates a node for BatchFlow testing.
func simpleLogNode() pf.BaseNode {
	n := pf.NewNode().
		SetExec(func(prepResult any, params pf.SharedContext) (any, error) {
			multi := params["multiplier"] // Get multiplier from params set by BatchFlow
			message := fmt.Sprintf("SimpleLogNode executed with multiplier: %v", multi)
			return message, nil
		}).
		SetPost(func(ctx pf.SharedContext, prepResult any, execResult any, params pf.SharedContext) (string, error) {
			message := execResult.(string)
			key := fmt.Sprintf("last_message_from_batch_%v", params["multiplier"])
			ctx[key] = message // Store message in shared context
			return pf.DefaultAction, nil
		})
	return n
}

// --- Test Methods ---

func TestSimpleLinearFlow(t *testing.T) {
	start := setNumberNode(10)
	add := addNumberNode(5)
	capture := resultCaptureNode()

	// Connect nodes: start -> add (on default) -> capture (on "added")
	start.Next(pf.DefaultAction, add).Next("added", capture)

	flow := pf.NewFlow(start)
	sharedContext := make(pf.SharedContext)

	lastAction, err := flow.Run(sharedContext)
	require.NoError(t, err)

	// Capture node is the last one, its default post returns "default"
	assert.Equal(t, pf.DefaultAction, lastAction) // Flow's post returns last node's action
	assert.Equal(t, 15, sharedContext["currentValue"])

	// Check the captured value in the capture node's *own* parameters
	captureParams := capture.GetParams()
	assert.Equal(t, 15, captureParams["capturedValue"])
}

func TestBranchingFlow(t *testing.T) {
	start := setNumberNode(10)
	add := addNumberNode(5)
	captureDefault := resultCaptureNode()
	captureOver20 := resultCaptureNode()

	// Connections:
	// start -> add (on default) -> captureDefault (on "added")
	// start -> captureOver20 (on "over_20")
	start.Next(pf.DefaultAction, add).Next("added", captureDefault)
	start.Next("over_20", captureOver20)

	flow := pf.NewFlow(start)
	sharedContext := make(pf.SharedContext)

	// Set parameters on the flow, which will be passed to the start node
	flow.SetParams(pf.SharedContext{"multiplier": 3})

	lastAction, err := flow.Run(sharedContext)
	require.NoError(t, err)

	// The flow should take the "over_20" branch to captureOver20, which returns "default"
	assert.Equal(t, pf.DefaultAction, lastAction)
	assert.Equal(t, 30, sharedContext["currentValue"])

	// Check the correct capture node got the value
	captureOver20Params := captureOver20.GetParams()
	captureDefaultParams := captureDefault.GetParams()

	assert.Equal(t, 30, captureOver20Params["capturedValue"])
	_, existsDefault := captureDefaultParams["capturedValue"]
	assert.False(t, existsDefault, "captureDefault should not have captured a value")
	// Check default value wasn't accidentally set if GetParams() returns nil map initially
	if defaultVal, ok := captureDefaultParams["capturedValue"]; ok {
		assert.NotEqual(t, -999, defaultVal, "Default prep value should not be in params")
	}

}

func TestBatchFlowExecution(t *testing.T) {
	batchFlow := pf.NewBatchFlow(simpleLogNode()) // Start node logs based on params

	batchFlow.SetPrepBatch(func(ctx pf.SharedContext, params pf.SharedContext) ([]pf.SharedContext, error) {
		// Generate parameter sets for each batch run
		return []pf.SharedContext{
			{"multiplier": 2},
			{"multiplier": 4},
		}, nil
	})

	batchFlow.SetPostBatch(func(ctx pf.SharedContext, batchPrepResult []pf.SharedContext, params pf.SharedContext) (string, error) {
		ctx["postBatchCalled"] = true
		assert.Len(t, batchPrepResult, 2, "PostBatch should receive the original prep result")
		return "batch_complete", nil
	})

	batchContext := make(pf.SharedContext)
	resultAction, err := batchFlow.Run(batchContext)
	require.NoError(t, err)

	assert.Equal(t, "batch_complete", resultAction)
	assert.True(t, batchContext["postBatchCalled"].(bool))

	// Check that the log messages were stored in the shared context by the simpleLogNode's PostFunc
	assert.Equal(t, "SimpleLogNode executed with multiplier: 2", batchContext["last_message_from_batch_2"])
	assert.Equal(t, "SimpleLogNode executed with multiplier: 4", batchContext["last_message_from_batch_4"])
}

// --- Additional Tests ---

func TestNodeRetrySuccess(t *testing.T) {
	execCount := 0
	node := pf.NewNode().
		SetRetry(3, 1*time.Millisecond).
		SetExec(func(prepResult any, params pf.SharedContext) (any, error) {
			execCount++
			if execCount < 3 {
				return nil, fmt.Errorf("temporary failure %d", execCount)
			}
			return "success", nil // Succeeds on 3rd try
		})

	_, err := node.Run(make(pf.SharedContext))
	require.NoError(t, err)
	assert.Equal(t, 3, execCount, "Exec should have been called 3 times")
}

func TestNodeRetryFailureWithFallback(t *testing.T) {
	execCount := 0
	fallbackCalled := false
	node := pf.NewNode().
		SetRetry(2, 1*time.Millisecond).
		SetExec(func(prepResult any, params pf.SharedContext) (any, error) {
			execCount++
			return nil, fmt.Errorf("permanent failure %d", execCount) // Always fail
		}).
		SetFallback(func(prepResult any, params pf.SharedContext, lastErr error) (any, error) {
			fallbackCalled = true
			assert.ErrorContains(t, lastErr, "permanent failure 2")
			return "fallback_success", nil // Fallback succeeds
		})

	action, err := node.Run(make(pf.SharedContext))
	require.NoError(t, err)
	assert.Equal(t, 2, execCount, "Exec should have been called 2 times")
	assert.True(t, fallbackCalled, "Fallback should have been called")
	assert.Equal(t, pf.DefaultAction, action) // Default post action
}

func TestNodeRetryFailureWithoutFallback(t *testing.T) {
	execCount := 0
	node := pf.NewNode().
		SetRetry(2, 1*time.Millisecond).
		SetExec(func(prepResult any, params pf.SharedContext) (any, error) {
			execCount++
			return nil, fmt.Errorf("permanent failure %d", execCount) // Always fail
		})
		// No fallback set

	_, err := node.Run(make(pf.SharedContext))
	require.Error(t, err)
	assert.ErrorContains(t, err, "Exec phase failed")
	assert.ErrorContains(t, err, "permanent failure 2") // Check cause
	assert.Equal(t, 2, execCount, "Exec should have been called 2 times")
}

func TestBatchNodeItemRetryAndFallback(t *testing.T) {
	itemExecCounts := make(map[string]int)
	itemFallbackCalled := make(map[string]bool)

	bnode := pf.NewBatchNode().
		SetRetry(3, 1*time.Millisecond). // Retries per item
		SetPrep(func(ctx pf.SharedContext, params pf.SharedContext) ([]any, error) {
			return []any{"ok", "fail_once", "fail_always"}, nil
		}).
		SetExecItem(func(item any, params pf.SharedContext) (any, error) {
			key := item.(string)
			itemExecCounts[key]++
			switch key {
			case "ok":
				return "OK_RES", nil
			case "fail_once":
				if itemExecCounts[key] < 2 {
					return nil, fmt.Errorf("temp fail %s", key)
				}
				return "FAIL_ONCE_RES", nil // Success on retry
			case "fail_always":
				return nil, fmt.Errorf("perm fail %s", key) // Always fail
			}
			return nil, fmt.Errorf("unknown item")
		}).
		SetItemFallback(func(item any, params pf.SharedContext, lastErr error) (any, error) {
			key := item.(string)
			if key == "fail_always" {
				itemFallbackCalled[key] = true
				assert.ErrorContains(t, lastErr, "perm fail fail_always")
				return "FAIL_ALWAYS_FALLBACK_RES", nil // Fallback success
			}
			// Fallback should not be called for others
			return nil, fmt.Errorf("unexpected fallback for %s", key)
		}).
		SetPost(func(ctx pf.SharedContext, prepResult []any, execResult []any, params pf.SharedContext) (string, error) {
			// Store results in context for assertion
			ctx["results"] = execResult
			return "batch_done", nil
		})

	ctx := make(pf.SharedContext)
	action, err := bnode.Run(ctx)

	require.NoError(t, err)
	assert.Equal(t, "batch_done", action)

	// Check execution counts
	assert.Equal(t, 1, itemExecCounts["ok"])
	assert.Equal(t, 2, itemExecCounts["fail_once"])
	assert.Equal(t, 3, itemExecCounts["fail_always"]) // All retries used

	// Check fallback calls
	assert.False(t, itemFallbackCalled["ok"])
	assert.False(t, itemFallbackCalled["fail_once"])
	assert.True(t, itemFallbackCalled["fail_always"])

	// Check final results passed to Post
	results := ctx["results"].([]any)
	require.Len(t, results, 3)
	assert.Equal(t, "OK_RES", results[0])
	assert.Equal(t, "FAIL_ONCE_RES", results[1])
	assert.Equal(t, "FAIL_ALWAYS_FALLBACK_RES", results[2])
}