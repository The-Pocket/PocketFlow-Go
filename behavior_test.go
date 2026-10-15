package pocketflow_test

import (
	"bytes"
	"context"
	"errors"
	"log"
	"reflect"
	"strings"
	"testing"
	"time"

	pf "github.com/The-Pocket/PocketFlow-Go"
)

func shared() *pf.PfContext { return pf.NewContext(context.Background()) }

func mustRun(t *testing.T, node pf.BaseNode, ctx *pf.PfContext) string {
	t.Helper()
	action, err := node.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return action
}

func appendValue(ctx *pf.PfContext, key string, value any) {
	ctx.Update(key, func(previous any) any {
		values, _ := previous.([]any)
		return append(values, value)
	})
}

func signal(action string) *pf.Node {
	return pf.NewNode().SetPost(func(*pf.PfContext, map[string]any, any, any) (string, error) {
		return action, nil
	})
}

func TestLifecycleAndEmptyActions(t *testing.T) {
	var phases []string
	node := pf.NewNode().SetPrep(func(ctx *pf.PfContext, params map[string]any) (any, error) {
		phases = append(phases, "prep")
		return 3, nil
	}).SetExec(func(ctx *pf.PfContext, params map[string]any, prep any) (any, error) {
		phases = append(phases, "exec")
		return prep.(int) * 2, nil
	}).SetPost(func(ctx *pf.PfContext, params map[string]any, prep, result any) (string, error) {
		phases = append(phases, "post")
		if prep != 3 || result != 6 {
			t.Fatalf("wrong lifecycle values: %v %v", prep, result)
		}
		return "", nil
	})
	if action := mustRun(t, pf.NewFlow(node), shared()); action != "" {
		t.Fatalf("empty action was rewritten: %q", action)
	}
	if !reflect.DeepEqual(phases, []string{"prep", "exec", "post"}) {
		t.Fatal(phases)
	}
	if action := mustRun(t, pf.NewFlow(nil), shared()); action != "" {
		t.Fatal(action)
	}
	if action := mustRun(t, pf.NewNode(), shared()); action != "" {
		t.Fatal(action)
	}
}

func TestBranchingCyclesAndStartChaining(t *testing.T) {
	for _, initial := range []int{5, -5} {
		t.Run(map[bool]string{true: "positive", false: "negative"}[initial >= 0], func(t *testing.T) {
			ctx := pf.WithParam(context.Background(), map[any]any{"value": initial})
			check := pf.NewNode().SetPost(func(ctx *pf.PfContext, _ map[string]any, _, _ any) (string, error) {
				if ctx.Value("value").(int) >= 0 {
					return "positive", nil
				}
				return "negative", nil
			})
			subtract := pf.NewNode().SetExec(func(ctx *pf.PfContext, _ map[string]any, _ any) (any, error) {
				ctx.SetValue("value", ctx.Value("value").(int)-3)
				return nil, nil
			})
			check.Next("positive", subtract).Next("", check)
			check.Next("negative", signal("finished"))
			flow := pf.NewFlow(nil)
			flow.Start(pf.NewNode()).Next("", check)
			if mustRun(t, flow, ctx) != "finished" {
				t.Fatal("wrong terminal action")
			}
			want := initial
			for want >= 0 {
				want -= 3
			}
			if ctx.Value("value") != want {
				t.Fatal(ctx.Snapshot())
			}
		})
	}
}

func TestEveryStepReceivesCurrentParams(t *testing.T) {
	first := pf.NewNode()
	second := pf.NewNode().SetExec(func(ctx *pf.PfContext, params map[string]any, _ any) (any, error) {
		appendValue(ctx, "seen", params["version"])
		if !reflect.DeepEqual(ctx.Params(), params) {
			t.Fatal("execution context disagrees with callback params")
		}
		return nil, nil
	})
	second.SetParams(map[string]any{"version": -1})
	first.Next("", second)
	flow, ctx := pf.NewFlow(first), shared()
	for _, version := range []int{1, 2} {
		flow.SetParams(map[string]any{"version": version})
		mustRun(t, flow, ctx)
	}
	if !reflect.DeepEqual(ctx.Value("seen"), []any{1, 2}) {
		t.Fatal(ctx.Snapshot())
	}
	if second.GetParams()["version"] != -1 {
		t.Fatal("execution mutated graph params")
	}
}

