// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package idtrack

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

type Item struct {
	Seen    time.Time `json:"seen"`
	Copied  time.Time `json:"copied"`
	Advised bool      `json:"advised,omitempty"`
	Size    float64   `json:"size"`
}

type Tracker struct {
	Items       map[string]*Item
	interval    time.Duration // how frequently we expect nodes to check in, IsRecent() is based on this
	warnAge     time.Duration
	sizeTrigger float64
	stateFile   string
	log         *logrus.Entry

	firstSeenCB func(string, Item)
	warnCB      func(map[string]Item)
	recoverCB   func(string, Item)
	expireCB    func(map[string]Item)

	stream     string
	replicator string
	worker     string

	sync.Mutex
}

const (
	_EMPTY_ = ""
)

func New(ctx context.Context, wg *sync.WaitGroup, interval time.Duration, warn time.Duration, sizeTrigger float64, stateFile string, stream string, worker string, replicator string, log *logrus.Entry) (*Tracker, error) {
	t := &Tracker{
		Items:       map[string]*Item{},
		interval:    interval,
		warnAge:     warn,
		sizeTrigger: sizeTrigger,
		stateFile:   stateFile,
		stream:      stream,
		worker:      worker,
		replicator:  replicator,
		Mutex:       sync.Mutex{},
	}

	t.log = log.WithFields(logrus.Fields{
		"state":    stateFile,
		"interval": t.interval,
		"warn":     warn,
	})

	err := t.loadState()
	if err != nil {
		t.log.Warnf("State loading failed, starting without state: %v", err)
	}

	wg.Add(1)
	go t.maintainer(ctx, wg)

	return t, nil
}

// NotifyRecover notifies a callback when an item that exceeded the warn threshold but not yet reached the expired threshold is seen again
func (t *Tracker) NotifyRecover(cb func(string, Item)) error {
	t.Lock()
	defer t.Unlock()

	if t.recoverCB != nil {
		return fmt.Errorf("call back already registered")
	}

	t.recoverCB = cb

	return nil
}

// NotifyAgeWarning notifies a callback when an item exceeds the warning threshold, callbacks will be sent until RecordAdvised() is called for an item, which will reset on recovery
func (t *Tracker) NotifyAgeWarning(cb func(map[string]Item)) error {
	t.Lock()
	defer t.Unlock()

	if t.warnCB != nil {
		return fmt.Errorf("call back already registered")
	}

	t.warnCB = cb

	return nil
}

// NotifyFirstSeen registers a callback for items that were seen for the first time
func (t *Tracker) NotifyFirstSeen(cb func(string, Item)) error {
	t.Lock()
	defer t.Unlock()

	if t.firstSeenCB != nil {
		return fmt.Errorf("call back already registered")
	}

	t.firstSeenCB = cb

	return nil
}

// NotifyExpired registers a callback for expired
func (t *Tracker) NotifyExpired(cb func(map[string]Item)) error {
	t.Lock()
	defer t.Unlock()

	if t.expireCB != nil {
		return fmt.Errorf("call back already registered")
	}

	t.expireCB = cb

	return nil
}

// RecordSeen records that we saw the item
func (t *Tracker) RecordSeen(v string, sz float64) {
	t.Lock()
	defer t.Unlock()

	i, ok := t.Items[v]
	if !ok {
		i = t.addItem(v)
	}

	if t.recoverCB != nil && !i.Seen.IsZero() {
		since := time.Since(i.Seen)
		if since > t.warnAge && since < t.interval {
			i.Advised = false
			go t.recoverCB(v, *i)
		}
	}

	i.Seen = time.Now().UTC()
	i.Size = sz
}

// RecordCopied records the fact the node data was copied, used to manage sampling
func (t *Tracker) RecordCopied(v string) {
	t.Lock()
	defer t.Unlock()

	i, ok := t.Items[v]
	if !ok {
		return
	}

	i.Copied = time.Now()
}

// RecordAdvised records that we advised about the item
func (t *Tracker) RecordAdvised(v string) {
	t.Lock()
	defer t.Unlock()

	i, ok := t.Items[v]
	if !ok {
		return
	}

	i.Advised = true
}

func (t *Tracker) lastSeen(v string) (time.Time, time.Time, float64) {
	t.Lock()
	defer t.Unlock()

	i, ok := t.Items[v]
	if !ok {
		return time.Time{}, time.Time{}, 0
	}

	return i.Seen, i.Copied, i.Size
}

