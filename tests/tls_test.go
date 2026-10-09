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

// This file is PRODUCT AGNOSTIC: it covers the hand-written client package, which is identical in
// every ONDEWO go client, and copies over unchanged.
//
// The handshakes are real: an in-process gRPC server on a loopback TCP port, with a PKI generated
// when the test runs (no private key is committed), and client.NewChannel dialing it exactly as an
// application would. An RPC that comes back from the server proves the handshake.
package tests

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/ondewo/ondewo-vtsi-client-go/v8/auth"
	"github.com/ondewo/ondewo-vtsi-client-go/v8/client"
)

// region test PKI

// testCA is a certificate authority generated for one test run.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  string
}

// testPKI is everything the handshake tests need: the CA the server and the client are issued by,
// and an unrelated CA with a client identity of its own.
type testPKI struct {
	ca           testCA
	otherCA      testCA
	server       tls.Certificate
	clientCert   string
	clientKey    string
	rogueCert    string
	rogueKey     string
	serverByName tls.Certificate // SAN DNS:nlu.example.internal only, no IP
}

// newSerial returns a random certificate serial number; the tests run in parallel, so no counter.
func newSerial(t *testing.T) *big.Int {
	t.Helper()

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		t.Fatalf("generating a serial number failed: %v", err)
	}

	return serial
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key failed: %v", err)
	}

	return key
}

func encodePEM(t *testing.T, blockType string, der []byte) string {
	t.Helper()

	return string(pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}))
}

func newCA(t *testing.T, name string) testCA {
	t.Helper()

	key := newKey(t)
	template := &x509.Certificate{
		SerialNumber:          newSerial(t),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating the CA %q failed: %v", name, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing the CA %q failed: %v", name, err)
	}

	return testCA{cert: cert, key: key, pem: encodePEM(t, "CERTIFICATE", der)}
}

// issue returns the PEM certificate and PKCS#8 PEM key of a leaf signed by ca.
func (ca testCA) issue(t *testing.T, name string, usage x509.ExtKeyUsage, dnsNames []string, ips []net.IP) (string, string) {
	t.Helper()

	key := newKey(t)
	template := &x509.Certificate{
		SerialNumber: newSerial(t),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		DNSNames:     dnsNames,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("issuing %q failed: %v", name, err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("encoding the key of %q failed: %v", name, err)
	}

	return encodePEM(t, "CERTIFICATE", der), encodePEM(t, "PRIVATE KEY", keyDER)
}

func newTestPKI(t *testing.T) testPKI {
	t.Helper()

	pki := testPKI{ca: newCA(t, "ONDEWO Test CA"), otherCA: newCA(t, "Unrelated Test CA")}

	serverCert, serverKey := pki.ca.issue(t, "localhost", x509.ExtKeyUsageServerAuth,
		[]string{"localhost"}, []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback})
	byNameCert, byNameKey := pki.ca.issue(t, "nlu.example.internal", x509.ExtKeyUsageServerAuth,
		[]string{"nlu.example.internal"}, nil)
	pki.clientCert, pki.clientKey = pki.ca.issue(t, "my-client", x509.ExtKeyUsageClientAuth, nil, nil)
	pki.rogueCert, pki.rogueKey = pki.otherCA.issue(t, "rogue-client", x509.ExtKeyUsageClientAuth, nil, nil)

	var err error
	if pki.server, err = tls.X509KeyPair([]byte(serverCert), []byte(serverKey)); err != nil {
		t.Fatalf("loading the server identity failed: %v", err)
	}
	if pki.serverByName, err = tls.X509KeyPair([]byte(byNameCert), []byte(byNameKey)); err != nil {
		t.Fatalf("loading the by-name server identity failed: %v", err)
	}

	return pki
}

// endregion

// region in-process server

// probeMethod is answered by the unknown-service handler of the test server; no generated service
// is needed, which keeps this file product agnostic.
const probeMethod = "/ondewo.probe.Probe/Echo"

// startServer serves on a loopback TCP port of network/address and returns its host and port.
// serverTLS nil means plaintext. The handler echoes the request payload, or, for an empty
// payload, answers with the authorization header it received.
func startServer(t *testing.T, address string, serverTLS *tls.Config) (string, string) {
	t.Helper()

	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listening on %s failed: %v", address, err)
	}

	return serve(t, listener, serverTLS)
}

