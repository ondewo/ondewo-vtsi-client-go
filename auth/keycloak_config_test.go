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

// The Keycloak config, T55-T61 of SPEC.md section 3 (T62 runs end to end in tests/). Product
// agnostic.
package auth

import (
	"bytes"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
)

func TestT55RefreshTokenIsTheLastFieldOfTheConfig(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeFor[KeycloakConfig]()
	if last := typ.Field(typ.NumField() - 1).Name; last != "RefreshToken" {
		t.Errorf("the last field of KeycloakConfig is %s, want RefreshToken", last)
	}
	// An unkeyed literal binds positionally: it is the positional surface of a go struct.
	cfg := KeycloakConfig{testKeycloakURL, testRealm, testClientID, testUsername, "", 0, false, "off-1"}
	if cfg.RefreshToken != "off-1" {
		t.Errorf("the positional literal bound RefreshToken to %q", cfg.RefreshToken)
	}
}

func TestT56ATokenOnlyConfigIsValidAndConfigured(t *testing.T) {
	t.Parallel()
	cfg := tokenConfig("off-1", "")
	if err := cfg.Validate(); err != nil || !cfg.Configured() {
		t.Errorf("Validate() = %v, Configured() = %t, want nil and true", err, cfg.Configured())
	}
}

func TestT57ATripleWithNeitherPasswordNorTokenIsRefused(t *testing.T) {
	t.Parallel()
	err := tokenConfig("", "").Validate()
	if err == nil || !strings.Contains(err.Error(), "refresh_token") || !strings.Contains(err.Error(), "password") {
		t.Errorf("err = %v, want it to name password and refresh_token", err)
	}
}

func TestT58APasswordConfigIsUnchanged(t *testing.T) {
	t.Parallel()
	if err := passwordConfig().Validate(); err != nil || !passwordConfig().Configured() {
		t.Errorf("Validate() = %v", err)
	}
	if err := (KeycloakConfig{Username: "u", Password: "p"}).Validate(); err != nil {
		t.Errorf("a config without Keycloak is refused: %v", err)
	}
}

func TestT59RenderingsRedactBothSecretsAndShowEmptyOnesEmpty(t *testing.T) {
	t.Parallel()
	cfg := tokenConfig("off-render-secret", "pw-render-secret")
	var text, jsonLog bytes.Buffer
	slog.New(slog.NewTextHandler(&text, nil)).Info("cfg", "cfg", cfg)
	slog.New(slog.NewJSONHandler(&jsonLog, nil)).Info("cfg", "cfg", cfg)
	renderings := []string{cfg.String(), text.String(), jsonLog.String(), fmt.Sprintf("%+v", &cfg)}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d"} {
		renderings = append(renderings, fmt.Sprintf(verb, cfg))
	}
	for _, rendering := range renderings {
		if strings.Count(rendering, redacted) != 2 || strings.Contains(rendering, "off-render-secret") ||
			strings.Contains(rendering, "pw-render-secret") || !strings.Contains(rendering, testRealm) {
			t.Errorf("rendering %q", rendering)
		}
	}
	empty := KeycloakConfig{Realm: testRealm}.String()
	want := `auth.KeycloakConfig{KeycloakURL: "", Realm: "ondewo-test", ClientID: "", Username: "", Password: "", ` +
		`TokenExpirationInS: 0, InsecureSkipVerify: false, RefreshToken: ""}`
	if empty != want {
		t.Errorf("String() = %q, want %q", empty, want)
	}
}

func TestT61APartialTripleIsRefusedNamingTheThreeFields(t *testing.T) {
	t.Parallel()
	partials := []KeycloakConfig{
		{KeycloakURL: testKeycloakURL}, {Realm: testRealm}, {ClientID: testClientID},
		{KeycloakURL: testKeycloakURL, Realm: testRealm}, {KeycloakURL: testKeycloakURL, ClientID: testClientID},
		{Realm: testRealm, ClientID: testClientID},
	}
	for _, cfg := range partials {
		cfg.Password = testPassword
		err := cfg.Validate()
		if err == nil {
			t.Errorf("%v was accepted", cfg)

			continue
		}
		for _, name := range []string{"keycloak_url", "realm", "client_id"} {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("err = %v does not name %s", err, name)
			}
		}
	}
}
