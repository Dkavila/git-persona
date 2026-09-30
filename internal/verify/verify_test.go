package verify_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Dkavila/git-pilot/internal/config"
	"github.com/Dkavila/git-pilot/internal/ssh"
	"github.com/Dkavila/git-pilot/internal/verify"
)

// fakeProber simulates a network probe. It records concurrency so the tests
// can prove the work really overlaps instead of merely finishing.
type fakeProber struct {
	mu        sync.Mutex
	active    int
	maxActive int
	calls     int

	delay   time.Duration
	results map[string]ssh.ProbeResult
	errs    map[string]error
}

func newFakeProber(delay time.Duration) *fakeProber {
	return &fakeProber{
		delay:   delay,
		results: map[string]ssh.ProbeResult{},
		errs:    map[string]error{},
	}
}

func (f *fakeProber) ProbeGitHub(ctx context.Context, keyPath string) (ssh.ProbeResult, error) {
	f.mu.Lock()
	f.calls++
	f.active++
	if f.active > f.maxActive {
		f.maxActive = f.active
	}
	f.mu.Unlock()

	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()

	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return ssh.ProbeResult{}, ctx.Err()
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.errs[keyPath]; ok {
		return ssh.ProbeResult{}, err
	}
	if res, ok := f.results[keyPath]; ok {
		return res, nil
	}
	return ssh.ProbeResult{Authenticated: true, Username: "octocat"}, nil
}

func (f *fakeProber) snapshot() (calls, maxActive, active int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.maxActive, f.active
}

func profiles(names ...string) []config.Profile {
	out := make([]config.Profile, 0, len(names))
	for _, n := range names {
		out = append(out, config.Profile{
			Name:    n,
			Email:   n + "@acme-corp.com",
			KeyPath: "/home/user/.ssh/id_ed25519_" + n,
		})
	}
	return out
}

func TestAll_ReturnsOneResultPerProfile(t *testing.T) {
	p := newFakeProber(0)
	got := verify.All(context.Background(), p, profiles("work", "personal", "client"), verify.Options{})

	if len(got) != 3 {
		t.Fatalf("len(results) = %d, want 3", len(got))
	}
	calls, _, _ := p.snapshot()
	if calls != 3 {
		t.Fatalf("probe calls = %d, want 3", calls)
	}
}

// Output has to be stable for a CLI, so results come back in the order the
// profiles were given, not the order the probes happened to finish.
func TestAll_PreservesInputOrder(t *testing.T) {
	p := newFakeProber(0)
	in := profiles("zulu", "alpha", "mike")

	got := verify.All(context.Background(), p, in, verify.Options{})

	for i, want := range in {
		if got[i].Profile.Name != want.Name {
			t.Fatalf("results[%d] = %q, want %q", i, got[i].Profile.Name, want.Name)
		}
	}
}

// The point of the command. Five probes of 100ms each take ~500ms in sequence
// and ~100ms in parallel. The margin is wide so a loaded machine does not
// produce a false failure, but it is far below the sequential time.
func TestAll_RunsConcurrently(t *testing.T) {
	p := newFakeProber(100 * time.Millisecond)

	start := time.Now()
	got := verify.All(context.Background(), p, profiles("a", "b", "c", "d", "e"), verify.Options{})
	elapsed := time.Since(start)

	if len(got) != 5 {
		t.Fatalf("len(results) = %d, want 5", len(got))
	}
	if elapsed > 350*time.Millisecond {
		t.Fatalf("took %v for 5 probes of 100ms; this looks sequential", elapsed)
	}

	_, maxActive, _ := p.snapshot()
	if maxActive < 2 {
		t.Fatalf("max concurrent probes = %d, want at least 2", maxActive)
	}
}

func TestAll_RespectsConcurrencyLimit(t *testing.T) {
	p := newFakeProber(40 * time.Millisecond)

	got := verify.All(context.Background(), p, profiles("a", "b", "c", "d", "e", "f"),
		verify.Options{Concurrency: 2})

	if len(got) != 6 {
		t.Fatalf("len(results) = %d, want 6", len(got))
	}

	_, maxActive, _ := p.snapshot()
	if maxActive > 2 {
		t.Fatalf("max concurrent probes = %d, want at most 2", maxActive)
	}
}

