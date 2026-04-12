package registry

import (
	"cmp"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// semVersion represents a parsed semantic version.
type semVersion struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string
}

func (v semVersion) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Prerelease != "" {
		s += "-" + v.Prerelease
	}
	return s
}

// parseSemver parses a version string like "4.17.21" or "4.17.21-beta.1".
// Partial versions are accepted: "4" → 4.0.0, "4.17" → 4.17.0.
func parseSemver(s string) (semVersion, error) {
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimSpace(s)
	if s == "" {
		return semVersion{}, fmt.Errorf("empty version string")
	}

	var v semVersion
	// Split off prerelease.
	if idx := strings.IndexByte(s, '-'); idx >= 0 {
		v.Prerelease = s[idx+1:]
		s = s[:idx]
	}

	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return v, fmt.Errorf("invalid version: too many parts")
	}

	nums := [3]int{}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, fmt.Errorf("invalid version component %q", p)
		}
		nums[i] = n
	}
	v.Major = nums[0]
	v.Minor = nums[1]
	v.Patch = nums[2]
	return v, nil
}

// compareSemver returns -1, 0, or 1.
func compareSemver(a, b semVersion) int {
	if c := cmp.Compare(a.Major, b.Major); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Minor, b.Minor); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Patch, b.Patch); c != 0 {
		return c
	}
	// No prerelease > prerelease (1.0.0 > 1.0.0-beta).
	if a.Prerelease == "" && b.Prerelease != "" {
		return 1
	}
	if a.Prerelease != "" && b.Prerelease == "" {
		return -1
	}
	return strings.Compare(a.Prerelease, b.Prerelease)
}

// matchSemverRange checks if version matches a range string.
// Supported: "*", "4.17.21" (exact), "^4.17.21", "~4.17.21",
// ">=4.0.0", "4" (= ^4.0.0), "4.17" (= ~4.17.0), "4.x", "4.x.x",
// "^3.25 || ^4.0" (OR ranges).
func matchSemverRange(version semVersion, rangeStr string) bool {
	rangeStr = strings.TrimSpace(rangeStr)

	// Handle OR ranges: "^3.25 || ^4.0"
	if strings.Contains(rangeStr, "||") {
		for _, part := range strings.Split(rangeStr, "||") {
			if matchSemverRange(version, strings.TrimSpace(part)) {
				return true
			}
		}
		return false
	}

	// Handle AND ranges (space-separated): ">= 2.1.2 < 3.0.0"
	// Split on spaces, group tokens into comparators, check all match.
	if parts := splitComparators(rangeStr); len(parts) > 1 {
		for _, part := range parts {
			if !matchSemverRange(version, part) {
				return false
			}
		}
		return true
	}

	if rangeStr == "" || rangeStr == "*" || rangeStr == "latest" {
		return version.Prerelease == "" // skip prereleases for wildcard
	}

	// Skip prerelease versions unless the range explicitly targets one.
	if version.Prerelease != "" && !strings.Contains(rangeStr, "-") {
		return false
	}

	// x-range: "4.x", "4.x.x", "4.*"
	rangeStr = strings.ReplaceAll(rangeStr, ".x", "")
	rangeStr = strings.ReplaceAll(rangeStr, ".*", "")

	if strings.HasPrefix(rangeStr, ">=") {
		min, err := parseSemver(strings.TrimSpace(rangeStr[2:]))
		if err != nil {
			return false
		}
		return compareSemver(version, min) >= 0
	}

	if strings.HasPrefix(rangeStr, "<=") {
		max, err := parseSemver(strings.TrimSpace(rangeStr[2:]))
		if err != nil {
			return false
		}
		return compareSemver(version, max) <= 0
	}

	if strings.HasPrefix(rangeStr, ">") {
		min, err := parseSemver(strings.TrimSpace(rangeStr[1:]))
		if err != nil {
			return false
		}
		return compareSemver(version, min) > 0
	}

	if strings.HasPrefix(rangeStr, "<") {
		max, err := parseSemver(strings.TrimSpace(rangeStr[1:]))
		if err != nil {
			return false
		}
		return compareSemver(version, max) < 0
	}

	if strings.HasPrefix(rangeStr, "^") {
		base, err := parseSemver(rangeStr[1:])
		if err != nil {
			return false
		}
		// ^major.minor.patch: >=base <nextMajor (for major>0)
		// ^0.minor.patch: >=base <0.nextMinor
		if compareSemver(version, base) < 0 {
			return false
		}
		if base.Major > 0 {
			return version.Major == base.Major
		}
		return version.Major == 0 && version.Minor == base.Minor
	}

	if strings.HasPrefix(rangeStr, "~") {
		base, err := parseSemver(rangeStr[1:])
		if err != nil {
			return false
		}
		// ~major.minor.patch: >=base <major.nextMinor
		if compareSemver(version, base) < 0 {
			return false
		}
		return version.Major == base.Major && version.Minor == base.Minor
	}

	// Exact or partial: "4.17.21", "4.17", "4"
	base, err := parseSemver(rangeStr)
	if err != nil {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(rangeStr, "v"), ".")
	switch len(parts) {
	case 1:
		// "4" → ^4.0.0
		return matchSemverRange(version, "^"+rangeStr+".0.0")
	case 2:
		// "4.17" → ~4.17.0
		return matchSemverRange(version, "~"+rangeStr+".0")
	default:
		// Exact match.
		return compareSemver(version, base) == 0
	}
}

// splitComparators splits a range string like ">= 2.1.2 < 3.0.0" into
// individual comparators: [">=2.1.2", "<3.0.0"]. Returns a single-element
// slice for non-compound ranges.
func splitComparators(s string) []string {
	s = strings.TrimSpace(s)
	var parts []string
	for len(s) > 0 {
		// Find the start of a comparator (operator or version number)
		s = strings.TrimSpace(s)
		if len(s) == 0 {
			break
		}
		// Determine if this token starts with an operator
		var end int
		if strings.HasPrefix(s, ">=") || strings.HasPrefix(s, "<=") {
			end = 2
		} else if s[0] == '>' || s[0] == '<' || s[0] == '=' {
			end = 1
		} else if s[0] == '^' || s[0] == '~' {
			end = 1
		}
		// Skip any space between operator and version
		for end < len(s) && s[end] == ' ' {
			end++
		}
		// Consume the version string (digits, dots, hyphens, alphanumeric)
		for end < len(s) && s[end] != ' ' {
			end++
		}
		parts = append(parts, strings.ReplaceAll(s[:end], " ", ""))
		s = s[end:]
	}
	return parts
}

// bestMatch finds the highest version matching the range from a list.
func bestMatch(versions []string, rangeStr string) (string, error) {
	var parsed []semVersion
	for _, vs := range versions {
		v, err := parseSemver(vs)
		if err != nil {
			continue
		}
		if matchSemverRange(v, rangeStr) {
			parsed = append(parsed, v)
		}
	}
	if len(parsed) == 0 {
		return "", fmt.Errorf("no version matches range %q", rangeStr)
	}
	sort.Slice(parsed, func(i, j int) bool {
		return compareSemver(parsed[i], parsed[j]) > 0
	})
	return parsed[0].String(), nil
}
