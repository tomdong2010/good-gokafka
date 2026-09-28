// Package message defines the payload exchanged between producer and consumer
// and the codecs used to put it on the wire.
package message

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/tomdong2010/good-gokafka/internal/message/pb"
)

// Limits applied by Validate so a single request cannot push oversized records to Kafka.
const (
	MaxFromLen   = 256
	MaxHeaderLen = 1024
	MaxBodyLen   = 64 * 1024
)

// ContentTypeHeader is the Kafka record header carrying the payload encoding.
const ContentTypeHeader = "content-type"

// ErrInvalid is wrapped by every validation error, so callers can map it to a 4xx response.
var ErrInvalid = errors.New("invalid message")

// Content is the message content.
type Content struct {
	Header string `json:"header"`
	Body   string `json:"body"`
}

// Message is the domain representation of a message.
type Message struct {
	From    string  `json:"from"`
	Content Content `json:"content"`
}

// Validate checks required fields and size limits.
func (m *Message) Validate() error {
	switch {
	case strings.TrimSpace(m.From) == "":
		return fmt.Errorf("%w: from is required", ErrInvalid)
	case strings.TrimSpace(m.Content.Body) == "":
		return fmt.Errorf("%w: content.body is required", ErrInvalid)
	case len(m.From) > MaxFromLen:
		return fmt.Errorf("%w: from exceeds %d bytes", ErrInvalid, MaxFromLen)
	case len(m.Content.Header) > MaxHeaderLen:
		return fmt.Errorf("%w: content.header exceeds %d bytes", ErrInvalid, MaxHeaderLen)
	case len(m.Content.Body) > MaxBodyLen:
		return fmt.Errorf("%w: content.body exceeds %d bytes", ErrInvalid, MaxBodyLen)
	}
	return nil
}

// Codec converts a Message to and from its wire representation.
type Codec interface {
	Encode(*Message) ([]byte, error)
	Decode([]byte) (*Message, error)
	// ContentType is sent as a Kafka record header so consumers can detect the format.
	ContentType() string
}

// Supported codec names, used by NewCodec.
const (
	FormatProto = "proto"
	FormatJSON  = "json"
)

// NewCodec returns the codec registered under name ("proto" or "json").
func NewCodec(name string) (Codec, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", FormatProto:
		return ProtoCodec{}, nil
	case FormatJSON:
		return JSONCodec{}, nil
	default:
		return nil, fmt.Errorf("unknown message format %q (want %q or %q)", name, FormatProto, FormatJSON)
	}
}

// ProtoCodec encodes messages with protocol buffers.
type ProtoCodec struct{}

// Encode implements Codec.
func (ProtoCodec) Encode(m *Message) ([]byte, error) {
	return proto.Marshal(&pb.Message{
		From: m.From,
		Content: &pb.Content{
			Header: m.Content.Header,
			Body:   m.Content.Body,
		},
	})
}

// Decode implements Codec. A record without content decodes to an empty Content
// instead of panicking on the nil pointer.
func (ProtoCodec) Decode(data []byte) (*Message, error) {
	var p pb.Message
	if err := proto.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("decode proto: %w", err)
	}
	return &Message{
		From: p.GetFrom(),
		Content: Content{
			Header: p.GetContent().GetHeader(),
			Body:   p.GetContent().GetBody(),
		},
	}, nil
}

// ContentType implements Codec.
func (ProtoCodec) ContentType() string { return "application/x-protobuf" }

// JSONCodec encodes messages as JSON.
type JSONCodec struct{}

// Encode implements Codec.
func (JSONCodec) Encode(m *Message) ([]byte, error) { return json.Marshal(m) }

// Decode implements Codec.
func (JSONCodec) Decode(data []byte) (*Message, error) {
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}
	return &m, nil
}

// ContentType implements Codec.
func (JSONCodec) ContentType() string { return "application/json" }

// CodecForContentType picks the codec matching a record's content-type header,
// falling back to def when the header is missing or unknown.
func CodecForContentType(contentType string, def Codec) Codec {
	switch contentType {
	case ProtoCodec{}.ContentType():
		return ProtoCodec{}
	case JSONCodec{}.ContentType():
		return JSONCodec{}
	default:
		return def
	}
}
