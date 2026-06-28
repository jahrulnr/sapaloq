package bridge

import "strings"

// LastUserOrToolIndex returns the index of the latest user or tool turn, or -1.
func LastUserOrToolIndex(msgs []Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" || msgs[i].Role == "tool" {
			return i
		}
	}
	return -1
}

// ComposeAgentUserText builds the Cursor Agent API user_text field. On the
// first provider call of a generation it includes system blocks and prior
// conversation; on tool continuations it sends only the tail since the last
// assistant turn so the server-side conversation is not duplicated.
func ComposeAgentUserText(messages []Message, continuation bool) string {
	if continuation {
		return composeAgentContinuation(messages)
	}
	return composeAgentFullTurn(messages)
}

func composeAgentContinuation(messages []Message) string {
	lastAsst := -1
	for i, m := range messages {
		if m.Role == "assistant" {
			lastAsst = i
		}
	}
	if lastAsst < 0 {
		return composeLatestTurn(messages)
	}
	var b strings.Builder
	for _, m := range messages[lastAsst:] {
		if m.Role == "system" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(m.Content)
	}
	return b.String()
}

func composeAgentFullTurn(messages []Message) string {
	var sys []string
	var convo []Message
	for _, m := range messages {
		if m.Role == "system" {
			sys = append(sys, m.Content)
			continue
		}
		convo = append(convo, m)
	}

	var b strings.Builder
	if len(sys) > 0 {
		b.WriteString("[system]\n")
		b.WriteString(strings.Join(sys, "\n\n"))
		b.WriteString("\n\n")
	}
	latestIdx := LastUserOrToolIndex(convo)
	if latestIdx > 0 {
		b.WriteString("[conversation]\n")
		for i := 0; i < latestIdx; i++ {
			b.WriteString(convo[i].Role)
			b.WriteString(": ")
			b.WriteString(convo[i].Content)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if latestIdx >= 0 {
		b.WriteString("[user]\n")
		b.WriteString(convo[latestIdx].Content)
	} else if len(convo) > 0 {
		b.WriteString(convo[len(convo)-1].Content)
	}
	return b.String()
}

func composeLatestTurn(messages []Message) string {
	if i := LastUserOrToolIndex(messages); i >= 0 {
		return messages[i].Content
	}
	if len(messages) > 0 {
		return messages[len(messages)-1].Content
	}
	return ""
}
