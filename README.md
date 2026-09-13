<div align="center">
  <table>
    <tr>
      <td>
        <a href="https://ondewo.com/">
            <img width="400px" src="https://raw.githubusercontent.com/ondewo/ondewo-logos/master/ondewo_we_automate_your_phone_calls.png"/>
        </a>
      </td>
    </tr>
    <tr>
       <td align="center">
          <a href="https://www.linkedin.com/company/ondewo"><img width="40px" src="https://cdn-icons-png.flaticon.com/512/3536/3536505.png"></a>
          <a href="https://www.facebook.com/ondewo"><img width="40px" src="https://cdn-icons-png.flaticon.com/512/733/733547.png"></a>
          <a href="https://twitter.com/ondewo"><img width="40px" src="https://cdn-icons-png.flaticon.com/512/733/733579.png"></a>
          <a href="https://www.instagram.com/ondewo.ai/"><img width="40px" src="https://cdn-icons-png.flaticon.com/512/174/174855.png"></a>
       </td>
    </tr>
  </table>
  <h1 align="center">
    ONDEWO VTSI Client Go
  </h1>
</div>

## Overview

`ondewo-vtsi-client-go` is the Go gRPC client of the ONDEWO VTSI (Virtual Telephony Server Interface) API. It is a
compiled version of the [ONDEWO VTSI API](https://github.com/ondewo/ondewo-vtsi-api),
generated with the [ONDEWO PROTO COMPILER](https://github.com/ondewo/ondewo-proto-compiler).

ONDEWO APIs use [Protocol Buffers](https://github.com/protocolbuffers/protobuf) version 3 (proto3)
as their Interface Definition Language (IDL) to define the API interface and the structure of the
payload messages. The same interface definition is used for the gRPC version of the API in all
languages, so the service and message names below are the ones documented for the API itself.

Everything under `api/` is **generated**. It is nevertheless committed to this repository, because a
Go module is resolved straight from its version control tree — there is no build step between
`go get` and the consumer's compiler. Hand-written code therefore lives *outside* `api/`.

## Installation

```shell
go get github.com/ondewo/ondewo-vtsi-client-go/v8
```

Then import the package of the service you need:

```go
import vtsipb "github.com/ondewo/ondewo-vtsi-client-go/v8/api/ondewo/vtsi"
```

> **Major versions.** From major version 2 on, a Go module path carries its major version as a
> `/vN` suffix (see [the module reference](https://go.dev/ref/mod#major-version-suffixes)): a
> `v8.y.z` tag on a module declared without `/v8` is invisible to `go get`. This release is
> `8.7.0`, so the module declares — and every generated file imports — `github.com/ondewo/ondewo-vtsi-client-go/v8`.
> The `Makefile` derives the suffix from `ONDEWO_VTSI_VERSION`; run `make TEST` to print the
> exact module path of the current release.

To work on the client itself:

```shell
git clone https://github.com/ondewo/ondewo-vtsi-client-go.git   ## Clone the repository
cd ondewo-vtsi-client-go                                        ## Change into the repo directory
make setup_developer_environment_locally              ## Check out submodules, install pre-commit hooks
```

## Usage

```go
package main

import (
    "context"
    "crypto/tls"
    "log"
    "os"
    "time"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials"

    vtsipb "github.com/ondewo/ondewo-vtsi-client-go/v8/api/ondewo/vtsi"
    "github.com/ondewo/ondewo-vtsi-client-go/v8/auth"
)

func main() {
    // auth.WithBearerToken sends the Keycloak access token as `authorization: Bearer <token>` on
    // every call of this connection, exactly as the other ONDEWO clients do. It refuses to attach
    // itself to a plaintext connection, so it is paired with transport credentials here.
    conn, err := grpc.NewClient(
        "grpc-vtsi.ondewo.com:443",
        grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})),
        auth.WithBearerToken(os.Getenv("ONDEWO_VTSI_ACCESS_TOKEN")),
    )
    if err != nil {
        log.Fatalf("could not connect: %v", err)
    }
    defer conn.Close()

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    // Every service of the API has a generated New<Service>Client constructor. Browse
    // api/ondewo/vtsi/ for this product's own three services, and
    // api/ondewo/{nlu,qa,s2t,sip,t2s}/ for the 20 vendored ones this client also exposes.
    client := vtsipb.NewProjectsClient(conn)

    response, err := client.ListVtsiProjects(ctx, &vtsipb.ListVtsiProjectsRequest{})
    if err != nil {
        log.Fatalf("rpc failed: %v", err)
    }
    log.Printf("projects: %v", response.GetVtsiProjects())
}
```

## Repository structure

```
.
├── api                                    <----- GENERATED - do not edit, `make generate_ondewo_protos` rewrites it
│   └── ondewo                             <----- each package below holds *.pb.go (messages,
│       ├── vtsi                           <-----   protoc-gen-go) and *_grpc.pb.go (service
│       ├── nlu                            <-----   stubs, protoc-gen-go-grpc)
│       ├── qa                             <----- vtsi holds this product's own three services;
│       ├── s2t                            <-----   nlu, qa, s2t, sip and t2s are vendored by
│       ├── sip                            <-----   ondewo-vtsi-api and published from here too
│       └── t2s
├── auth                                   <----- HAND WRITTEN - the `authorization: Bearer` credential
├── tests                                  <----- HAND WRITTEN - the go test suite (see Testing below)
├── ondewo-vtsi-api                             <----- submodule @ https://github.com/ondewo/ondewo-vtsi-api
├── ondewo-proto-compiler                  <----- submodule @ https://github.com/ondewo/ondewo-proto-compiler
├── .github
│   └── workflows
│       └── ci.yml
├── CONTRIBUTING.md
├── go.mod                                 <----- module manifest, written by the compiler on the first run
├── go.sum
├── LICENSE
├── Makefile
├── README.md
└── RELEASE.md
```

## Regenerating the stubs

The generated code is produced by the `ondewo-go-proto-compiler` docker image, which is built from
the `ondewo-proto-compiler` submodule. The fixed image tag is the only contract between the two
repositories.

```shell
make update_submodules                      ## git submodule update --init --recursive
make checkout_defined_submodule_versions    ## check out the pins from the Makefile Variables chapter
make build_compiler                         ## build ondewo-go-proto-compiler:latest from the submodule
make generate_ondewo_protos                 ## generate api/ from ondewo-vtsi-api/ondewo
make check_build                            ## assert every .proto produced a *.pb.go
make go_build                               ## compile the module
```

`make build` runs the whole chain in that order.

A few properties of the generation worth knowing:

* The image copies the mounted input volume into a temporary directory inside the container and
  compiles there, so the protos of `ondewo-vtsi-api` are never modified.
* `api/` is **wiped** before every run, so a proto that was renamed or deleted upstream leaves no
  orphaned stub behind. Never put hand-written code below it.
* The Go import path is baked into every generated file by `protoc-gen-go`, so the module path is
  passed to the image as the third positional argument — it cannot be fixed up afterwards.
* `go.mod` and `go.sum` are written by the image **only when this repository has neither**. Once
  they are committed they are yours to maintain; the image prints the module requirements of the
  generated stubs at the end of every run so a drift is visible.
* Generation needs no network: every module the stubs are compiled against was pre-downloaded when
  the image was built.

## Testing

```shell
make check_stubs            ## assert the generated stubs are committed
make test                   ## go test over every package
make test_coverage          ## the same suite under -race, plus the hand-written coverage gate
make test_coverage_generated ## report (never gate) how much of api/ the suite exercises
```

The suite lives in `tests/` — never below `api/`, which is wiped on every regeneration — and needs
neither a network nor a running ONDEWO server. gRPC connections are made over an in-memory
`bufconn` listener, so a client stub, a server stub and a real HTTP/2 connection are exercised
in-process.

What it asserts about the **generated** code:

* `CallLogEntry` — a 64 bit scalar, a well-known `Timestamp`, a `bool`, two enums of the vtsi
  package and one imported from the vendored NLU protos — survives `proto.Marshal` →
  `proto.Unmarshal` unchanged, and a truncated payload is rejected;
* a proto3 `optional` scalar keeps its explicit presence — `ListCallLogsRequest.oldest_first`
  set to `false` is transmitted and arrives as a non-nil pointer, while an unset field stays
  `nil`. Both are meaningful requests here, and this is the distinction the angular target of
  the same compiler once lost, which made `false`/`0`/`""` unsendable;
* the enum zero values are pinned with their generated name/value maps: `LogSource(0)` is
  `LOG_SOURCE_UNSPECIFIED`, and `CallView(0)` is `MINIMUM` — a view callers really request, so
  a client that cannot transmit the enum zero value cannot ask for it at all;
* the `grpc.ServiceDesc` of every service (from `protoc-gen-go-grpc`) lists exactly the RPCs the
  proto descriptor of that service (from `protoc-gen-go`) does — the two plugins run separately and
  each half compiles on its own, so a disagreement is otherwise invisible;
* every generated `New<Service>Client` binds to a connection, and every generated **unary** stub is
  actually called over the wire and has to come back as `codes.Unimplemented` — 411 of them
  across the 23 services of this product, which proves each one marshals its request and builds a
  method name the transport accepts;
* an RPC answered by a fake server round-trips its response, and one the server leaves to the
  generated `Unimplemented*Server` base type reports `codes.Unimplemented`;
* all 25 compiled `.proto` files are registered in the global descriptor registry as proto3.

**Coverage.** The threshold (`COVERAGE_THRESHOLD` in the `Makefile`, currently **100%**) is
enforced over the hand-written packages only — `auth/` — because everything below `api/` is machine
output: gating on it would measure how much of protoc's output a test happens to walk. The stubs
are still exercised for real, as listed above; `make test_coverage_generated` prints their figure
(**15.8%** of generated statements at the time of writing) for the record. `make test_coverage`
also fails if it ends up measuring no hand-written function at all, so a deleted package cannot
turn the gate into a green no-op.

`.github/workflows/ci.yml` runs exactly these targets on `ubuntu-latest` against the go directive of
`go.mod` and the toolchain the compiler image generates with. It does **not** build the compiler
image or check out the submodules: it builds and tests the committed stubs, which is what a
consumer of the module gets.

## Release

The release is driven entirely by the `Makefile` — see `make help` for the full list of targets.

```shell
make ondewo_release                         ## credentials from the devops-accounts repo, then `make release`
```

`make release` builds, commits, creates the release branch, pushes **two** tags for the same commit
— the ONDEWO release tag (`7.1.2`) and the `v`-prefixed tag Go tooling requires (`v7.1.2`) — creates
the GitHub release from the matching `RELEASE.md` entry, and asks the public module proxy to fetch
the new version.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache License 2.0 — see [LICENSE](LICENSE).
