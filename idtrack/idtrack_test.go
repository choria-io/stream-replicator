// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package idtrack

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
)

func TestTracker(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tracker")
}

var _ = Describe("Tracker", func() {
	var (
		ctx     context.Context
		cancel  context.CancelFunc
		wg      = sync.WaitGroup{}
		log     *logrus.Entry
		tracker *Tracker
		td      *os.File
		err     error
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		logger := logrus.New()
		logger.SetOutput(GinkgoWriter)
		log = logrus.NewEntry(logger)

		td, err = os.CreateTemp("", "")
		Expect(err).ToNot(HaveOccurred())
		td.Close()

		tracker, err = New(ctx, &wg, 60*time.Minute, 30*time.Minute, 1024, td.Name(), "TEST", "1", "GINKGO", nil, "", log)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(td.Name())
		cancel()
		wg.Wait()
	})

	Describe("scrub", func() {
		It("Should correctly detect expired entries", func() {
			for i := 1; i <= 10; i++ {
				tracker.RecordSeen(fmt.Sprintf("%d", i), float64(i*1024))
			}

			tracker.Items["1"].Seen = time.Now().UTC().Add(-1 * (time.Hour + 10*time.Second))
			tracker.Items["2"].Seen = time.Now().UTC().Add(-1 * (time.Hour - 10*time.Second))
			tracker.Items["3"].Seen = time.Now().UTC().Add(-1 * (time.Hour + 10*time.Second))
			tracker.Items["4"].Seen = time.Now().UTC().Add(-1 * (time.Hour - 10*time.Second))
			tracker.Items["5"].Seen = time.Now().UTC().Add(-1 * (time.Hour + 10*time.Second))
			tracker.Items["6"].Seen = time.Now().UTC().Add(-1 * (30*time.Minute + time.Second))

			expiredCnt := 0
			var expiredNodes []string
			tracker.NotifyExpired(func(expired map[string]Item) {
				expiredCnt = len(expired)
				for k := range expired {
					expiredNodes = append(expiredNodes, k)
				}
				sort.Strings(expiredNodes)
			})

			warnCnt := 0
			var warnedNodes []string
			tracker.NotifyAgeWarning(func(warn map[string]Item) {
				warnCnt = len(warn)
				for k := range warn {
					warnedNodes = append(warnedNodes, k)
				}
				sort.Strings(warnedNodes)
			})

			tracker.scrub()

			time.Sleep(10 * time.Millisecond)

			Expect(expiredCnt).To(Equal(3))
			Expect(expiredNodes).To(Equal([]string{"1", "3", "5"}))

			Expect(warnCnt).To(Equal(3))
			Expect(warnedNodes).To(Equal([]string{"2", "4", "6"}))
		})

		It("Should correctly detect warning entries", func() {})
	})

	Describe("State Load and Save", func() {
		It("Should save the state correctly", func() {
			tf, err := os.CreateTemp("", "")
			Expect(err).ToNot(HaveOccurred())
			tf.Close()
			defer os.Remove(tf.Name())

			tracker.stateFile = tf.Name()

			for i := 1; i <= 10; i++ {
				tracker.RecordSeen(fmt.Sprintf("%d", i), float64(i*1024))
			}
			Expect(tracker.Items).To(HaveLen(10))
			Expect(tracker.saveState()).ToNot(HaveOccurred())

			tracker.Items = make(map[string]*Item)
			Expect(tracker.loadState()).ToNot(HaveOccurred())
			Expect(tracker.Items).To(HaveLen(10))
		})
	})

	Describe("ShouldProcess", func() {
		It("Should handle empty values", func() {
			Expect(tracker.ShouldProcess("", 0)).To(BeTrue())
		})

		It("Should handle zero time as true", func() {
			Expect(tracker.ShouldProcess("new", 0)).To(BeTrue())
		})

		It("Should handle zero size as true", func() {
			tracker.RecordSeen("new", 0)
			tracker.RecordCopied("new")

			// zero to zero
			Expect(tracker.ShouldProcess("new", 0)).To(BeFalse())
			// zero to anything
			Expect(tracker.ShouldProcess("new", 10)).To(BeTrue())
		})

		It("Should trigger on size increases", func() {
			tracker.RecordSeen("new", 1024)
			tracker.RecordCopied("new")

			Expect(tracker.ShouldProcess("new", 1999)).To(BeFalse())
			Expect(tracker.ShouldProcess("new", 2048)).To(BeTrue())
		})

		It("Should trigger on size decreases", func() {
			tracker.RecordSeen("new", 2048)
			tracker.RecordCopied("new")

			Expect(tracker.ShouldProcess("new", 1999)).To(BeFalse())
			Expect(tracker.ShouldProcess("new", 1024)).To(BeTrue())
		})

		It("Should correctly handle the deadline", func() {
			tracker.noScrub = true
			tracker.RecordSeen("new", 2048)
			tracker.RecordCopied("new")

			tracker.Items["new"].Seen = time.Now().UTC().Add(-1 * 50 * time.Minute)
			Expect(tracker.ShouldProcess("new", 2048)).To(BeFalse())
			tracker.Items["new"].Seen = time.Now().UTC().Add(-1 * time.Hour)
			Expect(tracker.ShouldProcess("new", 2048)).To(BeTrue())
		})

		It("Should support last copied sampling", func() {
			tracker.RecordSeen("new", 2048)
			tracker.RecordCopied("new")

			tracker.Items["new"].Seen = time.Now().UTC().Add(-1 * 10 * time.Minute)
			Expect(tracker.ShouldProcess("new", 2048)).To(BeFalse())
			tracker.Items["new"].Copied = time.Now().UTC().Add(-1 * 61 * time.Minute)
			Expect(tracker.ShouldProcess("new", 2048)).To(BeTrue())
		})
	})

	Describe("lastSeen", func() {
		It("Should return the correct items", func() {
			t, _, sz := tracker.lastSeen("new")
			Expect(t.IsZero()).To(BeTrue())
			Expect(sz).To(Equal(float64(0)))

			tracker.RecordSeen("new", 1024)
			tracker.RecordCopied("new")
			t, copied, sz := tracker.lastSeen("new")
			Expect(t).To(BeTemporally("~", time.Now().UTC(), 500*time.Millisecond))
			Expect(copied).To(BeTemporally("~", time.Now().UTC(), 500*time.Millisecond))
			Expect(sz).To(Equal(float64(1024)))
		})
	})

	Describe("RecordAdvised", func() {
		It("Should correctly set the advised state", func() {
			tracker.RecordSeen("new", 1024)
			Expect(tracker.Items["new"].Advised).To(BeFalse())
			tracker.RecordAdvised("new")
			Expect(tracker.Items["new"].Advised).To(BeTrue())
		})
	})

	It("Should support registering callbacks", func() {
		Expect(tracker.recoverCB).To(BeNil())
		tracker.NotifyRecover(func(v string, i Item) {})
		Expect(tracker.recoverCB).ToNot(BeNil())

		Expect(tracker.warnCB).To(BeNil())
		tracker.NotifyAgeWarning(func(items map[string]Item) {})
		Expect(tracker.warnCB).ToNot(BeNil())

		Expect(tracker.firstSeenCB).To(BeNil())
		tracker.NotifyFirstSeen(func(v string, i Item) {})
		Expect(tracker.firstSeenCB).ToNot(BeNil())

		Expect(tracker.expireCB).To(BeNil())
		tracker.NotifyExpired(func(items map[string]Item) {})
		Expect(tracker.expireCB).ToNot(BeNil())
	})

	Describe("RecordSeen", func() {
		It("Should handle non existing items", func() {
			notified := false
			tracker.NotifyFirstSeen(func(v string, item Item) {
				notified = v == "new"
			})

			Expect(tracker.Items).To(HaveLen(0))
			tracker.RecordSeen("new", 1024)
			Expect(tracker.Items).To(HaveKey("new"))
			Expect(tracker.Items).To(HaveLen(1))

			time.Sleep(10 * time.Millisecond) // it's a go routine handling the notify

			Expect(notified).To(BeTrue())
		})

		It("Should notify on recoveries", func() {
			notified := false
			tracker.NotifyRecover(func(v string, i Item) {
				notified = v == "new"
			})

			tracker.RecordSeen("new", 1024)
			tracker.Items["new"].Seen = time.Now().UTC().Add(-1 * 40 * time.Minute)
			tracker.RecordSeen("new", 1024)

			time.Sleep(10 * time.Millisecond) // it's a go routine handling the notify

			Expect(notified).To(BeTrue())
		})

		It("Should correctly record the state", func() {
			tracker.RecordSeen("new", 1024)

			item := tracker.Items["new"]
			Expect(item.Seen).To(BeTemporally("~", time.Now().UTC(), 500*time.Millisecond))
			Expect(item.Advised).To(BeFalse())
			Expect(item.Size).To(Equal(float64(1024)))
		})
	})
})
