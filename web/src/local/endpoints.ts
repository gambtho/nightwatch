// The endpoint presets for "choose where your AI runs" (pivot spec,
// "First run" + "Endpoint agnosticism", amended by the board's
// five-preset decision, 2026-08-31): Anthropic, OpenAI, OpenRouter,
// GitHub Models, Azure AI Foundry, plus "another service" and "on this
// computer". Presets are frontend copy plus validation; the endpoint
// record itself is P1's server-side shape, faked in local/config.ts
// until it lands.

export type PresetId =
  "anthropic" | "openai" | "openrouter" | "github" | "azure" | "custom" | "local";

// The P1 endpoint record: {kind, base_url} plus the explicit `local`
// classification — a loopback URL alone never implies local (a tunnel to
// a paid service can sit on localhost); only the "on this computer"
// preset marks it.
export interface EndpointRecord {
  kind: "anthropic" | "openai_compatible";
  preset: PresetId;
  base_url: string;
  local: boolean;
}

export interface CaptureGuide {
  startUrl?: string;
  startLabel?: string;
  steps: string[];
}

export interface EndpointPreset {
  id: PresetId;
  label: string;
  blurb: string;
  kind: EndpointRecord["kind"];
  /** Fixed for the hosted presets; absent when the user supplies one. */
  baseUrl?: string;
  needsBaseUrl?: boolean;
  baseUrlPlaceholder?: string;
  /** "On this computer" only accepts loopback URLs. */
  requireLoopback?: boolean;
  local?: boolean;
  needsKey: boolean;
  /** Accepted key shapes, checked at paste to catch a wrong-string paste. */
  keyPrefixes?: string[];
  keyPlaceholder?: string;
  guide?: CaptureGuide;
  pricingUrl?: string;
  /** Extra budget-screen note where per-token pricing isn't the model. */
  budgetNote?: string;
}

export const ENDPOINT_PRESETS: EndpointPreset[] = [
  {
    id: "anthropic",
    label: "Anthropic",
    blurb: "Claude, with a key from the Anthropic console.",
    kind: "anthropic",
    baseUrl: "https://api.anthropic.com",
    needsKey: true,
    keyPrefixes: ["sk-ant-"],
    keyPlaceholder: "sk-ant-…",
    guide: {
      startUrl: "https://console.anthropic.com/settings/keys",
      startLabel: "Open the Anthropic console",
      steps: [
        "Click Create Key and give it any name.",
        "Copy the key — it starts with sk-ant- — and paste it below.",
      ],
    },
    pricingUrl: "https://www.anthropic.com/pricing",
  },
  {
    id: "openai",
    label: "OpenAI",
    blurb: "GPT models, with a key from the OpenAI platform.",
    kind: "openai_compatible",
    baseUrl: "https://api.openai.com/v1",
    needsKey: true,
    keyPrefixes: ["sk-"],
    keyPlaceholder: "sk-…",
    guide: {
      startUrl: "https://platform.openai.com/api-keys",
      startLabel: "Open the OpenAI platform",
      steps: [
        "Click Create new secret key.",
        "Copy the key — it starts with sk- — and paste it below.",
      ],
    },
    pricingUrl: "https://openai.com/api/pricing",
  },
  {
    id: "openrouter",
    label: "OpenRouter",
    blurb: "Many models behind one key.",
    kind: "openai_compatible",
    baseUrl: "https://openrouter.ai/api/v1",
    needsKey: true,
    keyPrefixes: ["sk-or-"],
    keyPlaceholder: "sk-or-…",
    guide: {
      startUrl: "https://openrouter.ai/keys",
      startLabel: "Open OpenRouter",
      steps: [
        "Click Create Key.",
        "Copy the key — it starts with sk-or- — and paste it below.",
      ],
    },
    pricingUrl: "https://openrouter.ai/models",
  },
  {
    id: "github",
    label: "GitHub Models",
    blurb: "Included with GitHub Copilot plans, via a GitHub token.",
    kind: "openai_compatible",
    baseUrl: "https://models.github.ai/inference",
    needsKey: true,
    keyPrefixes: ["github_pat_", "ghp_"],
    keyPlaceholder: "github_pat_…",
    guide: {
      startUrl: "https://github.com/settings/personal-access-tokens/new",
      startLabel: "Open GitHub token settings",
      steps: [
        "Create a fine-grained personal access token.",
        "Under account permissions, give it Models: read.",
        "Copy the token — it starts with github_pat_ — and paste it below.",
      ],
    },
    budgetNote:
      "GitHub Models usage is included with your GitHub plan rather than billed per call. Tomte still records what it uses and stops at your budget.",
  },
  {
    id: "azure",
    label: "Azure AI Foundry",
    blurb: "Your own Azure resource and key.",
    kind: "openai_compatible",
    needsBaseUrl: true,
    baseUrlPlaceholder: "https://your-resource.services.ai.azure.com/models",
    needsKey: true,
    keyPlaceholder: "your resource's key",
    guide: {
      startUrl: "https://ai.azure.com",
      startLabel: "Open Azure AI Foundry",
      steps: [
        "Open your resource and find Keys and Endpoint.",
        "Copy the endpoint URL and paste it above.",
        "Copy Key 1 and paste it below.",
      ],
    },
    pricingUrl:
      "https://azure.microsoft.com/pricing/details/cognitive-services/openai-service/",
  },
  {
    id: "custom",
    label: "Another service",
    blurb: "Any OpenAI-compatible service, by its address.",
    kind: "openai_compatible",
    needsBaseUrl: true,
    baseUrlPlaceholder: "https://api.example.com/v1",
    needsKey: true,
    keyPlaceholder: "the service's API key",
    guide: {
      steps: [
        "Find the service's API keys page and create a key.",
        "Paste the service's address above and the key below.",
      ],
    },
  },
  {
    id: "local",
    label: "On this computer",
    blurb: "Ollama, LM Studio — no key, free.",
    kind: "openai_compatible",
    needsBaseUrl: true,
    baseUrlPlaceholder: "http://localhost:11434/v1",
    requireLoopback: true,
    local: true,
    needsKey: false,
  },
];