func TestSequentialParamMutationAndNestedIsolation(t *testing.T) {
	mutate := pf.NewNode().SetExec(func(ctx *pf.PfContext, params map[string]any, _ any) (any, error) {
		params["version"] = 9
		return nil, nil
	})
	capture := pf.NewNode().SetExec(func(ctx *pf.PfContext, params map[string]any, _ any) (any, error) {
		appendValue(ctx, "seen", params["version"])
		return nil, nil
	})
	mutate.Next("", capture)
	inner := pf.NewFlow(mutate)
	inner.Next("", capture)
	outer, ctx := pf.NewFlow(inner), shared()
	outer.SetParams(map[string]any{"version": 1})
	mustRun(t, outer, ctx)
	if !reflect.DeepEqual(ctx.Value("seen"), []any{9, 1}) {
		t.Fatal(ctx.Snapshot())
	}
	if outer.GetParams()["version"] != 1 {
		t.Fatal("mutated outer definition")
	}
}

func TestNestedFlowHooksAndActionPropagation(t *testing.T) {
	ctx := shared()
	inner := pf.NewFlow(signal("inner_done")).SetPrep(func(ctx *pf.PfContext, _ map[string]any) (any, error) {
		appendValue(ctx, "phases", "inner_prep")
		return "prepared", nil
	}).SetPost(func(ctx *pf.PfContext, _ map[string]any, prep, result any) (string, error) {
		if prep != "prepared" || result != "inner_done" {
			t.Fatalf("flow hooks got %v/%v", prep, result)
		}
		appendValue(ctx, "phases", "inner_post")
		return "approved", nil
	})
	inner.Next("approved", signal("outer_done"))
	inner.Next("rejected", signal("wrong"))
	outer := pf.NewFlow(pf.NewFlow(inner))
	if mustRun(t, outer, ctx) != "outer_done" {
		t.Fatal("subflow action did not reach outer flow")
	}
	if !reflect.DeepEqual(ctx.Value("phases"), []any{"inner_prep", "inner_post"}) {
		t.Fatal(ctx.Snapshot())
	}
}

func TestBatchParamsMergeAndPropagation(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(map[bool]string{false: "sequential", true: "parallel"}[parallel], func(t *testing.T) {
			second := pf.NewNode().SetExec(func(ctx *pf.PfContext, params map[string]any, _ any) (any, error) {
				ctx.SetValue(params["key"], params["base"].(int)*params["multiplier"].(int))
				return nil, nil
			})
			first := pf.NewNode()
			first.Next("", second)
			var flow *pf.BatchFlow
			if parallel {
				flow = pf.NewParallelBatchFlow(first)
			} else {
				flow = pf.NewBatchFlow(first)
			}
			if returned := flow.SetParams(map[string]any{"base": 2, "multiplier": 100}); returned != flow {
				t.Fatal("SetParams lost batch identity")
			}
			flow.SetPrepBatch(func(*pf.PfContext, map[string]any) ([]map[string]any, error) {
				return []map[string]any{{"key": "a", "multiplier": 3}, {"key": "b", "multiplier": 4}}, nil
			}).SetPost(func(ctx *pf.PfContext, _ map[string]any, prep, result any) (string, error) {
				if len(prep.([]map[string]any)) != 2 || result != nil {
					t.Fatalf("batch post got %v/%v", prep, result)
				}
				return "batches_done", nil
			})
			flow.Next("batches_done", signal("finished"))
			ctx := shared()
			outer := pf.NewFlow(flow)
			outer.SetParams(map[string]any{"base": 2, "multiplier": 100})
			if mustRun(t, outer, ctx) != "finished" || ctx.Value("a") != 6 || ctx.Value("b") != 8 {
				t.Fatal(ctx.Snapshot())
			}
		})
	}
}

