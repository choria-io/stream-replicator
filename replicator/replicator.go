// Copyright (c) 2022-2023, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package replicator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/choria-io/stream-replicator/advisor"
	"github.com/choria-io/stream-replicator/backoff"
	"github.com/choria-io/stream-replicator/config"
	"github.com/choria-io/stream-replicator/election"
	"github.com/choria-io/stream-replicator/idtrack"
	"github.com/choria-io/stream-replicator/internal/util"
	"github.com/choria-io/stream-replicator/limiter/memory"
	"github.com/nats-io/jsm.go"
	"github.com/nats-io/jsm.go/api"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
)

type Limiter interface {
	ProcessAndRecord(msg *nats.Msg, f func(msg *nats.Msg, process bool) error) error
	Tracker() *idtrack.Tracker
}

type copier interface {
	copyMessages(context.Context) error
}

type Stream struct {
	sr         *config.Config
	cfg        *config.Stream
	cname      string
	log        *logrus.Entry
	source     *Target
	dest       *Target
	limiter    Limiter
	advisor    *advisor.Advisor
	hcInterval time.Duration
	paused     bool
	copier     copier
	mu         *sync.Mutex
}

type Target struct {
	mgr        *jsm.Manager
	stream     *jsm.Stream
	consumer   *jsm.Consumer
	cfg        api.StreamConfig
	nc         *nats.Conn
	mu         *sync.Mutex
	sub        *nats.Subscription
	resumeSeq  uint64
	resumeTime time.Time
}

const (
	pollFrequency    = 10 * time.Second
	srcHeader        = "Choria-SR-Source"
	srcHeaderPattern = "%s %d %s %s %d"
)

func (t *Target) Close() error {
	if t.nc != nil {
		err := t.nc.Drain()
		if err != nil {
			return err
		}

		t.nc.Close()
	}

	return nil
}

func NewStream(stream *config.Stream, sr *config.Config, log *logrus.Entry) (*Stream, error) {
	if stream.Stream == "" {
		return nil, fmt.Errorf("stream name is required")
	}
	if stream.SourceURL == "" {
		return nil, fmt.Errorf("source_url is required")
	}
	if stream.TargetURL == "" {
		return nil, fmt.Errorf("target_url is required")
	}
	if stream.TargetStream == "" {
		stream.TargetStream = stream.Stream
	}
	if stream.TargetInitiated && stream.NoTargetCreate {
		return nil, fmt.Errorf("target initiated streams requires no_target_create to not be set")
	}

	name := "stream_replicator"
	if stream.Name != "" {
		if strings.HasPrefix("SR_", stream.Name) {
			name = stream.Name
		} else {
			name = fmt.Sprintf("SR_%s", stream.Name)
		}
	}

	return &Stream{
		sr:         sr,
		cfg:        stream,
		cname:      name,
		mu:         &sync.Mutex{},
		hcInterval: time.Minute,
		paused:     stream.LeaderElectionName != "",
		log: log.WithFields(logrus.Fields{
			"source": stream.Stream,
			"target": stream.TargetStream,
		}),
	}, nil
}

func (s *Stream) Run(ctx context.Context, wg *sync.WaitGroup) error {
	defer wg.Done()

	var err error

	err = s.connect(ctx)
	if err != nil {
		s.log.Errorf("Could not setup connection: %v", err)
		return err
	}

	if s.cfg.InspectJSONField != "" && s.cfg.InspectDuration > 0 {
		nc, err := s.connectAdvisories(ctx)
		if err != nil {
			return err
		}

		s.limiter, err = memory.New(ctx, wg, s.cfg, s.cname, s.sr.ReplicatorName, nc, s.log)
		if err != nil {
			return err
		}

		if s.cfg.AdvisoryConf != nil {
			s.advisor, err = advisor.New(ctx, wg, s.cfg.AdvisoryConf, nc, s.limiter.Tracker(), s.cfg.InspectJSONField, s.cfg.Stream, s.sr.ReplicatorName, s.log)
			if err != nil {
				return err
			}
		}
	}

	if s.cfg.LeaderElectionName != "" {
		err = s.setupElection(ctx)
		if err != nil {
			s.log.Errorf("Could not set up elections: %v", err)
			return err
		}
	}

	if s.cfg.TargetInitiated {
		s.copier = newTargetInitiatedCopier(s, s.log)
	} else {
		s.copier = newSourceInitiatedCopier(s, s.log)
	}

	err = s.copier.copyMessages(ctx)
	if err != nil {
		s.log.Errorf("Copier failed: %v", err)
		return err
	}

	<-ctx.Done()
	s.log.Infof("Exiting on context interrupt")

	s.mu.Lock()
	defer s.mu.Unlock()

	s.source.Close()
	s.dest.Close()

	return nil
}

func (s *Stream) targetForSubject(subj string) string {
	if s.cfg.TargetPrefix != "" {
		subj = fmt.Sprintf("%s.%s", s.cfg.TargetPrefix, subj)
		subj = strings.Replace(subj, "..", ".", -1)
	}

	if s.cfg.TargetRemoveString != "" {
		subj = strings.Replace(subj, s.cfg.TargetRemoveString, "", -1)
		subj = strings.Replace(subj, "..", ".", -1)
	}

	return subj
}

