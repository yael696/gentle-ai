# FROZEN — NoClip Autonomous Reverse-Engineering Workflow v1

**Decision date:** 2026-10-24  
**Status:** FROZEN (architecture decision; implementation not claimed)  
**Scope:** NoClip reverse-engineering research workflow, not a general-purpose agent framework  
**Future candidate:** v2, Linux host + isolated Windows guest (NOT APPROVED FOR IMPLEMENTATION)

## Decision

Use **native Codex CLI orchestration on Windows** for largely mechanical, evidence-first Minecraft Bedrock reverse-engineering research. When elevated CDB work is needed, have the non-elevated research orchestrator prepare a bounded handoff for a **separate Codex session started by the operator in an elevated PowerShell**. The operator transfers the result back to the orchestrator.

This is an architecture freeze only. It does **not** mean the skill, automation, elevated agent, or V2 environment is already installed, configured, tested, or running.

## V1 — Windows native (FROZEN)

```text
Research objective
    |
    v
Agent A: Codex research orchestrator (non-elevated)
    |-- native Codex subagents, used selectively
    |-- existing REA / Ghidra read and analysis tools
    |-- existing evidence and BEC provenance rules
    |
    | if privileged runtime observation is necessary
    v
Structured CDB handoff prompt
    |
    | operator starts a separate elevated PowerShell/Codex session
    v
Agent B: scoped CDB executor (elevated; separate session)
    |-- validate target identity and authorized scope
    |-- perform only approved observations
    |-- write raw evidence to authorized local files
    v
Short verified findings + evidence references
    |
    | operator returns the handoff to Agent A
    v
Agent A resumes investigation
```

### Responsibilities

| Actor | Ownership | Authority boundary |
| --- | --- | --- |
| **Agent A — RE orchestrator** | Plan the next evidence-seeking action, delegate bounded independent research, reconcile findings, maintain progress and research checkpoints. | Runs without administrator privileges. Never elevates itself or silently launches an elevated process. |
| **Native Codex subagents** | Explore independent hypotheses, inspect large volumes of evidence, and return concise sourced findings. | Bounded delegated work; no independently evolving orchestration framework. |
| **Agent B — CDB executor** | Execute the authorized runtime investigation described by Agent A's handoff and return results. | Separate elevated PowerShell/Codex session launched by the operator; no broad research mandate. |
| **Human operator** | Start the elevated session, transfer prompts and results, run required Minecraft tests, approve privileged/risky actions, and launch operator-only heavyweight jobs. | No need to make routine research decisions. |
| **BEC / existing evidence store** | Preserve observed/derived/external/unknown claims, source provenance and reproducibility. | Do not modify frozen BEC records or publish private research artifacts. |

### Autonomous research loop

1. Reuse recorded evidence and identify the next unresolved question.
2. Run lightweight bounded analysis inline; delegate broad/independent exploration only when it preserves the parent's context budget.
3. Verify findings against their evidence before promoting claims. Keep **observed**, **derived**, **external**, and **unknown** separate.
4. Persist findings and provenance through already-authorized mechanisms. Store large raw logs externally to the model context.
5. Continue without human selection of routine next steps.

**Context rule:** Send subagents precise questions and required references, not the entire parent transcript. Handoffs contain the finding, evidence location/ID, confidence or unresolved uncertainty, and the next useful action. Context savings are a hypothesis to measure; they do not guarantee lower total token usage.

**Stop conditions:** the objective has enough reproducible evidence; a Minecraft in-game/runtime test needs the operator; privileged execution or approval is required; an operator-only expensive job is required; or progress is genuinely blocked. Do not promise endless execution across session/resource limits, and do not loop on repeated failures.

### CDB handoff contract

Agent A emits a self-contained prompt for Agent B with:

- A single runtime question and the minimum relevant prior findings.
- Target identity requirements (including exact executable/build identity where available) and validation steps.
- Explicit authorized CDB commands or read-only operations, expected outputs, safety checks and stop conditions.
- Evidence output location, required metadata, and a concise return format.
- A prohibition on unrelated work and on unapproved mutation, injection, termination, restart, or persistence.

Agent B must validate the target before attaching and must stop if the scope requires additional approval. The operator reviews elevated actions. A prompt **is not an operating-system security boundary**: the elevated session possesses actual administrator authority. Merely calling a command “read-only” does not eliminate runtime side effects (e.g. attach/pause behavior).

### Existing policies remain in force

- **No new fork**, external orchestrator, scheduler, daemon, automatic resume supervisor, or complex workflow.
- A small optional Codex skill (working name: `bedrock-re`) may describe research behavior; it must not create a new workflow framework.
- Reuse native Codex multi-agent capabilities if present and verified in the installed session; do not assume the tools exist from a prompt alone.
- Existing RE cost escalation and operator-execution policies remain binding: no autonomous heavyweight/full Ghidra analysis, bulk indexing, long builds or other operator-only jobs.
- NoClip's two completed Isolated Reviews (IRs) are historical checkpoints. **Do not automate or trigger an additional IR as part of routine research.** Preserve any existing review obligations for future implementation/release gates.
- Keep public product and private RE/evidence boundaries intact. This public document intentionally includes **no private RVAs, binary hashes, traces, filesystem paths, credentials, or unpublished experimental findings**.

## V1 acceptance experiment (not yet performed)

A scoped existing NoClip research question is a suitable pilot. Assess:

1. Verified evidence-backed research steps completed between operator interventions.
2. Growth of the parent context window and total token usage (measure both).
3. Correct handoff to/from an independently elevated CDB session if required.
4. Compliance with permission, evidence, and heavyweight-job boundaries.
5. Ability to resume from an accurate checkpoint after a session interruption.

Do not claim benchmark success or automation readiness before observing these results.

## V2 — Linux host + VirtualBox Windows guest (PLANNED ONLY)

**Intended direction:** Linux hosts the research orchestrator; a separate Windows guest contains the privileged Codex/CDB executor and, if feasible, the required Minecraft runtime. The goal is to automate the handoff **inside an isolated disposable environment** while protecting the host.

**Not guaranteed:** Running PowerShell as Administrator **inside the guest** does not make execution risk-free. Guest escape, shared folders/disks, credentials, networking and external side effects remain concerns; VM snapshots do not undo changes outside the VM.

Before any V2 implementation, require:

- Verified guest capability for the needed Minecraft/CDB debugging scenario. VirtualBox graphics/runtime compatibility is an **open question**.
- Isolation design covering shared folders, clipboard, credentials, network access and storage.
- Non-privileged host-side controller, guest-only elevation, snapshot/rollback and recovery tests.
- A demonstrated benefit over V1 based on observed bottlenecks, not anticipated complexity.

If VirtualBox cannot support the required target, re-evaluate virtualization technology without silently changing V1.

## Freeze and change control

- **V1 is frozen as an architectural baseline, not declared implemented.**
- Only explicit new decisions may supersede it (e.g. a documented v1.1 amendment or V2 approval).
- This record is deliberately isolated from the fork's upstream-tracking `main` branch and ongoing IR implementation branch.
- Treat this commit's SHA as the immutable evidence of what was decided on 2026-10-24; branch names themselves are movable.