// One broken profile must not hide or corrupt the others.
func TestAll_IsolatesFailures(t *testing.T) {
	p := newFakeProber(0)
	in := profiles("work", "broken", "personal")
	p.errs[in[1].KeyPath] = errors.New("ssh exploded")

	got := verify.All(context.Background(), p, in, verify.Options{})

	if got[0].Status != verify.StatusOK {
		t.Errorf("results[0].Status = %q, want %q", got[0].Status, verify.StatusOK)
	}
	if got[1].Status != verify.StatusError {
		t.Errorf("results[1].Status = %q, want %q", got[1].Status, verify.StatusError)
	}
	if got[1].Err == nil {
		t.Error("results[1].Err = nil, want the underlying failure")
	}
	if got[2].Status != verify.StatusOK {
		t.Errorf("results[2].Status = %q, want %q", got[2].Status, verify.StatusOK)
	}
}

func TestAll_MapsProbeVerdictsToStatus(t *testing.T) {
	p := newFakeProber(0)
	in := profiles("good", "rejected")
	p.results[in[0].KeyPath] = ssh.ProbeResult{Authenticated: true, Username: "octocat"}
	p.results[in[1].KeyPath] = ssh.ProbeResult{Authenticated: false}

	got := verify.All(context.Background(), p, in, verify.Options{})

	if got[0].Status != verify.StatusOK {
		t.Errorf("Status = %q, want %q", got[0].Status, verify.StatusOK)
	}
	if got[0].Username != "octocat" {
		t.Errorf("Username = %q, want %q", got[0].Username, "octocat")
	}
	if got[1].Status != verify.StatusDenied {
		t.Errorf("Status = %q, want %q", got[1].Status, verify.StatusDenied)
	}
}

// A hung probe must not hold the whole command hostage.
func TestAll_PerProfileTimeout(t *testing.T) {
	p := newFakeProber(2 * time.Second)

	start := time.Now()
	got := verify.All(context.Background(), p, profiles("slow"),
		verify.Options{Timeout: 50 * time.Millisecond})
	elapsed := time.Since(start)

	if len(got) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(got))
	}
	if got[0].Status != verify.StatusTimeout {
		t.Fatalf("Status = %q, want %q", got[0].Status, verify.StatusTimeout)
	}
	if elapsed > time.Second {
		t.Fatalf("took %v; the timeout did not cut the probe short", elapsed)
	}
}

// One slow profile must not degrade its siblings.
func TestAll_TimeoutIsPerProfile(t *testing.T) {
	p := newFakeProber(0)
	slow := newFakeProber(2 * time.Second)
	_ = slow

	in := profiles("fast1", "fast2")
	got := verify.All(context.Background(), p, in, verify.Options{Timeout: time.Second})

	for i := range got {
		if got[i].Status != verify.StatusOK {
			t.Errorf("results[%d].Status = %q, want %q", i, got[i].Status, verify.StatusOK)
		}
	}
}

func TestAll_RespectsContextCancellation(t *testing.T) {
	p := newFakeProber(2 * time.Second)
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	got := verify.All(ctx, p, profiles("a", "b", "c"), verify.Options{})
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Fatalf("took %v; cancellation was ignored", elapsed)
	}
	if len(got) != 3 {
		t.Fatalf("len(results) = %d, want 3 even when cancelled", len(got))
	}
}

func TestAll_EmptyProfiles(t *testing.T) {
	p := newFakeProber(0)

	done := make(chan []verify.Result, 1)
	go func() { done <- verify.All(context.Background(), p, nil, verify.Options{}) }()

	select {
	case got := <-done:
		if len(got) != 0 {
			t.Fatalf("len(results) = %d, want 0", len(got))
		}
	case <-time.After(time.Second):
		t.Fatal("All() deadlocked on an empty profile list")
	}
}

// Every worker must have exited by the time All returns: no goroutine may
// still be holding the prober, and the results channel must be drained.
func TestAll_LeavesNoWorkersRunning(t *testing.T) {
	p := newFakeProber(10 * time.Millisecond)

	verify.All(context.Background(), p, profiles("a", "b", "c", "d"), verify.Options{Concurrency: 2})

	_, _, active := p.snapshot()
	if active != 0 {
		t.Fatalf("%d probes still in flight after All() returned", active)
	}
}

func TestAll_RecordsDuration(t *testing.T) {
	p := newFakeProber(20 * time.Millisecond)

	got := verify.All(context.Background(), p, profiles("a"), verify.Options{})

	if got[0].Duration <= 0 {
		t.Fatalf("Duration = %v, want a positive measurement", got[0].Duration)
	}
}
