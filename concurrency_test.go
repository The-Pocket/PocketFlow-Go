package pocketflow_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pf "github.com/The-Pocket/PocketFlow-Go"
)

type runResult struct {
	action string
	err    error
}

func await[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case result := <-channel:
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("execution did not make progress")
	}
	var zero T
	return zero
}

func TestParallelBatchNodeOverlapsAndPreservesOrder(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx := pf.NewContext(parent)
	started := make(chan int, 3)
	release := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	node := pf.NewAsyncParallelBatchNode().SetPrep(func(*pf.PfContext, map[string]any) ([]any, error) {
		return []any{0, 1, 2}, nil
	}).SetExecItem(func(ctx *pf.PfContext, params map[string]any, item any) (any, error) {
		index := item.(int)
		params["item"] = index // Top-level item params must be isolated.
		started <- index
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release[index]:
		}
		if params["item"] != index {
			return nil, errors.New("item params leaked")
		}
		return index * 2, nil
	}).SetPost(func(ctx *pf.PfContext, _ map[string]any, _, results []any) (string, error) {
		ctx.SetValue("results", results)
		return "done", nil
	})
	finished := make(chan runResult, 1)
	go func() { action, err := node.RunAsync(ctx); finished <- runResult{action, err} }()
	for range release {
		await(t, started)
	}
	// All jobs have entered exec before any may finish: actual overlap.
	for index := len(release) - 1; index >= 0; index-- {
		close(release[index])
	}
	result := await(t, finished)
	if result.err != nil || result.action != "done" || !reflect.DeepEqual(ctx.Value("results"), []any{0, 2, 4}) {
		t.Fatalf("%+v %v", result, ctx.Snapshot())
	}
}

func TestParallelConcurrencyLimit(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, release := make(chan struct{}, 3), make(chan struct{}, 3)
	var active, maximum int32
	node := pf.NewParallelBatchNode().SetConcurrency(2).
		SetPrep(func(*pf.PfContext, map[string]any) ([]any, error) { return []any{1, 2, 3}, nil }).
		SetExecItem(func(ctx *pf.PfContext, _ map[string]any, item any) (any, error) {
			current := atomic.AddInt32(&active, 1)
			defer atomic.AddInt32(&active, -1)
			for old := atomic.LoadInt32(&maximum); current > old; old = atomic.LoadInt32(&maximum) {
				if atomic.CompareAndSwapInt32(&maximum, old, current) {
					break
				}
			}
			started <- struct{}{}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
				return item, nil
			}
		})
	finished := make(chan runResult, 1)
	go func() { action, err := node.Run(pf.NewContext(parent)); finished <- runResult{action, err} }()
	await(t, started)
	await(t, started)
	select {
	case <-started:
		t.Fatal("more than two items started")
	default:
	}
	release <- struct{}{}
	await(t, started)
	release <- struct{}{}
	release <- struct{}{}
	if result := await(t, finished); result.err != nil || atomic.LoadInt32(&maximum) != 2 {
		t.Fatal(result)
	}
}

func TestParallelItemRetryAndFallback(t *testing.T) {
	node := pf.NewParallelBatchNode().SetRetry(3, 0).
		SetPrep(func(*pf.PfContext, map[string]any) ([]any, error) { return []any{0, 1, 2}, nil }).
		SetExecItem(func(ctx *pf.PfContext, _ map[string]any, item any) (any, error) {
			index := item.(int)
			appendValue(ctx, fmt.Sprintf("attempts%d", index), ctx.RetryIndex())
			if index == 2 || index == 1 && ctx.RetryIndex() == 0 {
				return nil, errors.New("retry")
			}
			return index, nil
		}).SetItemFallback(func(ctx *pf.PfContext, _ map[string]any, item any, err error) (any, error) {
		if item != 2 || ctx.RetryIndex() != 2 || err == nil {
			return nil, errors.New("wrong fallback")
		}
		return 99, nil
	}).SetPost(func(ctx *pf.PfContext, _ map[string]any, _, results []any) (string, error) {
		ctx.SetValue("results", results)
		return "", nil
	})
	ctx := shared()
	mustRun(t, node, ctx)
	for key, want := range map[string][]any{"attempts0": {0}, "attempts1": {0, 1}, "attempts2": {0, 1, 2}, "results": {0, 1, 99}} {
		if !reflect.DeepEqual(ctx.Value(key), want) {
			t.Fatalf("%s=%v", key, ctx.Value(key))
		}
	}
}

