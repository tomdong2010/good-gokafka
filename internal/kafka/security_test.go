package kafka

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/xdg-go/scram"
)

func TestApplyNothing(t *testing.T) {
	cfg := ProducerConfig("test")
	if err := (Security{}).Apply(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Net.TLS.Enable || cfg.Net.SASL.Enable {
		t.Fatal("TLS and SASL should stay disabled")
	}
}

func TestApplySASL(t *testing.T) {
	for _, mech := range []string{SASLPlain, SASLScramSHA256, SASLScramSHA512} {
		t.Run(mech, func(t *testing.T) {
			cfg := ConsumerConfig("test")
			sec := Security{SASLMechanism: mech, SASLUsername: "alice", SASLPassword: "secret"}
			if err := sec.Apply(cfg); err != nil {
				t.Fatal(err)
			}
			if !cfg.Net.SASL.Enable || cfg.Net.SASL.User != "alice" || string(cfg.Net.SASL.Mechanism) != mech {
				t.Fatalf("SASL config = %+v", cfg.Net.SASL)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestApplySASLErrors(t *testing.T) {
	for name, sec := range map[string]Security{
		"unknown mechanism": {SASLMechanism: "GSSAPI", SASLUsername: "a", SASLPassword: "b"},
		"missing password":  {SASLMechanism: SASLPlain, SASLUsername: "a"},
	} {
		if err := sec.Apply(ProducerConfig("test")); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

// Runs the client produced by the config through a full SCRAM exchange with
// the reference server implementation.
func TestScramConversation(t *testing.T) {
	for mech, hash := range map[string]scram.HashGeneratorFcn{SASLScramSHA256: scram.SHA256, SASLScramSHA512: scram.SHA512} {
		t.Run(mech, func(t *testing.T) {
			stored := storedCredentials(t, hash, "alice", "secret")
			for password, wantOK := range map[string]bool{"secret": true, "wrong": false} {
				cfg := ProducerConfig("test")
				if err := (Security{SASLMechanism: mech, SASLUsername: "alice", SASLPassword: password}).Apply(cfg); err != nil {
					t.Fatal(err)
				}
				ok := converse(t, hash, stored, cfg.Net.SASL.SCRAMClientGeneratorFunc(), password)
				if ok != wantOK {
					t.Errorf("password %q: authenticated = %v", password, ok)
				}
			}
		})
	}
}

func storedCredentials(t *testing.T, hash scram.HashGeneratorFcn, user, password string) scram.StoredCredentials {
	t.Helper()
	client, err := hash.NewClient(user, password, "")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := client.GetStoredCredentialsWithError(scram.KeyFactors{Salt: "salt-for-test", Iters: 4096})
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func converse(t *testing.T, hash scram.HashGeneratorFcn, stored scram.StoredCredentials, client sarama.SCRAMClient, password string) bool {
	t.Helper()
	server, err := hash.NewServer(func(string) (scram.StoredCredentials, error) { return stored, nil })
	if err != nil {
		t.Fatal(err)
	}
	conv := server.NewConversation()
	if err := client.Begin("alice", password, ""); err != nil {
		t.Fatal(err)
	}
	msg, err := client.Step("")
	for err == nil && !client.Done() {
		var challenge string
		if challenge, err = conv.Step(msg); err != nil {
			break
		}
		msg, err = client.Step(challenge)
	}
	return err == nil && conv.Valid()
}

func TestApplyTLS(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeSelfSigned(t, dir)

	cfg := ProducerConfig("test")
	sec := Security{TLS: true, TLSCAFile: certFile, TLSCertFile: certFile, TLSKeyFile: keyFile}
	if err := sec.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Net.TLS.Enable || cfg.Net.TLS.Config.RootCAs == nil || len(cfg.Net.TLS.Config.Certificates) != 1 {
		t.Fatalf("TLS config = %+v", cfg.Net.TLS)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	empty := filepath.Join(dir, "empty.pem")
	if err := os.WriteFile(empty, []byte("no certificates here"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]Security{
		"missing CA file":   {TLS: true, TLSCAFile: filepath.Join(dir, "missing.pem")},
		"CA without certs":  {TLS: true, TLSCAFile: empty},
		"cert without key":  {TLS: true, TLSCertFile: certFile},
		"key is not a cert": {TLS: true, TLSCertFile: keyFile, TLSKeyFile: keyFile},
	} {
		if err := bad.Apply(ProducerConfig("test")); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestSecurityFromEnv(t *testing.T) {
	t.Setenv("KAFKA_TLS_ENABLED", "true")
	t.Setenv("KAFKA_SASL_MECHANISM", "scram-sha-512")
	t.Setenv("KAFKA_SASL_USERNAME", "alice")
	t.Setenv("KAFKA_SASL_PASSWORD", " secret ")
	sec, err := SecurityFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	want := Security{TLS: true, SASLMechanism: SASLScramSHA512, SASLUsername: "alice", SASLPassword: " secret "}
	if sec != want {
		t.Fatalf("SecurityFromEnv() = %+v, want %+v", sec, want)
	}
}

func writeSelfSigned(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	write := func(path, typ string, der []byte) {
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(certFile, "CERTIFICATE", der)
	write(keyFile, "PRIVATE KEY", keyDER)
	return certFile, keyFile
}
