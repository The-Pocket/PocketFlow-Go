"""Run shared behavioral scenarios against a checkout of upstream PocketFlow.

Usage: python -B tools/python_reference.py /path/to/PocketFlow
The reference checkout is supplied separately; this script never changes it.
"""

import asyncio
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(sys.argv[1]).resolve()))
from pocketflow import (
    Node, AsyncNode, BatchNode, AsyncBatchNode, AsyncParallelBatchNode,
    Flow, AsyncFlow, BatchFlow, AsyncBatchFlow, AsyncParallelBatchFlow,
)


def make_node(mode, prep=None, execute=None, post=None, fallback=None, attempts=1):
    base = Node if mode == "sync" else AsyncNode

    class Callback(base):
        def prep(self, shared):
            return prep(self, shared) if prep else None

        def exec(self, value):
            return execute(self, value) if execute else None

        def post(self, shared, before, result):
            return post(self, shared, before, result) if post else None

        def exec_fallback(self, value, error):
            if fallback:
                return fallback(self, value, error)
            raise error

        async def prep_async(self, shared):
            return self.prep(shared)

        async def exec_async(self, value):
            await asyncio.sleep(0)
            return self.exec(value)

        async def post_async(self, shared, before, result):
            return self.post(shared, before, result)

        async def exec_fallback_async(self, value, error):
            return self.exec_fallback(value, error)

    return Callback(max_retries=attempts)


def make_flow(mode, start=None, prep=None, post=None, batch=False):
    bases = {"sync": BatchFlow if batch else Flow,
             "async": AsyncBatchFlow if batch else AsyncFlow,
             "parallel": AsyncParallelBatchFlow if batch else AsyncFlow}
    base = bases[mode]

    class Hooks(base):
        def prep(self, shared):
            return prep(self, shared) if prep else None

        def post(self, shared, before, result):
            return post(self, shared, before, result) if post else result

        async def prep_async(self, shared):
            return self.prep(shared)

        async def post_async(self, shared, before, result):
            return self.post(shared, before, result)

    return Hooks(start=start)


def finish(node, state):
    error = False
    action = ""
    try:
        if isinstance(node, AsyncNode):
            action = asyncio.run(node.run_async(state))
        else:
            action = node.run(state)
    except ValueError:
        error = True
    return {"action": action or "", "error": error, "state": state}


outcomes = {}
for mode in ("sync", "async"):
    state = {"value": 5}
    a = make_node(mode, execute=lambda n, v: state.update(value=state["value"] + 3))
    b = make_node(mode, execute=lambda n, v: state.update(value=state["value"] * 2))
    a >> b
    outcomes[f"sequence/{mode}"] = finish(make_flow(mode, a), state)

    state = {"value": 5}
    check = make_node(mode, post=lambda n, s, p, r: "positive" if s["value"] >= 0 else "negative")
    subtract = make_node(mode, execute=lambda n, v: state.update(value=state["value"] - 3))
    terminal = make_node(mode, post=lambda *args: "finished")
    check - "positive" >> subtract
    check - "negative" >> terminal
    subtract >> check
    outcomes[f"cycle/{mode}"] = finish(make_flow(mode, check), state)

    state = {}
    inner = make_flow(mode, make_node(mode, post=lambda *args: "inner_done"))
    inner - "inner_done" >> make_node(mode, execute=lambda n, v: state.update(path="B"), post=lambda *args: "outer_done")
    inner - "other" >> make_node(mode, execute=lambda n, v: state.update(path="A"))
    outcomes[f"nested-flow/{mode}"] = finish(make_flow(mode, make_flow(mode, inner)), state)

    state = {"seen": []}
    mutate = make_node(mode, execute=lambda n, v: n.params.update(version=9))
    capture = make_node(mode, execute=lambda n, v: state["seen"].append(n.params["version"]))
    mutate >> capture
    inner = make_flow(mode, mutate)
    inner >> capture
    outer = make_flow(mode, inner)
    outer.set_params({"version": 1})
    outcomes[f"param-mutation/{mode}"] = finish(outer, state)

    state = {}
    child = make_node(mode, execute=lambda n, v: state.update(node_param=n.params["value"]), post=lambda *args: "done")

    def hooks_post(n, s, before, result):
        s.update(prepared=before["value"], last_action=result)
        return "hooks_done"

    flow = make_flow(mode, child, prep=lambda n, s: {"value": 999}, post=hooks_post)
    flow.set_params({"value": 3})
    outcomes[f"flow-hooks/{mode}"] = finish(flow, state)
    outcomes[f"empty-flow/{mode}"] = finish(make_flow(mode), {})

    for behavior in ("success", "fallback", "failure", "prep-failure", "post-failure"):
        state = {"attempts": [], "prep_calls": 0, "post_calls": 0}

        def prep(n, shared):
            shared["prep_calls"] += 1
            if behavior == "prep-failure":
                raise ValueError("prep")
            return 7

        def execute(n, before):
            state["attempts"].append(n.cur_retry)
            if behavior in ("fallback", "failure") or behavior == "success" and n.cur_retry < 2:
                raise ValueError("exec")
            return before

        def post(n, shared, before, result):
            shared["post_calls"] += 1
            if behavior == "post-failure":
                raise ValueError("post")
            shared["result"] = result
            return "done"

        node = make_node(mode, prep=prep, execute=execute, post=post,
                         fallback=(lambda n, p, e: 99) if behavior == "fallback" else None,
                         attempts=3)
        outcomes[f"retry-{behavior}/{mode}"] = finish(node, state)

for mode in ("sync", "async", "parallel"):
    for empty in (False, True):
        state = {}
        base = {"sync": BatchNode, "async": AsyncBatchNode, "parallel": AsyncParallelBatchNode}[mode]

        class Items(base):
            def prep(self, shared):
                return [] if empty else [1, 2, 3]

            def exec(self, value):
                return value * 2

            def post(self, shared, before, result):
                shared["results"] = result

            async def prep_async(self, shared):
                return self.prep(shared)

            async def exec_async(self, value):
                await asyncio.sleep(0)
                return self.exec(value)

            async def post_async(self, shared, before, result):
                return self.post(shared, before, result)

        outcomes[f"batch-node{'-empty' if empty else ''}/{mode}"] = finish(Items(), state)

    state = {}
    first = make_node(mode)
    second = make_node(mode, execute=lambda n, v: state.update({n.params["key"]: n.params["base"] * n.params["multiplier"]}))
    first >> second
    batch = make_flow(mode, first, batch=True,
                      prep=lambda n, s: [{"key": "a", "multiplier": 3}, {"key": "b", "multiplier": 4}])
    batch.set_params({"base": 2, "multiplier": 100})
    outcomes[f"batch-flow/{mode}"] = finish(batch, state)

    state = {"A": [], "B": []}
    groups = {"A": [1, 2], "B": [3, 4]}
    item = make_node(mode, execute=lambda n, v: state[n.params["group"]].append(n.params["item"] * 2))
    inner = make_flow(mode, item, batch=True,
                      prep=lambda n, s: [{"item": value} for value in groups[n.params["group"]]])
    outer = make_flow(mode, inner, batch=True,
                      prep=lambda n, s: [{"group": "A"}, {"group": "B"}])
    outcomes[f"nested-batch/{mode}"] = finish(outer, state)
    for group in groups:
        state[group].sort()

print(json.dumps(outcomes, sort_keys=True))
