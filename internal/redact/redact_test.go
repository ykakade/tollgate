package redact

import (
	"bytes"
	"encoding/json/jsontext"
	"strings"
	"testing"

	"github.com/ykakade/tollgate/internal/sse"
)

func newRedactor(t *testing.T, patterns ...Pattern) *Redactor {
	t.Helper()
	r, err := New(Kinds(), patterns)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMask(t *testing.T) {
	r := newRedactor(t, Pattern{Name: "EMPLOYEE_ID", Regex: `EMP-[0-9]{6}`})
	body := `{"model":"m","max_tokens":4111111111111111,"messages":[` +
		`{"role":"user","content":"Mail ann@example.com and bob@example.org.\nCard 4111 1111 1111 1111, SSN 123-45-6789, host 10.0.0.12, id EMP-004211."},` +
		`{"role":"assistant","content":"I wrote to ann@example.com."},` +
		`{"role":"user","content":"Card 1234 5678 9012 3456 fails Luhn. Version 1.2.3.4.5 is not an IP."}]}`
	out, m, err := r.Mask([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"model":"m","max_tokens":4111111111111111,"messages":[` +
		`{"role":"user","content":"Mail [EMAIL_1] and [EMAIL_2].\nCard [CARD_1], SSN [SSN_1], host [IP_1], id [EMPLOYEE_ID_1]."},` +
		`{"role":"assistant","content":"I wrote to [EMAIL_1]."},` +
		`{"role":"user","content":"Card 1234 5678 9012 3456 fails Luhn. Version 1.2.3.4.5 is not an IP."}]}`
	if string(out) != want {
		t.Errorf("Mask:\n got %s\nwant %s", out, want)
	}
	if !jsontext.Value(out).IsValid() {
		t.Error("the masked body is not valid JSON")
	}
	if m.Len() != 6 {
		t.Errorf("Len = %d, want 6", m.Len())
	}
	// The same body gives the same placeholders, so the prompt cache stays
	// valid.
	again, _, err := r.Mask([]byte(body))
	if err != nil || !bytes.Equal(again, out) {
		t.Errorf("a second Mask gave %s, %v", again, err)
	}
	got := m.Restore("Sent to [EMAIL_2] and [EMAIL_1], not [EMAIL_9] or [EMAIL_1")
	if got != "Sent to bob@example.org and ann@example.com, not [EMAIL_9] or [EMAIL_1" {
		t.Errorf("Restore = %q", got)
	}
}

func TestMaskNoMatchKeepsBody(t *testing.T) {
	r := newRedactor(t)
	body := []byte(`{"a":"caf\u00e9 \/ text","b":[1,2]}`)
	out, m, err := r.Mask(body)
	if err != nil || &out[0] != &body[0] || m.Len() != 0 {
		t.Errorf("Mask changed a body with no match: %s, %d, %v", out, m.Len(), err)
	}
	if _, _, err := r.Mask([]byte(`{"a":`)); err == nil {
		t.Error("Mask accepted JSON that is not valid")
	}
}

func TestUnmaskJSON(t *testing.T) {
	r := newRedactor(t, Pattern{Name: "QUOTE", Regex: `say "[a-z]+"`})
	_, m, err := r.Mask([]byte(`{"content":"Ask ann@example.com to say \"hello\"."}`))
	if err != nil {
		t.Fatal(err)
	}
	reply := `{"content":[{"type":"text","text":"I asked [EMAIL_1] to [QUOTE_1]."},{"type":"tool_use","input":{"to":"[EMAIL_1]"}}]}`
	got := string(m.UnmaskJSON([]byte(reply)))
	want := `{"content":[{"type":"text","text":"I asked ann@example.com to say \"hello\"."},{"type":"tool_use","input":{"to":"ann@example.com"}}]}`
	if got != want {
		t.Errorf("UnmaskJSON:\n got %s\nwant %s", got, want)
	}
}

func TestCheckPattern(t *testing.T) {
	for _, expr := range []string{"", "a*", "(", strings.Repeat("a", maxPatternLen+1)} {
		if CheckPattern(expr) == nil {
			t.Errorf("CheckPattern(%q) accepted the pattern", expr)
		}
	}
	if err := CheckPattern(`EMP-\d+`); err != nil {
		t.Errorf("CheckPattern: %v", err)
	}
	if _, err := New([]string{"phone"}, nil); err == nil {
		t.Error("New accepted an unknown kind")
	}
}

// events parses a stream into "name|data" lines.
func events(t *testing.T, stream string) []string {
	t.Helper()
	var out []string
	p := sse.NewParser(1 << 20)
	if err := p.Feed([]byte(stream), func(e sse.Event) { out = append(out, e.Name+"|"+string(e.Data)) }); err != nil {
		t.Fatal(err)
	}
	return out
}

// feed writes stream to a new unmasker in chunks of n bytes.
func feed(t *testing.T, m *Mapping, format, stream string, n int) string {
	t.Helper()
	var out bytes.Buffer
	u := m.NewStreamUnmasker(&out, format)
	for i := 0; i < len(stream); i += n {
		if _, err := u.Write([]byte(stream[i:min(i+n, len(stream))])); err != nil {
			t.Fatal(err)
		}
	}
	return out.String()
}

func TestStreamUnmaskerAnthropic(t *testing.T) {
	r := newRedactor(t)
	_, m, err := r.Mask([]byte(`{"content":"Write to ann@example.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	stream := "event: message_start\ndata: {\"type\":\"message_start\"}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Sent to [EMA\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"IL_1] now [\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"to\\\":\\\"[EMAIL_1]\\\"}\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	for _, n := range []int{1, 7, len(stream)} {
		got := events(t, feed(t, m, FormatAnthropic, stream, n))
		want := []string{
			`message_start|{"type":"message_start"}`,
			`content_block_delta|{"delta":{"text":"Sent to ","type":"text_delta"},"index":0,"type":"content_block_delta"}`,
			`content_block_delta|{"delta":{"text":"ann@example.com now ","type":"text_delta"},"index":0,"type":"content_block_delta"}`,
			`content_block_delta|{"delta":{"text":"[","type":"text_delta"},"index":0,"type":"content_block_delta"}`,
			`content_block_stop|{"type":"content_block_stop","index":0}`,
			`content_block_delta|{"delta":{"partial_json":"{\"to\":\"ann@example.com\"}","type":"input_json_delta"},"index":1,"type":"content_block_delta"}`,
			`message_stop|{"type":"message_stop"}`,
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("chunk size %d:\n got %s\nwant %s", n, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
}

func TestStreamUnmaskerOpenAI(t *testing.T) {
	r := newRedactor(t)
	_, m, err := r.Mask([]byte(`{"content":"Call 10.1.2.3"}`))
	if err != nil {
		t.Fatal(err)
	}
	stream := "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Host [I\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"P_1] [I\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"id\":\"c1\",\"choices\":[],\"usage\":{\"prompt_tokens\":9}}\n\n" +
		"data: [DONE]\n\n"
	got := events(t, feed(t, m, FormatOpenAI, stream, 5))
	want := []string{
		`|{"choices":[{"delta":{"content":"Host "},"finish_reason":null,"index":0}],"id":"c1"}`,
		`|{"choices":[{"delta":{"content":"10.1.2.3 "},"finish_reason":null,"index":0}],"id":"c1"}`,
		`|{"choices":[{"delta":{"content":"[I"},"finish_reason":"stop","index":0}],"id":"c1"}`,
		`|{"id":"c1","choices":[],"usage":{"prompt_tokens":9}}`,
		`|[DONE]`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("\n got %s\nwant %s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func FuzzMask(f *testing.F) {
	f.Add([]byte(`{"a":"ann@example.com 4111111111111111"}`))
	f.Add([]byte(`["10.0.0.1",{"k":"123-45-6789"}]`))
	r, err := New(Kinds(), nil)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		if !jsontext.Value(body).IsValid() {
			return
		}
		out, m, err := r.Mask(body)
		if err != nil {
			t.Fatalf("Mask of valid JSON failed: %v", err)
		}
		if !jsontext.Value(out).IsValid() {
			t.Fatalf("Mask made JSON that is not valid: %s", out)
		}
		back := m.UnmaskJSON(out)
		if !jsontext.Value(back).IsValid() {
			t.Fatalf("UnmaskJSON made JSON that is not valid: %s", back)
		}
	})
}
