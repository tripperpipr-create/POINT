package domain

import "strings"

// Справочник семейств моделей.
//
// До него контекстное окно человек вбивал числом в конструкторе — числом,
// которого знать не может: провайдеры почти никогда не отдают лимиты в списке
// моделей. Пустое поле заставляло угадывать, а угаданное окно означает либо
// потерянный контекст, либо отказ провайдера на середине квеста.
//
// Значения здесь — осознанный ориентир для семейства, а не факт о конкретной
// версии: подпись в интерфейсе так и говорит («known»). Точным значение
// становится только тогда, когда его прислал сам провайдер («confirmed»), а
// незнакомая модель честно остаётся «unknown» и спрашивает человека.
//
// Ключ — префикс model ID, поэтому новая точечная версия внутри семейства
// попадает в свою строку без правки кода.

type ModelReference struct {
	Prefix        string
	Family        string
	ContextWindow int
	MaxOutput     int
	Capabilities  []string
}

// ModelReferences отдаётся наружу, потому что тот же справочник нужен
// интерфейсу: без него конструктор снова просил бы у человека число, которого
// он знать не может. Дублировать таблицу в JS нельзя — разошлась бы молча.
func ModelReferences() []ModelReference {
	return modelReferences()
}

// Порядок важен: побеждает самый длинный совпавший префикс, поэтому частные
// строки могут стоять рядом с общими.
func modelReferences() []ModelReference {
	return []ModelReference{
		// Пятая серия: окно и потолок взяты не из головы, а из отчёта самого
		// Claude Code — он сообщает их в modelUsage при каждом ответе.
		{Prefix: "claude-opus-5", Family: "Claude Opus 5", ContextWindow: 1000000, MaxOutput: 64000, Capabilities: []string{"chat", "tools", "vision", "reasoning"}},
		{Prefix: "claude-sonnet-5", Family: "Claude Sonnet 5", ContextWindow: 1000000, MaxOutput: 64000, Capabilities: []string{"chat", "tools", "vision", "reasoning"}},
		{Prefix: "claude-fable-5", Family: "Claude Fable 5", ContextWindow: 1000000, MaxOutput: 64000, Capabilities: []string{"chat", "tools", "vision", "reasoning"}},
		{Prefix: "claude-opus-4", Family: "Claude Opus 4", ContextWindow: 200000, MaxOutput: 32000, Capabilities: []string{"chat", "tools", "vision", "reasoning"}},
		{Prefix: "claude-sonnet-4", Family: "Claude Sonnet 4", ContextWindow: 200000, MaxOutput: 64000, Capabilities: []string{"chat", "tools", "vision", "reasoning"}},
		{Prefix: "claude-haiku-4", Family: "Claude Haiku 4", ContextWindow: 200000, MaxOutput: 64000, Capabilities: []string{"chat", "tools", "vision"}},
		{Prefix: "claude-3-7", Family: "Claude 3.7", ContextWindow: 200000, MaxOutput: 8192, Capabilities: []string{"chat", "tools", "vision", "reasoning"}},
		{Prefix: "claude-3-5", Family: "Claude 3.5", ContextWindow: 200000, MaxOutput: 8192, Capabilities: []string{"chat", "tools", "vision"}},
		{Prefix: "claude-", Family: "Claude", ContextWindow: 200000, MaxOutput: 8192, Capabilities: []string{"chat", "tools", "vision"}},

		{Prefix: "gpt-5", Family: "GPT-5", ContextWindow: 400000, MaxOutput: 128000, Capabilities: []string{"chat", "tools", "vision", "reasoning"}},
		{Prefix: "gpt-4.1", Family: "GPT-4.1", ContextWindow: 1047576, MaxOutput: 32768, Capabilities: []string{"chat", "tools", "vision"}},
		{Prefix: "gpt-4o", Family: "GPT-4o", ContextWindow: 128000, MaxOutput: 16384, Capabilities: []string{"chat", "tools", "vision"}},
		{Prefix: "gpt-4", Family: "GPT-4", ContextWindow: 128000, MaxOutput: 4096, Capabilities: []string{"chat", "tools"}},
		{Prefix: "o4-mini", Family: "o4-mini", ContextWindow: 200000, MaxOutput: 100000, Capabilities: []string{"chat", "tools", "reasoning"}},
		{Prefix: "o3", Family: "o3", ContextWindow: 200000, MaxOutput: 100000, Capabilities: []string{"chat", "tools", "reasoning"}},

		{Prefix: "gemini-2.5", Family: "Gemini 2.5", ContextWindow: 1048576, MaxOutput: 65536, Capabilities: []string{"chat", "tools", "vision", "reasoning"}},
		{Prefix: "gemini-", Family: "Gemini", ContextWindow: 1048576, MaxOutput: 8192, Capabilities: []string{"chat", "tools", "vision"}},

		{Prefix: "deepseek-reasoner", Family: "DeepSeek Reasoner", ContextWindow: 65536, MaxOutput: 8192, Capabilities: []string{"chat", "reasoning"}},
		{Prefix: "deepseek-", Family: "DeepSeek", ContextWindow: 65536, MaxOutput: 8192, Capabilities: []string{"chat", "tools"}},
		{Prefix: "mistral-large", Family: "Mistral Large", ContextWindow: 131072, MaxOutput: 8192, Capabilities: []string{"chat", "tools"}},
		{Prefix: "mistral-", Family: "Mistral", ContextWindow: 32768, MaxOutput: 8192, Capabilities: []string{"chat", "tools"}},

		{Prefix: "llama-3.3", Family: "Llama 3.3", ContextWindow: 131072, MaxOutput: 8192, Capabilities: []string{"chat", "tools"}},
		{Prefix: "llama3.3", Family: "Llama 3.3", ContextWindow: 131072, MaxOutput: 8192, Capabilities: []string{"chat", "tools"}},
		{Prefix: "llama-3.1", Family: "Llama 3.1", ContextWindow: 131072, MaxOutput: 8192, Capabilities: []string{"chat", "tools"}},
		{Prefix: "llama3.1", Family: "Llama 3.1", ContextWindow: 131072, MaxOutput: 8192, Capabilities: []string{"chat", "tools"}},
		{Prefix: "qwen2.5-coder", Family: "Qwen2.5 Coder", ContextWindow: 32768, MaxOutput: 8192, Capabilities: []string{"chat", "tools"}},
		// Третья серия размышляет по умолчанию: рантайм не спрашивает разрешения,
		// он просто тратит вывод на размышление раньше ответа. Строка нужна не
		// ради окна, а ради признака «reasoning» — по нему предел вывода
		// поднимается до того, при котором ответ ещё помещается.
		{Prefix: "qwen3", Family: "Qwen3", ContextWindow: 131072, MaxOutput: 32768, Capabilities: []string{"chat", "tools", "reasoning"}},
		{Prefix: "qwen", Family: "Qwen", ContextWindow: 32768, MaxOutput: 8192, Capabilities: []string{"chat", "tools"}},
	}
}

