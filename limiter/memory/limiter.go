// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/choria-io/stream-replicator/idtrack"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

type limiter struct {
	field      string
	duration   time.Duration
	replicator string
	stream     string
	name       string
	processed  *idtrack.Tracker
	stateFile  string
	log        *logrus.Entry
	mu         *sync.Mutex
}

// TODO support inspecting a header and maybe a subject-prefix

func New(ctx context.Context, wg *sync.WaitGroup, inspectField string, inspectDuration time.Duration, warnDuration time.Duration, sizeTrigger float64, name string, stateFile string, stream string, replicator string, log *logrus.Entry) (*limiter, error) {
	if inspectField == "" {
		return nil, fmt.Errorf("inspect field not set, memory limiter can not start")
	}
	if inspectDuration == 0 {
		return nil, fmt.Errorf("inspect duration not set, memory limiter can not start")
	}
	if name == "" {
		return nil, fmt.Errorf("name is not set, memory limiter can not start")
	}
	if replicator == "" {
		return nil, fmt.Errorf("replicator name is required")
	}

	tracker, err := idtrack.New(ctx, wg, inspectDuration, warnDuration, sizeTrigger, stateFile, stream, replicator, log)
	if err != nil {
		return nil, err
	}

	l := &limiter{
		name:       name,
		duration:   inspectDuration,
		field:      inspectField,
		processed:  tracker,
		stateFile:  stateFile,
		stream:     stream,
		replicator: replicator,
		mu:         &sync.Mutex{},
		log: log.WithFields(logrus.Fields{
			"limiter":  "memory",
			"field":    inspectField,
			"duration": inspectDuration.String(),
		}),
	}

	l.log.Debugf("Memory based limiter started")

	return l, nil
}

func (l *limiter) Tracker() *idtrack.Tracker {
	return l.processed
}

func (l *limiter) ProcessAndRecord(msg *nats.Msg, f func(msg *nats.Msg, process bool) error) error {
	var trackValue string

	res := gjson.GetBytes(msg.Data, l.field)
	if res.Exists() {
		trackValue = res.String()
	} else {
		limiterMessagesWithoutTrackingFieldCount.WithLabelValues("memory", l.name, l.replicator).Inc()
	}

	sz := float64(len(msg.Data))
	shouldProcess := l.processed.ShouldProcess(trackValue, sz)
	l.processed.RecordSeen(trackValue, sz)

	return f(msg, shouldProcess)
}
