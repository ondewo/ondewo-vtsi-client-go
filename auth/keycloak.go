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
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/credentials"
)

// The Keycloak token-handling constants, identical in every ONDEWO client SDK.
const (
	// KeycloakExpiryLeeway: the access token is refreshed this long before it expires, so an
	// in-flight call never travels with a token that lapses mid-request.
	KeycloakExpiryLeeway = 30 * time.Second

	// KeycloakMinRefreshDelay is the floor of every background wait, so an expires_in of 0 cannot
	// spin the refresher into a hot loop.
	KeycloakMinRefreshDelay = 1 * time.Second

	// KeycloakRetryBaseDelay is the first wait after a FAILED refresh - deliberately far above the
	// 1 s floor: ondewo runs one client per call container, so a 1 Hz retry that is harmless for a
	// single process is an amplifier against the realm they all share.
	KeycloakRetryBaseDelay = 5 * time.Second

	// KeycloakRetryMaxDelay is the backoff ceiling: a persistent outage is retried at most this often.
	KeycloakRetryMaxDelay = 300 * time.Second

	// KeycloakMaxRetryExponent caps 2^n (5 s * 2^6 = 320 s already exceeds the ceiling), so a huge
	// failure count can never overflow.
	KeycloakMaxRetryExponent = 6

	// KeycloakHTTPTimeout bounds one whole token-endpoint call. A refresh runs under the provider
	// lock every RPC reads its token under, so this bounds how long a hung Keycloak can stall the
	// client's calls. It must stay below KeycloakExpiryLeeway.
	KeycloakHTTPTimeout = 10 * time.Second

	// KeycloakMaxErrorBodyChars is the longest slice of an error body quoted in an error: a proxy in
	// front of Keycloak can answer a 502 with a whole HTML page.
	KeycloakMaxErrorBodyChars = 500

	// KeycloakThreadJoinTimeout bounds how long Close waits for the background refresher.
	KeycloakThreadJoinTimeout = 5 * time.Second
)

// tokenEndpointPath follows the realm in the standard OIDC token endpoint of a Keycloak realm.
const tokenEndpointPath = "/protocol/openid-connect/token"

// maxTokenResponseBytes bounds how much of a token-endpoint answer is read at all.
const maxTokenResponseBytes = 1 << 20

// KeycloakAuthenticationError is the ONE error every failure of the Keycloak login, the refresh
// or the lazy read of an expired token surfaces as. The underlying error, if any, is reachable
// through errors.Unwrap / errors.Is / errors.As. Its text never contains a credential.
type KeycloakAuthenticationError struct {
	// Message is the complete error text.
	Message string

	// Err is the cause, or nil.
	Err error
}

// Error implements error.
func (e *KeycloakAuthenticationError) Error() string {
	return e.Message
}

// Unwrap returns the cause.
func (e *KeycloakAuthenticationError) Unwrap() error {
	return e.Err
}

// timings are the constants above, in seconds, per provider - so a test can shrink them without
// touching a package variable another test's background refresher reads concurrently.
type timings struct {
	expiryLeeway, minRefreshDelay, retryBaseDelay, retryMaxDelay float64
	maxRetryExponent                                             int
	httpTimeout, joinTimeout                                     time.Duration
}

var defaultTimings = timings{
	expiryLeeway:     KeycloakExpiryLeeway.Seconds(),
	minRefreshDelay:  KeycloakMinRefreshDelay.Seconds(),
	retryBaseDelay:   KeycloakRetryBaseDelay.Seconds(),
	retryMaxDelay:    KeycloakRetryMaxDelay.Seconds(),
	maxRetryExponent: KeycloakMaxRetryExponent,
	httpTimeout:      KeycloakHTTPTimeout,
	joinTimeout:      KeycloakThreadJoinTimeout,
}

// backoff is the wait, in seconds, before the next attempt after `failures` consecutive failed
// refreshes: uniform in [base, ceiling], the ceiling doubling from the base up to the maximum. The
// jitter is the load-bearing half - N containers whose refreshes fail in the same instant would
// otherwise retry in lockstep for as long as the outage lasts. A failure count below 1 counts as
// 1, and a fraction outside [0, 1] is clamped.
func (t timings) backoff(failures int, fraction float64) float64 {
	exponent := min(max(failures, 1)-1, t.maxRetryExponent)
	ceiling := min(t.retryBaseDelay*math.Pow(2, float64(exponent)), t.retryMaxDelay)
	fraction = min(max(fraction, 0), 1)

	return t.retryBaseDelay + fraction*(ceiling-t.retryBaseDelay)
}

