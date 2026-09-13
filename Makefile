export

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
# -------------- Release Process Steps --------------
# 1 - Get Credentials from devops-accounts repo
# 2 - Create Release Branch and push
# 3 - Create Release Tag and push (BOTH the ONDEWO tag and the go `vX.Y.Z` tag)
# 4 - GitHub Release
# 5 - Go module release (warm the public module proxy - a go module has no registry upload)

########################################################
# 		Variables
########################################################

# MUST BE THE SAME AS API in Major and Minor Version Number
# example: API 2.9.0 --> Client 2.9.X
ONDEWO_VTSI_VERSION=8.7.0

# Submodule pins - `make checkout_defined_submodule_versions` checks these out
ONDEWO_VTSI_API_GIT_BRANCH=tags/8.7.0
ONDEWO_PROTO_COMPILER_GIT_BRANCH=tags/5.15.0

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
# The fixed tag is the ONLY contract between this repo and the compiler submodule
PROTO_COMPILER_IMAGE=ondewo-go-proto-compiler:latest

# --- Go module identity
# Go resolves a module straight from its VCS path, and from major version 2 on that path has to
# carry the major as a `/vN` suffix (https://go.dev/ref/mod#major-version-suffixes): a v7.1.2
# tag on a module declared without `/v7` is invisible to `go get`. Derived from the version
# above so the module path, the go.mod the compiler renders and the import path baked by
# protoc-gen-go into every generated file all move together.
GO_MODULE_MAJOR=$(firstword $(subst ., ,$(ONDEWO_VTSI_VERSION)))
GO_MODULE_MAJOR_SUFFIX=$(if $(filter 0 1,$(GO_MODULE_MAJOR)),,/v$(GO_MODULE_MAJOR))
GO_MODULE_PATH=github.com/ondewo/ondewo-vtsi-client-go$(GO_MODULE_MAJOR_SUFFIX)
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

# --- Coverage
# The threshold is enforced over the HAND-WRITTEN packages only. Everything below ${STUBS_DIR}/ is
# machine output: gating on it would measure how much of protoc's output a test happens to walk,
# not how much of what somebody wrote is tested. The stubs are still exercised for real - the
# suite round-trips messages on the wire and calls every generated unary stub - it is only the
# NUMBER they are kept out of. `make test_coverage_generated` prints their figure for the record.
COVERAGE_PACKAGES=./auth/...
COVERAGE_THRESHOLD=100.0
COVERAGE_PROFILE=coverage.out

# Terminate on the ***** separator that delimits release entries, NOT on /\*\*/ - that matches
# the first markdown **bold** span inside the entry and silently truncates the notes there,
# with no error from `gh release create`.
CURRENT_RELEASE_NOTES=`cat RELEASE.md \
	| perl -ne 'print if /Release ONDEWO VTSI Go Client ${ONDEWO_VTSI_VERSION}/../^\*{5}/'`

GH_REPO="https://github.com/ondewo/ondewo-vtsi-client-go"
DEVOPS_ACCOUNT_GIT="ondewo-devops-accounts"
DEVOPS_ACCOUNT_DIR="./${DEVOPS_ACCOUNT_GIT}"

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
	@echo "GH Token: \t $(if $(GITHUB_GH_TOKEN),<set>,<unset>)"
	@echo "Go Module Path: \t $(GO_MODULE_PATH)"
	@echo "Go Release Tag: \t $(GO_RELEASE_TAG)"
	@echo "Compiler Image: \t $(PROTO_COMPILER_IMAGE)"

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

build_compiler: ## Build the proto compiler docker image from the pinned submodule
	@echo "$(BLUE)[INFO]$(NC) Building ${PROTO_COMPILER_IMAGE} from ${ONDEWO_PROTO_COMPILER_DIR}/go ..."
	cd ${ONDEWO_PROTO_COMPILER_DIR}/go && sh build.sh
	@echo "$(GREEN)[SUCCESS]$(NC) Built ${PROTO_COMPILER_IMAGE}"

