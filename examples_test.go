package pocketflow_test

import (
	"context"
	"fmt"
	"log"

	pf "github.com/The-Pocket/PocketFlow-Go" // Adjust import path
)

func Example() {
	// Define node logic using PocketFlow's functional style

	// myStartNode creates a node that starts the workflow.
	myStartNode := func() pf.BaseNode {
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
	myEndNode := func() pf.BaseNode {
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
