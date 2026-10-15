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

// The release is a sequence whose ORDER is the safety property: a pushed tag and a version the go
// module proxy has served are immutable, so everything that can fail has to fail before them, and a
// GitHub release may only exist for a module the proxy really serves. These tests parse the
// `release:` recipe and pin that order, and they prove that the devops-accounts clone is deleted
// before the release starts - statically and by running `make ondewo_release` against a stub.
package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// releaseSteps is the contract order of the release recipe. Each entry is a make target run by
// `$(MAKE) <target>` (optionally behind a prefix such as $(WITHOUT_CREDENTIALS)) or, for the git
// steps, the exact command line.
var releaseSteps = []string{
	"release_preflight",
	"build",
	"test_coverage",
	"git add ${STUBS_DIR}",
	"git diff --cached --quiet || git commit --no-verify -m \"PREPARING FOR RELEASE ${ONDEWO_VTSI_VERSION}\"",
	"publish_dry_run",
	"git push",
	"create_release_branch",
	"create_release_tag",
	"publish_go_module",
	"verify_go_module_served",
	"push_to_gh",
	"release_verify",
}

// targetRecipe returns the trimmed shell lines of a target's recipe; make comments at column 0 and
// make conditionals inside the recipe are skipped, the recipe ends at the first other line.
func targetRecipe(makefile, target string) []string {
	_, after, found := strings.Cut(makefile, "\n"+target+":")
	if !found {
		return nil
	}

	var recipe []string
	for _, line := range strings.Split(after, "\n")[1:] {
		if strings.HasPrefix(line, "#") || makeConditional.MatchString(line) {
			continue
		}
		if !strings.HasPrefix(line, "\t") {
			break
		}
		recipe = append(recipe, strings.TrimSpace(line))
	}

	return recipe
}

// targetPrerequisites returns the prerequisites named on a target's rule line.
func targetPrerequisites(makefile, target string) []string {
	_, after, found := strings.Cut(makefile, "\n"+target+":")
	if !found {
		return nil
	}
	rule, _, _ := strings.Cut(after, "\n")
	rule, _, _ = strings.Cut(rule, "##")

	return strings.Fields(rule)
}

// runsMakeTarget reports whether a recipe line runs exactly `make <target>` / `$(MAKE) <target>`.
func runsMakeTarget(line, target string) bool {
	fields := strings.Fields(strings.TrimPrefix(line, "-"))
	for i := 0; i+1 < len(fields); i++ {
		if (fields[i] == "$(MAKE)" || fields[i] == "make") && fields[i+1] == target {
			return true
		}
	}

	return false
}

func stepIndex(recipe []string, step string) int {
	for i, line := range recipe {
		if strings.Contains(step, " ") {
			if line == step {
				return i
			}
		} else if runsMakeTarget(line, step) {
			return i
		}
	}

	return -1
}

// releaseOrderViolations lists every way the recipe departs from releaseSteps.
func releaseOrderViolations(recipe []string) []string {
	var violations []string
	previous, previousStep := -1, ""
	for _, step := range releaseSteps {
		index := stepIndex(recipe, step)
		if index < 0 {
			violations = append(violations, "missing step: "+step)
			continue
		}
		if index <= previous {
			violations = append(violations, step+" runs before "+previousStep)
		}
		previous, previousStep = index, step
	}
	for i, line := range recipe {
		if strings.HasPrefix(line, "git ") || strings.HasPrefix(line, "$(MAKE)") || strings.HasPrefix(line, "make ") ||
			strings.HasPrefix(line, "$(WITHOUT_CREDENTIALS)") || strings.HasPrefix(line, "-") {
			if i < stepIndex(recipe, "release_preflight") {
				violations = append(violations, "runs before release_preflight: "+line)
			}
		}
		if strings.HasPrefix(line, "-") && strings.Contains(line, "git ") {
			violations = append(violations, "a failing git step is ignored: "+line)
		}
	}

	return violations
}

