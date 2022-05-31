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
			Stream:             "TEST",
			TargetStream:       "TEST_COPY",
			TargetPrefix:       "copy.x.redundant.",
			TargetRemoveString: "redundant",
			SourceURL:          srv,
			TargetURL:          srv,
		}

		sr := &config.Config{
			ReplicatorName: "GINKGO",
			Streams:        []*config.Stream{stream},
		}

		return sr, stream
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
		tcs, err := mgr.NewStream("TEST_COPY", jsm.Subjects("copy.>"))
		Expect(err).ToNot(HaveOccurred())

		publishToSource(nc, "TEST", insert)

		return ts, tcs
	}

	// for use with Eventually()
	streamMesssage := func(s *jsm.Stream) func() (uint64, error) {
		return func() (uint64, error) {
			nfo, err := s.State()
			if err != nil {
				return 0, err
			}
			return nfo.Msgs, nil
		}
	}

	// for use with Eventually()
	resumeSeq := func(s *Stream) func() uint64 {
		return func() uint64 {
			s.mu.Lock()
			r := s.source.resumeSeq
			s.mu.Unlock()

			return r
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

				Eventually(streamMesssage(tcs)).Should(BeNumerically(">=", 1000))

				// check prefix and remove string works
				msg, err := tcs.ReadMessage(1)
				Expect(err).ToNot(HaveOccurred())
				Expect(msg.Subject).To(Equal("copy.x.TEST"))
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

				Eventually(streamMesssage(tcs)).Should(BeNumerically(">=", 1000))
			})
		})

		It("Should support skipping old messages", func() {
			testutil.WithJetStream(log, func(nc *nats.Conn, mgr *jsm.Manager) {
				ts, tcs := prepareStreams(nc, mgr, 1000)

				sr, scfg := config(nc.ConnectedUrl())
				scfg.MaxAgeDuration = time.Second

				stream, err := NewStream(scfg, sr, log)
				Expect(err).ToNot(HaveOccurred())

				time.Sleep(time.Second)

				publishToSource(nc, "TEST", 10)

				go func() {
					defer GinkgoRecover()
					wg.Add(1)
					Expect(stream.Run(ctx, &wg)).ToNot(HaveOccurred())
				}()
				defer cancel()

				nfo, err := ts.State()
				Expect(err).ToNot(HaveOccurred())
				Eventually(resumeSeq(stream)).Should(BeNumerically(">=", nfo.LastSeq))

				nfo, err = tcs.State()
				Expect(err).ToNot(HaveOccurred())
				Expect(nfo.Msgs).To(Equal(uint64(10)))
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
				Eventually(streamMesssage(tcs)).Should(BeNumerically(">=", 10))

				nfo, err := ts.State()
				Expect(err).ToNot(HaveOccurred())
				Eventually(resumeSeq(stream)).Should(BeNumerically(">=", nfo.LastSeq))
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

				Eventually(streamMesssage(tcs)).Should(BeNumerically(">=", 1000))

				consumer, err := ts.LoadConsumer(stream.cname)
				Expect(err).ToNot(HaveOccurred())
				Expect(consumer.Delete()).ToNot(HaveOccurred())

				publishToSource(nc, "TEST", 1000)
				Eventually(streamMesssage(tcs)).Should(BeNumerically(">=", 2000))
			})
		})

		It("Should support leader election", func() {
			testutil.WithJetStream(log, func(nc *nats.Conn, mgr *jsm.Manager) {
				js, err := nc.JetStream()
				Expect(err).ToNot(HaveOccurred())
				_, err = js.CreateKeyValue(&nats.KeyValueConfig{Bucket: "CHORIA_LEADER_ELECTION", TTL: 2 * time.Second})
				Expect(err).ToNot(HaveOccurred())

				_, tcs := prepareStreams(nc, mgr, 1000)
				sr, scfg := config(nc.ConnectedUrl())
				scfg.LeaderElectionName = "ginkgo.example.net"

				stream, err := NewStream(scfg, sr, log)
				stream.hcInterval = 10 * time.Millisecond
				Expect(err).ToNot(HaveOccurred())

				go func() {
					defer GinkgoRecover()
					wg.Add(1)
					Expect(stream.Run(ctx, &wg)).ToNot(HaveOccurred())
				}()
				defer cancel()

				Eventually(streamMesssage(tcs), "1m").Should(BeNumerically(">=", 1000))
			})
		})
	})
})
