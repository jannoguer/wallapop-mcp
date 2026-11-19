package wallapop

import "strings"

// refKey extracts the id or slug from ref, which may be a Wallapop link such
// as https://es.wallapop.com/<kind>/<slug>?x=y.
func refKey(ref, kind string) string {
	ref = strings.TrimSpace(ref)
	if _, after, ok := strings.Cut(ref, "/"+kind+"/"); ok {
		ref = after
	}
	if i := strings.IndexAny(ref, "/?#"); i >= 0 {
		ref = ref[:i]
	}
	return ref
}

// isSlug tells slugs ("iphone-13-1311621547") from ids ("ejkml3e23yzx").
func isSlug(key string) bool { return strings.Contains(key, "-") }
