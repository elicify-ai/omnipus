// providersCatalog.stub.ts — T068-05's temporary test fixture for the
// registry-fed providers catalog (ADR-067 schema 2.0.0, GET /providers/catalog).
//
// Typed against the generated ProvidersCatalog so any contract drift is a
// compile error. Deliberately small: it carries exactly the shapes the SPA
// tests exercise (popular tier, plan × region variants sharing a `company`,
// aliases, an unsupported provider, a local provider). T068-18 replaces it
// with `src/test/fixtures/providers-catalog.json` — the 190-entry snapshot
// generated from the real B2-RELEASE document.

import type { CatalogProvider, ProvidersCatalog } from '@/lib/api/generated/openapi-types'

type ModelSeed = [id: string, name: string, contextWindow: number, imageCapable: boolean]

function models(seed: ModelSeed[]): CatalogProvider['models'] {
  return seed.map(([id, name, context_window, image]) => ({
    id,
    name,
    release_date: '2026-05-20',
    tool_call: true,
    context_window,
    max_output_tokens: 65536,
    input_modalities: image ? ['text', 'image'] : ['text'],
    status: 'active',
  }))
}

function provider(p: Partial<CatalogProvider> & Pick<CatalogProvider, 'id' | 'name' | 'company' | 'api'>): CatalogProvider {
  return {
    protocol: 'openai-compatible',
    tier: 'standard',
    auth_methods: ['api_key'],
    aliases: [],
    locality: 'cloud',
    models: models([[`${p.id}-default`, `${p.name} default`, 128000, false]]),
    ...p,
  }
}

export const STUB_PROVIDERS: CatalogProvider[] = [
  provider({ id: 'openai', name: 'OpenAI', company: 'OpenAI', api: 'https://api.openai.com/v1', tier: 'popular',
    models: models([['gpt-5', 'GPT-5', 400000, true]]) }),
  provider({ id: 'anthropic', name: 'Anthropic', company: 'Anthropic', api: 'https://api.anthropic.com/v1', protocol: 'anthropic', tier: 'popular',
    aliases: ['anthropic-messages'], models: models([['claude-sonnet-4-5', 'Claude Sonnet 4.5', 200000, true]]) }),
  provider({ id: 'openrouter', name: 'OpenRouter', company: 'OpenRouter', api: 'https://openrouter.ai/api/v1', tier: 'popular',
    models: models([['openrouter/auto', 'Auto', 200000, true]]) }),
  provider({ id: 'google', name: 'Google Gemini', company: 'Google Gemini', api: 'https://generativelanguage.googleapis.com', protocol: 'google', tier: 'popular',
    aliases: ['gemini'], models: models([['gemini-2.5-pro', 'Gemini 2.5 Pro', 1000000, true]]) }),
  provider({ id: 'groq', name: 'Groq', company: 'Groq', api: 'https://api.groq.com/openai/v1' }),
  provider({ id: 'deepseek', name: 'DeepSeek', company: 'DeepSeek', api: 'https://api.deepseek.com/v1', tier: 'popular' }),
  // Zhipu AI — plan × region variants sharing one company.
  provider({ id: 'zai', name: 'Z.AI', company: 'Zhipu AI', api: 'https://api.z.ai/api/paas/v4', region: 'intl', tier: 'popular',
    aliases: ['z-ai', 'zhipu', 'z.ai', 'glm'],
    models: models([['glm-5.2', 'GLM-5.2', 1000000, false], ['glm-5.2-flash', 'GLM-5.2 Flash', 200000, true]]) }),
  provider({ id: 'zai-coding-plan', name: 'Z.AI Coding Plan', company: 'Zhipu AI', api: 'https://api.z.ai/api/coding/paas/v4', plan: 'coding-plan', region: 'intl',
    aliases: ['glm-coding', 'zhipu-coding'] }),
  provider({ id: 'zhipuai', name: 'Zhipu AI (China)', company: 'Zhipu AI', api: 'https://open.bigmodel.cn/api/paas/v4', region: 'china',
    aliases: ['bigmodel'] }),
  provider({ id: 'zhipuai-coding-plan', name: 'Zhipu AI Coding Plan (China)', company: 'Zhipu AI', api: 'https://open.bigmodel.cn/api/coding/paas/v4', plan: 'coding-plan', region: 'china' }),
  // Moonshot — one plan, two regions.
  provider({ id: 'moonshot', name: 'Moonshot AI', company: 'Moonshot AI', api: 'https://api.moonshot.ai/v1', region: 'intl', aliases: ['kimi'] }),
  provider({ id: 'moonshot-cn', name: 'Moonshot AI (China)', company: 'Moonshot AI', api: 'https://api.moonshot.cn/v1', region: 'china' }),
  // MiniMax — one plan, two regions.
  provider({ id: 'minimax', name: 'MiniMax', company: 'MiniMax', api: 'https://api.minimax.io/v1', region: 'intl' }),
  provider({ id: 'minimax-cn', name: 'MiniMax (China)', company: 'MiniMax', api: 'https://api.minimaxi.com/v1', region: 'china' }),
  // Alibaba — three standard regions + one coding plan without a region split.
  provider({ id: 'alibaba', name: 'Alibaba Cloud Model Studio', company: 'Alibaba Cloud', api: 'https://dashscope-intl.aliyuncs.com/compatible-mode/v1', region: 'intl', aliases: ['qwen', 'dashscope'] }),
  provider({ id: 'alibaba-cn', name: 'Alibaba Cloud Model Studio (China)', company: 'Alibaba Cloud', api: 'https://dashscope.aliyuncs.com/compatible-mode/v1', region: 'china' }),
  provider({ id: 'alibaba-us', name: 'Alibaba Cloud Model Studio (US)', company: 'Alibaba Cloud', api: 'https://dashscope-us.aliyuncs.com/compatible-mode/v1', region: 'us' }),
  provider({ id: 'alibaba-coding-plan', name: 'Alibaba Cloud Coding Plan', company: 'Alibaba Cloud', api: 'https://coding-intl.dashscope.aliyuncs.com/v1', plan: 'coding-plan' }),
  // Deployment-configured: no fixed default base (onboarding requires an endpoint).
  provider({ id: 'azure', name: 'Azure OpenAI', company: 'Azure OpenAI', api: 'https://<resource>.openai.azure.com', aliases: ['azure-openai'] }),
  // Local runtime.
  provider({ id: 'ollama', name: 'Ollama', company: 'Ollama', api: 'http://localhost:11434/v1', protocol: 'ollama', locality: 'local' }),
  // Unsupported (needs request signing).
  provider({ id: 'bedrock', name: 'Amazon Bedrock', company: 'Amazon', api: 'https://bedrock-runtime.us-east-1.amazonaws.com', tier: 'unsupported', unsupported_reason: 'cloud-iam' }),
]

export const PROVIDERS_CATALOG_STUB: ProvidersCatalog = {
  schema_version: '2.0.0',
  version: 'v2026.8.22',
  updated_at: '2026-08-22T06:00:00Z',
  source: 'models.dev@stub litellm@stub overrides@stub',
  default_resize_limits: { long_edge_px: 7680, max_bytes: 10485760 },
  providers: STUB_PROVIDERS,
  served_from: 'embedded',
  stale: false,
}
