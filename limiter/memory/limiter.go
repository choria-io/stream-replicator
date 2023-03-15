// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package memory

import (
	"context"
	"fmt"
	"github.com/nats-io/nats.go"
	"strings"
	"sync"
	"time"

	"github.com/choria-io/stream-replicator/config"
	"github.com/choria-io/stream-replicator/idtrack"
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
	syncSubj   string
	log        *logrus.Entry
	mu         *sync.Mutex
}

var _EMPTY_ = ""

func New(ctx context.Context, wg *sync.WaitGroup, cfg *config.Stream, name string, replicator string, nc *nats.Conn, log *logrus.Entry) (*limiter, error) {
	if cfg.InspectDuration == 0 {
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
		duration:   cfg.InspectDuration,
		jsonField:  cfg.InspectJSONField,
		header:     cfg.InspectHeaderValue,
		token:      cfg.InspectSubjectToken,
		stateFile:  cfg.StateFile,
		stream:     cfg.Stream,
		replicator: replicator,
		log: log.WithFields(logrus.Fields{
			"limiter":  "memory",
			"duration": cfg.InspectDuration.String(),
		}),
		mu: &sync.Mutex{},
	}

	if cfg.AdvisoryConf != nil {
		l.syncSubj = fmt.Sprintf("choria.stream-replicator.sync.%s.%s", cfg.Stream, cfg.Name)
	}

	switch {
	case l.jsonField != _EMPTY_:
		l.log = l.log.WithField("field", l.jsonField)

	case l.header != _EMPTY_:
		l.log = l.log.WithField("header", l.header)

	case l.token != 0:
		l.log = l.log.WithField("token", l.token)

	default:
		return nil, fmt.Errorf("inspect field, header or token not set, memory limiter can not start")
	}

	var err error
	l.processed, err = idtrack.New(ctx, wg, l.duration, cfg.WarnDuration, cfg.PayloadSizeTrigger, l.stateFile, l.stream, cfg.Name, replicator, nc, l.syncSubj, log)
	if err != nil {
		return nil, err
	}

	l.log.Infof("Memory based limiter started")

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

	err := f(msg, shouldProcess)
	if err != nil {
		return err
	}

	if shouldProcess {
		l.processed.RecordCopied(trackValue)
	}

	return nil
}
