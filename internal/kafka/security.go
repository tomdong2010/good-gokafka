package kafka

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/IBM/sarama"
	"github.com/xdg-go/scram"

	"github.com/tomdong2010/good-gokafka/internal/config"
)

// Supported SASL mechanisms.
const (
	SASLPlain       = "PLAIN"
	SASLScramSHA256 = "SCRAM-SHA-256"
	SASLScramSHA512 = "SCRAM-SHA-512"
)

// Security describes how to authenticate to and encrypt traffic with the
// brokers, as needed by managed services such as Confluent Cloud or Amazon MSK.
type Security struct {
	TLS         bool
	TLSCAFile   string // PEM bundle to trust instead of the system roots
	TLSCertFile string // client certificate for mutual TLS
	TLSKeyFile  string

	SASLMechanism string // empty disables SASL
	SASLUsername  string
	SASLPassword  string
}

// SecurityFromEnv reads KAFKA_TLS_ENABLED, KAFKA_TLS_CA_FILE,
// KAFKA_TLS_CERT_FILE, KAFKA_TLS_KEY_FILE, KAFKA_SASL_MECHANISM,
// KAFKA_SASL_USERNAME and KAFKA_SASL_PASSWORD.
func SecurityFromEnv() (Security, error) {
	tlsEnabled, err := config.Bool("KAFKA_TLS_ENABLED", false)
	if err != nil {
		return Security{}, err
	}
	return Security{
		TLS:           tlsEnabled,
		TLSCAFile:     config.String("KAFKA_TLS_CA_FILE", ""),
		TLSCertFile:   config.String("KAFKA_TLS_CERT_FILE", ""),
		TLSKeyFile:    config.String("KAFKA_TLS_KEY_FILE", ""),
		SASLMechanism: strings.ToUpper(config.String("KAFKA_SASL_MECHANISM", "")),
		SASLUsername:  config.String("KAFKA_SASL_USERNAME", ""),
		SASLPassword:  os.Getenv("KAFKA_SASL_PASSWORD"), // not trimmed: whitespace may be part of it
	}, nil
}

// Apply configures TLS and SASL on cfg.
func (s Security) Apply(cfg *sarama.Config) error {
	if s.TLS || s.TLSCAFile != "" || s.TLSCertFile != "" {
		tlsCfg, err := s.tlsConfig()
		if err != nil {
			return err
		}
		cfg.Net.TLS.Enable = true
		cfg.Net.TLS.Config = tlsCfg
	}

	if s.SASLMechanism == "" {
		return nil
	}
	if s.SASLUsername == "" || s.SASLPassword == "" {
		return errors.New("SASL requires a username and a password")
	}
	cfg.Net.SASL.Enable = true
	cfg.Net.SASL.User = s.SASLUsername
	cfg.Net.SASL.Password = s.SASLPassword
	cfg.Net.SASL.Handshake = true
	switch s.SASLMechanism {
	case SASLPlain:
		cfg.Net.SASL.Mechanism = sarama.SASLTypePlaintext
	case SASLScramSHA256:
		cfg.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA256
		cfg.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient { return &scramClient{hash: scram.SHA256} }
	case SASLScramSHA512:
		cfg.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
		cfg.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient { return &scramClient{hash: scram.SHA512} }
	default:
		return fmt.Errorf("unsupported SASL mechanism %q (want %s, %s or %s)",
			s.SASLMechanism, SASLPlain, SASLScramSHA256, SASLScramSHA512)
	}
	return nil
}

func (s Security) tlsConfig() (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if s.TLSCAFile != "" {
		pem, err := os.ReadFile(s.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("read TLS CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates found in %s", s.TLSCAFile)
		}
		cfg.RootCAs = pool
	}
	if (s.TLSCertFile == "") != (s.TLSKeyFile == "") {
		return nil, errors.New("TLS client certificate and key must be set together")
	}
	if s.TLSCertFile != "" {
		cert, err := tls.LoadX509KeyPair(s.TLSCertFile, s.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load TLS client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// scramClient adapts xdg-go/scram to sarama.SCRAMClient.
type scramClient struct {
	hash scram.HashGeneratorFcn
	conv *scram.ClientConversation
}

func (c *scramClient) Begin(user, password, authzID string) error {
	client, err := c.hash.NewClient(user, password, authzID)
	if err != nil {
		return err
	}
	c.conv = client.NewConversation()
	return nil
}

func (c *scramClient) Step(challenge string) (string, error) { return c.conv.Step(challenge) }
func (c *scramClient) Done() bool                            { return c.conv.Done() }
