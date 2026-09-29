// Package verify checks every registered profile's SSH connectivity at once.
//
// Probes are network-bound and independent, so running them in sequence would
// cost the sum of their latencies. Running them concurrently costs roughly the
// slowest one.
package verify

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Dkavila/git-persona/internal/config"
	"github.com/Dkavila/git-persona/internal/ssh"
)

// Status is the outcome of one profile's probe.
type Status string

const (
	StatusOK      Status = "OK"
	StatusDenied  Status = "DENIED"
	StatusTimeout Status = "TIMEOUT"
	StatusError   Status = "ERROR"
)

// Prober is the slice of the ssh layer this package needs.
type Prober interface {
	ProbeGitHub(ctx context.Context, keyPath string) (ssh.ProbeResult, error)
}

// Result is one profile's verdict.
type Result struct {
	Profile  config.Profile
	Status   Status
	Username string
	Err      error
	Duration time.Duration
}

// Options tunes the sweep.
type Options struct {
	// Concurrency caps simultaneous probes. Zero means one goroutine per
	// profile, which is the right default for a handful of identities.
	Concurrency int
	// Timeout bounds a single probe. Zero means no per-probe deadline.
	Timeout time.Duration
}

// indexed pairs a result with its position, so the concurrent results can be
// restored to the caller's ordering.
type indexed struct {
	i      int
	result Result
}

// All probes every profile concurrently and returns the verdicts in the same
// order the profiles were given.
//
// It never returns a partial slice: every profile gets a Result, including the
// ones that timed out or were cancelled, so the caller can always account for
// each identity.
func All(ctx context.Context, p Prober, profiles []config.Profile, opts Options) []Result {
	results := make([]Result, len(profiles))
	if len(profiles) == 0 {
		// Nothing to fan out. Returning early also avoids constructing a
		// channel that nothing would ever close.
		return results
	}

	// Buffered to the number of profiles so no worker can block on send, even
	// if the collector is slow. That is what guarantees every goroutine exits.
	ch := make(chan indexed, len(profiles))

	// A nil channel disables the limiter, so the unlimited case costs nothing.
	var tokens chan struct{}
	if opts.Concurrency > 0 {
		tokens = make(chan struct{}, opts.Concurrency)
	}

	var wg sync.WaitGroup
	for i, profile := range profiles {
		wg.Add(1)

		go func(i int, profile config.Profile) {
			defer wg.Done()

			if tokens != nil {
				select {
				case tokens <- struct{}{}:
					defer func() { <-tokens }()
				case <-ctx.Done():
					ch <- indexed{i, Result{Profile: profile, Status: StatusError, Err: ctx.Err()}}
					return
				}
			}

			ch <- indexed{i, probeOne(ctx, p, profile, opts.Timeout)}
		}(i, profile)
	}

	// Closing from a separate goroutine lets the range below drain the channel
	// as results arrive rather than waiting for the whole batch.
	go func() {
		wg.Wait()
		close(ch)
	}()

	for r := range ch {
		results[r.i] = r.result
	}
	return results
}

// probeOne runs a single probe under its own deadline and classifies the
// outcome.
func probeOne(ctx context.Context, p Prober, profile config.Profile, timeout time.Duration) Result {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	start := time.Now()
	res, err := p.ProbeGitHub(ctx, profile.KeyPath)
	elapsed := time.Since(start)

	result := Result{
		Profile:  profile,
		Username: res.Username,
		Duration: elapsed,
	}

	switch {
	case err != nil && errors.Is(err, context.DeadlineExceeded):
		result.Status = StatusTimeout
		result.Err = err
	case err != nil:
		result.Status = StatusError
		result.Err = err
	case res.Authenticated:
		result.Status = StatusOK
	default:
		// The probe completed and the key was refused. That is a verdict about
		// the key, not a malfunction of the tool.
		result.Status = StatusDenied
	}
	return result
}