func TestParallelFlowBranchesHaveIndependentParams(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, release := make(chan struct{}, 4), make(chan struct{})
	first := pf.NewNode().SetExec(func(ctx *pf.PfContext, params map[string]any, _ any) (any, error) {
		params["local"] = params["group"]
		return nil, nil
	})
	second := pf.NewNode().SetExec(func(ctx *pf.PfContext, params map[string]any, _ any) (any, error) {
		started <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
		if params["local"] != params["group"] {
			return nil, errors.New("branch params leaked")
		}
		ctx.SetValue(params["group"], params["local"])
		return nil, nil
	})
	first.Next("", second)
	flow := pf.NewAsyncParallelBatchFlow(first).SetPrepBatch(func(*pf.PfContext, map[string]any) ([]map[string]any, error) {
		return []map[string]any{{"group": "a"}, {"group": "b"}, {"group": "c"}, {"group": "d"}}, nil
	})
	ctx, finished := pf.NewContext(parent), make(chan runResult, 1)
	go func() { action, err := flow.RunAsync(ctx); finished <- runResult{action, err} }()
	for count := 0; count < 4; count++ {
		await(t, started)
	}
	close(release)
	if result := await(t, finished); result.err != nil {
		t.Fatal(result)
	}
	for _, group := range []string{"a", "b", "c", "d"} {
		if ctx.Value(group) != group {
			t.Fatal(ctx.Snapshot())
		}
	}
	if len(first.GetParams()) != 0 || len(second.GetParams()) != 0 {
		t.Fatal("mutated graph state")
	}
}

func TestSameGraphCanRunConcurrently(t *testing.T) {
	first := pf.NewNode().SetExec(func(ctx *pf.PfContext, params map[string]any, _ any) (any, error) {
		params["run"] = ctx.Value("input")
		return nil, nil
	})
	second := pf.NewNode().SetExec(func(ctx *pf.PfContext, params map[string]any, _ any) (any, error) {
		ctx.SetValue("output", params["run"])
		return nil, nil
	})
	first.Next("", second)
	flow := pf.NewFlow(first)
	flow.SetParams(map[string]any{"base": 1})
	errorsCh := make(chan error, 40)
	var workers sync.WaitGroup
	for index := 0; index < 40; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			ctx := pf.WithParam(context.Background(), map[any]any{"input": index})
			_, err := flow.Run(ctx)
			if err == nil && ctx.Value("output") != index {
				err = fmt.Errorf("run %d got %v", index, ctx.Value("output"))
			}
			errorsCh <- err
		}(index)
	}
	workers.Wait()
	for count := 0; count < 40; count++ {
		if err := <-errorsCh; err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(flow.GetParams(), map[string]any{"base": 1}) {
		t.Fatal(flow.GetParams())
	}
}

