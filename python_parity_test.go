package pocketflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	pf "github.com/The-Pocket/PocketFlow-Go"
)

type parityOutcome struct {
	Action string         `json:"action"`
	Error  bool           `json:"error"`
	State  map[string]any `json:"state"`
}

func finishParity(node pf.BaseNode, ctx *pf.PfContext) parityOutcome {
	action, err := node.Run(ctx)
	state := make(map[string]any)
	for key, value := range ctx.Snapshot() {
		state[key.(string)] = value
	}
	return parityOutcome{Action: action, Error: err != nil, State: state}
}

func parityNode(mode string) *pf.Node {
	if mode == "sync" {
		return pf.NewNode()
	}
	return pf.NewAsyncNode()
}

func parityFlow(mode string, start pf.BaseNode) *pf.Flow {
	if mode == "sync" {
		return pf.NewFlow(start)
	}
	return pf.NewAsyncFlow(start)
}

func parityBatchFlow(mode string, start pf.BaseNode) *pf.BatchFlow {
	switch mode {
	case "sync":
		return pf.NewBatchFlow(start)
	case "async":
		return pf.NewAsyncBatchFlow(start)
	default:
		return pf.NewAsyncParallelBatchFlow(start)
	}
}

func goParityOutcomes() map[string]parityOutcome {
	outcomes := make(map[string]parityOutcome)
	for _, mode := range []string{"sync", "async"} {
		ctx := pf.WithParam(nil, map[any]any{"value": 5})
		a := parityNode(mode).SetExec(func(ctx *pf.PfContext, _ map[string]any, _ any) (any, error) {
			ctx.SetValue("value", ctx.Value("value").(int)+3)
			return nil, nil
		})
		b := parityNode(mode).SetExec(func(ctx *pf.PfContext, _ map[string]any, _ any) (any, error) {
			ctx.SetValue("value", ctx.Value("value").(int)*2)
			return nil, nil
		})
		a.Next("", b)
		outcomes["sequence/"+mode] = finishParity(parityFlow(mode, a), ctx)

		ctx = pf.WithParam(nil, map[any]any{"value": 5})
		check := parityNode(mode).SetPost(func(ctx *pf.PfContext, _ map[string]any, _, _ any) (string, error) {
			if ctx.Value("value").(int) >= 0 {
				return "positive", nil
			}
			return "negative", nil
		})
		subtract := parityNode(mode).SetExec(func(ctx *pf.PfContext, _ map[string]any, _ any) (any, error) {
			ctx.SetValue("value", ctx.Value("value").(int)-3)
			return nil, nil
		})
		check.Next("positive", subtract).Next("", check)
		check.Next("negative", signal("finished"))
		outcomes["cycle/"+mode] = finishParity(parityFlow(mode, check), ctx)

		inner := parityFlow(mode, signal("inner_done"))
		inner.Next("inner_done", parityNode(mode).SetExec(func(ctx *pf.PfContext, _ map[string]any, _ any) (any, error) {
			ctx.SetValue("path", "B")
			return nil, nil
		}).SetPost(func(*pf.PfContext, map[string]any, any, any) (string, error) { return "outer_done", nil }))
		inner.Next("other", parityNode(mode).SetExec(func(ctx *pf.PfContext, _ map[string]any, _ any) (any, error) {
			ctx.SetValue("path", "A")
			return nil, nil
		}))
		outcomes["nested-flow/"+mode] = finishParity(parityFlow(mode, parityFlow(mode, inner)), shared())

		ctx = pf.WithParam(nil, map[any]any{"seen": []any{}})
		mutate := parityNode(mode).SetExec(func(ctx *pf.PfContext, params map[string]any, _ any) (any, error) {
			params["version"] = 9
			return nil, nil
		})
		capture := parityNode(mode).SetExec(func(ctx *pf.PfContext, params map[string]any, _ any) (any, error) {
			appendValue(ctx, "seen", params["version"])
			return nil, nil
		})
		mutate.Next("", capture)
		inner = parityFlow(mode, mutate)
		inner.Next("", capture)
		outer := parityFlow(mode, inner)
		outer.SetParams(map[string]any{"version": 1})
		outcomes["param-mutation/"+mode] = finishParity(outer, ctx)

		child := parityNode(mode).SetExec(func(ctx *pf.PfContext, params map[string]any, _ any) (any, error) {
			ctx.SetValue("node_param", params["value"])
			return nil, nil
		}).SetPost(func(*pf.PfContext, map[string]any, any, any) (string, error) { return "done", nil })
		flow := parityFlow(mode, child).SetPrep(func(*pf.PfContext, map[string]any) (any, error) { return map[string]any{"value": 999}, nil }).
			SetPost(func(ctx *pf.PfContext, _ map[string]any, prep, result any) (string, error) {
				ctx.SetValue("prepared", prep.(map[string]any)["value"])
				ctx.SetValue("last_action", result)
				return "hooks_done", nil
			})
		flow.SetParams(map[string]any{"value": 3})
		outcomes["flow-hooks/"+mode] = finishParity(flow, shared())
		outcomes["empty-flow/"+mode] = finishParity(parityFlow(mode, nil), shared())

		for _, behavior := range []string{"success", "fallback", "failure", "prep-failure", "post-failure"} {
			ctx = pf.WithParam(nil, map[any]any{"attempts": []any{}, "prep_calls": 0, "post_calls": 0})
			node := parityNode(mode).SetRetry(3, 0).SetPrep(func(ctx *pf.PfContext, _ map[string]any) (any, error) {
				ctx.SetValue("prep_calls", ctx.Value("prep_calls").(int)+1)
				if behavior == "prep-failure" {
					return nil, errors.New("prep")
				}
				return 7, nil
			}).SetExec(func(ctx *pf.PfContext, _ map[string]any, prep any) (any, error) {
				appendValue(ctx, "attempts", ctx.RetryIndex())
				if behavior == "fallback" || behavior == "failure" || behavior == "success" && ctx.RetryIndex() < 2 {
					return nil, errors.New("exec")
				}
				return prep, nil
			}).SetPost(func(ctx *pf.PfContext, _ map[string]any, _, result any) (string, error) {
				ctx.SetValue("post_calls", ctx.Value("post_calls").(int)+1)
				if behavior == "post-failure" {
					return "", errors.New("post")
				}
				ctx.SetValue("result", result)
				return "done", nil
			})
			if behavior == "fallback" {
				node.SetFallback(func(*pf.PfContext, map[string]any, any, error) (any, error) { return 99, nil })
			}
			outcomes["retry-"+behavior+"/"+mode] = finishParity(node, ctx)
		}
	}
	for _, mode := range []string{"sync", "async", "parallel"} {
		for _, empty := range []bool{false, true} {
			node := pf.NewBatchNode()
			if mode == "async" {
				node = pf.NewAsyncBatchNode()
			}
			if mode == "parallel" {
				node = pf.NewAsyncParallelBatchNode()
			}
			node.SetPrep(func(*pf.PfContext, map[string]any) ([]any, error) {
				if empty {
					return []any{}, nil
				}
				return []any{1, 2, 3}, nil
			}).
				SetExecItem(func(_ *pf.PfContext, _ map[string]any, item any) (any, error) { return item.(int) * 2, nil }).
				SetPost(func(ctx *pf.PfContext, _ map[string]any, _, results []any) (string, error) {
					ctx.SetValue("results", results)
					return "", nil
				})
			name := "batch-node/"
			if empty {
				name = "batch-node-empty/"
			}
			outcomes[name+mode] = finishParity(node, shared())
		}
		first := parityNode(mode)
		second := parityNode(mode).SetExec(func(ctx *pf.PfContext, params map[string]any, _ any) (any, error) {
			ctx.SetValue(params["key"], params["base"].(int)*params["multiplier"].(int))
			return nil, nil
		})
		first.Next("", second)
		batch := parityBatchFlow(mode, first).SetPrepBatch(func(*pf.PfContext, map[string]any) ([]map[string]any, error) {
			return []map[string]any{{"key": "a", "multiplier": 3}, {"key": "b", "multiplier": 4}}, nil
		})
		batch.SetParams(map[string]any{"base": 2, "multiplier": 100})
		outcomes["batch-flow/"+mode] = finishParity(batch, shared())

		item := parityNode(mode).SetExec(func(ctx *pf.PfContext, params map[string]any, _ any) (any, error) {
			appendValue(ctx, params["group"].(string), params["item"].(int)*2)
			return nil, nil
		})
		inner := parityBatchFlow(mode, item).SetPrepBatch(func(ctx *pf.PfContext, params map[string]any) ([]map[string]any, error) {
			values := map[string][]int{"A": {1, 2}, "B": {3, 4}}[params["group"].(string)]
			return []map[string]any{{"item": values[0]}, {"item": values[1]}}, nil
		})
		outer := parityBatchFlow(mode, inner).SetPrepBatch(func(*pf.PfContext, map[string]any) ([]map[string]any, error) {
			return []map[string]any{{"group": "A"}, {"group": "B"}}, nil
		})
		outcome := finishParity(outer, pf.WithParam(nil, map[any]any{"A": []any{}, "B": []any{}}))
		for _, group := range []string{"A", "B"} {
			values := outcome.State[group].([]any)
			sort.Slice(values, func(i, j int) bool { return values[i].(int) < values[j].(int) })
		}
		outcomes["nested-batch/"+mode] = outcome
	}
	return outcomes
}

