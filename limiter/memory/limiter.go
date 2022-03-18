// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/choria-io/stream-replicator/idtrack"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

type limiter struct {
	jsonField  string
	header     string
	token      int
	duration   time.Duration
	replicator string
	stream     string
	name       string
	processed  *idtrack.Tracker
	stateFile  string
	log        *logrus.Entry
	mu         *sync.Mutex
}

var _EMPTY_ = ""

func New(ctx context.Context, wg *sync.WaitGroup, inspectJSONField string, inspectHeader string, inspectToken int, inspectDuration time.Duration, warnDuration time.Duration, sizeTrigger float64, name string, stateFile string, stream string, replicator string, log *logrus.Entry) (*limiter, error) {
	if inspectDuration == 0 {
		return nil, fmt.Errorf("inspect duration not set, memory limiter can not start")
	}
	if name == _EMPTY_ {
		return nil, fmt.Errorf("name is not set, memory limiter can not start")
	}
	if replicator == _EMPTY_ {
		return nil, fmt.Errorf("replicator name is required")
	}

	l := &limiter{
		name:       name,
		duration:   inspectDuration,
		jsonField:  inspectJSONField,
		header:     inspectHeader,
		token:      inspectToken,
		stateFile:  stateFile,
		stream:     stream,
		replicator: replicator,
		log: log.WithFields(logrus.Fields{
			"limiter":  "memory",
			"duration": inspectDuration.String(),
		}),
		mu: &sync.Mutex{},
	}

	switch {
	case inspectJSONField != _EMPTY_:
		l.log = log.WithField("field", inspectJSONField)

	case inspectHeader != _EMPTY_:
		l.log = log.WithField("header", inspectHeader)

	case inspectToken != 0:
		l.log = log.WithField("token", inspectToken)

	default:
		return nil, fmt.Errorf("inspect field, header or token not set, memory limiter can not start")
	}

	var err error
	l.processed, err = idtrack.New(ctx, wg, inspectDuration, warnDuration, sizeTrigger, stateFile, stream, replicator, log)
	if err != nil {
		return nil, err
	}

	l.log.Debugf("Memory based limiter started")

	return l, nil

}

func (l *limiter) Tracker() *idtrack.Tracker {
	return l.processed
}

func (l *limiter) ProcessAndRecord(msg *nats.Msg, f func(msg *nats.Msg, process bool) error) error {
	var trackValue string

	switch {
	case l.jsonField != _EMPTY_:
		res := gjson.GetBytes(msg.Data, l.jsonField)
		if res.Exists() {
			trackValue = res.String()
		}

	case l.token == -1:
		trackValue = msg.Subject

	case l.token > 0:
		parts := strings.Split(msg.Subject, ".")
		if len(parts) >= l.token {
			trackValue = parts[l.token-1]
		}

	case l.header != _EMPTY_:
		trackValue = msg.Header.Get(l.header)
	}

	if trackValue == _EMPTY_ {
		limiterMessagesWithoutTrackingFieldCount.WithLabelValues("memory", l.name, l.replicator).Inc()
	}

	sz := float64(len(msg.Data))
	shouldProcess := l.processed.ShouldProcess(trackValue, sz)
	l.processed.RecordSeen(trackValue, sz)

	return f(msg, shouldProcess)
}
