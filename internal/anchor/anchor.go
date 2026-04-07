package anchor

import (
	"regexp"
)

var reWikiAnchor = regexp.MustCompile(`<!--\s*wiki:anchor:([a-zA-Z0-9_-]+)\s*-->`)

// ExtractIDs returns all wiki anchor ids in order of appearance.
func ExtractIDs(md string) []string {
	m := reWikiAnchor.FindAllStringSubmatch(md, -1)
	out := make([]string, 0, len(m))
	for _, x := range m {
		if len(x) > 1 {
			out = append(out, x[1])
		}
	}
	return out
}

// StripForPreview removes wiki anchor HTML comments for cleaner reading (optional).
func StripForPreview(md string) string {
	return reWikiAnchor.ReplaceAllString(md, "")
}
