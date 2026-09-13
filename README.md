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
go get github.com/ondewo/ondewo-vtsi-client-go
```

Then import the package of the service you need:

```go
import vtsipb "github.com/ondewo/ondewo-vtsi-client-go/api/ondewo/vtsi"
```

> **Major versions.** From major version 2 on, a Go module path carries its major version as a
> `/vN` suffix (see [the module reference](https://go.dev/ref/mod#major-version-suffixes)), so the
> import path of release `7.1.2` is `github.com/ondewo/ondewo-vtsi-client-go/v7/api/ondewo/vtsi`. The
> `Makefile` derives the suffix from `ONDEWO_VTSI_VERSION`; run `make TEST` to print the
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
    "google.golang.org/grpc/metadata"

    vtsipb "github.com/ondewo/ondewo-vtsi-client-go/api/ondewo/vtsi"
)

func main() {
    conn, err := grpc.NewClient(
        "grpc-vtsi.ondewo.com:443",
        grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})),
    )
    if err != nil {
        log.Fatalf("could not connect: %v", err)
    }
    defer conn.Close()

    // Credentials travel as request metadata, exactly as in the other ONDEWO clients.
    bearerToken := os.Getenv("ONDEWO_VTSI_ACCESS_TOKEN")
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+bearerToken)

    // Every service of the API has a generated New<Service>Client constructor. Browse
    // api/ondewo/vtsi/ for the ones this product exposes.
    client := vtsipb.NewExampleServiceClient(conn)

    response, err := client.ExampleMethod(ctx, &vtsipb.ExampleRequest{})
    if err != nil {
        log.Fatalf("rpc failed: %v", err)
    }
    log.Printf("response: %v", response)
}
```

## Repository structure

```
.
├── api                                    <----- GENERATED - do not edit, `make generate_ondewo_protos` rewrites it
│   └── ondewo
│       └── vtsi
│           ├── *.pb.go                    <----- messages (protoc-gen-go)
│           └── *_grpc.pb.go               <----- service stubs (protoc-gen-go-grpc)
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
