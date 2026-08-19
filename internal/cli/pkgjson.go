package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Order-preserving edits to package.json.
//
// lnpm changes exactly one entry of "dependencies" or "devDependencies". Parsing
// the file into a map[string]interface{} and re-marshalling it would rewrite the
// whole document: encoding/json sorts map keys alphabetically, forces a 2-space
// indent, and rounds every number through float64. The result is a diff over
// nearly every line and silent corruption of integer literals beyond 2^53.
//
// So the editors below never re-serialize the document. They locate the byte
// range of the entry to change using encoding/json's token stream - real JSON
// parsing, no regex guesswork - and splice new bytes into that range. Everything
// outside it comes out byte-identical, which preserves key order, indentation
// style, line endings and number literals for free.

// jsonMember is one key/value pair of a JSON object, with the byte offsets its
// key and value occupy in the source.
type jsonMember struct {
	key      string
	keyStart int // offset of the key's opening quote
	valStart int // offset of the value's first byte
	valEnd   int // offset just past the value's last byte
}

// jsonObject is a parsed JSON object: the offsets of its braces and the spans of
// its members, all relative to the source the object was parsed from.
type jsonObject struct {
	open    int // offset of '{'
	close   int // offset just past '}'
	members []jsonMember
}

// member returns the last member with the given key, or nil. Last wins because
// that is how encoding/json resolves duplicate keys.
func (o *jsonObject) member(key string) *jsonMember {
	for i := len(o.members) - 1; i >= 0; i-- {
		if o.members[i].key == key {
			return &o.members[i]
		}
	}
	return nil
}

// setPackageJSONDep sets field.name to value, leaving every other byte of src
// alone. A missing or non-object field is created.
func setPackageJSONDep(src []byte, field, name, value string) ([]byte, error) {
	top, err := parsePackageJSON(src)
	if err != nil {
		return nil, err
	}

	encoded, err := encodeJSONString(value)
	if err != nil {
		return nil, err
	}
	key, err := encodeJSONString(name)
	if err != nil {
		return nil, err
	}
	entry := key + ": " + encoded

	unit := detectIndentUnit(src, top)
	nl := detectLineEnding(src)

	target := top.member(field)

	// The field is missing: add it, holding just this entry.
	if target == nil {
		fieldKey, err := encodeJSONString(field)
		if err != nil {
			return nil, err
		}
		indent, inline := memberIndent(src, top, unit)
		if inline {
			return insertMember(src, top, fieldKey+": {"+entry+"}", unit, nl), nil
		}
		body := "{" + nl + indent + unit + entry + nl + indent + "}"
		return insertMember(src, top, fieldKey+": "+body, unit, nl), nil
	}

	// The field is present but holds something other than an object (null, say):
	// replace it wholesale, as the map-based code used to.
	if src[target.valStart] != '{' {
		indent := lineLeadingSpace(src, target.keyStart)
		body := "{" + nl + indent + unit + entry + nl + indent + "}"
		return splice(src, target.valStart, target.valEnd, body), nil
	}

	deps, err := parseObject(src, target.valStart, target.valEnd)
	if err != nil {
		return nil, err
	}

	// The entry already exists: replace its value in place, keeping its position.
	if existing := deps.member(name); existing != nil {
		return splice(src, existing.valStart, existing.valEnd, encoded), nil
	}

	return insertMember(src, deps, entry, unit, nl), nil
}

// deletePackageJSONDep removes field.name, leaving every other byte of src
// alone. Removing an entry that is not there is a no-op, not an error.
func deletePackageJSONDep(src []byte, field, name string) ([]byte, error) {
	top, err := parsePackageJSON(src)
	if err != nil {
		return nil, err
	}

	target := top.member(field)
	if target == nil || src[target.valStart] != '{' {
		return src, nil
	}

	deps, err := parseObject(src, target.valStart, target.valEnd)
	if err != nil {
		return nil, err
	}

	i := -1
	for j := range deps.members {
		if deps.members[j].key == name {
			i = j
			break
		}
	}
	if i < 0 {
		return src, nil
	}

	victim := deps.members[i]
	switch {
	case i+1 < len(deps.members):
		// Delete up to the next key, so that key inherits the indentation the
		// removed one sat on and its separating comma goes with it.
		return splice(src, victim.keyStart, deps.members[i+1].keyStart, ""), nil
	case i > 0:
		// Last of several: take the comma that preceded it too.
		return splice(src, deps.members[i-1].valEnd, victim.valEnd, ""), nil
	default:
		// The only member: collapse the object to "{}".
		return splice(src, deps.open+1, deps.close-1, ""), nil
	}
}

// hasPackageJSONDep reports whether field.name is present in src.
func hasPackageJSONDep(src []byte, field, name string) (bool, error) {
	top, err := parsePackageJSON(src)
	if err != nil {
		return false, err
	}

	target := top.member(field)
	if target == nil || src[target.valStart] != '{' {
		return false, nil
	}

	deps, err := parseObject(src, target.valStart, target.valEnd)
	if err != nil {
		return false, err
	}
	return deps.member(name) != nil, nil
}

// insertMember appends entry ("key": value, already encoded) to obj, matching
// the layout of the members already there.
func insertMember(src []byte, obj *jsonObject, entry, unit, nl string) []byte {
	indent, inline := memberIndent(src, obj, unit)

	if len(obj.members) > 0 {
		last := obj.members[len(obj.members)-1]
		if inline {
			return splice(src, last.valEnd, last.valEnd, ", "+entry)
		}
		return splice(src, last.valEnd, last.valEnd, ","+nl+indent+entry)
	}

	// Empty object: expand it around the single new member.
	closeIndent := lineLeadingSpace(src, obj.open)
	return splice(src, obj.open+1, obj.close-1, nl+indent+entry+nl+closeIndent)
}

