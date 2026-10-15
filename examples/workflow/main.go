package main

import (
	"context"
	"fmt"
	"log"

	pf "github.com/The-Pocket/PocketFlow-Go"
)

func main() {
	ctx := pf.WithParam(context.Background(), map[any]any{"value": 5})
	check := pf.NewNode().SetPost(func(ctx *pf.PfContext, _ map[string]any, _, _ any) (string, error) {
		if ctx.Value("value").(int) >= 0 {
			return "positive", nil
		}
		return "negative", nil
	})
	finish := pf.NewNode().SetExec(func(ctx *pf.PfContext, params map[string]any, _ any) (any, error) {
		ctx.SetValue("value", ctx.Value("value").(int)+params["increment"].(int))
		return nil, nil
	}).SetPost(func(*pf.PfContext, map[string]any, any, any) (string, error) { return "done", nil })
	check.Next("positive", finish)
	flow := pf.NewFlow(check)
	flow.SetParams(map[string]any{"increment": 3})
	action, err := flow.Run(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("action=%s value=%v\n", action, ctx.Value("value"))
}
