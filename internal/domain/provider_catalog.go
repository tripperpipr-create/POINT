package domain

type ProviderPreset struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Description    string       `json:"description"`
	Kind           ProviderKind `json:"kind"`
	BaseURL        string       `json:"baseUrl"`
	DefaultModel   string       `json:"defaultModel"`
	Local          bool         `json:"local"`
	RequiresAPIKey bool         `json:"requiresApiKey"`
	External       bool         `json:"external"`
}

// BuiltInProviderCatalog uses stable protocols. OpenAI-compatible endpoints
// cover both hosted gateways and local runtimes while still allowing a fully
// custom URL and model ID. Anthropic и Azure перечислены отдельно: у первого
// свой протокол, у второго — свой способ авторизации и адресации deployment.
func BuiltInProviderCatalog() []ProviderPreset {
	return []ProviderPreset{
		{ID: "ollama", Name: "Ollama", Description: "Локальные модели через Ollama", Kind: ProviderOllama, BaseURL: "http://127.0.0.1:11434", DefaultModel: "qwen2.5-coder:7b", Local: true},
		{ID: "lm-studio", Name: "LM Studio", Description: "Локальный OpenAI-совместимый сервер", Kind: ProviderOpenAI, BaseURL: "http://127.0.0.1:1234/v1", DefaultModel: "local-model", Local: true},
		{ID: "vllm", Name: "vLLM", Description: "Локальный или сетевой vLLM endpoint", Kind: ProviderOpenAI, BaseURL: "http://127.0.0.1:8000/v1", DefaultModel: "local-model", Local: true},
		{ID: "llama-cpp", Name: "llama.cpp", Description: "Легковесный локальный сервер", Kind: ProviderOpenAI, BaseURL: "http://127.0.0.1:8080/v1", DefaultModel: "local-model", Local: true},
		{ID: "llmux", Name: "LLMux", Description: "Корпоративный OpenAI-совместимый шлюз: свой URL и токен", Kind: ProviderOpenAI, BaseURL: "", DefaultModel: "", RequiresAPIKey: true},
		{ID: "custom", Name: "Свой endpoint", Description: "Любой OpenAI-совместимый URL, model ID и токен", Kind: ProviderOpenAI, BaseURL: "", DefaultModel: "", RequiresAPIKey: true},
		{ID: "anthropic", Name: "Anthropic", Description: "Claude по Messages API", Kind: ProviderAnthropic, BaseURL: "https://api.anthropic.com/v1", DefaultModel: "claude-sonnet-4-5", RequiresAPIKey: true},
		{ID: "azure-openai", Name: "Azure OpenAI", Description: "Ресурс Azure: адрес, deployment и api-version", Kind: ProviderAzureOpenAI, BaseURL: "", DefaultModel: "", RequiresAPIKey: true},
		{ID: "openai", Name: "OpenAI", Description: "Модели OpenAI по API", Kind: ProviderOpenAI, BaseURL: "https://api.openai.com/v1", DefaultModel: "gpt-5-mini", RequiresAPIKey: true},
		{ID: "openrouter", Name: "OpenRouter", Description: "Единый каталог моделей разных провайдеров", Kind: ProviderOpenAI, BaseURL: "https://openrouter.ai/api/v1", DefaultModel: "openai/gpt-5-mini", RequiresAPIKey: true},
		{ID: "gemini", Name: "Google Gemini", Description: "OpenAI-совместимый endpoint Gemini", Kind: ProviderOpenAI, BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", DefaultModel: "gemini-2.5-flash", RequiresAPIKey: true},
		{ID: "groq", Name: "Groq", Description: "Быстрый OpenAI-совместимый inference", Kind: ProviderOpenAI, BaseURL: "https://api.groq.com/openai/v1", DefaultModel: "openai/gpt-oss-20b", RequiresAPIKey: true},
		{ID: "mistral", Name: "Mistral AI", Description: "Облачные модели Mistral", Kind: ProviderOpenAI, BaseURL: "https://api.mistral.ai/v1", DefaultModel: "mistral-small-latest", RequiresAPIKey: true},
		{ID: "deepseek", Name: "DeepSeek", Description: "DeepSeek API", Kind: ProviderOpenAI, BaseURL: "https://api.deepseek.com/v1", DefaultModel: "deepseek-chat", RequiresAPIKey: true},
		{ID: "together", Name: "Together AI", Description: "Каталог открытых моделей", Kind: ProviderOpenAI, BaseURL: "https://api.together.xyz/v1", DefaultModel: "meta-llama/Llama-3.3-70B-Instruct-Turbo", RequiresAPIKey: true},
	}
}

// SelfHostedProviderPreset — за пресетом стоит рантайм под управлением
// владельца: vLLM, SGLang, llama.cpp, Ollama или корпоративный шлюз перед ними.
//
// Отличие, ради которого список нужен: такие endpoint'ы принимают в теле
// запроса поля сверх спецификации OpenAI (например `chat_template_kwargs`,
// которым гасится «размышление» у Qwen3), а официальный API на неизвестное
// поле отвечает 400. Поэтому пресеты перечислены поимённо, а не выведены из
// «BaseURL задан руками»: у OpenRouter и Gemini адрес тоже свой, но тело они
// разбирают строго.
func SelfHostedProviderPreset(id string) bool {
	if id == "" {
		return false
	}
	for _, preset := range BuiltInProviderCatalog() {
		if preset.ID != id {
			continue
		}
		// `Local` покрывает четыре рантайма, поднимаемых у себя; шлюз (`llmux`)
		// и произвольный endpoint (`custom`) локальными не помечены — у них
		// адрес сетевой, — но стоят перед теми же рантаймами.
		return preset.Local || id == "llmux" || id == "custom"
	}
	return false
}

// RuntimeAcceptsThinkingSwitch — принимает ли рантайм просьбу не «размышлять».
//
// У Ollama это штатное поле её собственного API (`think`), поэтому вид
// провайдера отвечает за себя сам, даже если пресет в профиле не проставлен.
// На OpenAI-совместимом адресе тот же переключатель уходит полем сверх
// спецификации (`chat_template_kwargs`): его разбирают только рантаймы под
// управлением владельца, а официальный API отвечает 400 и подменяет настоящую
// причину отказа ошибкой формата.
func RuntimeAcceptsThinkingSwitch(kind ProviderKind, preset string) bool {
	return kind == ProviderOllama || SelfHostedProviderPreset(preset)
}

// RuntimeChargesForTokens — тарифицирует ли рантайм токены наружу.
//
// Отличие от SelfHostedProviderPreset: тот отвечает на технический вопрос
// («примет ли endpoint поля сверх спецификации»), а этот — на экономический.
// За Ollama и локальными серверами стоит своё железо, за шлюзом (`llmux`) —
// оплаченный владельцем пул: счётчик токенов там защищает не кошелёк, а только
// от зацикливания, и для этого есть предел ходов и время. `custom` остаётся
// платным: за произвольным URL может стоять и официальный API.
func RuntimeChargesForTokens(kind ProviderKind, preset string) bool {
	if kind == ProviderOllama {
		return false
	}
	if preset == "llmux" {
		return false
	}
	for _, item := range BuiltInProviderCatalog() {
		if item.ID == preset {
			return !item.Local
		}
	}
	return true
}

// ShouldSuppressThinking — гасить ли «размышление» аварийно, когда ход потратил
// на него весь вывод.
//
// Техническая возможность (RuntimeAcceptsThinkingSwitch) не делает глушение
// правильным. Размышление улучшает ответ, а признак глушения живёт до конца
// прогона: один обрезанный ход лишал бы размышления весь остаток квеста. Там,
// где токены ничего не стоят, честный путь другой — поднять предел вывода и
// подсказать модели уложиться в него. Платный рантайм гасит, потому что там
// каждый лишний токен размышления оплачен.
func ShouldSuppressThinking(kind ProviderKind, preset string) bool {
	return RuntimeAcceptsThinkingSwitch(kind, preset) && RuntimeChargesForTokens(kind, preset)
}