// memberIndent returns the indentation a new member of obj should carry, and
// whether obj is written on a single line (in which case the indent is unused).
func memberIndent(src []byte, obj *jsonObject, unit string) (string, bool) {
	if len(obj.members) > 0 {
		last := obj.members[len(obj.members)-1]
		if indent, ok := lineIndentOf(src, last.keyStart); ok {
			return indent, false
		}
		return "", true
	}
	return lineLeadingSpace(src, obj.open) + unit, false
}

// splice replaces src[start:end] with text.
func splice(src []byte, start, end int, text string) []byte {
	out := make([]byte, 0, len(src)-(end-start)+len(text))
	out = append(out, src[:start]...)
	out = append(out, text...)
	out = append(out, src[end:]...)
	return out
}

// lineIndentOf returns everything between the start of pos's line and pos, if
// that run is nothing but whitespace. It reports false when pos is preceded by
// other content on its line, which means the enclosing object is written inline.
func lineIndentOf(src []byte, pos int) (string, bool) {
	start := lineStart(src, pos)
	for i := start; i < pos; i++ {
		if !isSpace(src[i]) {
			return "", false
		}
	}
	return string(src[start:pos]), true
}

// lineLeadingSpace returns the leading whitespace of the line pos sits on,
// regardless of what else that line holds.
func lineLeadingSpace(src []byte, pos int) string {
	start := lineStart(src, pos)
	end := start
	for end < pos && isSpace(src[end]) {
		end++
	}
	return string(src[start:end])
}

func lineStart(src []byte, pos int) int {
	return bytes.LastIndexByte(src[:pos], '\n') + 1
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r'
}

// detectIndentUnit works out one level of indentation from the first top-level
// member, falling back to two spaces for a file that offers no evidence.
func detectIndentUnit(src []byte, top *jsonObject) string {
	if len(top.members) > 0 {
		if indent, ok := lineIndentOf(src, top.members[0].keyStart); ok && indent != "" {
			return indent
		}
	}
	return "  "
}

// detectLineEnding reports the line ending the file already uses, so inserted
// lines match it. Windows checkouts routinely have CRLF package.json files.
func detectLineEnding(src []byte) string {
	if i := bytes.IndexByte(src, '\n'); i > 0 && src[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// ensureTrailingNewline keeps lnpm's long-standing habit of leaving
// package.json with a final newline.
func ensureTrailingNewline(src []byte) []byte {
	if len(src) > 0 && src[len(src)-1] == '\n' {
		return src
	}
	return append(src, detectLineEnding(src)...)
}

// encodeJSONString renders s as a JSON string literal.
func encodeJSONString(s string) (string, error) {
	encoded, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// parsePackageJSON validates src and parses its top-level object. It rejects
// anything json.Unmarshal into a map would have rejected, so callers that used
// to abort on an unparseable package.json still do.
func parsePackageJSON(src []byte) (*jsonObject, error) {
	if !json.Valid(src) {
		return nil, fmt.Errorf("package.json is not valid JSON")
	}
	return parseObject(src, 0, len(src))
}

// parseObject parses the JSON object occupying src[start:end], returning member
// spans as offsets into src.
func parseObject(src []byte, start, end int) (*jsonObject, error) {
	dec := json.NewDecoder(bytes.NewReader(src[start:end]))
	dec.UseNumber()

	openAt, tok, err := nextToken(src, start, dec)
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("package.json must contain a JSON object at its top level")
	}

	obj := &jsonObject{open: openAt}
	for dec.More() {
		keyStart, keyTok, err := nextToken(src, start, dec)
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected JSON object key at byte %d", keyStart)
		}

		valStart, valTok, err := nextToken(src, start, dec)
		if err != nil {
			return nil, err
		}
		valEnd, err := skipValue(src, start, dec, valTok)
		if err != nil {
			return nil, err
		}

		obj.members = append(obj.members, jsonMember{key: key, keyStart: keyStart, valStart: valStart, valEnd: valEnd})
	}
	if _, _, err := nextToken(src, start, dec); err != nil {
		return nil, err
	}
	obj.close = start + int(dec.InputOffset())
	return obj, nil
}

// skipValue consumes the rest of a value whose first token has already been
// read, returning the offset just past its last byte.
func skipValue(src []byte, base int, dec *json.Decoder, first json.Token) (int, error) {
	depth := 0
	tok := first
	for {
		if delim, ok := tok.(json.Delim); ok {
			if delim == '{' || delim == '[' {
				depth++
			} else {
				depth--
			}
		}
		if depth == 0 {
			return base + int(dec.InputOffset()), nil
		}
		var err error
		if _, tok, err = nextToken(src, base, dec); err != nil {
			return 0, err
		}
	}
}

// nextToken reads the next token and reports where it starts in src. The decoder
// only exposes the offset it has read up to, so the token's own start is found
// by stepping over the separators and whitespace that precede it.
func nextToken(src []byte, base int, dec *json.Decoder) (int, json.Token, error) {
	prev := base + int(dec.InputOffset())
	tok, err := dec.Token()
	if err != nil {
		if err == io.EOF {
			return 0, nil, fmt.Errorf("package.json ended unexpectedly")
		}
		return 0, nil, err
	}
	end := base + int(dec.InputOffset())

	start := prev
	for start < end && (isSpace(src[start]) || src[start] == '\n' || src[start] == ',' || src[start] == ':') {
		start++
	}
	return start, tok, nil
}
