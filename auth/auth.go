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

// Package auth carries the ONDEWO credential on every gRPC call of this client.
//
// It is hand written - nothing below api/ is, and api/ is wiped on every regeneration - and it is
// deliberately the whole of the hand-written surface: the ONDEWO servers authenticate a client by
// the Keycloak access token in the `authorization` request header, exactly as the python, angular
// and typescript clients of the same API do.
package auth

import (
	"context"
	"errors"

	"google.golang.org/grpc"
)

// authorizationHeader is the gRPC metadata key the ONDEWO servers read the credential from. gRPC
// requires metadata keys to be lower case; `Authorization` would be rejected by the transport.
const authorizationHeader = "authorization"

// BearerToken sends a static Keycloak access token as `authorization: Bearer <token>` on every
// RPC. It implements google.golang.org/grpc/credentials.PerRPCCredentials, so it is attached to a
// connection with grpc.WithPerRPCCredentials - or, more conveniently, with WithBearerToken below.
//
// The zero value is not usable: an empty token is rejected rather than sent, because a request
// carrying `Bearer ` with nothing behind it fails at the server with an opaque 401 that is far
// harder to trace back than an error raised here.
type BearerToken struct {
	// Token is the access token, WITHOUT the `Bearer ` prefix - it is added on every call.
	Token string

	// AllowInsecureTransport permits the credential to travel over a plaintext connection. It is
	// false by default, so a token is never leaked onto an unencrypted wire by accident; set it
	// only against a local development server or an in-process test connection.
	AllowInsecureTransport bool
}

// GetRequestMetadata implements credentials.PerRPCCredentials.
func (b BearerToken) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	if b.Token == "" {
		return nil, errors.New(
			"ondewo/auth: BearerToken.Token is empty - set it to the Keycloak access token of the " +
				"ONDEWO user, or dial without per-RPC credentials if the server needs none",
		)
	}

	return map[string]string{authorizationHeader: "Bearer " + b.Token}, nil
}

// RequireTransportSecurity implements credentials.PerRPCCredentials. gRPC refuses to attach the
// credential to a plaintext connection while this reports true.
func (b BearerToken) RequireTransportSecurity() bool {
	return !b.AllowInsecureTransport
}

// WithBearerToken is the grpc.DialOption that sends token on every call of the connection. It is
// the shorthand for the common case; construct BearerToken directly to reach AllowInsecureTransport.
func WithBearerToken(token string) grpc.DialOption {
	return grpc.WithPerRPCCredentials(BearerToken{Token: token})
}
