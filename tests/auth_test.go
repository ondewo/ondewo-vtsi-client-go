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

// This file is PRODUCT AGNOSTIC: it covers the hand-written auth package, which is identical in
// every ONDEWO go client, and copies over unchanged.
package tests

import (
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/ondewo/ondewo-vtsi-client-go/v8/auth"
)

// TestBearerTokenReachesTheServerAsAnAuthorizationHeader is the end-to-end assertion about the
// hand-written credential: a real RPC over a real (in-process) connection has to arrive carrying
// `authorization: Bearer <token>`, which is what the ONDEWO servers read. The server side is a
// plain gRPC handler, so it sees exactly the metadata a production server would.
func TestBearerTokenReachesTheServerAsAnAuthorizationHeader(t *testing.T) {
	t.Parallel()

	const token = "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.ondewo.signature"

	// Buffered, so the handler never blocks on a test that has already failed.
	seen := make(chan []string, 1)
	// An unknown-service handler receives every RPC, whatever its method - so this observes the
	// credential without needing a generated service, which keeps the file product agnostic.
	capture := grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		md, _ := metadata.FromIncomingContext(stream.Context())
		seen <- md.Get("authorization")

		// A unary call expects exactly one message back; returning without one is reported to the
		// client as a cardinality violation rather than as the success this test needs.
		if err := stream.RecvMsg(&emptypb.Empty{}); err != nil {
			return err
		}

		return stream.SendMsg(&emptypb.Empty{})
	})

	// The credential travels over a plaintext in-process connection, so it has to be allowed onto
	// one explicitly - that is the guard RequireTransportSecurity implements.
	conn := dialInProcess(t,
		[]grpc.ServerOption{capture},
		func(*grpc.Server) {},
		grpc.WithPerRPCCredentials(auth.BearerToken{Token: token, AllowInsecureTransport: true}),
	)

	if err := conn.Invoke(t.Context(), "/ondewo.probe.Probe/Probe", &emptypb.Empty{}, &emptypb.Empty{}); err != nil {
		t.Fatalf("the probe RPC failed before the server could read its metadata: %v", err)
	}

	header := <-seen
	if len(header) != 1 {
		t.Fatalf("the server saw %d authorization headers, want exactly 1: %v", len(header), header)
	}
	if got, want := header[0], "Bearer "+token; got != want {
		t.Errorf("authorization header = %q, want %q", got, want)
	}
}

// TestBearerTokenRejectsAnEmptyToken asserts the credential fails loudly instead of sending an
// empty `Bearer ` that the server answers with an opaque 401.
func TestBearerTokenRejectsAnEmptyToken(t *testing.T) {
	t.Parallel()

	md, err := auth.BearerToken{}.GetRequestMetadata(t.Context())
	if err == nil {
		t.Fatalf("an empty token produced metadata %v, want an error", md)
	}
	if md != nil {
		t.Errorf("metadata = %v alongside the error, want nil", md)
	}
	if !strings.Contains(err.Error(), "Token is empty") {
		t.Errorf("error = %q, want it to name the empty Token field", err.Error())
	}
}

// TestBearerTokenMetadataUsesTheLowerCaseHeader pins the header spelling. gRPC rejects upper-case
// metadata keys, so this is not cosmetic.
func TestBearerTokenMetadataUsesTheLowerCaseHeader(t *testing.T) {
	t.Parallel()

	md, err := auth.BearerToken{Token: "abc"}.GetRequestMetadata(t.Context(), "ondewo.vtsi.Logs/ListCallLogs")
	if err != nil {
		t.Fatalf("GetRequestMetadata failed: %v", err)
	}
	if got, want := len(md), 1; got != want {
		t.Fatalf("metadata has %d entries, want %d: %v", got, want, md)
	}
	if got, want := md["authorization"], "Bearer abc"; got != want {
		t.Errorf("metadata[authorization] = %q, want %q", got, want)
	}
}

// TestBearerTokenRequiresTransportSecurityByDefault covers both sides of the guard that keeps a
// token off an unencrypted wire.
func TestBearerTokenRequiresTransportSecurityByDefault(t *testing.T) {
	t.Parallel()

	if !(auth.BearerToken{Token: "abc"}).RequireTransportSecurity() {
		t.Error("RequireTransportSecurity() = false by default, want true - a token must not travel in plaintext")
	}
	if (auth.BearerToken{Token: "abc", AllowInsecureTransport: true}).RequireTransportSecurity() {
		t.Error("RequireTransportSecurity() = true with AllowInsecureTransport, want false")
	}
}

// TestWithBearerTokenRefusesAPlaintextConnection asserts the convenience dial option really
// carries the strict credential: dialing without transport security has to be refused.
func TestWithBearerTokenRefusesAPlaintextConnection(t *testing.T) {
	t.Parallel()

	conn, err := grpc.NewClient(
		"passthrough:///ondewo.invalid:50055",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		auth.WithBearerToken("abc"),
	)
	if err == nil {
		_ = conn.Close()
		t.Fatal("dialing a plaintext connection with WithBearerToken succeeded, want it refused")
	}
	if !strings.Contains(err.Error(), "transport") {
		t.Errorf("error = %q, want it to name the missing transport security", err.Error())
	}
}
