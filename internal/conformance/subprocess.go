// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0
package conformance

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// Subprocess reaches a driver written in any language: the runner starts the command once and
// exchanges one JSON line per turn, an Instruction in and a Report out, on its stdin and stdout.
// One line each way is the whole protocol, so a driver is a loop around the adapter and nothing
// else; stderr is the driver's own and is passed through for a person to read.
type Subprocess struct {
	Command []string
	Stderr  io.Writer

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
}

func (s *Subprocess) start(ctx context.Context) error {
	if s.cmd != nil {
		return nil
	}
	if len(s.Command) == 0 {
		return fmt.Errorf("no driver command")
	}
	cmd := exec.CommandContext(ctx, s.Command[0], s.Command[1:]...)
	cmd.Stderr = s.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	s.cmd, s.stdin, s.stdout = cmd, stdin, bufio.NewReaderSize(stdout, 4<<20)
	return nil
}

func (s *Subprocess) Run(ctx context.Context, in Instruction) (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.start(ctx); err != nil {
		return Report{}, err
	}
	line, err := json.Marshal(in)
	if err != nil {
		return Report{}, err
	}
	if _, err := s.stdin.Write(append(line, '\n')); err != nil {
		return Report{}, fmt.Errorf("write to the driver: %w", err)
	}
	answer, err := s.stdout.ReadString('\n')
	if err != nil {
		return Report{}, fmt.Errorf("read from the driver: %w", err)
	}
	var report Report
	if err := json.Unmarshal([]byte(strings.TrimSpace(answer)), &report); err != nil {
		return Report{}, fmt.Errorf("the driver's report did not parse: %w", err)
	}
	return report, nil
}

// Close ends the driver by closing its input; a driver exits when its input ends.
func (s *Subprocess) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil {
		return nil
	}
	_ = s.stdin.Close()
	err := s.cmd.Wait()
	s.cmd = nil
	return err
}

// Serve is the other side of the protocol: a loop any driver can be, given an implementation.
// The reference is served this way to prove the protocol end to end.
func Serve(ctx context.Context, in io.Reader, out io.Writer, driver Driver) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 1<<20), 4<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var instruction Instruction
		if err := json.Unmarshal([]byte(line), &instruction); err != nil {
			return fmt.Errorf("instruction did not parse: %w", err)
		}
		report, err := driver.Run(ctx, instruction)
		if err != nil {
			return err
		}
		answer, err := json.Marshal(report)
		if err != nil {
			return err
		}
		if _, err := out.Write(append(answer, '\n')); err != nil {
			return err
		}
	}
	return scanner.Err()
}
