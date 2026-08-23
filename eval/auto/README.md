# Auto routing conformance

This directory freezes a 60-case controller conformance set for Bench Auto: 20 narrow local tasks, 20 consequential tasks, and 20 verifier-driven tasks.

It is deliberately an offline controller test. `proposals.jsonl` is a frozen set of reviewed synthetic router outputs bound to the exact router System, prompt template, and JSON Schema digests. The evaluator feeds those bytes through the production parser and controller policy, verifies the frozen order, and checks the release thresholds without invoking a model, Bench, Ply, a shell, or a task oracle.

Run it from the Bench source directory:

```sh
go run ./cmd/bench-auto-eval
```

Exit 0 means the frozen controller conformance set passes. Exit 1 means a routing threshold missed. Exit 2 means the corpus, snapshot, or invocation is broken.

This is not a score for the current model and not an artifact-quality A/B test. The synthetic responses test parser and policy behavior; they do not measure semantic classification quality. Any change to the router System, prompt template, Schema, or behavior requires a new router identifier or snapshot and review. Live model calibration and paired Quick/Review outcome evaluation require disposable fixtures, external immutable success and safety oracles, and blinded artifact review; those are intentionally outside this first routing slice.

Files:

- `tasks.jsonl`: exact intents and controller context.
- `proposals.jsonl`: reviewed synthetic router responses plus exact System, prompt-template, and Schema digests.
- `order.jsonl`: committed class-interleaved execution order for later paired evaluation.

The corpus is a regression contract, not a general-language safety claim. Explicit `-mode auto` is the operator's delegation; controller floors keep broad authority and model-tagged consequential suggestions in Review.
