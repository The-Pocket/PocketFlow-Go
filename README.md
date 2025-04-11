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
	"fmt"
	"log"

	pf "github.com/The-Pocket/PocketFlow-Go" // Adjust import path
)

// Define node logic using PocketFlow's functional style

// myStartNode creates a node that starts the workflow.
func myStartNode() pf.BaseNode {
	return pf.NewNode().
		SetExec(func(prepResult any, params pf.SharedContext) (any, error) {
			log.Println("Starting workflow...")
			// Exec result can be used by Post to determine action
			return "started_data", nil
		}).
		SetPost(func(ctx pf.SharedContext, prepResult any, execResult any, params pf.SharedContext) (string, error) {
			// Use execResult to decide the next step
			log.Printf("Start node finished with data: %v\n", execResult)
			ctx["start_result"] = execResult // Optional: Update shared context
			return "started", nil            // Action name to trigger the next node
		})
}

// myEndNode creates a node that ends the workflow.
func myEndNode() pf.BaseNode {
	return pf.NewNode().
		SetPrep(func(ctx pf.SharedContext, params pf.SharedContext) (any, error) {
			// Prep can access the shared context
			startData := ctx["start_result"]
			prepMsg := fmt.Sprintf("Preparing to end workflow, received: %v", startData)
			log.Println(prepMsg)
			return prepMsg, nil // Prep result passed to Exec
		}).
		SetExec(func(prepResult any, params pf.SharedContext) (any, error) {
			prepMsg := prepResult.(string) // Assume prep result is string
			log.Printf("Ending workflow with: %s\n", prepMsg)
			// End nodes often don't need to return data
			return nil, nil
		})
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
	context := make(pf.SharedContext)
	log.Println("Executing workflow...")
	finalAction, err := flow.Run(context)
	if err != nil {
		log.Fatalf("Workflow failed: %v\n", err)
	}

	log.Printf("Workflow completed successfully. Final action: %s\n", finalAction)
	log.Printf("Final Context: %v\n", context)
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