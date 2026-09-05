package coder

import (
	"bytes"
	"encoding/json"
)

// orderedProps is a JSON-Schema "properties" object that marshals its members
// in the order they are declared. A map cannot: encoding/json sorts map keys,
// so `content` went out ahead of `path` for no reason but the alphabet.
//
// The order is not decoration. Some models emit a tool call's arguments in the
// order the schema lists them, and Strument's streaming diff cannot draw a line
// until it knows which file the line belongs to — so with `content` advertised
// first, a 663-line write from Qwen3.6 arrived as `{"content": …, "path": …}`
// and the whole diff appeared in one block at the end instead of scrolling past
// as it was written. Naming `path` first restores the live diff for a model
// that follows the schema.
//
// It is a nudge, not a contract: nothing in either wire protocol obliges a
// model to order its arguments at all, and models that order by instinct will
// carry on doing so. render.ToolDiff still buffers diff lines until the path
// arrives, whenever it arrives, and that stays the thing that makes the output
// correct. This only makes the fast path reachable more often.
//
// Only the tools whose arguments are rendered as a streaming diff use this;
// everywhere else the order changes nothing a reader or a model can see, and a
// map is the plainer thing to write.
type orderedProps []schemaProp

// schemaProp is one property: its name, and the schema fragment describing it.
type schemaProp struct {
	name   string
	schema map[string]any
}

// MarshalJSON writes the properties as a JSON object in declared order.
func (p orderedProps) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, prop := range p {
		if i > 0 {
			buf.WriteByte(',')
		}
		name, err := json.Marshal(prop.name)
		if err != nil {
			return nil, err
		}
		buf.Write(name)
		buf.WriteByte(':')
		schema, err := json.Marshal(prop.schema)
		if err != nil {
			return nil, err
		}
		buf.Write(schema)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// lookup returns the named property's schema, for tests and callers that want
// to read one back out. A map indexes; this has to search, which is the price
// of the order and is paid nowhere hot.
func (p orderedProps) lookup(name string) (map[string]any, bool) {
	for _, prop := range p {
		if prop.name == name {
			return prop.schema, true
		}
	}
	return nil, false
}

// names lists the properties in declared order.
func (p orderedProps) names() []string {
	out := make([]string, len(p))
	for i, prop := range p {
		out[i] = prop.name
	}
	return out
}
