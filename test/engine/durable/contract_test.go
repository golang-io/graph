package durable_test

import (
	"testing"
	"time"

	"github.com/golang-io/graph/engine/durable"
	"github.com/golang-io/graph/test"
	"github.com/golang-io/graph/test/engine/enginetest"
)

func TestContract(t *testing.T) {
	enginetest.Run(t, test.TempDB, durable.With(durable.Options{Workers: 4, Shards: 4, PollInterval: 10 * time.Millisecond}))
}

func BenchmarkEngine(b *testing.B) {
	enginetest.Bench(b, test.TempDB, durable.With(durable.Options{Workers: 8, Shards: 8, PollInterval: 5 * time.Millisecond}))
}
