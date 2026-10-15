# NO global `export`: it used to hand every variable of this file - GITHUB_GH_TOKEN included - to
# every recipe, i.e. to go test, docker and pre-commit as well. The token now reaches only the
# steps that use it (see WITHOUT_CREDENTIALS and GH_RUN below).

# =====================================================================================
# ondewo-vtsi-client-go - Makefile
#
# The ONDEWO VTSI (Virtual Telephony Server Interface) gRPC client for go. Everything below the
# `api/` directory is GENERATED from the .proto files of the ondewo-vtsi-api submodule by the
# ondewo-proto-compiler image built from the ondewo-proto-compiler submodule - never edit it
# by hand, `make generate_ondewo_protos` wipes and rewrites it.
#
# Quick start:
#   make help                     # list every documented target
#   make makefile_chapters        # list the section headers below
#   make setup_developer_environment_locally
#   make build                    # submodules -> compiler image -> stubs -> go build
#   make test                     # go test ./...
#
# Versioning: ONDEWO_VTSI_VERSION (below) is the single source of truth and MUST
# match the ONDEWO VTSI API in major and minor version.
#
# Overriding variables: pass on the command line, e.g. `make build PROTOS_TARGET_DIR=ondewo`,
# or export in the environment. Credentials (GITHUB_GH_TOKEN) are only ever read at runtime
# and must not be committed.
# =====================================================================================

# ---------------- BEFORE RELEASE ----------------
# 1 - Update Version Number
# 2 - Update RELEASE.md
# 3 - make build
# -------------- Release Process Steps (make release) --------------
# 1 - release_preflight: read-only - token valid with push permission, version absent on origin and
#     on proxy.golang.org, non-empty RELEASE.md section, host tools present
# 2 - build, then the full test gate (fmt, vet, the suite with its coverage gate)
# 3 - local release commit, then publish_dry_run against exactly that commit
# 4 - push, release branch, BOTH tags (the ONDEWO tag and the go `vX.Y.Z` tag)
# 5 - publish_go_module + verify_go_module_served: the proxy fetches and serves the tag
# 6 - GitHub release, then release_verify (tags, non-empty release body, module served)
#
# Publishing a go module needs NO registry credential: proxy.golang.org serves whatever the public
# VCS tag carries, so `make publish_dry_run` rehearses the entire publication offline and without a
# single secret. The only credential in this repository is GITHUB_GH_TOKEN, and it buys the GitHub
# release, not the module.

########################################################
# 		Variables
########################################################

# MUST BE THE SAME AS API in Major and Minor Version Number
# example: API 2.9.0 --> Client 2.9.X
ONDEWO_VTSI_VERSION=9.1.0

# Submodule pins - `make checkout_defined_submodule_versions` checks these out
ONDEWO_VTSI_API_GIT_BRANCH=tags/9.1.0
ONDEWO_PROTO_COMPILER_GIT_BRANCH=tags/5.15.7

# You need to setup an access token at https://github.com/settings/tokens - permissions are important
GITHUB_GH_TOKEN?=ENTER_YOUR_TOKEN_HERE

# --- Directories
ONDEWO_API_DIR=ondewo-vtsi-api
ONDEWO_PROTO_COMPILER_DIR=ondewo-proto-compiler
ONDEWO_PROTOS_DIR=${ONDEWO_API_DIR}/ondewo
# Sub-directory of the proto root that is compiled (2nd positional argument of the image).
# It has to be closed over every non-google import of the protos it selects - protoc-gen-go
# emits a go import for a dependency proto even when protoc reports the import as unused -
# which `ondewo` is: the vendored google/** tree is excluded by the image itself and resolved
# from google.golang.org/protobuf + google.golang.org/genproto instead of being generated.
PROTOS_TARGET_DIR=ondewo
# Where the image writes the generated stubs inside the output volume. It WIPES this directory
# before every run so a renamed or deleted proto leaves no orphan behind -> nothing hand
# written may live below it.
STUBS_DIR=api
# The compiler image is tagged with the compiler RELEASE it was built from (5.15.7, not `latest`), so
# a stale image of another pin can never be picked up by mistake, and another repository building
# its own go compiler image on the same host cannot swap it out underneath a running release.
PROTO_COMPILER_VERSION=$(subst /,-,$(patsubst tags/%,%,$(ONDEWO_PROTO_COMPILER_GIT_BRANCH)))
PROTO_COMPILER_IMAGE=ondewo-go-proto-compiler:$(PROTO_COMPILER_VERSION)

# --- Go module identity
# Go resolves a module straight from its VCS path, and from major version 2 on that path has to
# carry the major as a `/vN` suffix (https://go.dev/ref/mod#major-version-suffixes): a v8.1.2
# tag on a module declared without `/v8` is invisible to `go get`. Derived from the version
# above so the module path, the go.mod the compiler renders and the import path baked by
# protoc-gen-go into every generated file all move together.
GO_MODULE_BASE_PATH=github.com/ondewo/ondewo-vtsi-client-go
GO_MODULE_MAJOR=$(firstword $(subst ., ,$(ONDEWO_VTSI_VERSION)))
GO_MODULE_MAJOR_SUFFIX=$(if $(filter 0 1,$(GO_MODULE_MAJOR)),,/v$(GO_MODULE_MAJOR))
GO_MODULE_PATH=$(GO_MODULE_BASE_PATH)$(GO_MODULE_MAJOR_SUFFIX)
# The `v`-prefixed spelling of the release, which is the only tag shape go tooling accepts
GO_RELEASE_TAG=v${ONDEWO_VTSI_VERSION}

# Both submodules are checked out INSIDE the module tree, so `./...` and a bare `gofmt .` would
# reach into them - the compiler ships a go file of its own (its module warm-up file), which is
# not part of this module and must not be built, vetted or reformatted here. `$$(...)`, so the
# filtering happens in the shell when the recipe runs, and so these still nest inside another
# command substitution (backticks would not).
GO_PACKAGES=$$(go list ./... | grep -v "/${ONDEWO_PROTO_COMPILER_DIR}/")
GO_SOURCES=$$(find . -name "*.go" ! -path "./${ONDEWO_PROTO_COMPILER_DIR}/*" ! -path "./${ONDEWO_API_DIR}/*")
# `go build` on a package that holds only _test.go files is an error ("no non-test Go files"), and
# tests/ is exactly such a package - it is compiled by `go test`, never by `go build`. .GoFiles is
# empty for those, which is how they are dropped here. `go vet` and `go test` handle them fine, so
# only the build target needs the narrower list.
GO_BUILD_PACKAGES=$$(go list -f '{{if .GoFiles}}{{.ImportPath}}{{end}}' ./... | grep -v "/${ONDEWO_PROTO_COMPILER_DIR}/")

