package server_test

import (
	"os/exec"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
)

// Adapted from https://github.com/Mic92/niks3
func terminateProcess(cmd *exec.Cmd) {
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		log.Error("failed to get pgid", "error", err)

		return
	}

	time.AfterFunc(30*time.Second, func() {
		err = syscall.Kill(pgid, syscall.SIGKILL)
		if err != nil {
			log.Error("failed to kill process", "error", err)

			return
		}

		log.Info("killed process")
	})

	err = syscall.Kill(pgid, syscall.SIGTERM)
	if err != nil {
		log.Error("failed to kill process", "error", err)
	}

	// Wait returns an error if the process was killed by a signal, which is expected.
	_ = cmd.Wait()
}
