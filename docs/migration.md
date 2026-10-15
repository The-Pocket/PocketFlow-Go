# Migrating from the initial Go implementation

The callback signatures, module path, node constructors and most setters are
unchanged. These corrections affect code that relied on earlier behavior.

## Empty actions

Default post returns `""`, corresponding to Python `None`. The engine uses
`DefaultAction` (`"default"`) only to look up a default edge. If your application
needs the literal `"default"` as its final action, return it explicitly in post.
An empty flow also returns an empty action.

## Execution params

SetParams configures the graph. GetParams returns a shallow snapshot of that
configuration. Executions use their own node copies and parameter maps; data
written to params by a callback is not retained in the original node afterward.
Use callback args or ctx.Params for current values, and ctx.SetValue/Update to
publish results. Every step now receives its current flow or batch params.

Sequential steps share the orchestration's params map. A nested flow copies it
for inner steps. Batch group params override the flow's params and are visible
to all steps in that group. Graph setters must not run concurrently with jobs.

## Batch flow interface

BatchFlow now implements BaseNode/CloneableNode. Its Exec signature is
`Exec(ctx *PfContext, prep any) (any, error)`, and it performs the batch work.
Calling SetParams on a batch flow returns that batch flow, not its embedded Flow.
SetPrep/SetPost preserve the batch receiver too. SetPrepBatch/SetPostBatch are
typed alternatives; whichever setter is called last selects that hook style.

BatchFlow post's generic exec result is nil, matching Python; use shared state
to aggregate results. BatchNode's default item exec returns nil, matching a
Python node with no exec override, rather than passing the input through.

## Concurrency

Use NewParallelBatchNode/NewParallelBatchFlow or their AsyncParallel aliases.
AsyncNode, AsyncFlow and sequential AsyncBatch aliases wait on the caller's Go
goroutine. RunAsync is a blocking await-style convenience, not a future API.

Parallel item results preserve input order. The first error returns promptly;
other jobs continue unless the caller cancels their context. Account for that
when consuming shared state after failures. Configured concurrency limits are
local to a batch, including nested batches.
A positive limit now bounds worker goroutines as well as active executions;
queued items do not allocate their own goroutines. Zero remains unlimited.

PfContext protects top-level store access. Use Update for atomic compound
operations. Nested application objects and captured callback state need their
own synchronization. Parallel batch node items receive separate top-level
parameter maps, preventing data races from per-item callback mutation.

## Go-specific boundaries

Use errors to report retryable failures. Panics are not retried. Invalid retry
settings and negative concurrency limits are rejected. Caller cancellation
interrupts retry waits and stops remaining cooperative phases; fallback does
not handle canceled executions. A canceled callback that ignores ctx may keep
running, as with other Go APIs.

Custom flow nodes implement Clone() BaseNode. Embedding Node supplies common
methods, but Go does not dispatch embedded lifecycle methods through a derived
receiver. Prefer callbacks or explicitly implement InternalRun and Run.
Custom SetParams overrides are called on execution clones, including for types
that embed Node, BatchNode, Flow, or BatchFlow.

WithParam copies the initial top-level value map; mutate shared values through
PfContext rather than the original input map. Snapshot is shallow, and PfContext
must not be copied by value because it contains execution/store initialization
state. WithContext derives cancellation while preserving the shared store.