# --- Go toolchain
# Every go target runs on the host's go when there is one, and otherwise re-runs ITSELF inside the
# pinned golang image - the same toolchain release.yml uses. The release host (the machine running
# `make release_go_client` in ondewo-vtsi-api) has no go installed, and `make release` used to die in
# `go_build` with `go: not found` after the stubs had already been generated. CI is unaffected: its
# runners have go, so HOST_GO is set there and every recipe below runs exactly as before.
# No credential is handed to the container: none of these targets needs one, and GITHUB_GH_TOKEN
# stays in the environment of the host make only. The build and module caches live in a host
# directory so consecutive targets of one release do not each download the dependency graph again;
# publish_dry_run still points GOMODCACHE at its own throwaway directory, inside the container too.
HOST_GO := $(shell command -v go 2>/dev/null)
# Pinned by version AND digest (the multi-arch index): a re-pushed tag cannot change the toolchain.
GO_DOCKER_IMAGE=golang:1.27.1@sha256:162be5298a40ed317005c8339c6de4d10d3eef336d66dc8e9259b03ab9d3a6d2
GO_DOCKER_CACHE_DIR?=$(HOME)/.cache/ondewo-go-docker
GO_DOCKER_RUN=mkdir -p "$(GO_DOCKER_CACHE_DIR)" && docker run --rm --user $$(id -u):$$(id -g) \
	-e HOME=/tmp/home -e GOCACHE=/go-docker-cache/build -e GOMODCACHE=/go-docker-cache/mod \
	-v "$(GO_DOCKER_CACHE_DIR)":/go-docker-cache -v "$(CURDIR)":"$(CURDIR)" -w "$(CURDIR)" \
	$(GO_DOCKER_IMAGE) make

# --- Coverage
# The threshold is enforced over the HAND-WRITTEN packages only. Everything below ${STUBS_DIR}/ is
# machine output: gating on it would measure how much of protoc's output a test happens to walk,
# not how much of what somebody wrote is tested. The stubs are still exercised for real - the
# suite round-trips messages on the wire and calls every generated unary stub - it is only the
# NUMBER they are kept out of. `make test_coverage_generated` prints their figure for the record.
COVERAGE_PACKAGES=./auth/...,./client/...
COVERAGE_THRESHOLD=100.0
COVERAGE_PROFILE=coverage.out

# Terminate on the ***** separator that delimits release entries, NOT on /\*\*/ - that matches
# the first markdown **bold** span inside the entry and silently truncates the notes there,
# with no error from `gh release create`. The heading is ANCHORED and the version quoted (\Q..\E),
# so `9.1.1` can never start at a `9.1.10` heading, nor at a line that merely mentions the version.
CURRENT_RELEASE_NOTES=`cat RELEASE.md \
	| perl -ne 'print if /^\#+ Release ONDEWO VTSI Go Client \Q${ONDEWO_VTSI_VERSION}\E$$/../^\*{5}/'`

GH_REPO="https://github.com/ondewo/ondewo-vtsi-client-go"
# <owner>/<repo>, the form the GitHub REST API addresses the repository by (check_gh_token_valid).
GH_REPO_PATH=ondewo/ondewo-vtsi-client-go
# The devops-accounts clone never lives inside this repository: it is cloned into a fresh mktemp
# directory and deleted before the release starts (run_release_with_devops). When the environment
# variable DEVOPS_ACCOUNT_DIR names an existing directory (the release driver's single clone, an
# absolute path), that one is read instead and nothing is cloned.
DEVOPS_ACCOUNT_GIT_URL?=git@bitbucket.org:ondewo/ondewo-devops-accounts.git

# --- gh, pinned
# The GitHub CLI is a pinned, checksum-verified release binary downloaded into a per-user cache
# (install_gh) - never the host's gh, whatever version that is. It is a static binary, so the
# release host needs only curl, tar and sha256sum to obtain it.
GH_VERSION=2.100.0
GH_SHA256_amd64=e4d4bb4498e8d007abe545b6568926793ace1b6447da598294a610018cb164be
GH_SHA256_arm64=ea4e7a581a32ccad6cc7923cb1576ac5859ba4b9a16ab22eb8f8a96e78e2e961
GH_CACHE_DIR?=$(HOME)/.cache/ondewo-gh/$(GH_VERSION)
GH=$(GH_CACHE_DIR)/bin/gh
# Every gh call: the token goes in as GH_TOKEN for THAT command only, expanded by the shell from the
# environment (never on argv, never in a gh config file - there is no `gh auth login`).
GH_RUN=GH_NO_UPDATE_NOTIFIER=1 GH_PROMPT_DISABLED=1 GH_CONFIG_DIR="$(GH_CACHE_DIR)/config" GH_TOKEN="$$GITHUB_GH_TOKEN" "$(GH)"

# Prefix for every step that does not need a credential (build, the suite, the rehearsal,
# pre-commit): the token is removed from that process tree altogether.
WITHOUT_CREDENTIALS=env -u GITHUB_GH_TOKEN -u GH_TOKEN -u GITHUB_TOKEN
# Non-empty under `make -n` (run_release_with_devops then clones nothing).
MAKE_DRY_RUN=$(findstring n,$(firstword -$(MAKEFLAGS)))
# The release driver (ondewo-vtsi-api) also runs `make release_preflight` and `make release_verify`
# STANDALONE, with only an absolute DEVOPS_ACCOUNT_DIR in the environment. Every recipe line that uses
# the token therefore starts with this: when GITHUB_GH_TOKEN is not already in the environment, it is
# read from $$DEVOPS_ACCOUNT_DIR/account_github.env (anchored ^NAME=) into that line's shell only.
LOAD_RELEASE_CREDENTIALS=if { [ -z "$$GITHUB_GH_TOKEN" ] || [ "$$GITHUB_GH_TOKEN" = "ENTER_YOUR_TOKEN_HERE" ]; } \
	&& [ -n "$$DEVOPS_ACCOUNT_DIR" ] && [ -f "$$DEVOPS_ACCOUNT_DIR/account_github.env" ]; then \
	set -a; eval "`grep -h -E '^(GITHUB_GH_TOKEN)=' "$$DEVOPS_ACCOUNT_DIR/account_github.env"`"; set +a; fi

# --- Release verification
GO_PROXY_URL=https://proxy.golang.org
# proxy.golang.org usually serves a fresh tag within seconds; 20 x 30 s is the patience the release
# allows it before failing.
RELEASE_VERIFY_ATTEMPTS?=20
RELEASE_VERIFY_INTERVAL_S?=30

# `make` with no target prints the help listing.
.DEFAULT_GOAL := help

