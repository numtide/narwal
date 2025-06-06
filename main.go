package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/numtide/narwal/cmd"
)

func main() {
	// Create a context that can be cancelled on SIGINT/SIGTERM
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Listen for interrupt signals
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		cancel()
	}()

	root := cmd.New()
	if err := root.ExecuteContext(ctx); err != nil {
		//nolint:gocritic
		os.Exit(1)
	}
}
