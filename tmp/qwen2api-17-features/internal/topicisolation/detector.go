// Package topicisolation detects when a user switches tasks within the same
// conversation. When entities (URLs, file paths, identifiers) in the latest
// user message diverge enough from the first user message, the caller can
// discard history to prevent stale context from confusing the model.
package topicisolation

import (
	"regexp"
	"strings"
	"unicode"
)

// Threshold is the Jaccard similarity below which a topic change is declared.
// A value of 0.1 means <10% entity overlap → new topic.
const DefaultThreshold = 0.1

var (
	urlRe      = regexp.MustCompile(`https?://[^\s<>"')\]]+`)
	unixPathRe = regexp.MustCompile(`(?:^|[\s"'(])(/[a-zA-Z0-9_./-]{3,})`)
	winPathRe  = regexp.MustCompile(`(?i)[A-Z]:\\[^\s<>"']+`)
	camelCaseRe = regexp.MustCompile(`\b[a-z]+[A-Z][a-zA-Z]*\b`)
	dottedFileRe = regexp.MustCompile(`\b[a-zA-Z0-9_-]+\.[a-zA-Z]{1,6}\b`)
)

// ExtractEntities pulls recognisable entities from text: URLs, file paths,
// camelCase identifiers and dotted filenames.
func ExtractEntities(text string) map[string]struct{} {
	ents := make(map[string]struct{})
	for _, m := range urlRe.FindAllString(text, -1) {
		ents[strings.ToLower(m)] = struct{}{}
	}
	for _, m := range unixPathRe.FindAllStringSubmatch(text, -1) {
		if len(m) > 1 {
			ents[m[1]] = struct{}{}
		}
	}
	for _, m := range winPathRe.FindAllString(text, -1) {
		ents[strings.ToLower(m)] = struct{}{}
	}
	for _, m := range camelCaseRe.FindAllString(text, -1) {
		ents[strings.ToLower(m)] = struct{}{}
	}
	for _, m := range dottedFileRe.FindAllString(text, -1) {
		lower := strings.ToLower(m)
		// skip common non-entities
		if lower == "e.g" || lower == "i.e" || lower == "etc." {
			continue
		}
		ents[lower] = struct{}{}
	}
	// Also extract quoted strings.
	ents = mergeQuotedStrings(ents, text)
	return ents
}

func mergeQuotedStrings(ents map[string]struct{}, text string) map[string]struct{} {
	inQuote := false
	var buf strings.Builder
	for _, r := range text {
		if r == '"' || r == '\'' || r == '`' || r == '\u201c' || r == '\u201d' {
			if inQuote {
				s := strings.TrimSpace(buf.String())
				if len(s) >= 2 && !allSpace(s) {
					ents[strings.ToLower(s)] = struct{}{}
				}
				buf.Reset()
			}
			inQuote = !inQuote
			continue
		}
		if inQuote {
			buf.WriteRune(r)
		}
	}
	return ents
}

func allSpace(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// Jaccard returns the Jaccard similarity of two sets: |A∩B| / |A∪B|.
func Jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}
	intersection := 0
	for k := range a {
		if _, ok := b[k]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 1.0
	}
	return float64(intersection) / float64(union)
}

// Detector checks whether the user has switched topics.
type Detector struct {
	Threshold float64
}

// NewDetector creates a Detector with the given similarity threshold.
func NewDetector(threshold float64) *Detector {
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	return &Detector{Threshold: threshold}
}

// Changed returns true when the latest user message appears to be a new topic
// compared to the first user message.
func (d *Detector) Changed(firstUserText, lastUserText string) bool {
	a := ExtractEntities(firstUserText)
	b := ExtractEntities(lastUserText)
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	return Jaccard(a, b) < d.Threshold
}
