// Copyright 2026 The pizzaplane Authors. SPDX-License-Identifier: Apache-2.0

package ble

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"k8s.io/utils/ptr"
)

const (
	bluez         = "org.bluez"
	adapter1      = bluez + ".Adapter1"
	device1       = bluez + ".Device1"
	objectManager = "org.freedesktop.DBus.ObjectManager"
	properties    = "org.freedesktop.DBus.Properties"
)

type nativeProvider struct{}

type bluezObjects map[dbus.ObjectPath]map[string]map[string]dbus.Variant

func (nativeProvider) Scan(ctx context.Context, ready func(), report func(Observation)) error {
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("connect to system D-Bus for BLE: %w", err)
	}
	defer conn.Close()

	setup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	signals := make(chan *dbus.Signal, 128)
	conn.Signal(signals)
	for _, match := range [][]dbus.MatchOption{
		{dbus.WithMatchSender(bluez), dbus.WithMatchInterface(objectManager)},
		{dbus.WithMatchSender(bluez), dbus.WithMatchInterface(properties), dbus.WithMatchPathNamespace("/org/bluez")},
		{dbus.WithMatchSender("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, bluez)},
	} {
		if err := conn.AddMatchSignalContext(setup, match...); err != nil {
			return fmt.Errorf("subscribe to BlueZ and D-Bus signals: %w", err)
		}
	}
	var objects bluezObjects
	if err := conn.Object(bluez, "/").CallWithContext(setup, objectManager+".GetManagedObjects", 0).Store(&objects); err != nil {
		return fmt.Errorf("read BlueZ adapters: %w", err)
	}
	var adapter dbus.ObjectPath
	for path, interfaces := range objects {
		if interfaces[adapter1]["Powered"].Value() == true && (adapter == "" || path < adapter) {
			adapter = path
		}
	}
	if adapter == "" {
		return fmt.Errorf("BlueZ has no powered Bluetooth adapter")
	}
	radio := conn.Object(bluez, adapter)
	filter := map[string]dbus.Variant{
		"Transport":     dbus.MakeVariant("le"),
		"DuplicateData": dbus.MakeVariant(true),
	}
	if err := radio.CallWithContext(setup, adapter1+".SetDiscoveryFilter", 0, filter).Err; err != nil {
		return fmt.Errorf("set BlueZ LE discovery filter: %w", err)
	}
	if err := radio.CallWithContext(setup, adapter1+".StartDiscovery", 0).Err; err != nil {
		return fmt.Errorf("start BlueZ LE discovery: %w", err)
	}
	cancel()
	// Closing our private D-Bus connection releases our discovery session.
	ready()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-conn.Context().Done():
			return fmt.Errorf("BLE system D-Bus connection closed")
		case signal, open := <-signals:
			if !open {
				return fmt.Errorf("BLE system D-Bus signal stream closed")
			}
			observation, err := bluezObservation(adapter, objects, signal)
			if err != nil {
				return err
			}
			if observation.Key != "" {
				report(observation)
			}
		}
	}
}

func bluezObservation(adapter dbus.ObjectPath, cached bluezObjects, signal *dbus.Signal) (Observation, error) {
	var path dbus.ObjectPath
	var data map[string]dbus.Variant
	switch signal.Name {
	case "org.freedesktop.DBus.NameOwnerChanged":
		return Observation{}, fmt.Errorf("BlueZ restarted")
	case objectManager + ".InterfacesRemoved":
		var interfaces []string
		if dbus.Store(signal.Body, &path, &interfaces) != nil {
			return Observation{}, nil
		}
		if path == adapter && slices.Contains(interfaces, adapter1) {
			return Observation{}, fmt.Errorf("BlueZ removed the Bluetooth adapter")
		}
		delete(cached, path)
	case objectManager + ".InterfacesAdded":
		var interfaces map[string]map[string]dbus.Variant
		if dbus.Store(signal.Body, &path, &interfaces) == nil {
			data = interfaces[device1]
		}
	case properties + ".PropertiesChanged":
		var iface string
		var invalidated []string
		if dbus.Store(signal.Body, &iface, &data, &invalidated) != nil {
			return Observation{}, nil
		}
		path = signal.Path
		if iface == adapter1 && path == adapter && data["Powered"].Value() == false {
			return Observation{}, fmt.Errorf("Bluetooth is turned off")
		}
		if iface != device1 {
			return Observation{}, nil
		}
	}
	if !strings.HasPrefix(string(path), string(adapter)+"/") || !hasAdvertisement(data) {
		return Observation{}, nil
	}
	// The initial inventory supplies metadata once we have a fresh sighting.
	// Cached devices alone do not create API objects.
	if previous := cached[path][device1]; previous != nil {
		maps.Copy(previous, data)
		data = previous
		delete(cached, path)
	}
	return decodeBluezDevice(path, data), nil
}

func hasAdvertisement(data map[string]dbus.Variant) bool {
	for _, property := range []string{"RSSI", "AdvertisingFlags", "AdvertisingData", "ManufacturerData", "ServiceData"} {
		if _, ok := data[property]; ok {
			return true
		}
	}
	return false
}

func decodeBluezDevice(path dbus.ObjectPath, data map[string]dbus.Variant) Observation {
	result := Observation{Key: string(path)}
	result.LocalName, _ = data["Name"].Value().(string)
	if rssi, ok := data["RSSI"].Value().(int16); ok && rssi != 127 {
		result.RSSI = ptr.To(int32(rssi))
	}
	if power, ok := data["TxPower"].Value().(int16); ok {
		result.TxPower = ptr.To(int32(power))
	}
	if services, ok := data["UUIDs"].Value().([]string); ok {
		result.ServiceUUIDs = slices.Clone(services)
	}
	if services, ok := data["ServiceData"].Value().(map[string]dbus.Variant); ok {
		for service := range services {
			result.ServiceUUIDs = append(result.ServiceUUIDs, service)
		}
	}
	if companies, ok := data["ManufacturerData"].Value().(map[uint16]dbus.Variant); ok {
		result.CompanyIDs = make([]int32, 0, len(companies))
		for company := range companies {
			result.CompanyIDs = append(result.CompanyIDs, int32(company))
		}
	}
	return result
}
