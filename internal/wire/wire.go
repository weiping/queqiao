// Package wire reads, from an agent's request body, only what mbridge's
// router needs: who is speaking in each message, their text, and the
// tool calls and results. It understands the three protocols the agents
// mbridge routes speak: Anthropic Messages, OpenAI Responses and OpenAI
// Chat Completions.
//
// The reading rules are magpie's, so that mbridge, now outside magpie,
// sees a request exactly as the gateway did (SP8 spec §5.3). Each function
// names the magpie function it follows, as of upstream 0d5fdbb2.
package wire

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Protocol is the API an agent's request was sent in.
type Protocol int

const (
	Anthropic Protocol = iota
	Responses
	Chat
)

// ProtocolOf says which protocol a request path is, for the three paths
// mbridge routes; ok is false for any other path.
func ProtocolOf(path string) (Protocol, bool) {
	switch path {
	case "/v1/messages", "/messages":
		return Anthropic, true
	case "/v1/responses", "/responses":
		return Responses, true
	case "/v1/chat/completions", "/chat/completions":
		return Chat, true
	}
	return 0, false
}

// PartKind is what a part of a message is.
type PartKind int

const (
	Other PartKind = iota
	Text
	Media // an image or a file: counts as the user saying something
	ToolCall
	ToolResult
	Thinking
)

// Part is one piece of a message.
type Part struct {
	Kind    PartKind
	Text    string
	IsError bool // a tool result the agent marked failed (Anthropic only)
}

// Message is one message: Role is "user", "assistant" or "system".
type Message struct {
	Role  string
	Parts []Part
}

// Request is what wire read of a request.
type Request struct {
	Model    string
	Messages []Message
}

// Parse reads body as protocol p. A body that isn't JSON of the protocol's
// shape is an error, as it is to magpie.
func Parse(p Protocol, body []byte) (*Request, error) {
	switch p {
	case Responses:
		return parseResponses(body)
	case Chat:
		return parseChat(body)
	}
	return parseAnthropic(body)
}

// --- Anthropic (magpie: gateway/anthropic.go parseAnthropic) ---

func parseAnthropic(body []byte) (*Request, error) {
	var a struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("invalid request: %v", err)
	}
	r := &Request{Model: a.Model}
	for _, m := range a.Messages {
		msg := Message{Role: m.Role}
		var s string
		if json.Unmarshal(m.Content, &s) == nil {
			if s != "" {
				msg.Parts = append(msg.Parts, Part{Kind: Text, Text: s})
			}
		} else {
			var blocks []struct {
				Type    string          `json:"type"`
				Text    string          `json:"text"`
				Source  json.RawMessage `json:"source"`
				IsError bool            `json:"is_error"`
			}
			if err := json.Unmarshal(m.Content, &blocks); err != nil {
				return nil, fmt.Errorf("invalid message content: %v", err)
			}
			for _, b := range blocks {
				switch b.Type {
				case "text":
					msg.Parts = append(msg.Parts, Part{Kind: Text, Text: b.Text})
				case "image":
					if len(b.Source) > 0 && string(b.Source) != "null" {
						msg.Parts = append(msg.Parts, Part{Kind: Media})
					}
				case "tool_use":
					msg.Parts = append(msg.Parts, Part{Kind: ToolCall})
				case "tool_result":
					msg.Parts = append(msg.Parts, Part{Kind: ToolResult, IsError: b.IsError})
				case "thinking":
					msg.Parts = append(msg.Parts, Part{Kind: Thinking})
				}
			}
		}
		r.Messages = append(r.Messages, msg)
	}
	return r, nil
}

// --- Chat Completions (magpie: gateway/chat.go parseChat, chatParts, chatFile) ---

