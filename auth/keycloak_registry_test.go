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

// The shared-provider registry, T48-T54 of SPEC.md section 3, plus T18's "a collected provider
// ends the loop". Every test uses its own KeycloakURL, so the parallel tests never share a key.
package auth

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// registrySequence keeps the keys unique across `go test -count=N` repetitions too.
var registrySequence atomic.Int64

func registryConfig(t *testing.T) KeycloakConfig {
	cfg := passwordConfig()
	cfg.KeycloakURL = fmt.Sprintf("https://%s-%d.example.com/auth", strings.ReplaceAll(t.Name(), "/", "-"),
		registrySequence.Add(1))

	return cfg
}

func shared(t *testing.T, cfg KeycloakConfig, transport *fakeTransport, clock *fakeClock) *KeycloakTokenProvider {
	t.Helper()
	provider, err := SharedKeycloakTokenProvider(cfg, withFakes(transport, clock, 0))
	if err != nil {
		t.Fatalf("SharedKeycloakTokenProvider failed: %v", err)
	}

	return provider
}

func registered(key string) bool {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	_, ok := registry.providers[key]

	return ok
}

func TestT48TheSameCredentialsShareOneProvider(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, token("acc-1"))
	cfg := registryConfig(t)

	first := shared(t, cfg, transport, clock)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if again := shared(t, cfg, transport, clock); again != first {
				t.Errorf("the same credentials got another provider")
			}
		})
	}
	wg.Wait()

	if got := len(transport.grants()); got != 1 {
		t.Errorf("logins = %d, want 1", got)
	}
}

func TestT49DifferentCredentialsNeverShareAProvider(t *testing.T) {
	t.Parallel()
	base := registryConfig(t)
	variants := map[string]func(*KeycloakConfig){
		"username":         func(c *KeycloakConfig) { c.Username = "other" },
		"password":         func(c *KeycloakConfig) { c.Password = "other" },
		"realm":            func(c *KeycloakConfig) { c.Realm = "other" },
		"keycloak_url":     func(c *KeycloakConfig) { c.KeycloakURL += "/other" },
		"client_id":        func(c *KeycloakConfig) { c.ClientID = "other" },
		"token_expiration": func(c *KeycloakConfig) { c.TokenExpirationInS = 60 },
		"verify_ssl":       func(c *KeycloakConfig) { c.InsecureSkipVerify = true },
		"refresh_token":    func(c *KeycloakConfig) { c.RefreshToken = "off-other" },
	}
	clock := newFakeClock()
	baseProvider := shared(t, base, newFakeTransport(clock, token("acc-1")), clock)
	for name, mutate := range variants {
		cfg := base
		mutate(&cfg)
		if registryKey(cfg) == registryKey(base) {
			t.Errorf("%s does not take part in the key", name)
		}
		if provider := shared(t, cfg, newFakeTransport(clock, token("acc-2")), clock); provider == baseProvider {
			t.Errorf("a different %s shared the provider", name)
		}
	}
}

func TestT50ConfigsCreatedAndDroppedInALoopNeverHandOverAProvider(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, token("acc-1"))
	base := registryConfig(t)
	for i := range 50 {
		cfg := base
		cfg.Username = fmt.Sprintf("user-%02d", i)
		_ = shared(t, cfg, transport, clock)
	}

	if got := len(transport.grants()); got != 50 {
		t.Fatalf("constructions = %d, want 50", got)
	}
	for i := range 50 {
		if got, want := transport.form(i).Get("username"), fmt.Sprintf("user-%02d", i); got != want {
			t.Errorf("construction %d logged in as %q, want %q", i, got, want)
		}
	}
}

func TestT51TwoOfflineTokensWithNoPasswordNeverShareAProvider(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, token("acc-1"))
	base := registryConfig(t)
	base.Password = ""
	a, b := base, base
	a.RefreshToken, b.RefreshToken = "off-tenant-a", "off-tenant-b"

	if shared(t, a, transport, clock) == shared(t, b, transport, clock) {
		t.Fatalf("two tenants' tokens share one provider")
	}
	if transport.form(0).Get("refresh_token") != "off-tenant-a" || transport.form(1).Get("refresh_token") != "off-tenant-b" {
		t.Errorf("forms = %v / %v", transport.form(0), transport.form(1))
	}
}

func TestT52T53TheKeyHoldsNoSecretAndIsASHA256Hex(t *testing.T) {
	t.Parallel()
	cfg := passwordConfig()
	cfg.RefreshToken = "off-secret-in-key"
	key := registryKey(cfg)
	if strings.Contains(key, "off-secret-in-key") || strings.Contains(key, testPassword) {
		t.Errorf("the key %q contains a secret", key)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(key) {
		t.Errorf("the key %q is not 64 hex characters", key)
	}
	// "\0"-joined, so a value cannot slide into the neighbouring field.
	shifted := cfg
	shifted.Realm, shifted.ClientID = cfg.Realm+cfg.ClientID[:1], cfg.ClientID[1:]
	if registryKey(shifted) == key {
		t.Errorf("shifting a character between fields keeps the key")
	}
}

func TestT54AClosedProviderIsNeverHandedToANewClient(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, token("acc-1"))
	cfg := registryConfig(t)
	first := shared(t, cfg, transport, clock)
	_ = first.Close()

	second := shared(t, cfg, transport, clock)

	if second == first || second.state.closed.Load() {
		t.Errorf("a closed provider was handed out again")
	}
	if got := len(transport.grants()); got != 2 {
		t.Errorf("logins = %d, want 2", got)
	}
}

func TestAFailedCreationIsNotRegistered(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	cfg := registryConfig(t)
	if _, err := SharedKeycloakTokenProvider(cfg, withFakes(newFakeTransport(clock, refused()), clock, 0)); err == nil {
		t.Fatalf("a refused login built a provider")
	}
	if registered(registryKey(cfg)) {
		t.Errorf("a failed creation left a registry entry")
	}
}

// TestT18ACollectedProviderEndsTheLoopAndLeavesTheRegistry is the go form of the python weakref:
// the worker holds only the provider's state, so an unreachable provider is collected, its
// cleanup stops the worker, and the next lookup prunes the dead registry entry.
func TestT18ACollectedProviderEndsTheLoopAndLeavesTheRegistry(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, token("acc-1"))
	cfg := registryConfig(t)
	state, key := func() (*tokenState, string) {
		provider, err := SharedKeycloakTokenProvider(cfg, func(o *keycloakOptions) { o.transport = transport }, quiet)
		if err != nil {
			t.Fatal(err)
		}

		return provider.state, registryKey(cfg)
	}()

	deadline := time.Now().Add(10 * time.Second)
	for !state.isClosed() && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(5 * time.Millisecond)
	}
	if !state.isClosed() {
		t.Fatalf("the unreachable provider was never collected")
	}
	state.worker.Wait()
	if got := len(transport.grants()); got != 1 {
		t.Errorf("requests = %d, want only the login", got)
	}

	other := registryConfig(t)
	_ = shared(t, other, newFakeTransport(clock, token("acc-2")), clock)
	if registered(key) {
		t.Errorf("the collected provider's entry was not pruned")
	}
}
