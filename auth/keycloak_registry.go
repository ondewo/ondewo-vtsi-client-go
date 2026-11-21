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

package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"weak"
)

// registry holds one provider per credential set, WEAKLY: a provider no client references any
// more is collected, and its cleanup stops its background refresher.
//
// The key is derived from the credentials, never from an address: a registry keyed by the address
// of a config handed the PREVIOUS user's live provider to the next config allocated at the same
// address, which then silently authenticated as the first user.
var registry = struct {
	mu        sync.Mutex
	providers map[string]weak.Pointer[KeycloakTokenProvider]
}{providers: map[string]weak.Pointer[KeycloakTokenProvider]{}}

// registryKey is the SHA-256 (hex) of every field the provider is built from, joined with "\0".
// Hashed, so the key holds no secret; RefreshToken takes part, so two token-only configs (both
// with an EMPTY password) of two tenants never collapse onto one provider.
func registryKey(cfg KeycloakConfig) string {
	expiration := ""
	if cfg.TokenExpirationInS > 0 {
		expiration = strconv.Itoa(cfg.TokenExpirationInS)
	}
	verify := "True"
	if cfg.InsecureSkipVerify {
		verify = "False"
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{
		cfg.KeycloakURL, cfg.Realm, cfg.ClientID, cfg.Username, cfg.Password, expiration, verify, cfg.RefreshToken,
	}, "\x00")))

	return hex.EncodeToString(digest[:])
}

// SharedKeycloakTokenProvider returns the provider of cfg's credential set, logging in only the
// first time: every client and connection built from the same credentials shares one provider,
// one login and one refreshed token. opts apply only when the provider is created. A provider that
// was closed is never handed out again - a new one is created in its place.
func SharedKeycloakTokenProvider(cfg KeycloakConfig, opts ...KeycloakOption) (*KeycloakTokenProvider, error) {
	key := registryKey(cfg)

	registry.mu.Lock()
	defer registry.mu.Unlock()

	for existing, pointer := range registry.providers {
		if pointer.Value() == nil {
			delete(registry.providers, existing)
		}
	}
	if provider := registry.providers[key].Value(); provider != nil && !provider.state.closed.Load() {
		return provider, nil
	}

	provider, err := NewKeycloakTokenProvider(cfg, opts...)
	if err != nil {
		return nil, err
	}
	registry.providers[key] = weak.Make(provider)

	return provider, nil
}
