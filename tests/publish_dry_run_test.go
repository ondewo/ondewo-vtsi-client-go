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

// `make publish_dry_run` resolves its own zip of the release version through a file:// GOPROXY. Go
// caches a module version once and for all, and the rehearsed zip does not hash like the one
// proxy.golang.org serves, so a rehearsal that ran on the caller's module cache made every later
// real `go get <module>@<release>` on that machine fail with a checksum SECURITY ERROR. The
// rehearsal must therefore run every go command on a throwaway module cache and be able to delete it.
package tests

import (
	"regexp"
	"strings"
	"testing"
)

var (
	goCommand          = regexp.MustCompile(`(?:^|[\s;&|(` + "`" + `])go\s+(?:build|clean|env|get|install|list|mod|run|test|vet|work)\b`)
	privateModCache    = regexp.MustCompile(`^export GOMODCACHE="\$\$work/[^"$]+"; \\$`)
	writableBeforeRm   = regexp.MustCompile(`trap '[^']*chmod -R u\+w "\$\$work"[^']*rm -rf "\$\$work"`)
	modCacheRWGoFlags  = regexp.MustCompile(`(?m)^export GOFLAGS="[^"]*-modcacherw[^"]*"; \\$`)
	modCacheAssignment = regexp.MustCompile(`\bGOMODCACHE=`)
)

// publishDryRunRecipe returns the trimmed recipe lines of the publish_dry_run target.
func publishDryRunRecipe(makefile string) []string {
	_, after, found := strings.Cut(makefile, "\npublish_dry_run:")
	if !found {
		return nil
	}

	var recipe []string
	for _, line := range strings.Split(after, "\n")[1:] {
		if !strings.HasPrefix(line, "\t") {
			break
		}
		recipe = append(recipe, strings.TrimSpace(line))
	}

	return recipe
}

// dryRunModCacheLeaks lists every way the publish_dry_run recipe can write into, or leave behind, a
// module cache other than its own throwaway one.
func dryRunModCacheLeaks(makefile string) []string {
	recipe := publishDryRunRecipe(makefile)
	if len(recipe) == 0 {
		return []string{"no publish_dry_run target"}
	}

	var leaks []string
	privateFrom, firstGo := -1, -1
	for i, line := range recipe {
		if privateFrom < 0 && privateModCache.MatchString(line) {
			privateFrom = i
		} else if modCacheAssignment.MatchString(line) {
			leaks = append(leaks, "GOMODCACHE is set to something other than the work dir: "+line)
		}
		if firstGo < 0 && goCommand.MatchString(line) {
			firstGo = i
		}
	}
	switch {
	case privateFrom < 0:
		leaks = append(leaks, `no export GOMODCACHE="$$work/..." - the rehearsal writes into the caller's module cache`)
	case firstGo >= 0 && firstGo < privateFrom:
		leaks = append(leaks, "a go command runs before GOMODCACHE points into the work dir: "+recipe[firstGo])
	}

	joined := strings.Join(recipe, "\n")
	if !writableBeforeRm.MatchString(joined) {
		leaks = append(leaks, `the cleanup trap does not chmod -R u+w "$$work" before rm -rf - go writes its cache read-only`)
	}
	if !modCacheRWGoFlags.MatchString(joined) {
		leaks = append(leaks, "GOFLAGS does not carry -modcacherw")
	}

	return leaks
}

func TestThePublishDryRunNeverTouchesTheCallersModuleCache(t *testing.T) {
	if leaks := dryRunModCacheLeaks(readRepoFile(t, "Makefile")); len(leaks) != 0 {
		t.Errorf("publish_dry_run can poison the caller's module cache:\n%s", strings.Join(leaks, "\n"))
	}
}

// The recipe that poisoned the cache must be caught, or the test above proves nothing.
func TestTheModCacheDetectorCatchesTheOldRecipe(t *testing.T) {
	const prefix = "\npublish_dry_run: check_stubs\n\t@set -e; \\\n\twork=`mktemp -d`; \\\n"
	const rest = "\tfirst_package=`go list ./api/... | head -1`; \\\n\tgo mod tidy; \\\n\tgo build ./...\n\npublish_go_module:\n"
	const goodTrap = "\ttrap 'chmod -R u+w \"$$work\" 2>/dev/null; rm -rf \"$$work\"' EXIT INT TERM; \\\n"
	const goodCache = "\texport GOMODCACHE=\"$$work/modcache\"; \\\n"
	const goodFlags = "\texport GOFLAGS=\"-mod=mod -modcacherw\"; \\\n"

	good := prefix + goodTrap + goodCache + goodFlags + rest
	if leaks := dryRunModCacheLeaks(good); len(leaks) != 0 {
		t.Errorf("false positive on the fixed recipe: %q", leaks)
	}

	for name, recipe := range map[string]string{
		"old recipe":           prefix + "\ttrap 'rm -rf \"$$work\"' EXIT INT TERM; \\\n\texport GOFLAGS=-mod=mod; \\\n" + rest,
		"cache after go list":  prefix + goodTrap + goodFlags + rest[:strings.Index(rest, "\tgo mod tidy")] + goodCache + rest[strings.Index(rest, "\tgo mod tidy"):],
		"cache outside work":   prefix + goodTrap + "\texport GOMODCACHE=\"$$HOME/go/pkg/mod\"; \\\n" + goodFlags + rest,
		"read-only cache left": prefix + "\ttrap 'rm -rf \"$$work\"' EXIT INT TERM; \\\n" + goodCache + "\texport GOFLAGS=-mod=mod; \\\n" + rest,
		"no target":            "\nbuild:\n\tgo build ./...\n",
	} {
		if len(dryRunModCacheLeaks(recipe)) == 0 {
			t.Errorf("not detected: %s", name)
		}
	}
}
