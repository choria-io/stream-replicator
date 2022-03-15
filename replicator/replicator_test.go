// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package replicator

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/choria-io/stream-replicator/config"
	"github.com/choria-io/stream-replicator/internal/testutil"
	"github.com/nats-io/jsm.go"
	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
)

func TestReplicator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Replicator")
}

var _ = Describe("Replicator", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		wg     = sync.WaitGroup{}
		log    *logrus.Entry
	)

	BeforeEach(func() {
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		logger := logrus.New()
		logger.SetOutput(GinkgoWriter)
		log = logrus.NewEntry(logger)
	})

	AfterEach(func() {
		cancel()
		wg.Wait()
	})

	config := func(srv string) (*config.Config, *config.Stream) {
		stream := &config.Stream{
			Stream:       "TEST",
			TargetStream: "TEST_COPY",
			TargetPrefix: "copy",
			SourceURL:    srv,
			TargetURL:    srv,
		}

		sr := &config.Config{
			ReplicatorName: "GINKGO",
			Streams:        []*config.Stream{stream},
		}

		return sr, stream
	}

	waitForStreamMessages := func(s *jsm.Stream, n uint64, timeout time.Duration) {
		to, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				nfo, err := s.State()
				Expect(err).ToNot(HaveOccurred())
				if nfo.Msgs >= n {
					return
				}
			case <-to.Done():
				Fail(fmt.Sprintf("timeout waiting for %d messages on stream %s", n, s.Name()))
			}
		}
	}

	publishToSource := func(nc *nats.Conn, subject string, insert int) {
		for i := 1; i <= insert; i++ {
			_, err := nc.Request("TEST", []byte(fmt.Sprintf(`{"msg":%d,"sender":"host%d"}`, i, i%10)), time.Second)
			Expect(err).ToNot(HaveOccurred())
		}
	}

	prepareStreams := func(nc *nats.Conn, mgr *jsm.Manager, insert int) (*jsm.Stream, *jsm.Stream) {
		ts, err := mgr.NewStream("TEST")
		Expect(err).ToNot(HaveOccurred())
		tcs, err := mgr.NewStream("TEST_COPY", jsm.Subjects("copy.TEST"))
		Expect(err).ToNot(HaveOccurred())

		publishToSource(nc, "TEST", insert)

		return ts, tcs
	}

	waitForResumeSeq := func(s *Stream, seq uint64) {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				s.mu.Lock()
				r := s.source.resumeSeq
				s.mu.Unlock()
				if r >= seq {
					return
				}
			case <-ctx.Done():
				Fail(fmt.Sprintf("timeout waiting for %d resume sequence", seq))
			}
		}
	}

	Describe("NewStream", func() {
		It("Should support a given consumer name with default", func() {
			sr, cfg := config("nats://localhost:4222")

			stream, err := NewStream(cfg, sr, log)
			Expect(err).ToNot(HaveOccurred())
			Expect(stream.cname).To(Equal("stream_replicator"))

			cfg.Name = "CUSTOM_NAME"
			stream, err = NewStream(cfg, sr, log)
			Expect(err).ToNot(HaveOccurred())
			Expect(stream.cname).To(Equal("SR_CUSTOM_NAME"))
		})
	})

	Describe("Run", func() {
		It("Should copy all data without inspection", func() {
			testutil.WithJetStream(log, func(nc *nats.Conn, mgr *jsm.Manager) {
				_, tcs := prepareStreams(nc, mgr, 1000)

				sr, scfg := config(nc.ConnectedUrl())
				stream, err := NewStream(scfg, sr, log)
				Expect(err).ToNot(HaveOccurred())

				go func() {
					defer GinkgoRecover()
					wg.Add(1)
					Expect(stream.Run(ctx, &wg)).ToNot(HaveOccurred())
				}()
				defer cancel()

				waitForStreamMessages(tcs, 1000, 10*time.Second)
			})
		})

		It("Should copy all data when no inspection data is found", func() {
			testutil.WithJetStream(log, func(nc *nats.Conn, mgr *jsm.Manager) {
				_, tcs := prepareStreams(nc, mgr, 1000)

				sr, scfg := config(nc.ConnectedUrl())
				scfg.InspectJSONField = "unknown"
				scfg.InspectDuration = time.Hour
				scfg.WarnDuration = 30 * time.Minute

				stream, err := NewStream(scfg, sr, log)
				Expect(err).ToNot(HaveOccurred())

				go func() {
					defer GinkgoRecover()
					wg.Add(1)
					Expect(stream.Run(ctx, &wg)).ToNot(HaveOccurred())
				}()
				defer cancel()

				waitForStreamMessages(tcs, 1000, 10*time.Second)
			})
		})

		It("Should limit correctly", func() {
			testutil.WithJetStream(log, func(nc *nats.Conn, mgr *jsm.Manager) {
				ts, tcs := prepareStreams(nc, mgr, 1000)

				sr, scfg := config(nc.ConnectedUrl())
				scfg.InspectJSONField = "sender"
				scfg.InspectDuration = time.Hour
				scfg.WarnDuration = 30 * time.Minute

				stream, err := NewStream(scfg, sr, log)
				Expect(err).ToNot(HaveOccurred())

				go func() {
					defer GinkgoRecover()
					wg.Add(1)
					Expect(stream.Run(ctx, &wg)).ToNot(HaveOccurred())
				}()
				defer cancel()

				// 10 unique senders in the stream
				waitForStreamMessages(tcs, 10, 10*time.Second)

				nfo, err := ts.State()
				Expect(err).ToNot(HaveOccurred())
				waitForResumeSeq(stream, nfo.LastSeq)
			})
		})

		It("Should recover from consumer loss", func() {
			testutil.WithJetStream(log, func(nc *nats.Conn, mgr *jsm.Manager) {
				ts, tcs := prepareStreams(nc, mgr, 1000)

				sr, scfg := config(nc.ConnectedUrl())

				stream, err := NewStream(scfg, sr, log)
				stream.hcInterval = 10 * time.Millisecond
				Expect(err).ToNot(HaveOccurred())

				go func() {
					defer GinkgoRecover()
					wg.Add(1)
					Expect(stream.Run(ctx, &wg)).ToNot(HaveOccurred())
				}()
				defer cancel()

				waitForStreamMessages(tcs, 1000, 10*time.Second)

				consumer, err := ts.LoadConsumer(stream.cname)
				Expect(err).ToNot(HaveOccurred())
				Expect(consumer.Delete()).ToNot(HaveOccurred())

				publishToSource(nc, "TEST", 1000)
				log.Infof("Starting to wait")
				waitForStreamMessages(tcs, 2000, 10*time.Second)
				log.Infof("Finished waiting")
			})
		})
	})
})