# Define colors globally (reused for [INFO]/[SUCCESS]/[WARN]/[ERROR] log lines in recipes)
BLUE   := \033[1;34m
GREEN  := \033[0;32m
YELLOW := \033[1;33m
RED    := \033[0;31m
NC     := \033[0m

########################################################
#       ONDEWO Standard Make Targets
########################################################

setup_developer_environment_locally: update_submodules install_precommit_hooks ## Ready a fresh laptop: check out submodules and install the pre-commit hooks

install_precommit_hooks: ## Installs pre-commit hooks and sets them up for the ondewo-vtsi-client-go repo
	pre-commit install
	pre-commit install --hook-type commit-msg

precommit_hooks_run_all_files: ## Runs all pre-commit hooks on all files and not just the changed ones
	pre-commit run --all-files

help: ## Print usage info about help targets
	# (first comment after target starting with double hashes ##)
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' Makefile | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-40s\033[0m %s\n", $$1, $$2}'

makefile_chapters: ## Shows all sections of Makefile
	@echo `cat Makefile| grep "########################################################" -A 1 | grep -v "########################################################"`

TEST: ## Prints some important variables
	@echo "Release Notes: \n \n$(CURRENT_RELEASE_NOTES)"
	@echo "GH Token: \t $(if $(filter-out ENTER_YOUR_TOKEN_HERE,$(GITHUB_GH_TOKEN)),<set>,<unset>)"
	@echo "Go Module Path: \t $(GO_MODULE_PATH)"
	@echo "Go Release Tag: \t $(GO_RELEASE_TAG)"
	@echo "Compiler Image: \t $(PROTO_COMPILER_IMAGE)"

# Single-value printers for scripts and CI - `make -s <target>` yields the bare value, so the
# release workflow reads the version, the tag and the module path from this Makefile instead of
# keeping a second copy of them in yaml that can drift.
print_version: ## Print ONDEWO_VTSI_VERSION and nothing else
	@echo "${ONDEWO_VTSI_VERSION}"

print_go_release_tag: ## Print the `v`-prefixed release tag and nothing else
	@echo "${GO_RELEASE_TAG}"

print_go_module_path: ## Print the go module path (including its /vN suffix) and nothing else
	@echo "${GO_MODULE_PATH}"

check_build: ## Checks if all built proto-code is there
	@rm -f build_check.txt
	@for proto in `find ${ONDEWO_PROTOS_DIR} -iname "*.proto"`; \
	do \
		basename $${proto} .proto >> build_check.txt; \
	done
	@echo "`sort build_check.txt | uniq`" > build_check.txt
	@for file in `cat build_check.txt`; \
	do \
		find ${STUBS_DIR} -iname "$${file}.pb.go" | grep -q . \
			|| { echo "$(RED)[ERROR]$(NC) No Proto-Code for $${file}"; rm -f build_check.txt; exit 1; }; \
	done
	@rm -f build_check.txt
	@echo "$(GREEN)[SUCCESS]$(NC) Every .proto below ${ONDEWO_PROTOS_DIR} has a generated *.pb.go"

########################################################
#       Repo Specific Make Targets
########################################################
#		Build

build: update_submodules checkout_defined_submodule_versions build_compiler generate_ondewo_protos go_build ## Build the client end to end: submodules -> compiler image -> stubs -> go build

build_compiler: ## Build the proto compiler docker image from the pinned submodule, tagged with its version
	@echo "$(BLUE)[INFO]$(NC) Building ${PROTO_COMPILER_IMAGE} from ${ONDEWO_PROTO_COMPILER_DIR}/go ..."
# The same `docker build` as the submodule's go/build.sh, which can only tag `:latest`. The build
# context is the compiler's own go/ directory, never this repository.
	docker build --no-cache -t ${PROTO_COMPILER_IMAGE} ${ONDEWO_PROTO_COMPILER_DIR}/go
	@echo "$(GREEN)[SUCCESS]$(NC) Built ${PROTO_COMPILER_IMAGE}"

generate_ondewo_protos: ## Generate the go stubs from the .proto files of the ondewo-vtsi-api submodule
	@[ -d ${ONDEWO_PROTOS_DIR} ] || { \
		echo "$(RED)[ERROR]$(NC) ${ONDEWO_PROTOS_DIR} does not exist - run 'make update_submodules' first"; \
		exit 1; \
	}
	@echo "$(BLUE)[INFO]$(NC) Generating go stubs into ${STUBS_DIR}/ for module ${GO_MODULE_PATH} ..."
# The positional arguments are the contract of the image, taken verbatim from
#	ondewo-proto-compiler/go/example/run-compile.sh:
#	  <relative_protos_dir>  directory BELOW the input volume that is protoc's -I root
#	  <target_sub_directory> sub-directory of that root to compile; quoted, because the module
#	                         path follows it and must not slide into its place when it is empty
#	  <go_module_path>       protoc-gen-go bakes it into every generated file, so it has to be
#	                         known before protoc runs
# Only what the image needs is mounted: the api submodule, READ-ONLY, as the input, and an empty
#	staging directory as the output. The repository root is never mounted, so nothing else that
#	sits in the working tree reaches a container that runs as root.
# The image writes the stubs as root (GOPATH and GOCACHE are root-owned inside it), so the staging
#	directory is handed back to the caller by a second run of the same image (`chown`, no sudo)
#	before ${STUBS_DIR}/ is swapped in. go.mod / go.sum are taken from the image ONLY when this
#	repository has neither - once committed they are maintained here.
# NOTE: no -it. A TTY-enabled container breaks every non-interactive caller with
#	"cannot attach stdin to a TTY-enabled container because stdin is not a terminal".
	@set -e; \
	out=`mktemp -d`; \
	give_back() { docker run --rm --entrypoint chown -v "$$out":/output-volume ${PROTO_COMPILER_IMAGE} -R "`id -u`:`id -g`" /output-volume >/dev/null 2>&1 || true; }; \
	trap 'give_back; rm -rf "$$out"' EXIT INT TERM; \
	docker run --rm \
		-v "$(CURDIR)/${ONDEWO_API_DIR}":/input-volume/${ONDEWO_API_DIR}:ro \
		-v "$$out":/output-volume \
		${PROTO_COMPILER_IMAGE} ${ONDEWO_API_DIR} "${PROTOS_TARGET_DIR}" "${GO_MODULE_PATH}"; \
	give_back; \
	[ -d "$$out/${STUBS_DIR}" ] || { echo "$(RED)[ERROR]$(NC) the compiler wrote no ${STUBS_DIR}/"; exit 1; }; \
	rm -rf "${STUBS_DIR}"; \
	mv "$$out/${STUBS_DIR}" "${STUBS_DIR}"; \
	if [ ! -f go.mod ] && [ ! -f go.sum ]; then \
		for manifest in go.mod go.sum; do [ ! -f "$$out/$$manifest" ] || cp "$$out/$$manifest" "$$manifest"; done; \
	fi
	@echo "$(GREEN)[SUCCESS]$(NC) Generated go stubs in ${STUBS_DIR}/"

go_build: ## Compile every package of the module that has non-test sources
ifeq ($(HOST_GO),)
	$(GO_DOCKER_RUN) $@
else
	go build $(GO_BUILD_PACKAGES)
endif

test: ## Run the go test suite
ifeq ($(HOST_GO),)
	$(GO_DOCKER_RUN) $@
else
	go test $(GO_PACKAGES)
endif

test_coverage: ## Run the suite under the race detector and fail if hand-written coverage < COVERAGE_THRESHOLD
ifeq ($(HOST_GO),)
	$(GO_DOCKER_RUN) $@
else
	@echo "$(BLUE)[INFO]$(NC) Running the test suite, measuring coverage of $(COVERAGE_PACKAGES) ..."
	go test -count=1 -race -coverpkg=$(COVERAGE_PACKAGES) -coverprofile=$(COVERAGE_PROFILE) $(GO_PACKAGES)
	@go tool cover -func=$(COVERAGE_PROFILE)
# An empty profile reports "total: 0.0%" and a threshold of 0 would wave it through, so the gate
# first insists that at least one hand-written function was measured at all - that is what turns a
# deleted or renamed COVERAGE_PACKAGES into a failure instead of a green run measuring nothing.
	@measured=`go tool cover -func=$(COVERAGE_PROFILE) | grep -c "%$$"`; \
	if [ "$$measured" -lt 2 ]; then \
		echo "$(RED)[ERROR]$(NC) the profile measured no function of $(COVERAGE_PACKAGES) - is the package still there?"; \
		exit 1; \
	fi; \
	total=`go tool cover -func=$(COVERAGE_PROFILE) | awk '/^total:/ {print $$3}' | tr -d '%'`; \
	awk -v total="$$total" -v threshold="$(COVERAGE_THRESHOLD)" 'BEGIN { exit !(total + 0 >= threshold + 0) }' || { \
		echo "$(RED)[ERROR]$(NC) hand-written coverage is $$total%, below the required $(COVERAGE_THRESHOLD)%"; \
		exit 1; \
	}; \
	echo "$(GREEN)[SUCCESS]$(NC) hand-written coverage is $$total% (threshold $(COVERAGE_THRESHOLD)%)"
endif

test_coverage_generated: ## Print how much of the GENERATED stubs the suite exercises (reported, never gated)
ifeq ($(HOST_GO),)
	$(GO_DOCKER_RUN) $@
else
	go test -count=1 -coverpkg=./${STUBS_DIR}/... -coverprofile=generated-$(COVERAGE_PROFILE) $(GO_PACKAGES)
	@go tool cover -func=generated-$(COVERAGE_PROFILE) | tail -1
endif

check_stubs: ## Assert the generated stubs are committed - the build has nothing to compile without them
	@[ -d ${STUBS_DIR} ] || { echo "$(RED)[ERROR]$(NC) ${STUBS_DIR}/ is missing - the generated stubs are not committed"; exit 1; }
	@messages=`find ${STUBS_DIR} -name "*.pb.go" ! -name "*_grpc.pb.go" | grep -c . || true`; \
	services=`find ${STUBS_DIR} -name "*_grpc.pb.go" | grep -c . || true`; \
	if [ "$$messages" -eq 0 ] || [ "$$services" -eq 0 ]; then \
		echo "$(RED)[ERROR]$(NC) ${STUBS_DIR}/ holds $$messages message stubs and $$services service stubs - both must be non-zero"; \
		exit 1; \
	fi; \
	echo "$(GREEN)[SUCCESS]$(NC) ${STUBS_DIR}/ holds $$messages message stubs and $$services service stubs"
	@[ -f go.mod ] && [ -f go.sum ] || { echo "$(RED)[ERROR]$(NC) go.mod / go.sum are missing - the module is not installable"; exit 1; }

# A go module carries its own major version in its path from v2 on, and nothing at build time
# notices when the two disagree: everything compiles, `go build` is green, and the module simply
# stays invisible to `go get` once the release is tagged ("module .../ondewo-vtsi-client-go@v8.7.0:
# invalid version: module contains a go.mod file, so major version must be compatible"). This is
# the gate that turns that silent mismatch into a failed build, in CI and before every release.
check_go_module_path: ## Assert go.mod and every import agree with the /vN suffix ONDEWO_VTSI_VERSION requires
	@declared=`sed -n 's|^module[[:space:]][[:space:]]*\([^[:space:]]*\).*|\1|p' go.mod | head -1`; \
	if [ "$$declared" != "${GO_MODULE_PATH}" ]; then \
		echo "$(RED)[ERROR]$(NC) go.mod declares module '$$declared', but ONDEWO_VTSI_VERSION=${ONDEWO_VTSI_VERSION} requires '${GO_MODULE_PATH}'"; \
		echo "        From major version 2 on the module path must end in /vN (https://go.dev/ref/mod#major-version-suffixes)."; \
		echo "        The path is baked into every generated import, so a go.mod-only edit is not enough - regenerate:"; \
		echo "            make generate_ondewo_protos"; \
		exit 1; \
	fi; \
	echo "$(GREEN)[SUCCESS]$(NC) go.mod declares $$declared, matching ONDEWO_VTSI_VERSION=${ONDEWO_VTSI_VERSION}"
# Second half of the same question, asked of the sources instead of the manifest: a self-import
# spelled without the suffix (the shape `protoc-gen-go` bakes in when it is handed the wrong module
# path) resolves to a DIFFERENT, unpublished module rather than to this one.
	@offenders=`grep -rn "\"${GO_MODULE_BASE_PATH}" --include="*.go" ${STUBS_DIR} auth tests \
		| grep -v "\"${GO_MODULE_PATH}/" | grep -v "\"${GO_MODULE_PATH}\"" || true`; \
	if [ -n "$$offenders" ]; then \
		echo "$(RED)[ERROR]$(NC) these self-imports are not spelled ${GO_MODULE_PATH}/...:"; \
		echo "$$offenders"; \
		echo "        Regenerate the stubs and repoint the hand-written imports at ${GO_MODULE_PATH}"; \
		exit 1; \
	fi; \
	echo "$(GREEN)[SUCCESS]$(NC) every self-import is spelled ${GO_MODULE_PATH}/..."

vet: ## Run go vet over the hand-written packages and the generated stubs
ifeq ($(HOST_GO),)
	$(GO_DOCKER_RUN) $@
else
	go vet $(GO_PACKAGES)
endif

# Both gofmt targets guard on an empty file list: gofmt with no arguments reads STDIN, which
# in CI is a hang, not an error. The list is empty in exactly one situation - a fresh clone
# before the first `make generate_ondewo_protos`.
fmt: ## Format the hand-written go sources in place
ifeq ($(HOST_GO),)
	$(GO_DOCKER_RUN) $@
else
	@sources=$(GO_SOURCES); \
	if [ -z "$$sources" ]; then \
		echo "$(YELLOW)[WARN]$(NC) no go sources yet - run 'make generate_ondewo_protos' first"; \
		exit 0; \
	fi; \
	gofmt -l -w $$sources
endif

fmt_check: ## Fail if any go source is not gofmt-clean (prints the offenders)
ifeq ($(HOST_GO),)
	$(GO_DOCKER_RUN) $@
else
	@sources=$(GO_SOURCES); \
	if [ -z "$$sources" ]; then \
		echo "$(YELLOW)[WARN]$(NC) no go sources yet - run 'make generate_ondewo_protos' first"; \
		exit 0; \
	fi; \
	offenders=$$(gofmt -l $$sources) || exit 1; \
	if [ -n "$$offenders" ]; then \
		echo "$(RED)[ERROR]$(NC) not gofmt-clean - run 'make fmt':"; \
		echo "$$offenders"; \
		exit 1; \
	fi; \
	echo "$(GREEN)[SUCCESS]$(NC) All go sources are gofmt-clean"
endif

clean_go_api: ## Remove the generated stubs
	rm -rf ${STUBS_DIR}

########################################################
#		Submodules

update_submodules: ## Initialize and update all submodules
	@echo "$(BLUE)[INFO]$(NC) START initializing submodules ..."
	git submodule update --init --recursive
	@echo "$(GREEN)[SUCCESS]$(NC) DONE initializing submodules"

checkout_defined_submodule_versions: ## Check out the submodule versions pinned in the Variables chapter
	@echo "$(BLUE)[INFO]$(NC) START checking out submodules ..."
	git -C ${ONDEWO_API_DIR} fetch --all
	git -C ${ONDEWO_API_DIR} checkout ${ONDEWO_VTSI_API_GIT_BRANCH}
	git -C ${ONDEWO_PROTO_COMPILER_DIR} fetch --all
	git -C ${ONDEWO_PROTO_COMPILER_DIR} checkout ${ONDEWO_PROTO_COMPILER_GIT_BRANCH}
	@echo "$(GREEN)[SUCCESS]$(NC) DONE checking out submodules"

########################################################
#		Release

release: ## Automate the entire release process (order pinned by tests/release_order_test.go)
	@echo "$(BLUE)[INFO]$(NC) Start Release"
# FIRST, before anything is built, committed, pushed or tagged, and changing nothing: every credential
# is present AND accepted by GitHub with push permission, the version exists neither on origin nor
# on proxy.golang.org, RELEASE.md has a non-empty section for it, and the host has its tools. A
# published go module version is immutable, so everything that can be refuted early is.
	$(MAKE) release_preflight
# Build and the FULL test gate, against the freshly generated stubs. No step from here to the
# commit needs the token, so it is removed from their environment (third-party code runs here).
	$(WITHOUT_CREDENTIALS) $(MAKE) build
	$(WITHOUT_CREDENTIALS) $(MAKE) check_build
	$(WITHOUT_CREDENTIALS) $(MAKE) check_go_module_path
	$(WITHOUT_CREDENTIALS) $(MAKE) fmt_check
	$(WITHOUT_CREDENTIALS) $(MAKE) vet
	$(WITHOUT_CREDENTIALS) $(MAKE) test_coverage
# pre-commit is optional on a release host; when present it may normalise whitespace before the commit.
	-$(WITHOUT_CREDENTIALS) $(MAKE) precommit_hooks_run_all_files
	git status
	git add ${STUBS_DIR}
	git add Makefile
	git add README.md
	git add RELEASE.md
# go.mod / go.sum are the manifest of the published module: a release whose tag does not carry
# them is not installable.
	git add go.mod
	git add go.sum
	git add ${ONDEWO_PROTO_COMPILER_DIR}
	git add ${ONDEWO_API_DIR}
	git status
# Commit only when something is staged, but never swallow a REAL commit failure (e.g. no git
# identity): the tags below would then silently name the previous commit.
	git diff --cached --quiet || git commit --no-verify -m "PREPARING FOR RELEASE ${ONDEWO_VTSI_VERSION}"
# The last local refusal: rehearse the publication of exactly the commit the tags will name -
# `git archive HEAD` resolved by an outside consumer - before anything leaves this machine.
	$(WITHOUT_CREDENTIALS) $(MAKE) publish_dry_run
	git push
	$(MAKE) create_release_branch
	$(MAKE) create_release_tag
# A go module has no upload: proxy.golang.org fetches the PUSHED tag, so for Go the publish step
# necessarily comes after the tag. It runs BEFORE the GitHub release, so a GitHub release only ever
# exists for a module the proxy actually serves.
	$(WITHOUT_CREDENTIALS) $(MAKE) publish_go_module
	$(MAKE) verify_go_module_served
	$(MAKE) push_to_gh
	$(MAKE) release_verify
	@echo "$(GREEN)[SUCCESS]$(NC) Release Finished"

release_preflight: check_release_host_tools check_release_credentials check_gh_token_valid check_version_unreleased check_release_notes ## Read-only: fail before anything is built, committed or pushed
	@echo "$(GREEN)[SUCCESS]$(NC) Preflight passed for ${ONDEWO_VTSI_VERSION} (${GO_RELEASE_TAG})"

# go itself is NOT required: every go target falls back to the pinned golang image, and gh is the
# pinned binary install_gh downloads.
check_release_host_tools: ## Assert the release host has docker (running), make, git, perl, curl, tar and sha256sum
	@missing=""; \
	for tool in docker make git perl curl tar sha256sum mktemp; do \
		command -v $$tool >/dev/null 2>&1 || missing="$$missing $$tool"; \
	done; \
	if [ -n "$$missing" ]; then \
		echo "$(RED)[ERROR]$(NC) the release needs these host tools, which are missing:$$missing"; \
		exit 1; \
	fi
	@docker info >/dev/null 2>&1 || { echo "$(RED)[ERROR]$(NC) the docker daemon is not reachable - the compiler (and go, if absent) run in containers"; exit 1; }
	@echo "$(GREEN)[SUCCESS]$(NC) host tools present (go: $(if $(HOST_GO),$(HOST_GO),pinned image ${GO_DOCKER_IMAGE}))"

create_release_branch: ## Create Release Branch and push it to origin
	git checkout -b "release/${ONDEWO_VTSI_VERSION}"
	git push -u origin "release/${ONDEWO_VTSI_VERSION}"

create_release_tag: ## Create Release Tag and push it to origin
	git tag -a ${ONDEWO_VTSI_VERSION} -m "release/${ONDEWO_VTSI_VERSION}"
	git push origin ${ONDEWO_VTSI_VERSION}
# The same commit gets a second, `v`-prefixed name. A go module version IS a `vX.Y.Z` tag, so
# without this one `go get ${GO_MODULE_PATH}@${GO_RELEASE_TAG}` resolves nothing and every
# consumer is stuck on a pseudo-version of the default branch.
	git tag -a ${GO_RELEASE_TAG} -m "release/${GO_RELEASE_TAG}"
	git push origin ${GO_RELEASE_TAG}

# The value is read from the ENVIRONMENT, never interpolated into the recipe text, so make's own
# command echo cannot leak it and a space or a glob character in the token cannot break the command
# apart. Only its emptiness and the placeholder are tested here; check_gh_token_valid asks GitHub.
check_release_credentials: ## Fail loudly when GITHUB_GH_TOKEN is unset or still the placeholder
	@$(LOAD_RELEASE_CREDENTIALS); \
	if [ -z "$$GITHUB_GH_TOKEN" ] || [ "$$GITHUB_GH_TOKEN" = "ENTER_YOUR_TOKEN_HERE" ]; then \
		echo "$(RED)[ERROR]$(NC) GITHUB_GH_TOKEN is not set - the GitHub release cannot be created"; \
		echo "        Use 'make ondewo_release', which reads it from ondewo-devops-accounts/account_github.env,"; \
		echo "        or create a token with the 'repo' scope at https://github.com/settings/tokens"; \
		exit 1; \
	fi
	@echo "$(GREEN)[SUCCESS]$(NC) GITHUB_GH_TOKEN is set"

check_gh_credentials: check_release_credentials ## Deprecated alias of check_release_credentials

# GET /repos/<owner>/<repo> changes nothing. `.permissions` is only present for an authenticated
# caller, and `push` is exactly what the release branch, the tags and the GitHub release need. An
# expired, revoked or under-scoped token therefore fails HERE instead of after the tags are pushed.
check_gh_token_valid: check_release_credentials install_gh ## Read-only: assert GitHub accepts GITHUB_GH_TOKEN and it may push to this repository
	@$(LOAD_RELEASE_CREDENTIALS); \
	push=`$(GH_RUN) api "repos/${GH_REPO_PATH}" --jq .permissions.push` \
		|| { echo "$(RED)[ERROR]$(NC) GitHub refused GITHUB_GH_TOKEN (see the gh error above)"; exit 1; }; \
	if [ "$$push" != "true" ]; then \
		echo "$(RED)[ERROR]$(NC) GITHUB_GH_TOKEN is valid but may not push to ${GH_REPO_PATH} (permissions.push=$$push)"; \
		exit 1; \
	fi
	@echo "$(GREEN)[SUCCESS]$(NC) GitHub accepts GITHUB_GH_TOKEN and it may push to ${GH_REPO_PATH}"

# Origin, not the local clone (spc covers that): the exact refs are compared (grep -Fx), so 9.1.0
# never matches 19.1.0 or 9.1.0-rc. An unreachable origin or proxy fails the check - "could not
# look" is never read as "absent".
# Only the proxy's version LIST is read, never `@v/<version>.info`: asking the proxy for a version
# that is not tagged yet makes it cache the "not found" answer, which would then delay serving the
# very tag this release is about to push.
check_version_unreleased: ## Read-only: assert the release branch and both tags are absent on origin and the proxy does not know the version
	@refs=`git ls-remote origin "refs/heads/release/${ONDEWO_VTSI_VERSION}" "refs/tags/${ONDEWO_VTSI_VERSION}" "refs/tags/${GO_RELEASE_TAG}"` \
		|| { echo "$(RED)[ERROR]$(NC) git ls-remote origin failed - cannot prove the version is unreleased"; exit 1; }; \
	found=`printf '%s\n' "$$refs" | cut -f2 | grep -Fx -e "refs/heads/release/${ONDEWO_VTSI_VERSION}" -e "refs/tags/${ONDEWO_VTSI_VERSION}" -e "refs/tags/${GO_RELEASE_TAG}" || true`; \
	if [ -n "$$found" ]; then \
		echo "$(RED)[ERROR]$(NC) ${ONDEWO_VTSI_VERSION} already exists on origin:"; echo "$$found"; \
		exit 1; \
	fi
	@list=`mktemp`; \
	code=`curl -sS -o "$$list" -w '%{http_code}' "${GO_PROXY_URL}/${GO_MODULE_PATH}/@v/list"` \
		|| { rm -f "$$list"; echo "$(RED)[ERROR]$(NC) ${GO_PROXY_URL} is unreachable - cannot prove ${GO_RELEASE_TAG} is unpublished"; exit 1; }; \
	case "$$code" in \
		200) if grep -Fxq "${GO_RELEASE_TAG}" "$$list"; then rm -f "$$list"; echo "$(RED)[ERROR]$(NC) ${GO_MODULE_PATH}@${GO_RELEASE_TAG} is already listed by the module proxy"; exit 1; fi ;; \
		404|410) ;; \
		*) rm -f "$$list"; echo "$(RED)[ERROR]$(NC) ${GO_PROXY_URL} answered HTTP $$code for the version list"; exit 1 ;; \
	esac; \
	rm -f "$$list"
	@echo "$(GREEN)[SUCCESS]$(NC) ${ONDEWO_VTSI_VERSION} / ${GO_RELEASE_TAG} exist neither on origin nor on the module proxy"

check_release_notes: ## Assert RELEASE.md has a non-empty '## Release ONDEWO VTSI Go Client <version>' section
# `gh release create -n ""` succeeds and publishes an empty release, so an entry that was forgotten
# (or a heading whose wording drifted away from what CURRENT_RELEASE_NOTES matches) is otherwise
# only noticed by whoever reads the release page afterwards. A heading followed only by the
# separator is refused too.
	@notes="$(CURRENT_RELEASE_NOTES)"; \
	body=`printf '%s\n' "$$notes" | sed '1d' | grep -v '^\*\{5\}' | tr -d '[:space:]'`; \
	if [ -z "$$notes" ] || [ -z "$$body" ]; then \
		echo "$(RED)[ERROR]$(NC) RELEASE.md has no non-empty '## Release ONDEWO VTSI Go Client ${ONDEWO_VTSI_VERSION}' section"; \
		echo "        The GitHub release would be created with empty notes - add the entry first."; \
		exit 1; \
	fi; \
	echo "$(GREEN)[SUCCESS]$(NC) RELEASE.md has release notes for ${ONDEWO_VTSI_VERSION}"

install_gh: ## Download the pinned gh (GH_VERSION) into GH_CACHE_DIR and verify its sha256 - no host gh needed
	@[ ! -x "$(GH)" ] || exit 0; \
	set -e; \
	case "`uname -m`" in \
		x86_64|amd64) arch=amd64; sum=${GH_SHA256_amd64} ;; \
		aarch64|arm64) arch=arm64; sum=${GH_SHA256_arm64} ;; \
		*) echo "$(RED)[ERROR]$(NC) no pinned gh for `uname -m`"; exit 1 ;; \
	esac; \
	tmp=`mktemp -d`; \
	trap 'rm -rf "$$tmp"' EXIT INT TERM; \
	echo "$(BLUE)[INFO]$(NC) Downloading gh ${GH_VERSION} ($$arch) ..."; \
	curl -fsSL --retry 3 -o "$$tmp/gh.tar.gz" "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_$$arch.tar.gz"; \
	echo "$$sum  $$tmp/gh.tar.gz" | sha256sum -c --quiet -; \
	tar -xzf "$$tmp/gh.tar.gz" -C "$$tmp"; \
	mkdir -p "$(GH_CACHE_DIR)/bin"; \
	cp "$$tmp/gh_${GH_VERSION}_linux_$$arch/bin/gh" "$(GH).partial"; \
	chmod 0755 "$(GH).partial"; \
	mv "$(GH).partial" "$(GH)"; \
	echo "$(GREEN)[SUCCESS]$(NC) gh ${GH_VERSION} verified and installed at $(GH)"

