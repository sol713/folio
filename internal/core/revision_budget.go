package core

import "unicode/utf8"

const revisionComparisonJSONLimit int64 = 64 * 1024 * 1024

// Count JSON string bytes without marshaling or allocating an escaped copy.
// Saturation permits an oversized legacy field to stop at the query budget.
func jsonStringBound(value string, limit int64) int64 {
	n := int64(2)
	for len(value) > 0 && n <= limit {
		b := value[0]
		cost := int64(1)
		width := 1
		if b < utf8.RuneSelf {
			switch b {
			case '"', '\\', '\b', '\f', '\n', '\r', '\t':
				cost = 2
			case '<', '>', '&':
				cost = 6
			default:
				if b < 0x20 {
					cost = 6
				}
			}
		} else {
			r, w := utf8.DecodeRuneInString(value)
			width = w
			cost = int64(w)
			if r == '\u2028' || r == '\u2029' || r == utf8.RuneError && w == 1 {
				cost = 6
			}
		}
		n += cost
		value = value[width:]
	}
	if len(value) > 0 || n > limit {
		return limit + 1
	}
	return n
}

func comparisonWithinBudget(from, to ProposalContent) bool {
	// Each source byte can appear in its complete snapshot, one changed-field
	// value, and one Markdown diff. Three copies bound all eight fields, including
	// unbounded legacy cover URLs. Reserve 64 KiB for keys, arrays, flags, version
	// metadata, limitations and the adapter envelope. This is conservative rather
	// than an exact serializer; smaller actual results may also be rejected.
	remaining := (revisionComparisonJSONLimit - 64*1024) / 3
	for _, p := range []ProposalContent{from, to} {
		remaining -= 256 // Eight keys, booleans, punctuation and the bounded tag array.
		values := []string{p.Title, p.Slug, p.Markdown, p.Excerpt, p.Category, p.Cover}
		for _, v := range values {
			remaining -= jsonStringBound(v, remaining)
			if remaining < 0 {
				return false
			}
		}
		for _, v := range p.Tags {
			remaining -= jsonStringBound(v, remaining)
			if remaining < 0 {
				return false
			}
		}
	}
	return remaining >= 0
}
