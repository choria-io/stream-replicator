package util

import (
	"testing"
	"time"

	server "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
)

// startTestServer starts an in-process NATS server and returns a stop func.
func startTestServer(t *testing.T) (url string, stop func()) {
	t.Helper()

	opts := &server.Options{
		Port:   -1,
		NoLog:  true,
		NoSigs: true,
	}
	s, err := server.NewServer(opts)
	if err != nil {
		t.Fatalf("failed to create nats server: %v", err)
	}
	go s.Start()
	if !s.ReadyForConnections(5 * time.Second) {
		s.Shutdown()
		t.Fatalf("nats server not ready in time")
	}
	return s.ClientURL(), func() { s.Shutdown() }
}

type noTLS struct{}

func (noTLS) CertificateAuthority() string { return "" }
func (noTLS) PublicCertificate() string    { return "" }
func (noTLS) PrivateKey() string           { return "" }

// minimal logger entry for tests
func testLogger() *logrus.Entry {
	l := logrus.New()
	l.SetLevel(logrus.PanicLevel)
	return logrus.NewEntry(l)
}

func TestUtil(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Util Options Suite")
}

var _ = Describe("buildNatsOptions", func() {
	var (
		nurl string
		stop func()
	)

	BeforeEach(func() {
		nurl, stop = startTestServer(&testing.T{})
	})

	AfterEach(func() {
		if stop != nil {
			stop()
		}
	})

	It("enables TLSHandshakeFirst when tls_handshake_first=true and tls config is set", func() {
		srv := nurl + "?tls_handshake_first=true"
		opts, _, err := buildNatsOptions("test", srv, noTLS{}, nil, false, nil, testLogger())
		Expect(err).ToNot(HaveOccurred())
		var nopts nats.Options
		for _, o := range opts {
			err := o(&nopts)
			Expect(err).ToNot(HaveOccurred())
		}
		Expect(nopts.TLSHandshakeFirst).To(BeTrue())
	})

	It("disable TLSHandshakeFirst when tls_handshake_first=true and tls config is not set", func() {
		srv := nurl + "?tls_handshake_first=true"
		opts, _, err := buildNatsOptions("test", srv, nil, nil, false, nil, testLogger())
		Expect(err).ToNot(HaveOccurred())
		var nopts nats.Options
		for _, o := range opts {
			err := o(&nopts)
			Expect(err).ToNot(HaveOccurred())
		}
		Expect(nopts.TLSHandshakeFirst).To(BeFalse())
	})

	It("defaults TLSHandshakeFirst to false when flag absent", func() {
		srv := nurl
		opts, _, err := buildNatsOptions("test", srv, noTLS{}, nil, false, nil, testLogger())
		Expect(err).ToNot(HaveOccurred())
		var nopts nats.Options
		for _, o := range opts {
			err := o(&nopts)
			Expect(err).ToNot(HaveOccurred())
		}
		Expect(nopts.TLSHandshakeFirst).To(BeFalse())
	})

	It("sets TLS InsecureSkipVerify when insecure=true", func() {
		srv := nurl + "?insecure=true"
		opts, _, err := buildNatsOptions("test", srv, noTLS{}, nil, false, nil, testLogger())
		Expect(err).ToNot(HaveOccurred())
		var nopts nats.Options
		for _, o := range opts {
			err := o(&nopts)
			Expect(err).ToNot(HaveOccurred())
		}
		Expect(nopts.Secure).To(BeTrue())
		Expect(nopts.TLSConfig).ToNot(BeNil())
		Expect(nopts.TLSConfig.InsecureSkipVerify).To(BeTrue())
	})

	It("sets connect timeout from query param", func() {
		srv := nurl + "?connect_timeout=1500ms"
		opts, _, err := buildNatsOptions("test", srv, noTLS{}, nil, false, nil, testLogger())
		Expect(err).ToNot(HaveOccurred())
		var nopts nats.Options
		for _, o := range opts {
			err := o(&nopts)
			Expect(err).ToNot(HaveOccurred())
		}
		Expect(nopts.Timeout).To(Equal(1500 * time.Millisecond))
	})
})
