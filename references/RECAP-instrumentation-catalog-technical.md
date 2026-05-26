c
Audience: someone joining the team, or anyone reviewing the architecture in detail. Assumes Helm, Kubernetes, Go, React, and the AMP codebase.

## Problem in one paragraph

The set of `amp-instrumentation` versions a running install accepts was frozen at AMP product build time in three places: a TS const in the Console, a comma-separated env (`OTEL_SUPPORTED_INSTRUMENTATION_VERSIONS`) on the server, and a platform-default env. A newly-published `amp-instrumentation 0.3.0` could not be selected without rebuilding both the server image and the Console bundle, then bumping the AMP product version. The PR moves that set into a runtime catalog the server owns, exposes it over the API, and rewires the Console to fetch it. Operators add versions via Helm values.

## Component map

```
+-------------------------------------------------------------------+
|                       Customer's K8s cluster                      |
|                                                                   |
|  +--------------------+     +-------------------------------+     |
|  | values.yaml        |     | Helm chart                    |     |
|  | defaultInstr...    |---->| renders 2 ConfigMaps + 1      |     |
|  | additionalInstr... |     | Deployment + checksum anno    |     |
|  +--------------------+     +---------------+---------------+     |
|                                             |                     |
|                                             v                     |
|                            +---------------------------------+    |
|                            | agent-manager-service pod       |    |
|                            |                                 |    |
|                            |  /etc/amp/instrumentation-      |    |
|                            |    extension.yaml  (mount)      |    |
|                            |  OTEL_DEFAULT_INSTR...  (env)   |    |
|                            |                                 |    |
|                            |  +---------------------------+  |    |
|                            |  | go binary                 |  |    |
|                            |  |  embed baseline.json      |  |    |
|                            |  |  + parse extension YAML   |  |    |
|                            |  |  = effective catalog      |  |    |
|                            |  +-------------+-------------+  |    |
|                            +----------------|----------------+    |
|                                             |                     |
|        +------------------+                 |                     |
|        | Console (browser)|--- GET /agent-build-options ---+      |
|        | useAgentBuild... |<-- {python, instrumentation} -+      |
|        +------------------+                                       |
+-------------------------------------------------------------------+
```

Three trust boundaries in that picture: the Helm value-to-ConfigMap rendering (operator → chart), the file mount + env read (chart → pod), and the HTTP API (pod → Console).

## Where things live (code)

| Concern | Path |
|---|---|
| Catalog package (Version struct, Load, accessors, package-level Default) | `agent-manager-service/instrumentation/catalog.go` |
| Embedded baseline | `agent-manager-service/instrumentation/baseline.json` (generated) |
| Baseline generator | `agent-manager-service/scripts/gen_instrumentation_baseline.go` |
| Wire providers (catalog, supported-python, default-python, controller) | `agent-manager-service/wiring/wire.go` |
| New controller | `agent-manager-service/controllers/agent_build_options_controller.go` |
| Route registration | `agent-manager-service/api/agent_build_options_routes.go` |
| Pair validation, normalisation, effective-version resolution | `agent-manager-service/services/agent_manager.go` (`buildpackPythonVersion`, `validateEffectivePythonInstrumentationPair`, `validatePythonInstrumentationPair`, `lookupAgentAutoInstrumentation`) |
| Chart values | `deployments/helm-charts/wso2-agent-manager/values.yaml` (`agentManagerService.config.otel.defaultInstrumentationVersion`, `additionalInstrumentationVersions`) |
| Extension ConfigMap template | `deployments/helm-charts/wso2-agent-manager/templates/agent-manager-service/instrumentation-extension-configmap.yaml` |
| Deployment volume mount + checksum anno | `deployments/helm-charts/wso2-agent-manager/templates/agent-manager-service/deployment.yaml` |
| Console types | `console/workspaces/libs/types/src/api/instrumentation.ts` |
| Console API client | `console/workspaces/libs/api-client/src/apis/agent-build-options.ts` |
| Console query hook | `console/workspaces/libs/api-client/src/hooks/agent-build-options.ts` |
| Create-agent form | `console/workspaces/pages/add-new-agent/src/forms/InternalAgentForm.tsx` |

## Catalog model

`Version` is the on-disk shape (in `baseline.json` and the extension YAML) and the in-memory shape:

