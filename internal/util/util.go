// Copyright (c) 2022-2023, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	fw "github.com/choria-io/go-choria/choria"
	"github.com/choria-io/stream-replicator/backoff"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
)

type tlsConfig interface {
	CertificateAuthority() string
	PublicCertificate() string
	PrivateKey() string
}

type choriaConn interface {
	SeedFile() string
	TokenFile() string
	Collective() string
}

func ConnectNats(ctx context.Context, name string, srv string, tlsc tlsConfig, choria choriaConn, oldStyle bool, log *logrus.Entry) (nc *nats.Conn, err error) {
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
			if err == nil {
				log.Warnf("NATS client connection got disconnected: %v", nc.LastError())
				return
			}

			log.Warnf("NATS client connection got disconnected: %v", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Warnf("NATS client reconnected after a previous disconnection, connected to %s", nc.ConnectedUrlRedacted())
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			err := nc.LastError()
			if err == nil {
				log.Warnf("Connection closed due to unknown reason")
				return
			}

			log.Warnf("NATS client connection closed: %v", err)
		}),
		nats.ErrorHandler(func(nc *nats.Conn, sub *nats.Subscription, err error) {
			log.Errorf("NATS client on %s encountered an error: %s", nc.ConnectedUrlRedacted(), err)
		}),
	}

	if oldStyle {
		opts = append(opts, nats.UseOldRequestStyle())
	}

	var configuredTLS bool

	if tlsc != nil {
		if tlsc.PrivateKey() != "" && tlsc.PublicCertificate() != "" {
			log.Infof("Configuring Client Certificate for connection")
			opts = append(opts, nats.ClientCert(tlsc.PublicCertificate(), tlsc.PrivateKey()))
			configuredTLS = true
		}

		if tlsc.CertificateAuthority() != "" {
			log.Infof("Configuring Certificate Authority for connection")
			opts = append(opts, nats.RootCAs(tlsc.CertificateAuthority()))
		}
	}

	if choria != nil {
		if choria.SeedFile() != "" && choria.TokenFile() != "" {
			token, err := os.ReadFile(choria.TokenFile())
			if err != nil {
				return nil, fmt.Errorf("could not set up choria connection: %w", err)
			}

			inbox, jwth, sigh, err := fw.NatsConnectionHelpers(string(token), choria.Collective(), choria.SeedFile(), log)
			if err != nil {
				return nil, fmt.Errorf("could not set up choria connection: %w", err)
			}

			opts = append(opts, nats.Token(string(token)))
			opts = append(opts, nats.CustomInboxPrefix(inbox))
			opts = append(opts, nats.UserJWT(jwth, sigh))

			if !configuredTLS {
				// in jwt mode we specifically want to avoid any mTLS and specifically want to be
				// able to connect without having x509 CA shared and so forth. This is compatible with
				// the Choria connector design that does additional checks based on signed NONCE and
				// a ed25519 keypair. So we will only validate the connection if TLS is configured
				opts = append(opts, nats.Secure(&tls.Config{InsecureSkipVerify: true}))
			}
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
	return fw.ParseDuration(dstr)
}
