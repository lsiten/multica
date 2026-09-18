package native

import (
	"bytes"
	"errors"
	"testing"
)

func TestReadMessageRejectsOversizedFrame(t *testing.T) {
	// Given
	input := bytes.NewReader([]byte{0, 1, 0, 1})
	// When
	var request Request
	err := ReadMessage(input, &request)
	// Then
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("got %v", err)
	}
}

func TestReadMessageRejectsUnknownFields(t *testing.T) {
	// Given
	input := bytes.NewBuffer(nil)
	body := []byte(`{"operation":"list","unknown":true}`)
	header := []byte{0, 0, 0, byte(len(body))}
	input.Write(header)
	input.Write(body)
	// When
	var request Request
	err := ReadMessage(input, &request)
	// Then
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("got %v", err)
	}
}

func TestProtocolRoundTrip(t *testing.T) {
	// Given
	input := bytes.NewBuffer(nil)
	want := Request{Version: 1, Build: "test", ID: "request", Operation: "list"}
	// When
	if err := WriteMessage(input, want); err != nil {
		t.Fatal(err)
	}
	var got Request
	if err := ReadMessage(input, &got); err != nil {
		t.Fatal(err)
	}
	// Then
	if got.ID != want.ID || got.Build != want.Build || got.Operation != want.Operation || got.Version != want.Version {
		t.Fatalf("got %+v", got)
	}
}