// tokenTransport POSTs one form to the token endpoint. The default is net/http; the tests inject
// a fake so the whole flow runs without network.
type tokenTransport interface {
	post(ctx context.Context, endpoint string, form url.Values, timeout time.Duration) (status int, body []byte, err error)
}

// httpTransport is the net/http tokenTransport. TLS verification can be switched off for THIS
// client only - never process-wide.
type httpTransport struct {
	client *http.Client
}

func newHTTPTransport(insecureSkipVerify bool) *httpTransport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true} //nolint:gosec // opt-in, documented
	}

	return &httpTransport{client: &http.Client{Transport: transport}}
}

func (h *httpTransport) post(
	ctx context.Context, endpoint string, form url.Values, timeout time.Duration,
) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := h.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxTokenResponseBytes))
	if err != nil {
		return 0, nil, err
	}

	return response.StatusCode, body, nil
}

// KeycloakOption customises a KeycloakTokenProvider.
type KeycloakOption func(*keycloakOptions)

type keycloakOptions struct {
	logger          *slog.Logger
	transport       tokenTransport
	clock           func() float64
	jitter          func() float64
	wait            func(done <-chan struct{}, delay time.Duration) (stopped bool)
	timings         timings
	startBackground bool
}

// WithKeycloakLogger sends the provider's WARNING (failed refresh), INFO (recovery) and DEBUG
// lines to logger instead of slog.Default().
func WithKeycloakLogger(logger *slog.Logger) KeycloakOption {
	return func(o *keycloakOptions) {
		if logger != nil {
			o.logger = logger
		}
	}
}

// processStart anchors the monotonic clock: time.Since reads the monotonic reading of time.Now.
var processStart = time.Now()

func monotonicSeconds() float64 {
	return time.Since(processStart).Seconds()
}

