[![Go Reference](https://pkg.go.dev/badge/github.com/The-Pocket/PocketFlow-Go.svg)](https://pkg.go.dev/github.com/The-Pocket/PocketFlow-Go)

# PocketFlow Go

A minimalist LLM framework concept, ported from Python to Go.

## Overview

PocketFlow Go is a port of the original [Python PocketFlow](https://github.com/The-Pocket/PocketFlow). It provides a lightweight, flexible system for building and executing LLM-based (or other sequential) workflows through a simple node-based architecture using Go interfaces and functions.

> **Note:** This is an initial synchronous implementation mirroring the Java version. It currently does not support asynchronous operations (goroutines for execution). Community contributors are welcome to help enhance and maintain this project, particularly with adding robust concurrency patterns if desired.

## Installation

Ensure you have Go (1.18 or later recommended, 1.21+ for map cloning functions) installed.

```bash
go get github.com/The-Pocket/PocketFlow-Go
```

## Usage

Here's a simple example of how to use PocketFlow Go in your application:

```go
package main

import (
	"context"
	"fmt"
	"log"

	pf "github.com/The-Pocket/PocketFlow-Go" // Adjust import path
)

// Define node logic using PocketFlow's functional style

// myStartNode creates a node that starts the workflow.
func myStartNode() pf.BaseNode {
	fnExec := func(ctx *pf.PfContext, params map[string]any, prepResult any) (any, error) {
		fmt.Println("LOG: Starting workflow...")
		execResult := "started_data" // Assume this is the result of some operation
		return execResult, nil       // Exec result can be used by Post to determine action
	}

	fnPost := func(ctx *pf.PfContext, params map[string]any, prepResult any, execResult any) (string, error) {
		// Use execResult to decide the next step
		fmt.Printf("LOG: Start node finished with data: %v\n", execResult)
		ctx.SetValue("start_result", execResult) // Optional: Set/update shared context key/value
		return "started", nil                    // Action name to trigger the next node
	}

	return pf.NewNode().SetExec(fnExec).SetPost(fnPost)
}

// myEndNode creates a node that ends the workflow.
func myEndNode() pf.BaseNode {
	fnPrep := func(ctx *pf.PfContext, params map[string]any) (any, error) {
		// Prep can access the shared context
		startData := ctx.Value("start_result")
		prepMsg := fmt.Sprintf("Preparing to end workflow, received: %v", startData)
		fmt.Println("LOG: " + prepMsg)
		return prepMsg, nil // Prep result passed to Exec
	}

	fnExec := func(ctx *pf.PfContext, params map[string]any, prepResult any) (any, error) {
		prepMsg := prepResult.(string) // Assume prep result is string
		fmt.Printf("LOG: Ending workflow with: \"%s\"\n", prepMsg)
		// End nodes often don't need to return data
		return nil, nil
	}

	return pf.NewNode().SetPrep(fnPrep).SetExec(fnExec)
	// Default Post (returns DefaultAction) is fine here
}

func main() {
	// Create instances of your nodes
	startNode := myStartNode()
	endNode := myEndNode()

	// Connect the nodes: start -> end (when action is "started")
	startNode.Next("started", endNode)

	// Create a flow with the start node
	flow := pf.NewFlow(startNode)

	// Create a context and run the flow
	baseCtx, cancel := context.WithCancel(context.TODO())
	defer cancel()                      // Ensure context is cancelled to avoid leaks
	myCtx := pf.WithParam(baseCtx, nil) // Create a new PocketFlow context

	fmt.Println("LOG: Executing workflow...")
	finalAction, err := flow.Run(myCtx)
	if err != nil {
		log.Fatalf("Workflow failed: %v\n", err)
	}

	fmt.Printf("LOG: Workflow completed successfully. Final action: %s\n", finalAction)
	fmt.Printf("LOG: Final Context: %v\n", myCtx)
	// Output:
	// LOG: Executing workflow...
	// LOG: Starting workflow...
	// LOG: Start node finished with data: started_data
	// LOG: Preparing to end workflow, received: started_data
	// LOG: Ending workflow with: "Preparing to end workflow, received: started_data"
	// LOG: Workflow completed successfully. Final action: default
	// LOG: Final Context: &{context.TODO.WithCancel map[start_result:started_data]}
}
```

## Development

### Building the Project

```bash
go build ./...
```

### Running Tests

```bash
go test ./...
```
Or with coverage:
```bash
go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out
```

## Contributing

Contributions are welcome! We're particularly looking for volunteers to:

1.  Implement asynchronous operation support (e.g., using goroutines, channels, `context.Context`).
2.  Add more comprehensive test coverage, including edge cases and error handling.
3.  Improve documentation and provide more complex examples (e.g., LLM integration stubs).
4.  Refine the API for better Go idiomatic usage if applicable.

Please feel free to submit pull requests or open issues for discussion.

## License

[MIT License](LICENSE)