// ShouldProcess determines if a message should be processed considering last seen times, size deltas and copied delta
func (t *Tracker) ShouldProcess(v string, sz float64) bool {
	if v == _EMPTY_ {
		return true
	}

	// we splay the deadline by 10% of the interval to avoid big copy spikes
	splay := rand.Int63n(int64(float64(t.interval) * 0.10))
	deadline := time.Now().Add(-1 * (t.interval - time.Second - time.Duration(splay)))

	seen, copied, psz := t.lastSeen(v)

	if copied.IsZero() || copied.Before(deadline) {
		return true
	}

	if seen.IsZero() || (psz == 0 && sz != 0) {
		return true
	}

	if t.sizeTrigger > 0 && math.Abs(psz-sz) >= t.sizeTrigger {
		return true
	}

	return seen.Before(deadline)
}

func (t *Tracker) maintainer(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	ticker := time.NewTicker(10 * time.Second)

	for {
		select {
		case <-ticker.C:
			t.saveState()
		case <-ctx.Done():
			t.saveState()
			t.log.Debugf("Maintenance process shutting down")
			return
		}
	}
}

func (t *Tracker) loadState() error {
	t.Lock()
	defer t.Unlock()

	if t.stateFile == _EMPTY_ {
		t.log.Warnf("Tacker state tracking not configured")
		return nil
	}

	if len(t.Items) > 0 {
		return fmt.Errorf("not empty")
	}

	if _, err := os.Stat(t.stateFile); os.IsNotExist(err) {
		return nil
	}

	d, err := os.ReadFile(t.stateFile)
	if err != nil {
		return err
	}

	tt := Tracker{}
	err = json.Unmarshal(d, &tt)
	if err != nil {
		return err
	}

	if len(tt.Items) == 0 {
		return nil
	}

	t.Items = tt.Items

	t.scrub()

	t.log.Infof("Read %d bytes of last-processed data with %d active entries", len(d), len(t.Items))

	return nil
}

func (t *Tracker) saveState() error {
	t.Lock()
	defer t.Unlock()

	if t.stateFile == _EMPTY_ {
		return nil
	}

	t.scrub()

	if len(t.Items) == 0 {
		// doesn't matter if it fails, load will scrub anyway
		os.Remove(t.stateFile)
		return nil
	}

	data, err := json.Marshal(t)
	if err != nil {
		return err
	}

	tmpfile, err := os.CreateTemp(filepath.Dir(t.stateFile), "cache")
	if err != nil {
		return fmt.Errorf("coult not create temp file: %v", err)
	}
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.Write(data)
	tmpfile.Close()
	if err != nil {

		return fmt.Errorf("temp file write failed: %v", err)
	}

	err = os.Rename(tmpfile.Name(), t.stateFile)
	if err != nil {
		return fmt.Errorf("rename failed: %v", err)
	}

	t.log.Debugf("Wrote %d bytes to last seen cache %s", len(data), t.stateFile)

	return nil
}

func (t *Tracker) scrub() {
	before := len(t.Items)

	expireDeadline := time.Now().Add(-1 * t.interval)
	expired := map[string]Item{}
	shouldExpire := t.expireCB != nil

	warnDeadline := time.Now().Add(-1 * t.warnAge)
	warnings := map[string]Item{}
	shouldWarn := t.warnCB != nil

	var deletes []string

	for v, item := range t.Items {
		if item.Seen.Before(expireDeadline) {
			if shouldExpire {
				expired[v] = *item
			}
			deletes = append(deletes, v)
		} else if shouldWarn && !item.Advised && item.Seen.Before(warnDeadline) {
			warnings[v] = *item
		}
	}

	for _, v := range deletes {
		trackedItems.WithLabelValues(t.stream, t.replicator, t.worker).Dec()
		delete(t.Items, v)
	}

	if shouldExpire && len(expired) > 0 {
		go t.expireCB(expired)
	}

	if shouldWarn && len(warnings) > 0 {
		go t.warnCB(warnings)
	}

	trackedItems.WithLabelValues(t.stream, t.replicator, t.worker).Set(float64(len(t.Items)))

	t.log.Debugf("Performed scrub %d -> %d", before, len(t.Items))
}

func (t *Tracker) addItem(v string) *Item {
	i := &Item{Seen: time.Now().UTC()}
	t.Items[v] = i

	trackedItems.WithLabelValues(t.stream, t.replicator, t.worker).Add(1)

	if t.firstSeenCB != nil {
		go t.firstSeenCB(v, *i)
	}

	return i
}
