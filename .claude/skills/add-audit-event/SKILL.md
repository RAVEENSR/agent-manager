---
name: add-audit-event
description: Add a semantic audit event to agent-manager-service (the Go control plane). Use when adding or changing an operation that touches credentials, permissions, membership, deployment or deletion — API keys, tokens, secrets, role/group changes, user lifecycle, deploy/promote/delete, gateway trust config — or when the build fails with "cannot derive an action for audited route". Covers action registration, the fail-open vs fail-closed decision, redaction rules, and the test helper that operations refusing to run unrecorded require.
---

# Add a semantic audit event (agent-manager-service)

**Read first:** `agent-manager-service/AGENTS.md` → "Audit logging", and `agent-manager-service/docs/audit-logging.md` for the full design.

## First: do you need this at all?

Every route registered through `RouteRegistrar`, and every MCP tool registered through `addTool`, is **already audited**. That record names the actor, org, action, resource, outcome and source. Most changes need nothing.

You need this skill only for one of these:

| Situation | What to do |
|---|---|
| Build fails: `cannot derive an action for audited route` (a route with no `rbac.Permission`) | Step 1 only — add an `actionOverrides` entry |
| The route's permission does not describe what it does | Step 1 only |
| The operation touches credentials, privileges, membership, deployment or deletion | All steps |
| Everything else | Nothing. Stop here. |

The test is whether the envelope answers the forensic question. `POST .../permissions/add → 200` does not say *which permission* was granted, so it needs a semantic event. `PUT .../agents/{name}` returning 200 is self-describing, so it does not.

## Step 1 — name the action

Actions read `<resource>:<verb>`. Reuse an existing constant if one fits; add to `audit/actions_domain.go` if not.

**The same action must be used by every surface that performs the operation** — REST, MCP, internal, background. `TestDomainActionsMatchRouteDerivedActions` fails the build if a semantic emit and its route disagree, because a split action silently halves every query.

If the route derives the wrong label, add one line to `actionOverrides` in `audit/policy.go`, keyed by the exact registrar pattern:

```go
"POST /orgs/{orgName}/identities/roles/{roleID}/permissions/add": "role:grant-permission",
```

## Step 2 — register class, severity and detail schema together

All three go in `audit/actions_domain.go`, in the same place, so they cannot drift apart:

```go
const ActionThingRotate Action = "thing:rotate"

// inside init()
registerCredential(ActionThingRotate, map[string]FieldKind{
    "ownerName": KindName,
    "keyName":   KindName,
})
```

Helpers: `registerCredential` (credential class, always critical) and `registerIdentity` (identity class, always critical). Otherwise call `Register(action, class, severity)` and `RegisterDetailSchema(action, fields)` directly.

| Class | Use for | Severity |
|---|---|---|
| `ClassCredential` | keys, tokens, secrets, OAuth clients, trusted issuers | Critical |
| `ClassIdentity` | users, groups, roles, permission assignment | Critical |
| `ClassDeployment` | build, deploy, promote, lifecycle state | Notice–Warning |
| `ClassConfig` | everything else that mutates | Info–Warning |

**A registered action with no detail schema fails `TestRegisteredActionsHaveDetailSchemas`.** An empty map is a valid answer; skipping the decision is not.

## Step 3 — choose fail-open or fail-closed

This is the decision that matters. Get it from the consequence, not the convenience.

**Fail-closed — `audit.Begin` / `attempt.Complete`.** Use when a live credential or a privilege change would otherwise exist with no trace of who caused it. The intent record is written *before* the change; if that write fails, the operation is refused.

```go
attempt, err := audit.Begin(ctx, audit.ActionThingRotate,
    audit.Org(ouID),
    audit.ResourceNamed(audit.ResourceAPIKey, ownerID, keyName),
    audit.Project(projName),
    audit.Environment(envID),
    audit.Detail("ownerType", audit.APIKeyOwnerAgent),
    audit.Detail("keyName", keyName),
)
if err != nil {
    return nil, err // do NOT perform the operation
}

resp, err := s.doTheThing(ctx, ...)
attempt.Complete(ctx, err)
return resp, err
```