func serve(t *testing.T, listener net.Listener, serverTLS *tls.Config) (string, string) {
	t.Helper()

	options := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(math.MaxInt32),
		grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
			request := &wrapperspb.BytesValue{}
			if err := stream.RecvMsg(request); err != nil {
				return err
			}
			if len(request.GetValue()) == 0 {
				md, _ := metadata.FromIncomingContext(stream.Context())
				request.Value = []byte(strings.Join(md.Get("authorization"), ","))
			}

			return stream.SendMsg(request)
		}),
	}
	if serverTLS != nil {
		options = append(options, grpc.Creds(credentials.NewTLS(serverTLS)))
	}
	server := grpc.NewServer(options...)

	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		<-served
	})

	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("splitting %s failed: %v", listener.Addr(), err)
	}

	return host, port
}

// tlsServer is a server TLS config; with clientCA set it requires a client certificate issued by it.
func tlsServer(identity tls.Certificate, clientCA *testCA) *tls.Config {
	config := &tls.Config{Certificates: []tls.Certificate{identity}, MinVersion: tls.VersionTLS12}
	if clientCA != nil {
		config.ClientCAs = x509.NewCertPool()
		config.ClientCAs.AddCert(clientCA.cert)
		config.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return config
}

// echo opens a channel for cfg and sends payload. It returns the answer and the RPC error.
func echo(t *testing.T, cfg client.Config, payload []byte, opts ...grpc.DialOption) ([]byte, error) {
	t.Helper()

	conn, err := client.NewChannel(cfg, opts...)
	if err != nil {
		t.Fatalf("client.NewChannel failed: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	response := &wrapperspb.BytesValue{}
	err = conn.Invoke(ctx, probeMethod, wrapperspb.Bytes(payload), response)

	return response.GetValue(), err
}

func mustReachServer(t *testing.T, cfg client.Config, opts ...grpc.DialOption) {
	t.Helper()

	got, err := echo(t, cfg, []byte("ping"), opts...)
	if err != nil {
		t.Fatalf("the RPC did not reach the server: %v", err)
	}
	if string(got) != "ping" {
		t.Fatalf("the server answered %q, want %q", got, "ping")
	}
}

// mustFailHandshake asserts the RPC fails as a failed handshake does: UNAVAILABLE, no crash.
func mustFailHandshake(t *testing.T, cfg client.Config, wantInDetails string) {
	t.Helper()

	_, err := echo(t, cfg, []byte("ping"))
	if err == nil {
		t.Fatal("the RPC succeeded, want the handshake refused")
	}
	if got := status.Code(err); got != codes.Unavailable {
		t.Fatalf("status = %v (%v), want %v", got, err, codes.Unavailable)
	}
	if !strings.Contains(err.Error(), wantInDetails) {
		t.Errorf("error = %q, want it to mention %q", err.Error(), wantInDetails)
	}
}

// endregion

// region real handshakes

func TestTLSWithACustomCAReachesTheServer(t *testing.T) {
	t.Parallel()

	pki := newTestPKI(t)
	host, port := startServer(t, "127.0.0.1:0", tlsServer(pki.server, nil))

	// Empty strings on both halves of the client identity mean plain server-authenticated TLS.
	mustReachServer(t, client.Config{Host: host, Port: port, GrpcCert: pki.ca.pem, GrpcClientCert: "", GrpcClientKey: ""})
}

func TestTLSWithoutGrpcCertUsesTheSystemTrustStore(t *testing.T) {
	t.Parallel()

	pki := newTestPKI(t)
	host, port := startServer(t, "127.0.0.1:0", tlsServer(pki.server, nil))

	// The test CA is in no system trust store, so the server certificate must be refused: an empty
	// GrpcCert means "the system's roots", never "do not verify".
	mustFailHandshake(t, client.Config{Host: host, Port: port}, "certificate")
}

func TestMutualTLSReachesTheServer(t *testing.T) {
	t.Parallel()

	pki := newTestPKI(t)
	host, port := startServer(t, "127.0.0.1:0", tlsServer(pki.server, &pki.ca))

	mustReachServer(t, client.Config{
		Host: host, Port: port, GrpcCert: pki.ca.pem, GrpcClientCert: pki.clientCert, GrpcClientKey: pki.clientKey,
	})
}

func TestMutualTLSServerRejectsAClientWithoutIdentity(t *testing.T) {
	t.Parallel()

	pki := newTestPKI(t)
	host, port := startServer(t, "127.0.0.1:0", tlsServer(pki.server, &pki.ca))

	_, err := echo(t, client.Config{Host: host, Port: port, GrpcCert: pki.ca.pem}, []byte("ping"))
	if got := status.Code(err); got != codes.Unavailable {
		t.Fatalf("status = %v (%v), want %v: a server requiring a client certificate let a client without one in",
			got, err, codes.Unavailable)
	}
}

func TestMutualTLSRejectsAnIdentityFromAnUnrelatedCA(t *testing.T) {
	t.Parallel()

	pki := newTestPKI(t)
	host, port := startServer(t, "127.0.0.1:0", tlsServer(pki.server, &pki.ca))

	_, err := echo(t, client.Config{
		Host: host, Port: port, GrpcCert: pki.ca.pem, GrpcClientCert: pki.rogueCert, GrpcClientKey: pki.rogueKey,
	}, []byte("ping"))
	if got := status.Code(err); got != codes.Unavailable {
		t.Fatalf("status = %v (%v), want %v: the server accepted a client certificate of an unrelated CA",
			got, err, codes.Unavailable)
	}
}

func TestTLSWithTheWrongCAFailsTheHandshake(t *testing.T) {
	t.Parallel()

	pki := newTestPKI(t)
	host, port := startServer(t, "127.0.0.1:0", tlsServer(pki.server, nil))

	mustFailHandshake(t, client.Config{Host: host, Port: port, GrpcCert: pki.otherCA.pem}, "certificate")
}

func TestTLSAcceptsCRLFPEMs(t *testing.T) {
	t.Parallel()

	pki := newTestPKI(t)
	host, port := startServer(t, "127.0.0.1:0", tlsServer(pki.server, &pki.ca))
	crlf := func(pem string) string { return strings.ReplaceAll(pem, "\n", "\r\n") }

	mustReachServer(t, client.Config{
		Host: host, Port: port,
		GrpcCert: crlf(pki.ca.pem), GrpcClientCert: crlf(pki.clientCert), GrpcClientKey: crlf(pki.clientKey),
	})
}

func TestMutualTLSOverIPv6(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback in this environment: %v", err)
	}
	pki := newTestPKI(t)
	_, port := serve(t, listener, tlsServer(pki.server, &pki.ca))

	// A bare IPv6 literal: NewChannel has to bracket it, and the server certificate has an IP SAN ::1.
	mustReachServer(t, client.Config{
		Host: "::1", Port: port, GrpcCert: pki.ca.pem, GrpcClientCert: pki.clientCert, GrpcClientKey: pki.clientKey,
	})
}

func TestWithAuthorityVerifiesTheServerCertificateAgainstAnotherName(t *testing.T) {
	t.Parallel()

	pki := newTestPKI(t)
	host, port := startServer(t, "127.0.0.1:0", tlsServer(pki.serverByName, nil))
	cfg := client.Config{Host: host, Port: port, GrpcCert: pki.ca.pem}

	// The certificate names only nlu.example.internal: connecting by IP fails the host check ...
	mustFailHandshake(t, cfg, "certificate")
	// ... unless the name to verify is given, as grpc.ssl_target_name_override does in python.
	mustReachServer(t, cfg, grpc.WithAuthority("nlu.example.internal"))
}

func TestBearerTokenTravelsOverTLS(t *testing.T) {
	t.Parallel()

	pki := newTestPKI(t)
	host, port := startServer(t, "127.0.0.1:0", tlsServer(pki.server, &pki.ca))

	got, err := echo(t, client.Config{
		Host: host, Port: port, GrpcCert: pki.ca.pem, GrpcClientCert: pki.clientCert, GrpcClientKey: pki.clientKey,
	}, nil, auth.WithBearerToken("abc"))
	if err != nil {
		t.Fatalf("the RPC failed: %v", err)
	}
	if string(got) != "Bearer abc" {
		t.Errorf("the server saw authorization %q, want %q", got, "Bearer abc")
	}
}

func TestMessagesLargerThanTheGRPCDefaultLimitGetThrough(t *testing.T) {
	t.Parallel()

	pki := newTestPKI(t)
	host, port := startServer(t, "127.0.0.1:0", tlsServer(pki.server, nil))

	// gRPC's default receive limit is 4 MiB: this answer only arrives because NewChannel raises it.
	payload := bytes.Repeat([]byte{'x'}, 5*1024*1024)
	got, err := echo(t, client.Config{Host: host, Port: port, GrpcCert: pki.ca.pem}, payload)
	if err != nil {
		t.Fatalf("a 5 MiB round trip failed: %v", err)
	}
	if len(got) != len(payload) {
		t.Errorf("received %d bytes, want %d", len(got), len(payload))
	}
}

func TestInsecureChannelReachesAPlaintextServerAndWarns(t *testing.T) {
	t.Parallel()

	host, port := startServer(t, "127.0.0.1:0", nil)
	var logged bytes.Buffer

	mustReachServer(t, client.Config{
		Host: host, Port: port, Insecure: true, Logger: slog.New(slog.NewTextHandler(&logged, nil)),
	})

	if !strings.Contains(logged.String(), "level=WARN") ||
		!strings.Contains(logged.String(), "INSECURE (plaintext) gRPC channel to "+host+":"+port) {
		t.Errorf("logged %q, want a warning naming %s:%s", logged.String(), host, port)
	}
}

// Not parallel: it swaps the process-wide default logger, which a parallel test could observe.
func TestInsecureChannelWarnsOnTheDefaultLoggerWhenNoneIsSet(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	conn, err := client.NewChannel(client.Config{Host: "ondewo.invalid", Port: "50051", Insecure: true})
	if err != nil {
		t.Fatalf("client.NewChannel failed: %v", err)
	}
	_ = conn.Close()

	if !strings.Contains(logged.String(), "INSECURE (plaintext) gRPC channel to ondewo.invalid:50051") {
		t.Errorf("logged %q, want the warning naming ondewo.invalid:50051", logged.String())
	}
}

// endregion

// region refused before gRPC

func TestNewChannelRefusesBrokenConfigsBeforeGRPC(t *testing.T) {
	t.Parallel()

	pki := newTestPKI(t)
	cases := map[string]struct {
		cfg  client.Config
		want string
	}{
		"client certificate without key": {
			client.Config{Host: "localhost", Port: "50051", GrpcCert: pki.ca.pem, GrpcClientCert: pki.clientCert},
			"sets only one of GrpcClientCert and GrpcClientKey",
		},
		"client key without certificate": {
			client.Config{Host: "localhost", Port: "50051", GrpcCert: pki.ca.pem, GrpcClientKey: pki.clientKey},
			"sets only one of GrpcClientCert and GrpcClientKey",
		},
		"half a pair on a plaintext channel": {
			client.Config{Host: "localhost", Port: "50051", Insecure: true, GrpcClientKey: pki.clientKey},
			"sets only one of GrpcClientCert and GrpcClientKey",
		},
		"plaintext channel with a client identity": {
			client.Config{
				Host: "localhost", Port: "50051", Insecure: true,
				GrpcClientCert: pki.clientCert, GrpcClientKey: pki.clientKey,
			},
			"carries a client certificate for mutual TLS, but Insecure would send it nowhere",
		},
		"GrpcCert holding a file path": {
			client.Config{Host: "localhost", Port: "50051", GrpcCert: "/etc/ondewo/ca.pem"},
			"GrpcCert holds no PEM certificate",
		},
		"client certificate and key that do not match": {
			client.Config{
				Host: "localhost", Port: "50051", GrpcCert: pki.ca.pem,
				GrpcClientCert: pki.clientCert, GrpcClientKey: pki.rogueKey,
			},
			"are not a PEM certificate and its private key",
		},
		"client certificate given as the key": {
			client.Config{
				Host: "localhost", Port: "50051", GrpcCert: pki.ca.pem,
				GrpcClientCert: pki.clientCert, GrpcClientKey: pki.clientCert,
			},
			"are not a PEM certificate and its private key",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			conn, err := client.NewChannel(tc.cfg)
			if err == nil {
				_ = conn.Close()
				t.Fatal("client.NewChannel succeeded, want it refused")
			}
			if conn != nil {
				t.Error("a connection was returned alongside the error")
			}
			message := err.Error()
			if !strings.Contains(message, tc.want) {
				t.Errorf("error = %q, want it to contain %q", message, tc.want)
			}
			if !strings.Contains(message, "localhost:50051") {
				t.Errorf("error = %q, want it to name localhost:50051", message)
			}
			assertNoSecret(t, message, pki)
		})
	}
}

// assertNoSecret fails when text contains a PEM, or any line of the test PKI's private keys.
func assertNoSecret(t *testing.T, text string, pki testPKI) {
	t.Helper()

	if strings.Contains(text, "-----BEGIN") {
		t.Errorf("%q contains a PEM block", text)
	}
	for _, key := range []string{pki.clientKey, pki.rogueKey} {
		for _, line := range strings.Split(key, "\n") {
			if len(line) > 16 && !strings.HasPrefix(line, "-----") && strings.Contains(text, line) {
				t.Errorf("%q contains private key material", text)
			}
		}
	}
}

// endregion

// region target and rendering

func TestTargetBracketsBareIPv6Literals(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"localhost":         "localhost:50051",
		"127.0.0.1":         "127.0.0.1:50051",
		"::1":               "[::1]:50051",
		"2001:db8::7":       "[2001:db8::7]:50051",
		"fe80::1%eth0":      "[fe80::1%eth0]:50051",
		"::ffff:10.0.0.5":   "[::ffff:10.0.0.5]:50051",
		"[::1]":             "[::1]:50051",
		"ipv6:[::1]":        "ipv6:[::1]:50051",
		"dns:///grpc.local": "dns:///grpc.local:50051",
	}
	for host, want := range cases {
		if got := (client.Config{Host: host, Port: "50051"}).Target(); got != want {
			t.Errorf("Config{Host: %q}.Target() = %q, want %q", host, got, want)
		}
	}
}

