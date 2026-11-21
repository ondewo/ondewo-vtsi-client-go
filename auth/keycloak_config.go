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
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// redacted replaces a non-empty secret in every rendering of a KeycloakConfig.
const redacted = "***REDACTED***"

// KeycloakConfig holds the credentials of the headless Keycloak login every ONDEWO client SDK
// implements: a one-time password (ROPC) login with scope=offline_access against the PUBLIC SDK
// client, or - preferred when N processes share one technical user - a handed-off OFFLINE refresh
// token, after which the access token is refreshed in the background and attached to every RPC.
//
// Formatting a KeycloakConfig with any fmt verb, or logging it through log/slog, renders Password
// and RefreshToken as ***REDACTED*** (and as "" when empty). encoding/json does NOT: treat a
// serialized KeycloakConfig as a secret.
//
// New fields are only ever appended, and RefreshToken stays the LAST one, so an unkeyed literal
// written against an earlier release keeps binding its values to the same fields.
type KeycloakConfig struct {
	// KeycloakURL is the Keycloak base URL, the part before /realms/<realm>, e.g.
	// "https://host/auth". Trailing '/' characters are stripped.
	KeycloakURL string

	// Realm is the Keycloak realm, e.g. "ondewo-ccai-platform".
	Realm string

	// ClientID is the PUBLIC SDK client id, e.g. "ondewo-<api>-cai-sdk-public". No client secret
	// exists and none is ever sent.
	ClientID string

	// Username is the technical user of the password grant.
	Username string

	// Password is the technical user's password. SECRET. Ignored whenever RefreshToken is set.
	Password string

	// TokenExpirationInS bounds, in seconds from the initial token acquisition, how long the access
	// token is refreshed. Zero or negative means "absent": refresh until the offline session itself
	// dies.
	TokenExpirationInS int

	// InsecureSkipVerify disables the verification of Keycloak's TLS certificate on the
	// token-endpoint call ONLY (verify_ssl=False in the python SDKs) - for a self-signed local
	// Envoy. The zero value verifies.
	InsecureSkipVerify bool

	// RefreshToken is a handed-off Keycloak OFFLINE refresh token used instead of the password.
	// SECRET. When set, the provider starts with a refresh grant and NEVER sends a password grant,
	// not even as a fallback.
	RefreshToken string
}

// Configured reports whether Keycloak authentication is configured: KeycloakURL, Realm and
// ClientID are all set. When it is false no provider is built and no authorization metadata is
// attached.
func (c KeycloakConfig) Configured() bool {
	return c.KeycloakURL != "" && c.Realm != "" && c.ClientID != ""
}

// Validate checks the config without any network I/O. A config with none of the three Keycloak
// fields is valid (Keycloak is simply not used); a partial triple, or a configured triple with
// neither Password nor RefreshToken, is refused.
func (c KeycloakConfig) Validate() error {
	anySet := c.KeycloakURL != "" || c.Realm != "" || c.ClientID != ""
	if anySet && !c.Configured() {
		return errors.New(
			"The Keycloak fields `KeycloakURL` (keycloak_url), `Realm` (realm), and `ClientID` (client_id) " +
				"must be provided together in the Keycloak config.",
		)
	}
	if c.Configured() && c.Password == "" && c.RefreshToken == "" {
		return errors.New(
			"Either the field `Password` (password) or the field `RefreshToken` (refresh_token) is mandatory " +
				"in the Keycloak config.",
		)
	}

	return nil
}

// String renders the config with Password and RefreshToken redacted.
func (c KeycloakConfig) String() string {
	return fmt.Sprintf(
		"auth.KeycloakConfig{KeycloakURL: %q, Realm: %q, ClientID: %q, Username: %q, Password: %s, "+
			"TokenExpirationInS: %d, InsecureSkipVerify: %t, RefreshToken: %s}",
		c.KeycloakURL, c.Realm, c.ClientID, c.Username, secret(c.Password),
		c.TokenExpirationInS, c.InsecureSkipVerify, secret(c.RefreshToken),
	)
}

// Format implements fmt.Formatter, so that EVERY verb renders the redacted String - without it,
// %d or %#v would print the fields, the secrets included.
func (c KeycloakConfig) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, c.String())
}

// LogValue implements slog.LogValuer, so that a config logged through log/slog carries no secret.
func (c KeycloakConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("keycloak_url", c.KeycloakURL),
		slog.String("realm", c.Realm),
		slog.String("client_id", c.ClientID),
		slog.String("username", c.Username),
		slog.String("password", secret(c.Password)),
		slog.Int("token_expiration_in_s", c.TokenExpirationInS),
		slog.Bool("insecure_skip_verify", c.InsecureSkipVerify),
		slog.String("refresh_token", secret(c.RefreshToken)),
	)
}

// normalizedURL is KeycloakURL without its trailing '/' characters.
func (c KeycloakConfig) normalizedURL() string {
	return strings.TrimRight(c.KeycloakURL, "/")
}

// secret renders a secret as ***REDACTED***, and an empty one as empty: "nobody set it" is
// usually exactly what is being debugged.
func secret(value string) string {
	if value == "" {
		return `""`
	}

	return redacted
}
