// First-run endpoint presets — "choose where your AI runs". Presets carry a
// guided capture card ("go here, click this, paste this"), the same pattern
// connectors use. All fake data: nothing here talks to a real service.
export interface EndpointPreset {
  id: string;
  name: string;
  blurb: string;
  // "on this computer" — no key, $0 by explicit classification.
  local?: boolean;
  // Also asks for an OpenAI-compatible base URL.
  needsBaseUrl?: boolean;
  // Shape check at paste; catches the wrong-string paste instantly.
  keyPrefix?: string;
  captureSteps: string[];
}

export const ENDPOINT_PRESETS: EndpointPreset[] = [
  {
    id: "anthropic",
    name: "Anthropic",
    blurb: "Claude models",
    keyPrefix: "sk-ant-",
    captureSteps: [
      "Go to console.anthropic.com and open API keys.",
      "Click Create Key.",
      "Copy the key that starts with sk-ant- and paste it below.",
    ],
  },
  {
    id: "openai",
    name: "OpenAI",
    blurb: "GPT models",
    keyPrefix: "sk-",
    captureSteps: [
      "Go to platform.openai.com and open API keys.",
      "Click Create new secret key.",
      "Copy the key that starts with sk- and paste it below.",
    ],
  },
  {
    id: "openrouter",
    name: "OpenRouter",
    blurb: "Many models, one key",
    keyPrefix: "sk-or-",
    captureSteps: [
      "Go to openrouter.ai and open Keys.",
      "Click Create Key.",
      "Copy the key that starts with sk-or- and paste it below.",
    ],
  },
  {
    id: "azure-foundry",
    name: "Azure AI Foundry",
    blurb: "Models your company runs on Azure",
    // Azure endpoints are per-resource — the card collects the URL too.
    needsBaseUrl: true,
    captureSteps: [
      "In the Azure AI Foundry portal, open your model deployment.",
      "Open Keys and Endpoint — copy the endpoint URL and paste it below.",
      "Copy a key and paste it below the URL.",
    ],
  },
  {
    id: "github-models",
    name: "GitHub Models",
    blurb: "Comes with your GitHub or Copilot plan",
    keyPrefix: "github_pat_",
    captureSteps: [
      "Go to github.com → Settings → Developer settings → Fine-grained tokens.",
      "Click Generate new token and allow it to read Models (models: read).",
      "Copy the token that starts with github_pat_ and paste it below.",
    ],
  },
  {
    id: "custom",
    name: "Another service",
    blurb: "Any OpenAI-compatible address",
    needsBaseUrl: true,
    captureSteps: [
      "Paste the service's address — it must start with https.",
      "Paste the key that service gave you below.",
    ],
  },
  {
    id: "local",
    name: "On this computer",
    blurb: "Ollama, LM Studio — no key, free",
    local: true,
    captureSteps: [],
  },
];
