// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package memory

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
)

func TestLimiter(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Limiter")
}

var _ = Describe("Limiter", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		wg     = sync.WaitGroup{}
		log    *logrus.Entry
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		logger := logrus.New()
		logger.SetOutput(GinkgoWriter)
		log = logrus.NewEntry(logger)
	})

	AfterEach(func() {
		cancel()
		wg.Wait()
	})

	Describe("ProcessAndRecord", func() {
		It("Should handle missing fields", func() {
			limiter, err := New(ctx, &wg, "sender", "", 0, time.Hour, 30*time.Minute, 1024, "GINKGO", "", "STREAM", "GINKGO", log)
			Expect(err).ToNot(HaveOccurred())

			msg := nats.NewMsg("test")
			msg.Data = []byte(`{"hello":"world"}`)

			Expect(limiter.ProcessAndRecord(msg, func(msg *nats.Msg, process bool) error {
				if process {
					return nil
				}

				return fmt.Errorf("expected to process")
			})).ToNot(HaveOccurred())
		})

		It("Should handle present json fields", func() {
			limiter, err := New(ctx, &wg, "sender", "", 0, time.Hour, 30*time.Minute, 1024, "GINKGO", "", "STREAM", "GINKGO", log)
			Expect(err).ToNot(HaveOccurred())

			msg := nats.NewMsg("test")
			msg.Data = []byte(`{"sender":"some.node"}`)

			processed := 0
			skipped := 0

			handler := func(msg *nats.Msg, process bool) error {
				if process {
					processed++
				} else {
					skipped++
				}

				return nil
			}

			Expect(limiter.ProcessAndRecord(msg, handler)).ToNot(HaveOccurred())
			Expect(limiter.ProcessAndRecord(msg, handler)).ToNot(HaveOccurred())
			Expect(processed).To(Equal(1))
			Expect(skipped).To(Equal(1))
		})
	})

	It("Should handle absent token values", func() {
		limiter, err := New(ctx, &wg, "", "", 10, time.Hour, 30*time.Minute, 1024, "GINKGO", "", "STREAM", "GINKGO", log)
		Expect(err).ToNot(HaveOccurred())

		msg := nats.NewMsg("test")
		msg.Subject = "x"
		msg.Data = []byte(`{"sender":"some.node"}`)

		Expect(limiter.ProcessAndRecord(msg, func(msg *nats.Msg, process bool) error {
			if process {
				return nil
			}

			return fmt.Errorf("expected to process")
		})).ToNot(HaveOccurred())
	})

	It("Should handle full subject inspections", func() {
		limiter, err := New(ctx, &wg, "", "", -1, time.Hour, 30*time.Minute, 1024, "GINKGO", "", "STREAM", "GINKGO", log)
		Expect(err).ToNot(HaveOccurred())

		msg := nats.NewMsg("test.1")
		msg.Header.Add("sender", "some.node")
		msg.Data = []byte(`{}`)

		processed := 0
		skipped := 0

		handler := func(msg *nats.Msg, process bool) error {
			if process {
				processed++
			} else {
				skipped++
			}

			return nil
		}

		Expect(limiter.ProcessAndRecord(msg, handler)).ToNot(HaveOccurred())
		Expect(limiter.ProcessAndRecord(msg, handler)).ToNot(HaveOccurred())
		Expect(processed).To(Equal(1))
		Expect(skipped).To(Equal(1))
		msg.Subject = "foo.1"
		Expect(limiter.ProcessAndRecord(msg, handler)).ToNot(HaveOccurred())
		Expect(processed).To(Equal(2))
	})

	It("Should handle present token values", func() {
		limiter, err := New(ctx, &wg, "", "", 2, time.Hour, 30*time.Minute, 1024, "GINKGO", "", "STREAM", "GINKGO", log)
		Expect(err).ToNot(HaveOccurred())

		msg := nats.NewMsg("test.1")
		msg.Header.Add("sender", "some.node")
		msg.Data = []byte(`{}`)

		processed := 0
		skipped := 0

		handler := func(msg *nats.Msg, process bool) error {
			if process {
				processed++
			} else {
				skipped++
			}

			return nil
		}

		Expect(limiter.ProcessAndRecord(msg, handler)).ToNot(HaveOccurred())
		Expect(limiter.ProcessAndRecord(msg, handler)).ToNot(HaveOccurred())
		Expect(processed).To(Equal(1))
		Expect(skipped).To(Equal(1))
		msg.Subject = "test.3"
		Expect(limiter.ProcessAndRecord(msg, handler)).ToNot(HaveOccurred())
		Expect(processed).To(Equal(2))
	})

	It("Should handle absent header values", func() {
		limiter, err := New(ctx, &wg, "", "sender", 0, time.Hour, 30*time.Minute, 1024, "GINKGO", "", "STREAM", "GINKGO", log)
		Expect(err).ToNot(HaveOccurred())

		msg := nats.NewMsg("test")
		msg.Data = []byte(`{"sender":"some.node"}`)

		Expect(limiter.ProcessAndRecord(msg, func(msg *nats.Msg, process bool) error {
			if process {
				return nil
			}

			return fmt.Errorf("expected to process")
		})).ToNot(HaveOccurred())
	})

	It("Should handle present header values", func() {
		limiter, err := New(ctx, &wg, "", "sender", 0, time.Hour, 30*time.Minute, 1024, "GINKGO", "", "STREAM", "GINKGO", log)
		Expect(err).ToNot(HaveOccurred())

		msg := nats.NewMsg("test")
		msg.Header.Add("sender", "some.node")
		msg.Data = []byte(`{}`)

		processed := 0
		skipped := 0

		handler := func(msg *nats.Msg, process bool) error {
			if process {
				processed++
			} else {
				skipped++
			}

			return nil
		}

		Expect(limiter.ProcessAndRecord(msg, handler)).ToNot(HaveOccurred())
		Expect(limiter.ProcessAndRecord(msg, handler)).ToNot(HaveOccurred())
		Expect(processed).To(Equal(1))
		Expect(skipped).To(Equal(1))
	})
})
