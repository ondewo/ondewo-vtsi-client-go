# Release History

*****************

## Release ONDEWO VTSI Go Client 8.7.0

### New Features

* Initial release of the ONDEWO VTSI (Virtual Telephony Server Interface) gRPC client for Go. The module
  ships the stubs generated from the [ONDEWO VTSI API](https://github.com/ondewo/ondewo-vtsi-api)
  by version 5.15.0 of the
  [ONDEWO Proto Compiler](https://github.com/ondewo/ondewo-proto-compiler): one `*.pb.go` of
  messages and one `*_grpc.pb.go` of service stubs per `.proto` — 48 files from 25 protos, below
  `api/ondewo/{vtsi,nlu,qa,s2t,sip,t2s}/` — compiled against the `google.golang.org/protobuf` and
  `google.golang.org/grpc` runtimes pinned by the compiler image. It exposes 23 services — its own `ondewo.vtsi.{Calls,Logs,Projects}` plus the sixteen `ondewo.nlu.*`, `ondewo.qa.QA`, `ondewo.s2t.Speech2Text`, `ondewo.sip.Sip` and `ondewo.t2s.Text2Speech` that `ondewo-vtsi-api` vendors.
* The generated stubs are **committed**. A Go module is resolved straight from its version control
  tree — `go get` clones the tag and compiles what is in it — so a module that generated its code at
  build time would publish nothing.
* A hand-written `auth` package carries the ONDEWO credential: `auth.WithBearerToken(token)` is a
  `grpc.DialOption` that sends the Keycloak access token as `authorization: Bearer <token>` on every
  call, and refuses to attach itself to a plaintext connection.
* `make build` reproduces the whole client from the two submodules — proto compiler image, stub
  generation and `go build` — and `make check_build` asserts that every `.proto` of the API
  produced a stub.

### Improvements

* A real test suite under `tests/`, run by `.github/workflows/ci.yml` on go 1.25 and go 1.27. It
  needs no network and no ONDEWO server: gRPC connections are made over an in-memory `bufconn`
  listener, so a generated client stub, a generated server stub and a real HTTP/2 connection are
  exercised in-process. Messages round-trip on the wire, the two generators' views of every service
  are cross-checked against each other, and all 411 generated unary stubs are called for real.
* `make test_coverage` gates the build on the coverage of the **hand-written** packages
  (`COVERAGE_THRESHOLD`, currently 100%) and fails if it ends up measuring no function at all, so a
  deleted package cannot turn the gate into a green no-op. `make test_coverage_generated` reports
  the generated stubs' figure without gating it.
* `make check_stubs` runs first in CI and fails unless `api/` holds both message and service stubs
  alongside `go.mod`/`go.sum` — no later step is allowed to pass by finding nothing to do.
* The module path carries the `/v8` major-version suffix its release number requires
  (`github.com/ondewo/ondewo-vtsi-client-go/v8`). Go resolves a module straight from its VCS path,
  and from major version 2 on that path has to carry the major
  ([module reference](https://go.dev/ref/mod#major-version-suffixes)): a `8.7.0` tag on a module
  declared without `/v8` is invisible to `go get`.
* The release targets tag each release twice on the same commit: with the ONDEWO release number
  (`8.7.0`) that the rest of the fleet uses, and with the `v`-prefixed spelling (`v8.7.0`) that is
  the only tag shape the Go module resolver accepts. `make publish_go_module` then warms
  `proxy.golang.org` so the new version is immediately installable with `go get`.

*****************
