// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package replicator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/choria-io/stream-replicator/config"
	"github.com/choria-io/stream-replicator/internal/testutil"
	"github.com/nats-io/jsm.go"
	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
)

var _ = Describe("Target Initiated Copier", func() {
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

		DeferCleanup(func() {
			cancel()
			wg.Wait()
		})
	})

	config := func(srv string) (*config.Config, *config.Stream) {
		stream := &config.Stream{
			Stream:             "TEST",
			Name:               "TR",
			TargetStream:       "TEST_COPY",
			TargetPrefix:       "copy.x.redundant.",
			FilterSubject:      "TEST",
			TargetRemoveString: "redundant",
			SourceURL:          srv,
			TargetURL:          srv,
			TargetInitiated:    true,
		}

		sr := &config.Config{
			ReplicatorName: "GINKGO",
			Streams:        []*config.Stream{stream},
		}

		return sr, stream
	}

	publishToSource := func(nc *nats.Conn, subject string, insert int) {
		for i := 1; i <= insert; i++ {
			_, err := nc.Request("TEST", []byte(fmt.Sprintf("%d", i)), time.Second)
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

	getMsg := func(s *jsm.Stream, seq uint64) (*nats.Msg, error) {
		msg, err := s.ReadMessage(seq)
		if err != nil {
			return nil, err
		}

		hdr, err := decodeHeadersMsg(msg.Header)
		if err != nil {
			return nil, err
		}

		nMsg := nats.NewMsg(msg.Subject)
		nMsg.Data = msg.Data
		nMsg.Header = hdr

		return nMsg, nil
	}

	Describe("copyMessages", func() {
		It("Should support resuming at the correct location when the consumer disappear", func() {
			testutil.WithJetStream(log, func(nc *nats.Conn, mgr *jsm.Manager) {
				ts, tcs := prepareStreams(nc, mgr, 1000)

				// copy the first 1000 messages
				sr, scfg := config(nc.ConnectedUrl())
				stream, err := NewStream(scfg, sr, log)
				Expect(err).ToNot(HaveOccurred())
				stream.hcInterval = 50 * time.Millisecond

				go func() {
					defer GinkgoRecover()
					wg.Add(1)
					Expect(stream.Run(ctx, &wg)).To(Succeed())
				}()
				Eventually(streamMesssage(tcs)).Should(BeNumerically("==", 1000))

				consumers, err := ts.ConsumerNames()
				Expect(err).ToNot(HaveOccurred())
				Expect(consumers).To(HaveLen(1))
				c, err := ts.LoadConsumer(consumers[0])
				Expect(err).ToNot(HaveOccurred())
				Expect(c.Delete()).To(Succeed())

				stream.source.mu.Lock()
				Expect(stream.source.resumeSeq).To(Equal(uint64(1001)))
				stream.source.mu.Unlock()

				// publish another 1000
				publishToSource(nc, "TEST", 1000)
				Eventually(streamMesssage(ts)).Should(BeNumerically("==", 2000))

				// and total messages should be  after a while 2k
				Eventually(streamMesssage(tcs)).Should(BeNumerically("==", 2000))

				// now we check some individual messages make sure there are no gaps
				msg, err := getMsg(tcs, 1)
				Expect(err).ToNot(HaveOccurred())
				Expect(msg.Data).To(Equal([]byte(`1`)))
				Expect(msg.Header.Get(srcHeader)).To(HavePrefix("TEST 1 GINKGO TR"))

				msg, err = getMsg(tcs, 1000)
				Expect(err).ToNot(HaveOccurred())
				Expect(msg.Data).To(Equal([]byte(`1000`)))
				Expect(msg.Header.Get(srcHeader)).To(HavePrefix("TEST 1000 GINKGO TR"))

				// first message after the restart
				msg, err = getMsg(tcs, 1001)
				Expect(err).ToNot(HaveOccurred())
				Expect(msg.Data).To(Equal([]byte(`1`)))
				Expect(msg.Header.Get(srcHeader)).To(HavePrefix("TEST 1001 GINKGO TR"))

				msg, err = getMsg(tcs, 2000)
				Expect(err).ToNot(HaveOccurred())
				Expect(msg.Data).To(Equal([]byte(`1000`)))
				Expect(msg.Header.Get(srcHeader)).To(HavePrefix("TEST 2000 GINKGO TR"))
			})
		})

		It("Should support resuming at the correct location after restart", func() {
			testutil.WithJetStream(log, func(nc *nats.Conn, mgr *jsm.Manager) {
				ts, tcs := prepareStreams(nc, mgr, 1000)

				// copy the first 1000 messages
				sr, scfg := config(nc.ConnectedUrl())
				stream, err := NewStream(scfg, sr, log)
				Expect(err).ToNot(HaveOccurred())

				runCtx, runCancel := context.WithTimeout(ctx, time.Second)
				go func() {
					defer GinkgoRecover()
					wg.Add(1)
					Expect(stream.Run(runCtx, &wg)).To(Succeed())
				}()
				Eventually(streamMesssage(tcs)).Should(BeNumerically("==", 1000))
				runCancel()
				wg.Wait()

				// publish another 1000
				publishToSource(nc, "TEST", 1000)
				Eventually(streamMesssage(ts)).Should(BeNumerically("==", 2000))

				// start it new, it should now resume where it was
				runCtx, runCancel = context.WithTimeout(ctx, time.Second)
				stream, err = NewStream(scfg, sr, log)
				Expect(err).ToNot(HaveOccurred())
				go func() {
					defer GinkgoRecover()
					wg.Add(1)
					Expect(stream.Run(runCtx, &wg)).To(Succeed())
				}()
				// and total messages should be 2k
				Eventually(streamMesssage(tcs)).Should(BeNumerically("==", 2000))
				runCancel()
				wg.Wait()

				// now we check some individual messages make sure there are no gaps
				msg, err := getMsg(tcs, 1)
				Expect(err).ToNot(HaveOccurred())
				Expect(msg.Data).To(Equal([]byte(`1`)))
				Expect(msg.Header.Get(srcHeader)).To(HavePrefix("TEST 1 GINKGO TR"))

				msg, err = getMsg(tcs, 1000)
				Expect(err).ToNot(HaveOccurred())
				Expect(msg.Data).To(Equal([]byte(`1000`)))
				Expect(msg.Header.Get(srcHeader)).To(HavePrefix("TEST 1000 GINKGO TR"))

				// first message after the restart
				msg, err = getMsg(tcs, 1001)
				Expect(err).ToNot(HaveOccurred())
				Expect(msg.Data).To(Equal([]byte(`1`)))
				Expect(msg.Header.Get(srcHeader)).To(HavePrefix("TEST 1001 GINKGO TR"))

				msg, err = getMsg(tcs, 2000)
				Expect(err).ToNot(HaveOccurred())
				Expect(msg.Data).To(Equal([]byte(`1000`)))
				Expect(msg.Header.Get(srcHeader)).To(HavePrefix("TEST 2000 GINKGO TR"))
			})
		})

		It("Should resume from the correct location after purge", func() {
			testutil.WithJetStream(log, func(nc *nats.Conn, mgr *jsm.Manager) {
				ts, tcs := prepareStreams(nc, mgr, 1000)

				// copy the first 1000 messages
				sr, scfg := config(nc.ConnectedUrl())
				stream, err := NewStream(scfg, sr, log)
				Expect(err).ToNot(HaveOccurred())

				runCtx, runCancel := context.WithTimeout(ctx, 2*time.Second)
				go func() {
					defer GinkgoRecover()
					wg.Add(1)
					Expect(stream.Run(runCtx, &wg)).To(Succeed())
				}()
				Eventually(streamMesssage(tcs)).Should(BeNumerically("==", 1000))
				runCancel()
				wg.Wait()

				Expect(tcs.Purge()).To(Succeed())
				pt := time.Now()

				// start it new, it should now resume based on the timestamp
				runCtx, runCancel = context.WithTimeout(ctx, 100*time.Second)
				stream, err = NewStream(scfg, sr, log)
				Expect(err).ToNot(HaveOccurred())
				go func() {
					defer GinkgoRecover()
					wg.Add(1)
					Expect(stream.Run(runCtx, &wg)).To(Succeed())
				}()

				// lets the copier make the consumer
				time.Sleep(500 * time.Millisecond)

				Expect(stream.source.consumer).To(Not(BeNil()))
				Expect(stream.source.consumer.Configuration().OptStartTime).To(Not(BeNil()))
				Expect(*stream.source.consumer.Configuration().OptStartTime).To(BeTemporally("~", pt, 50*time.Millisecond))

				// publish another 1000
				publishToSource(nc, "TEST", 1000)
				Eventually(streamMesssage(ts)).Should(BeNumerically("==", 2000))

				// and total messages should be 1k, as we purged 1000 out it should now only sent the newest 1000
				Eventually(streamMesssage(tcs)).Should(BeNumerically("==", 1000))

				runCancel()
				wg.Wait()

			})
		})

		It("Should copy all data", func() {
			testutil.WithJetStream(log, func(nc *nats.Conn, mgr *jsm.Manager) {
				_, tcs := prepareStreams(nc, mgr, 1000)

				sr, scfg := config(nc.ConnectedUrl())
				stream, err := NewStream(scfg, sr, log)
				Expect(err).ToNot(HaveOccurred())

				go func() {
					defer GinkgoRecover()
					wg.Add(1)
					Expect(stream.Run(ctx, &wg)).To(Succeed())
				}()
				defer cancel()

				Eventually(streamMesssage(tcs)).Should(BeNumerically(">=", 1000))

				Expect(stream.copier).To(BeAssignableToTypeOf(&targetInitiatedCopier{}))

				// check prefix and remove string works
				msg, err := tcs.ReadMessage(1)
				Expect(err).ToNot(HaveOccurred())
				Expect(msg.Subject).To(Equal("copy.x.TEST"))
			})
		})
	})
})
