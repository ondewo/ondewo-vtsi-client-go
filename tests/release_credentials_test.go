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

// Release credentials reach the release recipes and the release workflow through the environment
// only. /proc/<pid>/cmdline is world-readable, so a token on any process command line - docker's,
// make's, gh's or the `/bin/sh -c` make starts for a recipe line - is visible to every user on the
// release host for the life of that process. make expands $(NAME) and ${NAME} in a recipe line
// BEFORE the shell runs, so a recipe may read a secret only as $$NAME (expanded by the shell from the
// exported environment).
package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// secretName matches every variable name that holds a credential.
const secretName = `[A-Z0-9_]*(?:TOKEN|PASSWORD|PASSPHRASE|API_KEY|SECRET)[A-Z0-9_]*`

var (
	// $(NAME) or ${NAME}: expanded by make into the recipe text. The caller rejects a preceding `$`
	// ($$NAME / $${NAME} is the shell's expansion, which is fine).
	makeExpandedSecret = regexp.MustCompile(`\$[({]` + secretName + `[)}]`)
	// `$(if $(NAME),<set>,<unset>)` and `$(if $(filter-out PLACEHOLDER,$(NAME)),...)` only render a
	// fixed word, never the value.
	makeIfSecret       = regexp.MustCompile(`\$\(if (?:\$\(filter-out [^,$]*,)?\$[({]` + secretName + `[)}]\)?,`)
	dockerEnvWithValue = regexp.MustCompile(`(?:^|\s)(?:-e|--env)[\s=]+` + secretName + `=`)
	makeWithSecretArg  = regexp.MustCompile(`(?:\$\(MAKE\)|\bmake\b)[^\n]*\s` + secretName + `=`)
	secretFlagOnArgv   = regexp.MustCompile(`(?:--token|--with-token|--api-key|--password|-k|-p)[\s=]+["']?\$+[({]?` + secretName)
	secretInAuthHeader = regexp.MustCompile(`Authorization:[^\n]*\$+[({]?` + secretName)
	runKey             = regexp.MustCompile(`^(\s*)(?:-\s+)?run:(.*)$`)
)

// recipeLines returns the Makefile lines make hands to the shell (tab-indented recipe lines).
func recipeLines(makefile string) []string {
	var lines []string
	for _, line := range strings.Split(makefile, "\n") {
		if strings.HasPrefix(line, "\t") {
			lines = append(lines, line)
		}
	}

	return lines
}

func makeExpandsSecret(line string) bool {
	line = makeIfSecret.ReplaceAllString(line, "")
	for _, loc := range makeExpandedSecret.FindAllStringIndex(line, -1) {
		if loc[0] == 0 || line[loc[0]-1] != '$' {
			return true
		}
	}

	return false
}

// makefileArgvLeaks lists every recipe line that puts a credential value on a process argv.
func makefileArgvLeaks(makefile string) []string {
	var leaks []string
	for _, line := range recipeLines(makefile) {
		if makeExpandsSecret(line) || dockerEnvWithValue.MatchString(line) || makeWithSecretArg.MatchString(line) ||
			secretFlagOnArgv.MatchString(line) || secretInAuthHeader.MatchString(line) {
			leaks = append(leaks, strings.TrimSpace(line))
		}
	}

	return leaks
}

// devopsReleaseLeaks checks that run_release_with_devops loads the credentials into the environment
// with anchored patterns and starts the release without passing them as make arguments.
func devopsReleaseLeaks(makefile string) []string {
	_, after, found := strings.Cut(makefile, "\nrun_release_with_devops:")
	if !found {
		return []string{"no run_release_with_devops target"}
	}
	recipe, _, _ := strings.Cut(after, "\n\n")

	var leaks []string
	if strings.Contains(recipe, "$(info)") {
		leaks = append(leaks, "make release $(info) passes the credentials as make arguments")
	}
	if !strings.Contains(recipe, "set -a") || !strings.Contains(recipe, "grep -h -E '^(") {
		leaks = append(leaks, "credentials are not loaded into the environment with anchored ^NAME= patterns")
	}
	if !regexp.MustCompile(`\$\(MAKE\) release\s*$`).MatchString(recipe) {
		leaks = append(leaks, "the recipe does not end in a bare $(MAKE) release")
	}

	return leaks
}

