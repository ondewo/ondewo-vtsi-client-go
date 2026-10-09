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

// Package client opens the gRPC connection to an ONDEWO server: plaintext, TLS or mutual TLS.
//
// It is hand written (nothing below api/ is, and api/ is wiped on every regeneration) and follows
// the TLS contract every ONDEWO client SDK implements, with ondewo-client-utils-python as the
// reference: the certificates are PEM CONTENT, never file paths; a client certificate and its key
// go together or not at all; a plaintext connection never silently drops a client identity; and
// no error message or rendering of a Config contains a PEM or the private key.
package client

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/netip"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// The connection defaults, the same values the python SDKs pass to grpc-core.
const (
	// KeepaliveTime is the interval of the keepalive pings. They are only sent while an RPC is
	// active (PermitWithoutStream stays false), so an idle connection never pings a server that
	// would answer a ping-only client with GOAWAY "too_many_pings".
	KeepaliveTime = 30 * time.Second

	// KeepaliveTimeout is how long an unanswered keepalive ping may take before the connection
	// counts as dead. grpc-go has one setting for what grpc-core splits into
	// keepalive_timeout_ms and http2.ping_timeout_ms; both are 20 s in the python SDKs.
	KeepaliveTimeout = 20 * time.Second

	// MaxReconnectBackoff caps the wait between two reconnection attempts (gRPC default 120 s), so
	// a client finds a server that came back after an outage within seconds.
	MaxReconnectBackoff = 5 * time.Second

	// MaxMessageLength is the largest message the connection sends or receives (gRPC default for
	// receiving: 4 MiB).
	MaxMessageLength = math.MaxInt32
)

// minConnectTimeout is gRPC's own default, restated because grpc.WithConnectParams replaces it.
const minConnectTimeout = 20 * time.Second

// redacted replaces a secret in every rendering of a Config.
const redacted = "***REDACTED***"

// Config describes the connection to one ONDEWO server.
//
// Formatting a Config with any fmt verb, or logging it through log/slog, renders GrpcClientKey as
// ***REDACTED*** and the certificates by their size only. encoding/json does NOT: it writes every
// field, the private key included, in clear text - treat a serialized Config as a secret.
type Config struct {
	// Host is a host name, an IPv4 or IPv6 literal, or a gRPC target with a scheme
	// ("dns:///...", "unix:..."). A bare IPv6 literal is bracketed by Target.
	Host string

	// Port is the server port, e.g. "50051".
	Port string

	// GrpcCert is the PEM CONTENT (not the path) of the CA certificate the server certificate is
	// verified against. Empty: the system's trust store is used.
	GrpcCert string

	// GrpcClientCert and GrpcClientKey are the PEM CONTENT of the client certificate chain and
	// its private key, for mutual TLS. Set both or neither.
	GrpcClientCert string
	GrpcClientKey  string

	// Insecure opens a plaintext connection (use_secure_channel=False in the python SDKs). The
	// zero value is a TLS connection. Never use it in production.
	Insecure bool

	// Logger receives the warning about a plaintext connection. Nil: slog.Default().
	Logger *slog.Logger
}

// Target is the gRPC target of the connection, "Host:Port". A bare IPv6 literal is bracketed
// ("::1" gives "[::1]:50051"); a bracketed host or one with a scheme is left as it is.
func (c Config) Target() string {
	if addr, err := netip.ParseAddr(c.Host); err == nil && addr.Is6() {
		return net.JoinHostPort(c.Host, c.Port)
	}

	return c.Host + ":" + c.Port
}

// String renders the Config without any secret: see the Config documentation.
func (c Config) String() string {
	return fmt.Sprintf(
		"client.Config{Host: %q, Port: %q, GrpcCert: %s, GrpcClientCert: %s, GrpcClientKey: %s, Insecure: %t}",
		c.Host, c.Port, pemSummary(c.GrpcCert), pemSummary(c.GrpcClientCert), secret(c.GrpcClientKey), c.Insecure,
	)
}

// Format implements fmt.Formatter, so that EVERY verb renders the redacted String - without it,
// %d or %#v would print the fields, the private key included.
func (c Config) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, c.String())
}

