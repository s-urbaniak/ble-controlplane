// Copyright 2026 The pizzaplane Authors. SPDX-License-Identifier: Apache-2.0

package ble

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type nativeProvider struct{}

// The helper owns Core Bluetooth and its dispatch queue. The daemon owns its lifetime.
func (nativeProvider) Scan(ctx context.Context, ready func(), report func(Observation)) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating the macOS BLE helper: %w", err)
	}
	helper := filepath.Join(filepath.Dir(executable), "ble-controlplane-ble-darwin")
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, helper)
	cmd.Stderr = os.Stderr
	output, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("opening macOS BLE helper output: %w", err)
	}
	if err := cmd.Start(); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("macOS BLE helper is missing; build ble-controlplane-ble-darwin next to the daemon")
		}
		return fmt.Errorf("starting the macOS BLE helper: %w", err)
	}

	lines := bufio.NewScanner(output)
	var scanErr error
	for lines.Scan() {
		var message struct {
			Ready bool
			Error string
			Observation
		}
		if err := json.Unmarshal(lines.Bytes(), &message); err != nil {
			scanErr = fmt.Errorf("decoding macOS BLE helper output: %w", err)
			break
		}
		switch {
		case message.Error != "":
			scanErr = fmt.Errorf("Core Bluetooth: %s", message.Error)
		case message.Ready:
			ready()
		case message.Key != "":
			report(message.Observation)
		}
		if scanErr != nil {
			break
		}
	}
	if scanErr == nil {
		scanErr = lines.Err()
	}
	if scanErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if scanErr != nil {
		return scanErr
	}
	if waitErr != nil {
		return fmt.Errorf("macOS BLE helper exited: %w", waitErr)
	}
	return nil
}
