package pocketflow_test

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	pf "github.com/The-Pocket/PocketFlow-Go"
)

// Pause one inherited lookup at the same points where a goroutine can be
// preempted around an ordinary context.WithCancel(root).Value call.
type pausedValueContext struct {
	context.Context
	started, read, captured, release chan struct{}
	paused                           int32
}

func (c *pausedValueContext) Value(key any) any {
	if key == "counter" && atomic.CompareAndSwapInt32(&c.paused, 0, 1) {
		close(c.started)
		<-c.read
		value := c.Context.Value(key)
		close(c.captured)
		<-c.release
		return value
	}
	return c.Context.Value(key)
}

func TestUpdateDoesNotRestoreDeletedParentValue(t *testing.T) {
	root := pf.NewContext(nil)
	parent, cancel := context.WithCancel(root)
	defer cancel()
	paused := &pausedValueContext{
		Context: parent, started: make(chan struct{}), read: make(chan struct{}),
		captured: make(chan struct{}), release: make(chan struct{}),
	}
	child := root.WithContext(paused)
	done := make(chan any, 1)
	var calls int32
	go func() {
		done <- child.Update("counter", func(value any) any {
			atomic.AddInt32(&calls, 1)
			previous, _ := value.(int)
			return previous + 1
		})
	}()
	await(t, paused.started)
	root.SetValue("counter", 10)
	close(paused.read)
	await(t, paused.captured)
	root.Delete("counter")
	close(paused.release)
	if got := await(t, done); got != 1 || root.Value("counter") != 1 {
		t.Fatalf("deleted value restored: result=%v store=%v", got, root.Snapshot())
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatal("Update must invoke the application callback exactly once")
	}
}

func TestUpdatePreservesInheritedValuesAndNilShadowing(t *testing.T) {
	type keyType string
	key := keyType("inherited")
	ctx := pf.NewContext(context.WithValue(context.Background(), key, 7))
	if got := ctx.Update(key, func(value any) any { return value.(int) + 1 }); got != 8 {
		t.Fatal(got)
	}
	ctx.SetValue(key, nil)
	if got := ctx.Update(key, func(value any) any {
		if value != nil {
			t.Fatal("stored nil did not shadow the parent")
		}
		return 1
	}); got != 1 {
		t.Fatal(got)
	}
	ctx.Delete(key)
	if got := ctx.Update(key, func(value any) any { return value.(int) + 1 }); got != 8 {
		t.Fatal("delete should restore inherited fallback", got)
	}
}

func TestRecoveredValuePanicReleasesStoreLock(t *testing.T) {
	ctx := pf.NewContext(nil)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected a panic for a non-comparable key")
			}
		}()
		ctx.Value([]int{1})
	}()
	done := make(chan struct{})
	go func() {
		ctx.SetValue("valid", 1)
		close(done)
	}()
	await(t, done)
	if ctx.Value("valid") != 1 {
		t.Fatal("store is unusable after recovering a Value panic")
	}
}

type adaptingNode struct {
	*pf.Node
	configured int
}

func (n *adaptingNode) Clone() pf.BaseNode {
	clone := *n
	clone.Node = n.Node.Clone().(*pf.Node)
	return &clone
}

func (n *adaptingNode) SetParams(params map[string]any) pf.BaseNode {
	n.configured++
	n.Node.SetParams(params)
	values := n.Node.GetParams()
	values["derived"] = values["base"].(int) + 1
	n.Node.SetParams(values)
	return n
}

func TestFlowCallsCustomSetParams(t *testing.T) {
	node := &adaptingNode{Node: pf.NewNode().SetExec(func(ctx *pf.PfContext, params map[string]any, _ any) (any, error) {
		ctx.SetValue("derived", params["derived"])
		ctx.SetValue("context_param", ctx.Params()["derived"])
		return nil, nil
	})}
	flow := pf.NewFlow(node)
	flow.SetParams(map[string]any{"base": 7})
	for run := 0; run < 2; run++ {
		ctx := shared()
		mustRun(t, flow, ctx)
		if ctx.Value("derived") != 8 || ctx.Value("context_param") != 8 {
			t.Fatal("custom SetParams was bypassed", ctx.Snapshot())
		}
	}
	if node.configured != 0 || len(node.GetParams()) != 0 || flow.GetParams()["derived"] != nil {
		t.Fatal("custom parameter setup mutated the graph")
	}
}

