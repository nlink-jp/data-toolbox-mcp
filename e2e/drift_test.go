package e2e

// Untagged, like config_test.go: the comparison TestE2E_v020_ManifestDrift
// makes is tested here in `make test`, so a check that cannot fail is caught
// without podman.

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// declaredPackage is one package as describe_runtime advertises it.
type declaredPackage struct {
	Name              string `json:"name"`
	VersionConstraint string `json:"version_constraint"`
}

var pipNameRuns = regexp.MustCompile(`[-_.]+`)

// pipName folds a distribution name the way pip compares them (PEP 503):
// "Pillow" and "pillow", "polars_runtime" and "polars-runtime" are one name.
func pipName(s string) string {
	return pipNameRuns.ReplaceAllString(strings.ToLower(s), "-")
}

// manifestDrift lists every way the declared packages disagree with the
// installed ones (pip names folded by pipName → version). The image installs
// from a hash lock, so a declaration that is not an exact == version is
// itself a disagreement: it cannot say what the image holds.
func manifestDrift(declared []declaredPackage, installed map[string]string) []string {
	var out []string
	for _, p := range declared {
		want, ok := strings.CutPrefix(p.VersionConstraint, "==")
		if !ok || want == "" {
			out = append(out, fmt.Sprintf("manifest declares %s %q, not an exact == version", p.Name, p.VersionConstraint))
			continue
		}
		got, ok := installed[pipName(p.Name)]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("manifest declares %s==%s but it is not installed", p.Name, want))
		case got != want:
			out = append(out, fmt.Sprintf("manifest declares %s==%s but %s is installed", p.Name, want, got))
		}
	}
	sort.Strings(out)
	return out
}

func TestManifestDrift(t *testing.T) {
	installed := map[string]string{"duckdb": "1.5.6", "pillow": "13.0.0", "polars-runtime-32": "1.44.2"}
	cases := []struct {
		name     string
		declared []declaredPackage
		want     []string
	}{
		{"agrees, names folded", []declaredPackage{
			{"duckdb", "==1.5.6"}, {"Pillow", "==13.0.0"}, {"polars_runtime.32", "==1.44.2"},
		}, nil},
		{"older image", []declaredPackage{{"duckdb", "==1.5.7"}},
			[]string{"manifest declares duckdb==1.5.7 but 1.5.6 is installed"}},
		{"not installed", []declaredPackage{{"numpy", "==2.5.4"}},
			[]string{"manifest declares numpy==2.5.4 but it is not installed"}},
		{"range, not a pin", []declaredPackage{{"duckdb", "~=1.5"}},
			[]string{`manifest declares duckdb "~=1.5", not an exact == version`}},
		{"empty pin", []declaredPackage{{"duckdb", "=="}},
			[]string{`manifest declares duckdb "==", not an exact == version`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := manifestDrift(c.declared, installed); !reflect.DeepEqual(got, c.want) {
				t.Errorf("manifestDrift = %q, want %q", got, c.want)
			}
		})
	}
}