build_gh_release: check_release_notes install_gh ## Create the GitHub release (pinned gh, token per call) - refuses empty notes and a missing tag
	@$(LOAD_RELEASE_CREDENTIALS); \
	printf '%s\n' "$(CURRENT_RELEASE_NOTES)" | $(GH_RUN) release create --repo $(GH_REPO) "$(ONDEWO_VTSI_VERSION)" \
		--verify-tag --notes-file - -t "Release ${ONDEWO_VTSI_VERSION}"

push_to_gh: check_release_credentials build_gh_release ## Create the GitHub release
	@echo "$(GREEN)[SUCCESS]$(NC) Released to Github"

# The credential-free rehearsal of the publication, and the reason this repository needs no registry
# account: a go module is never uploaded. proxy.golang.org clones the tag and serves a zip of exactly
# what git has under it, so the whole publishing surface can be reproduced locally - and must be,
# because every way of getting it wrong (a gitignored ${STUBS_DIR}/, a module path whose /vN
# disagrees with the tag, a self-import spelled without the suffix) is invisible until a consumer's
# `go get` fails, long after the tag has been pushed and can no longer be moved.
#
# It packs `git archive HEAD` rather than the working tree ON PURPOSE - the proxy only ever sees
# committed files - and then resolves the result under its real version through a file:// GOPROXY,
# which is what makes this a rehearsal of `go get` instead of another local build.
#
# Every go command of the rehearsal runs on a THROWAWAY module cache (GOMODCACHE inside the mktemp
# work dir), never the caller's. Go caches a downloaded module version once and for all, and the
# rehearsed zip does not hash like the one proxy.golang.org builds from the tag, so with the shared
# cache a later real `go get ${GO_MODULE_PATH}@${GO_RELEASE_TAG}` on the same machine reads the
# rehearsed copy and fails with a checksum SECURITY ERROR against sum.golang.org. Go writes its
# cache read-only, hence -modcacherw and the `chmod -R u+w` before the cleanup. The build cache
# (GOCACHE) stays shared: it is keyed by content hashes, so nothing in it can stand in for a
# published module version.
publish_dry_run: check_stubs check_go_module_path ## Pack the committed tree exactly as the module proxy would and install it as an outside consumer (no credentials)
ifeq ($(HOST_GO),)
	$(GO_DOCKER_RUN) $@