// waitOrStop waits delay, or until done is closed; it reports whether done was closed.
func waitOrStop(done <-chan struct{}, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func seconds(value float64) time.Duration {
	return time.Duration(value * float64(time.Second))
}

// tokenState is everything the background refresher needs. It never points back at the
// KeycloakTokenProvider, so an unreachable provider is collected and its cleanup stops the worker.
type tokenState struct {
	mu sync.Mutex

	clientID, username, password string
	tokenExpirationInS           int
	endpoint                     string

	transport tokenTransport
	clock     func() float64
	jitter    func() float64
	wait      func(done <-chan struct{}, delay time.Duration) bool
	logger    *slog.Logger
	timings   timings

	accessToken   string
	refreshToken  string
	expiresAt     float64
	loginDeadline float64
	hasDeadline   bool

	failures      int
	nextAttemptAt float64

	done      chan struct{}
	stopOnce  sync.Once
	worker    sync.WaitGroup
	hasWorker bool
	// stopped: the refresher ended because the TokenExpirationInS window closed.
	stopped bool
	// closed: Close was called; the registry never hands such a provider to a new client.
	closed atomic.Bool
}

// KeycloakTokenProvider acquires a Keycloak access token once and keeps it fresh: proactively in
// a background goroutine that wakes KeycloakExpiryLeeway before expiry, and lazily on every read.
// Both paths share one lock and one failure backoff, so an outage never turns every RPC into a
// token request, and a still-valid token keeps being served while a refresh fails. A refused
// token is NEVER answered with a password login.
//
// It implements credentials.PerRPCCredentials (TLS only); PerRPCCredentials returns a variant
// that may travel over a plaintext connection.
type KeycloakTokenProvider struct {
	state *tokenState
}

// NewKeycloakTokenProvider validates cfg, performs the initial token acquisition - ONE refresh
// grant with cfg.RefreshToken when it is set, else ONE password grant - and starts the background
// refresher. A failed acquisition is returned as *KeycloakAuthenticationError, with no retry and
// no fallback. Close the provider when it is no longer needed; an unreachable provider is also
// stopped when it is garbage collected.
//
// Most callers want SharedKeycloakTokenProvider, which logs in once per credential set.
func NewKeycloakTokenProvider(cfg KeycloakConfig, opts ...KeycloakOption) (*KeycloakTokenProvider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !cfg.Configured() {
		return nil, fmt.Errorf("ondewo/auth: the Keycloak config sets none of KeycloakURL, Realm and ClientID")
	}

	options := keycloakOptions{
		logger:          slog.Default(),
		clock:           monotonicSeconds,
		jitter:          rand.Float64,
		wait:            waitOrStop,
		timings:         defaultTimings,
		startBackground: true,
	}
	for _, opt := range opts {
		opt(&options)
	}
	if options.transport == nil {
		options.transport = newHTTPTransport(cfg.InsecureSkipVerify)
	}

	state := &tokenState{
		clientID:           cfg.ClientID,
		username:           cfg.Username,
		password:           cfg.Password,
		tokenExpirationInS: cfg.TokenExpirationInS,
		endpoint:           cfg.normalizedURL() + "/realms/" + url.PathEscape(cfg.Realm) + tokenEndpointPath,
		transport:          options.transport,
		clock:              options.clock,
		jitter:             options.jitter,
		wait:               options.wait,
		logger:             options.logger,
		timings:            options.timings,
		refreshToken:       cfg.RefreshToken,
		done:               make(chan struct{}),
	}

	var err error
	if cfg.RefreshToken != "" {
		// Keycloak's brute-force detection counts failed LOGINS, so N processes refreshing one
		// offline token at once is not the burst the realm penalises, while N password grants is.
		err = state.attempt("refresh", state.refresh)
	} else {
		err = state.attempt("login", state.login)
	}
	if err != nil {
		return nil, err
	}
	if state.tokenExpirationInS > 0 {
		state.loginDeadline = state.clock() + float64(state.tokenExpirationInS)
		state.hasDeadline = true
	}

	provider := &KeycloakTokenProvider{state: state}
	if options.startBackground {
		state.hasWorker = true
		state.worker.Add(1)
		go state.run()
	}
	// The worker only holds the state, so this runs once the provider itself is unreachable.
	runtime.AddCleanup(provider, func(s *tokenState) { s.signalStop() }, state)

	return provider, nil
}

// AccessToken is the most recently issued access token, without refreshing it.
func (p *KeycloakTokenProvider) AccessToken() string {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()

	return p.state.accessToken
}

// AuthorizationMetadata refreshes the access token if it is inside the leeway and returns the
// gRPC metadata pair ("authorization", "Bearer <access token>"). It fails with
// *KeycloakAuthenticationError only once the token has expired and could not be refreshed.
func (p *KeycloakTokenProvider) AuthorizationMetadata() (key, value string, err error) {
	s := p.state
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.refreshIfWithinWindow(s.clock()); err != nil {
		return "", "", err
	}

	return authorizationHeader, "Bearer " + s.accessToken, nil
}

// GetRequestMetadata implements credentials.PerRPCCredentials.
//
// The caller's cancellation is deliberately NOT applied to the token request: the refresh is
// shared by every RPC of every client of this credential set, and one cancelled RPC must not put
// all of them into the failure backoff. The request stays bounded by KeycloakHTTPTimeout.
func (p *KeycloakTokenProvider) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	key, value, err := p.AuthorizationMetadata()
	if err != nil {
		return nil, err
	}

	return map[string]string{key: value}, nil
}

// RequireTransportSecurity implements credentials.PerRPCCredentials: the provider itself only
// travels over TLS. Use PerRPCCredentials(true) for a plaintext connection.
func (p *KeycloakTokenProvider) RequireTransportSecurity() bool {
	return true
}

// PerRPCCredentials returns the provider as credentials.PerRPCCredentials, allowed onto a
// plaintext connection when allowInsecureTransport is true (a local development server, or an
// ingress that terminates TLS in front of the server).
func (p *KeycloakTokenProvider) PerRPCCredentials(allowInsecureTransport bool) credentials.PerRPCCredentials {
	return keycloakCredentials{provider: p, allowInsecureTransport: allowInsecureTransport}
}

// Close stops the background refresher, interrupting a pending wait (a backoff included) so no
// further request is sent, and waits for it at most KeycloakThreadJoinTimeout. It is idempotent
// and safe from any state. The provider keeps working lazily afterwards.
func (p *KeycloakTokenProvider) Close() error {
	p.state.close()

	return nil
}

// Stop is an alias of Close.
func (p *KeycloakTokenProvider) Stop() {
	p.state.close()
}

type keycloakCredentials struct {
	provider               *KeycloakTokenProvider
	allowInsecureTransport bool
}

func (c keycloakCredentials) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return c.provider.GetRequestMetadata(ctx, uri...)
}

