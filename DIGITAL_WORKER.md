# Digital worker architecture

**Status:** proposed architecture, not an implementation claim.  
**Research cut:** 2026-08-30.

This document distinguishes **Today** (behavior documented and implemented in
the repository) from **Target** (work that would have to be designed, built,
and accepted). A target component must not be described as available until its
acceptance gate below has executable evidence.

## Executive verdict

Bench already has the semantic core that many agent systems lack: visible
intent-to-contract admission, exact process boundaries, explicit approvals,
executable completion checks, conservative treatment of uncertain effects, and
verified learning. Its largest missing boundary is the machine around the
worker. Cage intentionally is not a confidentiality, identity, credential,
syscall, CPU, memory, or process-count boundary, and Tend intentionally is not
a distributed or exactly-once workflow engine.

The target is a **verifiable, revocable, resumable digital worker**:

- Bench remains a foreground terminal control plane that composes ordinary
  programs. It does not gain a daemon, scheduler, private tool protocol,
  telemetry database, model client, or second implementation of a filter.
- A proposed external Linux-VM execution plane becomes the replaceable
  "hands" for terminal, browser, and desktop actions. The admitted contract,
  approval authority, verifier, credentials, and durable evidence remain
  outside the guest.
- Every run binds a signed capability lock. The lock says exactly which image,
  binaries, skills, mounts, network destinations, identities, budgets, and
  approval policy were available. It grants no completion authority.
- A VM or harness crash can lose expendable compute, never the sole session
  history or the classification of a possibly-started external effect.

The product advantage is not maximum autonomy. It is maximum useful capability
under inspectable, least authority, with checks that can prove the outcome.

## Current and target boundary

### Today