func TestNestedBatchFlows(t *testing.T) {
	for _, outerParallel := range []bool{false, true} {
		for _, innerParallel := range []bool{false, true} {
			name := map[bool]string{false: "sequential", true: "parallel"}
			t.Run(name[outerParallel]+"/"+name[innerParallel], func(t *testing.T) {
				item := pf.NewNode().SetExec(func(ctx *pf.PfContext, params map[string]any, _ any) (any, error) {
					group, value := params["group"].(string), params["item"].(int)
					appendValue(ctx, group, value*2)
					return nil, nil
				})
				inner := pf.NewBatchFlow(item)
				if innerParallel {
					inner = pf.NewParallelBatchFlow(item)
				}
				inner.SetPrepBatch(func(ctx *pf.PfContext, params map[string]any) ([]map[string]any, error) {
					values := map[string][]int{"A": {1, 2}, "B": {3, 4}}[params["group"].(string)]
					return []map[string]any{{"item": values[0]}, {"item": values[1]}}, nil
				})
				outer := pf.NewBatchFlow(inner)
				if outerParallel {
					outer = pf.NewParallelBatchFlow(inner)
				}
				outer.SetPrepBatch(func(*pf.PfContext, map[string]any) ([]map[string]any, error) {
					return []map[string]any{{"group": "A"}, {"group": "B"}}, nil
				})
				ctx := shared()
				mustRun(t, outer, ctx)
				for group, expected := range map[string]int{"A": 6, "B": 14} {
					values := ctx.Value(group).([]any)
					if len(values) != 2 || values[0].(int)+values[1].(int) != expected {
						t.Fatal(ctx.Snapshot())
					}
				}
			})
		}
	}
}

func TestRetryIndexFallbackAndErrorWrapping(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "fallback"}[fallback], func(t *testing.T) {
			failure := errors.New("upstream unavailable")
			var attempts []int
			prepCalls, postCalls := 0, 0
			node := pf.NewNode().SetRetry(3, 0).SetPrep(func(*pf.PfContext, map[string]any) (any, error) {
				prepCalls++
				return "input", nil
			}).SetExec(func(ctx *pf.PfContext, _ map[string]any, prep any) (any, error) {
				attempts = append(attempts, ctx.RetryIndex())
				return nil, failure
			}).SetPost(func(ctx *pf.PfContext, _ map[string]any, _, result any) (string, error) {
				postCalls++
				if result != "recovered" || ctx.RetryIndex() != 2 {
					t.Fatal("fallback did not feed post")
				}
				return "recovered", nil
			})
			if fallback {
				node.SetFallback(func(ctx *pf.PfContext, _ map[string]any, prep any, err error) (any, error) {
					if ctx.RetryIndex() != 2 || prep != "input" || !errors.Is(err, failure) {
						t.Fatal("wrong fallback arguments")
					}
					return "recovered", nil
				})
			}
			action, err := pf.NewFlow(node).Run(shared())
			if !reflect.DeepEqual(attempts, []int{0, 1, 2}) || prepCalls != 1 {
				t.Fatalf("attempts %v; prep calls %d", attempts, prepCalls)
			}
			if fallback {
				if err != nil || action != "recovered" || postCalls != 1 {
					t.Fatalf("%q %v, post=%d", action, err, postCalls)
				}
			} else {
				var wrapped *pf.PocketFlowError
				if !errors.Is(err, failure) || !errors.As(err, &wrapped) || postCalls != 0 {
					t.Fatalf("lost failure or ran post: %v", err)
				}
			}
		})
	}
}

