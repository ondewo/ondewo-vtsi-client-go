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

// The Keycloak token-provider test catalogue, T01-T54 of SPEC.md section 3, mirroring
// ondewo-*-client-python tests/unit/utils/test_keycloak_refresh_resilience.py. Product agnostic.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// region A. Backoff ladder

func TestT01TheCeilingDoublesFromTheBaseAndStopsAtTheMaximum(t *testing.T) {
	t.Parallel()
	want := []float64{5, 10, 20, 40, 80, 160, 300, 300, 300, 300}
	for n := 1; n <= 10; n++ {
		if got := defaultTimings.backoff(n, 1.0); got != want[n-1] {
			t.Errorf("backoff(%d, 1.0) = %v, want %v", n, got, want[n-1])
		}
	}
}

func TestT02AZeroFractionAlwaysWaitsTheBase(t *testing.T) {
	t.Parallel()
	for n := 1; n <= 19; n++ {
		if got := defaultTimings.backoff(n, 0); got != 5 {
			t.Errorf("backoff(%d, 0) = %v, want 5", n, got)
		}
	}
}

func TestT03TheDelayIsDrawnInsideTheWindow(t *testing.T) {
	t.Parallel()
	if got := defaultTimings.backoff(2, 0.5); got != 7.5 {
		t.Errorf("backoff(2, 0.5) = %v, want 7.5", got)
	}
	if got := defaultTimings.backoff(3, 0.25); got != 8.75 {
		t.Errorf("backoff(3, 0.25) = %v, want 8.75", got)
	}
}

func TestT04AFaultyJitterSourceIsClampedIntoTheWindow(t *testing.T) {
	t.Parallel()
	for _, fraction := range []float64{-1, -0.001, 1.5, 1e9} {
		if got := defaultTimings.backoff(4, fraction); got < 5 || got > 40 {
			t.Errorf("backoff(4, %v) = %v, want within [5, 40]", fraction, got)
		}
	}
}

func TestT05ANonPositiveFailureCountIsTheFirstFailure(t *testing.T) {
	t.Parallel()
	for _, n := range []int{0, -3} {
		if got := defaultTimings.backoff(n, 1.0); got != 5 {
			t.Errorf("backoff(%d, 1.0) = %v, want 5", n, got)
		}
	}
}

func TestT06AHugeFailureCountNeitherOverflowsNorExceedsTheCeiling(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1_000_000_000, int(^uint(0) >> 1)} {
		if got := defaultTimings.backoff(n, 1.0); got != 300 {
			t.Errorf("backoff(%d, 1.0) = %v, want 300", n, got)
		}
	}
}

func TestT07TheConstantsKeepTheirDocumentedValuesAndRelations(t *testing.T) {
	t.Parallel()
	if KeycloakExpiryLeeway != 30*time.Second || KeycloakMinRefreshDelay != time.Second ||
		KeycloakRetryBaseDelay != 5*time.Second || KeycloakRetryMaxDelay != 300*time.Second ||
		KeycloakMaxRetryExponent != 6 || KeycloakHTTPTimeout != 10*time.Second ||
		KeycloakMaxErrorBodyChars != 500 || KeycloakThreadJoinTimeout != 5*time.Second {
		t.Errorf("a Keycloak constant moved away from SPEC.md table 2.1")
	}
	if KeycloakRetryBaseDelay <= KeycloakMinRefreshDelay {
		t.Errorf("the retry base must stay above the 1 s floor")
	}
	if KeycloakRetryBaseDelay*(1<<KeycloakMaxRetryExponent) < KeycloakRetryMaxDelay {
		t.Errorf("the exponent cap must reach the ceiling")
	}
	if KeycloakHTTPTimeout >= KeycloakExpiryLeeway {
		t.Errorf("the HTTP timeout must stay below the leeway")
	}
	if defaultTimings.expiryLeeway != 30 || defaultTimings.minRefreshDelay != 1 || defaultTimings.retryBaseDelay != 5 ||
		defaultTimings.retryMaxDelay != 300 || defaultTimings.maxRetryExponent != 6 ||
		defaultTimings.httpTimeout != 10*time.Second || defaultTimings.joinTimeout != 5*time.Second {
		t.Errorf("defaultTimings = %+v, want the constants", defaultTimings)
	}
}

// endregion

// region B. The background loop survives every failure

func TestT08ARefusedRefreshIsRetriedAndTheProviderRecovers(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, token("acc-1"), refused(), token("acc-3"))
	provider := build(t, passwordConfig(), transport, clock, 0)

	runLoop(provider, &scriptedWaiter{results: timedOut(2), clock: clock})

	if got, want := transport.grants(), []string{"password", "refresh_token", "refresh_token"}; !equalSlices(got, want) {
		t.Errorf("grants = %v, want %v", got, want)
	}
	if access, _, _, failures := provider.snapshot(); access != "acc-3" || failures != 0 {
		t.Errorf("token = %q, failures = %d, want acc-3 and 0", access, failures)
	}
}

