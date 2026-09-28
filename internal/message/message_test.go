package message

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func sample() *Message {
	return &Message{From: "Wuriyanto", Content: Content{Header: "This is Message 2", Body: "Hello Kafka"}}
}

func TestCodecRoundTrip(t *testing.T) {
	for _, c := range []Codec{ProtoCodec{}, JSONCodec{}} {
		t.Run(c.ContentType(), func(t *testing.T) {
			data, err := c.Encode(sample())
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			got, err := c.Decode(data)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if *got != *sample() {
				t.Fatalf("round trip = %+v, want %+v", got, sample())
			}
		})
	}
}

// The field numbers are the contract with records already in Kafka; this pins them.
func TestProtoWireFormat(t *testing.T) {
	want := []byte{0x0a, 0x01, 'a', 0x12, 0x06, 0x0a, 0x01, 'h', 0x12, 0x01, 'b'}
	got, err := ProtoCodec{}.Encode(&Message{From: "a", Content: Content{Header: "h", Body: "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("encoded = %x, want %x", got, want)
	}
}

func TestProtoDecodeWithoutContent(t *testing.T) {
	got, err := ProtoCodec{}.Decode([]byte{0x0a, 0x01, 'a'})
	if err != nil {
		t.Fatal(err)
	}
	if got.From != "a" || got.Content != (Content{}) {
		t.Fatalf("decoded = %+v", got)
	}
}

func TestDecodeGarbage(t *testing.T) {
	for _, c := range []Codec{ProtoCodec{}, JSONCodec{}} {
		if _, err := c.Decode([]byte{0xff, 0xff, 0xff}); err == nil {
			t.Errorf("%s: expected error", c.ContentType())
		}
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Message)
		ok     bool
	}{
		{"valid", func(*Message) {}, true},
		{"missing from", func(m *Message) { m.From = "  " }, false},
		{"missing body", func(m *Message) { m.Content.Body = "" }, false},
		{"header optional", func(m *Message) { m.Content.Header = "" }, true},
		{"from too long", func(m *Message) { m.From = strings.Repeat("x", MaxFromLen+1) }, false},
		{"header too long", func(m *Message) { m.Content.Header = strings.Repeat("x", MaxHeaderLen+1) }, false},
		{"body too long", func(m *Message) { m.Content.Body = strings.Repeat("x", MaxBodyLen+1) }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := sample()
			tt.mutate(m)
			err := m.Validate()
			if tt.ok != (err == nil) {
				t.Fatalf("Validate() = %v, want ok=%v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Fatalf("error %v does not wrap ErrInvalid", err)
			}
		})
	}
}

func TestNewCodec(t *testing.T) {
	for name, want := range map[string]Codec{"": ProtoCodec{}, "proto": ProtoCodec{}, " JSON ": JSONCodec{}} {
		got, err := NewCodec(name)
		if err != nil || got != want {
			t.Errorf("NewCodec(%q) = %T, %v", name, got, err)
		}
	}
	if _, err := NewCodec("avro"); err == nil {
		t.Error("NewCodec(avro): expected error")
	}
}

func TestCodecForContentType(t *testing.T) {
	if got := CodecForContentType("application/json", ProtoCodec{}); got != (JSONCodec{}) {
		t.Errorf("json header -> %T", got)
	}
	if got := CodecForContentType("application/x-protobuf", JSONCodec{}); got != (ProtoCodec{}) {
		t.Errorf("proto header -> %T", got)
	}
	if got := CodecForContentType("", JSONCodec{}); got != (JSONCodec{}) {
		t.Errorf("missing header -> %T, want default", got)
	}
}
