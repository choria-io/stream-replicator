// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package advisor

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/choria-io/stream-replicator/config"
	"github.com/choria-io/stream-replicator/idtrack"
	"github.com/choria-io/stream-replicator/internal/testutil"
	"github.com/golang/mock/gomock"
	"github.com/nats-io/jsm.go"
	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
)

func TestTracker(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Advisor")
}

var _ = Describe("Advisor", func() {
	var (
		ctx      context.Context
		cancel   context.CancelFunc
		wg       = sync.WaitGroup{}
		log      *logrus.Entry
		tracker  *MockTracker
		mockctrl *gomock.Controller
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		logger := logrus.New()
		logger.SetOutput(GinkgoWriter)
		log = logrus.NewEntry(logger)
		mockctrl = gomock.NewController(GinkgoT())
		tracker = NewMockTracker(mockctrl)

		tracker.EXPECT().NotifyFirstSeen(gomock.Any()).AnyTimes()
		tracker.EXPECT().NotifyAgeWarning(gomock.Any()).AnyTimes()
		tracker.EXPECT().NotifyRecover(gomock.Any()).AnyTimes()
		tracker.EXPECT().NotifyExpired(gomock.Any()).AnyTimes()
	})

	AfterEach(func() {
		cancel()
		wg.Wait()
		mockctrl.Finish()
	})

	setup := func(nc *nats.Conn) (*Advisor, error) {
		return New(ctx, &wg, &config.Advisory{Subject: "advisories.%s.%v"}, nc, tracker, "sender", "STREAM", "GINKGO", log)
	}

	assertAdvisoryType := func(msg *nats.Msg, v string, et EventType) {
		adv := &AgeAdvisoryV2{}
		err := json.Unmarshal(msg.Data, adv)
		Expect(err).ToNot(HaveOccurred())

		Expect(adv.Protocol).To(Equal(AdvisoryProtocol))
		Expect(adv.EventID).To(HaveLen(27))
		Expect(adv.InspectField).To(Equal("sender"))
		Expect(adv.Age).To(BeNumerically("~", 0))
		Expect(adv.Seen).To(BeNumerically("~", time.Now().Unix()), 1)
		Expect(adv.Replicator).To(Equal("GINKGO"))
		Expect(adv.Timestamp).To(BeNumerically("~", time.Now().Unix()), 1)
		Expect(adv.Event).To(Equal(et))
		Expect(adv.Value).To(Equal(v))
	}

	It("Should support first seen callbacks", func() {
		testutil.WithJetStream(log, func(nc *nats.Conn, _ *jsm.Manager) {
			adv, err := setup(nc)
			Expect(err).ToNot(HaveOccurred())

			sub, err := nc.SubscribeSync("advisories.>")
			Expect(err).ToNot(HaveOccurred())

			adv.firstSeenCB("ginkgo.example.net", idtrack.Item{Seen: time.Now()})

			msg, err := sub.NextMsg(time.Second)
			Expect(err).ToNot(HaveOccurred())
			Expect(msg.Subject).To(Equal("advisories.new.ginkgo.example.net"))

			assertAdvisoryType(msg, "ginkgo.example.net", FirstSeenEvent)
		})
	})

	It("Should support expired callbacks", func() {
		testutil.WithJetStream(log, func(nc *nats.Conn, _ *jsm.Manager) {
			adv, err := setup(nc)
			Expect(err).ToNot(HaveOccurred())

			sub, err := nc.SubscribeSync("advisories.>")
			Expect(err).ToNot(HaveOccurred())

			adv.expireCB(map[string]idtrack.Item{
				"new": {Seen: time.Now()},
				"old": {Seen: time.Now()},
			})

			msg, err := sub.NextMsg(time.Second)
			Expect(err).ToNot(HaveOccurred())
			Expect(msg.Subject).To(Equal("advisories.expire.new"))
			assertAdvisoryType(msg, "new", ExpireEvent)

			msg, err = sub.NextMsg(time.Second)
			Expect(err).ToNot(HaveOccurred())
			Expect(msg.Subject).To(Equal("advisories.expire.old"))
			assertAdvisoryType(msg, "old", ExpireEvent)
		})
	})

	It("Should support recover callbacks", func() {
		testutil.WithJetStream(log, func(nc *nats.Conn, _ *jsm.Manager) {
			adv, err := setup(nc)
			Expect(err).ToNot(HaveOccurred())

			sub, err := nc.SubscribeSync("advisories.>")
			Expect(err).ToNot(HaveOccurred())

			adv.recoverCB("ginkgo.example.net", idtrack.Item{Seen: time.Now()})

			msg, err := sub.NextMsg(time.Second)
			Expect(err).ToNot(HaveOccurred())
			Expect(msg.Subject).To(Equal("advisories.recover.ginkgo.example.net"))

			assertAdvisoryType(msg, "ginkgo.example.net", RecoverEvent)
		})
	})

	It("Should support warn callbacks", func() {
		testutil.WithJetStream(log, func(nc *nats.Conn, _ *jsm.Manager) {
			adv, err := setup(nc)
			Expect(err).ToNot(HaveOccurred())

			sub, err := nc.SubscribeSync("advisories.>")
			Expect(err).ToNot(HaveOccurred())

			tracker.EXPECT().RecordAdvised("new").Times(1)
			tracker.EXPECT().RecordAdvised("old").Times(1)

			adv.warnCB(map[string]idtrack.Item{
				"new": {Seen: time.Now()},
				"old": {Seen: time.Now()},
			})

			msg, err := sub.NextMsg(time.Second)
			Expect(err).ToNot(HaveOccurred())
			Expect(msg.Subject).To(Equal("advisories.timeout.new"))
			assertAdvisoryType(msg, "new", TimeoutEvent)

			msg, err = sub.NextMsg(time.Second)
			Expect(err).ToNot(HaveOccurred())
			Expect(msg.Subject).To(Equal("advisories.timeout.old"))
			assertAdvisoryType(msg, "old", TimeoutEvent)
		})
	})
})
