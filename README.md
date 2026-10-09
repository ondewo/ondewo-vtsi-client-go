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

The client is a plain Go module, published to the public module proxy
([`proxy.golang.org`](https://proxy.golang.org)) straight from this repository's release tags — there
is no registry account, no token and no `go install` step in between. Add it to your module with:

```shell
go get github.com/ondewo/ondewo-vtsi-client-go/v8@latest   ## or @v8.7.0 to pin an exact release
```

Then import the package of the service you need:

```go
import vtsipb "github.com/ondewo/ondewo-vtsi-client-go/v8/api/ondewo/vtsi"
```

> **The `/v8` is part of the name, not a version selector.** From major version 2 on, a Go module
> path carries its major version as a `/vN` suffix (see
> [the module reference](https://go.dev/ref/mod#major-version-suffixes)). Dropping it is not a
> shorter spelling of the same module — `go get github.com/ondewo/ondewo-vtsi-client-go` names a
> *different*, unpublished module and fails with `invalid version: module contains a go.mod file, so
> major version must be compatible`. The suffix moves with the major version of the ONDEWO VTSI API,
> so a `9.x` release will be imported as `/v9`, and a program can depend on both at once.

Releases are tagged twice on the same commit: with the ONDEWO release number (`8.7.0`), which is
what the [GitHub releases page](https://github.com/ondewo/ondewo-vtsi-client-go/releases) lists and
what the rest of the ONDEWO client fleet uses, and with the `v`-prefixed spelling (`v8.7.0`), which
is the only tag shape Go tooling recognises as a module version. Use the `v`-prefixed one in
`go get`, `go.mod` and anywhere else a version is written.

Nothing needs to be configured for a private proxy or a credential: the module is public, so the
default `GOPROXY=https://proxy.golang.org,direct` and `GOSUMDB=sum.golang.org` resolve and verify it
as they do any other dependency. `make TEST` prints the exact module path and release tag of the
checked-out version.

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
    "log"
    "os"
    "time"

    vtsipb "github.com/ondewo/ondewo-vtsi-client-go/v8/api/ondewo/vtsi"
    "github.com/ondewo/ondewo-vtsi-client-go/v8/auth"
    "github.com/ondewo/ondewo-vtsi-client-go/v8/client"
)

func main() {
    // client.NewChannel opens a TLS connection, verified against the system trust store, with the
    // ONDEWO connection defaults; see "TLS, mutual TLS and certificates" for a private CA and
    // mutual TLS. auth.WithBearerToken sends the Keycloak access token as
    // `authorization: Bearer <token>` on every call, exactly as the other ONDEWO clients do.
    conn, err := client.NewChannel(
        client.Config{Host: "grpc-vtsi.ondewo.com", Port: "443"},
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
    stub := vtsipb.NewProjectsClient(conn)

    response, err := stub.ListVtsiProjects(ctx, &vtsipb.ListVtsiProjectsRequest{})
    if err != nil {
        log.Fatalf("rpc failed: %v", err)
    }
    log.Printf("projects: %v", response.GetVtsiProjects())
}
```

## TLS, mutual TLS and certificates

gRPC encrypts with **TLS**. `client.NewChannel` opens the connection from a `client.Config`; it
behaves like the `ondewo-client-utils` channel helpers of the python SDKs, so one set of certificates
works with every ONDEWO client.

| Mode                                     | `client.Config` fields                                                   |
|------------------------------------------|--------------------------------------------------------------------------|
| Plaintext (not for production)           | `Insecure: true`                                                         |
| TLS, server verified by the system roots | `GrpcCert` empty                                                         |
| TLS, server verified by your CA          | `GrpcCert` = PEM of the CA that signed the server certificate            |
| Mutual TLS                               | `GrpcCert` (or the system roots) plus `GrpcClientCert` and `GrpcClientKey` |

Rules the code enforces:

* `GrpcCert`, `GrpcClientCert` and `GrpcClientKey` hold **PEM content**, **not file paths**. Read the
  files yourself (`os.ReadFile`). A `GrpcCert` that holds no PEM certificate, typically a path, is
  refused with an error.
* `GrpcClientCert` and `GrpcClientKey` go together: setting only one of them is refused by
  `NewChannel` with an error, before gRPC sees either. Both empty means plain server-authenticated
  TLS. A certificate and key that do not form a pair are refused the same way.
* `Insecure: true` with a client certificate is an error instead of silently dropping the identity.
  A plaintext connection otherwise works, and logs a warning naming `host:port` through `log/slog`
  (`Config.Logger`, or `slog.Default()` when it is nil; the package never configures logging).
* No error message contains a PEM, a key or the whole `Config`: they name the field and `host:port`.
* The server certificate is verified against the trust anchors above, and the host you connect to
  must be one of its subject alternative names (SAN). When you connect by IP and the certificate has
  no IP SAN, pass the name to check with `grpc.WithAuthority("<name in the SAN>")` (python:
  `grpc.ssl_target_name_override`).
* A bare IPv6 literal host is bracketed (`::1` connects to `[::1]:50051`); a bracketed host or one
  with a scheme (`dns:///...`, `unix:...`) is used as it is. PEMs with CRLF line endings work.

```go
package main

import (
    "log"
    "os"

    "google.golang.org/grpc"

    "github.com/ondewo/ondewo-vtsi-client-go/v8/auth"
    "github.com/ondewo/ondewo-vtsi-client-go/v8/client"
)

func mustRead(path string) string {
    content, err := os.ReadFile(path)
    if err != nil {
        log.Fatalf("could not read %s: %v", path, err)
    }
    return string(content)
}

func main() {
    conn, err := client.NewChannel(
        client.Config{
            Host:           "10.0.0.5",
            Port:           "50051",
            GrpcCert:       mustRead("certs/ca.pem"),
            GrpcClientCert: mustRead("certs/client.pem"), // leave both out for server-authenticated TLS
            GrpcClientKey:  mustRead("certs/client.key"),
        },
        grpc.WithAuthority("vtsi.example.internal"), // only when connecting by IP
        auth.WithBearerToken(os.Getenv("ONDEWO_VTSI_ACCESS_TOKEN")),
    )
    if err != nil {
        log.Fatalf("could not connect: %v", err)
    }
    defer conn.Close()
    // Every generated New<Service>Client takes this one connection: one TCP connection and one
    // TLS handshake for all services.
}
```

Like `grpc.NewClient`, `NewChannel` does not connect: the first RPC does, and a failed handshake
surfaces there as `codes.Unavailable`.

### Connection defaults

`NewChannel` applies the channel defaults of the python SDKs that grpc-go exposes; every
`grpc.DialOption` you pass is applied after them and wins.

| Setting                    | Value                                     | python / grpc-core option                                   |
|----------------------------|-------------------------------------------|-------------------------------------------------------------|
| keepalive ping interval    | 30 s (`client.KeepaliveTime`)             | `grpc.keepalive_time_ms`                                    |
| keepalive ping timeout     | 20 s (`client.KeepaliveTimeout`)          | `grpc.keepalive_timeout_ms` and `grpc.http2.ping_timeout_ms` |
| pings without an RPC       | no (`PermitWithoutStream: false`)         | `grpc.keepalive_permit_without_calls`                       |
| maximum reconnect backoff  | 5 s (`client.MaxReconnectBackoff`)        | `grpc.max_reconnect_backoff_ms`                             |
| maximum message size       | 2³¹−1 bytes, both directions (`client.MaxMessageLength`) | `grpc.max_send_message_length` / `grpc.max_receive_message_length` |

Gaps, documented rather than faked: grpc-go has no `http2.max_pings_without_data` (its client only
pings while an RPC is active, which is what that limit protects), and one keepalive timeout instead
of grpc-core's two. The per-method retry policy of the python SDKs is not configured here: only
gRPC's transparent retries apply, so a non-idempotent call is never sent twice. Pass
`grpc.WithDefaultServiceConfig(...)` to add one.

### A test PKI with openssl

A CA, a server certificate with SANs, and a client certificate with the `clientAuth` extended key
usage. For tests only: the keys are unencrypted.

```bash
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 365 \
  -subj "/CN=Test CA" -keyout ca.key -out ca.pem

printf 'subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n' > server.ext
openssl req -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes \
  -subj "/CN=localhost" -keyout server.key -out server.csr
openssl x509 -req -in server.csr -CA ca.pem -CAkey ca.key -CAcreateserial -days 365 \
  -extfile server.ext -out server.pem

printf 'extendedKeyUsage=clientAuth\n' > client.ext
openssl req -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes \
  -subj "/CN=my-client" -keyout client.key -out client.csr
openssl x509 -req -in client.csr -CA ca.pem -CAkey ca.key -CAcreateserial -days 365 \
  -extfile client.ext -out client.pem

chmod 600 *.key
openssl verify -CAfile ca.pem server.pem client.pem
```

The client then uses `ca.pem` / `client.pem` / `client.key`; a server that requires client
certificates uses `server.pem` / `server.key` and trusts `ca.pem` for its clients. The test suite
builds the same PKI in memory with `crypto/x509` on every run (`tests/tls_test.go`).

### TLS security notes

* Every `fmt` verb (`%v`, `%+v`, `%#v`, `%s`, ...) and `log/slog`, with any handler, render a
  `client.Config` with `GrpcClientKey` as `***REDACTED***` and the certificates by their size only.
  An empty key renders empty.
* `encoding/json` writes **every** field of a `client.Config`, `GrpcClientKey` included, **in clear
  text**. Treat a serialized config as a secret (file mode `0600`, never commit it), or better keep
  the key out of it and read it from a file or secret store at startup.
* Keep `client.key` readable by the service user only, and never log the `tls.Config` or the PEMs
  you read.

### TLS troubleshooting

A failed handshake is `codes.Unavailable`; the cause is in the error text:

* **`x509: certificate signed by unknown authority`**: `GrpcCert` is not the CA that issued the
  server certificate, the server does not send its intermediate certificates, or `GrpcCert` is
  empty and the server's CA is not in the system trust store.
* **`x509: cannot validate certificate for <ip> because it doesn't contain any IP SANs`** or
  **`x509: certificate is valid for <names>, not <host>`**: the host you connect to is not in the
  server certificate's SAN. Connect by a name in the SAN, add the SAN, or pass `grpc.WithAuthority`.
* **`remote error: tls: certificate required`**, **`tls: unknown certificate authority`**, or a bare
  `broken pipe` / `connection reset by peer` against a server that requires client certificates: no
  client certificate was presented, or one the server's CA did not issue. The server log names the
  reason. Set `GrpcClientCert` / `GrpcClientKey`.
* **`GrpcCert holds no PEM certificate`** from `NewChannel`: `GrpcCert` holds something that is not
  PEM, typically a file path. Pass the file's content instead.

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
├── client                                 <----- HAND WRITTEN - client.NewChannel: plaintext, TLS and mutual TLS
├── tests                                  <----- HAND WRITTEN - the go test suite (see Testing below)
├── ondewo-vtsi-api                             <----- submodule @ https://github.com/ondewo/ondewo-vtsi-api
├── ondewo-proto-compiler                  <----- submodule @ https://github.com/ondewo/ondewo-proto-compiler
├── .github
│   └── workflows
│       ├── ci.yml                          <----- build, test, lint and the credential-free publish rehearsal
│       └── release.yml                     <----- tagged-tree verification + module proxy warm-up, on a v* tag push
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

What it asserts about the **hand-written** `client` package (`tests/tls_test.go`), with real
handshakes against an in-process gRPC server on a loopback port and a PKI generated per run:
plain TLS, mutual TLS, a server requiring a client certificate refusing a client without one or
with one from an unrelated CA, a wrong CA and an empty `GrpcCert` (system roots) failing the
handshake as `codes.Unavailable`, CRLF PEMs, IPv6 `[::1]` (skipped without an IPv6 loopback),
`grpc.WithAuthority`, the bearer token over TLS and a message above gRPC's 4 MiB default; and,
before gRPC is reached, half a client identity, a plaintext channel with an identity, a path in
`GrpcCert` and a mismatched key refused with messages that carry no PEM, the insecure warning
naming `host:port`, and every `fmt` / `slog` rendering of a `Config` redacting the key.
`tests/release_notes_test.go` pins the `RELEASE.md` slice `make build_gh_release` publishes.

**Coverage.** The threshold (`COVERAGE_THRESHOLD` in the `Makefile`, currently **100%**) is
enforced over the hand-written packages only — `auth/` and `client/` — because everything below `api/` is machine
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
— the ONDEWO release tag (`8.7.0`) and the `v`-prefixed tag Go tooling requires (`v8.7.0`) — creates
the GitHub release from the matching `RELEASE.md` entry, and asks the public module proxy to fetch
the new version.

### How publishing works, and what it costs

Nothing is uploaded. `proxy.golang.org` clones the tag on the first request for it and serves a zip
of exactly what git has under that tag, so **the tag *is* the published artifact** and there is no
registry account to own, no namespace to claim and no publishing credential to rotate. The whole
correctness question is therefore about the tag:

* it must be spelled `v<semver>` — `8.7.0` alone is not a Go module version;
* the module path in `go.mod` must end in `/vN` matching the tag's major version, and that path is
  baked by `protoc-gen-go` into every generated import, so it cannot be patched after generation —
  `make generate_ondewo_protos` passes it to the compiler image as the third positional argument;
* everything a consumer compiles has to be *committed*, because the proxy never sees a working tree.

Each of those is a gate rather than a convention:

```shell
make check_go_module_path   ## go.mod and every self-import carry the /vN that ONDEWO_VTSI_VERSION implies
make check_release_notes    ## RELEASE.md has an entry for this version (`gh release create -n ""` would not complain)
make publish_dry_run        ## rehearse the whole publication, offline and without credentials
```

`make publish_dry_run` is the interesting one. It packs `git archive HEAD` into the module zip the
proxy would serve, publishes it through a throwaway `file://` module proxy, and then resolves and
compiles it from a consumer module outside this tree under the real release version — so a module
path that disagrees with the tag, a self-import missing its suffix, or generated code that never
reached the commit all fail here instead of at a stranger's `go get`. It needs no network beyond the
module cache and no secret of any kind, which is why `.github/workflows/ci.yml` runs it on every
push, and `make release` runs it once more immediately before the tags are created.

### Credentials

The only credential in this repository buys the **GitHub release**, not the module:

| Variable | Where it comes from | What it is for |
| --- | --- | --- |
| `GITHUB_GH_TOKEN` | `ondewo-devops-accounts/account_github.env` | `gh release create` — the GitHub release page and its notes |

`make ondewo_release` clones `ondewo-devops-accounts`, reads `account_github.env` and passes the
token into `make release` (`clone_devops_accounts` + `run_release_with_devops`); the working default
in the `Makefile` is the placeholder `ENTER_YOUR_TOKEN_HERE`, and a real token is never committed.

`.github/workflows/release.yml` re-verifies the tagged tree from CI on a `v*` tag push, reading the
token from the repository secret **`ONDEWO_GH_TOKEN`** — the same value as `GITHUB_GH_TOKEN` above.
The secret cannot carry that name: GitHub reserves every secret name beginning with `GITHUB_`, and
`ONDEWO_GH_TOKEN` is the one spelling the whole client fleet uses. Its first step fails the run with
an explicit message when that secret is missing, before anything is built — a release that cannot be
verified has to stop there, because the tag it describes is already immutable.

The workflow does **not** create the GitHub release. `make release` does, and `gh release create` is
not idempotent — a second create for the same tag is HTTP 422 `already_exists` — so when both sides
created it, the local run won the race and the workflow went red on every single release. It now
waits for that release to appear and then reads its body back, failing if the notes are empty.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache License 2.0 — see [LICENSE](LICENSE).
