# Python core capability parity

Reference: `The-Pocket/PocketFlow` commit
`f74d023f93607b8c3268133339a5e532a949898c` (`pocketflow/__init__.py`).

## Execution contract

- A node runs prep, exec, then post. Only exec is retried. The retry count is
  the total number of attempts; fallback feeds its result to post.
- Empty Go actions represent Python `None`. They select the `default` edge
  without changing the action returned to the caller.
- A flow passes its current params to every visited node. Batch params override
  flow params. An outer flow supplies the params of a nested flow, matching the
  Python implementation's shallow node copies and `set_params` calls.
- Graphs are configuration. Runs use shallow copies of built-in nodes and fresh
  top-level parameter maps. Shared values and objects captured by callbacks are
  intentionally shared. Custom graph nodes implement `CloneableNode`.
- Flows have configurable prep/post hooks. All flow variants implement
  `BaseNode`, including batch flows and parallel batch flows.
- Batch nodes retry each item independently. Sequential batches stop on the
  first unhandled error. Parallel batch results preserve input order.
- Parallel batches emulate `asyncio.gather`: the first completed error is
  returned promptly, post is skipped, and sibling jobs are not implicitly
  canceled. Explicit caller cancellation stops cooperative work. Pending tasks
  can continue after an error return, so callbacks must honor context deadlines
  when bounded execution is required.

## Go execution model

Go callbacks already block or wait on their caller's goroutine. AsyncNode,
AsyncFlow, AsyncBatchNode, and AsyncBatchFlow are aliases of the corresponding
ordinary types, with constructors and RunAsync convenience methods. Parallel
variants enable concurrent item or complete-flow execution. Graph transitions
within a flow remain sequential. No separate event loop is needed.

A positive concurrency limit creates a fixed worker pool and one dispatcher
per parallel batch. The job channel is unbuffered, so queued items need no
separate goroutines. Cancellation stops dispatch; errors notify the caller
without blocking workers or abandoning queued sibling jobs. Successful
completion waits for all workers before post can read their results.

PfContext shares a synchronized store between execution contexts. Value,
SetValue, Delete, Snapshot, and Update protect top-level access. Update executes
a read/modify/write while holding the store lock. Nested maps, slices, callback
closures, and application objects still require their own synchronization.
RetryIndex and Params expose execution-local information.
Inherited Update lookups run outside the lock and are retried when a store
revision changes. This prevents stale values from being restored after a
concurrent delete through a context sharing the same store. The application
update callback runs once under the write lock. Deferred unlocks also preserve
store usability after a caller recovers an invalid-key or callback panic.

Graph configuration must finish before execution. Mutating callbacks, retry
settings, successors, or configured params during a run is unsupported. Do not
capture a node's GetParams in a callback to access run params: use the callback
argument or ctx.Params instead.
Custom node clones receive params through their public SetParams method.
Only concrete built-in types use the internal shared-params shortcut.

## Validation

Keep the existing regression tests, adapt assertions that depended on mutation
of original node params, and add Python-derived behavior cases for sequencing,
branching, cycles, composition, batch variants, retries, and fallback. A Python
reference script and Go test compare serialized outcomes across languages.
Additional Go tests cover repeat/concurrent runs, shared updates, ordered
parallel results, first-error behavior, cancellation, and error wrapping.
Run go test, go vet, go test -race, examples, and the cross-language comparison.

## Compatibility

The module path and existing callback signatures stay unchanged. Deliberate
corrections: default returned action is empty, node params no longer collect
runtime results, BatchFlow.Exec takes context and actually executes batches,
and BatchNode's default exec returns nil rather than passing items through.
Invalid retry/concurrency options are rejected. Go errors model Python
exceptions; Go panics are programming errors and are not retried.
