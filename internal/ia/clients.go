package ia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// modelClient is one way to reach a model: text, tokens in and out.
type modelClient interface {
	complete(ctx context.Context, model string, req Request) (text string, in, out int64, err error)
}

// post sends JSON and decodes the JSON answer, with the API's message on
// an error status.
func post(ctx context.Context, h *http.Client, url string, headers map[string]string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := h.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return fmt.Errorf("%s answered %d: %s", url, res.StatusCode, msg)
	}
	return json.Unmarshal(raw, out)
}

// ollama is the local model, through Ollama's chat API (thinking off: the
// platform's questions are short).
type ollama struct {
	url  string
	http *http.Client
}

func (o ollama) complete(ctx context.Context, model string, req Request) (string, int64, int64, error) {
	msgs := []Message{}
	if req.System != "" {
		msgs = append(msgs, Message{Role: "system", Content: req.System})
	}
	msgs = append(msgs, req.Messages...)
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		PromptEvalCount int64 `json:"prompt_eval_count"`
		EvalCount       int64 `json:"eval_count"`
	}
	err := post(ctx, o.http, strings.TrimRight(o.url, "/")+"/api/chat", nil, map[string]any{
		"model": model, "messages": msgs, "stream": false, "think": false,
		"options": map[string]any{"num_predict": req.MaxTokens},
	}, &out)
	return out.Message.Content, out.PromptEvalCount, out.EvalCount, err
}

// anthropic is Anthropic's Messages API.
type anthropic struct {
	url, key string
	http     *http.Client
}

func (a anthropic) complete(ctx context.Context, model string, req Request) (string, int64, int64, error) {
	url := or(a.url, "https://api.anthropic.com")
	body := map[string]any{"model": model, "max_tokens": req.MaxTokens, "messages": req.Messages}
	if req.System != "" {
		body["system"] = req.System
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			In  int64 `json:"input_tokens"`
			Out int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	err := post(ctx, a.http, strings.TrimRight(url, "/")+"/v1/messages",
		map[string]string{"x-api-key": a.key, "anthropic-version": "2023-06-01"}, body, &out)
	var text strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	return text.String(), out.Usage.In, out.Usage.Out, err
}

// openai is any OpenAI-compatible chat completions API (OpenAI, and the
// many services that speak it).
type openai struct {
	base, key string
	http      *http.Client
}

func (o openai) complete(ctx context.Context, model string, req Request) (string, int64, int64, error) {
	if o.base == "" || model == "" {
		return "", 0, 0, errors.New("an OpenAI-compatible provider needs its address and a model")
	}
	msgs := []Message{}
	if req.System != "" {
		msgs = append(msgs, Message{Role: "system", Content: req.System})
	}
	msgs = append(msgs, req.Messages...)
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			In  int64 `json:"prompt_tokens"`
			Out int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	err := post(ctx, o.http, strings.TrimRight(o.base, "/")+"/chat/completions",
		map[string]string{"Authorization": "Bearer " + o.key}, map[string]any{"model": model, "messages": msgs, "max_tokens": req.MaxTokens}, &out)
	text := ""
	if len(out.Choices) > 0 {
		text = out.Choices[0].Message.Content
	}
	return text, out.Usage.In, out.Usage.Out, err
}