func TestT09NoKindOfFailureEndsTheLoop(t *testing.T) {
	t.Parallel()
	failures := map[string]step{
		"network error":          {err: errors.New("network is unreachable")},
		"connection refused":     {err: syscall.ECONNREFUSED},
		"timeout":                {err: context.DeadlineExceeded},
		"a bug in the transport": {panicWith: errors.New("transport bug")},
		"503 with a JSON body":   unavailable(),
		"502 with an empty body": {status: 502},
		"200 with HTML":          {status: 200, body: "<html>bad gateway</html>"},
		"200 with a JSON array":  {status: 200, body: `["acc"]`},
		"200 without a token":    {status: 200, body: `{"expires_in":300}`},
		"200 with expires_in soon": {
			status: 200, body: `{"access_token":"acc-bad","expires_in":"soon"}`,
		},
	}
	for name, failure := range failures {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			clock := newFakeClock()
			transport := newFakeTransport(clock, token("acc-1"), failure, token("acc-ok"))
			provider := build(t, passwordConfig(), transport, clock, 0)

			runLoop(provider, &scriptedWaiter{results: timedOut(2), clock: clock})

			if got := len(transport.grants()); got != 3 {
				t.Errorf("requests = %d, want 3", got)
			}
			if access, _, _, _ := provider.snapshot(); access != "acc-ok" {
				t.Errorf("token = %q, want acc-ok", access)
			}
		})
	}
}

func TestT10APersistentFailureKeepsTheLoopAlive(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, token("acc-1"), refused())
	provider := build(t, passwordConfig(), transport, clock, 0)

	runLoop(provider, &scriptedWaiter{results: timedOut(6), clock: clock})

	want := append([]string{"password"}, repeat("refresh_token", 6)...)
	if got := transport.grants(); !equalSlices(got, want) {
		t.Errorf("grants = %v, want %v", got, want)
	}
	if access, _, _, failures := provider.snapshot(); access != "acc-1" || failures != 6 {
		t.Errorf("token = %q, failures = %d, want acc-1 and 6", access, failures)
	}
}

func TestT11NoFailureIsEverAnsweredWithAPasswordGrant(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, token("acc-1"), step{status: 401, body: `{"error":"invalid_client"}`})
	provider := build(t, passwordConfig(), transport, clock, 0)

	runLoop(provider, &scriptedWaiter{results: timedOut(10), clock: clock})

	passwordGrants := 0
	for _, grant := range transport.grants() {
		if grant == "password" {
			passwordGrants++
		}
	}
	if passwordGrants != 1 {
		t.Errorf("password grants = %d, want exactly 1 (the login)", passwordGrants)
	}
}

// endregion

// region C. The background loop backs off

func TestT12TheFirstRetryWaitsTheBaseNotTheFloor(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	provider := build(t, passwordConfig(), newFakeTransport(clock, token("acc-1"), unavailable()), clock, 0)
	waiter := &scriptedWaiter{results: timedOut(2), clock: clock}

	runLoop(provider, waiter)

	if waiter.delays[0] != 270 || waiter.delays[1] != 5 {
		t.Errorf("waits = %v, want 270 then 5", waiter.delays)
	}
}

func TestT13ConsecutiveFailuresClimbTheLadderToTheCeiling(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	provider := build(t, passwordConfig(), newFakeTransport(clock, token("acc-1"), unavailable()), clock, 1.0)
	waiter := &scriptedWaiter{results: timedOut(10), clock: clock}

	runLoop(provider, waiter)

	if got, want := waiter.delays[1:10], []float64{5, 10, 20, 40, 80, 160, 300, 300, 300}; !equalSlices(got, want) {
		t.Errorf("waits after the first = %v, want %v", got, want)
	}
}

func TestT14ASuccessResetsTheLadderToTheHealthySchedule(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	logs, logger := captureLogs()
	transport := newFakeTransport(clock, token("acc-1"), unavailable(), unavailable(),
		tokenStep("acc-4", 120, "off-x"), unavailable())
	provider := build(t, passwordConfig(), transport, clock, 1.0, withLogger(logger))
	waiter := &scriptedWaiter{results: timedOut(5), clock: clock}

	runLoop(provider, waiter)

	if got, want := waiter.delays[:5], []float64{270, 5, 10, 90, 5}; !equalSlices(got, want) {
		t.Errorf("waits = %v, want %v", got, want)
	}
	if got := strings.Count(logs.String(), "recovered after 2 failed attempt(s)"); got != 1 {
		t.Errorf("recovery lines = %d, want exactly 1:\n%s", got, logs)
	}
	if got := strings.Count(logs.String(), "level=INFO"); got != 1 {
		t.Errorf("INFO lines = %d, want exactly 1:\n%s", got, logs)
	}
}

func TestT15TheBackoffIsMeasuredFromWhenTheFailureWasRecorded(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	slow := unavailable()
	slow.advance = 10
	provider := build(t, passwordConfig(), newFakeTransport(clock, token("acc-1"), slow), clock, 0)
	waiter := &scriptedWaiter{results: timedOut(2), clock: clock}

	runLoop(provider, waiter)

	if waiter.delays[1] != 5 {
		t.Errorf("wait after a 10 s failed request = %v, want 5", waiter.delays[1])
	}
}