// LookupModel ищет семейство по самому длинному совпавшему префиксу.
// Второе значение — нашлось ли: пустая структура и false честнее нуля.
//
// Провайдер здесь не спрашивается намеренно: агрегаторы вроде OpenRouter
// отдают чужие семейства под своим протоколом, и привязка справочника к виду
// подключения означала бы, что claude через OpenRouter считается незнакомым.
func LookupModel(modelID string) (ModelReference, bool) {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if id == "" {
		return ModelReference{}, false
	}
	// Каталоги-агрегаторы отдают ID вида "anthropic/claude-sonnet-4-5":
	// семейство определяет часть после последней косой черты.
	if cut := strings.LastIndex(id, "/"); cut >= 0 && cut+1 < len(id) {
		id = id[cut+1:]
	}
	best := ModelReference{}
	found := false
	for _, item := range modelReferences() {
		if strings.HasPrefix(id, item.Prefix) && len(item.Prefix) > len(best.Prefix) {
			best, found = item, true
		}
	}
	return best, found
}

// DefaultContextWindow — то, что подставляется в конструктор, когда модель
// известна. Ноль означает «неизвестна»: интерфейс обязан спросить, а не
// подставить произвольное число.
func DefaultContextWindow(modelID string) int {
	if reference, ok := LookupModel(modelID); ok {
		return reference.ContextWindow
	}
	return 0
}

// MinThinkingOutputTokens — предел вывода, ниже которого размышляющий ход
// заканчивается ничем.
//
// Размышление тратит тот же бюджет вывода, что и ответ, и тратит его первым.
// При 1400 и при 4096 наблюдалось одно и то же: finish_reason=length, ноль
// content, ход потерян целиком — вместе с потраченными токенами и временем.
const MinThinkingOutputTokens = 8192

// OutputBudgetForThinking поднимает запрошенный предел вывода до того, при
// котором размышляющая модель успевает и подумать, и ответить.
//
// Потолок семейства из справочника не превышается: просить у провайдера
// больше, чем он отдаёт, — это отказ на ровном месте. Незнакомая модель
// считается размышляющей только когда её об этом просят (effort), потому что
// поднимать предел всем подряд означало бы резервировать чужой бюджет впустую.
func OutputBudgetForThinking(requested int, modelID, effort string) int {
	thinks := false
	known, found := LookupModel(modelID)
	if found {
		for _, capability := range known.Capabilities {
			if capability == "reasoning" {
				thinks = true
				break
			}
		}
	}
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "", "none", "minimal":
	default:
		thinks = true
	}
	if !thinks || requested >= MinThinkingOutputTokens {
		return requested
	}
	budget := MinThinkingOutputTokens
	if found && known.MaxOutput > 0 && known.MaxOutput < budget {
		budget = known.MaxOutput
	}
	if budget < requested {
		return requested
	}
	return budget
}

// MaxThinkingOutputTokens — докуда растить предел вывода после хода, который
// целиком ушёл в размышление.
//
// Выше этого числа рост перестаёт лечить: модель, не уложившаяся в тридцать две
// тысячи токенов вывода, не уложится и в шестьдесят четыре — она зациклилась, а
// не «немного не успела». Зато каждый такой повтор стоит человеку минут
// ожидания, а на платном рантайме ещё и денег.
const MaxThinkingOutputTokens = 32768

// GrowThinkingOutputBudget — предел вывода для повтора. Ноль означает, что
// расти некуда и повторять бессмысленно.
//
// Размышление тратит бюджет вывода первым, и ход, потративший его целиком,
// заканчивается ничем: ни текста, ни вызова инструмента. Лечится это местом для
// ответа, а не тишиной: гасить размышление там, где оно бесплатно и делает
// ответ лучше, — решение не техническое.
//
// Растим сразу до потолка, а не удвоением. Удвоение выглядит бережливее, но
// каждый шаг — это ещё один полный ход к модели: на локальной девятке лестница
// 8к→16к→32к стоит человеку трёх ожиданий подряд вместо одного, а разницы в
// цене нет — рантайм, ради которого всё затевалось, бесплатный.
//
// Потолок семейства из справочника не превышается: просить у провайдера больше,
// чем он отдаёт, — отказ на ровном месте.
func GrowThinkingOutputBudget(current int, modelID string) int {
	next := MaxThinkingOutputTokens
	if known, found := LookupModel(modelID); found && known.MaxOutput > 0 && next > known.MaxOutput {
		next = known.MaxOutput
	}
	if next <= current {
		return 0
	}
	return next
}
