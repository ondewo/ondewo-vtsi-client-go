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

// This file is PRODUCT AGNOSTIC: Keycloak authentication through the public surface - a real
// (in-process) token endpoint, a real gRPC server and client.NewChannel - and the client.Config
// half of the configuration contract (SPEC.md T55, T56-T62). The provider's own catalogue is in
// auth/keycloak_*_test.go.
package tests

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ondewo/ondewo-vtsi-client-go/v9/auth"
	"github.com/ondewo/ondewo-vtsi-client-go/v9/client"
)

// keycloakStub is a token endpoint that answers every grant with a fresh access token and records
// the grant types it saw.
type keycloakStub struct {
	mu     sync.Mutex
	forms  []string
	status int
}

func (k *keycloakStub) serve(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		k.mu.Lock()
		k.forms = append(k.forms, r.PostForm.Encode())
		status := k.status
		k.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"Offline user session not found"}`)

			return
		}
		_, _ = fmt.Fprintf(w, `{"access_token":"acc-e2e-%s","expires_in":300,"refresh_token":"off-e2e"}`,
			r.PostForm.Get("grant_type"))
	}))
	t.Cleanup(server.Close)

	return server
}

func (k *keycloakStub) recorded() []string {
	k.mu.Lock()
	defer k.mu.Unlock()

	return append([]string(nil), k.forms...)
}

func keycloakClientConfig(t *testing.T, keycloakURL, host, port string) client.Config {
	return client.Config{
		Host: host, Port: port, Insecure: true, Logger: slog.New(slog.DiscardHandler),
		KeycloakURL: keycloakURL, Realm: "ondewo-test", ClientID: "ondewo-test-sdk-public",
		Username: "technical-user@example.com", Password: "pw-" + strings.ReplaceAll(t.Name(), "/", "-"),
	}
}

func TestKeycloakPasswordLoginAuthenticatesEveryRPC(t *testing.T) {
	t.Parallel()
	stub := &keycloakStub{}
	keycloak := stub.serve(t)
	host, port := startServer(t, "127.0.0.1:0", nil)
	cfg := keycloakClientConfig(t, keycloak.URL+"/auth", host, port)
	// Held for the whole test: the registry holds providers weakly, so without a reference a
	// garbage collection between the two connections would legitimately log in a second time.
	provider, err := auth.SharedKeycloakTokenProvider(cfg.KeycloakConfig(), auth.WithKeycloakLogger(cfg.Logger))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = provider.Close() }()

	for range 2 {
		header, err := echo(t, cfg, nil)
		if err != nil {
			t.Fatalf("the RPC failed: %v", err)
		}
		if string(header) != "Bearer acc-e2e-password" {
			t.Errorf("authorization = %q, want Bearer acc-e2e-password", header)
		}
	}
	// Two connections of one credential set share one provider: ONE login.
	forms := stub.recorded()
	if len(forms) != 1 || !strings.Contains(forms[0], "grant_type=password") || !strings.Contains(forms[0], "scope=offline_access") {
		t.Errorf("token requests = %v, want exactly one password grant", forms)
	}
}

// TestT56T60AnOfflineTokenConfigNeverSendsAPassword is the token-only start-up end to end: one
// refresh grant with the handed-off token, no password anywhere, and the token on the wire.
func TestT56T60AnOfflineTokenConfigNeverSendsAPassword(t *testing.T) {
	t.Parallel()
	stub := &keycloakStub{}
	keycloak := stub.serve(t)
	host, port := startServer(t, "127.0.0.1:0", nil)
	cfg := keycloakClientConfig(t, keycloak.URL, host, port)
	cfg.Password = ""
	cfg.RefreshToken = "off-handed-" + t.Name()

	header, err := echo(t, cfg, nil)
	if err != nil {
		t.Fatalf("the RPC failed: %v", err)
	}
	if string(header) != "Bearer acc-e2e-refresh_token" {
		t.Errorf("authorization = %q", header)
	}
	forms := stub.recorded()
	if len(forms) != 1 || !strings.Contains(forms[0], "grant_type=refresh_token") ||
		!strings.Contains(forms[0], "refresh_token=off-handed-") || strings.Contains(forms[0], "password") {
		t.Errorf("token requests = %v, want one refresh grant without a password", forms)
	}
}

func TestARefusedKeycloakLoginFailsNewChannelWithTheTypedError(t *testing.T) {
	t.Parallel()
	stub := &keycloakStub{status: http.StatusBadRequest}
	keycloak := stub.serve(t)
	cfg := keycloakClientConfig(t, keycloak.URL, "localhost", "50051")
	cfg.RefreshToken = "off-dead"

	conn, err := client.NewChannel(cfg)

	var authErr *auth.KeycloakAuthenticationError
	if conn != nil || !errors.As(err, &authErr) || !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("NewChannel = %v, %v, want the typed error naming status 400", conn, err)
	}
	if forms := stub.recorded(); len(forms) != 1 || strings.Contains(forms[0], "grant_type=password") {
		t.Errorf("token requests = %v, want the refused refresh grant only (no password fallback)", forms)
	}
}

// TestT62WithoutAKeycloakTripleNoCredentialIsAttached: neither a provider nor a header.
func TestT62WithoutAKeycloakTripleNoCredentialIsAttached(t *testing.T) {
	t.Parallel()
	host, port := startServer(t, "127.0.0.1:0", nil)
	cfg := client.Config{
		Host: host, Port: port, Insecure: true, Logger: slog.New(slog.DiscardHandler),
		Username: "user", Password: "pw",
	}

	header, err := echo(t, cfg, nil)
	if err != nil || len(header) != 0 {
		t.Errorf("authorization = %q, %v, want none", header, err)
	}
}

func TestNewChannelRefusesAnInvalidKeycloakConfigBeforeAnyNetworkIO(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		cfg  client.Config
		want []string
	}{
		"partial triple": {
			client.Config{Host: "localhost", Port: "50051", Realm: "ondewo-test", Password: "pw"},
			[]string{"keycloak_url", "realm", "client_id"},
		},
		"neither password nor refresh token": {
			client.Config{
				Host: "localhost", Port: "50051", KeycloakURL: "http://ondewo.invalid", Realm: "r", ClientID: "c",
			},
			[]string{"password", "refresh_token"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			conn, err := client.NewChannel(tc.cfg)
			if conn != nil || err == nil {
				t.Fatalf("NewChannel accepted the config")
			}
			for _, want := range append(tc.want, "localhost:50051") {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestT55RefreshTokenIsTheLastFieldOfClientConfig(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeFor[client.Config]()
	if last := typ.Field(typ.NumField() - 1).Name; last != "RefreshToken" {
		t.Errorf("the last field of client.Config is %s, want RefreshToken", last)
	}
}

func TestT59ClientConfigRenderingsRedactThePasswordAndTheRefreshToken(t *testing.T) {
	t.Parallel()
	cfg := client.Config{
		Host: "localhost", Port: "50051", KeycloakURL: "https://kc", Realm: "r", ClientID: "c",
		Username: "u", Password: "pw-client-secret", RefreshToken: "off-client-secret",
	}
	var text bytes.Buffer
	slog.New(slog.NewJSONHandler(&text, nil)).Info("cfg", "cfg", cfg)
	for _, rendering := range []string{cfg.String(), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", &cfg), text.String()} {
		if strings.Contains(rendering, "pw-client-secret") || strings.Contains(rendering, "off-client-secret") ||
			strings.Count(rendering, "***REDACTED***") != 2 {
			t.Errorf("rendering %q", rendering)
		}
	}
	keycloak := cfg.KeycloakConfig()
	if keycloak.Password != "pw-client-secret" || keycloak.RefreshToken != "off-client-secret" || !keycloak.Configured() {
		t.Errorf("KeycloakConfig() = %v", keycloak)
	}
}