```go
type Version struct {
    Version         string   `json:"version"          yaml:"version"`
    TraceloopSDK    string   `json:"traceloopSdk"     yaml:"traceloopSdk"`
    PythonVersions  []string `json:"pythonVersions"   yaml:"pythonVersions"`
    ImageRepository string   `json:"imageRepository"  yaml:"imageRepository"`
    Source          string   `json:"source"           yaml:"-"`  // "bundled" | "extension"
}
```

`Catalog` is the merged effective set:

```go
type Catalog struct {
    versions       []Version
    defaultVersion string
    byVersion      map[string]Version
}
```

The merge is union by `version`, with extension entries overwriting bundled entries on collision. That collision is the mechanism for redirecting `imageRepository` to an internal mirror without losing other fields.

`All()` and `Get()` return defensive copies (the slice is fresh and `PythonVersions` is cloned) so a caller iterating the catalog cannot mutate the shared state. `SetCatalog(nil)` panics so a boot-order bug fails at the source.

## Boot sequence (Wire DI)

```
docker entrypoint
       |
       v
config.GetConfig()             <-- reads env, including
       |                            OTEL_DEFAULT_INSTRUMENTATION_VERSION
       |                            and INSTRUMENTATION_EXTENSION_PATH
       v
wiring.InitializeAppParams
       |
       v
ProvideInstrumentationCatalog
   |
   |---> instrumentation.Load(extensionPath, defaultVersion)
   |        |
   |        |---> decodeBaseline()    (embed.FS read of baseline.json)
   |        |---> readExtension(...)  (yaml.Unmarshal of mounted file)
   |        |---> merge + dedup (extension wins)
   |        |---> assert default in effective set  -> error if not
   |        +---> return *Catalog
   |
   |---> validateDefaultCoversBuildpackPython(cat)
   |        +---> ensure default entry's PythonVersions
   |              overlaps utils.SupportedPythonVersions()
   |
   +---> instrumentation.SetCatalog(cat)
                                       <-- legacy validator boundary
                                           in services/agent_manager.go
                                           reads through GetCatalog()
       |
       v
ProvideDefaultPythonVersion
       |
       +---> panic if hardcoded "3.11" not in utils.SupportedPythonVersions()
       |
       v
ProvideAgentBuildOptionsController(catalog, supportedPython, defaultPython)
       |
       v
api.MakeHTTPHandler  -- registers /api/v1/orgs/{org}/agent-build-options
       |
       v
http.Server.ListenAndServe
```

Three startup checks together: (1) default exists in the catalog, (2) default's `pythonVersions` overlaps the buildpack-supported Python set, (3) hardcoded default Python is in that same set. Any of them failing returns an error from the Wire build, the app fails to start, and the pod logs the message. `helm upgrade` then reports the rollout as failed.

## Request path: `GET /agent-build-options`

```
Browser (Console)
  |
  | useAgentBuildOptions({ orgName })  // React Query, staleTime 5min
  | -> GET /api/v1/orgs/{orgName}/agent-build-options
  v
HTTP server (agent-manager-service)
  |
  | JWT middleware (existing org-scoped auth)
  v
AgentBuildOptionsController.GetAgentBuildOptions
  |
  | catalog.All()  -> defensive copy
  | sort newest-first via compareVersionsDesc (numeric component compare)
  v
{
  "python": {
    "defaultVersion":    "3.11",                   // hardcoded constant
    "supportedVersions": ["3.10","3.11","3.12","3.13"]  // utils.Buildpacks
  },
  "instrumentation": {
    "defaultVersion": cat.Default(),
    "versions": [{ version, pythonVersions }, ...]      // catalog entries
  }
}
```

Versions are sorted with a small numeric-component comparator (`controllers/agent_build_options_controller.go:compareVersionsDesc`). Lex sort would invert `0.10.0` and `0.2.1`; the comparator parses each dot-separated component as an int and falls back to lex on non-numeric segments so the function stays total.

The endpoint is org-scoped to match `/orgs/{orgName}/catalog`'s routing pattern even though the response is identical across orgs. Saves a new auth surface.

## Helm chart mechanics

### Two ConfigMaps

The chart already had one ConfigMap for env vars. We added a second one for the extension YAML.

```yaml
# templates/agent-manager-service/instrumentation-extension-configmap.yaml
data:
  instrumentation-extension.yaml: |
    additionalInstrumentationVersions:
{{ toYaml .Values.agentManagerService.config.otel.additionalInstrumentationVersions | indent 6 }}
```

Always rendered, even when the list is empty. An empty list serialises to `[]` and the loader treats it as a no-op. Two ConfigMaps instead of one because the env ConfigMap is read via `envFrom`, but the extension is a file that the pod mounts and the Go process reads. Different access patterns, different ConfigMaps.