func TestT16TheLoginDeadlineStillEndsTheLoopWhileItBacksOff(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	cfg := passwordConfig()
	cfg.TokenExpirationInS = 100
	provider := build(t, cfg, newFakeTransport(clock, tokenStep("acc-1", 30, "off-x"), unavailable()), clock, 1.0)
	waiter := &scriptedWaiter{results: timedOut(1000), clock: clock}

	runLoop(provider, waiter)

	if len(waiter.results) == 0 {
		t.Fatalf("the loop only ended because the script ran out")
	}
	if clock.read() > 1101 || !provider.state.stopped || !provider.state.isClosed() {
		t.Errorf("clock = %v, stopped = %t, want <= 1101 and stopped", clock.read(), provider.state.stopped)
	}
	for _, delay := range waiter.delays {
		if delay > 100 {
			t.Errorf("a wait of %v exceeds the deadline", delay)
		}
	}
}

func TestT17StopDuringABackoffWaitEndsTheLoopWithoutAnotherRequest(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, token("acc-1"), unavailable())
	provider := build(t, passwordConfig(), transport, clock, 0)

	runLoop(provider, &scriptedWaiter{results: []bool{false, true}, advanceBy: []float64{270, 1}, clock: clock})

	if got, want := transport.grants(), []string{"password", "refresh_token"}; !equalSlices(got, want) {
		t.Errorf("grants = %v, want %v", got, want)
	}
}

func TestT18AClosedProviderEndsTheLoopWithoutARequest(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, token("acc-1"))
	provider := build(t, passwordConfig(), transport, clock, 0)
	provider.state.signalStop()

	runLoop(provider, &scriptedWaiter{results: timedOut(5), clock: clock})

	if got := transport.grants(); len(got) != 1 {
		t.Errorf("grants = %v, want only the login", got)
	}
}

// endregion

// region D. The lazy path shares the backoff

func TestT19AStillValidTokenIsServedWhenItsRefreshFails(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	logs, logger := captureLogs()
	provider := build(t, passwordConfig(), newFakeTransport(clock, token("acc-1"), unavailable()), clock, 0, withLogger(logger))
	clock.advance(271)

	key, value, err := provider.AuthorizationMetadata()

	if err != nil || key != "authorization" || value != "Bearer acc-1" {
		t.Fatalf("AuthorizationMetadata() = %q, %q, %v, want authorization, Bearer acc-1, nil", key, value, err)
	}
	if got := strings.Count(logs.String(), "level=WARN"); got != 1 || !strings.Contains(logs.String(), "refresh failed (attempt 1)") {
		t.Errorf("want exactly one WARNING about attempt 1, got:\n%s", logs)
	}
	if !strings.Contains(logs.String(), "stays valid for 29.0s") {
		t.Errorf("the warning does not state the remaining validity:\n%s", logs)
	}
}

func TestT20NoRequestIsSentWhileTheBackoffRuns(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, token("acc-1"), unavailable())
	provider := build(t, passwordConfig(), transport, clock, 0)
	clock.advance(275)

	for range 100 {
		if _, _, err := provider.AuthorizationMetadata(); err != nil {
			t.Fatalf("AuthorizationMetadata failed: %v", err)
		}
		clock.advance(0.04)
	}

	if got, want := transport.grants(), []string{"password", "refresh_token"}; !equalSlices(got, want) {
		t.Errorf("grants = %v, want %v", got, want)
	}
}

func TestT21AnExpiredTokenRaisesDuringTheBackoffWithoutARequest(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, token("acc-1"), unavailable())
	provider := build(t, passwordConfig(), transport, clock, 1.0)
	clock.advance(299)
	if _, _, err := provider.AuthorizationMetadata(); err != nil {
		t.Fatalf("a token valid for 1 s more was not served: %v", err)
	}
	clock.advance(2)

	_, _, err := provider.AuthorizationMetadata()

	var authErr *KeycloakAuthenticationError
	if !errors.As(err, &authErr) || !strings.Contains(err.Error(), "backing off after 1 consecutive failure") {
		t.Fatalf("err = %v, want the typed backing-off error", err)
	}
	if !strings.Contains(err.Error(), "the next attempt is due in 3.0s") {
		t.Errorf("err = %v, want the due time with one decimal", err)
	}
	if got := len(transport.grants()); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
}

func TestT22AnExpiredTokenWhoseRefreshFailsRaises(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	logs, logger := captureLogs()
	provider := build(t, passwordConfig(), newFakeTransport(clock, token("acc-1"), refused()), clock, 0, withLogger(logger))
	clock.advance(400)

	_, err := provider.GetRequestMetadata(t.Context())

	var authErr *KeycloakAuthenticationError
	if !errors.As(err, &authErr) || !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("err = %v, want the typed error naming status 400", err)
	}
	if !strings.Contains(logs.String(), "the current access token has expired") {
		t.Errorf("the warning does not say the token expired:\n%s", logs)
	}
}