// workflowArgvLeaks lists every workflow `run:` line that interpolates a secret into the script text
// (GitHub renders ${{ secrets.X }} into the command before the shell starts).
func workflowArgvLeaks(workflow string) []string {
	var leaks []string
	runIndent := -1
	for _, line := range strings.Split(workflow, "\n") {
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if runIndent >= 0 && strings.TrimSpace(line) != "" && indent <= runIndent {
			runIndent = -1
		}
		if match := runKey.FindStringSubmatch(line); match != nil {
			runIndent = len(match[1])
			line = match[2]
		} else if runIndent < 0 {
			continue
		}
		if strings.Contains(line, "${{ secrets.") || strings.Contains(line, "${{secrets.") {
			leaks = append(leaks, strings.TrimSpace(line))
		}
	}

	return leaks
}

func TestNoReleaseRecipePutsACredentialOnArgv(t *testing.T) {
	if leaks := makefileArgvLeaks(readRepoFile(t, "Makefile")); len(leaks) != 0 {
		t.Errorf("Makefile recipe lines put a credential on a process argv:\n%s", strings.Join(leaks, "\n"))
	}
}

func TestTheDevopsReleaseHandsTheCredentialsOverTheEnvironment(t *testing.T) {
	if leaks := devopsReleaseLeaks(readRepoFile(t, "Makefile")); len(leaks) != 0 {
		t.Errorf("run_release_with_devops: %s", strings.Join(leaks, "; "))
	}
}

func TestNoWorkflowRunLineInterpolatesASecret(t *testing.T) {
	workflows, err := filepath.Glob("../.github/workflows/*.y*ml")
	if err != nil {
		t.Fatalf("listing the workflows failed: %v", err)
	}
	if len(workflows) == 0 {
		t.Fatal("no workflow found under .github/workflows")
	}
	for _, path := range workflows {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s failed: %v", path, err)
		}
		if leaks := workflowArgvLeaks(string(content)); len(leaks) != 0 {
			t.Errorf("%s: run: lines interpolate a secret (move it to env:):\n%s", path, strings.Join(leaks, "\n"))
		}
	}
}

// The detectors must catch every shape that has leaked a credential before, or the tests above prove
// nothing.
func TestTheLeakDetectorsCatchTheKnownShapes(t *testing.T) {
	for _, leak := range []string{
		"\t@echo $(GITHUB_GH_TOKEN) | gh auth login --with-token",
		"\tgh auth login --with-token <<< ${GITHUB_GH_TOKEN}",
		"\tdocker run --rm -e GITHUB_GH_TOKEN=$${GITHUB_GH_TOKEN} image make push_to_gh",
		"\t@docker run --env NPM_AUTOMATION_TOKEN=x image",
		"\t$(MAKE) release GITHUB_GH_TOKEN=$$value",
		"\tcargo publish --token $$CARGO_TOKEN",
		"\tdotnet nuget push -k $${NUGET_API_KEY} pkg.nupkg",
		`	curl -H "Authorization: Bearer $$PACKAGIST_API_TOKEN" url`,
	} {
		if len(makefileArgvLeaks(leak)) == 0 {
			t.Errorf("not detected: %q", leak)
		}
	}
	for _, safe := range []string{
		`	@printf '%s\n' "$$GITHUB_GH_TOKEN" | gh auth login -p ssh --with-token`,
		"\t@docker run --rm -e GITHUB_GH_TOKEN image make push_to_gh",
		"\t@echo \"GH Token: $(if $(GITHUB_GH_TOKEN),<set>,<unset>)\"",
		"\t@echo \"GH Token: $(if $(filter-out ENTER_YOUR_TOKEN_HERE,$(GITHUB_GH_TOKEN)),<set>,<unset>)\"",
		"\t$(MAKE) release",
	} {
		if leaks := makefileArgvLeaks(safe); len(leaks) != 0 {
			t.Errorf("false positive: %q", safe)
		}
	}

	if len(devopsReleaseLeaks("\nrun_release_with_devops:\n\t$(eval info:= $(shell cat f | grep GITHUB_GH))\n\t@make release $(info)\n")) == 0 {
		t.Error("the old run_release_with_devops is not detected")
	}

	leakyWorkflow := "    steps:\n      - run: gh release view --token ${{ secrets.X }}\n      - name: y\n        run: |\n          echo ok\n          curl -u ${{ secrets.Y }} url\n"
	if got := workflowArgvLeaks(leakyWorkflow); len(got) != 2 {
		t.Errorf("workflow leaks detected: %q, want both run: lines", got)
	}
	safeWorkflow := "      - name: y\n        env:\n          GH_TOKEN: ${{ secrets.X }}\n        run: |\n          gh release view\n"
	if got := workflowArgvLeaks(safeWorkflow); len(got) != 0 {
		t.Errorf("false positive in workflow: %q", got)
	}
}