### The pod mount

```yaml
# templates/agent-manager-service/deployment.yaml (excerpts)
spec:
  template:
    metadata:
      annotations:
        checksum/instrumentation-extension: {{ include (print $.Template.BasePath "/agent-manager-service/instrumentation-extension-configmap.yaml") . | sha256sum }}
    spec:
      containers:
        - name: agent-manager-service
          volumeMounts:
            - name: instrumentation-extension
              mountPath: /etc/amp
              readOnly: true
      volumes:
        - name: instrumentation-extension
          configMap:
            name: {{ ... }}-instrumentation-extension
```

The `checksum/instrumentation-extension` annotation is what makes `helm upgrade` actually roll the pod when the extension list changes. Without it, Kubernetes sees no change to the Deployment spec, and an update to a mounted ConfigMap doesn't trigger a pod restart on its own. The server reads the catalog at startup; without a restart, the new entries don't take effect.

### Surface presented to the operator

```yaml
# values.yaml (excerpts)
agentManagerService:
  config:
    otel:
      defaultInstrumentationVersion: "0.2.1"
      additionalInstrumentationVersions: []
      # Example:
      #   - version: "0.4.0"
      #     traceloopSdk: "0.65.0"
      #     pythonVersions: ["3.10","3.11","3.12","3.13"]
      #     imageRepository: "my-mirror.example/amp-python-instrumentation-provider"
```

Two values, both under the existing `otel` block. `defaultInstrumentationVersion` is read by the env-ConfigMap template and rendered as `OTEL_DEFAULT_INSTRUMENTATION_VERSION`. `additionalInstrumentationVersions` is read by the new ConfigMap template.

For internal-registry installs, the operator sets `imageRepository` to their registry. The server constructs the full image tag from `imageRepository + ":" + version + "-python" + chosenPython` at agent deploy time.

## Server-side validation

Two paths reach `validateEffectivePythonInstrumentationPair`: `CreateAgent` and `UpdateAgentBuildParameters`. Both:

1. Compute `buildpackPythonVersion(req.Build)`. The helper normalises the input (strips patch suffix, trims whitespace, exact-matches the language string against `"python"`). Returns empty for non-python buildpack agents.
2. Determine the *effective auto-instrumentation* setting. On create: from the request, default true. On update: from the request if set, else from `agent_configs.enable_auto_instrumentation` via a new helper `lookupAgentAutoInstrumentation`.
3. Gate the pair check on `requested != nil || autoInstr`. The reasoning: if a version is pinned, intent must be consistent. If auto-instrumentation is on, the default will be injected as an init-container, so the (python, default) pair must be valid. If neither is true, no init-container will run, so the pair is moot.
4. If the gate fires and no version is pinned, resolve the effective version: catalog default on create, agent's pinned-or-default on update.
5. Validate that the effective version's `pythonVersions` contains the bare-minor Python.

On `CreateAgent`, the call sits *after* the kind-based build replacement (`req.Build = modelBuildToSpecBuild(sourceComponent.Build)`), so kind-based agents are validated against the build that actually deploys, not the request's empty build.

## Console form behaviour

State machine for the create-agent form's python and instrumentation fields:

```
mount
  |
  | initial: { languageVersion: undefined, instrumentationVersion: undefined }
  v
useAgentBuildOptions fetches /agent-build-options
  |
  | loading -> dropdowns disabled
  |
  +-- error -> error helpertext, no defaults seeded
  |
  v
data arrives
  |
  | seed effect:
  |   if languageVersion == null || not in supportedVersions:
  |     languageVersion = options.python.defaultVersion
  |   if instrumentationVersion == null:
  |     instrumentationVersion = options.instrumentation.defaultVersion
  v
user picks Python X.Y
  |
  | reset effect:
  |   compat = options.instrumentation.versions filter pythonVersions includes X.Y
  |   if current instr in compat: no-op
  |   else:
  |     if default in compat: instr = default
  |     elif compat not empty: instr = compat[0]   (newest-first)
  |     else:                   instr = null
  v
render
  |
  | if compat empty:
  |   "No AMP-provided instrumentation available for Python X.Y..."
  |   form remains submittable with instrumentationVersion = null
  | else:
  |   dropdown lists compat
```

