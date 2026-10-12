package matchmaker

import (
	"strconv"
	"strings"
)

// Query subset, modeled on query strings
// (docs: concepts/multiplayer/matchmaker):
//
//	"*"                                        — match anyone
//	"properties.region:europe"                 — SHOULD (boost only, no filter)
//	"+properties.region:europe"                — MUST equal (string)
//	"+properties.rank:>=5 +properties.rank:<=10" — numeric ranges
//
// Only MUST ("+") terms filter; SHOULD terms are ignored (they affect
// scoring in Bleve, which we don't implement)... matching is
// MUTUAL: both tickets' queries must accept each other's properties.

// matchesQuery reports whether query accepts the ticket's properties.
func matchesQuery(query string, t *Ticket) bool {
	q := strings.TrimSpace(query)
	if q == "" || q == "*" {
		return true
	}
	for _, term := range strings.Fields(q) {
		required := strings.HasPrefix(term, "+")
		term = strings.TrimPrefix(term, "+")
		if !required {
			continue // SHOULD term: scoring only, not a filter
		}
		if !matchTerm(term, t) {
			return false
		}
	}
	return true
}

// matchTerm evaluates one required term "properties.key:value".
func matchTerm(term string, t *Ticket) bool {
	rest, ok := strings.CutPrefix(term, "properties.")
	if !ok {
		return false
	}
	key, rawVal, ok := strings.Cut(rest, ":")
	if !ok || key == "" {
		return false
	}

	// Numeric operators: >=, <=, >, <, = (checked in this order).
	for _, op := range []string{">=", "<=", ">", "<", "="} {
		if v, found := strings.CutPrefix(rawVal, op); found {
			want, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return false
			}
			got, exists := t.NumericProps[key]
			if !exists {
				return false
			}
			switch op {
			case ">=":
				return got >= want
			case "<=":
				return got <= want
			case ">":
				return got > want
			case "<":
				return got < want
			case "=":
				return got == want
			}
		}
	}

	// Plain equality: string property, or numeric equality shorthand.
	if got, exists := t.StringProps[key]; exists {
		return got == rawVal
	}
	if got, exists := t.NumericProps[key]; exists {
		want, err := strconv.ParseFloat(rawVal, 64)
		return err == nil && got == want
	}
	return false // property missing
}

// compatible reports whether two tickets mutually accept each other.
func compatible(a, b *Ticket) bool {
	return matchesQuery(a.Query, b) && matchesQuery(b.Query, a)
}