func (c keycloakCredentials) RequireTransportSecurity() bool {
	return !c.allowInsecureTransport
}

// signalStop closes done exactly once.
func (s *tokenState) signalStop() {
	s.stopOnce.Do(func() { close(s.done) })
}

// isClosed reports whether the provider was stopped (closed, collected or past its deadline).
func (s *tokenState) isClosed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *tokenState) close() {
	s.closed.Store(true)
	s.signalStop()
	if !s.hasWorker {
		return
	}
	finished := make(chan struct{})
	go func() {
		s.worker.Wait()
		close(finished)
	}()
	// Bounded: a refresh stuck in its HTTP call - or Close called from inside the worker itself -
	// can only delay this, never block it.
	timer := time.NewTimer(s.timings.joinTimeout)
	defer timer.Stop()
	select {
	case <-finished:
	case <-timer.C:
	}
}

// run is the background refresher. A failed refresh NEVER ends it: the failure was recorded,
// logged and scheduled by recordFailure, so the loop only has to keep going.
func (s *tokenState) run() {
	defer s.worker.Done()
	for !s.isClosed() {
		s.mu.Lock()
		now := s.clock()
		if s.hasDeadline && now >= s.loginDeadline {
			// The bounded auto-refresh window has closed: stop renewing and let the token lapse.
			s.stopped = true
			s.mu.Unlock()
			s.signalStop()

			return
		}
		delay := s.expiresAt - s.timings.expiryLeeway - now
		if s.failures > 0 {
			// A failed refresh leaves expiresAt unchanged, so the line above would retry at the 1 s
			// floor for the whole outage: wait for the backoff instead.
			delay = s.nextAttemptAt - now
		}
		delay = max(delay, s.timings.minRefreshDelay)
		if s.hasDeadline {
			delay = min(delay, s.loginDeadline-now)
		}
		s.mu.Unlock()

		if s.wait(s.done, seconds(delay)) {
			return
		}

		s.mu.Lock()
		err := s.refreshIfWithinWindow(s.clock())
		s.mu.Unlock()
		if err != nil {
			// Already recorded and warned about; logging it again would double every warning.
			s.logger.Debug(fmt.Sprintf("Keycloak background refresh will be retried (%s)", describe(err)))
		}
	}
}

// refreshIfWithinWindow is the decision shared by the lazy path and the background refresher.
// The caller holds mu.
func (s *tokenState) refreshIfWithinWindow(now float64) error {
	if s.hasDeadline && now >= s.loginDeadline {
		return nil
	}
	if now < s.expiresAt-s.timings.expiryLeeway {
		return nil
	}
	if now < s.nextAttemptAt {
		if now < s.expiresAt {
			return nil
		}

		return &KeycloakAuthenticationError{Message: fmt.Sprintf(
			"The Keycloak access token has expired and the refresh is backing off after %d consecutive "+
				"failure(s); the next attempt is due in %.1fs",
			s.failures, s.nextAttemptAt-now,
		)}
	}

	err := s.attempt("refresh", s.refresh)
	if err == nil {
		s.recordSuccess()

		return nil
	}
	s.recordFailure(err)
	// Re-read the clock: the failed attempt itself may have taken up to the HTTP timeout.
	if s.clock() < s.expiresAt {
		return nil
	}

	return err
}

// attempt runs one grant, turning a panic of the transport (a bug in an injected one) into the
// typed error, so neither the caller nor the background refresher can be taken down by it.
func (s *tokenState) attempt(action string, grant func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			cause, ok := recovered.(error)
			if !ok {
				cause = fmt.Errorf("%v", recovered)
			}
			err = &KeycloakAuthenticationError{
				Message: fmt.Sprintf("Keycloak token %s failed (%s)", action, describe(cause)),
				Err:     cause,
			}
		}
	}()

	return grant()
}

func (s *tokenState) recordFailure(err error) {
	now := s.clock()
	s.failures++
	delay := s.timings.backoff(s.failures, s.jitter())
	s.nextAttemptAt = now + delay
	remaining := s.expiresAt - now
	validity := "the current access token has expired"
	if remaining > 0 {
		validity = fmt.Sprintf("the current access token stays valid for %.1fs", remaining)
	}
	s.logger.Warn(fmt.Sprintf(
		"Keycloak token refresh failed (attempt %d); retrying in %.1fs, %s (%s)",
		s.failures, delay, validity, describe(err),
	))
}

