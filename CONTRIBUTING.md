# How to become a contributor and submit your own code

## Contributor License Agreements

We'd love to accept your sample apps and patches! Before we can take them, we have to jump a couple of legal
hurdles.

Please fill out either the individual or corporate Contributor License Agreement (CLA).

* If you are an individual writing original source code and you're sure you own the intellectual property,
  then you'll need to sign an individual CLA.
* If you work for a company that wants to allow you to contribute your work, then you'll need to sign a
  corporate CLA.

Contact <office@ondewo.com> to receive the appropriate CLA and instructions for how to sign and return it.
Once we receive it, we'll be able to accept your pull requests.

## Contributing A Patch

1. Submit an issue describing your proposed change to the repo in question.
1. The repo owner will respond to your issue promptly.
1. If your proposed change is accepted, and you haven't already done so, sign a
   Contributor License Agreement (see details above).
1. Fork the desired repo, develop and test your code changes.
1. Ensure that your code adheres to the existing style in the sample to which
   you are contributing. Run `make fmt` and `make vet` before you push.
1. Ensure that your code has an appropriate set of unit tests which all pass
   (`make test`).
1. Submit a pull request.

## What you may and may not edit

This repository is roughly 95% generated code.

* **`api/` is generated and must never be edited by hand.** It is rewritten from scratch by
  `make generate_ondewo_protos`, which wipes the directory first — any change made there is lost on
  the next generation, silently. A change to the *API surface* belongs in
  [ondewo-vtsi-api](https://github.com/ondewo/ondewo-vtsi-api); a change to *how* the stubs are generated
  belongs in [ondewo-proto-compiler](https://github.com/ondewo/ondewo-proto-compiler).
* **`ondewo-vtsi-api/` and `ondewo-proto-compiler/` are submodules.** Do not commit changes inside them
  from here. Change them in their own repository, release them, then move the pin in the `Makefile`
  (`ONDEWO_VTSI_API_GIT_BRANCH`, `ONDEWO_PROTO_COMPILER_GIT_BRANCH`) and commit the new
  submodule commit.
* **Everything else is hand written** and is where a fix or a convenience wrapper belongs. Keep it
  outside `api/`.

## Local setup

```shell
make setup_developer_environment_locally    ## submodules + pre-commit hooks
make build                                  ## compiler image, stub generation, go build
make test                                   ## go test
```

## Commit messages

Commits follow [Conventional Commits](https://www.conventionalcommits.org/) (`feat: …`,
`fix(scope): …`, `docs: …`); the `conventional-pre-commit` hook enforces the subject line. The
JIRA ticket prefix is added automatically by the `giticket` hook from the branch name — do not
write it yourself, or the subject ends up with the ticket twice.

## Versioning

`ONDEWO_VTSI_VERSION` in the `Makefile` is the single source of truth, and it **must
match the ONDEWO VTSI API in major and minor version**. Add a `RELEASE.md` entry for
every version you cut, in the format the file already uses — `make build_gh_release` slices the
GitHub release notes out of it by matching the heading and the `*****` separator.
