// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestConfig(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Config")
}

var _ = Describe("Config", func() {
	var cfg *Config
	BeforeEach(func() {
		cfg = &Config{
			ReplicatorName: "GINKGO",
		}
	})

	Describe("validate", func() {
		It("Should require a replicator name", func() {
			cfg.ReplicatorName = ""

			err := cfg.validate()
			Expect(err).To(MatchError("name is required"))
		})

		It("Should create the state directory", func() {
			tf, err := os.CreateTemp("", "")
			Expect(err).ToNot(HaveOccurred())
			tf.Close()
			os.Remove(tf.Name())
			defer os.RemoveAll(tf.Name())

			cfg.StateDirectory = tf.Name()

			Expect(cfg.validate()).ToNot(HaveOccurred())
			Expect(tf.Name()).To(BeADirectory())
		})

		It("Should require a stream", func() {
			cfg.Streams = []*Stream{{}}
			Expect(cfg.validate()).To(MatchError("stream not specified"))
		})

		It("Should support inheriting TLS from the replicator", func() {
			cfg.TLS = &TLS{}
			cfg.Streams = []*Stream{
				{Stream: "GINKGO"},
			}
			Expect(cfg.validate()).ToNot(HaveOccurred())
			Expect(cfg.Streams[0].TLS).To(BeIdenticalTo(cfg.TLS))
			Expect(cfg.Streams[0].SourceTLS).To(BeIdenticalTo(cfg.TLS))
			Expect(cfg.Streams[0].TargetTLS).To(BeIdenticalTo(cfg.TLS))
		})

		It("Should support source specific TLS", func() {
			stls := &TLS{}
			cfg.TLS = &TLS{}
			cfg.Streams = []*Stream{
				{Stream: "GINKGO", SourceTLS: stls},
			}
			Expect(cfg.validate()).ToNot(HaveOccurred())
			Expect(cfg.Streams[0].TLS).To(BeIdenticalTo(cfg.TLS))
			Expect(cfg.Streams[0].SourceTLS).To(BeIdenticalTo(stls))
			Expect(cfg.Streams[0].TargetTLS).To(BeIdenticalTo(cfg.TLS))
		})

		It("Should support target specific TLS", func() {
			ttls := &TLS{}
			cfg.TLS = &TLS{}
			cfg.Streams = []*Stream{
				{Stream: "GINKGO", TargetTLS: ttls},
			}
			Expect(cfg.validate()).ToNot(HaveOccurred())
			Expect(cfg.Streams[0].TLS).To(BeIdenticalTo(cfg.TLS))
			Expect(cfg.Streams[0].TargetTLS).To(BeIdenticalTo(ttls))
			Expect(cfg.Streams[0].SourceTLS).To(BeIdenticalTo(cfg.TLS))
		})

		It("Should configure the state file", func() {
			cfg.StateDirectory = os.TempDir()
			cfg.Streams = []*Stream{{Stream: "GINKGO"}}
			Expect(cfg.validate()).ToNot(HaveOccurred())
			Expect(cfg.Streams[0].StateFile).To(Equal(filepath.Join(os.TempDir(), "GINKGO_GINKGO.json")))

			cfg.Streams[0].Name = "OTHER"
			Expect(cfg.validate()).ToNot(HaveOccurred())
			Expect(cfg.Streams[0].StateFile).To(Equal(filepath.Join(os.TempDir(), "GINKGO_OTHER.json")))
		})

		It("Should parse inspect durations", func() {
			cfg.Streams = []*Stream{{
				Stream:           "GINKGO",
				StartDeltaString: "wrong",
			}}
			Expect(cfg.validate()).To(MatchError("invalid start_delta: invalid time unit g"))
			cfg.Streams[0].StartDeltaString = "1h"
			Expect(cfg.validate()).ToNot(HaveOccurred())
			Expect(cfg.Streams[0].StartDelta).To(Equal(time.Hour))

			cfg.Streams[0].InspectDurationString = "wrong"
			Expect(cfg.validate()).To(MatchError("invalid inspect_duration: invalid time unit g"))
			cfg.Streams[0].InspectDurationString = "1h"
			Expect(cfg.validate()).ToNot(HaveOccurred())
			Expect(cfg.Streams[0].InspectDuration).To(Equal(time.Hour))

			cfg.Streams[0].WarnDurationString = "wrong"
			Expect(cfg.validate()).To(MatchError("invalid warn_duration: invalid time unit g"))
			cfg.Streams[0].WarnDurationString = "1h"
			Expect(cfg.validate()).ToNot(HaveOccurred())
			Expect(cfg.Streams[0].WarnDuration).To(Equal(time.Hour))

			cfg.Streams[0].MaxAgeString = "wrong"
			Expect(cfg.validate()).To(MatchError("invalid max_age: invalid time unit g"))
			cfg.Streams[0].MaxAgeString = "1h"
			Expect(cfg.validate()).ToNot(HaveOccurred())
			Expect(cfg.Streams[0].MaxAgeDuration).To(Equal(time.Hour))
		})

		It("Should fail for duplicate names on the same stream", func() {
			cfg.Streams = []*Stream{
				{Stream: "TEST"},
				{Stream: "TEST"},
				{Stream: "OTHER"},
			}

			err := cfg.validate()
			Expect(err).To(MatchError(errors.New("duplicate stream configuration name GINKGO for stream TEST")))

			cfg.Streams[1].Name = "OTHER"
			Expect(cfg.validate()).ToNot(HaveOccurred())
		})

		It("Should support only 1 inspection mode", func() {
			cfg.Streams = []*Stream{
				{Stream: "TEST", InspectJSONField: "x"},
			}
			Expect(cfg.validate()).ToNot(HaveOccurred())

			cfg.Streams = []*Stream{
				{Stream: "TEST", InspectSubjectToken: 1, InspectJSONField: "x"},
			}
			Expect(cfg.validate()).To(MatchError("only one inspection mode can be set per stream"))

			cfg.Streams = []*Stream{
				{Stream: "TEST", InspectHeaderValue: "H", InspectJSONField: "x"},
			}
			Expect(cfg.validate()).To(MatchError("only one inspection mode can be set per stream"))

		})
	})
})