func (s *tokenState) recordSuccess() {
	if s.failures > 0 {
		s.logger.Info(fmt.Sprintf("Keycloak token refresh recovered after %d failed attempt(s)", s.failures))
	}
	s.failures = 0
	s.nextAttemptAt = 0
}

func (s *tokenState) login() error {
	return s.exchange("login", url.Values{
		"grant_type": {"password"},
		"client_id":  {s.clientID},
		"username":   {s.username},
		"password":   {s.password},
		"scope":      {"offline_access"},
	})
}

func (s *tokenState) refresh() error {
	return s.exchange("refresh", url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {s.clientID},
		"refresh_token": {s.refreshToken},
	})
}

func (s *tokenState) exchange(action string, form url.Values) error {
	payload, err := s.postTokenRequest(action, form)
	if err != nil {
		return err
	}

	return s.storeTokens(payload)
}

// postTokenRequest POSTs the form and returns the JSON object it answered. Every way the exchange
// can fail is a *KeycloakAuthenticationError with the original error as its cause.
func (s *tokenState) postTokenRequest(action string, form url.Values) (map[string]any, error) {
	status, body, err := s.transport.post(context.Background(), s.endpoint, form, s.timings.httpTimeout)
	if err != nil {
		return nil, &KeycloakAuthenticationError{
			Message: fmt.Sprintf("Keycloak token %s could not reach the token endpoint (%s)", action, describe(err)),
			Err:     err,
		}
	}
	if status < 200 || status >= 300 {
		text := []rune(string(body))
		if len(text) > KeycloakMaxErrorBodyChars {
			text = text[:KeycloakMaxErrorBodyChars]
		}

		return nil, &KeycloakAuthenticationError{
			Message: fmt.Sprintf("Keycloak token %s failed with status %d: %s", action, status, string(text)),
		}
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, &KeycloakAuthenticationError{
			Message: fmt.Sprintf("Keycloak token %s returned status %d with a body that is not JSON", action, status),
			Err:     err,
		}
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return nil, &KeycloakAuthenticationError{
			Message: fmt.Sprintf("Keycloak token %s returned a JSON %s instead of an object", action, jsonType(decoded)),
		}
	}

	return object, nil
}

// storeTokens validates the whole response before it assigns anything, so a malformed response
// changes no state.
func (s *tokenState) storeTokens(payload map[string]any) error {
	accessToken, ok := payload["access_token"].(string)
	if !ok || accessToken == "" {
		// The field NAMES, never the values: a token response is made almost entirely of
		// credentials, and an offline refresh token mints access tokens for the session's life.
		names := make([]string, 0, len(payload))
		for name := range payload {
			names = append(names, "'"+name+"'")
		}
		sort.Strings(names)

		return &KeycloakAuthenticationError{Message: fmt.Sprintf(
			"Keycloak token response did not contain an access_token; the response carried the fields [%s] "+
				"(values withheld: a token response is credential material)",
			strings.Join(names, ", "),
		)}
	}
	expiresIn, err := expiresInSeconds(payload)
	if err != nil {
		return &KeycloakAuthenticationError{
			Message: "Keycloak token response carried a non-numeric expires_in (value withheld)",
			Err:     err,
		}
	}

	s.accessToken = accessToken
	// Keep the previous refresh token when the response omits it, so a same-token refresh does not
	// blank out the offline token.
	if refreshToken, ok := payload["refresh_token"].(string); ok && refreshToken != "" {
		s.refreshToken = refreshToken
	}
	s.expiresAt = s.clock() + max(expiresIn, 0)

	return nil
}

// expiresInSeconds reads expires_in: missing is 0, a number or a numeric string is accepted.
func expiresInSeconds(payload map[string]any) (float64, error) {
	raw, present := payload["expires_in"]
	if !present {
		return 0, nil
	}
	var value float64
	switch typed := raw.(type) {
	case float64:
		value = typed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, err
		}
		value = parsed
	default:
		return 0, fmt.Errorf("expires_in is a JSON %s", jsonType(raw))
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("expires_in is not finite")
	}

	return value, nil
}

// jsonType names the JSON type of a value decoded by encoding/json.
func jsonType(value any) string {
	switch value.(type) {
	case []any:
		return "array"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case map[string]any:
		return "object"
	default:
		return "null"
	}
}

// describe renders an error as "<Type>: <message>".
func describe(err error) string {
	return fmt.Sprintf("%T: %v", err, err)
}
