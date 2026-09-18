package settings

import (
	"strings"
	"testing"
)

// Every kind must have its own slug, and every slug must be a token rather than
// a sentence. The screen branches on these, and two kinds sharing one would
// render the same control for both.
func TestEveryKindHasItsOwnMachineReadableSlug(t *testing.T) {
	kinds := []Kind{KindString, KindInt, KindBool, KindTimeOfDay, KindTimezone, KindChoice}

	seen := map[string]Kind{}
	for _, k := range kinds {
		slug := k.Slug()
		if slug == "" {
			t.Errorf("%v has no slug", k)
			continue
		}
		if strings.ContainsAny(slug, " ") {
			t.Errorf("%v has slug %q, which is a phrase; a slug is a token", k, slug)
		}
		if other, clash := seen[slug]; clash {
			t.Errorf("%v and %v both slug to %q; the screen cannot tell them apart", k, other, slug)
		}
		seen[slug] = k
	}
}

// And every setting that ships must resolve to one of them, so a settings
// screen driven off the slug has a control for every row it will ever draw.
func TestEverySettingHasAKindTheScreenCanRender(t *testing.T) {
	renderable := map[string]bool{
		"string": true, "int": true, "bool": true,
		"time_of_day": true, "timezone": true, "choice": true,
	}
	for _, def := range All {
		if !renderable[def.Kind.Slug()] {
			t.Errorf("%s is a %q, which the settings screen has no control for",
				def.Key, def.Kind.Slug())
		}
		if def.Kind == KindChoice && len(def.Options) == 0 {
			t.Errorf("%s is a choice with no options; the screen would draw an empty list", def.Key)
		}
		if def.Doc == "" {
			t.Errorf("%s has no doc; the screen would show the key with nothing under it", def.Key)
		}
	}
}
