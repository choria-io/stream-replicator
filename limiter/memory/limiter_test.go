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
			limiter, err := New(ctx, &wg, "sender", time.Hour, 30*time.Minute, 1024, "GINKGO", "", "STREAM", "GINKGO", log)
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

		It("Should handle present fields", func() {
			limiter, err := New(ctx, &wg, "sender", time.Hour, 30*time.Minute, 1024, "GINKGO", "", "STREAM", "GINKGO", log)
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
})
