package updater

import (
	"fmt"
	"regexp"
	"strings"
)

var semanticVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

type semanticVersion struct {
	major      string
	minor      string
	patch      string
	prerelease []string
}

func parseSemanticVersion(value string) (semanticVersion, error) {
	matches := semanticVersionPattern.FindStringSubmatch(value)
	if matches == nil {
		return semanticVersion{}, fmt.Errorf("%q is not a strict semantic version", value)
	}

	version := semanticVersion{
		major: matches[1],
		minor: matches[2],
		patch: matches[3],
	}
	if matches[4] != "" {
		version.prerelease = strings.Split(matches[4], ".")
		for _, identifier := range version.prerelease {
			if isNumeric(identifier) && len(identifier) > 1 && identifier[0] == '0' {
				return semanticVersion{}, fmt.Errorf("%q is not a strict semantic version: numeric prerelease identifiers cannot have leading zeroes", value)
			}
		}
	}
	return version, nil
}

func compareSemanticVersions(left, right semanticVersion) int {
	for _, pair := range [][2]string{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if comparison := compareNumericStrings(pair[0], pair[1]); comparison != 0 {
			return comparison
		}
	}

	if len(left.prerelease) == 0 && len(right.prerelease) == 0 {
		return 0
	}
	if len(left.prerelease) == 0 {
		return 1
	}
	if len(right.prerelease) == 0 {
		return -1
	}

	limit := len(left.prerelease)
	if len(right.prerelease) < limit {
		limit = len(right.prerelease)
	}
	for index := 0; index < limit; index++ {
		leftIdentifier := left.prerelease[index]
		rightIdentifier := right.prerelease[index]
		leftNumeric := isNumeric(leftIdentifier)
		rightNumeric := isNumeric(rightIdentifier)
		switch {
		case leftNumeric && rightNumeric:
			if comparison := compareNumericStrings(leftIdentifier, rightIdentifier); comparison != 0 {
				return comparison
			}
		case leftNumeric:
			return -1
		case rightNumeric:
			return 1
		default:
			if leftIdentifier < rightIdentifier {
				return -1
			}
			if leftIdentifier > rightIdentifier {
				return 1
			}
		}
	}
	if len(left.prerelease) < len(right.prerelease) {
		return -1
	}
	if len(left.prerelease) > len(right.prerelease) {
		return 1
	}
	return 0
}

func compareNumericStrings(left, right string) int {
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func isNumeric(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