A record left at `outcome: "unknown"` means the process died mid-operation. That orphan is deliberate forensic signal, not a defect.

**Fail-open — `audit.Record`.** Use for frequent operations that issue no credential: builds, console test keys, gateway configuration, monitor lifecycle. Blocking CI on the audit path costs more than it protects.

```go
err := s.doTheThing(ctx, ...)
audit.Record(ctx, audit.ActionThingUpdate,
    audit.Org(ouID),
    audit.ResourceNamed("thing", id, name),
    audit.Result(err),
)
```

**`audit.RecordAncillary`** — for a fact *about* how a request was handled (an authorization bypass, a rate-limited rejection) rather than what it did. Unlike `Record`, it does not suppress the coverage-tier record, so you keep both.

**Emit from the service, not the controller.** Controllers handle HTTP; a fail-closed emit is domain behaviour. The one exception is the identity surface, which has no service layer at all (see the documented exception in `agent-manager-service/AGENTS.md` → Layering) — do not treat it as precedent for new code.

In a controller, use `beginAuditOrFail(w, r, operation, failureMessage, action, opts...)`: it writes the 503 and logs for you, so the refusal cannot be forgotten or answered with the wrong status.

## Step 4 — what may go in the record

- **Never a request or response body.** Bodies are structurally out of reach; that is what makes auditing every route safe. Name the fields you want instead.
- **`audit.Detail` records `string`, `bool`, `int`, `int32`, `int64`, `float64`, `[]string` and `fmt.Stringer`.** Anything else becomes a `[unsupported:<type>]` marker instead of being serialised. Pass the field you mean rather than relying on that.
- **Never a secret value.** Use `audit.SecretRef(key, value)` (SHA-256 prefix + last four) or record the key *name*.
- **Free-form maps: keys only.** Use `audit.AttributeKeySummary(attrs)` — it returns sorted key names, a count, and a flag when a key looks credential-shaped. Never the values.
- **URLs: declare them `KindURL`.** Pass the URL as-is; the kind is what strips userinfo, query and fragment at redaction, so a token in `?access_token=` or an `https://user:pass@host/` cannot reach a record. A test fails if a detail whose name ends in `uri`, `url` or `endpoint` is declared as anything else.
- **Identifiers and scope strings are fine, and usually the point.** `role:grant-permission` records the granted scopes in full; they are identifiers, not credentials.
- Keys not in the action's schema are dropped and reported under `_droppedKeys`.

## Step 5 — test it

Services that fail closed refuse to run without a recorder, so a bare `context.Background()` makes them fail by design (`audit: recorder unavailable`).

```go
// Exercising the operation: install a discarding recorder.
resp, err := svc.RotateThing(auditableCtx(t), ...)

// Asserting the refusal itself: bare context.
_, err := svc.RotateThing(context.Background(), ...)
require.ErrorIs(t, err, audit.ErrRecorderUnavailable)
```

`auditableCtx(t)` lives in `services/audit_testing_test.go`. Do not redeclare it.

To assert on the records themselves, write the test inside the `audit` package — `audit.NewMemorySink()` is a test double declared in `audit/sink_doubles_test.go` and is not importable from `services/` or `controllers/`. See `audit/actions_domain_test.go`. From another package, assert the behaviour (did the operation proceed or refuse) rather than the record.

## Done checklist

- [ ] Action reuses the constant its route derives (or `actionOverrides` makes them agree).
- [ ] Class, severity and detail schema registered together in `audit/actions_domain.go`.
- [ ] Fail-open vs fail-closed chosen from the consequence, and a fail-closed site aborts on error.
- [ ] No body, no secret value, no struct or map in `audit.Detail`.
- [ ] Tests exercising the path use `auditableCtx(t)`.
- [ ] `make test-unit` passes — including `TestDomainActionsMatchRouteDerivedActions`, `TestRegisteredActionsHaveDetailSchemas` and `TestEveryMutatingRouteIsAudited`.
- [ ] `docs/audit-logging.md` semantic-event table updated if you added an action.
- [ ] CI lint clean: `golangci-lint run --config .github/linters/.golangci.yaml ./...`
