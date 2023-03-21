// Copyright (c) 2022-2023, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

// Package heartbeat defines the heartbeat system that starts up along side the stream replicator.
// If configured it will send a heartbeat messages to the configured subjects on a configurable interval.
package heartbeat

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/choria-io/stream-replicator/backoff"
	"github.com/choria-io/stream-replicator/config"
	"github.com/choria-io/stream-replicator/election"
	"github.com/choria-io/stream-replicator/internal/util"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

const (
	DefaultUpdateInterval = "10s"
	OriginatorHeader      = "Choria-SR-Originator"
	SubjectHeader         = "Choria-SR-Subject"
)

var (
	enableBackoff = true
	stubHostname  string // Facilitates testing
)

type HeartBeat struct {
	url            string
	replicatorName string
	electionName   string
	tls            config.TLS
	choria         config.ChoriaConnection
	leaderElection bool
	interval       string
	headers        map[string]string
	subjects       []*Subject
	log            *logrus.Entry
	paused         atomic.Bool
	hostname       string
}

type Subject struct {
	name     string
	interval time.Duration
	headers  map[string]string
}

// New creates a new instance of the Heartbeat struct
func New(hbcfg *config.HeartBeat, replicatorName string, log *logrus.Entry) (*HeartBeat, error) {
	var err error
	hb := &HeartBeat{
		replicatorName: replicatorName,
		electionName:   fmt.Sprintf("SR_%s_HB", replicatorName),
		tls:            hbcfg.TLS,
		choria:         hbcfg.Choria,
		leaderElection: hbcfg.LeaderElection,
		headers:        hbcfg.Headers,
		log:            log,
		url:            hbcfg.URL,
	}

	if hb.headers == nil {
		hb.headers = make(map[string]string)
	}

	hb.interval = DefaultUpdateInterval
	if hbcfg.Interval != "" {
		hb.interval = hbcfg.Interval
	}

	hb.hostname, err = hb.getHostName()
	if err != nil {
		return nil, fmt.Errorf("cannot create heartbeat: %v", err)
	}

	if hb.leaderElection {
		hb.paused.Store(true)
		hbPaused.WithLabelValues(hb.replicatorName, hb.hostname).Set(1)
	}

	for _, s := range hbcfg.Subjects {
		var err error
		sub := &Subject{name: s.Name}
		if s.Interval == "" {
			s.Interval = hb.interval
		}

		sub.interval, err = util.ParseDurationString(s.Interval)
		if err != nil {
			return nil, err
		}

		if s.Headers == nil {
			s.Headers = make(map[string]string)
		}
		sub.headers = s.Headers

		for k, v := range hb.headers {
			if _, ok := sub.headers[k]; ok {
				continue
			}
			sub.headers[k] = v
		}
		hb.subjects = append(hb.subjects, sub)
	}

	return hb, nil
}

// Run initializes a the jetstream connection and spawns a go routine for every configured subject
// that will publish a heartbeat message on the defined interval
func (hb *HeartBeat) Run(ctx context.Context, wg *sync.WaitGroup) error {
	nc, err := util.ConnectNats(ctx, "subject-heartbeats", hb.url, &hb.tls, &hb.choria, false, hb.log)
	if err != nil {
		return err
	}

	if hb.leaderElection {
		err = hb.setupElection(ctx, nc)
		if err != nil {
			hb.log.Errorf("Could not set up elections: %v", err)
			return err
		}
	}

	js, err := nc.JetStream()
	if err != nil {
		return fmt.Errorf("unable to create jetstream context: %v", err)
	}

	for _, subject := range hb.subjects {
		wg.Add(1)
		hbSubjects.WithLabelValues(hb.replicatorName).Inc()
		go heartBeatWorker(ctx, wg, subject, nc, js, hb.replicatorName, hb.hostname, &hb.paused, hb.log.WithField("subject", subject.name))
	}

	return nil
}

func heartBeatWorker(ctx context.Context, wg *sync.WaitGroup, sub *Subject, nc *nats.Conn, js nats.JetStreamContext, replicatorName, hostname string, paused *atomic.Bool, log *logrus.Entry) {
	defer wg.Done()

	log.Infof("Starting heartbeat with interval: %v", sub.interval)

	msg := nats.NewMsg(sub.name)
	for k, v := range sub.headers {
		msg.Header.Add(k, v)
	}

	msg.Header.Add(OriginatorHeader, hostname)
	msg.Header.Add(SubjectHeader, sub.name)

	ticker := time.NewTicker(sub.interval)
	if enableBackoff {
		ticker.Reset(1 * time.Hour)
		time.AfterFunc(backoff.FiveSec.Duration(10), func() { ticker.Reset(sub.interval) })
	}

	for {
		select {
		case <-ticker.C:
			if paused.Load() {
				log.Debug("Not sending heartbeat when paused")
				continue
			}
			msg.Data = []byte(strconv.Itoa(int(time.Now().Unix())))

			timer := hbPublishTime.WithLabelValues(replicatorName, sub.name)
			obs := prometheus.NewTimer(timer)
			log.Debugf("Sending heartbeat message")
			_, err := js.PublishMsg(msg, nats.AckWait(2*time.Second))
			obs.ObserveDuration()

			// Check the function PublishMSG
			if err != nil {
				hbPublishedCtrErr.WithLabelValues(replicatorName, sub.name).Inc()
				log.Errorf("Unable to publish message to subject: %v", err)
			}

			hbPublishedCtr.WithLabelValues(replicatorName, sub.name).Inc()

		case <-ctx.Done():
			return
		}
	}
}

func (hb *HeartBeat) setupElection(ctx context.Context, nc *nats.Conn) error {
	js, err := nc.JetStream()
	if err != nil {
		return err
	}

	kv, err := js.KeyValue("CHORIA_LEADER_ELECTION")
	if err != nil {
		return err
	}

	win := func() {
		hb.log.Warnf("%s became the leader", hb.hostname)
		hb.paused.Store(false)
		hbPaused.WithLabelValues(hb.replicatorName, hb.hostname).Set(0)
	}

	lost := func() {
		hb.log.Warnf("%s lost the leadership", hb.hostname)
		hb.paused.Store(true)
		hbPaused.WithLabelValues(hb.replicatorName, hb.hostname).Set(1.0)
	}

	e, err := election.NewElection(hb.hostname, hb.electionName, kv, election.WithBackoff(backoff.FiveSec), election.OnWon(win), election.OnLost(lost))
	if err != nil {
		return err
	}

	go e.Start(ctx)

	hb.log.Infof("Set up leader election 'heartbeat' using candidate name %s", hb.hostname)
	return nil
}

// Facilitates testing
func (hb *HeartBeat) getHostName() (string, error) {
	if stubHostname != "" {
		// Reset the stub value so that we don't leak hostnames between tests
		rval := stubHostname
		stubHostname = ""
		return rval, nil
	} else {
		hostname, err := os.Hostname()
		if err != nil {
			return "", err
		}
		return hostname, nil
	}
}