func TestT23ANonKeycloakErrorReachesTheCallerAsAKeycloakError(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	bug := errors.New("transport bug")
	provider := build(t, passwordConfig(), newFakeTransport(clock, token("acc-1"), step{panicWith: bug}), clock, 0)
	clock.advance(400)

	_, _, err := provider.AuthorizationMetadata()

	var authErr *KeycloakAuthenticationError
	if !errors.As(err, &authErr) || !errors.Is(err, bug) {
		t.Fatalf("err = %v, want the typed error caused by the transport's error", err)
	}
	if !strings.Contains(err.Error(), "Keycloak token refresh failed (*errors.errorString: transport bug)") {
		t.Errorf("err = %v", err)
	}
}

func TestT24TheLazyPathRetriesOnceTheBackoffElapsesAndRecovers(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	logs, logger := captureLogs()
	provider := build(t, passwordConfig(), newFakeTransport(clock, token("acc-1"), unavailable(), token("acc-3")), clock, 0,
		withLogger(logger))
	clock.advance(280)
	if _, _, err := provider.AuthorizationMetadata(); err != nil {
		t.Fatalf("AuthorizationMetadata failed: %v", err)
	}
	clock.advance(5)

	_, value, err := provider.AuthorizationMetadata()

	if err != nil || value != "Bearer acc-3" {
		t.Fatalf("value = %q, err = %v, want Bearer acc-3", value, err)
	}
	if _, _, _, failures := provider.snapshot(); failures != 0 {
		t.Errorf("failures = %d, want 0", failures)
	}
	if !strings.Contains(logs.String(), "recovered after 1 failed attempt") {
		t.Errorf("no recovery line:\n%s", logs)
	}
}

func TestT25ALazyFailureSchedulesTheBackgroundLoopsNextWake(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	provider := build(t, passwordConfig(), newFakeTransport(clock, token("acc-1"), unavailable(), token("acc-3")), clock, 1.0)
	clock.advance(280)
	if _, _, err := provider.AuthorizationMetadata(); err != nil {
		t.Fatalf("AuthorizationMetadata failed: %v", err)
	}
	clock.advance(2)
	waiter := &scriptedWaiter{results: timedOut(1), clock: clock}

	runLoop(provider, waiter)

	if !almostEqual(waiter.delays[0], 3.0) {
		t.Errorf("first wait = %v, want 3.0", waiter.delays[0])
	}
	if access, _, _, _ := provider.snapshot(); access != "acc-3" {
		t.Errorf("token = %q, want acc-3", access)
	}
}

func TestT26ConcurrentCallsDuringAnOutageSendOneRequestAndNeverFail(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, tokenStep("acc-1", 20, "off-x"), step{err: errors.New("network is unreachable")})
	transport.delay = 50 * time.Millisecond
	provider := build(t, passwordConfig(), transport, clock, 0)

	var wg sync.WaitGroup
	values := make(chan string, 16*20)
	failures := make(chan error, 16*20)
	for range 16 {
		wg.Go(func() {
			for range 20 {
				_, value, err := provider.AuthorizationMetadata()
				if err != nil {
					failures <- err
				}
				values <- value
			}
		})
	}
	wg.Wait()
	close(values)
	close(failures)

	for err := range failures {
		t.Errorf("a concurrent caller failed: %v", err)
	}
	count := 0
	for value := range values {
		count++
		if value != "Bearer acc-1" {
			t.Errorf("value = %q, want Bearer acc-1", value)
		}
	}
	if count != 320 {
		t.Errorf("values = %d, want 320", count)
	}
	if got, want := transport.grants(), []string{"password", "refresh_token"}; !equalSlices(got, want) {
		t.Errorf("grants = %v, want %v", got, want)
	}
}

// endregion

// region E. Response validation

func newProviderError(t *testing.T, cfg KeycloakConfig, steps ...step) error {
	t.Helper()
	clock := newFakeClock()
	_, err := NewKeycloakTokenProvider(cfg, withFakes(newFakeTransport(clock, steps...), clock, 0))
	if err == nil {
		t.Fatalf("NewKeycloakTokenProvider succeeded, want an error")
	}
	var authErr *KeycloakAuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatalf("err = %T %v, want *KeycloakAuthenticationError", err, err)
	}

	return err
}

func TestT27ANetworkFailureAtLoginIsAKeycloakErrorWithItsCause(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{errors.New("network is unreachable"), syscall.ECONNREFUSED, context.DeadlineExceeded} {
		err := newProviderError(t, passwordConfig(), step{err: cause})
		if !strings.Contains(err.Error(), "Keycloak token login could not reach the token endpoint") || !errors.Is(err, cause) {
			t.Errorf("err = %v, want 'could not reach the token endpoint' caused by %v", err, cause)
		}
	}
}

func TestT28ANonJSONBodyIsAKeycloakError(t *testing.T) {
	t.Parallel()
	err := newProviderError(t, passwordConfig(), step{status: 200, body: "<html>bad gateway</html>"})
	if !strings.Contains(err.Error(), "returned status 200 with a body that is not JSON") || errors.Unwrap(err) == nil {
		t.Errorf("err = %v", err)
	}
}