else
	@git diff --quiet HEAD -- ${STUBS_DIR} go.mod go.sum || \
		echo "$(YELLOW)[WARN]$(NC) ${STUBS_DIR}/, go.mod or go.sum differ from HEAD - the rehearsal packs HEAD, not your working tree"
	@echo "$(BLUE)[INFO]$(NC) Packing ${GO_MODULE_PATH}@${GO_RELEASE_TAG} out of the committed tree ..."
	@set -e; \
	work=`mktemp -d`; \
	trap 'chmod -R u+w "$$work" 2>/dev/null; rm -rf "$$work"' EXIT INT TERM; \
	export GOMODCACHE="$$work/modcache"; \
	versions="$$work/proxy/${GO_MODULE_PATH}/@v"; \
	mkdir -p "$$versions"; \
	git archive --format=zip --prefix="${GO_MODULE_PATH}@${GO_RELEASE_TAG}/" HEAD > "$$versions/${GO_RELEASE_TAG}.zip"; \
	git show HEAD:go.mod > "$$versions/${GO_RELEASE_TAG}.mod"; \
	printf '{"Version":"%s","Time":"%s"}\n' "${GO_RELEASE_TAG}" "`git show -s --format=%cI HEAD`" > "$$versions/${GO_RELEASE_TAG}.info"; \
	printf '%s\n' "${GO_RELEASE_TAG}" > "$$versions/list"; \
	first_package=`go list ./${STUBS_DIR}/... | head -1`; \
	echo "$(BLUE)[INFO]$(NC) Installing it into a throwaway consumer module, importing $$first_package ..."; \
	mkdir -p "$$work/consumer"; \
	cd "$$work/consumer"; \
	go mod init ondewo.local/publish-dry-run >/dev/null; \
	printf 'package main\n\nimport (\n\t_ "%s"\n\t_ "%s/auth"\n)\n\nfunc main() {}\n' \
		"$$first_package" "${GO_MODULE_PATH}" > main.go; \
	go mod edit -require="${GO_MODULE_PATH}@${GO_RELEASE_TAG}"; \
	export GOFLAGS="-mod=mod -modcacherw"; \
	export GONOSUMDB="${GO_MODULE_BASE_PATH}"; \
	export GOPROXY="file://$$work/proxy,$${GOPROXY:-https://proxy.golang.org,direct}"; \
	go mod tidy; \
	go build ./...; \
	resolved=`go list -m ${GO_MODULE_PATH}`; \
	echo "$(GREEN)[SUCCESS]$(NC) an outside consumer resolved and compiled against $$resolved"