// LogValue implements slog.LogValuer, so that a Config logged through log/slog, by any handler,
// carries no secret either.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("host", c.Host),
		slog.String("port", c.Port),
		slog.String("grpc_cert", pemSummary(c.GrpcCert)),
		slog.String("grpc_client_cert", pemSummary(c.GrpcClientCert)),
		slog.String("grpc_client_key", secret(c.GrpcClientKey)),
		slog.Bool("insecure", c.Insecure),
	)
}

// NewChannel opens a connection to the server described by cfg, with the ONDEWO connection
// defaults (keepalive, reconnect backoff, message size). opts are applied after the defaults, so
// they can override them; pass e.g. auth.WithBearerToken(token), or grpc.WithAuthority(name) to
// verify the server certificate against another name than Host.
//
// Like grpc.NewClient, it does not connect: the first RPC does, and a failed TLS handshake is
// reported there with codes.Unavailable.
//
// It refuses, before gRPC sees any of it, a Config that sets only one of GrpcClientCert and
// GrpcClientKey, a plaintext Config that carries a client identity, a GrpcCert that holds no PEM
// certificate, and a client certificate and key that do not form a pair.
func NewChannel(cfg Config, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	target := cfg.Target()
	hasClientCert, hasClientKey := cfg.GrpcClientCert != "", cfg.GrpcClientKey != ""

	if hasClientCert != hasClientKey {
		return nil, fmt.Errorf(
			"ondewo/client: client.Config for %s sets only one of GrpcClientCert and GrpcClientKey; "+
				"set both to use mutual TLS, or neither", target,
		)
	}

	var transport credentials.TransportCredentials
	if cfg.Insecure {
		if hasClientCert {
			return nil, fmt.Errorf(
				"ondewo/client: client.Config for %s carries a client certificate for mutual TLS, but "+
					"Insecure would send it nowhere; use a TLS connection", target,
			)
		}
		logger := cfg.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn(fmt.Sprintf("ondewo/client: using an INSECURE (plaintext) gRPC channel to %s", target))
		transport = insecure.NewCredentials()
	} else {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
		if cfg.GrpcCert != "" {
			tlsConfig.RootCAs = x509.NewCertPool()
			if !tlsConfig.RootCAs.AppendCertsFromPEM([]byte(cfg.GrpcCert)) {
				return nil, fmt.Errorf(
					"ondewo/client: client.Config for %s: GrpcCert holds no PEM certificate; "+
						"pass the content of the CA certificate file, not its path", target,
				)
			}
		}
		if hasClientCert {
			identity, err := tls.X509KeyPair([]byte(cfg.GrpcClientCert), []byte(cfg.GrpcClientKey))
			if err != nil {
				// The tls/x509 errors name block types and mismatches, never PEM content.
				return nil, fmt.Errorf(
					"ondewo/client: client.Config for %s: GrpcClientCert and GrpcClientKey are not a "+
						"PEM certificate and its private key: %w", target, err,
				)
			}
			tlsConfig.Certificates = []tls.Certificate{identity}
		}
		transport = credentials.NewTLS(tlsConfig)
	}

	backoffConfig := backoff.DefaultConfig
	backoffConfig.MaxDelay = MaxReconnectBackoff
	dialOptions := append([]grpc.DialOption{
		grpc.WithTransportCredentials(transport),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                KeepaliveTime,
			Timeout:             KeepaliveTimeout,
			PermitWithoutStream: false,
		}),
		// MinConnectTimeout has to be set with the backoff: left zero, a connection attempt would
		// be cut off after the current backoff delay (1 s at first) instead of gRPC's 20 s.
		grpc.WithConnectParams(grpc.ConnectParams{Backoff: backoffConfig, MinConnectTimeout: minConnectTimeout}),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(MaxMessageLength),
			grpc.MaxCallSendMsgSize(MaxMessageLength),
		),
	}, opts...)

	return grpc.NewClient(target, dialOptions...)
}

// pemSummary renders a certificate by its size: it is not secret, but it is long.
func pemSummary(pem string) string {
	if pem == "" {
		return `""`
	}

	return fmt.Sprintf("<PEM, %d bytes>", len(pem))
}

// secret renders a secret as ***REDACTED***, and an empty one as empty.
func secret(value string) string {
	if value == "" {
		return `""`
	}

	return redacted
}