func boundedBatch(kind string, count, limit int, work func(*pf.PfContext) error, post func()) pf.BaseNode {
	if kind == "node" {
		return pf.NewParallelBatchNode().SetConcurrency(limit).
			SetPrep(func(*pf.PfContext, map[string]any) ([]any, error) { return make([]any, count), nil }).
			SetExecItem(func(ctx *pf.PfContext, _ map[string]any, _ any) (any, error) { return nil, work(ctx) }).
			SetPost(func(*pf.PfContext, map[string]any, []any, []any) (string, error) { post(); return "", nil })
	}
	step := pf.NewNode().SetExec(func(ctx *pf.PfContext, _ map[string]any, _ any) (any, error) { return nil, work(ctx) })
	return pf.NewParallelBatchFlow(step).SetConcurrency(limit).
		SetPrepBatch(func(*pf.PfContext, map[string]any) ([]map[string]any, error) {
			return make([]map[string]any, count), nil
		}).
		SetPostBatch(func(*pf.PfContext, map[string]any, []map[string]any) (string, error) { post(); return "", nil })
}

func TestBoundedBatchesLimitGoroutines(t *testing.T) {
	for _, kind := range []string{"node", "flow"} {
		t.Run(kind, func(t *testing.T) {
			const count, limit = 4000, 2
			parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			started, release := make(chan struct{}, count), make(chan struct{})
			var completed, posts int32
			runner := boundedBatch(kind, count, limit, func(ctx *pf.PfContext) error {
				started <- struct{}{}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-release:
					atomic.AddInt32(&completed, 1)
					return nil
				}
			}, func() { atomic.AddInt32(&posts, 1) })
			before := runtime.NumGoroutine()
			done := make(chan error, 1)
			go func() { _, err := runner.Run(pf.NewContext(parent)); done <- err }()
			for worker := 0; worker < limit; worker++ {
				await(t, started)
			}
			// Leave time for a faulty scheduler to spawn the remaining jobs while
			// callbacks stay blocked. Allow unrelated runtime goroutines ample room.
			peak := 0
			for until := time.Now().Add(100 * time.Millisecond); time.Now().Before(until); {
				if extra := runtime.NumGoroutine() - before; extra > peak {
					peak = extra
				}
				if peak > limit+32 {
					t.Fatalf("limit=%d, tasks=%d, extra goroutines=%d", limit, count, peak)
				}
				time.Sleep(time.Millisecond)
			}
			close(release)
			if err := await(t, done); err != nil {
				t.Fatal(err)
			}
			if completed != count || posts != 1 {
				t.Fatalf("completed=%d posts=%d", completed, posts)
			}
			t.Logf("tasks=%d, limit=%d, peak extra goroutines=%d", count, limit, peak)
		})
	}
}

func TestBoundedBatchesContinueQueuedJobsAfterError(t *testing.T) {
	for _, kind := range []string{"node", "flow"} {
		t.Run(kind, func(t *testing.T) {
			const count = 32
			parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			release, completed := make(chan struct{}), make(chan struct{}, count)
			failure := errors.New("first job failed")
			var calls, posts int32
			runner := boundedBatch(kind, count, 1, func(ctx *pf.PfContext) error {
				if atomic.AddInt32(&calls, 1) == 1 {
					return failure
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-release:
					completed <- struct{}{}
					return errors.New("later job failed")
				}
			}, func() { atomic.AddInt32(&posts, 1) })
			done := make(chan error, 1)
			go func() { _, err := runner.Run(pf.NewContext(parent)); done <- err }()
			if err := await(t, done); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			close(release)
			for job := 1; job < count; job++ {
				await(t, completed)
			}
			if atomic.LoadInt32(&calls) != count || atomic.LoadInt32(&posts) != 0 {
				t.Fatal("queued work was abandoned or post ran after a failure")
			}
		})
	}
}

func TestBoundedBatchesCancelQueuedJobs(t *testing.T) {
	for _, kind := range []string{"node", "flow"} {
		t.Run(kind, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls, posts int32
			runner := boundedBatch(kind, 4000, 1, func(*pf.PfContext) error {
				atomic.AddInt32(&calls, 1)
				cancel()
				return context.Canceled
			}, func() { atomic.AddInt32(&posts, 1) })
			_, err := runner.Run(pf.NewContext(parent))
			if !errors.Is(err, context.Canceled) || atomic.LoadInt32(&calls) != 1 || atomic.LoadInt32(&posts) != 0 {
				t.Fatalf("err=%v calls=%d posts=%d", err, atomic.LoadInt32(&calls), atomic.LoadInt32(&posts))
			}
		})
	}
}
