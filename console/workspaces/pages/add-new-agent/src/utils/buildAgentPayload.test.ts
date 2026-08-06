import { describe, it, expect } from "vitest";
import { buildModelConfig, findLowestEnvironmentName, buildCatalogAgentPayload, deriveCatalogEnvSeed } from "./buildAgentPayload";
import type { CreateAgentFormValues, LLMProviderFormEntry } from "../form/schema";
import type { AgentKindConfigSchemaItem, OrgProjPathParams } from "@agent-management-platform/types";

function entry(over: Partial<LLMProviderFormEntry> = {}): LLMProviderFormEntry {
  return {
    selectedProviderByEnv: { Development: { uuid: "u1", handle: "openai" } },
    urlVarName: undefined,
    apikeyVarName: undefined,
    guardrails: [],
    ...over,
  };
}

describe("buildModelConfig (flat shape)", () => {
  it("maps a single provider with no env overrides", () => {
    const out = buildModelConfig([entry()], "Development");
    expect(out).toEqual([{ providerName: "openai" }]);
  });

  it("uses the provider selected for the lowest environment", () => {
    const out = buildModelConfig([
      entry({
        selectedProviderByEnv: {
          Production: { uuid: "u2", handle: "anthropic" },
          Development: { uuid: "u1", handle: "openai" },
        },
      }),
    ], "Development");

    expect(out).toEqual([{ providerName: "openai" }]);
  });

  it("includes env-var name overrides", () => {
    const out = buildModelConfig([entry({ urlVarName: "MY_URL", apikeyVarName: "MY_KEY" })], "Development");
    expect(out?.[0]).toMatchObject({
      providerName: "openai",
      environmentVariables: [
        { key: "url", name: "MY_URL" },
        { key: "apikey", name: "MY_KEY" },
      ],
    });
  });

  it("preserves guardrail policies in configuration", () => {
    const out = buildModelConfig([entry({
      guardrails: [{ name: "pii", version: "v1", settings: { mode: "block" } }],
    })], "Development");
    expect(out?.[0].configuration?.policies).toEqual([
      { name: "pii", version: "v1", paths: [{ path: "/*", methods: ["*"], params: { mode: "block" } }] },
    ]);
  });

  it("returns undefined when no providers", () => {
    expect(buildModelConfig([], "Development")).toBeUndefined();
  });

  it("returns undefined when no provider is selected for the lowest environment", () => {
    expect(buildModelConfig([entry()], "Production")).toBeUndefined();
  });
});

describe("findLowestEnvironmentName", () => {
  it("returns the source environment that is not a promotion target", () => {
    expect(findLowestEnvironmentName([
      { sourceEnvironmentRef: "Development", targetEnvironmentRefs: [{ name: "Staging" }] },
      { sourceEnvironmentRef: "Staging", targetEnvironmentRefs: [{ name: "Production" }] },
    ])).toBe("Development");
  });

  it("returns undefined when no lowest environment can be resolved", () => {
    expect(findLowestEnvironmentName([])).toBeUndefined();
  });
});

function baseFormData(over: Partial<CreateAgentFormValues> = {}): CreateAgentFormValues {
  return {
    deploymentType: "new",
    enableAutoInstrumentation: true,
    name: "test-agent",
    displayName: "Test Agent",
    description: "",
    repositoryUrl: "https://github.com/wso2/agent-catalog-template",
    branch: "main",
    appPath: "/",
    runCommand: "python main.py",
    language: "python",
    languageVersion: "3.11",
    dockerfilePath: "/Dockerfile",
    interfaceType: "DEFAULT",
    basePath: "/",
    openApiPath: "",
    env: [],
    files: [],
    ...over,
  } as CreateAgentFormValues;
}