export function presetById(id: PresetId): EndpointPreset {
  const preset = ENDPOINT_PRESETS.find((p) => p.id === id);
  if (!preset) throw new Error(`unknown endpoint preset: ${id}`);
  return preset;
}

export function endpointLabel(record: EndpointRecord): string {
  const preset = ENDPOINT_PRESETS.find((p) => p.id === record.preset);
  return preset ? preset.label : record.base_url;
}

function isLoopbackHost(hostname: string): boolean {
  const host = hostname.toLowerCase();
  return (
    host === "localhost" ||
    host === "::1" ||
    host === "[::1]" ||
    /^127(\.\d{1,3}){3}$/.test(host)
  );
}

export type BaseUrlResult = { ok: true; url: string } | { ok: false; message: string };

/**
 * The spec's entry-time validation, mirrored for instant feedback: HTTPS
 * required except for loopback hosts (the local-model case), and no
 * userinfo. P1 enforces the same rules server-side.
 */
export function validateBaseUrl(
  raw: string,
  opts: { requireLoopback?: boolean } = {},
): BaseUrlResult {
  const trimmed = raw.trim();
  if (trimmed === "") return { ok: false, message: "Enter the service's address." };
  let url: URL;
  try {
    url = new URL(trimmed);
  } catch {
    return { ok: false, message: "That doesn't look like a web address." };
  }
  if (url.username || url.password) {
    return { ok: false, message: "The address can't contain a username or password." };
  }
  const loopback = isLoopbackHost(url.hostname);
  if (opts.requireLoopback && !loopback) {
    return {
      ok: false,
      message:
        "“On this computer” only accepts an address on this computer (localhost). For a service elsewhere, choose “another service”.",
    };
  }
  if (url.protocol !== "https:" && !(url.protocol === "http:" && loopback)) {
    return {
      ok: false,
      message: "The address must start with https:// (http:// is only for localhost).",
    };
  }
  return { ok: true, url: url.toString().replace(/\/$/, "") };
}

/** Instant wrong-string-paste check; null when the shape looks right. */
export function keyShapeError(preset: EndpointPreset, key: string): string | null {
  const prefixes = preset.keyPrefixes;
  if (!prefixes || prefixes.length === 0 || key === "") return null;
  if (prefixes.some((p) => key.startsWith(p))) return null;
  return `That doesn't look like a ${preset.label} key — it should start with ${prefixes.join(" or ")}.`;
}
