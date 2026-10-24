package preflight

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"testing"
)

func TestDecode(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantModel  string
		wantStream bool
		wantErr    error
	}{
		{"anthropic", `{"model":"claude-x","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`, "claude-x", false, nil},
		{"stream true", `{"stream":true,"model":"m"}`, "m", true, nil},
		{"stream null", `{"stream":null,"model":"m"}`, "m", false, nil},
		{"whitespace", " \n{ \"model\" : \"m\" , \"stream\" : false }\n", "m", false, nil},
		{"escaped model", `{"model":"a\u0062c"}`, "abc", false, nil},
		{"nested model is ignored", `{"metadata":{"model":"x"},"model":"m"}`, "m", false, nil},
		{"not JSON", `{"model":`, "", false, ErrNotJSON},
		{"trailing data", `{"model":"m"} {}`, "", false, ErrNotJSON},
		{"duplicate model", `{"model":"a","model":"b"}`, "", false, ErrNotJSON},
		{"array", `[1]`, "", false, ErrNotObject},
		{"no model", `{"messages":[]}`, "", false, ErrNoModel},
		{"empty model", `{"model":""}`, "", false, ErrNoModel},
		{"model number", `{"model":5}`, "", false, ErrNoModel},
		{"stream string", `{"model":"m","stream":"yes"}`, "", false, ErrStream},
		{"empty", ``, "", false, ErrNotJSON},
	}
	for _, tt := range tests {
		r, err := Decode([]byte(tt.body))
		if tt.wantErr != nil {
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("%s: err = %v, want %v", tt.name, err, tt.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if r.Model != tt.wantModel || r.Stream != tt.wantStream {
			t.Errorf("%s: got model %q stream %v, want %q %v", tt.name, r.Model, r.Stream, tt.wantModel, tt.wantStream)
		}
	}
}

func TestRewrite(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		model        string
		includeUsage bool
		want         string
	}{
		{"no change", `{"model":"m","stream":true}`, "m", false, `{"model":"m","stream":true}`},
		{"model", `{"model": "m" ,"x":1}`, "provider-m", false, `{"model": "provider-m" ,"x":1}`},
		{"model with quote", `{"model":"m"}`, `a"b`, false, `{"model":"a\"b"}`},
		{"add stream_options", `{"model":"m","stream":true}`, "m", true, `{"stream_options":{"include_usage":true},"model":"m","stream":true}`},
		{"add stream_options with space", "{ \"model\":\"m\"}", "m", true, "{\"stream_options\":{\"include_usage\":true}, \"model\":\"m\"}"},
		{"null stream_options", `{"model":"m","stream_options":null}`, "m", true, `{"model":"m","stream_options":{"include_usage":true}}`},
		{"empty stream_options", `{"model":"m","stream_options":{ }}`, "m", true, `{"model":"m","stream_options":{"include_usage":true }}`},
		{"other option", `{"model":"m","stream_options":{"x":1}}`, "m", true, `{"model":"m","stream_options":{"include_usage":true,"x":1}}`},
		{"usage false", `{"model":"m","stream_options":{"include_usage": false}}`, "m", true, `{"model":"m","stream_options":{"include_usage": true}}`},
		{"usage true", `{"model":"m","stream_options":{"include_usage":true}}`, "m", true, `{"model":"m","stream_options":{"include_usage":true}}`},
		{"stream_options not object", `{"model":"m","stream_options":5}`, "m", true, `{"model":"m","stream_options":5}`},
		{"both edits", `{"stream_options":{},"model":"m"}`, "n", true, `{"stream_options":{"include_usage":true},"model":"n"}`},
		{"both edits model first", `{"model":"m","stream_options":{"a":true}}`, "longer-name", true, `{"model":"longer-name","stream_options":{"include_usage":true,"a":true}}`},
	}
	for _, tt := range tests {
		body := []byte(tt.body)
		r, err := Decode(body)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		got, err := r.Rewrite(tt.model, tt.includeUsage)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if string(got) != tt.want {
			t.Errorf("%s:\n got %s\nwant %s", tt.name, got, tt.want)
		}
		if string(body) != tt.body {
			t.Errorf("%s: Rewrite changed the input body", tt.name)
		}
	}
}

func TestEstimateInputTokens(t *testing.T) {
	for n, want := range map[int]int64{0: 0, 1: 1, 4: 1, 5: 2, 4000: 1000} {
		if got := EstimateInputTokens(n); got != want {
			t.Errorf("EstimateInputTokens(%d) = %d, want %d", n, got, want)
		}
	}
}

func FuzzPreflightDecode(f *testing.F) {
	f.Add([]byte(`{"model":"m","stream":true,"messages":[]}`))
	f.Add([]byte(`{"model":"m","stream_options":{"include_usage":false,"x":[1,{"y":null}]}}`))
	f.Add([]byte(`{"stream_options":null,"model":"\u00e9"}`))
	f.Add([]byte(`[{"model":"m"}]`))
	f.Fuzz(func(t *testing.T, body []byte) {
		r, err := Decode(body)
		if err != nil {
			return
		}
		out, err := r.Rewrite("new-model", true)
		if err != nil {
			t.Fatalf("Rewrite: %v", err)
		}
		if !jsontext.Value(out).IsValid() {
			t.Fatalf("Rewrite made JSON that is not valid: %s", out)
		}
		r2, err := Decode(out)
		if err != nil {
			t.Fatalf("Decode after Rewrite: %v\nbody: %s", err, out)
		}
		if r2.Model != "new-model" || r2.Stream != r.Stream {
			t.Fatalf("after Rewrite: model %q stream %v, want new-model %v", r2.Model, r2.Stream, r.Stream)
		}
		var opts struct {
			StreamOptions jsontext.Value `json:"stream_options"`
		}
		if err := json.Unmarshal(out, &opts); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if opts.StreamOptions.Kind() == '{' {
			var so struct {
				IncludeUsage bool `json:"include_usage"`
			}
			if err := json.Unmarshal(opts.StreamOptions, &so); err == nil && !so.IncludeUsage {
				t.Fatalf("include_usage is not true: %s", out)
			}
		}
	})
}
