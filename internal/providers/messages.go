package providers

import "strings"

// NormalizeChatMessages makes chat payloads acceptable for strict OpenAI-compatible
// gateways (LLMux, Qwen, DeepSeek, etc.):
//   - exactly one leading system message (all system parts merged)
//   - no system messages after the first non-system turn
//   - drop empty text-only messages
//   - never start with assistant/tool
func NormalizeChatMessages(messages []Message) []Message {
	systems := make([]string, 0, 2)
	rest := make([]Message, 0, len(messages))
	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		switch role {
		case "system":
			if text := strings.TrimSpace(message.Content); text != "" {
				systems = append(systems, text)
			}
		case "user", "assistant", "tool":
			if strings.TrimSpace(message.Content) == "" && len(message.ToolCalls) == 0 && len(message.Images) == 0 && strings.TrimSpace(message.ToolCallID) == "" {
				continue
			}
			message.Role = role
			rest = append(rest, message)
		default:
			if text := strings.TrimSpace(message.Content); text != "" {
				message.Role = "user"
				message.Content = text
				rest = append(rest, message)
			}
		}
	}
	out := make([]Message, 0, len(rest)+1)
	if len(systems) > 0 {
		out = append(out, Message{Role: "system", Content: strings.Join(systems, "\n\n")})
	}
	out = append(out, rest...)
	if len(out) == 0 {
		return out
	}
	switch out[0].Role {
	case "assistant":
		out = append([]Message{{Role: "user", Content: "(continued)"}}, out...)
	case "tool":
		out = append([]Message{{Role: "user", Content: "(tool results follow)"}}, out...)
	}
	return out
}
