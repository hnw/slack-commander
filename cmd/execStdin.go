package cmd

import (
	"io"
	"os/exec"
)

// execStdin は Start 前の接続を所有し、成功後は入力側へ所有権を渡す。
type execStdin struct {
	onStarted func(io.WriteCloser)
	unclaimed io.WriteCloser
}

func (s *execStdin) prepare(command *exec.Cmd) error {
	if s.onStarted == nil {
		return nil
	}
	var err error
	s.unclaimed, err = command.StdinPipe()
	return err
}

func (s *execStdin) started() {
	if s.unclaimed == nil {
		return
	}
	writer := s.unclaimed
	s.unclaimed = nil
	s.onStarted(writer)
}

func (s *execStdin) closeUnclaimed() {
	if s.unclaimed != nil {
		_ = s.unclaimed.Close()
	}
}
