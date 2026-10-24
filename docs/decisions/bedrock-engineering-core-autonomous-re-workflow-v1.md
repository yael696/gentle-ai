# FROZEN — Bedrock Engineering Core (BEC) Autonomous RE Workflow v1

**Decision date:** 2026-10-24  
**Status:** FROZEN — architectural baseline, not an implemented or validated system  
**Owner/domain:** Bedrock Engineering Core (BEC), Minecraft Bedrock evidence-first reverse engineering  
**First application:** NoClip investigation  
**Future candidate:** V2 with an isolated Windows guest; Linux + VirtualBox first if validated, Windows + VMware or another solution otherwise (NOT APPROVED FOR IMPLEMENTATION)

## Decision

Use **native Codex CLI orchestration** for bounded, largely mechanical Bedrock reverse-engineering research. The primary orchestrator chooses routine next steps and selectively delegates evidence-heavy exploration to native Codex subagents, without creating another orchestration framework.

For a task needing **elevated Windows CDB**, the non-elevated orchestrator (Agent A) creates a self-contained, narrowly scoped prompt. The operator starts **another, separate Codex session in administrator PowerShell** (Agent B), transfers the prompt, and returns its evidence handoff to Agent A. Agent A then resumes.

BEC is the long-term evidence and knowledge backbone. **NoClip is the initial use case, not the name or boundary of this workflow.** The workflow is reusable for future BEC reverse-engineering research without mandating CDB, Windows debugging, or NoClip-specific checkpoints in unrelated investigations.

This document is a freeze of decisions only. It does **not** claim installation, successful autonomous execution, a working skill, runtime tests, or V2 approval.

## V1 — Windows-native pilot (FROZEN)

```text
BEC research objective
      |
      v
Agent A: Codex research orchestrator (non-elevated)
      |-- bounded inline research
      |-- native Codex subagents, selectively and when available
      |-- authorized REA / Ghidra analysis
      |-- evidence-first BEC claim/provenance discipline
      |
      | if elevated CDB observation is actually necessary
      v
Scoped privileged-debugging prompt
      |
      | operator transfers prompt to a separate elevated session
      v
Agent B: Codex CDB executor (administrator PowerShell)
      |-- verify target and scope before attaching
      |-- execute only authorized CDB checks
      |-- preserve output and provenance outside parent context
      v
Concise handoff + evidence references
      |
      | operator returns the handoff
      v
Agent A continues until verified completion or an actual blocker
```

### Responsibilities and boundaries

| Role | Responsibility | Boundary |
| --- | --- | --- |
| **Agent A — BEC RE orchestrator** | Maintain objective, choose useful next investigations, delegate when worthwhile, reconcile evidence, preserve accurate checkpoints. | No administrator privileges; never auto-elevate or start Agent B without the operator. |
| **Native Codex subagents** | Broad mapping, independent hypotheses, bounded read/analysis, concise evidence-based handoffs. | Do not inherit unnecessary entire transcripts; do not become independent framework/scheduler. |
| **Agent B — elevated CDB executor** | Answer the single authorized debugging question; return reproducible CDB observations and evidence paths. | Separate operator-launched elevated Codex; no unrelated investigation or unapproved mutation. |
| **Operator** | Start elevated Codex, transfer prompts/results, approve privileged or risky actions, run in-game tests, and launch heavyweight operator-only jobs. | Routine evidence-navigation decisions remain with Agent A. |
| **BEC evidence system** | Preserve observed / derived / external / unknown claims and verifiable provenance. | Do not change BEC's frozen v1 records or publish private binaries, findings, hashes, traces, or mappings. |

### Autonomous research and context economy

1. Start from existing BEC findings and identify the next unresolved technical question.
2. Conduct small checks inline. Delegate evidence-heavy mapping or independent investigations when that avoids loading raw data into the parent's context.
3. Have subagents return short, verifiable handoffs: finding, evidence ID/path, relevant build/symbol provenance, uncertainty and next suggested check.
4. Process bulky output with scripts or tools; store raw results outside the model conversation, transmitting only bounded summaries and references.
5. Verify claims against primary evidence before treating them as observed. Keep **observed**, **derived**, **external**, **unknown** distinct.
6. Persist allowed findings using existing evidence mechanisms. Continue without requesting a fresh human prompt for routine work.

**Measure effectiveness instead of assuming it:** track parent-context growth, total tokens, verified research progress and human interruptions. Saving parent context is not necessarily saving total tokens.

**Stop conditions:** research objective proven sufficiently; testing in Minecraft requires the operator; authorization or privileged/debugger execution is needed; operator-only heavyweight work is needed; or a genuine unresolved blocker. No infinite loops, invented evidence, or claims that sessions can run indefinitely after resource limits.

