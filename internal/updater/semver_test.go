package updater

import "testing"

func TestSemanticVersionPrecedence(t *testing.T) {
	ordered := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
		"1.0.1",
		"1.1.0",
		"2.0.0",
		"1000000000000000000000000000000.0.0",
	}
	for index := 0; index < len(ordered)-1; index++ {
		left, err := parseSemanticVersion(ordered[index])
		if err != nil {
			t.Fatalf("parseSemanticVersion(%q) error = %v", ordered[index], err)
		}
		right, err := parseSemanticVersion(ordered[index+1])
		if err != nil {
			t.Fatalf("parseSemanticVersion(%q) error = %v", ordered[index+1], err)
		}
		if got := compareSemanticVersions(left, right); got >= 0 {
			t.Fatalf("compareSemanticVersions(%q, %q) = %d, want < 0", ordered[index], ordered[index+1], got)
		}
	}

	withBuild, err := parseSemanticVersion("1.2.3+build.9")
	if err != nil {
		t.Fatalf("parse version with build metadata: %v", err)
	}
	withoutBuild, err := parseSemanticVersion("1.2.3")
	if err != nil {
		t.Fatalf("parse version without build metadata: %v", err)
	}
	if got := compareSemanticVersions(withBuild, withoutBuild); got != 0 {
		t.Fatalf("build metadata changed precedence: got %d", got)
	}
}

func TestSemanticVersionRejectsNonStrictInput(t *testing.T) {
	invalid := []string{
		"", "v1.2.3", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03",
		"1.2.3-", "1.2.3-alpha..1", "1.2.3-01", "1.2.3+", "1.2.3+meta_1",
		" 1.2.3", "1.2.3 ",
	}
	for _, value := range invalid {
		t.Run(value, func(t *testing.T) {
			if _, err := parseSemanticVersion(value); err == nil {
				t.Fatalf("parseSemanticVersion(%q) error = nil", value)
			}
		})
	}
}