The current source of truth is [Bench's README](README.md),
[development guidance](AGENTS.md), [Agent security](../agent/SECURITY.md), and
[Tend's design](../tend/DESIGN.md).

- The pinned Bench suite ships 16 components as ordinary executables using
  argv, stdin, stdout, stderr, exit status, and explicit files. Packaging is
  not runtime composition: Bench's open-task path composes the phase-relevant
  filters, while Agent's core path is Brief -> Ply -> Ask with Cage around
  model-authored actions. Action, Tend, Context, Cite, MCP, OAuth, Trail, Hone,
  May, and Draft remain explicit controller, lifecycle, evidence, protocol, or
  operator seams; no run loads every filter merely to maximize a count.
- Default work is `Intent -> Contract -> Task <-> Tools`. Contract admission,
  exact approvals, verifier receipts, review, and loop results are implemented.
- The experimental Agent sibling supplies a folder-shaped worker, caged model
  actions, checkpoints, specialists, external-effect proposals, history, and
  verified fail-to-pass learning.
- Cage limits persistent writes and normally denies network for model-authored
  actions, but it can still expose reads and inherited environment values of
  the host OS identity. It does not supply the stronger boundaries listed in
  the verdict.
- Tend supplies local crash durability, immutable events, waits/signals,
  artifact bindings, serialization keys, and an explicit `unknown` state for a
  started attempt whose result is missing. It is local-only and makes no
  exactly-once claim.
- MCP and OAuth are protocol edges. The browser helper is not an unattended
  full computer-use runtime. This repository now contains a deliberately inert
  QEMU/mkosi development image and an exact guest process seam, not a shipped
  execution plane: there is still no VM broker, signed capability verifier,
  workload identity issuer, credential vault, governed memory index, A2A or
  AG-UI boundary, or signed OS-update service.

### Target invariants

The proposed work must preserve these rules:

1. **Bench stays compositional.** New runtime functions are separate ordinary
   programs or deployment infrastructure, invoked through public seams.
2. **The contract and verifier stay outside the worker VM.** Guest code cannot
   rewrite its judge or promote model prose into completion.
3. **The durable session stays outside replaceable compute.** Context
   compaction is an optimization with pointers, never the only history.
4. **Long-lived credentials never enter the guest or model context.** External
   proxies use short-lived task grants to act on behalf of a named principal.
5. **Network is default-deny.** Permitted domains are capabilities whose
   methods, accounts, headers, and data flows are constrained and recorded.
6. **Unknown effects are never retried automatically.** They are observed,
   reconciled, or explicitly resolved.
7. **Updates and extensions are content-addressed and signed.** Mutable tags,
   install-time trust, and an unsigned plugin directory are insufficient.
8. **Receipts remain authority; telemetry is derived.** An OTLP backend or UI
   may render evidence but cannot create a verdict.

## Target architecture

`[TODAY]` labels implemented composition. `[PROPOSED]` labels new external
boundaries. The diagram is a target topology, not a statement that those
components exist.

```text
                         user / operator
                               |
                     [TODAY] Bench TUI/CLI
                 foreground, 80x24, no daemon
                               |
               argv + stdin + stdout/stderr + status
                               |
       [TODAY] Ask / Brief / Ply / May / Cage / Agent / Tend
               Trail / Action / Hone / Context / Cite
                         MCP / OAuth / Web
                               |
                  admitted contract + exact check
                               |
        .----------------------' `-----------------------.
        |                                                  |
 [PROPOSED] signed                                 [TODAY] durable
 capability-lock verifier                         sessions/receipts/files
        |                                                  |
        | lock digest in run receipt                        |
        v                                                  |
 [PROPOSED EXTERNAL RUNTIME -- not Bench internals]        |
  +----------------------+  +---------------------------+  |
  | VM runner / broker   |  | policy, identity, action  |  |
  | exact argv + cwd     |  | and credential proxies    |  |
  | stdin/out/err/status |  | outside generated code    |  |
  +----------+-----------+  +-------------+-------------+  |
             | vsock / explicit artifact paths          |  |
             v                                           |  |
  +----------------------------------------------------+  |  |
  | [PROPOSED] replaceable per-run Linux microVM      |  |  |
  | unprivileged user; immutable root; COW work/state |  |  |
  | terminal and optional Chromium/Wayland/AT-SPI     |  |  |
  | no ambient secrets; all egress through proxy      |--'  |
  | guest seccomp+LSM; host jailer+cgroups+quotas     |     |
  +----------------------------------------------------+     |
             | artifacts, observations, exit status         |
             `-----------------------------------------------'
```

The VM boundary should be exposed as an ordinary execution contract: exact
argv, physical working directory, optional exact stdin, separate stdout and
stderr artifacts, exit status, cancellation, and an explicit receipt path. It
must never require Bench to interpolate user text into a shell command or
import VM-runner internals.

The default Linux/KVM backend should be evaluated against Firecracker with its
jailer. Kata is a reasonable alternate backend where Kubernetes/containerd
integration is the primary constraint. Firecracker does not filter guest
network traffic itself, so host-side egress enforcement is part of the
boundary, not an optional hardening step.

## Signed capability lock

The lock is proposed. It is a canonical, immutable description of available
authority, separate from the mutable plan and from the outcome contract. An
illustrative v1 payload is:

```json
{
  "schema": "bench.capability-lock/v1",
  "suite": {"sha256": "..."},
  "agent": {"definition_sha256": "..."},
  "vm": {
    "profile": "browser-worker",
    "image": "registry.example/bench-worker@sha256:...",
    "kernel_sha256": "...",
    "snapshot_sha256": "..."
  },
  "tools": [
    {"name": "ply", "sha256": "..."},
    {"name": "web", "sha256": "..."}
  ],
  "skills": [{"name": "example", "sha256": "..."}],
  "mounts": [
    {"source_id": "workspace-7", "target": "/work", "mode": "rw"}
  ],
  "network": {
    "default": "deny",
    "rules": [
      {"service": "docs", "origin": "https://example.com", "methods": ["GET"]}
    ]
  },
  "identity": {
    "workload": "spiffe://example/bench/worker",
    "token_audiences": ["action-proxy"]
  },
  "approvals": {
    "policy_sha256": "...",
    "consequential_actions": "exact-action"
  },
  "resources": {
    "vcpus": 2,
    "memory_mib": 4096,
    "pids": 512,
    "disk_mib": 16384,
    "wall_seconds": 3600
  },
  "verifier": {"source": "controller", "sha256": "..."},
  "model": {"provider": "...", "name": "..."},
  "update": {"channel": "stable", "tuf_root_sha256": "..."}
}
```

Required lock behavior:

- Canonicalize deterministically, hash with SHA-256, and sign a DSSE/in-toto
  envelope with an organizational or Sigstore identity. Use TUF metadata for
  discovery, delegation, expiry, revocation, and rollback protection.
- Resolve images, tools, skills, policies, and snapshots by digest. A mutable
  tag may aid discovery but must not be an execution identity.
- Verify before VM creation. A missing, expired, revoked, malformed, or
  mismatched lock fails closed without starting guest code.
- Bind the lock digest, contract envelope ID, agent-definition digest,
  workload identity, and VM instance ID into the run receipt. A material
  authority change requires a new lock and a new admitted run binding.
- Do not put signing keys in the guest. The guest may produce unsigned
  artifacts; a controller-side signer signs only after checks and policy pass.
- A valid signature proves publisher identity and bytes, not safety. Release
  evals remain mandatory.
- No field in the lock can declare task completion. Only the configured
  controller-owned check and its sealed receipt can do that.

## VM profiles

All four profiles are proposed. Every profile shares an immutable signed root,
an unprivileged guest user, separate COW work/state volumes, no ambient host
environment, a host-side jailer and cgroup v2 limits, guest seccomp plus an
LSM, a vsock control channel, default-deny egress, and external credentials.

| Profile | Added surface | Intended use | Forbidden by default |
|---|---|---|---|
| `terminal-minimal` | Shell, editor, compiler/runtime selected by the lock; no display server | Repository work, data transforms, checks, constrained code execution | Browser, arbitrary network, host home, credentials, signing keys |
| `browser-worker` | Headless Chromium, Playwright, DOM/accessibility snapshots, downloads quarantine, screenshots and trace capture | Research and web workflows; structured operations first, screenshot coordinates as fallback | Reused personal profile, extensions, arbitrary downloads execution, consequential submit without an exact approval |
| `desktop-worker` | Headless Wayland compositor, AT-SPI, browser plus selected GUI applications, screenshot/video stream | Cross-application OS tasks and visual verification | Host desktop control, USB/device passthrough, clipboard bridge, unrestricted account access |
| `trusted-build` | Pinned build toolchains, larger CPU/RAM/disk budget, pre-fetched sources or narrow repository mirror | Reproducible release candidates and SBOM/provenance generation | Production deploy credentials, artifact-signing keys, general browsing, unpinned dependency fetches |

Browser storage state is bearer-like sensitive data. If persistence is needed,
it is encrypted outside the guest, scoped to one task/account, materialized
only after authorization, and excluded from reusable snapshots. Snapshot
restore must regenerate randomness, machine ID, workload credential, network
lease, and session binding.

## Identity, actions, and recovery

The target records four distinct identities: the requesting human or service,
the agent-definition digest that proposed work, the attested VM/process that
executed it, and the task/delegation grant that limited what it could do.
A short-lived SPIFFE SVID may authenticate the workload to external proxies;
audience-bound OAuth tokens or connector credentials remain in the proxy or
vault.

An approval is valid only for canonical action bytes: operation, target,
arguments or diff, account, data leaving the boundary, contract revision,
capability-lock digest, expiry, and reversibility. Any change invalidates it.
The existing May/Action property is the model: proposed browser or connector
adapters must preserve the same exactness as separate programs rather than
teaching Bench a new approval path.

The durable effect state is:

```text
planned -> policy-allowed -> approved? -> dispatched
        -> acknowledged -> observed -> verified
                       `-> unknown-effect
```

`unknown-effect` blocks the relevant serialization key and is never retried
automatically. Recovery queries the destination with a stable idempotency or
correlation key; absent decisive evidence, a person resolves it. A checkpoint
may restore conversation or filesystem state, but it cannot roll back an
external effect.

Tend remains the local default for one-process durability. A future multi-host
workflow system, if needed, is an optional external adapter; it is not a Bench
daemon. The durable session/effect log remains outside disposable VMs, and
model compaction contains stable pointers to contracts, artifacts, receipts,
open approvals, unresolved effects, and the next objective.

## Persistent memory

Governed cross-session memory is proposed for P1. It has four tiers:

1. trusted agent definition and policy;
2. complete run/session evidence;
3. curated long-term memories with provenance and validity;
4. ephemeral retrieval/cache and guest workspace state.

Each memory has a source run/artifact, namespace and subject, trust class,
created/valid/expiry times, correction or conflict links, and a
`candidate -> quarantined -> verified -> active` lifecycle. User confirmation
or Hone's verified fail-to-pass recovery can promote a candidate. Web pages,
email, remote tool results, and subagent prose cannot directly become trusted
procedural memory. Forgetting, correction, TTL, namespace ACLs, and a complete
promotion receipt are required before calling the memory layer shipped.

## Roadmap

Each item is a separately verified vertical slice. Later priorities do not
authorize a P0 implementation to widen Bench's current boundary.

### P0: credible unattended local worker

1. Specify and implement a standalone capability-lock verifier and signed
   `terminal-minimal` image.
2. Add an ordinary VM runner that preserves exact argv/stdin/out/err/status
   and binds its receipt to the admitted contract and lock digest.
3. Enforce host mounts, cgroups, secret-free launch, default-deny egress, and a
   credential/action proxy with workload and delegated-user identity.
4. Deliver `browser-worker` with Playwright/accessibility-first operations,
   screenshot fallback, downloads quarantine, trace artifacts, and exact
   point-of-risk approvals.
5. Prove crash recovery and unknown-effect handling, then ship signed staged
   image updates with canary health checks and rollback.
6. Export redacted OTLP from existing receipts without adding a private Bench
   event store or making telemetry authoritative.

### P1: standout worker product

- Deliver `desktop-worker`, content-addressed artifact previews, and an
  operator cockpit that remains fully usable in the existing 80x24 no-colour,
  no-mouse TUI; a richer local web view is optional and derived.
- Add governed memory with provenance, review, correction, forgetting, and
  poisoning tests while preserving Hone as the strongest automatic promotion
  path.
- Enforce multi-agent delegation envelopes containing a contract subset,
  tool/network/mount scopes, memory namespace, write lease, budget, deadline,
  cancellation handle, required evidence, and return schema. Give independent
  writers separate VMs/worktrees and hard quotas.
- Deliver `trusted-build`; emit SBOM and provenance in the guest, verify and
  sign outside it.

### P2: ecosystem and fleet reach

- Add optional A2A at remote independently operated agent boundaries, AG-UI
  for frontend events/steering, and MCP Apps for sandboxed tool-owned UI.
- Add a replaceable multi-host workflow/placement adapter without changing
  the local ordinary-process contract or making exactly-once claims.
- Evaluate measured boot, hardware-backed workload attestation, federated
  identity, and confidential-compute profiles only for deployments whose
  threat model justifies them.
- Add signed extension distribution and continuous remote-tool/schema drift
  review; discovery or marketplace listing never grants authority.

## Acceptance gates

Security and authority gates are zero-tolerance deterministic gates. Capability
benchmarks are measured against an accepted baseline under the identical lock;
they are not allowed to excuse a security regression.

| Gate | Required executable evidence | Blocks release when |
|---|---|---|
| G0 — current semantics | `go test ./...`; fake-executable/state-machine tests need no model, network, or credentials; model prose never creates completion | Any current contract, approval, receipt, exit-status, or 80x24 behavior regresses |
| G1 — lock | Canonicalization/signature vectors; wrong digest, mutable-only reference, expired/revoked signer, changed policy/tool/image, and downgrade cases all fail before launch | A mismatched or unverifiable capability set starts a VM or produces a run receipt |
| G2 — isolation/resources | Host read/write sentinels, inherited-env sentinel, symlink/hard-link escape, device, namespace, syscall, PID bomb, memory, CPU, disk, and I/O tests | Guest reaches an ungranted host resource, bypasses egress, or exceeds a hard quota without a typed failure |
| G3 — identity/secrets | Scan environment, `/proc`, filesystems, artifacts, logs, and reusable snapshots; revoke a live grant; verify every action receipt binds user, workload, task, contract, and lock | Long-lived credential material enters the guest/model, revocation is ineffective, or an action lacks accountable identity |
| G4 — approvals/effects | Modify each field after approval; inject duplicate delivery; kill before and after dispatch/acknowledgement; reconcile via idempotency key | Stale approval executes, an unrecorded-effect window exists, or unknown work is retried automatically |
| G5 — browser/computer | Deterministic login, navigation, download, upload, form, cross-origin, modal, and screenshot-fallback tasks; direct/indirect prompt-injection and allowed-domain exfiltration corpus | A consequential action occurs without exact approval, untrusted content widens authority, or required trace artifacts are absent |
| G6 — recovery | Kill VM, runner, browser, proxy, verifier, and controller at every durable transition; resume from external evidence | Sole history lived in disposable compute, a completed check is invented, or effect state cannot be conservatively classified |
| G7 — memory | Write/execute/forget poisoning corpus; namespace/ACL, correction, expiry, conflict, and deletion cases; verify promotion receipts | Untrusted observation becomes active procedural memory without allowed promotion or deletion/correction is unverifiable |
| G8 — multi-agent | Attempt authority, mount, network, memory, budget, writer, and trust escalation from every child; cancel parent/child independently | A child exceeds its signed delegation, two writers bypass the lease, or child prose is treated as verified evidence |
| G9 — supply/update | Reproducible image build, SBOM, SLSA provenance, signature and TUF verification, staged canary, schema migration, failed-health rollback | Unsigned/downgraded/mixed content boots, a key is present in guest, or rollback cannot restore the last accepted release |
| G10 — UX/audit | TUI-only walkthrough at 80x24 with colour disabled and mouse unplugged; export and independently hash-check an audit bundle | Approval target/data/identity is hidden, live work becomes an opaque spinner, or a consequential verdict exists only in transient UI |

## Evaluation matrix

All reported runs record the capability-lock digest, VM image, CPU/RAM/I/O,
network policy, concurrency, timeout, model, tool and prompt bundle, trials,
cost, latency, and confidence interval. Infrastructure is part of an agentic
evaluation and must not vary silently.

| Suite | Profiles | What it measures | Cadence and ship rule |
|---|---|---|---|
| Bench/filter regression and frozen Auto corpora | all | Existing process, contract, approval, verifier, routing, and UI semantics | Every commit; G0 must pass |
| Boundary and chaos corpus | all | G1–G6 and G9 hard properties, duplicate effects, crash recovery | Every runtime/image release; zero deterministic violations |
| Terminal-Bench plus project-owned fresh terminal tasks | `terminal-minimal`, `trusted-build` | End-to-end terminal completion, resource efficiency, artifact correctness | Nightly and model/image change; no security failure and no capability regression beyond the declared release tolerance |
| OSWorld 2 pinned release | `desktop-worker` | Long-horizon GUI and cross-application completion | Candidate release; report success, steps, latency, cost, and interventions |
| BrowserGym/WebArena | `browser-worker`, `desktop-worker` | Browser state, DOM/visual fallback, cross-site tasks, evaluator correctness | Candidate release; compare only under identical lock and site release |
| TheAgentCompany subset | browser/desktop/build | Workplace workflows spanning browser, code, programs, files, and communication | Milestone and quarterly; outcome graders plus transcript review |
| Prompt-injection, malicious MCP/tool, and memory-poisoning corpora | browser/desktop/memory | Unauthorized action, exfiltration, trust escalation, persistent compromise | Every security, connector, browser, or memory change; zero unauthorized deterministic effects |
| Inspect AI release harness | applicable profile | Reproducible multi-trial execution, tool approval, sandbox and external-agent comparison | Release candidate; logs retained and graders human-calibrated |
| Update canary | all | Signature/provenance policy, boot health, task drain/checkpoint, forward/backward state compatibility, rollback | Every published image; promotion only after successful rollback drill |

Public benchmark scores are diagnostic, not completion authority. A release
must also pass project-owned tasks drawn from real failures, deterministic
end-state checks, sampled transcript review, and mutation tests that ensure a
worker cannot pass by weakening or gaming the verifier.

## Primary sources

### Agent execution, computer use, and long-running work

- Anthropic, [Scaling Managed Agents: Decoupling the brain from the hands](https://www.anthropic.com/engineering/managed-agents)
- Anthropic, [How we contain Claude across products](https://www.anthropic.com/engineering/how-we-contain-claude)
- Anthropic, [Effective harnesses for long-running agents](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents)
- Anthropic, [Computer use tool](https://platform.claude.com/docs/en/agents-and-tools/tool-use/computer-use-tool)
- OpenAI, [Computer use](https://developers.openai.com/api/docs/guides/tools-computer-use)
- OpenAI Agents SDK, [Sandbox Agent concepts](https://openai.github.io/openai-agents-python/sandbox/guide/), [human-in-the-loop](https://openai.github.io/openai-agents-python/human_in_the_loop/), and [tracing](https://openai.github.io/openai-agents-python/tracing/)
- Temporal, [Workflow execution and event history](https://docs.temporal.io/workflows)
- LangGraph, [Persistence](https://docs.langchain.com/oss/python/langgraph/persistence) and [fault tolerance](https://docs.langchain.com/oss/python/langgraph/fault-tolerance)

### Isolation, identity, and policy

- Firecracker, [design](https://github.com/firecracker-microvm/firecracker/blob/main/docs/design.md) and [production host setup](https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md)
- Kata Containers, [architecture](https://github.com/kata-containers/kata-containers/blob/main/docs/design/architecture/README.md)
- Linux kernel, [Landlock](https://www.kernel.org/doc/html/latest/userspace-api/landlock.html), [seccomp](https://kernel.org/doc/html/latest/userspace-api/seccomp_filter.html), and [cgroup v2](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html)
- NIST NCCoE, [Software and AI Agent Identity and Authorization concept paper](https://www.nccoe.nist.gov/sites/default/files/2026-02/accelerating-the-adoption-of-software-and-ai-agent-identity-and-authorization-concept-paper.pdf)
- SPIFFE, [concepts](https://spiffe.io/docs/latest/spiffe/concepts/) and [Workload API](https://spiffe.io/docs/latest/spiffe-specs/spiffe_workload_api/)
- Open Policy Agent, [deployment](https://www.openpolicyagent.org/docs/deploy)

### Protocols and UI

- MCP, [2026-07-28 changelog](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2026-07-28/changelog.mdx), [Tasks extension](https://github.com/modelcontextprotocol/ext-tasks), [security practices](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/docs/2026-07-28/tutorials/security/security_best_practices.mdx), and [authorization security](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2026-07-28/basic/authorization/security-considerations.mdx)
- A2A, [v1.0 specification](https://github.com/a2aproject/A2A/blob/main/docs/specification.md)
- AG-UI, [agent-user event protocol](https://github.com/ag-ui-protocol/ag-ui)
- MCP Apps, [embedded tool UI extension](https://github.com/modelcontextprotocol/ext-apps)
- Microsoft Research, [Magentic-One](https://www.microsoft.com/en-us/research/articles/magentic-one-a-generalist-multi-agent-system-for-solving-complex-tasks/) and [Magentic-UI](https://www.microsoft.com/en-us/research/wp-content/uploads/2025/07/magentic-ui-report.pdf)

### Memory, evaluation, and supply chain

- [MemSecBench](https://arxiv.org/abs/2607.27080), [environment-injected trajectory memory poisoning](https://arxiv.org/abs/2604.02623), and [MPBench](https://arxiv.org/abs/2606.04329)
- Anthropic, [Demystifying evals for AI agents](https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents) and [infrastructure noise in agentic coding evals](https://www.anthropic.com/engineering/infrastructure-noise)
- UK AI Security Institute, [Inspect AI](https://inspect.aisi.org.uk/) and [tool approval](https://inspect.aisi.org.uk/approval.html)
- [Terminal-Bench 2.0](https://arxiv.org/abs/2601.11868), [OSWorld 2](https://github.com/xlang-ai/OSWorld-V2), [BrowserGym](https://github.com/ServiceNow/BrowserGym), [WebArena](https://github.com/web-arena-x/webarena), and [TheAgentCompany](https://github.com/TheAgentCompany/TheAgentCompany)
- bootc, [transactional upgrades and rollback](https://bootc.dev/bootc/upgrades.html)
- [The Update Framework](https://theupdateframework.github.io/specification/latest/), [SLSA build provenance](https://slsa.dev/spec/v1.2/build-provenance), [Sigstore verification](https://docs.sigstore.dev/cosign/verifying/verify/), and [CycloneDX](https://cyclonedx.org/specification/overview/)
