package gateway

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestRequestObjectMatchesUnmarshal(t *testing.T) {
	for _, body := range []string{
		`{}`, ` { } `, `{"model":"m"}`,
		"{\r\n\t\"model\" : \"m\" ,\n \"stream\":true , \"n\":2}",
		`{"model":"a","model":"b"}`,
		`{"model":"escaped","messages":[{"content":"} ] { [ \" \\"}],"x":{"y":[1,2,{"z":null}]}}`,
		`{"a":-1.5e+3,"b":false,"c":null,"d":"","e":[],"f":{}}`,
		`{"stream_options":{"include_usage":false},"model":"m"}`,
	} {
		obj, ok := parseObject([]byte(body))
		if !ok {
			t.Fatalf("rejected %s", body)
		}
		var want map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &want); err != nil {
			t.Fatal(err)
		}
		got := map[string]json.RawMessage{}
		for _, m := range obj.members {
			got[m.key] = json.RawMessage(body[m.valueStart:m.end])
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %s want %s", body, got, want)
		}
		for key, value := range want {
			if raw, _ := obj.get(key); !bytes.Equal(raw, value) {
				t.Fatalf("%s: get(%q) = %s", body, key, raw)
			}
		}
		rewritten := obj.with("model", []byte(`"mapped"`))
		var after map[string]json.RawMessage
		if err := json.Unmarshal(rewritten, &after); err != nil {
			t.Fatalf("%s: invalid rewrite %s", body, rewritten)
		}
		want["model"] = json.RawMessage(`"mapped"`)
		if !reflect.DeepEqual(after, want) {
			t.Fatalf("%s: rewrite %s", body, rewritten)
		}
		if bytes.Count(rewritten, []byte(`"mapped"`)) != 1 {
			t.Fatalf("%s: duplicate keys survived rewrite: %s", body, rewritten)
		}
	}
	for _, body := range []string{``, `null`, `[]`, `"x"`, `{"a":1}{}`, `{"a":}`, `{"a":1,}`} {
		if _, ok := parseObject([]byte(body)); ok {
			t.Fatalf("accepted %q", body)
		}
	}
}

func FuzzRequestObject(f *testing.F) {
	f.Add([]byte(`{"model":"m","messages":[{"content":"x"}],"stream":true}`))
	f.Add([]byte(`{"a\"b":{"c":[1,"]"]},"model":null}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		obj, ok := parseObject(body)
		var want map[string]json.RawMessage
		if (json.Unmarshal(body, &want) == nil && want != nil) != ok {
			t.Fatalf("acceptance differs for %q", body)
		}
		if !ok {
			return
		}
		for key, value := range want {
			if raw, _ := obj.get(key); !bytes.Equal(bytes.TrimSpace(raw), bytes.TrimSpace(value)) {
				t.Fatalf("get(%q) = %q want %q", key, raw, value)
			}
		}
		if !json.Valid(obj.with("model", []byte(`"m"`))) {
			t.Fatal("rewrite produced invalid JSON")
		}
	})
}
