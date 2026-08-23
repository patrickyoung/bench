package ui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"charm.land/bubbletea/v2"
	"github.com/patrickyoung/bench/internal/plyexec"
)

const (
	maxSteeringLine  = 8 << 10
	maxSteeringBatch = 64 << 10
)

func (m *Model) armLoopSteering(options plyexec.TaskOptions) error {
	if !options.Loop {
		m.cleanupLoopSteering()
		return nil
	}
	if strings.TrimSpace(options.Check) == "" {
		return errors.New("loop needs a configured check")
	}
	m.cleanupLoopSteering()
	dir := filepath.Dir(m.session)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create steering directory: %w", err)
	}
	file, err := os.CreateTemp(dir, ".bench-steer-*")
	if err != nil {
		return fmt.Errorf("create steering file: %w", err)
	}
	path := file.Name()
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		os.Remove(path)
		return fmt.Errorf("protect steering file: %w", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return fmt.Errorf("close steering file: %w", err)
	}
	m.steeringPath = path
	m.composer.SetValue("")
	m.composer.Placeholder = "Steer the running loop at its next model turn…"
	m.composer.Focus()
	return nil
}

func (m *Model) cleanupLoopSteering() {
	if m.steeringPath != "" {
		_ = os.Remove(m.steeringPath)
		m.steeringPath = ""
	}
}

func (m *Model) canSteerLoop() bool {
	return m.running && m.job == jobPlyTask && m.taskOptions.Loop && m.steeringPath != ""
}

func (m *Model) queueLoopSteering() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.composer.Value())
	if text == "" {
		m.notice = "Type implementation guidance before queuing steering"
		return m, nil
	}
	if err := validateSteering(text); err != nil {
		m.notice = "Steering was not queued · " + err.Error()
		return m, nil
	}
	info, err := os.Lstat(m.steeringPath)
	if err != nil {
		m.notice = "Steering was not queued · " + err.Error()
		return m, nil
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		m.notice = "Steering was not queued · steering path is not a regular file"
		return m, nil
	}
	file, err := os.OpenFile(m.steeringPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		m.notice = "Steering was not queued · " + err.Error()
		return m, nil
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() {
		file.Close()
		if err != nil {
			m.notice = "Steering was not queued · " + err.Error()
		} else {
			m.notice = "Steering was not queued · steering path is not a regular file"
		}
		return m, nil
	}
	body := []byte(strings.TrimRight(text, "\n") + "\n")
	_, writeErr := file.Write(body)
	closeErr := file.Close()
	if writeErr != nil {
		m.notice = "Steering was not queued · " + writeErr.Error()
		return m, nil
	}
	if closeErr != nil {
		m.notice = "Steering was not queued · " + closeErr.Error()
		return m, nil
	}
	m.messages = append(m.messages, message{role: roleUser, text: "STEERING QUEUED FOR NEXT MODEL TURN\n" + text})
	m.composer.SetValue("")
	m.notice = "Steering queued for the next model turn · tools and verifier are unchanged"
	m.syncContent()
	return m, m.composer.Focus()
}

func validateSteering(text string) error {
	if !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
		return errors.New("guidance must be valid UTF-8 without NUL bytes")
	}
	if len(text) > maxSteeringBatch {
		return fmt.Errorf("guidance exceeds %d bytes", maxSteeringBatch)
	}
	for _, line := range bytes.Split([]byte(text), []byte{'\n'}) {
		if len(line) > maxSteeringLine {
			return fmt.Errorf("one guidance line exceeds %d bytes", maxSteeringLine)
		}
	}
	return nil
}
