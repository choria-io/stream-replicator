// Copyright (c) 2022-2023, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/choria-io/stream-replicator/internal/util"
	"github.com/ghodss/yaml"
	"github.com/nats-io/nats.go"
)

type Config struct {
	// ReplicatorName is a name for the site, used in stats, logs and headers to distinguish origin
	ReplicatorName string `json:"name"`
	// Streams are a list of streams to replicate
	Streams []*Stream `json:"streams"`
	// StateDirectory is where limiters will store state
	StateDirectory string `json:"state_store"`
	// TLS configures an overall default TLS when not set in stream or target/source level
	TLS *TLS `json:"tls"`
	// ChoriaConn configures an overall defaults Choria configuration when not set in stream or start/source level
	ChoriaConn *ChoriaConnection `json:"choria"`
	// MonitorPort is where prometheus stats will be exposed
	MonitorPort int `json:"monitor_port"`
	// Profiling enables starting go profiling on the monitor port
	Profiling bool `json:"profiling"`
	// LogLevel file to log to, stdout when empty
	LogFile string `json:"logfile"`
	// LogLevel is the logging level: debug, warn or info
	LogLevel string `json:"loglevel"`
	// Heartbeat defines monitoring heartbeats
	HeartBeat *HeartBeat `json:"heartbeats"`
}

type Stream struct {
	// Name is a friendly name that will be used in the consumer name and show up in every message header
	Name string `json:"name"`
	// Stream is the source stream name
	Stream string `json:"stream"`
	// FilterSubject creates a consumer that listens to a specific subject only
	FilterSubject string `json:"filter_subject"`
	// TargetStream is the name of the stream on the remote, if this is unset the Stream value will be used
	TargetStream string `json:"target_stream"`
	// TargetPrefix is a prefix to put in-front of subjects from the Stream. The final subject is <prefix>.<msg subject>
	TargetPrefix string `json:"target_subject_prefix"`
	// TargetRemoveString removes a part from the target subject after applying the prefix
	TargetRemoveString string `json:"target_subject_remove"`
	// SourceURL is the NATS server to source messages from in nats://user:pass@server form
	SourceURL string `json:"source_url"`
	// SourceProcess configures a in-process connection for the source
	SourceProcess nats.InProcessConnProvider `json:"-"`
	// NoTargetCreate in source initiated replication will prevent target stream creation or checks at start
	NoTargetCreate bool `json:"no_target_create"`
	// Ephemeral in source initiated replication indicates that an ephemeral consumer should be used, this will result in the entire stream being replicated at start, useful for KV buckets
	Ephemeral bool ` json:"ephemeral"`
	// TargetURL is the NATS server to send messages to in nats://user:pass@server form
	TargetURL string `json:"target_url"`
	// TargetProcess configures a in-process connection for the source
	TargetProcess nats.InProcessConnProvider `json:"-"`
	// TargetInitiated indicates that the replicator is running nearest to the target and so will use a latency optimized approach
	TargetInitiated bool `json:"target_initiated"`
	// StartSequence is an optional initial sequence to replicate from
	StartSequence uint64 `json:"start_sequence"`
	// StartTime is an optional initial time to replicate from in the RFC3339 form eg. 2006-01-02T15:04:05Z07:00
	StartTime time.Time `json:"start_time"`
	// StartDeltaString is a duration for time since now to start replicating from, 1h, 1d, 1w, 1M, 1Y
	StartDeltaString string `json:"start_delta"`
	// StartAtEnd indicates that the next message to arrive should be the first to be replicated
	StartAtEnd bool `json:"start_at_end"`
	// TLS is TLS settings that would be used, see also SourceTLS and TargetTLS
	TLS *TLS `json:"tls"`
	// SourceTLS overrides TLS for the source only
	SourceTLS *TLS `json:"source_tls"`
	// TargetTLS overrides TLS for the target only
	TargetTLS *TLS `json:"target_tls"`
	// ChoriaConn is Choria connection settings that would be used, see also SourceChoriaConn and TargetChoriaConn
	ChoriaConn *ChoriaConnection `json:"choria"`
	// SourceChoriaConn overrides the Choria connection for a specific source only
	SourceChoriaConn *ChoriaConnection `json:"source_choria"`
	// TargetChoriaConn overrides the Choria connection for a specific target only
	TargetChoriaConn *ChoriaConnection `json:"target_choria"`

	// MaxAgeString will skip messages older than this
	MaxAgeString string `json:"max_age"`
	// InspectJSONField will inspect a specific field in JSON payloads and limit sends by this field
	InspectJSONField string `json:"inspect_field"`
	// InspectJSONForceField will inspect a specific field in the JSON payloads and force the limiter to publish the message
	InspectJSONForceField string `json:"inspect_force_field"`
	// InspectHeaderValue inspects the value of a header and does limiting based on that
	InspectHeaderValue string `json:"inspect_header"`
	// InspectSubjectToken inspects a certain token and limits based on that, -1 inspects the entire subject, 0 disables
	InspectSubjectToken int `json:"inspect_subject_token"`
	// InspectDurationString will limit the sending of messages to 1 per duration based on the value of InspectJSONField
	InspectDurationString string `json:"inspect_duration"`
	// WarnDurationString is how long to allow an item not to be seen before advising about it
	WarnDurationString string `json:"warn_duration"`
	// PayloadSizeTrigger sets a trigger size that, if a message has a size change bigger than this, will cause an immediate replicate to do overriding the usual inspect_duration based limits
	PayloadSizeTrigger float64 `json:"size_trigger"`
	// LeaderElection indicates that this replicator is part of a group and will elect a leader to replicate, limiter will share state among the group
	LeaderElectionName string `json:"leader_election_name"`

	// AdvisoryConf configures advisories for streams with Inspection enabled
	AdvisoryConf *Advisory `json:"advisory"`

	// StartDelta is a parsed StartDeltaString
	StartDelta time.Duration `json:"-"`
	// InspectDuration is a parsed InspectDurationString
	InspectDuration time.Duration `json:"-"`
	// WarnDuration is a parsed WarnDurationString
	WarnDuration time.Duration `json:"-"`
	// MaxAgeDuration will discard messages older than this
	MaxAgeDuration time.Duration `json:"-"`
	// StateFile where state will be written
	StateFile string `json:"-"`
}