func TestT29AJSONBodyThatIsNotAnObjectIsAKeycloakError(t *testing.T) {
	t.Parallel()
	for body, kind := range map[string]string{`["acc"]`: "array", `"acc"`: "string", `5`: "number", `true`: "boolean", `null`: "null"} {
		err := newProviderError(t, passwordConfig(), step{status: 200, body: body})
		if want := "returned a JSON " + kind + " instead of an object"; !strings.Contains(err.Error(), want) {
			t.Errorf("body %s: err = %v, want %q", body, err, want)
		}
	}
}

func TestT30AHugeErrorPageIsTruncatedInTheMessage(t *testing.T) {
	t.Parallel()
	err := newProviderError(t, passwordConfig(), step{status: 502, body: strings.Repeat("<", 10000)})
	if len(err.Error()) >= 500+200 || !strings.Contains(err.Error(), "failed with status 502") {
		t.Errorf("len(err) = %d, want < 700: %.120s", len(err.Error()), err)
	}
}

func TestT31AResponseWithoutAnAccessTokenNamesFieldsNeverValues(t *testing.T) {
	t.Parallel()
	err := newProviderError(t, passwordConfig(),
		step{status: 200, body: `{"refresh_token":"secret-offline-token-77","expires_in":300}`})
	want := "Keycloak token response did not contain an access_token; the response carried the fields " +
		"['expires_in', 'refresh_token'] (values withheld: a token response is credential material)"
	if err.Error() != want || strings.Contains(err.Error(), "secret-offline-token-77") {
		t.Errorf("err = %q, want %q", err, want)
	}
	if err := newProviderError(t, passwordConfig(), step{status: 200, body: `{"access_token":7}`}); !strings.Contains(err.Error(), "['access_token']") {
		t.Errorf("a non-string access_token: err = %v", err)
	}
}

func TestT32ANonNumericExpiresInLeavesThePreviousTokenUntouched(t *testing.T) {
	t.Parallel()
	for _, expiresIn := range []string{`"soon"`, `true`, `null`, `{}`, `"NaN"`, `"Infinity"`} {
		clock := newFakeClock()
		provider := build(t, passwordConfig(), newFakeTransport(clock, token("acc-1"),
			step{status: 200, body: `{"access_token":"acc-2","refresh_token":"off-2","expires_in":` + expiresIn + `}`}), clock, 0)
		clock.advance(280)

		if _, _, err := provider.AuthorizationMetadata(); err != nil {
			t.Fatalf("expires_in %s: AuthorizationMetadata failed: %v", expiresIn, err)
		}

		if access, refresh, expiresAt, _ := provider.snapshot(); access != "acc-1" || refresh != "off-x" || expiresAt != 1300 {
			t.Errorf("expires_in %s: state = %q %q %v, want acc-1 off-x 1300", expiresIn, access, refresh, expiresAt)
		}
	}
	err := newProviderError(t, passwordConfig(), step{status: 200, body: `{"access_token":"acc-1","expires_in":"soon"}`})
	if err.Error() != "Keycloak token response carried a non-numeric expires_in (value withheld)" || errors.Unwrap(err) == nil {
		t.Errorf("err = %v", err)
	}
}

func TestT33ANumericStringExpiresInIsAccepted(t *testing.T) {
	t.Parallel()
	for _, expiresIn := range []any{"300", " 300 ", 300} {
		clock := newFakeClock()
		provider := build(t, passwordConfig(), newFakeTransport(clock, tokenStep("acc-1", expiresIn, "off-x")), clock, 0)
		if _, _, expiresAt, _ := provider.snapshot(); expiresAt != 1300 {
			t.Errorf("expires_in %#v: expiresAt = %v, want 1300", expiresIn, expiresAt)
		}
	}
	clock := newFakeClock()
	provider := build(t, passwordConfig(), newFakeTransport(clock, step{status: 200, body: `{"access_token":"acc-1"}`}), clock, 0)
	if _, _, expiresAt, _ := provider.snapshot(); expiresAt != 1000 {
		t.Errorf("a missing expires_in: expiresAt = %v, want 1000", expiresAt)
	}
}

func TestT34ANegativeExpiresInCountsAsAlreadyExpired(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	provider := build(t, passwordConfig(), newFakeTransport(clock, tokenStep("acc-1", -50, "off-x")), clock, 0)
	if _, _, expiresAt, _ := provider.snapshot(); expiresAt != 1000 {
		t.Errorf("expiresAt = %v, want 1000", expiresAt)
	}
}

func TestT35ARefreshWithoutANewRefreshTokenKeepsThePreviousOne(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, tokenStep("acc-1", 300, "off-1"), tokenStep("acc-2", 300, ""), token("acc-3"))
	provider := build(t, passwordConfig(), transport, clock, 0)
	clock.advance(280)
	if _, _, err := provider.AuthorizationMetadata(); err != nil {
		t.Fatal(err)
	}
	clock.advance(280)
	if _, value, err := provider.AuthorizationMetadata(); err != nil || value != "Bearer acc-3" {
		t.Fatalf("value = %q, err = %v", value, err)
	}

	for _, i := range []int{1, 2} {
		if got := transport.form(i).Get("refresh_token"); got != "off-1" {
			t.Errorf("refresh %d sent refresh_token=%q, want off-1", i, got)
		}
	}
}

