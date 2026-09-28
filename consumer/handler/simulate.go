package handler

import (
	"context"
	"errors"
	"math/rand/v2"

	"github.com/tomdong2010/good-gokafka/consumer/sub"
)

// ErrSimulated is returned by handlers wrapped with SimulateFailures.
var ErrSimulated = errors.New("simulated transient failure")

// SimulateFailures makes next fail with ErrSimulated for a random fraction
// (rate, between 0 and 1) of calls. It exists for demos and load tests, to
// exercise retries and the dead-letter topic; do not enable it in production.
func SimulateFailures(next sub.Handler, rate float64) sub.Handler {
	return func(ctx context.Context, r *sub.Record) error {
		if rand.Float64() < rate {
			return ErrSimulated
		}
		return next(ctx, r)
	}
}
