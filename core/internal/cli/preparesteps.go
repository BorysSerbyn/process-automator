package cli

import (
	"regexp"
	"strings"
)

// stepLinkRE matches a link in a plugin's Prepare steps: a Markdown link,
// [text](https://…), or a bare web address.
var stepLinkRE = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)|https?://[^\s)]+`)

// plainSteps is a plugin's Prepare steps for the terminal: each Markdown link
// as its text and its address.
func plainSteps(steps string) string {
	return stepLinkRE.ReplaceAllStringFunc(steps, func(m string) string {
		if s := stepLinkRE.FindStringSubmatch(m); s[1] != "" {
			return s[1] + " (" + s[2] + ")"
		}
		return m
	})
}

// stepPart is a run of a plugin's Prepare steps: text, or a link (URL set).
type stepPart struct{ Text, URL string }

// stepParts splits a plugin's Prepare steps into text and links, for the
// secret page.
func stepParts(steps string) []stepPart {
	var parts []stepPart
	text := func(s string) {
		if s != "" {
			parts = append(parts, stepPart{Text: s})
		}
	}
	at := 0
	for _, m := range stepLinkRE.FindAllStringSubmatchIndex(steps, -1) {
		text(steps[at:m[0]])
		at = m[1]
		if m[2] >= 0 { // [text](url)
			parts = append(parts, stepPart{Text: steps[m[2]:m[3]], URL: steps[m[4]:m[5]]})
			continue
		}
		// A bare address keeps the punctuation that ends its sentence out.
		addr := strings.TrimRight(steps[m[0]:m[1]], ".,;:!?")
		at = m[0] + len(addr)
		parts = append(parts, stepPart{Text: addr, URL: addr})
	}
	text(steps[at:])
	return parts
}
