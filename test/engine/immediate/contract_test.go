package immediate_test

import (
	"testing"

	"github.com/golang-io/graph/engine/immediate"
	"github.com/golang-io/graph/test"
	"github.com/golang-io/graph/test/engine/enginetest"
)

func TestContract(t *testing.T) { enginetest.Run(t, test.TempDB, immediate.Build) }

func BenchmarkEngine(b *testing.B) { enginetest.Bench(b, test.TempDB, immediate.Build) }