func TestPrepAndPostFailuresAreNotRetried(t *testing.T) {
	for _, phase := range []string{"prep", "post", "fallback"} {
		t.Run(phase, func(t *testing.T) {
			failure := errors.New(phase)
			prepCalls, execCalls, postCalls, fallbackCalls := 0, 0, 0, 0
			node := pf.NewNode().SetRetry(3, 0).SetPrep(func(*pf.PfContext, map[string]any) (any, error) {
				prepCalls++
				if phase == "prep" {
					return nil, failure
				}
				return nil, nil
			}).SetExec(func(*pf.PfContext, map[string]any, any) (any, error) {
				execCalls++
				if phase == "fallback" {
					return nil, failure
				}
				return nil, nil
			}).SetPost(func(*pf.PfContext, map[string]any, any, any) (string, error) {
				postCalls++
				return "", failure
			}).SetFallback(func(*pf.PfContext, map[string]any, any, error) (any, error) {
				fallbackCalls++
				return nil, failure
			})
			_, err := node.Run(shared())
			if !errors.Is(err, failure) || prepCalls != 1 {
				t.Fatal(err)
			}
			wantExec, wantPost, wantFallback := 0, 0, 0
			if phase == "post" {
				wantExec, wantPost = 1, 1
			}
			if phase == "fallback" {
				wantExec, wantFallback = 3, 1
			}
			if execCalls != wantExec || postCalls != wantPost || fallbackCalls != wantFallback {
				t.Fatalf("exec/post/fallback=%d/%d/%d", execCalls, postCalls, fallbackCalls)
			}
		})
	}
}

func TestBatchFlowErrorsIdentifyGroup(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(map[bool]string{false: "sequential", true: "parallel"}[parallel], func(t *testing.T) {
			failure := errors.New("upstream unavailable")
			node := pf.NewNode().SetExec(func(_ *pf.PfContext, params map[string]any, _ any) (any, error) {
				if params["group"] == "b" {
					return nil, failure
				}
				return nil, nil
			})
			flow := pf.NewBatchFlow(node)
			if parallel {
				flow = pf.NewParallelBatchFlow(node)
			}
			flow.SetPrepBatch(func(*pf.PfContext, map[string]any) ([]map[string]any, error) {
				return []map[string]any{{"group": "a"}, {"group": "b"}}, nil
			})
			_, err := flow.Run(shared())
			if !errors.Is(err, failure) || !strings.Contains(err.Error(), "batch item 1") {
				t.Fatalf("missing batch index or error cause: %v", err)
			}
		})
	}
}

func TestFallbackErrorsIdentifyPhase(t *testing.T) {
	for _, kind := range []string{"node", "batch", "parallel"} {
		t.Run(kind, func(t *testing.T) {
			execFailure, fallbackFailure := errors.New("exec failed"), errors.New("recovery unavailable")
			exec := func(*pf.PfContext, map[string]any, any) (any, error) { return nil, execFailure }
			fallback := func(_ *pf.PfContext, _ map[string]any, _ any, err error) (any, error) {
				if !errors.Is(err, execFailure) {
					t.Errorf("fallback received wrong cause: %v", err)
				}
				return nil, fallbackFailure
			}
			var runner pf.BaseNode
			if kind == "node" {
				runner = pf.NewNode().SetRetry(2, 0).SetExec(exec).SetFallback(fallback)
			} else {
				batch := pf.NewBatchNode()
				if kind == "parallel" {
					batch = pf.NewParallelBatchNode()
				}
				runner = batch.SetRetry(2, 0).
					SetPrep(func(*pf.PfContext, map[string]any) ([]any, error) { return []any{"item"}, nil }).
					SetExecItem(exec).SetItemFallback(fallback)
			}
			_, err := runner.Run(shared())
			var detail *pf.PocketFlowError
			if !errors.Is(err, fallbackFailure) || !errors.As(err, &detail) ||
				!strings.Contains(err.Error(), "Fallback failed after 2 attempts") {
				t.Fatalf("missing fallback phase, attempt count or error cause: %v", err)
			}
		})
	}
}

