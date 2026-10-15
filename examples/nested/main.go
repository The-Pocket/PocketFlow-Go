package main

import (
	"context"
	"fmt"
	"log"

	pf "github.com/The-Pocket/PocketFlow-Go"
)

func main() {
	groups := map[string][]int{"A": {1, 2}, "B": {3, 4}}
	item := pf.NewNode().SetExec(func(ctx *pf.PfContext, params map[string]any, _ any) (any, error) {
		ctx.Update("total", func(current any) any { return current.(int) + params["item"].(int)*2 })
		return nil, nil
	})
	inner := pf.NewAsyncParallelBatchFlow(item).SetPrepBatch(func(_ *pf.PfContext, params map[string]any) ([]map[string]any, error) {
		result := make([]map[string]any, 0)
		for _, value := range groups[params["group"].(string)] {
			result = append(result, map[string]any{"item": value})
		}
		return result, nil
	})
	outer := pf.NewAsyncParallelBatchFlow(inner).SetPrepBatch(func(*pf.PfContext, map[string]any) ([]map[string]any, error) {
		return []map[string]any{{"group": "A"}, {"group": "B"}}, nil
	})
	ctx := pf.WithParam(context.Background(), map[any]any{"total": 0})
	if _, err := outer.RunAsync(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("total=%v\n", ctx.Value("total"))
}
