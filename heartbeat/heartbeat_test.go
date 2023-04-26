// Copyright (c) 2022-2023, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package heartbeat

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/choria-io/stream-replicator/config"
	"github.com/choria-io/stream-replicator/election"
	"github.com/choria-io/stream-replicator/internal/testutil"
	"github.com/nats-io/jsm.go"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/sirupsen/logrus"
)

func TestHeartBeat(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Heartbeats")
}

var _ = Describe("Subject Heartbeat", func() {
	var (
		ctx      context.Context
		cancel   context.CancelFunc
		wg       = sync.WaitGroup{}
		log      *logrus.Entry
		hbConfig config.HeartBeat
	)

	BeforeEach(func() {
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		logger := logrus.New()
		logger.SetOutput(GinkgoWriter)
		log = logrus.NewEntry(logger)

		DeferCleanup(func() {
			cancel()
			wg.Wait()
		})

		hbConfig = config.HeartBeat{
			LeaderElection: false,
			Subjects: []config.Subject{
				{
					Name:     "heartbeat",
					Interval: "500ms",
				},
			},
		}
		enableBackoff = false
		election.SkipTTLValidateForTests()
	})

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

	Describe("run", func() {
		It("should send at least 1 heart beat message", func() {
			testutil.WithJetStream(log, func(_ *server.Server, nc *nats.Conn, mgr *jsm.Manager) {
				jstream, err := mgr.NewStream("TEST", jsm.Subjects("heartbeat"))
				Expect(err).ToNot(HaveOccurred())
				hbConfig.URL = nc.ConnectedUrl()

				hb, err := New(&hbConfig, "test_replicator", log)
				Expect(err).ToNot(HaveOccurred())

				go func() {
					defer GinkgoRecover()
					err = hb.Run(ctx, &wg)
					Expect(err).ToNot(HaveOccurred())
				}()
				defer cancel()
				Eventually(streamMesssage(jstream)).Should(BeNumerically(">=", 1))
			})
		})

		It("should send to multiple subjects", func() {
			testutil.WithJetStream(log, func(srv *server.Server, nc *nats.Conn, mgr *jsm.Manager) {
				hbConfig.Subjects = append(hbConfig.Subjects, config.Subject{
					Name:     "heartbeat2",
					Interval: "500ms",
				})

				jstream, err := mgr.NewStream("TEST", jsm.Subjects("heartbeat"))
				Expect(err).ToNot(HaveOccurred())
				jstream2, err := mgr.NewStream("TEST", jsm.Subjects("heartbeat"))
				Expect(err).ToNot(HaveOccurred())
				hbConfig.URL = nc.ConnectedUrl()

				hb, err := New(&hbConfig, "test_replicator", log)
				Expect(err).ToNot(HaveOccurred())

				go func() {
					defer GinkgoRecover()
					err = hb.Run(ctx, &wg)
					Expect(err).ToNot(HaveOccurred())
				}()
				defer cancel()
				Eventually(streamMesssage(jstream)).Should(BeNumerically(">=", 1))
				Eventually(streamMesssage(jstream2)).Should(BeNumerically(">=", 1))
			})
		})

		It("Should support in-process connections", func() {
			testutil.WithJetStream(log, func(srv *server.Server, nc *nats.Conn, mgr *jsm.Manager) {
				hbConfig.Subjects = append(hbConfig.Subjects, config.Subject{
					Name:     "heartbeat2",
					Interval: "500ms",
				})
				hbConfig.Process = srv
				hbConfig.URL = nc.ConnectedUrl()

				jstream, err := mgr.NewStream("TEST", jsm.Subjects("heartbeat"))
				Expect(err).ToNot(HaveOccurred())

				hb, err := New(&hbConfig, "test_replicator", log)
				Expect(err).ToNot(HaveOccurred())

				go func() {
					defer GinkgoRecover()
					err = hb.Run(ctx, &wg)
					Expect(err).ToNot(HaveOccurred())
				}()
				defer cancel()
				Eventually(streamMesssage(jstream)).Should(BeNumerically(">=", 1))

				conns, err := srv.Connz(nil)
				Expect(err).ToNot(HaveOccurred())
				conn := conns.Conns[1]
				if conn.IP != "" || conn.Port != 0 {
					Fail("Connection was not done in-process")
				}
			})
		})

		It("should send a well formed message", func() {
			testutil.WithJetStream(log, func(_ *server.Server, nc *nats.Conn, mgr *jsm.Manager) {
				jstream, err := mgr.NewStream("TEST", jsm.Subjects("heartbeat"))
				Expect(err).ToNot(HaveOccurred())
				hbConfig.URL = nc.ConnectedUrl()
				hbConfig.Headers = map[string]string{
					"test1": "value1",
				}
				hbConfig.Subjects[0].Headers = map[string]string{
					"test2": "value2",
				}

				stubHostname = "test_host"
				hb, err := New(&hbConfig, "test_replicator", log)
				Expect(err).ToNot(HaveOccurred())

				go func() {
					defer GinkgoRecover()
					err = hb.Run(ctx, &wg)
					Expect(err).ToNot(HaveOccurred())
				}()
				defer cancel()
				Eventually(streamMesssage(jstream)).Should(BeNumerically(">=", 1))

				x, err := nc.JetStream()
				Expect(err).ToNot(HaveOccurred())
				sub, err := x.PullSubscribe("heartbeat", "")
				Expect(err).ToNot(HaveOccurred())
				msgs, err := sub.Fetch(1)
				Expect(err).ToNot(HaveOccurred())

				timestamp, err := strconv.ParseInt(string(msgs[0].Data), 10, 64)
				Expect(err).ToNot(HaveOccurred())
				tm := time.Unix(timestamp, 0)

				Expect(tm).To(BeTemporally("~", time.Now().Add(-1*time.Second), 1*time.Second))
				Expect(msgs[0].Subject).To(Equal("heartbeat"))
				Expect(msgs[0].Header.Get(OriginatorHeader)).To(Equal("test_host"))
				Expect(msgs[0].Header.Get(SubjectHeader)).To(Equal("heartbeat"))
				Expect(msgs[0].Header.Get("test1")).To(Equal("value1"))
				Expect(msgs[0].Header.Get("test2")).To(Equal("value2"))
			})
		})

		It("should perform leader election and set metrics", func() {
			testutil.WithJetStream(log, func(_ *server.Server, nc *nats.Conn, mgr *jsm.Manager) {
				hbConfig.LeaderElection = true
				jstream, err := mgr.NewStream("TEST", jsm.Subjects("heartbeat"))
				Expect(err).ToNot(HaveOccurred())

				js, err := nc.JetStream()
				Expect(err).ToNot(HaveOccurred())
				_, err = js.CreateKeyValue(&nats.KeyValueConfig{
					Bucket: "CHORIA_LEADER_ELECTION",
					TTL:    750 * time.Millisecond,
				})
				Expect(err).ToNot(HaveOccurred())

				hbConfig.LeaderElection = true
				hbConfig.URL = nc.ConnectedUrl()

				stubHostname = "host1"
				hb1, err := New(&hbConfig, "leader_election_replicator", log)
				Expect(err).ToNot(HaveOccurred())

				stubHostname = "host2"
				hb2, err := New(&hbConfig, "leader_election_replicator", log)
				Expect(err).ToNot(HaveOccurred())

				go func() {
					defer GinkgoRecover()
					err = hb1.Run(ctx, &wg)
					Expect(err).ToNot(HaveOccurred())
					err = hb2.Run(ctx, &wg)
					Expect(err).ToNot(HaveOccurred())
				}()
				defer cancel()
				Eventually(streamMesssage(jstream), "10s").Should(BeNumerically(">=", 1))
				Eventually(hb1.paused.Load() == hb2.paused.Load(), "10s").Should(BeFalse())

				if !hb1.paused.Load() {
					Expect(getPromGaugeValue(hbPaused, "leader_election_replicator", "host1")).To(Equal(0.0))
					Expect(getPromGaugeValue(hbPaused, "leader_election_replicator", "host2")).To(Equal(1.0))
				} else {
					Expect(getPromGaugeValue(hbPaused, "leader_election_replicator", "host1")).To(Equal(1.0))
					Expect(getPromGaugeValue(hbPaused, "leader_election_replicator", "host2")).To(Equal(0.0))
				}

				Expect(getPromGaugeValue(hbSubjects, "leader_election_replicator")).To(BeNumerically(">=", 1.0))
				Expect(getPromCountValue(hbPublishedCtr, "leader_election_replicator", "heartbeat")).To(BeNumerically(">=", 1.0))
				Expect(getPromCountValue(hbPublishedCtrErr, "leader_election_replicator", "heartbeat")).To(Equal(0.0))
			})
		})
	})
})

func getPromCountValue(ctr *prometheus.CounterVec, labels ...string) float64 {
	pb := &dto.Metric{}
	m, err := ctr.GetMetricWithLabelValues(labels...)
	if err != nil {
		return 0
	}

	if m.Write(pb) != nil {
		return 0
	}

	return pb.GetCounter().GetValue()
}

func getPromGaugeValue(ctr *prometheus.GaugeVec, labels ...string) float64 {
	pb := &dto.Metric{}
	m, err := ctr.GetMetricWithLabelValues(labels...)
	if err != nil {
		return 0
	}

	if m.Write(pb) != nil {
		return 0
	}

	return pb.GetGauge().GetValue()
}