### Elevated CDB handoff and previously used command

For the NoClip CDB investigations, the operator reports having started a separate Codex session from an already elevated PowerShell using **exactly**:

```powershell
codex --no-daemon --sandbox danger-full-access --ask-for-approval on-request -c 'features.prefer_mxc=true'
```

This is **historical operator-reported usage**, not a newly tested command or a promise that every flag is accepted by every future Codex CLI release. Before reuse, confirm the current Codex version and actual tool availability. Do not silently discard or reinterpret flags.

Agent A's prompt for Agent B must include the single debugging question; relevant existing evidence; expected target build/process identity and validation; permitted CDB commands/checks; outputs and metadata to save; stop/timeout criteria; and the concise evidence handoff format. Agent B validates the target, stays in scope and stops for new authorization when required.

**Risk boundary:** `danger-full-access` removes Codex sandbox constraints; combined with administrator privileges on the Windows host, it exposes host resources. `--ask-for-approval on-request` does not guarantee an individual approval for every privileged action. A prompt is not a security sandbox; attaching a debugger can itself pause or affect the target. Thus V1 **does not authorize automatic privilege escalation**, unattended host-admin operation, destructive commands, memory changes, injection, termination, or restart.

### Policies unchanged

- **No additional Gentle AI fork**, custom daemon, scheduler, external orchestration framework or auto-restart supervisor.
- An optional lightweight skill (working name `bedrock-re`) may specialize existing native Codex behavior; it is not a new workflow engine.
- Verify native multi-agent capability in the actual Codex session; a prompt alone does not create unavailable tools.
- Preserve BEC cost escalation and operator-only execution: no autonomous heavyweight/full Ghidra analysis, large bulk operations, long builds or other prohibited expensive jobs.
- Existing Isolated Review (IR) obligations remain separate from routine reverse-engineering discovery. NoClip has **two earlier IR checkpoints**; no new IR is automatically triggered by this research workflow.
- Private RE evidence and public product boundaries remain intact. This public architecture record contains no private executable hashes, RVAs, credentials, logs or experimental findings.

## V1 pilot acceptance (NOT YET TESTED)

Use a bounded existing BEC research question (initial candidate: a NoClip unresolved concurrency question) to observe:

1. Evidence-backed research steps completed without an operator choosing routine next actions.
2. Parent context growth **and** overall token consumption.
3. Accurate handoff/re-entry through an elevated CDB Codex session, if needed.
4. Compliance with evidence, security, IR and expensive-job boundaries.
5. Checkpoint quality after a session interruption.

Pilot results must be measured; this freeze does not claim they exist.

## V2 — validated isolated Windows guest (PLANNED ONLY)

**First choice, conditional on successful tests:** Linux host running the BEC research orchestrator, with **VirtualBox hosting an isolated Windows guest** for administrator PowerShell, the Codex CDB executor and (if required and technically feasible) Minecraft.

**Fallback if Linux + VirtualBox is incompatible:** Evaluate **VMware on a Windows host** or another suitable virtualization/isolation solution. We freeze the isolation goal, **not a specific hypervisor or host OS**, until real Minecraft/CDB experiments demonstrate feasibility. Selection should use observed guest compatibility, recoverability, boundary strength and automation needs.

V2's possible improvement is to automate safe orchestrator/executor handoffs **inside a disposable guest**, potentially allowing guest-local PowerShell Administrator by default. This never authorizes running the host orchestrator as administrator. Virtualization reduces some risk but **does not mean zero risk**: guest escape, credentials, writable shared folders/disks, clipboard, networking and external side effects must be considered; snapshots do not roll back the external world.

Before approving V2:

- Test Minecraft Bedrock and the required CDB/debugger workflow in a Linux-hosted VirtualBox Windows guest, including relevant graphics/runtime behavior.
- If that fails, evaluate VMware on Windows, then other alternatives against the same requirements.
- Review guest-host isolation: permissions, network, clipboard, shared storage, secrets and recovery.
- Verify snapshot/rollback and unexpected-failure recovery.
- Demonstrate a real bottleneck in V1 that automation alleviates; avoid speculative workflow complexity.

## Freeze provenance and changes

- This file **supersedes the NoClip-scoped title** by defining the workflow for BEC, retaining NoClip as the first pilot and the source of historical CDB usage.
- The original NoClip-named architectural freeze is preserved in Git history at `ab58ff20d5f0938f66c4f3e42dc732e22871f13a`. That immutable commit is not rewritten.
- V1 is frozen as a design baseline; substantive changes require an explicit documented amendment or approved V2.
- This document belongs to a dedicated documentation branch. No direct modifications to upstream-tracking `main`, the IR implementation branch, BEC code, or NoClip code are implied.