func TestEmptyAndInvalidBatches(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(map[bool]string{false: "sequential", true: "parallel"}[parallel], func(t *testing.T) {
			node := pf.NewBatchNode()
			flow := pf.NewBatchFlow(nil)
			if parallel {
				node, flow = pf.NewParallelBatchNode(), pf.NewParallelBatchFlow(nil)
			}
			for _, empty := range [][]any{nil, {}} {
				postCalled := false
				node.SetPrep(func(*pf.PfContext, map[string]any) ([]any, error) { return empty, nil }).
					SetPost(func(_ *pf.PfContext, _ map[string]any, items, results []any) (string, error) {
						postCalled = true
						if len(items) != 0 || len(results) != 0 {
							t.Fatal(items, results)
						}
						return "", nil
					})
				mustRun(t, node, shared())
				if !postCalled {
					t.Fatal("empty batch skipped post")
				}
			}
			mustRun(t, flow, shared())
			if _, err := node.Exec(shared(), "wrong type"); err == nil {
				t.Fatal("accepted invalid items")
			}
			flow.SetPrep(func(*pf.PfContext, map[string]any) (any, error) { return "wrong type", nil })
			if _, err := flow.Run(shared()); err == nil {
				t.Fatal("accepted invalid batch params")
			}
		})
	}
}

func TestWarningsAndStandaloneSuccessors(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	called := false
	next := pf.NewNode().SetExec(func(*pf.PfContext, map[string]any, any) (any, error) { called = true; return nil, nil })
	node := pf.NewNode()
	node.Next("", next)
	mustRun(t, node, shared())
	if called || !strings.Contains(output.String(), "Use Flow") {
		t.Fatal(output.String())
	}
	output.Reset()
	node.Next("", next)
	if !strings.Contains(output.String(), "Overwriting") {
		t.Fatal(output.String())
	}
	output.Reset()
	node.SetPost(func(*pf.PfContext, map[string]any, any, any) (string, error) { return "missing", nil })
	if mustRun(t, pf.NewFlow(node), shared()) != "missing" || !strings.Contains(output.String(), "not found") {
		t.Fatal(output.String())
	}
}

func TestOptionValidation(t *testing.T) {
	for name, configure := range map[string]func(){
		"node attempts":    func() { pf.NewNode().SetRetry(0, 0) },
		"node wait":        func() { pf.NewNode().SetRetry(1, -time.Second) },
		"batch attempts":   func() { pf.NewBatchNode().SetRetry(0, 0) },
		"batch wait":       func() { pf.NewBatchNode().SetRetry(1, -time.Second) },
		"node concurrency": func() { pf.NewParallelBatchNode().SetConcurrency(-1) },
		"flow concurrency": func() { pf.NewParallelBatchFlow(nil).SetConcurrency(-1) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid option was accepted")
				}
			}()
			configure()
		})
	}
}

func TestAsyncConstructorsAndMixedFlows(t *testing.T) {
	types := []interface {
		RunAsync(*pf.PfContext) (string, error)
	}{
		pf.NewAsyncNode(), pf.NewAsyncBatchNode(), pf.NewAsyncParallelBatchNode(),
		pf.NewAsyncFlow(nil), pf.NewAsyncBatchFlow(nil), pf.NewAsyncParallelBatchFlow(nil),
	}
	for _, node := range types {
		if action, err := node.RunAsync(shared()); action != "" || err != nil {
			t.Fatalf("%q %v", action, err)
		}
	}
	first := pf.NewAsyncNode()
	first.Next("", signal("done"))
	if action, err := pf.NewAsyncFlow(first).RunAsync(shared()); action != "done" || err != nil {
		t.Fatalf("%q %v", action, err)
	}
}
