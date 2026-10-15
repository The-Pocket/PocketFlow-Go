package main

import (
	"context"
	"fmt"
	"log"

	pf "github.com/The-Pocket/PocketFlow-Go"
)

func main() {
	squares := pf.NewAsyncParallelBatchNode().SetConcurrency(2).
		SetPrep(func(*pf.PfContext, map[string]any) ([]any, error) { return []any{1, 2, 3, 4}, nil }).
		SetExecItem(func(_ *pf.PfContext, _ map[string]any, item any) (any, error) {
			value := item.(int)
			return value * value, nil
		}).
		SetPost(func(ctx *pf.PfContext, _ map[string]any, _, results []any) (string, error) {
			ctx.SetValue("squares", results)
			return "", nil
		})
	reduce := pf.NewNode().SetExec(func(ctx *pf.PfContext, _ map[string]any, _ any) (any, error) {
		total := 0
		for _, value := range ctx.Value("squares").([]any) {
			total += value.(int)
		}
		ctx.SetValue("total", total)
		return nil, nil
	})
	squares.Next("", reduce)
	ctx := pf.NewContext(context.Background())
	if _, err := pf.NewAsyncFlow(squares).RunAsync(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("squares=%v total=%v\n", ctx.Value("squares"), ctx.Value("total"))
}
