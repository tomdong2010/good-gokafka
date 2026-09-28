package config

import (
	"reflect"
	"testing"
	"time"
)

func TestBrokers(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", " a:9092, ,b:9092 ")
	t.Setenv("LEGACY", "c:9092")
	got, err := Brokers("LEGACY")
	if err != nil || !reflect.DeepEqual(got, []string{"a:9092", "b:9092"}) {
		t.Fatalf("Brokers() = %v, %v", got, err)
	}
}

func TestBrokersLegacyFallback(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", "")
	t.Setenv("LEGACY", "c:9092")
	got, err := Brokers("MISSING", "LEGACY")
	if err != nil || !reflect.DeepEqual(got, []string{"c:9092"}) {
		t.Fatalf("Brokers() = %v, %v", got, err)
	}
}

func TestBrokersMissing(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", "")
	if _, err := Brokers(); err == nil {
		t.Fatal("expected error")
	}
}

func TestRequiredAndString(t *testing.T) {
	t.Setenv("SET", "v")
	t.Setenv("BLANK", " ")
	if v, err := Required("SET"); err != nil || v != "v" {
		t.Errorf("Required(SET) = %q, %v", v, err)
	}
	if _, err := Required("BLANK"); err == nil {
		t.Error("Required(BLANK): expected error")
	}
	if v := String("BLANK", "def"); v != "def" {
		t.Errorf("String(BLANK) = %q", v)
	}
}

func TestDuration(t *testing.T) {
	t.Setenv("D", "3s")
	if d, err := Duration("D", time.Second); err != nil || d != 3*time.Second {
		t.Errorf("Duration(D) = %v, %v", d, err)
	}
	if d, err := Duration("UNSET_DURATION", time.Second); err != nil || d != time.Second {
		t.Errorf("Duration(unset) = %v, %v", d, err)
	}
	t.Setenv("D", "soon")
	if _, err := Duration("D", time.Second); err == nil {
		t.Error("Duration(invalid): expected error")
	}
}