func TestT36TheGrantFormsAreExact(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, token("acc-1"), token("acc-2"))
	cfg := passwordConfig()
	cfg.KeycloakURL += "//"
	provider := build(t, cfg, transport, clock, 0)
	clock.advance(280)
	if _, _, err := provider.AuthorizationMetadata(); err != nil {
		t.Fatal(err)
	}

	wantLogin := url.Values{
		"grant_type": {"password"}, "client_id": {testClientID}, "username": {testUsername},
		"password": {testPassword}, "scope": {"offline_access"},
	}
	wantRefresh := url.Values{"grant_type": {"refresh_token"}, "client_id": {testClientID}, "refresh_token": {"off-x"}}
	if got := transport.form(0); got.Encode() != wantLogin.Encode() {
		t.Errorf("login form = %v, want %v", got, wantLogin)
	}
	if got := transport.form(1); got.Encode() != wantRefresh.Encode() {
		t.Errorf("refresh form = %v, want %v", got, wantRefresh)
	}
	wantURL := testKeycloakURL + "/realms/" + testRealm + "/protocol/openid-connect/token"
	for i, endpoint := range transport.endpoints {
		if endpoint != wantURL || transport.timeouts[i] != 10*time.Second {
			t.Errorf("request %d went to %q with timeout %v, want %q and 10s", i, endpoint, transport.timeouts[i], wantURL)
		}
	}
}

// endregion

// region F. No credential in logs or errors

func TestT37TheFailureLinesAndErrorsCarryNoCredential(t *testing.T) {
	t.Parallel()
	secrets := []string{testPassword, "acc-secret-1", "off-secret-1", "acc-secret-2"}
	clock := newFakeClock()
	logs, logger := captureLogs()
	provider := build(t, passwordConfig(), newFakeTransport(clock, tokenStep("acc-secret-1", 300, "off-secret-1"),
		refused(), tokenStep("acc-secret-2", 300, "off-secret-2")), clock, 0, withLogger(logger))

	runLoop(provider, &scriptedWaiter{results: timedOut(2), clock: clock})

	if !strings.Contains(logs.String(), "refresh failed (attempt 1)") || !strings.Contains(logs.String(), "recovered") {
		t.Fatalf("the log lacks the failure or the recovery:\n%s", logs)
	}
	var texts []string
	texts = append(texts, logs.String())
	for _, failing := range []step{
		refused(), {err: errors.New("down")}, {status: 200, body: "<html>"}, {status: 200, body: "[]"},
		{status: 200, body: `{"refresh_token":"off-secret-1","expires_in":"x"}`},
		{status: 200, body: `{"access_token":"acc-secret-1","expires_in":"x"}`},
	} {
		texts = append(texts, newProviderError(t, passwordConfig(), failing).Error())
	}
	expired := build(t, passwordConfig(), newFakeTransport(clock, tokenStep("acc-secret-1", 300, "off-secret-1"), refused()), clock, 0)
	clock.advance(400)
	_, _, err := expired.AuthorizationMetadata()
	texts = append(texts, err.Error())
	_, _, err = expired.AuthorizationMetadata()
	texts = append(texts, err.Error())
	for _, text := range texts {
		for _, secretValue := range secrets {
			if strings.Contains(text, secretValue) {
				t.Errorf("%q leaked into %q", secretValue, text)
			}
		}
	}
}

// endregion

// region G. stop / close

func TestT38StopIsIdempotentAndSafeWithoutAWorker(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	provider := build(t, passwordConfig(), newFakeTransport(clock, token("acc-1")), clock, 0)

	if err := provider.Close(); err != nil {
		t.Fatal(err)
	}
	provider.Stop()
	if err := provider.Close(); err != nil {
		t.Fatal(err)
	}
	if _, value, err := provider.AuthorizationMetadata(); err != nil || value != "Bearer acc-1" {
		t.Errorf("the lazy path stopped working after Close: %q, %v", value, err)
	}
}