The seed effect's stale-value handling matters for one specific scenario: a customer's IT admin runs `helm upgrade` that drops a Python version from the buildpack list, and a user has the form open at that moment. React Query refetches eventually (5-minute staleTime); when it does, the form's previously-seeded `languageVersion` may no longer be supported. The effect resets it on the next data tick instead of letting the user submit a stale value.

## Edge cases handled

- **Lex sort drift at multi-digit minors.** `0.10.0` lex-sorts below `0.2.1` because `'1' < '2'` at index 2. `compareVersionsDesc` handles this with int parsing per component.
- **Patch suffix in LanguageVersion.** A client posting `"3.11.4"` would otherwise produce a 400 ("does not support python 3.11.4") even though the catalog supports 3.11. `buildpackPythonVersion` collapses to bare-minor.
- **Capital-P language.** `"Python"` would have taken different branches in `isPythonBuildpack` (which uses `==`) and in `buildpackPythonVersion` (which previously used `EqualFold`). Aligned both to `==` so an agent isn't half-configured.
- **SetCatalog race under -race.** Tests install their own `NewForTest` catalogs in parallel with Wire-installed ones. `sync.RWMutex` makes the access race-free.
- **Default outside buildpack Python.** A misconfigured override where the default's `pythonVersions` doesn't overlap the buildpack set would let the server boot but make the create-agent form useless. Caught by `validateDefaultCoversBuildpackPython` at boot.
- **Default Python pruned from buildpack list.** The hardcoded `"3.11"` would silently advertise an unsupported value. `ProvideDefaultPythonVersion` panics at boot if `3.11` is missing from `utils.SupportedPythonVersions()`.
- **Update path skipping pair-check.** The previous version of the update validator missed it entirely; the current version mirrors create's gate and reads the agent's persisted `EnableAutoInstrumentation` when the request doesn't override.

## CI guard

```
.github/workflows -> Test job -> go test ./...
                                    |
                                    v
            TestHelmDefaultInstrumentationVersionConsistent
                                    |
                                    | reads deployments/helm-charts/.../values.yaml
                                    | parses agentManagerService.config.otel.defaultInstrumentationVersion
                                    | decodes embedded baseline.json
                                    | asserts the default appears in the baseline
                                    v
                          fail with actionable message:
                "chart default 'X.Y.Z' is not in the embedded baseline ...;
                 add the entry to .github/release-config.json and run
                 `make gen-instrumentation-baseline` before bumping the chart default"
```

The buildpack-python overlap check that the Wire provider runs at boot is intentionally not duplicated in this test: pulling `utils` into the test would drag `config.init()` and its DB env requirements into a package that currently has a clean dep graph. The runtime check still catches it at `helm install` time.

## Maintainer flow (cutting AMP 1.1 with default 0.3.0)

1. Cut `amp-instrumentation 0.3.0` upstream (PyPI + ghcr images). Existing process, separate workflow.
2. Add the entry to `.github/release-config.json` (the build matrix source of truth).
3. `cd agent-manager-service && make gen-instrumentation-baseline` to regenerate the embedded baseline.json. Commit it alongside the release-config.json change.
4. Bump `defaultInstrumentationVersion` in `values.yaml` to `"0.3.0"`.
5. Update the docs version table.

The CI guard fails the PR if steps 3 and 4 disagree. The runtime guards fail the helm rollout if the resulting binary disagrees with the resulting chart. Three layers of catch.

## What's not in scope

- A WSO2-hosted remote catalog poll. The proposal sketches one. The hybrid (bundled + extension) closes the air-gap-safe path on its own, so the remote layer is deferred.
- An admin UI in the Console for adding catalog entries. Today the only path is Helm. A UI is reasonable for future iterations but would need persistence (DB), RBAC, and a story for race conditions between Helm-managed and UI-managed entries.
- General buildpack-version distribution. Adding Java 25 or NodeJS 24 still requires a code change in `utils.Buildpacks`. The same B11-style problem applies to every language; that's a sibling proposal.
- Hot reload of the extension file. Operators trigger updates via `helm upgrade`, which rolls the pod via the checksum annotation. No fsnotify watcher.

## Pointers

- Design spec: `references/2026-05-25-instrumentation-catalog-m1-design.md`
- Implementation plan: `references/2026-05-25-instrumentation-catalog-m1-plan.md`
- Operator docs: `documentation/docs/administration/instrumentation-catalog.mdx`
- Maintainer runbook: `python-instrumentation-provider/RELEASING.md` (Scenario A)
- PR: https://github.com/wso2/agent-manager/pull/960