func parseChat(body []byte) (*Request, error) {
	var c struct {
		Model    string `json:"model"`
		Messages []struct {
			Role             string          `json:"role"`
			Content          json.RawMessage `json:"content"`
			ReasoningContent string          `json:"reasoning_content"`
			ToolCalls        []struct{}      `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("invalid request: %v", err)
	}
	r := &Request{Model: c.Model}
	for _, m := range c.Messages {
		switch m.Role {
		case "user":
			r.Messages = append(r.Messages, Message{Role: "user", Parts: chatParts(m.Content)})
		case "assistant":
			msg := Message{Role: "assistant"}
			if m.ReasoningContent != "" {
				msg.Parts = append(msg.Parts, Part{Kind: Thinking})
			}
			msg.Parts = append(msg.Parts, chatParts(m.Content)...)
			for range m.ToolCalls {
				msg.Parts = append(msg.Parts, Part{Kind: ToolCall})
			}
			r.Messages = append(r.Messages, msg)
		case "tool":
			r.Messages = append(r.Messages, Message{Role: "user", Parts: []Part{{Kind: ToolResult}}})
		}
	}
	return r, nil
}

func chatParts(raw json.RawMessage) []Part {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "" {
			return nil
		}
		return []Part{{Kind: Text, Text: s}}
	}
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text"`
		File struct {
			FileData string `json:"file_data"`
			FileID   string `json:"file_id"`
		} `json:"file"`
	}
	json.Unmarshal(raw, &items)
	var out []Part
	for _, it := range items {
		switch it.Type {
		case "text":
			out = append(out, Part{Kind: Text, Text: it.Text})
		case "image_url":
			out = append(out, Part{Kind: Media})
		case "file":
			if chatFileOK(it.File.FileData, it.File.FileID) {
				out = append(out, Part{Kind: Media})
			}
		}
	}
	return out
}

// chatFileOK is whether magpie's chatFile makes a part of a file: inline
// data (a data: URL with a payload, or bare data) or a file id.
func chatFileOK(data, id string) bool {
	if strings.HasPrefix(data, "data:") {
		if _, payload, ok := strings.Cut(strings.TrimPrefix(data, "data:"), ","); ok && payload != "" {
			return true
		}
	} else if data != "" {
		return true
	}
	return id != ""
}

// --- Responses (magpie: gateway/responses.go parseResponses, responsesParts, mergeTurns) ---

func parseResponses(body []byte) (*Request, error) {
	var q struct {
		Model string          `json:"model"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &q); err != nil {
		return nil, fmt.Errorf("invalid request: %v", err)
	}
	r := &Request{Model: q.Model}
	var s string
	if json.Unmarshal(q.Input, &s) == nil {
		r.Messages = append(r.Messages, Message{Role: "user", Parts: []Part{{Kind: Text, Text: s}}})
		return r, nil
	}
	var items []struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Summary []struct {
			Text string `json:"text"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(q.Input, &items); err != nil {
		return nil, fmt.Errorf("invalid input: %v", err)
	}
	replied := false
	for _, it := range items {
		if it.Role == "assistant" || strings.HasPrefix(it.Type, "function_call") || strings.HasPrefix(it.Type, "custom_tool_call") ||
			strings.HasPrefix(it.Type, "tool_search") || it.Type == "reasoning" {
			replied = true
		}
		switch {
		case it.Type == "message" || (it.Type == "" && it.Role != ""):
			parts := responsesParts(it.Content)
			if it.Role == "system" || it.Role == "developer" {
				// before a reply, lifted into the system prompt; after one,
				// kept in place as a reminder in the user's message
				if t := joinText(parts); t != "" && replied {
					r.Messages = append(r.Messages, Message{Role: "user", Parts: []Part{{Kind: Text,
						Text: "<system-reminder>\n" + t + "\n</system-reminder>"}}})
				}
				continue
			}
			role := "user"
			if it.Role == "assistant" {
				role = "assistant"
			}
			r.Messages = append(r.Messages, Message{Role: role, Parts: parts})
		case it.Type == "agent_message":
			if parts := responsesParts(it.Content); len(parts) > 0 {
				r.Messages = append(r.Messages, Message{Role: "user", Parts: parts})
			}
		case it.Type == "function_call", it.Type == "custom_tool_call", it.Type == "tool_search_call":
			r.Messages = append(r.Messages, Message{Role: "assistant", Parts: []Part{{Kind: ToolCall}}})
		case it.Type == "tool_search_output", it.Type == "function_call_output", it.Type == "custom_tool_call_output":
			r.Messages = append(r.Messages, Message{Role: "user", Parts: []Part{{Kind: ToolResult}}})
		case it.Type == "reasoning":
			if reasoningText(it.Content, it.Summary) {
				r.Messages = append(r.Messages, Message{Role: "assistant", Parts: []Part{{Kind: Thinking}}})
			}
		}
	}
	r.Messages = mergeTurns(r.Messages)
	return r, nil
}

func reasoningText(content json.RawMessage, summary []struct {
	Text string `json:"text"`
}) bool {
	var cs []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(content, &cs)
	for _, c := range cs {
		if c.Type == "reasoning_text" && c.Text != "" {
			return true
		}
	}
	for _, s := range summary {
		if s.Text != "" {
			return true
		}
	}
	return false
}

func responsesParts(raw json.RawMessage) []Part {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "" {
			return nil
		}
		return []Part{{Kind: Text, Text: s}}
	}
	var items []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL string `json:"image_url"`
	}
	json.Unmarshal(raw, &items)
	var out []Part
	for _, it := range items {
		switch it.Type {
		case "input_text", "output_text", "text":
			out = append(out, Part{Kind: Text, Text: it.Text})
		case "input_image":
			if it.ImageURL != "" {
				out = append(out, Part{Kind: Media})
			}
		}
	}
	return out
}

