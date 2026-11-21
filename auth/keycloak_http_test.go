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

// The default net/http transport against a real (in-process) token endpoint. Product agnostic.
package auth

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordedRequest struct {
	method, path, contentType, body string
}

func tokenServer(t *testing.T, tls bool, handler http.HandlerFunc) (*httptest.Server, *[]recordedRequest, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	var requests []recordedRequest
	wrapped := func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, recordedRequest{r.Method, r.URL.Path, r.Header.Get("Content-Type"), string(body)})
		mu.Unlock()
		handler(w, r)
	}
	var server *httptest.Server
	if tls {
		server = httptest.NewTLSServer(http.HandlerFunc(wrapped))
	} else {
		server = httptest.NewServer(http.HandlerFunc(wrapped))
	}
	t.Cleanup(server.Close)

	return server, &requests, &mu
}

func answerToken(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"access_token":"acc-http","expires_in":300,"refresh_token":"off-http"}`)
}

func TestTheDefaultTransportPostsTheFormToTheRealmTokenEndpoint(t *testing.T) {
	t.Parallel()
	server, requests, mu := tokenServer(t, false, answerToken)
	cfg := passwordConfig()
	cfg.KeycloakURL = server.URL + "/auth/"

	provider, err := NewKeycloakTokenProvider(cfg, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = provider.Close() }()

	if got := provider.AccessToken(); got != "acc-http" {
		t.Errorf("token = %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	request := (*requests)[0]
	if request.method != http.MethodPost || request.path != "/auth/realms/"+testRealm+"/protocol/openid-connect/token" ||
		request.contentType != "application/x-www-form-urlencoded" ||
		!strings.Contains(request.body, "grant_type=password") || !strings.Contains(request.body, "scope=offline_access") {
		t.Errorf("request = %+v", request)
	}
}

func TestTLSVerificationOfTheTokenEndpointIsOnByDefaultAndCanBeSkipped(t *testing.T) {
	t.Parallel()
	server, _, _ := tokenServer(t, true, answerToken)
	cfg := passwordConfig()
	cfg.KeycloakURL = server.URL

	_, err := NewKeycloakTokenProvider(cfg, quiet)
	if err == nil || !strings.Contains(err.Error(), "could not reach the token endpoint") {
		t.Fatalf("a self-signed Keycloak was trusted by default: %v", err)
	}

	cfg.InsecureSkipVerify = true
	provider, err := NewKeycloakTokenProvider(cfg, quiet)
	if err != nil {
		t.Fatalf("InsecureSkipVerify did not skip the verification: %v", err)
	}
	_ = provider.Close()
	// Scoped to that client only: the process-wide default transport still verifies.
	if transport := http.DefaultTransport.(*http.Transport); transport.TLSClientConfig != nil &&
		transport.TLSClientConfig.InsecureSkipVerify {
		t.Errorf("the switch leaked into http.DefaultTransport")
	}
}

func TestTheDefaultTransportReportsAnUnusableURLAndATruncatedBody(t *testing.T) {
	t.Parallel()
	cfg := passwordConfig()
	cfg.KeycloakURL = "http://bad host\x7f"
	if _, err := NewKeycloakTokenProvider(cfg, quiet); err == nil || !strings.Contains(err.Error(), "could not reach the token endpoint") {
		t.Errorf("an unusable URL: err = %v", err)
	}

	// A body shorter than its Content-Length: the read fails.
	server, _, _ := tokenServer(t, false, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, `{"access_token"`)
	})
	cfg.KeycloakURL = server.URL
	_, err := NewKeycloakTokenProvider(cfg, quiet)
	if err == nil || !strings.Contains(err.Error(), "could not reach the token endpoint") || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("a truncated body: err = %v", err)
	}
}

func TestTheDefaultTransportTimesOut(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		// Accept and never answer.
		conn, err := listener.Accept()
		if err == nil {
			defer func() { _ = conn.Close() }()
			time.Sleep(2 * time.Second)
		}
	}()
	shrunk := defaultTimings
	shrunk.httpTimeout = 50 * time.Millisecond
	cfg := passwordConfig()
	cfg.KeycloakURL = "http://" + listener.Addr().String()

	started := time.Now()
	_, err = NewKeycloakTokenProvider(cfg, withTimings(shrunk), quiet)
	if err == nil || !strings.Contains(err.Error(), "could not reach the token endpoint") || time.Since(started) > time.Second {
		t.Errorf("err = %v after %v, want a timeout within the HTTP timeout", err, time.Since(started))
	}
}