func TestT39StopFromInsideTheWorkerAndFromACleanupNeverBlocksOrPanics(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, tokenStep("acc-1", 0.05, "off-x"))
	shrunk := defaultTimings
	shrunk.expiryLeeway, shrunk.minRefreshDelay, shrunk.joinTimeout = 0, 0.01, 50*time.Millisecond
	var current atomic.Pointer[KeycloakTokenProvider]
	closedFromTheWorker := make(chan struct{})
	var once sync.Once
	transport.onPost = func() {
		if provider := current.Load(); provider != nil {
			once.Do(func() {
				provider.Stop() // from inside the worker: bounded by the join timeout, never a deadlock
				close(closedFromTheWorker)
			})
		}
	}
	provider, err := NewKeycloakTokenProvider(passwordConfig(), withTimings(shrunk), func(o *keycloakOptions) {
		o.transport = transport
	}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	current.Store(provider)

	select {
	case <-closedFromTheWorker:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker never refreshed")
	}
	provider.state.worker.Wait()
	provider.state.signalStop() // what the garbage-collection cleanup does after a Close
	if err := provider.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestT40AJoinThatTimesOutDoesNotEscapeClose(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	release := make(chan struct{})
	transport := newFakeTransport(clock, tokenStep("acc-1", 0, "off-x"), token("acc-2"))
	entered := make(chan struct{}, 1)
	var once sync.Once
	transport.onPost = func() {
		if len(transport.grants()) > 1 {
			once.Do(func() { entered <- struct{}{} })
			<-release
		}
	}
	shrunk := defaultTimings
	shrunk.minRefreshDelay, shrunk.joinTimeout = 0.001, 20*time.Millisecond
	provider, err := NewKeycloakTokenProvider(passwordConfig(), withTimings(shrunk), func(o *keycloakOptions) {
		o.transport = transport
	}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	<-entered

	started := time.Now()
	_ = provider.Close()
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("Close blocked %v on a stuck refresh", elapsed)
	}
	close(release)
	provider.state.worker.Wait()
}

// endregion

// region H. The real worker end to end

func TestT41TheRealWorkerSurvivesFailuresAndTheTokenRecovers(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, tokenStep("acc-1", 0.1, "off-x"), step{err: errors.New("network is unreachable")},
		unavailable(), step{panicWith: "generic failure"}, tokenStep("acc-ok", 3600, "off-ok"))
	shrunk := defaultTimings
	shrunk.expiryLeeway, shrunk.minRefreshDelay, shrunk.retryBaseDelay, shrunk.retryMaxDelay = 0.05, 0.01, 0.05, 0.05
	provider, err := NewKeycloakTokenProvider(passwordConfig(), withTimings(shrunk), func(o *keycloakOptions) {
		o.transport = transport
	}, quiet)
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for provider.AccessToken() != "acc-ok" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	if got := provider.AccessToken(); got != "acc-ok" {
		t.Fatalf("token = %q after 10 s, want acc-ok", got)
	}
	if provider.state.isClosed() {
		t.Errorf("the worker stopped")
	}
	if got, want := transport.grants(), append([]string{"password"}, repeat("refresh_token", 4)...); !equalSlices(got, want) {
		t.Errorf("grants = %v, want %v", got, want)
	}
	_ = provider.Close()
	finished := make(chan struct{})
	go func() { provider.state.worker.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Errorf("the worker is still alive after Close")
	}
}

// endregion

// region I. Offline-token start-up

func tokenConfig(refreshToken, password string) KeycloakConfig {
	cfg := passwordConfig()
	cfg.Password = password
	cfg.RefreshToken = refreshToken

	return cfg
}

func TestT42TheStartUpIsARefreshGrantWithTheHandedOffToken(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, token("acc-1"))
	provider := build(t, tokenConfig("off-handed-0", ""), transport, clock, 0)

	form := transport.form(0)
	if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "off-handed-0" || form.Has("password") {
		t.Errorf("start-up form = %v", form)
	}
	if got := provider.AccessToken(); got != "acc-1" {
		t.Errorf("token = %q", got)
	}
	if provider.state.password != "" {
		t.Errorf("a token-only provider holds a password") // T60
	}
}

func TestT43APasswordAlongsideTheTokenIsNeverSent(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, token("acc-1"), refused(), token("acc-3"))
	provider := build(t, tokenConfig("off-handed-0", testPassword), transport, clock, 0)
	clock.advance(280)
	_, _, _ = provider.AuthorizationMetadata()
	clock.advance(10)
	if _, value, err := provider.AuthorizationMetadata(); err != nil || value != "Bearer acc-3" {
		t.Fatalf("value = %q, err = %v", value, err)
	}

	for i, grant := range transport.grants() {
		if grant != "refresh_token" || transport.form(i).Has("password") {
			t.Errorf("request %d = %v, want a refresh grant without a password", i, transport.form(i))
		}
	}
}

func TestT44ARefusedStartUpTokenFailsWithoutAPasswordFallback(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, refused(), token("acc-1"))
	_, err := NewKeycloakTokenProvider(tokenConfig("off-dead", testPassword), withFakes(transport, clock, 0))

	var authErr *KeycloakAuthenticationError
	if !errors.As(err, &authErr) || !strings.Contains(err.Error(), "Keycloak token refresh failed with status 400") {
		t.Fatalf("err = %v", err)
	}
	if got := transport.grants(); !equalSlices(got, []string{"refresh_token"}) {
		t.Errorf("grants = %v, want only the refresh grant", got)
	}
}

func TestT45TheRotatedRefreshTokenIsUsedFromThenOn(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, tokenStep("acc-1", 300, "off-rotated"), token("acc-2"))
	provider := build(t, tokenConfig("off-handed-0", ""), transport, clock, 0)
	clock.advance(280)
	if _, _, err := provider.AuthorizationMetadata(); err != nil {
		t.Fatal(err)
	}

	if got := transport.form(1).Get("refresh_token"); got != "off-rotated" {
		t.Errorf("second refresh sent %q, want off-rotated", got)
	}
}