endif
# The three settings are exported rather than prefixed onto `go mod tidy` alone, because every go
# command after it resolves the module too. The checksum database is switched off for THIS module
# only (GONOSUMDB) - the version being rehearsed is by definition not in sum.golang.org yet - and
# the rehearsal proxy is the FIRST element of the GOPROXY list, so everything else (the module's own
# dependencies) still comes from wherever the caller's GOPROXY points.

publish_go_module: ## Ask the public go module proxy to fetch the pushed tag, with retries (a go module has no registry upload - the tag IS the release)
ifeq ($(HOST_GO),)
	$(GO_DOCKER_RUN) $@
else
	@echo "$(BLUE)[INFO]$(NC) Requesting ${GO_MODULE_PATH}@${GO_RELEASE_TAG} from proxy.golang.org ..."
# The proxy fetches the tag from GitHub on demand; a fresh tag can be briefly unknown to it, so the
# request is retried rather than failing the release one step after the tags were pushed.
	@for attempt in 1 2 3 4 5 6; do \
		if GOPROXY=${GO_PROXY_URL} GOFLAGS= go list -m ${GO_MODULE_PATH}@${GO_RELEASE_TAG}; then \
			echo "$(GREEN)[SUCCESS]$(NC) ${GO_MODULE_PATH}@${GO_RELEASE_TAG} is served by the module proxy"; \
			exit 0; \
		fi; \
		echo "$(YELLOW)[WARN]$(NC) attempt $$attempt/6: the proxy does not serve ${GO_RELEASE_TAG} yet, retrying in 30s"; \
		sleep 30; \
	done; \
	echo "$(RED)[ERROR]$(NC) proxy.golang.org did not fetch ${GO_MODULE_PATH}@${GO_RELEASE_TAG}"; \
	exit 1
