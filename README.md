# PocketFlow Go

A small workflow framework with the core capabilities of
[Python PocketFlow](https://github.com/The-Pocket/PocketFlow): nodes, action
routing, retries, fallback, nested flows, sequential batches, and parallel
batches. The core uses only the Go standard library.

Behavior tests use Python commit `f74d023` as their reference. Python's LLM
integrations and cookbook applications are examples built on the core; provide
your own Go callbacks for model calls, retrieval, tools, and other application
logic.

## Installation

Go 1.18 or newer is required. A current supported release is recommended.

```sh
go get github.com/The-Pocket/PocketFlow-Go
```

## Development

From the repository root:

```sh
go mod download
go test ./...
go vet ./...
go run ./examples/workflow
go run ./examples/parallel
go run ./examples/nested
```

To test a local checkout from another module, add a replace directive to that
application's go.mod and adjust the path for your checkout:

```go
require github.com/The-Pocket/PocketFlow-Go v0.0.0
replace github.com/The-Pocket/PocketFlow-Go => ../PocketFlow-Go
```

## Nodes and action routing

A node prepares inputs, executes work, and posts its result to the shared store.
Post returns the action used to select the next node. An empty action selects
the `default` edge and remains empty in the returned result, matching Python's
`None`. A nonempty missing action ends the flow with a warning; it does not fall
back to the default edge.

```go
package main

import (
    "context"
    "fmt"
    "log"
    pf "github.com/The-Pocket/PocketFlow-Go"
)

func main() {
    start := pf.NewNode().
        SetPrep(func(ctx *pf.PfContext, params map[string]any) (any, error) {
            return ctx.Value("input"), nil
        }).
        SetExec(func(ctx *pf.PfContext, params map[string]any, input any) (any, error) {
            return input.(int) * params["factor"].(int), nil
        }).
        SetPost(func(ctx *pf.PfContext, params map[string]any, input, result any) (string, error) {
            ctx.SetValue("answer", result)
            return "done", nil
        })
    end := pf.NewNode()
    start.Next("done", end)
    flow := pf.NewFlow(start)
    flow.SetParams(map[string]any{"factor": 2})
    ctx := pf.WithParam(context.Background(), map[any]any{"input": 21})
    if _, err := flow.Run(ctx); err != nil { log.Fatal(err) }
    fmt.Println(ctx.Value("answer")) // 42
}
```

`Next` returns its target, so `a.Next("", b).Next("", c)` creates a chain.
Connect a node back to an earlier node to create a loop. `NewFlow(nil)` creates
an empty flow; `flow.Start(node)` configures its entry. Calling `node.Run` runs
only that node, even when successors are connected.

## Retries and fallback

`SetRetry(3, 100*time.Millisecond)` makes at most three total exec attempts.
Prep and post run once and are not retried. Read `ctx.RetryIndex()` inside exec
or fallback for the zero-based attempt number. `SetFallback` supplies an exec
result after the last failure; that result is passed to post.

Batch nodes use `SetItemFallback` and retry each item independently. Sequential
batches stop at the first item whose failure is not recovered. Errors carry
their phase and preserve the original cause through `errors.Is`/`errors.As`.
Fallback failures include the attempt count. Errors from an item or group
include its zero-based batch index. Go panics are not treated as retryable errors.

## Flow hooks and nesting

Ordinary flows have `SetPrep` and `SetPost`, using the same callback signatures
as nodes. Prep's result is passed to post, and the default flow post returns the
last node action. A custom post can return a different action for outer routing.

```go
inner := pf.NewFlow(first)
inner.Next("approved", approved)
inner.Next("rejected", rejected)
outer := pf.NewFlow(inner)
```

All flow variants implement `BaseNode`, so ordinary, batch and parallel batch
flows can be nested or connected as nodes. Successors of a flow are followed by
its containing flow, rather than by the flow's own standalone Run.

## Params and execution isolation

- Every visited node receives the current flow params, replacing that node's
  configured params for this execution. Batch group params override flow params.
- An outer flow supplies the params of a nested flow. The nested flow copies
  this map for its inner orchestration, matching the Python implementation.
- Sequential steps share their execution's top-level params map. Param changes
  can reach later steps, but never change the original graph configuration.
- Every batch group gets a separate map. Parallel batch node items also get
  separate top-level maps so callback mutations cannot race between items.
- Copying is shallow. Nested values and callback closure variables remain
  shared and need application-level synchronization when mutated concurrently.

Callbacks should use their params argument or `ctx.Params()`. `GetParams`
returns a snapshot of configured params; it does not collect execution results.
Return results or put them in PfContext instead.

Finish configuring a graph before running it. Do not call setters or mutate
callback fields during executions. The same frozen graph can run concurrently
with different PfContext instances. `InternalRun` is the low-level engine hook,
used on execution-local clones; normally call Run or RunAsync.

## Batch and parallel processing

`NewBatchNode()` prepares a `[]any`, executes each item sequentially, and posts
the ordered `[]any` results. `NewParallelBatchNode()` executes those items
concurrently and still returns results in input order.

```go
batch := pf.NewParallelBatchNode().SetConcurrency(4).
    SetPrep(func(ctx *pf.PfContext, params map[string]any) ([]any, error) {
        return []any{1, 2, 3}, nil
    }).
    SetExecItem(func(ctx *pf.PfContext, params map[string]any, item any) (any, error) {
        return item.(int) * 2, nil
    }).
    SetPost(func(ctx *pf.PfContext, params map[string]any, items, results []any) (string, error) {
        ctx.SetValue("results", results) // [2, 4, 6]
        return "", nil
    })
```

`NewBatchFlow(start)` repeats a complete flow for parameter maps returned by
`SetPrepBatch`. `NewParallelBatchFlow(start)` runs those groups concurrently.
`SetPostBatch` runs once after successful completion. Its generic `SetPost`
alternative receives the prep result and a nil exec result, as in Python.

`SetConcurrency(0)` is unlimited, matching Python's default parallel batches.
A positive limit bounds active item or group executions and the worker
goroutines that execute them. Queued jobs do not each allocate a goroutine.
Each nested parallel batch applies its own limit; this is not a global
concurrency budget.

Parallel errors match `asyncio.gather`: return the first completed error, skip
post, and leave sibling tasks running. Shared state may still change after an
error return. To stop siblings, the caller must explicitly cancel their common
context. The SDK does not silently switch to fail-fast sibling cancellation.

## Python async equivalents

Go callbacks execute on their caller's goroutine, so regular callbacks can wait
for HTTP, model calls, channels or other I/O. No separate event loop is needed.

| Python type | Go constructor | Scheduling |
|---|---|---|
| BaseNode | BaseNode interface / custom CloneableNode | Single lifecycle |
| Node | NewNode | Single lifecycle with retry/fallback |
| BatchNode | NewBatchNode | Sequential items |
| Flow | NewFlow | Sequential graph transitions |
| BatchFlow | NewBatchFlow | Sequential parameter groups |
| AsyncNode | NewAsyncNode | Same Node API on caller goroutine |
| AsyncBatchNode | NewAsyncBatchNode | Sequential items |
| AsyncParallelBatchNode | NewAsyncParallelBatchNode | Concurrent items |
| AsyncFlow | NewAsyncFlow | Sequential transitions; any callback can wait |
| AsyncBatchFlow | NewAsyncBatchFlow | Sequential parameter groups |
| AsyncParallelBatchFlow | NewAsyncParallelBatchFlow | Concurrent parameter groups |

Async names preserve the Python API mapping while sharing Go's execution core.
RunAsync waits for the lifecycle result, like awaiting Python's run_async.
AsyncBatch variants process items or groups sequentially;
AsyncParallelBatch variants execute them concurrently. Run and RunAsync are
both supported in Go, and flows can mix ordinary and Async nodes. Start
independent runs in goroutines when overlap is needed.

## Shared state and cancellation

PfContext provides synchronized Value/SetValue, Delete, Snapshot and Update.
Use Update for compound modifications:

```go
ctx.Update("total", func(previous any) any {
    value, _ := previous.(int)
    return value + 1
})
```

The Update callback holds the store lock; do not call other shared-store methods
from inside it. Protect nested maps/slices too: retrieving a map with Value
does not make later map mutations safe. Value and Snapshot return shallow values.
When Update needs an inherited value, it resolves that value outside the lock
and retries the lookup if the shared store changed. The update callback itself
runs once, including when a cancellation context wraps the same shared store.

```go
parent, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
ctx := pf.NewContext(parent)
```

Execution checks cancellation between phases and transitions, and retry waits
are interruptible. Callback I/O must use the supplied ctx too. Cancellation is
cooperative: Go cannot forcibly stop a callback that ignores its context.
Canceled executions skip retries, fallback and remaining phases. PfContext
is a pointer object; use WithContext to share its store under another context.

## Custom nodes

Callbacks cover the standard extension points. For a custom BaseNode used in a
flow, implement `Clone() BaseNode` as well. Preserve the concrete type and clone
its params/configuration. For example, with a struct embedding `*pf.Node`, copy
the struct and set `copy.Node = original.Node.Clone().(*pf.Node)`.

Go embedding does not dynamically override an embedded Node's lifecycle. Use
callbacks, or implement your custom InternalRun/Run methods explicitly. A flow
rejects nodes without Clone instead of mutating the graph during execution.
For custom nodes, orchestration calls the clone's public SetParams method, so
overrides can validate or transform execution params.

## Tests and parity reference

The tests cover synchronous composition, retries, sequential/parallel batches,
multi-level nested batches, execution isolation, atomic updates, cancellation,
ordered results, concurrency limits and sibling behavior after errors.

`TestPythonCoreParity` compares 34 serialized scenarios with a separate Python
PocketFlow checkout. To use the same reference as CI:

```sh
git clone https://github.com/The-Pocket/PocketFlow .python-reference
git -C .python-reference checkout f74d023f93607b8c3268133339a5e532a949898c
go test -run TestPythonCoreParity -v
go test -race ./...
```

The test discovers `.python-reference` or a neighboring Python checkout.
Set `POCKETFLOW_PYTHON_PATH` to use a different checkout, and `POCKETFLOW_PYTHON`
to select an interpreter. Without explicit configuration, the reference test
skips when Python or a checkout is absent. An unavailable explicitly configured
checkout or interpreter fails the test. CI supplies the pinned Python checkout
and runs on Windows and Linux, with Go 1.18 and stable. The Linux stable job also
runs the race detector. Windows race tests require a compatible C compiler in PATH.

See [the execution contract](docs/design.md) and
[migration notes](docs/migration.md). The alignment concerns SDK primitives;
Python operator syntax, exception types, event-loop internals and cookbook
applications are represented through Go's own language and extension points.

## Contributing

Pull requests are welcome. Include tests for changed behavior and update the
migration notes when changing an existing API or execution contract. Run the
tests, vet, examples and race detector before submitting.

## License

[MIT](LICENSE)