func TestGatherFirstErrorDoesNotCancelSibling(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan struct{}, 2)
	fail, release, siblingDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	failure := errors.New("first failure")
	var postCalls int32
	node := pf.NewParallelBatchNode().SetPrep(func(*pf.PfContext, map[string]any) ([]any, error) {
		return []any{0, 1}, nil
	}).SetExecItem(func(ctx *pf.PfContext, _ map[string]any, item any) (any, error) {
		started <- struct{}{}
		gate := release
		if item == 0 {
			gate = fail
		}
		select {
		case <-ctx.Done():
			if item == 1 {
				siblingDone <- ctx.Err()
			}
			return nil, ctx.Err()
		case <-gate:
		}
		if item == 0 {
			return nil, failure
		}
		siblingDone <- ctx.Err()
		return 1, nil
	}).SetPost(func(*pf.PfContext, map[string]any, []any, []any) (string, error) {
		atomic.AddInt32(&postCalls, 1)
		return "", nil
	})
	finished := make(chan runResult, 1)
	go func() { action, err := node.Run(pf.NewContext(parent)); finished <- runResult{action, err} }()
	await(t, started)
	await(t, started)
	close(fail)
	result := await(t, finished)
	if !errors.Is(result.err, failure) || atomic.LoadInt32(&postCalls) != 0 {
		t.Fatal(result)
	}
	// The sibling is still blocked, and the caller has already received the error.
	close(release)
	if err := await(t, siblingDone); err != nil {
		t.Fatalf("sibling was canceled: %v", err)
	}
}

func TestCancellationInterruptsRetryWait(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 1)
	var attempts, postCalls, fallbackCalls int32
	node := pf.NewNode().SetRetry(3, time.Hour).
		SetExec(func(*pf.PfContext, map[string]any, any) (any, error) {
			atomic.AddInt32(&attempts, 1)
			started <- struct{}{}
			return nil, errors.New("retry")
		}).SetFallback(func(*pf.PfContext, map[string]any, any, error) (any, error) {
		atomic.AddInt32(&fallbackCalls, 1)
		return nil, nil
	}).SetPost(func(*pf.PfContext, map[string]any, any, any) (string, error) {
		atomic.AddInt32(&postCalls, 1)
		return "", nil
	})
	finished := make(chan runResult, 1)
	go func() { action, err := node.Run(pf.NewContext(parent)); finished <- runResult{action, err} }()
	await(t, started)
	cancel()
	result := await(t, finished)
	if !errors.Is(result.err, context.Canceled) || atomic.LoadInt32(&attempts) != 1 || atomic.LoadInt32(&postCalls) != 0 || atomic.LoadInt32(&fallbackCalls) != 0 {
		t.Fatal(result)
	}
}

func TestCancellationStopsCycleAndCanceledEmptyRuns(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	node := pf.NewNode().SetExec(func(*pf.PfContext, map[string]any, any) (any, error) {
		calls++
		if calls == 3 {
			cancel()
		}
		return nil, nil
	})
	node.Next("", node)
	ctx := pf.NewContext(parent)
	if _, err := pf.NewFlow(node).Run(ctx); !errors.Is(err, context.Canceled) || calls != 3 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, canceled := range []context.Context{parent, expired} {
		for _, runner := range []pf.BaseNode{pf.NewNode(), pf.NewBatchNode(), pf.NewParallelBatchNode(), pf.NewFlow(nil), pf.NewBatchFlow(nil), pf.NewParallelBatchFlow(nil)} {
			if _, err := runner.Run(pf.NewContext(canceled)); !errors.Is(err, canceled.Err()) {
				t.Fatalf("%T: got %v, want %v", runner, err, canceled.Err())
			}
		}
	}
}

func TestCancellationStopsParallelCallbacks(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, finishedItems := make(chan struct{}, 2), make(chan struct{}, 2)
	node := pf.NewParallelBatchNode().SetPrep(func(*pf.PfContext, map[string]any) ([]any, error) { return []any{1, 2}, nil }).
		SetExecItem(func(ctx *pf.PfContext, _ map[string]any, _ any) (any, error) {
			started <- struct{}{}
			<-ctx.Done()
			finishedItems <- struct{}{}
			return nil, ctx.Err()
		})
	finished := make(chan error, 1)
	go func() { _, err := node.Run(pf.NewContext(parent)); finished <- err }()
	await(t, started)
	await(t, started)
	cancel()
	if err := await(t, finished); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	await(t, finishedItems)
	await(t, finishedItems)
}