type HeartBeat struct {
	// LeaderElection indicates that this replicator is part of a group and will elect a leader to send hearbeats
	LeaderElection bool `json:"leader_election"`
	// Interval determines how often a heartbeat message will be sent
	Interval string `json:"interval"`
	// Headers are custom headers to add to the heartbeat message
	Headers map[string]string `json:"headers"`
	// Subjects are the subjects the heatbeat messages will be sent to
	Subjects []Subject `json:"subjects"`
	// TLS is TLS settings that would be used,
	TLS TLS `json:"tls"`
	// Choria is the Choria settings that would be used
	Choria ChoriaConnection `json:"choria"`
	// Process sets a in-process connection for the heartbeats
	Process nats.InProcessConnProvider
	// URL is the url of the nats broker
	URL string `json:"url"`
}

type Subject struct {
	// Name is the name of the subject
	Name string `json:"subject"`
	// Interval determines how often a heartbeat message will be sent
	Interval string `json:"interval"`
	// Headers are custom headers to add to the heartbeat message
	Headers map[string]string `json:"headers"`
}

type Advisory struct {
	// Subject is the NATS subject to publish messages too, a %s in the string will be replaced by the event type, %v with the value
	Subject string `json:"subject"`

	// Reliable indicates that the subject is a JetStream subject, so we should retry deliveries of advisories
	Reliable bool `json:"reliable"`
}

type ChoriaConnection struct {
	SeedFileName   string `json:"seed_file"`
	JWTFileName    string `json:"jwt_file"`
	CollectiveName string `json:"collective"`
}

func (c *ChoriaConnection) Collective() string {
	if c == nil {
		return ""
	}
	return c.CollectiveName
}
func (c *ChoriaConnection) SeedFile() string {
	if c == nil {
		return ""
	}
	return c.SeedFileName
}
func (c *ChoriaConnection) TokenFile() string {
	if c == nil {
		return ""
	}
	return c.JWTFileName
}

type TLS struct {
	CA   string `json:"ca"`
	Cert string `json:"cert"`
	Key  string `json:"key"`
}

func (t *TLS) CertificateAuthority() string {
	if t == nil {
		return ""
	}
	return t.CA
}
func (t *TLS) PublicCertificate() string {
	if t == nil {
		return ""
	}
	return t.Cert
}
func (t *TLS) PrivateKey() string {
	if t == nil {
		return ""
	}
	return t.Key
}