// joinText concatenates the text parts, as magpie's ir.go text does.
func joinText(parts []Part) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Kind == Text {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func mergeTurns(msgs []Message) []Message {
	var out []Message
	for _, m := range msgs {
		if n := len(out); n > 0 && out[n-1].Role == m.Role {
			out[n-1].Parts = append(out[n-1].Parts, m.Parts...)
			continue
		}
		out = append(out, m)
	}
	return out
}

// --- what the router reads ---

var reminders = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

// UserText is what the user said to begin the turn: the last user
// message's text without <system-reminder> blocks, its middle left out
// when long (magpie: gateway/classify.go userText).
func UserText(r *Request) string {
	var parts []string
	for i := len(r.Messages) - 1; i >= 0; i-- {
		m := r.Messages[i]
		if m.Role != "user" {
			continue
		}
		for _, p := range m.Parts {
			if p.Kind == Text {
				parts = append(parts, p.Text)
			}
		}
		break
	}
	text := strings.TrimSpace(reminders.ReplaceAllString(strings.Join(parts, "\n"), ""))
	const head, tail = 3000, 1000
	if rs := []rune(text); len(rs) > head+tail {
		text = string(rs[:head]) + "\n…\n" + string(rs[len(rs)-tail:])
	}
	return text
}

// PreviousAnswer is the assistant's reply before the user's latest message
// (SP11): the text of the nearest assistant message with text, looking back
// past the user messages that end the request. "" when there is none.
func PreviousAnswer(r *Request) string {
	i := len(r.Messages) - 1
	for i >= 0 && r.Messages[i].Role == "user" {
		i--
	}
	for ; i >= 0; i-- {
		m := r.Messages[i]
		if m.Role == "user" {
			return "" // an earlier turn: the reply before this one had no text
		}
		if m.Role != "assistant" {
			continue
		}
		var parts []string
		for _, p := range m.Parts {
			if p.Kind == Text {
				parts = append(parts, p.Text)
			}
		}
		if t := strings.TrimSpace(strings.Join(parts, "\n")); t != "" {
			return t
		}
	}
	return ""
}

// TurnOf counts the user's turns, and says whether the request goes on
// within the last one: its last user message hands back tool results
// (magpie: gateway/affinity.go turnIn).
func TurnOf(r *Request) (turn int, within bool) {
	for _, m := range r.Messages {
		if m.Role != "user" {
			continue
		}
		said, result := false, false
		for _, p := range m.Parts {
			switch p.Kind {
			case Text, Media:
				said = true
			case ToolResult:
				result = true
			}
		}
		if said && !result {
			turn++
		}
		within = result
	}
	return turn, within
}

// FirstWords is a short hash of the conversation's first user message:
// a subagent shares its parent's session but not this (magpie:
// gateway/rules.go firstWords).
func FirstWords(r *Request) string {
	h := sha256.New()
	for _, m := range r.Messages {
		if m.Role != "user" {
			continue
		}
		for _, p := range m.Parts {
			if p.Kind == Text {
				h.Write([]byte(p.Text))
			}
		}
		break
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// ToolStats counts a request's tool results and the failed ones.
type ToolStats struct{ Calls, Failures int }

// Tools counts every tool result in the request, as the gateway hook's
// Sessions.Observe did. Only Anthropic marks a failed result.
func Tools(r *Request) ToolStats {
	var st ToolStats
	for _, m := range r.Messages {
		for _, p := range m.Parts {
			if p.Kind == ToolResult {
				st.Calls++
				if p.IsError {
					st.Failures++
				}
			}
		}
	}
	return st
}