describe("buildCatalogAgentPayload", () => {
  const params: OrgProjPathParams = { orgName: "org1", projName: "proj1" };

  // An untouched secret field must be OMITTED from the request, not sent as an
  // empty string, so the backend can tell "left as default" apart from
  // "explicitly cleared" and apply the kind's real default itself.
  it("drops an empty-valued env row instead of sending it as an empty value", () => {
    const data = baseFormData({
      env: [
        { key: "OPENAI_API_KEY", value: "", isSensitive: true },
        { key: "MODEL_NAME", value: "gpt-4", isSensitive: false },
      ],
    });

    const { body } = buildCatalogAgentPayload(data, params, "my-kind", "v1");

    expect(body.configurations?.env?.map((e) => e.key)).toEqual(["MODEL_NAME"]);
  });

  it("keeps an explicit secret override value and marks it sensitive", () => {
    const data = baseFormData({
      env: [{ key: "OPENAI_API_KEY", value: "my-own-key", isSensitive: true }],
    });

    const { body } = buildCatalogAgentPayload(data, params, "my-kind", "v1");

    expect(body.configurations?.env).toEqual([
      { key: "OPENAI_API_KEY", value: "my-own-key", isSensitive: true },
    ]);
  });

  it("drops a whitespace-only value the same way as an empty one", () => {
    const data = baseFormData({
      env: [
        { key: "OPENAI_API_KEY", value: "   ", isSensitive: true },
        { key: "MODEL_NAME", value: "gpt-4", isSensitive: false },
      ],
    });

    const { body } = buildCatalogAgentPayload(data, params, "my-kind", "v1");

    expect(body.configurations?.env?.map((e) => e.key)).toEqual(["MODEL_NAME"]);
  });

  it("trims leading/trailing whitespace off a value that is otherwise kept", () => {
    const data = baseFormData({
      env: [{ key: "OPENAI_API_KEY", value: "  sk-my-key\n", isSensitive: true }],
    });

    const { body } = buildCatalogAgentPayload(data, params, "my-kind", "v1");

    expect(body.configurations?.env).toEqual([
      { key: "OPENAI_API_KEY", value: "sk-my-key", isSensitive: true },
    ]);
  });
});

function schemaItem(
  over: Partial<AgentKindConfigSchemaItem> & { name: string },
): AgentKindConfigSchemaItem {
  return {
    isSecret: false,
    isMandatory: false,
    ...over,
  };
}

describe("deriveCatalogEnvSeed", () => {
  it("seeds a non-secret item's real default value", () => {
    const seed = deriveCatalogEnvSeed([
      schemaItem({ name: "MODEL_NAME", defaultValue: "gpt-4" }),
    ]);

    expect(seed.env).toEqual([{ key: "MODEL_NAME", value: "gpt-4", isSensitive: false }]);
    expect(seed.lockedEnvKeys.has("MODEL_NAME")).toBe(true);
    expect(seed.kindSecretKeysWithDefault.has("MODEL_NAME")).toBe(false);
  });

  // isSecret alone must not be enough to lock the field — a mandatory secret with no
  // default would otherwise look identical to one that has a default, even though
  // there is nothing to fall back on if the user leaves it untouched. Only an item
  // that is BOTH secret AND has a real default should end up locked.
  it("does not lock a secret item that has no default — nothing to fall back on", () => {
    const seed = deriveCatalogEnvSeed([
      schemaItem({ name: "OPENAI_API_KEY", isSecret: true, isMandatory: true }),
    ]);

    expect(seed.env).toEqual([{ key: "OPENAI_API_KEY", value: "", isSensitive: true }]);
    expect(seed.lockedEnvKeys.has("OPENAI_API_KEY")).toBe(true);
    expect(seed.kindSecretKeysWithDefault.has("OPENAI_API_KEY")).toBe(false);
  });

  it("locks a secret item that has a real default, without seeding its value", () => {
    const seed = deriveCatalogEnvSeed([
      schemaItem({ name: "OPENAI_API_KEY", isSecret: true, defaultValue: "••••••••" }),
    ]);

    expect(seed.env).toEqual([{ key: "OPENAI_API_KEY", value: "", isSensitive: true }]);
    expect(seed.kindSecretKeysWithDefault.has("OPENAI_API_KEY")).toBe(true);
  });

  it("returns an empty seed for an empty schema", () => {
    const seed = deriveCatalogEnvSeed([]);

    expect(seed.env).toEqual([]);
    expect(seed.lockedEnvKeys.size).toBe(0);
    expect(seed.kindSecretKeysWithDefault.size).toBe(0);
  });
});