func TestConfigRenderingsRedactTheClientKey(t *testing.T) {
	t.Parallel()

	pki := newTestPKI(t)
	cfg := client.Config{
		Host: "localhost", Port: "50051", GrpcCert: pki.ca.pem, GrpcClientCert: pki.clientCert, GrpcClientKey: pki.clientKey,
	}

	var text, jsonLog bytes.Buffer
	slog.New(slog.NewTextHandler(&text, nil)).Info("config", "cfg", cfg)
	slog.New(slog.NewJSONHandler(&jsonLog, nil)).Info("config", "cfg", cfg)

	renderings := map[string]string{
		"String": cfg.String(), "slog text": text.String(), "slog json": jsonLog.String(),
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x"} {
		renderings[verb] = fmt.Sprintf(verb, cfg)
	}
	renderings["pointer %+v"] = fmt.Sprintf("%+v", &cfg)

	for name, rendering := range renderings {
		if !strings.Contains(rendering, "***REDACTED***") {
			t.Errorf("%s rendering %q does not show the key as ***REDACTED***", name, rendering)
		}
		if !strings.Contains(rendering, "localhost") {
			t.Errorf("%s rendering %q does not show the host", name, rendering)
		}
		assertNoSecret(t, rendering, pki)
	}
}

func TestConfigRendersAnEmptyKeyAsEmpty(t *testing.T) {
	t.Parallel()

	got := client.Config{Host: "localhost", Port: "50051"}.String()
	want := `client.Config{Host: "localhost", Port: "50051", GrpcCert: "", GrpcClientCert: "", GrpcClientKey: "", Insecure: false}`
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// TestJSONCarriesTheKeyAsDocumented pins the documented exception: encoding/json is persistence,
// not logging, and writes the private key in clear text.
func TestJSONCarriesTheKeyAsDocumented(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(client.Config{GrpcClientKey: "k3y"})
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	if !strings.Contains(string(encoded), `"GrpcClientKey":"k3y"`) {
		t.Errorf("json = %s, want the key in clear text as the README documents", encoded)
	}
}

func TestConnectionDefaultsMatchThePythonSDKs(t *testing.T) {
	t.Parallel()

	if client.KeepaliveTime != 30*time.Second || client.KeepaliveTimeout != 20*time.Second ||
		client.MaxReconnectBackoff != 5*time.Second || client.MaxMessageLength != math.MaxInt32 {
		t.Errorf("defaults = %v / %v / %v / %v, want 30s / 20s / 5s / %d",
			client.KeepaliveTime, client.KeepaliveTimeout, client.MaxReconnectBackoff, client.MaxMessageLength,
			math.MaxInt32)
	}
}

// endregion
