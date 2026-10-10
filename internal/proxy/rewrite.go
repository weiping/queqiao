package proxy

import (
	"bytes"
	"encoding/json"
)

var bom = []byte("\xef\xbb\xbf")

// topModel finds the top-level "model" string of a JSON object body: its
// value and the byte range of the value (quotes included). ok is false for
// a body that isn't an object, or has no such string.
func topModel(body []byte) (model string, start, end int, ok bool) {
	off := 0
	if bytes.HasPrefix(body, bom) {
		off = len(bom)
	}
	dec := json.NewDecoder(bytes.NewReader(body[off:]))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return "", 0, 0, false
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return "", 0, 0, false
		}
		key, _ := t.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return "", 0, 0, false
		}
		if key != "model" {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return "", 0, 0, false
		}
		e := off + int(dec.InputOffset())
		return s, e - len(raw), e, true
	}
	return "", 0, 0, false
}

// RewriteModel replaces the top-level "model" value of body with model,
// leaving every other byte as it was; ok is false when body has none.
func RewriteModel(body []byte, model string) ([]byte, bool) {
	_, start, end, ok := topModel(body)
	if !ok {
		return body, false
	}
	v, _ := json.Marshal(model)
	out := make([]byte, 0, len(body)-(end-start)+len(v))
	out = append(append(append(out, body[:start]...), v...), body[end:]...)
	return out, true
}
