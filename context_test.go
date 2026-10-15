package pocketflow_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	pf "github.com/The-Pocket/PocketFlow-Go"
)

func TestSharedStoreParentSnapshotAndUpdate(t *testing.T) {
	type keyType string
	parentKey := keyType("parent")
	parent := context.WithValue(context.Background(), parentKey, "inherited")
	initial := map[any]any{"value": 1}
	ctx := pf.WithParam(parent, initial)
	initial["value"] = 99
	if ctx.Value("value") != 1 || ctx.Value(parentKey) != "inherited" {
		t.Fatal(ctx.Snapshot())
	}
	ctx.SetValue(parentKey, nil)
	if ctx.Value(parentKey) != nil {
		t.Fatal("nil did not shadow parent")
	}
	ctx.Delete(parentKey)
	if ctx.Value(parentKey) != "inherited" {
		t.Fatal("delete did not restore fallback")
	}
	ctx.Update("value", func(value any) any { return value.(int) + 1 })
	snapshot := ctx.Snapshot()
	snapshot["value"] = 0
	if ctx.Value("value") != 2 {
		t.Fatal("snapshot mutated shared store")
	}
	child := ctx.WithContext(context.Background())
	child.SetValue("value", 3)
	if ctx.Value("value") != 3 {
		t.Fatal("derived context lost shared store")
	}
}

func TestUpdateWithParentWrappingSameSharedStore(t *testing.T) {
	ctx := shared()
	parent, cancel := context.WithCancel(ctx)
	defer cancel()
	child := ctx.WithContext(parent)
	updated := make(chan any, 1)
	go func() {
		updated <- child.Update("new", func(any) any { return 1 })
	}()
	if await(t, updated) != 1 || ctx.Value("new") != 1 {
		t.Fatal("derived update lost shared state")
	}
	cancel()
	if !errors.Is(child.Err(), context.Canceled) {
		t.Fatal(child.Err())
	}
}

func TestAtomicUpdatesAcrossGoroutines(t *testing.T) {
	ctx := pf.WithParam(nil, map[any]any{"count": 0})
	var workers sync.WaitGroup
	for index := 0; index < 40; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for count := 0; count < 100; count++ {
				ctx.Update("count", func(value any) any { return value.(int) + 1 })
			}
		}()
	}
	workers.Wait()
	if ctx.Value("count") != 4000 {
		t.Fatal(ctx.Value("count"))
	}
}

func TestZeroContextAndUpdatePanicRelease(t *testing.T) {
	ctx := &pf.PfContext{}
	ctx.SetValue("key", 1)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("missing panic")
			}
		}()
		ctx.Update("key", func(any) any { panic("application failure") })
	}()
	ctx.SetValue("key", 2)
	if ctx.Value("key") != 2 || ctx.Err() != nil {
		t.Fatal(ctx.Snapshot())
	}
	if _, err := pf.NewNode().Run(nil); err != nil {
		t.Fatal(err)
	}
}

type customNode struct {
	*pf.Node
	executions int
}

func (n *customNode) Clone() pf.BaseNode {
	clone := *n
	clone.Node = n.Node.Clone().(*pf.Node)
	return &clone
}

func (n *customNode) InternalRun(ctx *pf.PfContext) (string, error) {
	n.executions++
	ctx.SetValue("custom", n.executions)
	return n.Node.InternalRun(ctx)
}

type nonCloneableNode struct{ pf.BaseNode }

func TestCustomNodeCloningContract(t *testing.T) {
	node := &customNode{Node: pf.NewNode()}
	ctx := shared()
	mustRun(t, pf.NewFlow(node), ctx)
	mustRun(t, pf.NewFlow(node), ctx)
	if ctx.Value("custom") != 1 || node.executions != 0 {
		t.Fatal("custom clone lost concrete behavior or mutated the graph")
	}
	wrapped := &nonCloneableNode{BaseNode: pf.NewNode()}
	_, err := pf.NewFlow(wrapped).Run(shared())
	var detail *pf.PocketFlowError
	if err == nil || !errors.As(err, &detail) {
		t.Fatal("missing clone should produce an actionable error")
	}
}

func TestConfiguredParamsAndSuccessorsAreSnapshots(t *testing.T) {
	configured := map[string]any{"key": 1}
	node := pf.NewNode()
	node.SetParams(configured)
	configured["key"] = 2
	params := node.GetParams()
	params["key"] = 3
	if node.GetParams()["key"] != 1 {
		t.Fatal(node.GetParams())
	}
	next := pf.NewNode()
	node.Next("", next)
	edges := node.GetSuccessors()
	delete(edges, pf.DefaultAction)
	if node.GetNextNode("") != next {
		t.Fatal("successor snapshot mutated graph")
	}
}
