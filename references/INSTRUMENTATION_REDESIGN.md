# Auto-Instrumentation Redesign — Brainstorm, Approaches & Roadmap

## Context

AMP today delivers Python auto-instrumentation by bundling **one** library (Traceloop / OpenLLMetry) at **one** pinned version range and baking it into a platform-controlled init container. The team has hit recurring failures rooted in this single decision — most recently `cannot import name 'GenAICustomOperationName' from 'opentelemetry.semconv_ai'`, caused by transitive-dependency drift between the bundled OpenLLMetry build and what landed at runtime. The bundled-by-platform model is no longer paying for itself: customers can't escape it, and we own the upgrade burden for everyone.

This is a brainstorming document. It frames the problem, lays out **all the realistic design approaches with pros/cons**, recommends a path that fits the **GA target (3rd week of June 2026)**, and stages the rest as a post-GA roadmap.

The user's three fundamentals:
1. Support traces/spans from **all** instrumentation libraries (OpenLLMetry, OpenInference, OpenLIT, vanilla OTel GenAI).
2. Customer / developer does **minimal work**.
3. **No reinvention** — we lean on the upstream ecosystem.

---

## Problem Statement

Four concrete failure modes, all rooted in the same architectural assumption: **AMP owns the SDK choice, the version, and the upgrade cadence on behalf of every customer.**

### F1 — Bundled-vs-customer compatibility skew

OpenLLMetry depends on transitive packages (e.g., `opentelemetry-semconv-ai`) whose surface changes between minor versions. We pin `traceloop-sdk>=0.47.0,<=0.60.0` in two places (`python-instrumentation-provider/requirements.txt:3` and `libs/amp-instrumentation/pyproject.toml:15`). When the customer's own dependency tree resolves a different transitive that conflicts at import time, we get errors like `GenAICustomOperationName` — emitted **inside** an instrumentor-init at agent startup, often silently degrading agent behavior.

The customer cannot opt-out of our pin; we cannot adapt quickly to their environment.

### F2 — Forward-migration risk on the platform side

When AMP wants to raise the OpenLLMetry version, **every customer is moved at once**. We must also re-verify that `traces-observer-service/opensearch/process.go` still extracts attributes correctly under the new version's emitted shape. This is serial, fragile, and slow — the kind of upgrade work that gets indefinitely deferred.

### F3 — Vendor lock-out for non-OpenLLMetry users

A customer who has standardized on **OpenInference** (Arize Phoenix instrumentations) or **OpenLIT** cannot use AMP's UI properly today. Their spans land in OpenSearch (the gateway accepts any valid OTLP traffic with a valid `x-amp-api-key`) but the observer does not detect `openinference.span.kind` or OpenLIT's scope name, so spans are classified as `kind: unknown` (`process.go:1664`) and rendered without rich UI affordances. Worse, evaluators rely on `AmpAttributes` shape and so cannot run on these spans.

The user's note in the prompt confirms this: "the experience is not broken as the traces/spans are published and shown as normal logs … but customer won't be able to do evaluations on them."

### F4 — No safety hatch

There is no per-agent override for instrumentation provider or version. The only escape is to disable auto-instrumentation entirely (`EnableAutoInstrumentation: false` in `agent_manager.go:285`). That removes the bundled SDK injection but leaves the customer with just a bare endpoint+API-key, with no documented path for plugging in their own SDK.

---

## Scope

**In scope.**
- Instrumentation **delivery** mechanism (init container for platform-hosted; CLI wrapper for externally-hosted; Python only for now).
- **Observer** schema-tolerance — accepting and rendering spans from OpenLLMetry / OpenInference / OpenLIT / vanilla OTel GenAI semconv.
- **Per-agent configuration** model so customers can pick provider and version.

**Out of scope.**
- Trace storage and OpenSearch indexing.
- Evaluation pipeline (only depends on getting correctly shaped spans to the observer).
- AI Gateway, LLM Proxy, Console rendering of brand-new span kinds (called out as follow-up where relevant).
- Non-Python languages — same pattern can extend later.

---

## Guiding Principles (the three fundamentals, restated as rules)