func (c *Config) Validate() (err error) {
	if c.ReplicatorName == "" {
		return fmt.Errorf("name is required")
	}

	if c.StateDirectory != "" {
		err = os.MkdirAll(c.StateDirectory, 0700)
		if err != nil {
			return fmt.Errorf("could not create state directory: %v", err)
		}
	}

	names := map[string]map[string]struct{}{}
	for _, s := range c.Streams {
		inspections := 0
		if s.InspectHeaderValue != "" {
			inspections++
		}
		if s.InspectJSONField != "" {
			inspections++
		}
		if s.InspectSubjectToken != 0 {
			inspections++
		}
		if inspections > 1 {
			return fmt.Errorf("only one inspection mode can be set per stream")
		}

		if s.Name == "" {
			s.Name = c.ReplicatorName
		}

		_, has := names[s.Stream]
		if !has {
			names[s.Stream] = map[string]struct{}{}
		}
		_, has = names[s.Stream][s.Name]
		if has {
			return fmt.Errorf("duplicate stream configuration name %s for stream %s", s.Name, s.Stream)
		}
		names[s.Stream][s.Name] = struct{}{}

		if s.Stream == "" {
			return fmt.Errorf("stream not specified")
		}

		if s.TargetStream == "" {
			s.TargetStream = s.Stream
		}

		if c.TLS == nil {
			c.TLS = &TLS{}
		}
		if s.TLS == nil {
			s.TLS = c.TLS
		}
		if s.SourceTLS == nil {
			s.SourceTLS = s.TLS
		}
		if s.TargetTLS == nil {
			s.TargetTLS = s.TLS
		}

		if c.ChoriaConn == nil {
			c.ChoriaConn = &ChoriaConnection{}
		}
		if s.ChoriaConn == nil {
			s.ChoriaConn = c.ChoriaConn
		}
		if s.SourceChoriaConn == nil {
			s.SourceChoriaConn = s.ChoriaConn
		}
		if s.TargetChoriaConn == nil {
			s.TargetChoriaConn = s.ChoriaConn
		}

		if c.StateDirectory != "" {
			s.StateFile = filepath.Join(c.StateDirectory, fmt.Sprintf("%s_%s.json", s.Stream, s.Name))
		}

		if s.StartDeltaString != "" {
			s.StartDelta, err = util.ParseDurationString(s.StartDeltaString)
			if err != nil {
				return fmt.Errorf("invalid start_delta: %v", err)
			}
		}

		if s.InspectDurationString != "" {
			s.InspectDuration, err = util.ParseDurationString(s.InspectDurationString)
			if err != nil {
				return fmt.Errorf("invalid inspect_duration: %v", err)
			}

			s.WarnDuration = s.InspectDuration / 2
		}

		if s.WarnDurationString != "" {
			s.WarnDuration, err = util.ParseDurationString(s.WarnDurationString)
			if err != nil {
				return fmt.Errorf("invalid warn_duration: %v", err)
			}
		}

		if s.MaxAgeString != "" {
			s.MaxAgeDuration, err = util.ParseDurationString(s.MaxAgeString)
			if err != nil {
				return fmt.Errorf("invalid max_age: %v", err)
			}
		}

		if s.TargetInitiated {
			if s.FilterSubject == "" {
				return fmt.Errorf("filter_subject is required with target_initiated")
			}
			if s.MaxAgeString != "" {
				return fmt.Errorf("max_age cannot be used with target_initiated")
			}
			if inspections > 0 {
				return fmt.Errorf("message inspection and sampling cannot be used with target_initiated")
			}
		}
	}

	if c.HeartBeat != nil {
		if c.HeartBeat.URL == "" {
			return fmt.Errorf("url is required with heartbeat")
		}

		_, err = util.ParseDurationString(c.HeartBeat.Interval)
		if err != nil {
			return fmt.Errorf("invalid interval: %v", err)
		}

		if len(c.HeartBeat.Subjects) == 0 {
			return fmt.Errorf("heartbeat requires at least one subject")
		}

		for _, subject := range c.HeartBeat.Subjects {
			if subject.Name == "" {
				return fmt.Errorf("name is required with subject")
			}

			_, err = util.ParseDurationString(subject.Interval)
			if err != nil {
				return fmt.Errorf("invalid interval: %v", err)
			}
		}
	}

	return nil
}

func Load(file string) (*Config, error) {
	c, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	j, err := yaml.YAMLToJSON(c)
	if err != nil {
		return nil, err
	}

	config := &Config{Profiling: true}
	err = json.Unmarshal(j, config)
	if err != nil {
		return nil, err
	}

	err = config.Validate()
	if err != nil {
		return nil, err
	}

	return config, nil
}
