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

// The fakes of the Keycloak test catalogue (SPEC.md section 3): a scripted token endpoint that
// records every form, a fake monotonic clock, a fixed jitter fraction and a scripted waiter, so
// the whole catalogue runs without network and without a real sleep. Product agnostic.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/url"
	"runtime"
	"sync"
	"testing"
	"time"
)

const (
	testKeycloakURL = "https://keycloak.example.com/auth"
	testRealm       = "ondewo-test"
	testClientID    = "ondewo-test-sdk-public"
	testUsername    = "technical-user@example.com"
	testPassword    = "pw-never-logged-5d1c"
	deadSession     = `{"error":"invalid_grant","error_description":"Offline user session not found"}`
)

// fakeClock is a monotonic clock the test moves by hand. Start: 1000.
type fakeClock struct {
	mu  sync.Mutex
	now float64
}

func newFakeClock() *fakeClock { return &fakeClock{now: 1000} }

func (c *fakeClock) read() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeClock) advance(by float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += by
}

func (c *fakeClock) set(at float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = at
}

// step is one scripted answer of the fake token endpoint.
type step struct {
	status    int
	body      string
	err       error
	panicWith any
	// advance moves the fake clock while the request is "in flight".
	advance float64
}

func tokenStep(access string, expiresIn any, refresh string) step {
	body := map[string]any{"access_token": access, "expires_in": expiresIn}
	if refresh != "" {
		body["refresh_token"] = refresh
	}
	encoded, _ := json.Marshal(body)

	return step{status: 200, body: string(encoded)}
}

func token(access string) step { return tokenStep(access, 300, "off-x") }

func refused() step { return step{status: 400, body: deadSession} }

func unavailable() step { return step{status: 503, body: `{"error":"unavailable"}`} }

// fakeTransport replays its steps (the last one repeats once the script runs out) and records
// every request.
type fakeTransport struct {
	mu        sync.Mutex
	steps     []step
	delay     time.Duration
	clock     *fakeClock
	forms     []url.Values
	endpoints []string
	timeouts  []time.Duration
	onPost    func()
}

func newFakeTransport(clock *fakeClock, steps ...step) *fakeTransport {
	return &fakeTransport{steps: steps, clock: clock}
}

func (f *fakeTransport) post(_ context.Context, endpoint string, form url.Values, timeout time.Duration) (int, []byte, error) {
	f.mu.Lock()
	f.forms = append(f.forms, form)
	f.endpoints = append(f.endpoints, endpoint)
	f.timeouts = append(f.timeouts, timeout)
	current := f.steps[0]
	if len(f.steps) > 1 {
		f.steps = f.steps[1:]
	}
	onPost := f.onPost
	f.mu.Unlock()

	if onPost != nil {
		onPost()
	}
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if current.advance != 0 {
		f.clock.advance(current.advance)
	}
	if current.panicWith != nil {
		panic(current.panicWith)
	}
	if current.err != nil {
		return 0, nil, current.err
	}

	return current.status, []byte(current.body), nil
}

func (f *fakeTransport) grants() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	grants := make([]string, 0, len(f.forms))
	for _, form := range f.forms {
		grants = append(grants, form.Get("grant_type"))
	}

	return grants
}

func (f *fakeTransport) form(i int) url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.forms[i]
}

// scriptedWaiter stands in for the interruptible wait of the refresher: it records each delay,
// advances the fake clock by it (or by a scripted amount) and returns the scripted result (true =
// stopped). Once the script runs out it returns true, so no loop ever runs away.
type scriptedWaiter struct {
	results   []bool
	advanceBy []float64
	clock     *fakeClock
	delays    []float64
}

func (w *scriptedWaiter) wait(_ <-chan struct{}, delay time.Duration) bool {
	w.delays = append(w.delays, delay.Seconds())
	if len(w.advanceBy) > 0 {
		w.clock.advance(w.advanceBy[0])
		w.advanceBy = w.advanceBy[1:]
	} else {
		w.clock.advance(delay.Seconds())
	}
	if len(w.results) == 0 {
		return true
	}
	result := w.results[0]
	w.results = w.results[1:]

	return result
}

// timedOut is n waits that time out, i.e. n refresh attempts.
func timedOut(n int) []bool { return make([]bool, n) }

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func captureLogs() (*syncBuffer, *slog.Logger) {
	buffer := &syncBuffer{}

	return buffer, slog.New(slog.NewTextHandler(buffer, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func passwordConfig() KeycloakConfig {
	return KeycloakConfig{
		KeycloakURL: testKeycloakURL, Realm: testRealm, ClientID: testClientID,
		Username: testUsername, Password: testPassword,
	}
}

func withFakes(transport *fakeTransport, clock *fakeClock, jitter float64) KeycloakOption {
	return func(o *keycloakOptions) {
		o.transport = transport
		o.clock = clock.read
		o.jitter = func() float64 { return jitter }
		o.startBackground = false
		o.logger = slog.New(slog.DiscardHandler)
	}
}

// quiet discards the provider's log lines.
var quiet = WithKeycloakLogger(slog.New(slog.DiscardHandler))

func withLogger(logger *slog.Logger) KeycloakOption { return WithKeycloakLogger(logger) }

func withTimings(t timings) KeycloakOption { return func(o *keycloakOptions) { o.timings = t } }

// build is a logged-in provider on the fake clock with a fixed jitter and no background worker.
func build(t *testing.T, cfg KeycloakConfig, transport *fakeTransport, clock *fakeClock, jitter float64, opts ...KeycloakOption) *KeycloakTokenProvider {
	t.Helper()
	provider, err := NewKeycloakTokenProvider(cfg, append([]KeycloakOption{withFakes(transport, clock, jitter)}, opts...)...)
	if err != nil {
		t.Fatalf("NewKeycloakTokenProvider failed: %v", err)
	}

	return provider
}

// runLoop drives the background refresher synchronously with a scripted waiter.
// The provider is kept alive: an unreachable provider is collected mid-loop, and its cleanup
// stops the loop exactly as it should in production (TestT18...).
func runLoop(provider *KeycloakTokenProvider, waiter *scriptedWaiter) {
	defer runtime.KeepAlive(provider)
	provider.state.wait = waiter.wait
	provider.state.worker.Add(1)
	provider.state.run()
}

func (p *KeycloakTokenProvider) snapshot() (access, refresh string, expiresAt float64, failures int) {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()

	return p.state.accessToken, p.state.refreshToken, p.state.expiresAt, p.state.failures
}

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func equalSlices[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func repeat(value string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = value
	}

	return out
}