generate_ondewo_protos: ## Generate the go stubs from the .proto files of the ondewo-vtsi-api submodule
	@[ -d ${ONDEWO_PROTOS_DIR} ] || { \
		echo "$(RED)[ERROR]$(NC) ${ONDEWO_PROTOS_DIR} does not exist - run 'make update_submodules' first"; \
		exit 1; \
	}
	@echo "$(BLUE)[INFO]$(NC) Generating go stubs into ${STUBS_DIR}/ for module ${GO_MODULE_PATH} ..."
# The mounts and the positional arguments are the contract of the image, taken verbatim from
#	ondewo-proto-compiler/go/example/run-compile.sh:
#	  <relative_protos_dir>  directory BELOW the input volume that is protoc's -I root
#	  <target_sub_directory> sub-directory of that root to compile; quoted, because the module
#	                         path follows it and must not slide into its place when it is empty
#	  <go_module_path>       protoc-gen-go bakes it into every generated file, so it has to be
#	                         known before protoc runs
# The input volume is this repository (it holds the api submodule); the output volume is this
#	repository too, because a go module IS its directory tree - the stubs have to land under
#	${STUBS_DIR}/ of the module named by <go_module_path>.
# NOTE: no -it. A TTY-enabled container breaks every non-interactive caller with
#	"cannot attach stdin to a TTY-enabled container because stdin is not a terminal".
# No --user either: GOPATH=/go and GOCACHE=/go/build-cache are root-owned inside the image, so
#	the `go build ./...` that type checks the generated stubs has to run as root - which leaves
#	the output root-owned, hence the `fix_ownership` below.
	docker run \
		-v ${shell pwd}:/input-volume \
		-v ${shell pwd}:/output-volume \
		${PROTO_COMPILER_IMAGE} ${ONDEWO_API_DIR} "${PROTOS_TARGET_DIR}" "${GO_MODULE_PATH}"
	-make fix_ownership
	@echo "$(GREEN)[SUCCESS]$(NC) Generated go stubs in ${STUBS_DIR}/"

fix_ownership: ## Give the root-owned output of the proto compiler back to the current user
	@for f in $$(find . -maxdepth 2 -user 0 ! -path "./.git/*"); \
	do \
		sudo chown -R $$(id -u):$$(id -g) "$$f" && echo "$(BLUE)[INFO]$(NC) chowned $$f"; \
	done

go_build: ## Compile every package of the module that has non-test sources
	go build $(GO_BUILD_PACKAGES)

test: ## Run the go test suite
	go test $(GO_PACKAGES)

test_coverage: ## Run the suite under the race detector and fail if hand-written coverage < COVERAGE_THRESHOLD
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

test_coverage_generated: ## Print how much of the GENERATED stubs the suite exercises (reported, never gated)
	go test -count=1 -coverpkg=./${STUBS_DIR}/... -coverprofile=generated-$(COVERAGE_PROFILE) $(GO_PACKAGES)
	@go tool cover -func=generated-$(COVERAGE_PROFILE) | tail -1

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

vet: ## Run go vet over the hand-written packages and the generated stubs
	go vet $(GO_PACKAGES)

# Both gofmt targets guard on an empty file list: gofmt with no arguments reads STDIN, which
# in CI is a hang, not an error. The list is empty in exactly one situation - a fresh clone
# before the first `make generate_ondewo_protos`.
fmt: ## Format the hand-written go sources in place
	@sources=$(GO_SOURCES); \
	if [ -z "$$sources" ]; then \
		echo "$(YELLOW)[WARN]$(NC) no go sources yet - run 'make generate_ondewo_protos' first"; \
		exit 0; \
	fi; \
	gofmt -l -w $$sources

fmt_check: ## Fail if any go source is not gofmt-clean (prints the offenders)
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

