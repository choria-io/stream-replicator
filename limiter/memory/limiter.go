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
	jsonField      string
	jsonForceField string
	header         string
	token          int
	duration       time.Duration
	replicator     string
	stream         string
	name           string
	processed      *idtrack.Tracker
	stateFile      string
	syncSubj       string
	log            *logrus.Entry
	mu             *sync.Mutex
}

func New(ctx context.Context, wg *sync.WaitGroup, cfg *config.Stream, name string, replicator string, nc *nats.Conn, log *logrus.Entry) (*limiter, error) {
	if cfg.InspectDuration == 0 {
		return nil, fmt.Errorf("inspect duration not set, memory limiter can not start")
	}
	if name == "" {
		return nil, fmt.Errorf("name is not set, memory limiter can not start")
	}
	if replicator == "" {
		return nil, fmt.Errorf("replicator name is required")
	}
	if cfg.InspectJSONForceField != "" && cfg.InspectJSONField == "" {
		return nil, fmt.Errorf("forcing based on json field requires both inspect_field and inspect_force_field set")
	}

	l := &limiter{
		name:           name,
		duration:       cfg.InspectDuration,
		jsonField:      cfg.InspectJSONField,
		jsonForceField: cfg.InspectJSONForceField,
		header:         cfg.InspectHeaderValue,
		token:          cfg.InspectSubjectToken,
		stateFile:      cfg.StateFile,
		stream:         cfg.Stream,
		replicator:     replicator,
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
	case l.jsonField != "":
		l.log = l.log.WithField("field", l.jsonField)
		if l.jsonForceField != "" {
			l.log = l.log.WithField("force_field", l.jsonForceField)
		}

	case l.header != "":
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
	var mustCopy bool

	switch {
	case l.jsonForceField != "" && l.jsonField != "":
		res := gjson.GetBytes(msg.Data, l.jsonField)
		if res.Exists() {
			trackValue = res.String()
		}

		res = gjson.GetBytes(msg.Data, l.jsonForceField)
		if res.Exists() {
			mustCopy = true
		}

	case l.jsonField != "":
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

	case l.header != "":
		trackValue = msg.Header.Get(l.header)
	}

	if trackValue == "" {
		limiterMessagesWithoutTrackingFieldCount.WithLabelValues("memory", l.name, l.replicator).Inc()
	}

	if mustCopy {
		limiterMessageForcedByField.WithLabelValues("memory", l.name, l.replicator).Inc()
	}

	sz := float64(len(msg.Data))
	var shouldProcess bool

	if mustCopy {
		shouldProcess = true
	} else {
		shouldProcess = l.processed.ShouldProcess(trackValue, sz)
	}
	l.processed.RecordSeen(trackValue, sz)

	err := f(msg, shouldProcess)
	if err != nil {
		return err
	}

	if shouldProcess || mustCopy {
		l.processed.RecordCopied(trackValue)
	}

	return nil
}
