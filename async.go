package pocketflow

// Async types mirror Python's node and flow API using the same Go execution core.
// RunAsync waits for the lifecycle result, like awaiting run_async in Python.
// AsyncBatch variants are sequential; AsyncParallelBatch variants overlap jobs.
// Use goroutines to overlap independent runs.
type AsyncNode = Node
type AsyncBatchNode = BatchNode
type AsyncParallelBatchNode = ParallelBatchNode
type AsyncFlow = Flow
type AsyncBatchFlow = BatchFlow
type AsyncParallelBatchFlow = ParallelBatchFlow

func NewAsyncNode() *AsyncNode                           { return NewNode() }
func NewAsyncBatchNode() *AsyncBatchNode                 { return NewBatchNode() }
func NewAsyncParallelBatchNode() *AsyncParallelBatchNode { return NewParallelBatchNode() }
func NewAsyncFlow(start BaseNode) *AsyncFlow             { return NewFlow(start) }
func NewAsyncBatchFlow(start BaseNode) *AsyncBatchFlow   { return NewBatchFlow(start) }
func NewAsyncParallelBatchFlow(start BaseNode) *AsyncParallelBatchFlow {
	return NewParallelBatchFlow(start)
}

func (n *Node) RunAsync(ctx *PfContext) (string, error)      { return n.Run(ctx) }
func (n *BatchNode) RunAsync(ctx *PfContext) (string, error) { return n.Run(ctx) }
func (f *Flow) RunAsync(ctx *PfContext) (string, error)      { return f.Run(ctx) }
func (f *BatchFlow) RunAsync(ctx *PfContext) (string, error) { return f.Run(ctx) }
