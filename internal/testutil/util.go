// Copyright (c) 2022, R.I. Pienaar and the Choria Project contributors
//
// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"io/ioutil"
	"os"
	"time"

	"github.com/nats-io/jsm.go"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"

	. "github.com/onsi/ginkgo/v2" //lint:ignore ST1001 silly bot
	. "github.com/onsi/gomega"    //lint:ignore ST1001 silly bot
)

type logger struct {
	*logrus.Entry
}

func (l *logger) Noticef(format string, v ...interface{}) {
	l.Infof(format, v...)
}

func WithJetStream(log *logrus.Entry, cb func(nc *nats.Conn, mgr *jsm.Manager)) {
	d, err := ioutil.TempDir("", "jstest")
	Expect(err).ToNot(HaveOccurred())
	defer os.RemoveAll(d)

	opts := &server.Options{
		JetStream: true,
		StoreDir:  d,
		Port:      -1,
		Host:      "localhost",
		LogFile:   "/dev/stdout",
	}

	s, err := server.NewServer(opts)
	Expect(err).ToNot(HaveOccurred())

	if log != nil {
		s.SetLogger(&logger{log}, true, true)
	}

	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		Fail("nats server did not start")
	}
	defer func() {
		s.Shutdown()
		s.WaitForShutdown()
	}()

	nc, err := nats.Connect(s.ClientURL(), nats.UseOldRequestStyle())
	Expect(err).ToNot(HaveOccurred())
	defer nc.Close()

	mgr, err := jsm.New(nc, jsm.WithTimeout(time.Second))
	Expect(err).ToNot(HaveOccurred())

	cb(nc, mgr)
}