endif

verify_go_module_served: ## Read-only: poll proxy.golang.org until it serves the version's .info AND module zip
	@info="${GO_PROXY_URL}/${GO_MODULE_PATH}/@v/${GO_RELEASE_TAG}.info"; \
	zip="${GO_PROXY_URL}/${GO_MODULE_PATH}/@v/${GO_RELEASE_TAG}.zip"; \
	attempt=1; \
	while :; do \
		version=`curl -fsS "$$info" 2>/dev/null | perl -ne 'print $$1 if /"Version":"([^"]+)"/'`; \
		if [ "$$version" = "${GO_RELEASE_TAG}" ] && curl -fsS -o /dev/null "$$zip" 2>/dev/null; then \
			echo "$(GREEN)[SUCCESS]$(NC) proxy.golang.org serves ${GO_MODULE_PATH}@${GO_RELEASE_TAG} (.info and module zip)"; \
			exit 0; \
		fi; \
		if [ $$attempt -ge $(RELEASE_VERIFY_ATTEMPTS) ]; then \
			echo "$(RED)[ERROR]$(NC) proxy.golang.org does not serve ${GO_RELEASE_TAG} after $(RELEASE_VERIFY_ATTEMPTS) attempts ($$info)"; \
			exit 1; \
		fi; \
		echo "$(YELLOW)[WARN]$(NC) attempt $$attempt/$(RELEASE_VERIFY_ATTEMPTS): ${GO_RELEASE_TAG} not served yet, retrying in $(RELEASE_VERIFY_INTERVAL_S)s"; \
		attempt=`expr $$attempt + 1`; \
		sleep $(RELEASE_VERIFY_INTERVAL_S); \
	done