func TestTheReleaseRecipeRunsItsStepsInTheContractOrder(t *testing.T) {
	t.Parallel()

	recipe := targetRecipe(readRepoFile(t, "Makefile"), "release")
	if len(recipe) == 0 {
		t.Fatal("the Makefile has no release recipe")
	}
	if violations := releaseOrderViolations(recipe); len(violations) != 0 {
		t.Errorf("the release recipe breaks the contract order:\n%s", strings.Join(violations, "\n"))
	}
}

// The recipe this repository shipped 9.1.0 with, reduced to its steps: the GitHub release before
// the module proxy, a commit whose failure was ignored, the rehearsal after the push. The order
// check must reject it, or the test above proves nothing.
func TestTheOrderCheckRejectsTheOldRecipe(t *testing.T) {
	t.Parallel()

	old := []string{
		"make check_gh_credentials", "make check_release_notes", "make build", "make test_coverage",
		"git add ${STUBS_DIR}", "-git commit --no-verify -m \"PREPARING FOR RELEASE ${ONDEWO_VTSI_VERSION}\"",
		"git push", "make create_release_branch", "make publish_dry_run", "make create_release_tag",
		"make push_to_gh", "make publish_go_module",
	}
	violations := strings.Join(releaseOrderViolations(old), "\n")
	for _, want := range []string{
		"missing step: release_preflight",
		"a failing git step is ignored",
		"git push runs before publish_dry_run",
		"push_to_gh runs before publish_go_module",
		"missing step: release_verify",
	} {
		if !strings.Contains(violations, want) {
			t.Errorf("the order check misses %q on the old recipe; it reported:\n%s", want, violations)
		}
	}
}

func TestThePreflightChecksEverythingAndChangesNothing(t *testing.T) {
	t.Parallel()

	makefile := readRepoFile(t, "Makefile")
	prerequisites := targetPrerequisites(makefile, "release_preflight")
	for _, check := range []string{
		"check_release_host_tools", "check_release_credentials", "check_gh_token_valid",
		"check_version_unreleased", "check_release_notes",
	} {
		found := false
		for _, prerequisite := range prerequisites {
			found = found || prerequisite == check
		}
		if !found {
			t.Errorf("release_preflight does not run %s (prerequisites: %v)", check, prerequisites)
		}
		for _, line := range targetRecipe(makefile, check) {
			for _, write := range []string{"git push", "git tag", "git commit", "release create", "git checkout"} {
				if strings.Contains(line, write) {
					t.Errorf("preflight step %s is not read-only: %s", check, line)
				}
			}
		}
	}
	if !strings.Contains(strings.Join(targetRecipe(makefile, "check_gh_token_valid"), "\n"), ".permissions.push") {
		t.Error("check_gh_token_valid does not ask GitHub for the token's push permission")
	}
	if !strings.Contains(strings.Join(targetRecipe(makefile, "check_version_unreleased"), "\n"), "/@v/list") {
		t.Error("check_version_unreleased does not ask proxy.golang.org for its version list")
	}
}

// No step authenticates gh on the host: the token reaches gh as GH_TOKEN of that one command.
func TestNoRecipePersistsAGitHubLogin(t *testing.T) {
	t.Parallel()

	for _, line := range recipeLines(readRepoFile(t, "Makefile")) {
		if strings.Contains(line, "gh auth login") {
			t.Errorf("a recipe logs gh in on the host (the token would persist in hosts.yml): %s", strings.TrimSpace(line))
		}
	}
}

func TestTheDevopsCloneIsDeletedBeforeTheReleaseStarts(t *testing.T) {
	t.Parallel()

	recipe := strings.Join(targetRecipe(readRepoFile(t, "Makefile"), "run_release_with_devops"), "\n")
	release := strings.LastIndex(recipe, "$(MAKE) release")
	removal := strings.Index(recipe, `rm -rf "$$clone"; clone=""`)
	switch {
	case release < 0:
		t.Fatal("run_release_with_devops does not run $(MAKE) release")
	case removal < 0 || removal > release:
		t.Error("the devops-accounts clone is not deleted before $(MAKE) release")
	}
	for _, want := range []string{"trap '", "`mktemp -d`", `"$$DEVOPS_ACCOUNT_DIR"`} {
		if !strings.Contains(recipe, want) {
			t.Errorf("run_release_with_devops lacks %q", want)
		}
	}
}