func TestPythonCoreParity(t *testing.T) {
	python := os.Getenv("POCKETFLOW_PYTHON")
	if python == "" {
		python = "python"
	}
	if _, err := exec.LookPath(python); err != nil {
		if os.Getenv("POCKETFLOW_PYTHON") != "" || os.Getenv("POCKETFLOW_PYTHON_PATH") != "" {
			t.Fatalf("required Python reference interpreter is unavailable: %v", err)
		}
		t.Skip("Python is unavailable; set POCKETFLOW_PYTHON to enable reference tests")
	}
	reference := os.Getenv("POCKETFLOW_PYTHON_PATH")
	if reference == "" {
		for _, candidate := range []string{".python-reference", "../PockerFlowPython", "../PocketFlowPython"} {
			if _, err := os.Stat(filepath.Join(candidate, "pocketflow", "__init__.py")); err == nil {
				reference = candidate
				break
			}
		}
	}
	if reference == "" {
		t.Skip("set POCKETFLOW_PYTHON_PATH to a Python PocketFlow checkout")
	}
	if _, err := os.Stat(filepath.Join(reference, "pocketflow", "__init__.py")); err != nil {
		t.Fatalf("invalid Python reference checkout: %v", err)
	}
	deadline, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(deadline, python, "-B", "tools/python_reference.py", reference)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Python reference failed: %v\n%s", err, output)
	}
	var expected map[string]any
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatalf("invalid reference output: %v\n%s", err, output)
	}
	encoded, err := json.Marshal(goParityOutcomes())
	if err != nil {
		t.Fatal(err)
	}
	var actual map[string]any
	if err := json.Unmarshal(encoded, &actual); err != nil {
		t.Fatal(err)
	}
	if len(actual) != len(expected) {
		t.Fatalf("scenario counts differ: Go=%d Python=%d", len(actual), len(expected))
	}
	for name, want := range expected {
		t.Run(name, func(t *testing.T) {
			if !reflect.DeepEqual(actual[name], want) {
				t.Fatalf("Go: %#v\nPython: %#v", actual[name], want)
			}
		})
	}
}

func TestPythonReferenceRejectsMissingExplicitInterpreter(t *testing.T) {
	t.Setenv("POCKETFLOW_PYTHON", filepath.Join(t.TempDir(), "missing-python"))
	t.Setenv("POCKETFLOW_PYTHON_PATH", "")
	deadline, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(deadline, os.Args[0], "-test.run=^TestPythonCoreParity$", "-test.v")
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "required Python reference interpreter is unavailable") {
		t.Fatalf("invalid explicit interpreter must fail, not skip: %v\n%s", err, output)
	}
}