release_verify: check_release_credentials install_gh ## Read-only: fail unless both tags are on origin, the GitHub release has a non-empty body and the proxy serves the module
	@refs=`git ls-remote origin "refs/tags/${ONDEWO_VTSI_VERSION}" "refs/tags/${GO_RELEASE_TAG}" | cut -f2`; \
	for tag in ${ONDEWO_VTSI_VERSION} ${GO_RELEASE_TAG}; do \
		printf '%s\n' "$$refs" | grep -Fxq "refs/tags/$$tag" \
			|| { echo "$(RED)[ERROR]$(NC) tag $$tag is not on origin"; exit 1; }; \
	done
	@echo "$(GREEN)[SUCCESS]$(NC) tags ${ONDEWO_VTSI_VERSION} and ${GO_RELEASE_TAG} are on origin"
	@$(LOAD_RELEASE_CREDENTIALS); \
	body=`$(GH_RUN) release view "${ONDEWO_VTSI_VERSION}" --repo $(GH_REPO) --json body --jq .body` \
		|| { echo "$(RED)[ERROR]$(NC) GitHub release ${ONDEWO_VTSI_VERSION} cannot be read"; exit 1; }; \
	if [ -z "`printf '%s' "$$body" | tr -d '[:space:]'`" ]; then \
		echo "$(RED)[ERROR]$(NC) GitHub release ${ONDEWO_VTSI_VERSION} has an empty body"; \
		exit 1; \
	fi
	@echo "$(GREEN)[SUCCESS]$(NC) GitHub release ${ONDEWO_VTSI_VERSION} has release notes"
	@$(MAKE) --no-print-directory verify_go_module_served

########################################################
#		DEVOPS-ACCOUNTS

ondewo_release: spc run_release_with_devops ## Release with credentials from devops-accounts repo

# ONE shell, so that the clone is deleted (a) right after the token is read - before `$(MAKE) release`
# starts, so no container, test or hook of the release can ever see the account files - and (b) on
# every failure, by the trap. The clone is made in a fresh mktemp directory OUTSIDE this repository.
# A DEVOPS_ACCOUNT_DIR from the environment (the release driver's single clone, absolute path) is
# read in place and left alone: its owner deletes it.
# The token is loaded into the environment of the sub-make (anchored ^NAME=, so a comment line naming
# the variable cannot break the value) and never passed as `make release NAME=<value>`: make's argv,
# like every /proc/<pid>/cmdline, is world-readable.
# A line holding $(MAKE) runs even under `make -n`, so a dry run must not clone the credentials.
run_release_with_devops: ## Gets Credentials from devops-repo and run release command with them
	@set -e; \
	clone=""; \
	trap '[ -z "$$clone" ] || rm -rf "$$clone"' EXIT INT TERM; \
	if [ -n "$(MAKE_DRY_RUN)" ]; then \
		accounts=""; \
		echo "$(BLUE)[INFO]$(NC) dry run (make -n): the devops accounts are neither cloned nor read"; \
	elif [ -n "$$DEVOPS_ACCOUNT_DIR" ] && [ -d "$$DEVOPS_ACCOUNT_DIR" ]; then \
		accounts="$$DEVOPS_ACCOUNT_DIR"; \
		echo "$(BLUE)[INFO]$(NC) reading the credentials from DEVOPS_ACCOUNT_DIR=$$DEVOPS_ACCOUNT_DIR (not cloned, not deleted here)"; \
	else \
		clone=`mktemp -d`; \
		accounts="$$clone/ondewo-devops-accounts"; \
		git clone --quiet --depth 1 "${DEVOPS_ACCOUNT_GIT_URL}" "$$accounts"; \
	fi; \
	set -a; \
	[ -z "$$accounts" ] || eval "`grep -h -E '^(GITHUB_GH_TOKEN)=' "$$accounts/account_github.env"`"; \
	set +a; \
	if [ -n "$$clone" ]; then rm -rf "$$clone"; clone=""; echo "$(BLUE)[INFO]$(NC) devops-accounts clone deleted before the release starts"; fi; \
	$(MAKE) release

spc: ## Checks (anchored, local refs) that the Release Branch and the two Release Tags do not exist yet
	@if { git for-each-ref --format='%(refname:lstrip=2)' refs/heads; git for-each-ref --format='%(refname:lstrip=3)' refs/remotes; } \
		| grep -Fxq "release/${ONDEWO_VTSI_VERSION}"; then echo "-- Test 1: Branch exists!!" && exit 1; else echo "-- Test 1: Branch is fine"; fi
	@if git rev-parse -q --verify "refs/tags/${ONDEWO_VTSI_VERSION}" >/dev/null; then echo "-- Test 2: Tag exists!!" && exit 1; else echo "-- Test 2: Tag is fine"; fi
	@if git rev-parse -q --verify "refs/tags/${GO_RELEASE_TAG}" >/dev/null; then echo "-- Test 3: Go tag exists!!" && exit 1; else echo "-- Test 3: Go tag is fine"; fi