// The behavioural half: `make ondewo_release` with `release` replaced by a stub that checks, while
// it runs, that the token was loaded and that the clone is already gone - and then fails, so the
// trap path is exercised as well.
func TestTheDevopsCloneIsGoneWhileTheReleaseRuns(t *testing.T) {
	for _, tool := range []string{"make", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	makefile := readRepoFile(t, "Makefile")
	stub := "\nrelease:\n" +
		"\t@[ \"$$GITHUB_GH_TOKEN\" = \"stub-token-value\" ] && echo TOKEN_LOADED\n" +
		"\t@[ -z \"$$(ls -A \"$$TMPDIR\")\" ] && echo CLONE_ALREADY_GONE\n" +
		"\t@exit 1\n"

	git := func(t *testing.T, dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	setup := func(t *testing.T) (repo, accounts, scratch string) {
		root := t.TempDir()
		repo, accounts, scratch = filepath.Join(root, "repo"), filepath.Join(root, "accounts"), filepath.Join(root, "tmp")
		for _, dir := range []string{repo, accounts, scratch} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(repo, "Makefile"), []byte(makefile+stub), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, repo, "init", "-q", "-b", "master")
		env := "# GITHUB_GH_TOKEN=not-this-one\nGITHUB_GH_TOKEN=stub-token-value\n"
		if err := os.WriteFile(filepath.Join(accounts, "account_github.env"), []byte(env), 0o600); err != nil {
			t.Fatal(err)
		}
		git(t, accounts, "init", "-q", "-b", "master")
		git(t, accounts, "add", ".")
		git(t, accounts, "commit", "-q", "-m", "accounts")

		return repo, accounts, scratch
	}
	run := func(t *testing.T, repo, scratch string, extraEnv []string, args ...string) string {
		t.Helper()
		cmd := exec.Command("make", append([]string{"-C", repo, "ondewo_release"}, args...)...)
		var env []string
		for _, kv := range os.Environ() {
			name, _, _ := strings.Cut(kv, "=")
			switch name {
			case "GITHUB_GH_TOKEN", "GH_TOKEN", "GITHUB_TOKEN", "DEVOPS_ACCOUNT_DIR", "TMPDIR", "MAKEFLAGS", "MAKELEVEL":
				continue
			}
			env = append(env, kv)
		}
		cmd.Env = append(env, append([]string{"TMPDIR=" + scratch}, extraEnv...)...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("make ondewo_release succeeded although the stubbed release fails:\n%s", out)
		}

		return string(out)
	}

	t.Run("own clone", func(t *testing.T) {
		repo, accounts, scratch := setup(t)
		out := run(t, repo, scratch, nil, "DEVOPS_ACCOUNT_GIT_URL="+accounts)
		for _, want := range []string{"TOKEN_LOADED", "CLONE_ALREADY_GONE"} {
			if !strings.Contains(out, want) {
				t.Errorf("the stubbed release did not report %s:\n%s", want, out)
			}
		}
		if strings.Contains(out, "stub-token-value") {
			t.Error("the token value reached the output")
		}
		if entries, _ := os.ReadDir(scratch); len(entries) != 0 {
			t.Errorf("the failed release left %d entries in TMPDIR (the clone)", len(entries))
		}
	})

	t.Run("driver clone", func(t *testing.T) {
		repo, accounts, scratch := setup(t)
		out := run(t, repo, scratch, []string{"DEVOPS_ACCOUNT_DIR=" + accounts}, "DEVOPS_ACCOUNT_GIT_URL=/nonexistent/clone/must/not/happen")
		for _, want := range []string{"TOKEN_LOADED", "CLONE_ALREADY_GONE"} {
			if !strings.Contains(out, want) {
				t.Errorf("the stubbed release did not report %s:\n%s", want, out)
			}
		}
		if _, err := os.Stat(filepath.Join(accounts, "account_github.env")); err != nil {
			t.Errorf("the driver's DEVOPS_ACCOUNT_DIR was touched: %v", err)
		}
	})
}
