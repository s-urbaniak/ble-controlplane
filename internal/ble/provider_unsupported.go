//go:build !darwin && !linux

// Copyright 2026 The pizzaplane Authors. SPDX-License-Identifier: Apache-2.0

package ble

import (
	"context"
	"fmt"
	"runtime"
)

type nativeProvider struct{}

func (nativeProvider) Scan(context.Context, func(), func(Observation)) error {
	return fmt.Errorf("BLE scanning is unavailable on %s", runtime.GOOS)
}
