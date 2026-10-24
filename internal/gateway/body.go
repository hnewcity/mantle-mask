package gateway

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"
)

// requestObject indexes the top-level members of a JSON object in place.
// Values are sub-slices of body, so routing lookups on a multi-megabyte prompt
// do not copy it, and a rewrite copies the body exactly once.
type requestObject struct {
	body    []byte
	members []member
}

type member struct {
	key        string // unescaped
	start      int    // offset of the key's opening quote
	valueStart int
	end        int // offset just past the value
}

// parseObject accepts exactly one JSON object, like json.Unmarshal into a map.
func parseObject(body []byte) (requestObject, bool) {
	if !json.Valid(body) {
		return requestObject{}, false
	}
	return indexObject(body)
}

// indexObject requires body to be valid JSON already.
func indexObject(body []byte) (requestObject, bool) {
	i := skipSpace(body, 0)
	if i >= len(body) || body[i] != '{' {
		return requestObject{}, false
	}
	obj := requestObject{body: body}
	i = skipSpace(body, i+1)
	if body[i] == '}' {
		return obj, true
	}
	for {
		start := i
		end := skipString(body, i)
		key := string(body[start+1 : end-1])
		if (bytes.IndexByte(body[start:end], '\\') >= 0 || !utf8.Valid(body[start:end])) && json.Unmarshal(body[start:end], &key) != nil {
			return requestObject{}, false
		}
		i = skipSpace(body, skipSpace(body, end)+1) // past ':'
		valueStart := i
		i = skipValue(body, i)
		obj.members = append(obj.members, member{key: key, start: start, valueStart: valueStart, end: i})
		i = skipSpace(body, i)
		if body[i] == '}' {
			return obj, true
		}
		i = skipSpace(body, i+1) // past ','
	}
}

// get matches json.Unmarshal into a map: the last duplicate key wins.
func (o requestObject) get(key string) ([]byte, bool) {
	for i := len(o.members) - 1; i >= 0; i-- {
		if o.members[i].key == key {
			m := o.members[i]
			return o.body[m.valueStart:m.end], true
		}
	}
	return nil, false
}

// with returns a copy of the object with every member named key (including
// escaped spellings) replaced by one member holding value. Other members keep
// their original bytes and order.
func (o requestObject) with(key string, value []byte) []byte {
	name, _ := json.Marshal(key)
	out := make([]byte, 0, len(o.body)+len(name)+len(value)+3)
	out = append(out, '{')
	for _, m := range o.members {
		if m.key == key {
			continue
		}
		if len(out) > 1 {
			out = append(out, ',')
		}
		out = append(out, o.body[m.start:m.end]...)
	}
	if len(out) > 1 {
		out = append(out, ',')
	}
	out = append(out, name...)
	out = append(out, ':')
	out = append(out, value...)
	return append(out, '}')
}

func skipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// skipString returns the offset just past the string starting at b[i] == '"'.
func skipString(b []byte, i int) int {
	for i++; b[i] != '"'; i++ {
		if b[i] == '\\' {
			i++
		}
	}
	return i + 1
}

func skipValue(b []byte, i int) int {
	switch b[i] {
	case '"':
		return skipString(b, i)
	case '{', '[':
		depth := 0
		for {
			switch b[i] {
			case '"':
				i = skipString(b, i)
				continue
			case '{', '[':
				depth++
			case '}', ']':
				if depth--; depth == 0 {
					return i + 1
				}
			}
			i++
		}
	default:
		for i < len(b) && b[i] != ',' && b[i] != '}' && b[i] != ']' && b[i] != ' ' && b[i] != '\t' && b[i] != '\n' && b[i] != '\r' {
			i++
		}
		return i
	}
}