func TestT46TheDeadlineIsArmedOnTheOfflineTokenPath(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	cfg := tokenConfig("off-handed-0", "")
	cfg.TokenExpirationInS = 200
	provider := build(t, cfg, newFakeTransport(clock, token("acc-1"), token("acc-2")), clock, 0)
	if !provider.state.hasDeadline || provider.state.loginDeadline != 1200 {
		t.Fatalf("deadline = %v (%t), want 1200", provider.state.loginDeadline, provider.state.hasDeadline)
	}
	waiter := &scriptedWaiter{results: timedOut(100), clock: clock}

	runLoop(provider, waiter)

	if clock.read() > 1201 || !provider.state.stopped {
		t.Errorf("clock = %v, stopped = %t, want <= 1201 and stopped", clock.read(), provider.state.stopped)
	}
	// After the deadline the lazy path no longer refreshes either.
	clock.advance(1000)
	if _, value, err := provider.AuthorizationMetadata(); err != nil || value != "Bearer acc-1" {
		t.Errorf("after the deadline: %q, %v, want the lapsed token served as-is", value, err)
	}
}

func TestT47TheLoopSurvivesAFailingOfflineToken(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	transport := newFakeTransport(clock, token("acc-1"), refused(), refused(), token("acc-4"))
	provider := build(t, tokenConfig("off-handed-0", ""), transport, clock, 0)

	runLoop(provider, &scriptedWaiter{results: timedOut(3), clock: clock})

	if got := transport.grants(); !equalSlices(got, repeat("refresh_token", 4)) {
		t.Errorf("grants = %v", got)
	}
	if got := provider.AccessToken(); got != "acc-4" {
		t.Errorf("token = %q, want acc-4", got)
	}
}

// endregion

// region edge cases of the provider surface

func TestTheProviderRefusesAnInvalidOrUnconfiguredConfig(t *testing.T) {
	t.Parallel()
	if _, err := NewKeycloakTokenProvider(KeycloakConfig{Realm: testRealm}); err == nil || !strings.Contains(err.Error(), "KeycloakURL") {
		t.Errorf("partial triple: err = %v", err)
	}
	if _, err := NewKeycloakTokenProvider(KeycloakConfig{}); err == nil || !strings.Contains(err.Error(), "sets none of") {
		t.Errorf("no triple: err = %v", err)
	}
}

func TestAPanicWithANonErrorValueAtStartUpIsATypedError(t *testing.T) {
	t.Parallel()
	err := newProviderError(t, passwordConfig(), step{panicWith: 42})
	if err.Error() != "Keycloak token login failed (*errors.errorString: 42)" || errors.Unwrap(err) == nil {
		t.Errorf("err = %v", err)
	}
}

func TestThePerRPCCredentialsVariants(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	provider := build(t, passwordConfig(), newFakeTransport(clock, token("acc-1"), refused()), clock, 0)

	if !provider.RequireTransportSecurity() || !provider.PerRPCCredentials(false).RequireTransportSecurity() ||
		provider.PerRPCCredentials(true).RequireTransportSecurity() {
		t.Errorf("RequireTransportSecurity does not follow allowInsecureTransport")
	}
	metadata, err := provider.PerRPCCredentials(true).GetRequestMetadata(t.Context(), "uri")
	if err != nil || metadata["authorization"] != "Bearer acc-1" || len(metadata) != 1 {
		t.Errorf("metadata = %v, %v", metadata, err)
	}
	clock.advance(400)
	if _, err := provider.PerRPCCredentials(true).GetRequestMetadata(t.Context()); err == nil {
		t.Errorf("an expired, unrefreshable token was served")
	}
}

func TestWithKeycloakLoggerIgnoresNil(t *testing.T) {
	t.Parallel()
	options := keycloakOptions{logger: nil}
	WithKeycloakLogger(nil)(&options)
	if options.logger != nil {
		t.Errorf("a nil logger replaced the default")
	}
}

func TestTheDebugLineOfASwallowedBackgroundFailureIsDebugOnly(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	logs, logger := captureLogs()
	provider := build(t, passwordConfig(), newFakeTransport(clock, tokenStep("acc-1", 0, "off-x"), refused()), clock, 0,
		withLogger(logger))

	runLoop(provider, &scriptedWaiter{results: timedOut(1), clock: clock})

	if got := strings.Count(logs.String(), "level=WARN"); got != 1 {
		t.Errorf("WARNING lines = %d, want exactly 1 (no second warning from the loop):\n%s", got, logs)
	}
	if !strings.Contains(logs.String(), "level=DEBUG msg=\"Keycloak background refresh will be retried") {
		t.Errorf("no DEBUG line:\n%s", logs)
	}
}

func ExampleKeycloakAuthenticationError() {
	err := error(&KeycloakAuthenticationError{Message: "Keycloak token login failed with status 401: {}"})
	var authErr *KeycloakAuthenticationError
	fmt.Println(errors.As(err, &authErr), authErr)
	// Output: true Keycloak token login failed with status 401: {}
}

// endregion
