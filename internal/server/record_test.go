//go:build record

package server

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRecord sends requests to the real providers and writes their
// responses to testdata/streams, in place of the files that were written by
// hand. Run it with "make record". It needs these environment variables:
//
//   - ANTHROPIC_API_KEY and RECORD_ANTHROPIC_MODEL for the Anthropic files.
//   - OPENAI_API_KEY and RECORD_OPENAI_MODEL for the OpenAI files.
//   - RECORD_ANTHROPIC_BASE_URL and RECORD_OPENAI_BASE_URL are optional.
//
// The test does not write the .usage.json files. After a recording, read
// the usage in each file and write its .usage.json file by hand, so the
// golden test checks the meters against numbers that a person read.
func TestRecord(t *testing.T) {
	anthropicKey, anthropicModel := os.Getenv("ANTHROPIC_API_KEY"), os.Getenv("RECORD_ANTHROPIC_MODEL")
	openaiKey, openaiModel := os.Getenv("OPENAI_API_KEY"), os.Getenv("RECORD_OPENAI_MODEL")
	anthropicURL := envOr("RECORD_ANTHROPIC_BASE_URL", "https://api.anthropic.com") + "/v1/messages"
	openaiURL := envOr("RECORD_OPENAI_BASE_URL", "https://api.openai.com") + "/v1/chat/completions"

	const weatherTool = `{"name":"get_weather","description":"Get the current weather in a location.","input_schema":{"type":"object","properties":{"location":{"type":"string"},"unit":{"type":"string","enum":["celsius","fahrenheit"]}},"required":["location"]}}`
	const openaiWeatherTool = `{"type":"function","function":{"name":"get_weather","description":"Get the current weather in a location.","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}}`
	recordings := []struct {
		file, url, key, model, body string
	}{
		{"anthropic_text.sse", anthropicURL, anthropicKey, anthropicModel,
			`{"model":%q,"max_tokens":64,"stream":true,"messages":[{"role":"user","content":"Say hello in five words."}]}`},
		{"anthropic_tool_use.sse", anthropicURL, anthropicKey, anthropicModel,
			`{"model":%q,"max_tokens":256,"stream":true,"tools":[` + weatherTool + `],"messages":[{"role":"user","content":"What is the weather in San Francisco? Use the tool."}]}`},
		{"anthropic_thinking.sse", anthropicURL, anthropicKey, anthropicModel,
			`{"model":%q,"max_tokens":2048,"stream":true,"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"What is 27 * 453?"}]}`},
		{"anthropic_message.json", anthropicURL, anthropicKey, anthropicModel,
			`{"model":%q,"max_tokens":64,"messages":[{"role":"user","content":"Say hello in five words."}]}`},
		{"openai_text_usage.sse", openaiURL, openaiKey, openaiModel,
			`{"model":%q,"stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"Say hello in five words."}]}`},
		{"openai_tool_calls.sse", openaiURL, openaiKey, openaiModel,
			`{"model":%q,"stream":true,"stream_options":{"include_usage":true},"tools":[` + openaiWeatherTool + `],"messages":[{"role":"user","content":"What is the weather in Paris? Use the tool."}]}`},
		{"openai_completion.json", openaiURL, openaiKey, openaiModel,
			`{"model":%q,"messages":[{"role":"user","content":"Say hello in five words."}]}`},
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	for _, rec := range recordings {
		t.Run(rec.file, func(t *testing.T) {
			if rec.key == "" || rec.model == "" {
				t.Skip("the API key or the model variable is not set")
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, rec.url, strings.NewReader(fmt.Sprintf(rec.body, rec.model)))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			if strings.HasPrefix(rec.file, "anthropic") {
				req.Header.Set("X-Api-Key", rec.key)
				req.Header.Set("Anthropic-Version", "2023-06-01")
			} else {
				req.Header.Set("Authorization", "Bearer "+rec.key)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d: %s", resp.StatusCode, body)
			}
			path := filepath.Join(streamsDir, rec.file)
			if err := os.WriteFile(path, body, 0o644); err != nil {
				t.Fatal(err)
			}
			t.Logf("wrote %s. Read its usage and update the .usage.json file.", path)
		})
	}
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return strings.TrimSuffix(v, "/")
	}
	return def
}
