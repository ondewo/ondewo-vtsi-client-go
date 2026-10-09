// Copyright 2020-2026 ONDEWO GmbH
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// The GitHub release body is sliced out of RELEASE.md by the Makefile's CURRENT_RELEASE_NOTES: from
// the line naming `Release ONDEWO <PRODUCT> Go Client <version>` to the next `*****` line. A heading
// spelled any other way gives an empty slice, and `gh release create -n ""` then publishes a release
// without notes and without an error. Only the two constants below name the product.
package tests

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const (
	releaseHeading  = "Release ONDEWO VTSI Go Client"
	versionVariable = "ONDEWO_VTSI_VERSION"
)

var separatorLine = regexp.MustCompile(`^\*{5}`)

func readRepoFile(t *testing.T, name string) string {
	t.Helper()

	content, err := os.ReadFile("../" + name)
	if err != nil {
		t.Fatalf("reading %s failed: %v", name, err)
	}

	return string(content)
}

// releaseNotesSlice reproduces the Makefile's perl range /<heading> <version>/../^\*{5}/; ok is
// false when the heading is missing or no separator ends the section.
func releaseNotesSlice(lines []string, version string) (slice []string, ok bool) {
	for start, line := range lines {
		if !strings.Contains(line, releaseHeading+" "+version) {
			continue
		}
		for end := start + 1; end < len(lines); end++ {
			if separatorLine.MatchString(lines[end]) {
				return lines[start : end+1], true
			}
		}

		return nil, false
	}

	return nil, false
}

func TestTheMakefileSlicesTheHeadingThisTestChecks(t *testing.T) {
	t.Parallel()

	want := `perl -ne 'print if /` + releaseHeading + ` ${` + versionVariable + `}/../^\*{5}/'`
	if !strings.Contains(readRepoFile(t, "Makefile"), want) {
		t.Errorf("the Makefile's CURRENT_RELEASE_NOTES no longer runs %s", want)
	}
}

func TestEveryReleaseHeadingUsesTheSpellingTheMakefileSlices(t *testing.T) {
	t.Parallel()

	heading := regexp.MustCompile(`^## ` + regexp.QuoteMeta(releaseHeading) + ` \d+\.\d+\.\d+$`)
	headings := 0
	for _, line := range strings.Split(readRepoFile(t, "RELEASE.md"), "\n") {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		headings++
		if !heading.MatchString(line) {
			t.Errorf("heading %q is not spelled %q", line, "## "+releaseHeading+" <version>")
		}
	}
	if headings == 0 {
		t.Error("RELEASE.md has no release heading")
	}
}

func TestEveryReleaseSectionEndsAtItsSeparator(t *testing.T) {
	t.Parallel()

	lines := strings.Split(readRepoFile(t, "RELEASE.md"), "\n")
	prefix := "## " + releaseHeading + " "
	for _, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		version := strings.TrimPrefix(line, prefix)
		slice, ok := releaseNotesSlice(lines, version)
		if !ok {
			t.Errorf("the section of %s is not closed by a ***** separator", version)
			continue
		}
		for _, inner := range slice[1 : len(slice)-1] {
			if strings.HasPrefix(inner, "## ") {
				t.Errorf("the slice of %s runs into the next section %q", version, inner)
			}
		}
	}
}

func TestTheCurrentVersionHasNonEmptyReleaseNotes(t *testing.T) {
	t.Parallel()

	match := regexp.MustCompile(`(?m)^` + versionVariable + `=(\S+)$`).FindStringSubmatch(readRepoFile(t, "Makefile"))
	if match == nil {
		t.Fatalf("the Makefile does not set %s", versionVariable)
	}
	slice, ok := releaseNotesSlice(strings.Split(readRepoFile(t, "RELEASE.md"), "\n"), match[1])
	if !ok {
		t.Fatalf("RELEASE.md has no section for %s closed by a ***** separator", match[1])
	}
	body := 0
	for _, line := range slice[1 : len(slice)-1] {
		if strings.TrimSpace(line) != "" {
			body++
		}
	}
	if body < 2 {
		t.Errorf("the release notes of %s have %d non-blank lines, want at least 2", match[1], body)
	}
}
