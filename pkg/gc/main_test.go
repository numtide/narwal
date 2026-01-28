package gc_test

import (
	"context"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(innerTestMain(m))
}

func innerTestMain(m *testing.M) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// get a rustfs server going
	rustfs := getRustfsServer(ctx)
	defer rustfs.Cleanup()

	return m.Run()
}
