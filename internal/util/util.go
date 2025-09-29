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
	"strconv"
	"strings"
	"time"

	"github.com/choria-io/stream-replicator/backoff"
	"github.com/choria-io/tokens"
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

func buildNatsOptions(name string, srv string, tlsc tlsConfig, choria choriaConn, oldStyle bool, conn nats.InProcessConnProvider, log *logrus.Entry) (opts []nats.Option, urls []string, err error) {
	opts = []nats.Option{
		nats.MaxReconnects(-1),
		nats.IgnoreAuthErrorAbort(),
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
				return nil, nil, fmt.Errorf("could not set up choria connection: %w", err)
			}

			inbox, jwth, sigh, err := tokens.NatsConnectionHelpers(string(token), choria.Collective(), choria.SeedFile(), log)
			if err != nil {
				return nil, nil, fmt.Errorf("could not set up choria connection: %w", err)
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

	var hasCreds bool
	for _, u := range strings.Split(srv, ",") {
		parsed, err := url.Parse(strings.TrimSpace(u))
		if err != nil {
			return nil, nil, err
		}

		if !hasCreds && parsed.RawQuery != "" {
			queries, err := url.ParseQuery(parsed.RawQuery)
			if err != nil {
				return nil, nil, err
			}

			if queries.Has("credentials") {
				creds := queries.Get("credentials")
				log.Debugf("Using %q as credentials file", creds)
				opts = append(opts, nats.UserCredentials(creds))
				hasCreds = true
			}

			if queries.Has("tls_handshake_first") {
				tlsHandshakeFirst, _ := strconv.ParseBool(queries.Get("tls_handshake_first"))
				if tlsHandshakeFirst && tlsc != nil {
					log.Debugf("TLS Handshake before INFO protocol")
					opts = append(opts, nats.TLSHandshakeFirst())
				}
			}

			if queries.Has("insecure") {
				insecure, _ := strconv.ParseBool(queries.Get("insecure"))
				if insecure {
					log.Debugf("Skipping TLS verification")
					opts = append(opts, nats.Secure(&tls.Config{InsecureSkipVerify: true}))
				}
			}

			if queries.Has("connect_timeout") {
				connect_timeout, err := ParseDurationString(queries.Get("connect_timeout"))
				if err != nil {
					log.Debugf("invalid connect_timeout: %v", err)
				} else {
					log.Debugf("Setting NATS connect timeout to %q", connect_timeout)
					opts = append(opts, nats.Timeout(connect_timeout))
				}
			}

			if queries.Has("jwt") && queries.Has("nkey") {
				jwt := queries.Get("jwt")
				nkey := queries.Get("nkey")
				log.Debugf("Using %q as jwt file and %q as nkey file", jwt, nkey)
				opts = append(opts, nats.UserCredentials(jwt, nkey))
				hasCreds = true
			}
			parsed.RawQuery = ""
		}

		urls = append(urls, parsed.Redacted())
	}

	if conn != nil {
		opts = append(opts, nats.InProcessServer(conn))
	}

	return opts, urls, nil
}

func ConnectNats(ctx context.Context, name string, srv string, tlsc tlsConfig, choria choriaConn, oldStyle bool, conn nats.InProcessConnProvider, log *logrus.Entry) (nc *nats.Conn, err error) {
	opts, urls, err := buildNatsOptions(name, srv, tlsc, choria, oldStyle, conn, log)
	if err != nil {
		return nil, err
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
