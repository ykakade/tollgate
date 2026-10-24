package server

import (
	"bytes"
	"encoding/json/v2"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// streamsDir holds the golden files. See its README.md for their source.
var streamsDir = filepath.Join("..", "..", "testdata", "streams")

// chunked returns an upstream handler that sends body in chunks of random
// size, with a flush after each chunk, so the relay and the meters read
// events that are split at any byte.
func chunked(contentType string, body []byte, seed uint64) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		rng := rand.New(rand.NewPCG(seed, seed))
		for len(body) > 0 {
			n := min(1+rng.IntN(64), len(body))
			_, _ = w.Write(body[:n])
			w.(http.Flusher).Flush()
			body = body[n:]
		}
	}
}

type wantUsage struct {
	InputTokens      float64 `json:"input_tokens"`
	OutputTokens     float64 `json:"output_tokens"`
	CacheReadTokens  float64 `json:"cache_read_tokens"`
	CacheWriteTokens float64 `json:"cache_write_tokens"`
	ReasoningTokens  float64 `json:"reasoning_tokens"`
}

func TestGoldenStreams(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(streamsDir, "*.usage.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no golden files in %s (err %v)", streamsDir, err)
	}
	for _, usageFile := range files {
		name := strings.TrimSuffix(filepath.Base(usageFile), ".usage.json")
		var want wantUsage
		b, err := os.ReadFile(usageFile)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &want); err != nil {
			t.Fatalf("%s: %v", usageFile, err)
		}

		stream := true
		body, err := os.ReadFile(filepath.Join(streamsDir, name+".sse"))
		if os.IsNotExist(err) {
			stream = false
			body, err = os.ReadFile(filepath.Join(streamsDir, name+".json"))
		}
		if err != nil {
			t.Fatal(err)
		}

		variants := map[string][]byte{name: body}
		if stream {
			variants[name+"_crlf"] = bytes.ReplaceAll(body, []byte("\n"), []byte("\r\n"))
		}
		for vname, vbody := range variants {
			t.Run(vname, func(t *testing.T) {
				contentType := "application/json"
				if stream {
					contentType = "text/event-stream"
				}
				e := newEnv(t, chunked(contentType, vbody, uint64(len(vname))), setup{})
				path, reqBody := "/v1/chat/completions", `{"model":"gpt-test","messages":[]}`
				if strings.HasPrefix(name, "anthropic") {
					path, reqBody = "/v1/messages", `{"model":"claude-test","max_tokens":100,"messages":[]}`
				}
				if stream {
					reqBody = strings.Replace(reqBody, `"messages"`, `"stream":true,"messages"`, 1)
				}
				resp, got := e.post(path, reqBody)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status %d, body %s", resp.StatusCode, got)
				}
				if got != string(vbody) {
					t.Fatalf("the relay changed the bytes.\n got: %q\nwant: %q", got, vbody)
				}
				line := e.logLine(resp.Header.Get("X-Request-Id"))
				gotUsage := wantUsage{
					InputTokens:      num(line["input_tokens"]),
					OutputTokens:     num(line["output_tokens"]),
					CacheReadTokens:  num(line["cache_read_tokens"]),
					CacheWriteTokens: num(line["cache_write_tokens"]),
					ReasoningTokens:  num(line["reasoning_tokens"]),
				}
				if gotUsage != want || line["usage_estimated"] != false {
					t.Errorf("usage = %+v (estimated %v), want %+v", gotUsage, line["usage_estimated"], want)
				}
			})
		}
	}
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}
