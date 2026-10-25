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

func TestDecodeMaxTokens(t *testing.T) {
	for _, tt := range []struct {
		body string
		want int64
	}{
		{`{"model":"m","max_tokens":1024}`, 1024},
		{`{"model":"m"}`, 0},
		{`{"model":"m","max_completion_tokens":300,"max_tokens":20}`, 300},
		{`{"max_tokens":20,"max_completion_tokens":null,"model":"m"}`, 20},
		{`{"model":"m","max_tokens":1.5}`, 0},
		{`{"model":"m","max_tokens":-4}`, 0},
		{`{"model":"m","max_tokens":"9"}`, 0},
		{`{"model":"m","max_tokens":1e3}`, 0},
		{`{"model":"m","max_tokens":99999999999999999999}`, 0},
		{`{"model":"m","metadata":{"max_tokens":7}}`, 0},
	} {
		r, err := Decode([]byte(tt.body))
		if err != nil {
			t.Errorf("Decode(%s): %v", tt.body, err)
			continue
		}
		if r.MaxTokens != tt.want {
			t.Errorf("Decode(%s).MaxTokens = %d, want %d", tt.body, r.MaxTokens, tt.want)
		}
	}
	if _, err := Decode([]byte(`{"model":"m","max_tokens":[}`)); !errors.Is(err, ErrNotJSON) {
		t.Errorf("Decode() of a bad max_tokens: err = %v, want ErrNotJSON", err)
	}
}

func TestEstimateInputTokens(t *testing.T) {
	for n, want := range map[int]int64{0: 0, 1: 1, 4: 1, 5: 2, 4000: 1000} {
		if got := EstimateInputTokens(n); got != want {
			t.Errorf("EstimateInputTokens(%d) = %d, want %d", n, got, want)
		}
	}
}

func TestVertexBody(t *testing.T) {
	const v = "vertex-2023-10-16"
	for _, tt := range []struct{ name, body, want string }{
		{"model first", `{"model":"m","max_tokens":5}`, `{"anthropic_version":"vertex-2023-10-16","max_tokens":5}`},
		{"model first with space", "{ \"model\" : \"m\" ,\n \"max_tokens\":5}", "{\"anthropic_version\":\"vertex-2023-10-16\",\n \"max_tokens\":5}"},
		{"model last", `{"max_tokens":5, "model":"m"}`, `{"anthropic_version":"vertex-2023-10-16","max_tokens":5}`},
		{"model in the middle", `{"a":1,"model":"m","b":2}`, `{"anthropic_version":"vertex-2023-10-16","a":1,"b":2}`},
		{"only model", `{"model":"m"}`, `{"anthropic_version":"vertex-2023-10-16"}`},
		{"version in body", `{"anthropic_version":"x","model":"m"}`, `{"anthropic_version":"x"}`},
		{"nested model stays", `{"model":"m","metadata":{"model":"keep"}}`, `{"anthropic_version":"vertex-2023-10-16","metadata":{"model":"keep"}}`},
	} {
		body := []byte(tt.body)
		r, err := Decode(body)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		got, err := r.VertexBody(v)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if string(got) != tt.want {
			t.Errorf("%s:\n got %s\nwant %s", tt.name, got, tt.want)
		}
		if string(body) != tt.body {
			t.Errorf("%s: VertexBody changed the input body", tt.name)
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
		if h := r.PrefixHash(); h != "" && len(h) != 32 {
			t.Fatalf("PrefixHash = %q, want 32 hex digits", h)
		}
		out, err := r.Rewrite("new-model", true)
		if err != nil {
			t.Fatalf("Rewrite: %v", err)
		}
		if !jsontext.Value(out).IsValid() {
			t.Fatalf("Rewrite made JSON that is not valid: %s", out)
		}
		vb, err := r.VertexBody("vertex-2023-10-16")
		if err != nil {
			t.Fatalf("VertexBody: %v", err)
		}
		var top map[string]jsontext.Value
		if err := json.Unmarshal(vb, &top); err != nil {
			t.Fatalf("VertexBody made JSON that is not valid: %v\nbody: %s", err, vb)
		}
		if _, ok := top["model"]; ok || top["anthropic_version"] == nil {
			t.Fatalf("VertexBody: model or anthropic_version is wrong: %s", vb)
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

func TestPrefixHash(t *testing.T) {
	hash := func(body string) string {
		t.Helper()
		r, err := Decode([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		return r.PrefixHash()
	}
	turn1 := `{"model":"m","system":"Be brief.","messages":[{"role":"user","content":"Fix the bug."}]}`
	turn2 := `{"model":"m2","max_tokens":9,"system":"Be brief.","messages":[{"role":"user","content":"Fix the bug."},{"role":"assistant","content":"Done."},{"role":"user","content":"Thanks."}]}`
	h := hash(turn1)
	if len(h) != 32 || h != hash(turn2) {
		t.Errorf("two turns of one session have the hashes %q and %q", h, hash(turn2))
	}
	for _, other := range []string{
		`{"model":"m","system":"Be long.","messages":[{"role":"user","content":"Fix the bug."}]}`,
		`{"model":"m","system":"Be brief.","messages":[{"role":"user","content":"Fix the test."}]}`,
		`{"model":"m","messages":[{"role":"user","content":"Fix the bug."}]}`,
	} {
		if hash(other) == h {
			t.Errorf("a different session has the same hash: %s", other)
		}
	}
	openai1 := `{"model":"g","messages":[{"role":"system","content":"S"},{"role":"user","content":"U"}]}`
	openai2 := `{"model":"g","messages":[{"role":"system","content":"S"},{"role":"user","content":"U"},{"role":"assistant","content":"A"}]}`
	if hash(openai1) == "" || hash(openai1) != hash(openai2) {
		t.Errorf("OpenAI turns have the hashes %q and %q", hash(openai1), hash(openai2))
	}
	for _, none := range []string{
		`{"model":"m"}`,
		`{"model":"m","messages":[]}`,
		`{"model":"m","messages":[{"role":"system","content":"S"}]}`,
		`{"model":"m","messages":"text"}`,
	} {
		if got := hash(none); got != "" {
			t.Errorf("PrefixHash(%s) = %q, want empty", none, got)
		}
	}
}
