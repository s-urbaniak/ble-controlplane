// Copyright 2026 The pizzaplane Authors. SPDX-License-Identifier: Apache-2.0

package ble

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/watch"

	api "example.com/ble-controlplane/pkg/apis/generic"
)

const (
	deviceTTL    = 3 * time.Second
	publishEvery = time.Second
	maxDevices   = 256
	historySize  = 2048
)

type sighting struct {
	published *api.BLEDevice
	latest    api.BLEDeviceStatus
	seenAt    time.Time
}

// Index owns the current observations and a bounded replay buffer.
type Index struct {
	mu          sync.Mutex
	key         []byte
	epoch       string
	revision    uint64
	unavailable string
	changed     chan struct{}
	devices     map[string]*sighting
	history     [historySize]watch.Event
}

func NewIndex() *Index {
	return &Index{
		key:         []byte(rand.Text()),
		epoch:       rand.Text(),
		unavailable: "BLE scanner is starting",
		changed:     make(chan struct{}),
		devices:     make(map[string]*sighting),
	}
}

func (i *Index) setReady() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.unavailable = ""
	i.notify()
}

func (i *Index) setUnavailable(reason string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.unavailable == reason {
		return false
	}
	if i.unavailable == "" {
		// A new scan starts with a new collection. Clients of the old scan relist.
		clear(i.devices)
		clear(i.history[:])
		i.epoch = rand.Text()
		i.revision = 0
	}
	i.unavailable = reason
	i.notify()
	return true
}

func (i *Index) Get(name, version string) (*api.BLEDevice, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if _, err := i.resolve(version); err != nil {
		return nil, err
	}
	device, ok := i.devices[name]
	if !ok {
		return nil, apierrors.NewNotFound(api.Resource(api.ResourceName), name)
	}
	return device.published.DeepCopy(), nil
}

func (i *Index) List(options metainternalversion.ListOptions) (*api.BLEDeviceList, error) {
	if options.Continue != "" {
		return nil, apierrors.NewBadRequest("BLEDevice lists do not use continuation tokens")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	revision, err := i.resolve(options.ResourceVersion)
	if err != nil {
		return nil, err
	}
	if options.ResourceVersionMatch == metav1.ResourceVersionMatchExact && revision != i.revision {
		return nil, expired()
	}
	return i.snapshot(options), nil
}

func (i *Index) snapshot(options metainternalversion.ListOptions) *api.BLEDeviceList {
	list := &api.BLEDeviceList{
		ListMeta: metav1.ListMeta{ResourceVersion: i.version()},
		Items:    make([]api.BLEDevice, 0, len(i.devices)),
	}
	for _, device := range i.devices {
		if matches(options, device.published) {
			list.Items = append(list.Items, *device.published.DeepCopy())
		}
	}
	slices.SortFunc(list.Items, func(a, b api.BLEDevice) int { return strings.Compare(a.Name, b.Name) })
	return list
}

func matches(options metainternalversion.ListOptions, device *api.BLEDevice) bool {
	return (options.LabelSelector == nil || options.LabelSelector.Matches(labels.Set(device.Labels))) &&
		(options.FieldSelector == nil || options.FieldSelector.Matches(fields.Set{"metadata.name": device.Name, "metadata.namespace": device.Namespace}))
}

func expired() error {
	return apierrors.NewResourceExpired("BLE resource version is no longer available; list again")
}

// resolve resolves a client's version against the current scan. The caller holds i.mu.
func (i *Index) resolve(version string) (uint64, error) {
	if i.unavailable != "" {
		return 0, apierrors.NewServiceUnavailable(i.unavailable)
	}
	if version == "" || version == "0" {
		return i.revision, nil
	}
	epoch, counter, ok := strings.Cut(version, ".")
	revision, err := strconv.ParseUint(counter, 10, 64)
	if !ok || err != nil {
		return 0, apierrors.NewBadRequest("invalid BLE resource version")
	}
	if epoch != i.epoch {
		return 0, expired()
	}
	if revision > i.revision {
		return 0, apierrors.NewTimeoutError("BLE resource version is newer than the current scan", 1)
	}
	return revision, nil
}

func (i *Index) version() string {
	return i.epoch + "." + strconv.FormatUint(i.revision, 10)
}

func (i *Index) observe(observation Observation, now time.Time) {
	if observation.Key == "" {
		return
	}
	hash := hmac.New(sha256.New, i.key)
	_, _ = hash.Write([]byte(observation.Key))
	name := "ble-" + hex.EncodeToString(hash.Sum(nil)[:12])

	i.mu.Lock()
	defer i.mu.Unlock()
	device, exists := i.devices[name]
	if !exists {
		if len(i.devices) == maxDevices {
			i.evictOldest()
		}
		device = &sighting{
			published: &api.BLEDevice{ObjectMeta: metav1.ObjectMeta{
				Namespace:         api.LocalNamespace,
				Name:              name,
				UID:               uuid.NewUUID(),
				CreationTimestamp: metav1.NewTime(now),
			}},
			latest: api.BLEDeviceStatus{FirstSeen: metav1.NewTime(now)},
		}
		i.devices[name] = device
	}
	device.seenAt = now
	device.latest.LastSeen = metav1.NewTime(now)
	observation.mergeInto(&device.latest)
	if !exists {
		i.publish(device, watch.Added)
	}
}

func (i *Index) tick(now time.Time) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for name, device := range i.devices {
		if now.Sub(device.seenAt) >= deviceTTL {
			i.remove(name, device)
		} else if device.seenAt.After(device.published.Status.LastSeen.Time) {
			i.publish(device, watch.Modified)
		}
	}
}

func (i *Index) evictOldest() {
	var oldestName string
	var oldest *sighting
	for name, device := range i.devices {
		if oldest == nil || device.seenAt.Before(oldest.seenAt) ||
			(device.seenAt.Equal(oldest.seenAt) && name < oldestName) {
			oldestName, oldest = name, device
		}
	}
	i.remove(oldestName, oldest)
}

func (i *Index) remove(name string, device *sighting) {
	i.publish(device, watch.Deleted)
	delete(i.devices, name)
}

func (i *Index) publish(device *sighting, eventType watch.EventType) {
	i.revision++
	object := device.published.DeepCopy()
	object.Status = *device.latest.DeepCopy()
	object.ResourceVersion = i.version()
	i.history[(i.revision-1)%historySize] = watch.Event{Type: eventType, Object: object}
	device.published = object
	i.notify()
}

func (i *Index) notify() {
	close(i.changed)
	i.changed = make(chan struct{})
}