1. **Schema-tolerant ingest.** The observer must understand spans from any reasonable GenAI instrumentation library. Anchor on OTel GenAI semconv as the primary schema; vendor extensions (Traceloop, OpenInference, OpenLIT) are translation layers on top.
2. **Default = zero work, escape hatch always available.** Most customers should change nothing today and keep working. An "advanced" pathway must exist for customers who need per-agent control.
3. **No upstream reinvention.** AMP does not fork OpenLLMetry / OpenInference / OpenLIT. Where we add code, it is *adapter logic* (server-side mapping) or *wiring* (per-agent config). We benefit from upstream evolution for free.

---

## Current State (key file map)

Some of these are already half-built seams the redesign just needs to *use*.

| Concern | File | Note |
|---|---|---|
| Init container image build | `python-instrumentation-provider/Dockerfile:33` | Single-provider image; sets `INSTRUMENTATION_PROVIDER=otel-tracing` |
| Init container script | `python-instrumentation-provider/setup-instrumentation.py:21` | **Already has multi-provider plumbing** via `INSTRUMENTATION_PROVIDER` env var — copies from `/instrumentations/{provider}` |
| Platform-hosted Traceloop init | `python-instrumentation-provider/sitecustomize.py:9` | Hardcoded `from traceloop.sdk import Traceloop` |
| External CLI Traceloop init | `libs/amp-instrumentation/.../initialization.py:115` | Same hardcode |
| SDK pin (platform image) | `python-instrumentation-provider/requirements.txt:3` | `traceloop-sdk>=0.47.0,<=0.60.0` |
| SDK pin (external CLI pkg) | `libs/amp-instrumentation/pyproject.toml:15` | Same pin |
| Init container image tag | `agent-manager-service/clients/openchoreosvc/client/components.go:1944-1950` | Hardcoded `amp-python-instrumentation-provider:{version}-python{pyver}`, tag from platform-wide `AMP_VERSION` |
| Trait selection logic | `agent-manager-service/services/agent_manager.go:285-303` | **Already supports** `EnableAutoInstrumentation` flag, switches between OTEL trait and env-injection trait |
| OTEL trait (injects SDK) | `deployments/.../python-otel-instrumentation-trait.yaml` | Uses init container; takes `instrumentationImage` as parameter |
| Env injection trait (BYO foundation) | `deployments/.../instrumentation-trait-env-injection.yaml` | **Already exists** — sets `AMP_OTEL_ENDPOINT` + `AMP_AGENT_API_KEY` only |
| Span type detection | `traces-observer-service/opensearch/process.go:1592-1665` | `DetermineSpanType` priority cascade. Already 80% OTel GenAI standard; **no OpenInference / OpenLIT branches** |
| AmpAttributes shape | `traces-observer-service/opensearch/types.go:61-67` | Stable contract with Console; `kind` enum already vendor-neutral |
| CrewAI special case | `traces-observer-service/opensearch/crewai_process.go` | Hardcoded extraction; isolated |

The pin and the hardcoded library import live in only **four files**. The init-container "provider" concept already exists in plumbing but is exposed as one default. The env-injection trait is already a clean BYO foundation. The observer is already mostly OTel-GenAI-standard.

---

## Delivery mechanism — how the SDK actually gets installed

Before evaluating approaches: there's a separate axis from "which framework do we support" — *how* the chosen SDK physically lands in the agent's Python process. The two hosting models differ on who runs `pip install` and when.

### Platform-hosted agents (built and run by AMP)

The init container is responsible for putting the SDK on the agent's `PYTHONPATH`. Two delivery options:

- **Pre-installed in image** (status quo). The SDK is baked into the init container at image-build time. Fast pod startup, but every (provider, version) tuple needs its own pre-built image — that's a maintenance matrix.
- **Runtime `pip install`** (recommended). One base init container with Python + pip. At pod startup it reads `INSTRUMENTATION_PROVIDER` and `INSTRUMENTATION_VERSION` env vars and runs `pip install --target=/otel-tracing-sdk <provider>==<version>`. **Single image, any version, no matrix.**

Trade-offs of the runtime approach:
- Adds ~10–30s to pod cold start (network round-trip + dependency resolution).
- Requires PyPI reachability from the init container (or a private mirror in air-gapped envs).
- Both costs are paid only at pod creation/restart — steady-state runtime is unaffected.

For AMP's workload profile (long-running agents, not FaaS), this trade-off is acceptable and the simplification dominates. We document PyPI reachability as a deployment requirement.

### Externally-hosted agents (run on customer's infrastructure)

The customer already controls their venv and runs `pip` themselves. The architectural fix is just to **stop pinning Traceloop in `amp-instrumentation`**:

