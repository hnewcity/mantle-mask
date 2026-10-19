package gateway

import (
	"bytes"
	"errors"
	"testing"

	"github.com/hnewcity/mantle-mask/internal/usage"
)

func TestObserverEveryChunkBoundary(t *testing.T) {
	stream := []byte(": heartbeat\r\n\r\nevent: response\r\ndata: {\"type\":\"response.created\",\r\ndata: \"response\":{\"id\":\"r1\"}}\r\n\r\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"usage\":{\"input_tokens\":8,\"output_tokens\":4}}}\n\ndata: [DONE]\n\n")
	for split := 0; split <= len(stream); split++ {
		responses, counts := 0, 0
		o := observer{onResponse: func(id string) error {
			responses++
			if id != "r1" {
				t.Fatal(id)
			}
			return nil
		}, onUsage: func(c usage.Counters) error { counts++; return nil }}
		for _, chunk := range [][]byte{stream[:split], stream[split:]} {
			if err := o.write(chunk); err != nil {
				t.Fatal(err)
			}
		}
		if responses != 1 || counts != 1 || !o.complete || !o.sawDone || o.input != 8 || o.output != 4 {
			t.Fatalf("split %d: %+v callbacks %d/%d", split, o, responses, counts)
		}
	}
	o := observer{}
	for _, b := range stream {
		if err := o.write([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	if !o.complete || o.input != 8 {
		t.Fatal("bytewise chunks failed")
	}
}

func TestObserverResponseBindingRetriesAndUsage(t *testing.T) {
	attempts := 0
	o := observer{onResponse: func(string) error {
		attempts++
		if attempts == 1 {
			return errors.New("disk full")
		}
		return nil
	}}
	event := []byte(`{"object":"response","id":"r1","usage":{"input_tokens":8,"output_tokens":4}}`)
	if o.json(event) == nil || o.boundResponse != "" {
		t.Fatal("failed binding cached")
	}
	if err := o.json(event); err != nil {
		t.Fatal(err)
	}
	if err := o.json([]byte(`{"type":"response.failed","response":{"id":"r1","usage":{"input_tokens":8,"output_tokens":9}}}`)); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || o.output != 9 || !o.failed || !o.complete {
		t.Fatal("dedup skipped usage or status")
	}
	if err := o.json([]byte(`{"object":"response","id":"r2"}`)); err != nil || attempts != 3 {
		t.Fatal("different response not persisted")
	}
}

func TestObserverLimitsAndChatCompletion(t *testing.T) {
	for _, fragmented := range []bool{false, true} {
		o := observer{}
		line := bytes.Repeat([]byte("x"), 1<<20)
		if fragmented {
			for i := 0; i < len(line); i += 1024 {
				if err := o.write(line[i : i+1024]); err != nil {
					t.Fatal(err)
				}
			}
		} else if err := o.write(line); err != nil {
			t.Fatal(err)
		}
		if err := o.write([]byte("x")); err == nil {
			t.Fatal("oversized line accepted")
		}
	}
	o := observer{}
	line := append([]byte("data: "), bytes.Repeat([]byte("x"), 600000)...)
	line = append(line, '\n')
	if err := o.write(line); err != nil {
		t.Fatal(err)
	}
	if err := o.write(line); err == nil {
		t.Fatal("oversized multiline event accepted")
	}
	o = observer{expectedChoices: 2}
	for _, event := range []string{`{"object":"chat.completion.chunk","choices":[{"index":0,"finish_reason":null}]}`, `{"object":"chat.completion.chunk","choices":[{"index":0,"finish_reason":"stop"}]}`} {
		if err := o.write([]byte("data: " + event + "\n\n")); err != nil {
			t.Fatal(err)
		}
		if o.complete {
			t.Fatal("premature completion")
		}
	}
	if err := o.write([]byte("data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":1,\"finish_reason\":\"stop\"}]}\n\n")); err != nil || !o.complete {
		t.Fatal("missing terminal choices")
	}
}