release: ## Automate the entire release process
	@echo "$(BLUE)[INFO]$(NC) Start Release"
	make build
	make check_build
	-make precommit_hooks_run_all_files
	git status
	git add ${STUBS_DIR}
	git add Makefile
	git add README.md
	git add RELEASE.md
# go.mod / go.sum are the manifest of the published module: a release whose tag does not carry
# them is not installable. go.sum only exists once the module has been resolved, so it is
# staged leniently.
	git add go.mod
	-git add go.sum
	git add ${ONDEWO_PROTO_COMPILER_DIR}
	git add ${ONDEWO_API_DIR}
	git status
	-git commit --no-verify -m "PREPARING FOR RELEASE ${ONDEWO_VTSI_VERSION}"
	git push
	make create_release_branch
	make create_release_tag
	make push_to_gh
	make publish_go_module
	@echo "$(GREEN)[SUCCESS]$(NC) Release Finished"

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

login_to_gh: ## Login to Github CLI with Access Token
	@echo $(GITHUB_GH_TOKEN) | gh auth login -p ssh --with-token

build_gh_release: ## Generate Github Release with CLI
	gh release create --repo $(GH_REPO) "$(ONDEWO_VTSI_VERSION)" -n "$(CURRENT_RELEASE_NOTES)" -t "Release ${ONDEWO_VTSI_VERSION}"

push_to_gh: login_to_gh build_gh_release ## Logs into GitHub CLI and Releases
	@echo "$(GREEN)[SUCCESS]$(NC) Released to Github"

publish_go_module: ## Ask the public go module proxy to fetch the pushed tag (a go module has no registry upload - the tag IS the release)
	@echo "$(BLUE)[INFO]$(NC) Requesting ${GO_MODULE_PATH}@${GO_RELEASE_TAG} from proxy.golang.org ..."
	GOPROXY=https://proxy.golang.org GOFLAGS= go list -m ${GO_MODULE_PATH}@${GO_RELEASE_TAG}
	@echo "$(GREEN)[SUCCESS]$(NC) ${GO_MODULE_PATH}@${GO_RELEASE_TAG} is served by the module proxy"

########################################################
#		DEVOPS-ACCOUNTS

ondewo_release: spc clone_devops_accounts run_release_with_devops ## Release with credentials from devops-accounts repo
	@rm -rf ${DEVOPS_ACCOUNT_GIT}

clone_devops_accounts: ## Clones devops-accounts repo
	if [ -d $(DEVOPS_ACCOUNT_GIT) ]; then rm -Rf $(DEVOPS_ACCOUNT_GIT); fi
	git clone git@bitbucket.org:ondewo/${DEVOPS_ACCOUNT_GIT}.git

run_release_with_devops: ## Gets Credentials from devops-repo and run release command with them
	$(eval info:= $(shell cat ${DEVOPS_ACCOUNT_DIR}/account_github.env | grep GITHUB_GH))
	@make release $(info)

spc: ## Checks if the Release Branch and the two Release Tags already exist
	$(eval filtered_branches:= $(shell git branch --all | grep "release/${ONDEWO_VTSI_VERSION}"))
	$(eval filtered_tags:= $(shell git tag --list | grep "^${ONDEWO_VTSI_VERSION}$$"))
	$(eval filtered_go_tags:= $(shell git tag --list | grep "^${GO_RELEASE_TAG}$$"))
	@if test "$(filtered_branches)" != ""; then echo "-- Test 1: Branch exists!!" & exit 1; else echo "-- Test 1: Branch is fine";fi
	@if test "$(filtered_tags)" != ""; then echo "-- Test 2: Tag exists!!" & exit 1; else echo "-- Test 2: Tag is fine";fi
	@if test "$(filtered_go_tags)" != ""; then echo "-- Test 3: Go tag exists!!" & exit 1; else echo "-- Test 3: Go tag is fine";fi
