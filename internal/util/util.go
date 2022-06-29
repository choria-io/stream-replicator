// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/choria-io/stream-replicator/backoff"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
)

type TLSConfig interface {
	CertificateAuthority() string
	PublicCertificate() string
	PrivateKey() string
}

func ConnectNats(ctx context.Context, name string, srv string, tls TLSConfig, oldStyle bool, log *logrus.Entry) (nc *nats.Conn, err error) {
	opts := []nats.Option{
		nats.MaxReconnects(-1),
		nats.NoEcho(),
		nats.Name(fmt.Sprintf("Choria Stream Replicator: %s", name)),
		nats.CustomReconnectDelay(func(n int) time.Duration {
			d := backoff.TwoMinutesSlowStart.Duration(n)
			log.Infof("Sleeping %v till the next reconnection attempt", d)
			return d
		}),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			if err != nil {
				log.Warnf("NATS client connection got disconnected: %v", nc.LastError())
			}
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Warnf("NATS client reconnected after a previous disconnection, connected to %s", nc.ConnectedUrlRedacted())
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			err := nc.LastError()
			if err != nil {
				log.Warnf("NATS client connection closed: %v", err)
			}
		}),
		nats.ErrorHandler(func(nc *nats.Conn, sub *nats.Subscription, err error) {
			log.Errorf("NATS client on %s encountered an error: %s", nc.ConnectedUrlRedacted(), err)
		}),
	}

	if oldStyle {
		opts = append(opts, nats.UseOldRequestStyle())
	}

	if tls != nil {
		if tls.PrivateKey() != "" && tls.PublicCertificate() != "" {
			log.Infof("Configuring Client Certificate for connection")
			opts = append(opts, nats.ClientCert(tls.PublicCertificate(), tls.PrivateKey()))
		}

		if tls.CertificateAuthority() != "" {
			log.Infof("Configuring Certificate Authority for connection")
			opts = append(opts, nats.RootCAs(tls.CertificateAuthority()))
		}
	}

	var urls []string
	for _, u := range strings.Split(srv, ",") {
		url, err := url.Parse(strings.TrimSpace(u))
		if err != nil {
			return nil, err
		}

		urls = append(urls, url.Redacted())
	}

	err = backoff.TwoMinutesSlowStart.For(ctx, func(try int) error {
		log.Infof("Attempting to connect to %s on try %d", strings.Join(urls, ", "), try)

		nc, err = nats.Connect(srv, opts...)
		if err != nil {
			log.Errorf("Initial connection failed: %v", err)
			return err
		}
		log.Infof("Connected to %s", nc.ConnectedUrlRedacted())

		return nil
	})
	if err != nil {
		return nil, err
	}

	return nc, err
}

func ParseDurationString(dstr string) (dur time.Duration, err error) {
	dstr = strings.TrimSpace(dstr)

	if len(dstr) <= 0 {
		return dur, nil
	}

	ls := len(dstr)
	di := ls - 1
	unit := dstr[di:]

	switch unit {
	case "w", "W":
		val, err := strconv.ParseFloat(dstr[:di], 32)
		if err != nil {
			return dur, err
		}

		dur = time.Duration(val*7*24) * time.Hour

	case "d", "D":
		val, err := strconv.ParseFloat(dstr[:di], 32)
		if err != nil {
			return dur, err
		}

		dur = time.Duration(val*24) * time.Hour
	case "M":
		val, err := strconv.ParseFloat(dstr[:di], 32)
		if err != nil {
			return dur, err
		}

		dur = time.Duration(val*24*30) * time.Hour
	case "Y", "y":
		val, err := strconv.ParseFloat(dstr[:di], 32)
		if err != nil {
			return dur, err
		}

		dur = time.Duration(val*24*365) * time.Hour
	case "s", "S", "m", "h", "H":
		dur, err = time.ParseDuration(dstr)
		if err != nil {
			return dur, err
		}

	default:
		return dur, fmt.Errorf("invalid time unit %s", unit)
	}

	return dur, nil
}
