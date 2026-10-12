package gateway

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/hnewcity/mantle-mask/internal/usage"
)

// Observer parses a bounded copy for routing/usage only. The original SSE bytes
// are forwarded without reconstruction, including tool calls and unknown events.
type observer struct {
	line            []byte
	event           []byte
	onResponse      func(string) error
	onUsage         func(usage.Counters) error
	input           int64
	output          int64
	cached          int64
	reasoning       int64
	known           bool
	complete        bool
	failed          bool
	sawDone         bool
	sawChat         bool
	expectedChoices int
	finishedChoices map[int]bool
}

func (o *observer) json(data []byte) error {
	var item struct {
		ID      string      `json:"id"`
		Object  string      `json:"object"`
		Type    string      `json:"type"`
		Status  string      `json:"status"`
		Usage   *tokenUsage `json:"usage"`
		Choices []struct {
			Index        int     `json:"index"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Response *struct {
			ID     string      `json:"id"`
			Status string      `json:"status"`
			Usage  *tokenUsage `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &item) != nil {
		return nil
	}
	if item.Response != nil {
		item.ID, item.Object, item.Usage = item.Response.ID, "response", item.Response.Usage
	}
	if item.Object == "chat.completion.chunk" {
		o.sawChat = true
		expected := max(1, o.expectedChoices)
		if o.finishedChoices == nil {
			o.finishedChoices = make(map[int]bool)
		}
		for _, choice := range item.Choices {
			if choice.Index >= 0 && choice.Index < expected && choice.FinishReason != nil && *choice.FinishReason != "" {
				o.finishedChoices[choice.Index] = true
			}
		}
		// Mantle can end Chat SSE at EOF after terminal choice chunks without
		// sending [DONE]. Every requested choice must have a finish reason.
		if len(o.finishedChoices) == expected {
			o.complete = true
		}
	}
	if item.Object == "response" && item.ID != "" && o.onResponse != nil {
		if err := o.onResponse(item.ID); err != nil {
			return err
		}
	}
	if counts, ok := item.Usage.counters(); ok {
		counts.InputTokens = max(o.input, counts.InputTokens)
		counts.OutputTokens = max(o.output, counts.OutputTokens)
		counts.CachedTokens = max(o.cached, counts.CachedTokens)
		counts.ReasoningTokens = max(o.reasoning, counts.ReasoningTokens)
		if !o.known || counts != (usage.Counters{InputTokens: o.input, OutputTokens: o.output, CachedTokens: o.cached, ReasoningTokens: o.reasoning}) {
			if o.onUsage != nil {
				if err := o.onUsage(counts); err != nil {
					return err
				}
			}
			o.input, o.output, o.cached, o.reasoning, o.known = counts.InputTokens, counts.OutputTokens, counts.CachedTokens, counts.ReasoningTokens, true
		}
	}
	if item.Type == "response.completed" || item.Type == "response.incomplete" || item.Type == "response.failed" {
		o.complete = true
	}
	if item.Type == "error" || item.Type == "response.failed" || item.Status == "failed" {
		o.failed = true
	}
	return nil
}

type tokenDetails struct {
	CachedTokens    int64 `json:"cached_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

type tokenUsage struct {
	InputTokens       *int64       `json:"input_tokens"`
	OutputTokens      *int64       `json:"output_tokens"`
	PromptTokens      *int64       `json:"prompt_tokens"`
	CompletionTokens  *int64       `json:"completion_tokens"`
	InputDetails      tokenDetails `json:"input_tokens_details"`
	OutputDetails     tokenDetails `json:"output_tokens_details"`
	PromptDetails     tokenDetails `json:"prompt_tokens_details"`
	CompletionDetails tokenDetails `json:"completion_tokens_details"`
}

func (u *tokenUsage) counters() (usage.Counters, bool) {
	if u == nil {
		return usage.Counters{}, false
	}
	input, output := u.InputTokens, u.OutputTokens
	if input == nil {
		input = u.PromptTokens
	}
	if output == nil {
		output = u.CompletionTokens
	}
	if input == nil || output == nil || *input < 0 || *output < 0 {
		return usage.Counters{}, false
	}
	return usage.Counters{
		InputTokens: *input, OutputTokens: *output,
		CachedTokens:    min(*input, max(0, u.InputDetails.CachedTokens, u.PromptDetails.CachedTokens)),
		ReasoningTokens: min(*output, max(0, u.OutputDetails.ReasoningTokens, u.CompletionDetails.ReasoningTokens)),
	}, true
}

func (o *observer) write(chunk []byte) error {
	for _, b := range chunk {
		if b != '\n' {
			if len(o.line) >= 1<<20 {
				return errors.New("SSE event exceeds 1 MiB observation limit")
			}
			o.line = append(o.line, b)
			continue
		}
		line := bytes.TrimSuffix(o.line, []byte{'\r'})
		if len(line) == 0 {
			data := bytes.TrimSpace(o.event)
			if bytes.Equal(data, []byte("[DONE]")) {
				o.complete = true
				o.sawDone = true
			} else if err := o.json(data); err != nil {
				return err
			}
			o.event = o.event[:0]
		} else if bytes.HasPrefix(line, []byte("data:")) {
			part := bytes.TrimPrefix(line[5:], []byte(" "))
			if len(o.event)+len(part) > 1<<20 {
				return errors.New("SSE event exceeds 1 MiB observation limit")
			}
			o.event = append(o.event, part...)
			o.event = append(o.event, '\n')
		}
		o.line = o.line[:0]
	}
	return nil
}