func (s *Stream) setupElection(ctx context.Context) error {
	js, err := s.source.nc.JetStream()
	if err != nil {
		return err
	}

	kv, err := js.KeyValue("CHORIA_LEADER_ELECTION")
	if err != nil {
		return err
	}

	win := func() {
		s.mu.Lock()
		s.log.Warnf("Became the leader")
		s.paused = false
		if s.advisor != nil {
			s.advisor.Resume()
		}
		s.mu.Unlock()
	}

	lost := func() {
		s.mu.Lock()
		s.log.Warnf("Lost the leadership")
		s.paused = true
		if s.advisor != nil {
			s.advisor.Pause()
		}

		streamSequence.WithLabelValues(s.cfg.Stream, s.sr.ReplicatorName, s.cfg.Name).Set(0)

		s.mu.Unlock()
	}

	if s.advisor != nil {
		s.advisor.Pause()
	}

	e, err := election.NewElection(s.cfg.LeaderElectionName, fmt.Sprintf("%s_%s", s.cname, s.cfg.Stream), kv, election.WithReplicator(s.sr.ReplicatorName), election.WithBackoff(backoff.FiveSec), election.OnWon(win), election.OnLost(lost))
	if err != nil {
		return err
	}

	go e.Start(ctx)

	s.log.Infof("Set up leader election %s using candidate name %s", s.cname, s.cfg.LeaderElectionName)
	return nil
}

func (s *Stream) limitedProcess(msg *nats.Msg, cb func(msg *nats.Msg, process bool) error) error {
	if s.limiter == nil {
		return cb(msg, true)
	}

	return s.limiter.ProcessAndRecord(msg, cb)
}

func (s *Stream) isPaused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paused
}

func (s *Stream) connect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.source != nil || s.dest != nil {
		return fmt.Errorf("already have connections")
	}

	err := s.connectSource(ctx)
	if err != nil {
		return err
	}

	err = s.connectDestination(ctx)
	if err != nil {
		return err
	}

	if s.source == nil || s.dest == nil {
		return fmt.Errorf("connection setup failed")
	}

	return nil
}

func (s *Stream) connectAdvisories(ctx context.Context) (nc *nats.Conn, err error) {
	return util.ConnectNats(ctx, "stream-replicator-advisories", s.cfg.SourceURL, s.cfg.SourceTLS, s.cfg.SourceChoriaConn, false, s.cfg.SourceProcess, s.log.WithField("connection", "advisories"))
}

func (s *Stream) connectSource(ctx context.Context) (err error) {
	log := s.log.WithField("connection", "source")

	s.source, err = s.setupConnection(ctx, s.cfg.SourceURL, s.cfg.SourceTLS, s.cfg.SourceChoriaConn, s.cfg.SourceProcess, log)
	if err != nil {
		return fmt.Errorf("source connection failed: %v", err)
	}

	err = backoff.TwentySec.For(ctx, func(try int) error {
		s.source.stream, err = s.source.mgr.LoadStream(s.cfg.Stream)
		if err != nil {
			log.Infof("Loading stream failed on try %d: %v", try, err)
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	s.source.cfg = s.source.stream.Configuration()

	return err
}

func (s *Stream) connectDestination(ctx context.Context) (err error) {
	s.source.mu.Lock()
	scfg := s.source.cfg
	s.source.mu.Unlock()

	if s.cfg.TargetPrefix != "" || s.cfg.TargetRemoveString != "" {
		var subjects []string

		for _, sub := range scfg.Subjects {
			var target string

			if s.cfg.TargetPrefix != "" {
				target = fmt.Sprintf("%s.%s", s.cfg.TargetPrefix, sub)
			} else {
				target = sub
			}

			if s.cfg.TargetRemoveString != "" {
				target = strings.Replace(target, s.cfg.TargetRemoveString, "", -1)
				target = strings.Replace(target, "..", ".", -1)
			}

			subjects = append(subjects, target)
		}

		scfg.Subjects = subjects
	}

	log := s.log.WithField("connection", "target")

	s.dest, err = s.setupConnection(ctx, s.cfg.TargetURL, s.cfg.TargetTLS, s.cfg.TargetChoriaConn, s.cfg.TargetProcess, log)
	if err != nil {
		return fmt.Errorf("source connection failed: %v", err)
	}

	if s.cfg.NoTargetCreate {
		return nil
	}

	return backoff.TwentySec.For(ctx, func(try int) error {
		s.dest.stream, err = s.dest.mgr.LoadOrNewStreamFromDefault(s.cfg.TargetStream, scfg)
		if err != nil {
			log.Infof("Loading stream failed on try %d: %v", try, err)
			return err
		}

		return nil
	})
}

func (s *Stream) setupConnection(ctx context.Context, url string, tls *config.TLS, choria *config.ChoriaConnection, inproc nats.InProcessConnProvider, log *logrus.Entry) (*Target, error) {
	t := &Target{mu: &sync.Mutex{}}
	var err error

	t.nc, err = util.ConnectNats(ctx, s.cfg.Stream, url, tls, choria, true, inproc, log)
	if err != nil {
		return nil, err
	}

	t.mgr, err = jsm.New(t.nc)
	if err != nil {
		return nil, err
	}

	return t, nil
}