- Today, `libs/amp-instrumentation/pyproject.toml:15` declares `traceloop-sdk>=0.47.0,<=0.60.0` as a hard dependency. If the customer wants Traceloop 0.65 (or whatever's compatible with their LangChain), pip refuses to resolve.
- After: drop the upper bound (or move the dependency to an optional extra). Document in the package README: "install `amp-instrumentation` alongside your preferred OpenLLMetry version; we don't pin it."
- The bootstrap (`_bootstrap/initialization.py:115`) does `from traceloop.sdk import Traceloop` — it picks up whatever the customer's venv has. No code change needed there.

The customer's "version selector" for externally-hosted is just their own `requirements.txt`. AMP doesn't need a Console UI for it — the customer is already in full control.

**Symmetry.** In both models the customer ends up running their preferred Traceloop version. The mechanism differs (init container does `pip install` for platform-hosted; customer does `pip install` for externally-hosted), but the customer outcome is identical.

---

## Design space — five approaches, with pros and cons

I'll line up every realistic approach (including the do-the-minimum and do-the-most ones) so the team can pick a point on the spectrum rather than feel like there's only one answer.

For each, I'll grade against:
- **F1–F4** (which failure modes it fixes)
- The 3 fundamentals (✓ / partial / ✗)
- **GA feasibility** (can this realistically ship by 3rd week of June 2026?)
- **Maintenance cost** (ongoing burden after ship)

### Approach A — Schema-tolerant observer + BYO escape hatch

The minimum that materially improves the situation. Two components.

**(A1) Observer schema tolerance.** Extend `process.go` to detect OpenInference (`openinference.span.kind`) and OpenLIT (scope-name based). Add per-vendor adapter functions that map their attributes into the existing `AmpAttributes` shape. Strengthen the unknown bucket so any OTel-GenAI-standard span gets useful Input/Output/TokenUsage even if no vendor matches.

**(A2) BYO mode promoted to first-class.** Today's `instrumentation-trait-env-injection` is undocumented infrastructure. Promote it: expose "BYO instrumentation" as an explicit toggle in the agent config, ship a thin `amp-otel-helper` PyPI package that does the OTLP+headers wiring (one import, one call), document it. Customer using OpenInference/OpenLIT does `pip install openinference-instrumentation-langchain` and adds two lines.

| Failure modes addressed | F3 ✓, F4 ✓, F1 partial (customer can opt-out + BYO), F2 unchanged |
|---|---|
| Fundamentals | #1 ✓ (any library now renders), #2 partial (BYO is some work), #3 ✓ |
| GA feasibility | **Yes** — A1 is one engineer ~3 weeks; A2 is wiring + a 200-line PyPI package |
| Maintenance | Low — observer adapter table grows linearly per vendor; BYO has no AMP-side image pipeline |

**Pros.** Smallest change that meaningfully shifts the architecture. No DB migration. No multi-image build pipeline. Each piece ships independently. Doesn't break any existing customer.

**Cons.** Customers using OpenInference / OpenLIT do small one-time integration work — not zero-code. Still leaves Traceloop as the only "managed" provider. F1 (compat skew) is fixed only by the customer choosing BYO; default-mode customers still hit pins.

### Approach B — Per-agent provider+version with full managed parity

Fully execute the three pillars from the original sketch.

- DB migration adding per-agent `instrumentation_mode`, `provider`, `version`.
- Single base init container image (per Python version) does runtime `pip install <provider>==<version>` based on per-agent config — no pre-built matrix.
- `sitecustomize.py` and `initialization.py` rewritten as dispatchers that pick which SDK to init based on `INSTRUMENTATION_PROVIDER`.
- Console UI for per-agent provider+version selection.
- Customer gets zero-code instrumentation regardless of which library they pick.
- Wrinkle: OpenInference is per-library (`openinference-instrumentation-langchain`, `-openai`, ...). "Managed OpenInference" needs a second decision — which sub-packages to install — that doesn't apply to OpenLLMetry/OpenLIT (single-SDK).

| Failure modes addressed | F1 ✓, F2 ✓, F3 ✓, F4 ✓ |
|---|---|
| Fundamentals | #1 ✓, #2 ✓, #3 partial (we maintain per-provider dispatcher logic) |
| GA feasibility | **No** for full scope. Realistic minimum 6–8 weeks even with runtime `pip install` — DB + UI + per-provider dispatcher + cross-vendor integration testing |
| Maintenance | Medium — one base init container; per-provider sitecustomize logic + observer adapter; OpenInference sub-package strategy |

**Pros.** Best customer ergonomics. Cleanly separates SDK lifecycle from platform lifecycle. F1, F2, F3, F4 all properly resolved.

**Cons.** Doesn't fit the GA window. We own three vendor adapters in CI as well as in the observer. OpenInference's per-library shape is a real complication.

### Approach C — Hybrid: Traceloop managed (versioned), others BYO

Middle ground.

- Per-agent **Traceloop version** picker — DB migration adds `instrumentation_version` column. Init container does runtime `pip install traceloop-sdk==<version>` at pod startup; no pre-built matrix.
- Console UI: an "OpenLLMetry version" input when in Managed mode (default pre-filled).
- OpenInference / OpenLIT supported via BYO (Approach A2's helper package + docs).
- Externally-hosted: drop the upper-bound pin in `libs/amp-instrumentation/pyproject.toml:15` so customer's `requirements.txt` decides the version.
- Observer schema tolerance from A1 still ships.

| Failure modes addressed | F1 ✓ (customer picks compatible Traceloop version), F2 ✓ (per-agent pinning, no forced migrations), F3 ✓ (via BYO), F4 ✓ |
|---|---|
| Fundamentals | #1 ✓, #2 ✓ for Traceloop / partial for others, #3 ✓ |
| GA feasibility | **Yes** — runtime `pip install` removes the matrix constraint. A1 in ~2 weeks + version selector in ~2 weeks + BYO + hardening fits 6.5 weeks |
| Maintenance | Low — one base init container image; observer adapter table grows per vendor |

**Pros.** Solves all four failure modes for the *most common* path (Traceloop) without the full multi-provider burden. Aligns with industry direction — Arize Phoenix and Traceloop's own products treat their default vendor as managed and others as BYO.

**Cons.** Two-tier UX: managed users have a dropdown; BYO users have docs and a helper. Customers may be confused why OpenInference isn't a clickable choice. We may end up doing Approach B post-GA anyway, so this is "Approach B with the easy half first."

### Approach D — Fully decoupled: AMP owns the gateway, never the SDK

Burn down the bundling entirely. Even Traceloop becomes optional.

- Remove all init container injection (or keep it as a deprecated convenience that users opt **into**).
- AMP just provides an OTLP endpoint + `x-amp-api-key`. Customers set up their own instrumentation.
- The `amp-otel-helper` package does endpoint/auth setup; the customer brings their preferred library.
- Observer schema-tolerance still ships (from A1).

| Failure modes addressed | F1 ✓, F2 ✓ (no platform pin to migrate), F3 ✓, F4 ✓ |
|---|---|
| Fundamentals | #1 ✓, #2 ✗ (regression — current managed users now have to do work), #3 ✓✓ (max anti-reinvention) |
| GA feasibility | **No** — removing the managed default is a breaking change for all current customers. |
| Maintenance | Very low |

**Pros.** Architecturally cleanest. AMP becomes a pure observability backend. Zero ongoing version-pinning headache.

**Cons.** Major UX regression for current customers. Effectively a deprecation of "auto-instrumentation," which is part of AMP's pitch. Not GA-friendly.

### Approach E — Status quo + observer-only

Just ship A1 (observer schema tolerance). Don't touch the SDK delivery.

| Failure modes addressed | F3 ✓, F4 partial (still nothing documented), F1 ✗, F2 ✗ |
|---|---|
| Fundamentals | #1 ✓, #2 unchanged, #3 ✓ |
| GA feasibility | **Yes** easily |
| Maintenance | Very low |

**Pros.** Minimum effort. Unblocks customers who already disable auto-instrumentation and run their own SDK.

**Cons.** Leaves the core complaint (F1, the version pin) untouched. Doesn't fix the reason this brainstorm started.

### Comparison at a glance

| Approach | F1 | F2 | F3 | F4 | Fund#1 | Fund#2 | Fund#3 | GA-feasible | Maint cost |
|---|---|---|---|---|---|---|---|---|---|
| **A — Observer + BYO** | partial | ✗ | ✓ | ✓ | ✓ | partial | ✓ | yes | low |
| **B — Full managed parity** | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | partial | no | medium |
| **C — Hybrid (Traceloop managed, others BYO)** | ✓ | ✓ | ✓ | ✓ | ✓ | ✓/partial | ✓ | **yes** | low |
| **D — Fully decoupled** | ✓ | ✓ | ✓ | ✓ | ✓ | ✗ | ✓✓ | no | very low |
| **E — Status quo + observer** | ✗ | ✗ | ✓ | partial | ✓ | unchanged | ✓ | yes | very low |

(Maintenance grades reflect the runtime `pip install` delivery model — no pre-built image matrix to maintain in any approach.)

### Adjacent option worth naming — Approach F: Build-time compatibility resolution

A wildcard: when AMP builds an internal agent from a Git repo, resolve a compatible OpenLLMetry version from the customer's `requirements.txt` automatically and bake a per-build init container. Zero customer work, zero version-pin pain.

- Pros: best of all worlds for internal agents. Solves F1 elegantly.
- Cons: only applies to internal agents (externally-hosted unaffected); requires a known compatibility matrix that we'd have to maintain; adds build-time complexity and failure modes; smells of reinvention.
- GA feasibility: not by GA. Worth flagging as a potential post-GA enhancement layered on top of B or C.

---

## Recommendation: **C for GA, expand to managed OpenLIT in Q3, OpenInference in Q4 (conditional)**

**For GA (3rd week of June):** Approach C — observer schema-tolerance (A1) + per-agent **OpenLLMetry version selector** for managed mode + BYO promoted as first-class + `amp-instrumentation` unpinned for externally-hosted.

The runtime `pip install` delivery model (see "Delivery Mechanism" above) is what makes this realistic in 6.5 weeks — there is no pre-built image matrix to build or maintain. With C in this form:

- **F1** is resolved directly: customer picks the OpenLLMetry version compatible with their stack. Both platform-hosted (Console input → init container `pip install`) and externally-hosted (customer's `requirements.txt` is authoritative) are covered.
- **F2** is resolved: per-agent version pinning means platform default bumps don't drag everyone along; existing agents stay where they are.
- **F3** is resolved via observer schema-tolerance plus first-class BYO mode (covers OpenInference / OpenLIT / vanilla OTel-GenAI users until those become managed in later quarters).
- **F4** is resolved: BYO is documented and trivially toggled in Console.

**Post-GA Q3 (July–September 2026):** Add **OpenLIT** as a second managed provider.

OpenLIT also has a single `init()` call, so adding it on top of the GA work is small: one extra provider entry in the dispatcher + a provider dropdown in Console. The runtime `pip install` mechanism stays the same — `pip install openlit==<version>` instead of `pip install traceloop-sdk==<version>`.

**Post-GA Q4 (October–December 2026) — conditional on demand:** Add managed **OpenInference** if there is real customer demand (≥3 customers asking).

OpenInference is per-library (no single SDK), so it needs more design care than OpenLIT — likely a sub-package selector in Console (which of `langchain`, `openai`, `llama-index`, etc. to instrument), or build-time auto-detection from the customer's stack.

This staging respects fundamental #3 (don't reinvent more than demand justifies), gives a credible roadmap to show customers, and defers the highest-cost / lowest-clarity work (OpenInference's per-library shape) until validated.

---

## GA Milestone Plan (today → 3rd week of June 2026, ~6.5 weeks)

> Working assumption: 1 backend engineer + occasional UI/docs help. Adjust dates if staffing differs.

### Milestone 1 — Observer schema-tolerance (Weeks 1–2, May 8–22)

Drops vendor lock-out for any spans we receive — independent of the SDK delivery work, so it ships value first.

- Refactor `traces-observer-service/opensearch/process.go` so `DetermineSpanType` consults a vendor-adapter table.
- Implement `openinferenceAdapter` (kind detection from `openinference.span.kind`; attribute extraction from `llm.*`, `embedding.*`, `tool.*`, `retrieval.*`, `input.value`, `output.value`).
- Implement `openlitAdapter` (kind detection from `otel.scope.name LIKE 'openlit%'`; mostly reuses OTel-GenAI-standard extraction).
- Strengthen the unknown bucket: even when no vendor matches, populate `Input` / `Output` / `TokenUsage` from `gen_ai.input.messages` / `gen_ai.output.messages` / `gen_ai.usage.*` if present.
- Capture real fixture spans from each vendor into `traces-observer-service/testdata/`.
- Tests: fixture-driven unit tests per adapter; integration test that ingests one fixture per vendor and asserts `AmpAttributes` shape.
- Decide GA handling for OpenInference's `GUARDRAIL` and `EVALUATOR` kinds — proposal: map both to `tool` for GA; revisit post-GA when Console UI is designed.

**Exit criteria:** all tests green; manual trace UI smoke passes for OpenInference + OpenLIT spans from a sample agent.

### Milestone 2 — OpenLLMetry version selector (Weeks 2–4, May 15–Jun 5)

Customer picks any Traceloop version; AMP installs it at pod startup. Externally-hosted customer's `requirements.txt` becomes authoritative.

**Platform-hosted:**
- DB migration: add `instrumentation_version` column to `agent_configs`. Backfill existing agents to the current pin (resolve to a concrete version, e.g. `0.60.0`) so nothing breaks.
- Update `python-instrumentation-provider/Dockerfile`: switch from pre-installed packages to a base image with Python + pip.
- Update `python-instrumentation-provider/setup-instrumentation.py`: read `INSTRUMENTATION_VERSION` env var and run `pip install --target=/otel-tracing-sdk traceloop-sdk==$INSTRUMENTATION_VERSION` at startup, then dispatch to the existing `sitecustomize.py` initialization.
- Update `agent-manager-service/clients/openchoreosvc/client/components.go:1944-1950`: parameterize the init container image tag so we can ship a single base image (no longer keyed off `AMP_VERSION` for SDK content).
- Update `agent-manager-service/services/agent_manager.go:285-303`: read `instrumentation_version` from agent config and pass through as env var via the trait.
- Update `deployments/.../python-otel-instrumentation-trait.yaml` to plumb the new env var.
- Console UI: add an "OpenLLMetry version" input on the agent config page, default pre-filled. Optional: PyPI lookup for autocomplete / typo catch.

**Externally-hosted:**
- Drop the upper-bound pin in `libs/amp-instrumentation/pyproject.toml:15` (`traceloop-sdk>=0.47.0` instead of `>=0.47.0,<=0.60.0`); document that customer's `requirements.txt` is the source of truth for version.

**Exit criteria:** new agent created with an explicit version; pod starts successfully (cold start <30s); spans flow with that version's instrumentations active. Existing agents continue to work unchanged (backfilled to current default). Externally-hosted user can `pip install amp-instrumentation traceloop-sdk==0.65.0` without conflict.

### Milestone 3 — BYO mode promoted (Week 4–5, Jun 5–12)

Cover OpenInference / OpenLIT / vanilla OTel users until those become managed (Q3+).

- Either ship a thin `amp-otel-helper` PyPI package or a documented one-page setup recipe — one function call that configures OTLP+headers, leaving instrumentation-library choice to the customer.
- Promote BYO in agent config UI: "Auto-instrumentation" becomes Default (OpenLLMetry, version-pickable) / BYO / Off. (The underlying trait selection in `agent_manager.go:285-303` already supports the wire-up; we just expose the option clearly.)
- Update `documentation/docs/concepts/observability.mdx` with a "BYO Instrumentation" page covering OpenInference, OpenLIT, and vanilla OTel-GenAI quickstarts.

**Exit criteria:** UI toggle reachable; docs reviewed; one customer-facing example agent using OpenInference verified end-to-end.

### Milestone 4 — Hardening (Weeks 5–6, Jun 12–22)

- Smoke deploys: one platform-hosted internal agent + one externally-hosted agent, each tested with (a) default OpenLLMetry, (b) custom OpenLLMetry version, (c) BYO OpenInference, (d) BYO OpenLIT. Trace fidelity + evaluator runs verified for each.
- Cold-start measurements: confirm pod startup time with runtime `pip install` is acceptable (target <30s). If consistently over, fall back to a small pre-built default image for the platform default version, runtime install only when overridden.
- Customer-facing changelog entry, release notes for GA.
- A "compat hint" doc page: known-good (OpenLLMetry version × LangChain version) pairings; pointers to BYO when none fit.
- Buffer for unknowns. **Do not overstuff this milestone.**

**Exit criteria:** GA build cut on schedule; release notes published; customer-facing doc in place.

### What is NOT in GA scope

Naming this explicitly so the team can push back on scope creep:
- Managed OpenLIT or OpenInference (the dispatcher pattern is in place but no second provider ships in GA)
- Migration UX for default-version bumps (informational only in GA)
- Pre-flight compatibility checks (read customer requirements.txt, warn)
- CrewAI extraction generalization
- New AmpAttributes kinds for `GUARDRAIL` / `EVALUATOR`
- Build-time SDK injection into the agent's own image (post-GA optimization if cold-start cost becomes a real problem)

---

## Post-GA Roadmap

### Q3 2026 (July–September) — Add managed OpenLIT

- Extend the per-agent config: add `instrumentation_provider` column (default = `traceloop`).
- Add an OpenLIT branch to the init container's dispatcher: `pip install openlit==<version>` and a small OpenLIT-specific `sitecustomize.py` snippet (one `openlit.init()` call).
- Console UI: provider dropdown alongside the version input.
- Migration UX: when AMP raises a platform default, existing agents keep their pinned (provider, version); Console shows a "newer version available" indicator with one-click upgrade.
- The runtime `pip install` mechanism is unchanged — this is just adding entries to a dispatcher table.

### Q4 2026 (October–December) — OpenInference (conditional)

Decision gate: ≥3 customers explicitly asking for managed OpenInference?

- **If yes** — design how to express which OpenInference instrumentation packages to install (sub-package selector in Console, or build-time auto-detection from the customer's stack). Bundle the most common ones for the default case (`openinference-instrumentation-langchain`, `-openai`, `-llama-index`).
- **If no** — stay on BYO for OpenInference indefinitely.

### Continuous (anytime post-GA)

- Generalize CrewAI extraction (`crewai_process.go`) into the new vendor-adapter pattern when cleanup cost is low.
- Add new AmpAttributes kinds for `GUARDRAIL` / `EVALUATOR` once Console rendering is designed.
- Build-time SDK injection (Approach G — bake the SDK into the agent's own build at the customer-specified version, eliminating both the init container and pod cold-start cost) as a stretch goal if cold-start measurements in M4 reveal a real problem, or if path-shadowing issues recur.
- Approach F (build-time compatibility resolution) as a stretch goal if F1 keeps recurring after C ships.

---

## Open tactical questions (to resolve during M1 or in implementation)

1. **GUARDRAIL / EVALUATOR span kinds.** Map to `tool` for GA; revisit semantically post-GA.
2. **Cold-start budget.** What's the acceptable pod cold-start ceiling with runtime `pip install`? If we measure >30s consistently, do we ship a pre-built default image (current pin) for the default case and only `pip install` on override?
3. **PyPI mirror strategy.** Do AMP's deployment guides assume PyPI reachability from pods, or do we ship a recommended private-mirror config for air-gapped customers?
4. **Default-tracking strategy.** When AMP raises the platform default in Q3+, do we move existing agents (no), warn them (yes), or auto-upgrade after N days (no, post-GA decision)?
5. **`amp-otel-helper` packaging.** Standalone PyPI package or extras on existing `amp-instrumentation`? Extras is simpler; standalone has cleaner naming.
6. **CrewAI generalization timing.** Touch in M1 (risky — bigger refactor) or post-GA (safer, defers cleanup).

---

## Verification

**This document.** Walk the team through it. The pros/cons table is meant to surface disagreements early — if someone thinks Approach D is the right answer, that needs to come out before M1.

**Before M1 ships.** Customer interview with at least one customer who hit F1 or F3. Confirm Approach A unblocks them.

**Per-milestone gates:**
- M1 — observer schema-tolerance tests green; manual trace UI smoke passes for OpenInference and OpenLIT spans.
- M2 — new agent created with explicit OpenLLMetry version; pod cold start <30s; existing agents unchanged after migration; externally-hosted user can install any Traceloop version without conflict.
- M3 — `amp-otel-helper` (or equivalent) shipped; BYO toggle reachable in Console; one OpenInference example agent verified end-to-end.
- M4 — four-way smoke deploy (default Traceloop / custom Traceloop version / BYO OpenInference / BYO OpenLIT) all green; release notes published.

**Post-GA gates:**
- Q3 — managed OpenLIT selectable as a provider; one bump-the-default rehearsal completes without breakage.
- Q4 (if executed) — managed OpenInference smoke deploy green.

---

## Sources

- [OpenTelemetry GenAI semantic conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/)
- [OpenInference Semantic Conventions](https://arize-ai.github.io/openinference/spec/semantic_conventions.html)
- [OpenLIT (GitHub)](https://github.com/openlit/openlit)
- [Traceloop / OpenLLMetry semconv contribution to OTel](https://www.traceloop.com/docs/openllmetry/contributing/semantic-conventions)
