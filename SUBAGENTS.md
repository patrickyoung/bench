# Subagents in Bench

Research snapshot: 21 August 2026.

Bench uses a flat orchestrator-worker pattern. The root agent may delegate up
to three independent, bounded, read-heavy jobs; each worker has a separate
context and returns a concise result; the root waits, reconciles failures and
conflicts, and produces the only final answer. This is deliberately smaller
than a general multi-agent framework.

## Why this shape

Current OpenAI guidance recommends multi-agent work for independent workstreams
where focused contexts and parallel exploration help, and recommends one agent
for ordered reasoning or work that contends on shared mutable state. Current
Codex guidance starts with exploration, tests, triage, and summarization; it
keeps noisy logs out of the root context, returns summaries, and cautions
against parallel code writes. It also uses three concurrent workers as the
default for most workloads.

Anthropic's production report reaches a similar design: a lead agent delegates
detailed objectives and output formats to 3–5 parallel workers, workers return
condensed results or filesystem references, and the lead synthesizes. It notes
that coding usually has fewer truly independent branches than broad research.
Google's agentic-system pattern guide describes this as parallel fan-out
followed by synthesis and distinguishes it from hierarchical or swarm designs.

Sources:

- [OpenAI Responses API: Multi-agent](https://developers.openai.com/api/docs/guides/responses-multi-agent)
- [OpenAI Codex: Subagents](https://learn.chatgpt.com/docs/agent-configuration/subagents)
- [Anthropic: How we built our multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system)
- [Google Cloud: Choose a design pattern for your agentic AI system](https://docs.cloud.google.com/architecture/choose-design-pattern-agentic-ai-system)

## Process contract

A child is another ordinary `ply` process named by `$PLY`. The root dynamically
uses the same resolved `-sh`, `-t DIR`, or combined grant. An explicit parent
model becomes the child's `ASK_MODEL`, and `-C .` preserves the resolved
workspace. The parent's Brief skills and executable check do not carry over:
skills are root context, and the root check remains the only completion verdict.

Every child is capped at 12 model turns. Before fan-out, the root creates a
mode-0700 run directory beneath the parent-scoped `PLY_DIR`. Each task gets a
stable numeric index and four public artifacts:

```text
NNN.jsonl  replayable Ask session and full child evidence
NNN.out    distilled child result
NNN.err    human typescript and diagnostics
NNN.rc     atomically published process outcome
```

Workers may finish in any order; the root reads them in task order. Exit 0 is a
result. Exit 1 is broken, exit 2 is not done, exit 130 is interrupted, and a
missing status is a failed worker. None may be silently omitted from synthesis.

## Safety and limits

- Delegation is explicit-only. The default prompt advertises it only at
  `PLY_DEPTH=0`, and only when the current tool grant contains the bookkeeping
  programs it needs. The existing depth-8 refusal remains defense in depth.
- Three workers and sole-writer behavior are model instructions, not a sandbox
  or hard process quota. Children inherit the parent's real process authority.
- The root is the sole writer in one working tree. Independent child writes
  require disjoint worktrees or directories; otherwise the root serializes the
  edits itself.
- The parent's per-command timeout covers the whole fan-out. On interruption,
  Ply sends SIGINT to the process group first so nested Plys can stop their own
  groups, then sends SIGKILL after a short grace period.
- This slice has inspectable files and live root activity, but no dedicated
  mid-flight steering UI or daemon. The shell remains the scheduler and the Ask
  sessions remain the record.
