package client

import (
	"encoding/json"
	"strconv"
)

// flexFloat is a number that a provider may send as a JSON string.
//
// opencode Go reports "cost":"0" — quoted — where OpenRouter sends 0.000942.
// Both are the same optional, decorative field, but a *float64 rejects the
// quoted form, and in the SSE parser a failed unmarshal raises a StreamError
// that ends the turn. A cosmetic field must never be able to do that: the
// answer is already on screen by the time usage arrives, and losing it to a
// pair of quotes would look like the model failed.
//
// Anything that is neither a number nor a numeric string decodes as absent
// rather than as an error, which is the same "we do not know" a missing field
// already means.
type flexFloat struct {
	Value float64
	Known bool
}

func (f *flexFloat) UnmarshalJSON(data []byte) error {
	// Decode into any and inspect, rather than trying float64 and falling back
	// on the error: every failure here means the same thing, "unknown", so
	// there is no error to propagate.
	// The error is deliberately ignored: data comes from a document the outer
	// decoder already validated, and on any failure v stays nil, the switch
	// below matches nothing, and the value reads as unknown — which is the
	// answer for every malformed case anyway.
	var v any
	_ = json.Unmarshal(data, &v)

	switch t := v.(type) {
	case float64:
		f.Value, f.Known = t, true
	case string:
		if n, err := strconv.ParseFloat(t, 64); err == nil {
			f.Value, f.Known = n, true
		}
	}
	return nil
}

// ptr returns the cost as llm.Usage wants it: nil when unknown.
func (f *flexFloat) ptr() *float64 {
	if f == nil || !f.Known {
		return nil
	}
	v := f.Value
	return &v
}
